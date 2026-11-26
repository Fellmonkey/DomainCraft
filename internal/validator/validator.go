// Package validator checks the parsed schema for logical consistency.
package validator

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"github.com/DomainCraft/DomainCraft/pkg/textutil"
)

// ValidationError describes a logical validation error.
type ValidationError struct {
	Entity  string
	Field   string
	Message string
	Warning bool // non-fatal hint, not a hard error
}

// Error formats the entity, optional field and message of one validation error.
func (e ValidationError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("entity %q field %q: %s", e.Entity, e.Field, e.Message)
	}
	return fmt.Sprintf("entity %q: %s", e.Entity, e.Message)
}

// invalid builds a non-warning validation error for an entity and field.
func invalid(entity, field, message string) ValidationError {
	return ValidationError{Entity: entity, Field: field, Message: message}
}

// warn builds a warning validation error for an entity and field.
func warn(entity, field, message string) ValidationError {
	return ValidationError{Entity: entity, Field: field, Message: message, Warning: true}
}

// invalidf builds a non-warning validation error with a formatted message.
func invalidf(entity, field, format string, args ...any) ValidationError {
	return invalid(entity, field, fmt.Sprintf(format, args...))
}

// warnf builds a warning validation error with a formatted message.
func warnf(entity, field, format string, args ...any) ValidationError {
	return warn(entity, field, fmt.Sprintf(format, args...))
}

// Validator checks ParsedSchema for logical consistency.
type Validator struct {
	schema *parser.ParsedSchema
}

// New creates a Validator for the parsed schema.
func New(schema *parser.ParsedSchema) *Validator {
	return &Validator{schema: schema}
}

// boolValues are valid boolean default values.
var boolValues = map[string]bool{"true": true, "false": true}

// Validate returns all logical errors and warnings in deterministic order.
func (v *Validator) Validate() []ValidationError {
	if v == nil || v.schema == nil {
		return []ValidationError{{Entity: "<schema>", Message: "schema is nil"}}
	}

	var errs []ValidationError

	errs = append(errs, v.validateProject()...)
	errs = append(errs, v.validateEnums()...)

	// Entity vs enum name collision.
	if v.schema.Enums != nil {
		for _, entityName := range v.schema.EntityOrder {
			if _, isEnum := v.schema.Enums[entityName]; isEnum {
				errs = append(errs, warnf(entityName, "",
					"entity name %q collides with an enum of the same name; templates may produce ambiguous output", entityName))
			}
		}
	}

	for _, entityName := range v.schema.EntityOrder {
		entity := v.schema.Entities[entityName]
		if entity == nil {
			continue
		}
		errs = append(errs, v.validateEntity(entityName, entity)...)
	}

	errs = append(errs, v.validateCircularRelations()...)

	slices.SortStableFunc(errs, func(a, b ValidationError) int {
		if a.Warning != b.Warning {
			if a.Warning {
				return 1 // errors first
			}
			return -1
		}
		if c := cmp.Compare(a.Entity, b.Entity); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Field, b.Field); c != 0 {
			return c
		}
		return cmp.Compare(a.Message, b.Message)
	})

	return errs
}

// --- project-level ---

func (v *Validator) validateProject() []ValidationError {
	var errs []ValidationError
	p := v.schema.Project

	if strings.TrimSpace(p.Name) == "" {
		errs = append(errs, invalid("<schema>", "", "project name must not be empty"))
	}
	if len(v.schema.Entities) == 0 {
		errs = append(errs, warn("<schema>", "", "no entities defined"))
	}
	if v.schema.Database != "" && !specmeta.IsDatabase(v.schema.Database) {
		errs = append(errs, invalidf("<schema>", "",
			"unknown database %q; allowed: %s", v.schema.Database, strings.Join(specmeta.Databases, ", ")))
	}
	if v.schema.Auth != nil {
		errs = append(errs, v.validateAuth()...)
	}
	if v.schema.APIStyle != "" && !specmeta.IsAPIStyle(v.schema.APIStyle) {
		errs = append(errs, invalidf("<schema>", "",
			"unknown api_style %q; allowed: %s", v.schema.APIStyle, strings.Join(specmeta.APIStyles, ", ")))
	}
	if p.MultiTenancy != nil && p.MultiTenancy.Enabled && p.MultiTenancy.Mode != "" &&
		!specmeta.IsMultiTenancyMode(p.MultiTenancy.Mode) {
		errs = append(errs, invalidf("<schema>", "",
			"unknown multi_tenancy.mode %q; allowed: %s", p.MultiTenancy.Mode, strings.Join(specmeta.MultiTenancyModes, ", ")))
	}
	if p.CORS != nil && p.CORS.Enabled && len(p.CORS.Origins) == 0 {
		errs = append(errs, warn("<schema>", "", "cors.enabled is true but no origins specified"))
	}
	if p.Deploy != nil && (p.Deploy.Port < 0 || p.Deploy.Port > 65535) {
		errs = append(errs, invalidf("<schema>", "",
			"deploy.port must be between 1 and 65535, got %d", p.Deploy.Port))
	}
	if p.Versioning != nil && p.Versioning.Enabled && p.Versioning.DefaultVersion == "" {
		errs = append(errs, warn("<schema>", "", "versioning.enabled is true but default_version is empty"))
	}

	errs = append(errs, validateCacheConfig(p)...)
	errs = append(errs, validateRateLimitConfig(p)...)
	errs = append(errs, validatePaginationConfig(p)...)
	errs = append(errs, validateInfrastructureConfig(p)...)
	return errs
}

// validateCacheConfig keeps the three cache rules together: they describe one
// policy (enabled, provider and connection string must agree).
func validateCacheConfig(p parser.ProjectConfig) []ValidationError {
	var errs []ValidationError
	c := p.Cache
	if c == nil {
		return nil
	}
	if c.Enabled {
		if c.Provider != "" && !specmeta.IsCacheProvider(c.Provider) {
			errs = append(errs, invalidf("<schema>", "",
				"unknown cache.provider %q; allowed: %s", c.Provider, strings.Join(specmeta.CacheProviders, ", ")))
		}
		if c.TTLSeconds < 0 {
			errs = append(errs, warn("<schema>", "", "cache.ttl_seconds must be non-negative"))
		}
		if c.Provider != "" && c.ConnectionString == "" {
			errs = append(errs, warnf("<schema>", "",
				"cache.provider %q specified but cache.connection_string is empty", c.Provider))
		}
		return errs
	}
	if c.Provider != "" || c.ConnectionString != "" {
		errs = append(errs, warn("<schema>", "",
			"cache.provider or cache.connection_string specified but cache.enabled is false"))
	}
	return errs
}

func validateRateLimitConfig(p parser.ProjectConfig) []ValidationError {
	var errs []ValidationError
	r := p.RateLimit
	if r == nil {
		return nil
	}
	if r.Policy != "" && !specmeta.IsRateLimitPolicy(r.Policy) {
		errs = append(errs, invalidf("<schema>", "",
			"unknown rate_limit.policy %q; allowed: %s", r.Policy, strings.Join(specmeta.RateLimitPolicies, ", ")))
	}
	if r.PermitLimit < 0 {
		errs = append(errs, warn("<schema>", "", "rate_limit.permit_limit must be non-negative"))
	}
	// An explicit 0 means "no window"; only negatives are invalid. The parser
	// fills in 60 when the key is omitted, so 0 never means "unset" here.
	if r.WindowSeconds < 0 {
		errs = append(errs, warn("<schema>", "", "rate_limit.window_seconds must be non-negative"))
	}
	return errs
}

func validatePaginationConfig(p parser.ProjectConfig) []ValidationError {
	var errs []ValidationError
	pg := p.Pagination
	if pg == nil {
		return nil
	}
	// An explicit 0 is meaningful (no default / no cap); only negatives are invalid.
	if pg.DefaultPageSize < 0 {
		errs = append(errs, warn("<schema>", "", "pagination.default_page_size must be non-negative"))
	}
	if pg.MaxPageSize < 0 {
		errs = append(errs, warn("<schema>", "", "pagination.max_page_size must be non-negative"))
	}
	if pg.DefaultPageSize > 0 && pg.MaxPageSize > 0 && pg.DefaultPageSize > pg.MaxPageSize {
		errs = append(errs, warnf("<schema>", "",
			"pagination.default_page_size (%d) must not exceed max_page_size (%d)", pg.DefaultPageSize, pg.MaxPageSize))
	}
	return errs
}

// validateInfrastructureConfig checks the provider-agnostic backing services
// used by addons such as Dapr.
func validateInfrastructureConfig(p parser.ProjectConfig) []ValidationError {
	var errs []ValidationError
	i := p.Infrastructure
	if i == nil {
		return nil
	}
	checks := []struct {
		value string
		valid func(string) bool
		kind  string
		allowed []string
	}{
		{i.Queue, specmeta.IsInfraQueue, "queue", specmeta.InfraQueues},
		{i.Cache, specmeta.IsInfraCacheStore, "cache", specmeta.InfraCacheStores},
		{i.Secrets, specmeta.IsInfraSecretStore, "secrets", specmeta.InfraSecretStores},
		{i.Storage, specmeta.IsInfraStore, "storage", specmeta.InfraStores},
	}
	for _, c := range checks {
		if c.value != "" && !c.valid(c.value) {
			errs = append(errs, invalidf("<schema>", "",
				"unknown infrastructure.%s %q; allowed: %s", c.kind, c.value, strings.Join(c.allowed, ", ")))
		}
	}
	return errs
}

// validateCircularRelations detects cycles in the FK graph (entity A references
// B which, directly or transitively, references A). Such cycles can produce
// problematic delete orders and seed ordering in generated code.
//
// Only FK-bearing relations participate in the graph: self-referential links
// (e.g. Category.parentId -> Category) are excluded because they are a normal
// hierarchy pattern, and many-to-many links (which own no FK column) are excluded
// because the FK physically lives on the other side.
func (v *Validator) validateCircularRelations() []ValidationError {
	var errs []ValidationError

	// Build adjacency: entity -> list of target entities it references via an FK.
	adj := make(map[string][]string)
	for _, entityName := range v.schema.EntityOrder {
		entity := v.schema.Entities[entityName]
		if entity == nil {
			continue
		}
		for _, fieldName := range entity.FieldOrder {
			field := entity.Fields[fieldName]
			if field == nil || !field.IsRelation() || field.TargetEntity == "" {
				continue
			}
			// Skip many-to-many links (no FK on this side) and self-references.
			if field.IsMany || field.TargetEntity == entityName {
				continue
			}
			adj[entityName] = append(adj[entityName], field.TargetEntity)
		}
	}

	// Standard DFS cycle detection (white/gray/black). All nodes on a gray stack
	// when a back-edge is found are part of the cycle.
	const (
		white = iota
		gray
		black
	)
	color := make(map[string]int)
	inCycle := make(map[string]bool)
	stack := make([]string, 0, len(v.schema.EntityOrder))

	var dfs func(node string)
	dfs = func(node string) {
		color[node] = gray
		stack = append(stack, node)
		for _, target := range adj[node] {
			switch color[target] {
			case white:
				dfs(target)
			case gray:
				// Back-edge found: target is gray, so it is on the stack.
				// Mark everything from target up to the current node.
				for _, n := range stack[slices.Index(stack, target):] {
					inCycle[n] = true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[node] = black
	}

	for _, entityName := range v.schema.EntityOrder {
		if color[entityName] == white {
			dfs(entityName)
		}
	}

	for _, entityName := range slices.Sorted(maps.Keys(inCycle)) {
		errs = append(errs, warn(entityName, "",
			"entity is part of a circular relation chain (A → B → … → A); generated delete/seed ordering may be ambiguous"))
	}

	return errs
}

func (v *Validator) validateAuth() []ValidationError {
	var errs []ValidationError
	auth := v.schema.Auth

	if auth.Type != "" && !specmeta.IsAuthType(auth.Type) {
		errs = append(errs, invalidf("<schema>", "",
			"unknown auth.type %q; allowed: %s", auth.Type, strings.Join(specmeta.AuthTypes, ", ")))
	}

	if !specmeta.IsAuthEnabled(auth.Type) {
		return errs
	}

	// Entity validation
	if auth.Entity != "" {
		entity, ok := v.schema.Entities[auth.Entity]
		if !ok {
			errs = append(errs, invalidf("<schema>", "",
				"auth.entity %q does not exist", auth.Entity))
		} else if hasEmail, hasPassword := specmeta.AuthFieldState(entity.FieldOrder); !hasEmail || !hasPassword {
			errs = append(errs, invalidf("<schema>", "",
				"auth.entity %q must have both 'email' and 'password' fields (missing: %s)", auth.Entity,
				missingFields(!hasEmail, !hasPassword)))
		}
	} else {
		// Auto-detect: find entity with email + password using specmeta helper.
		entityFields := v.buildEntityFields()
		if specmeta.FindAuthEntity(v.schema.EntityOrder, entityFields) == "" {
			errs = append(errs, invalidf("<schema>", "",
				"auth.type is %q but no entity has both 'email' and 'password' fields; specify auth.entity explicitly", auth.Type))
		}
	}

	if auth.Type == specmeta.AuthTypeJWT && len(auth.Roles) == 0 {
		errs = append(errs, invalidf("<schema>", "",
			"auth.roles must declare at least one role when auth.type is \"jwt\""))
	}

	// Roles must be valid identifiers
	for _, role := range auth.Roles {
		if !specmeta.IsValidIdentifier(role) {
			errs = append(errs, invalidf("<schema>", "",
				"auth.roles: %q is not a valid identifier", role))
		}
	}

	// Validate that auth roles are used in at least one entity's permissions.
	// When auth.roles is non-empty, check that permission roles are declared.
	// When auth.roles is empty, warn if any entity references named roles.
	roleSet := make(map[string]bool, len(auth.Roles))
	for _, r := range auth.Roles {
		roleSet[r] = true
	}
	for _, entityName := range v.schema.EntityOrder {
		entity := v.schema.Entities[entityName]
		if entity == nil || entity.Permissions == nil {
			continue
		}
		for _, roles := range [][]string{
			entity.Permissions.Read,
			entity.Permissions.Create,
			entity.Permissions.Update,
			entity.Permissions.Delete,
		} {
			for _, r := range roles {
				if specmeta.IsReservedPermissionToken(r) || r == "" {
					continue
				}
				if len(auth.Roles) > 0 && !roleSet[r] {
					errs = append(errs, warnf(entityName, "",
						"permission role %q is not defined in auth.roles; add it to auth.roles or remove from permissions", r))
				} else if len(auth.Roles) == 0 {
					errs = append(errs, warnf(entityName, "",
						"permission role %q is used but auth.roles is empty; define auth.roles to declare valid roles", r))
				}
			}
		}
	}

	return errs
}

// --- enum-level ---

// buildEntityFields returns entity name -> field order mapping for specmeta helpers.
func (v *Validator) buildEntityFields() map[string][]string {
	return specmeta.BuildEntityFields(v.schema.EntityOrder, func(name string) []string {
		if entity := v.schema.Entities[name]; entity != nil {
			return entity.FieldOrder
		}
		return nil
	})
}

// singleFKBack returns the first single (non-many) relation field declared on
// target that points back to sourceName, or nil when there is none. It mirrors
// the IR builder's one-to-many reconciliation: a `[many]` relation whose target
// declares such a FK describes the same relationship twice.
func (v *Validator) singleFKBack(target *parser.ParsedEntity, sourceName string) *parser.ParsedField {
	if target == nil {
		return nil
	}
	for _, fieldName := range target.FieldOrder {
		field := target.Fields[fieldName]
		if field != nil && specmeta.IsSingleBackRelationField(field.IsRelation(), field.IsMany, field.TargetEntity, sourceName) {
			return field
		}
	}
	return nil
}

func (v *Validator) validateEnums() []ValidationError {
	var errs []ValidationError
	for _, name := range slices.Sorted(maps.Keys(v.schema.Enums)) {
		values := v.schema.Enums[name]
		if !specmeta.IsValidIdentifier(name) {
			errs = append(errs, invalid("<enum>", name,
				"enum name is not a valid identifier"))
		}
		if len(values) == 0 {
			errs = append(errs, invalid("<enum>", name,
				"enum must have at least one value"))
			continue
		}
		// The IR derives each enum's wire value with textutil.ToDatabaseColumnName.
		// Use the same canonicalization here: two declared values that map to one
		// wire value produce duplicate members in every bridge.
		seen := make(map[string]string, len(values))
		for _, val := range values {
			if val == "" {
				errs = append(errs, invalid("<enum>", name,
					"enum value must not be empty"))
			}
			wire := textutil.ToDatabaseColumnName(val)
			if first, exists := seen[wire]; exists {
				errs = append(errs, invalidf("<enum>", name,
					"enum values %q and %q map to the same wire value %q", first, val, wire))
				continue
			}
			seen[wire] = val
		}
	}

	// Check for unused enums.
	if v.schema.Enums != nil {
		usedEnums := make(map[string]bool)
		for _, entity := range v.schema.Entities {
			if entity == nil {
				continue
			}
			for _, field := range entity.Fields {
				if field == nil {
					continue
				}
				if field.Type == specmeta.FieldTypeEnum && field.TargetType != "" {
					usedEnums[field.TargetType] = true
				}
				if field.Type == specmeta.FieldTypeArray && field.TargetType != "" {
					usedEnums[field.TargetType] = true
				}
			}
		}
		for _, enumName := range slices.Sorted(maps.Keys(v.schema.Enums)) {
			if !usedEnums[enumName] {
				errs = append(errs, warn("<enum>", enumName,
					"enum is declared but never used by any entity field"))
			}
		}
	}

	return errs
}

// --- entity-level ---

func (v *Validator) validateEntity(entityName string, entity *parser.ParsedEntity) []ValidationError {
	var errs []ValidationError

	// Entity name must be a valid identifier.
	if !specmeta.IsValidIdentifier(entityName) {
		errs = append(errs, invalid(entityName, "",
			"entity name is not a valid identifier"))
	}

	// old_name is a rename hint; like a field's old_name it must be a valid
	// identifier and must not point back at the entity itself.
	if entity.OldName != "" {
		if !specmeta.IsValidIdentifier(entity.OldName) {
			errs = append(errs, invalid(entityName, "",
				"old_name is not a valid identifier"))
		}
		if entity.OldName == entityName {
			errs = append(errs, invalid(entityName, "",
				"old_name cannot equal the entity name (it is the entity's previous name)"))
		}
	}

	// Must have at least one field.
	if len(entity.Fields) == 0 {
		errs = append(errs, invalid(entityName, "",
			"entity has no fields"))
	}

	// Must have a primary key.
	pkCount := countPrimaryKeys(entity)
	if pkCount == 0 {
		errs = append(errs, invalid(entityName, "",
			"entity must have at least one primary key"))
	} else if pkCount > 1 {
		// Must not have duplicate primary keys.
		errs = append(errs, invalidf(entityName, "",
			"entity has %d primary keys; expected exactly one", pkCount))
	}
	// A relation cannot be a primary key: validatePrimaryKeyField reports it
	// once per field; repeating the check here produced duplicate errors.

	// Track DB column names for collision detection.
	columnNames := make(map[string]string) // column -> field name

	// Track navigation names for collision detection.
	navigationNames := make(map[string]string) // navigation name -> field name

	for _, fieldName := range entity.FieldOrder {
		field := entity.Fields[fieldName]
		if field == nil {
			continue
		}

		errs = append(errs, v.validateField(entityName, fieldName, field)...)

		// old_name is a rename hint: the previous name of this field. It must not
		// equal the current name (that would be a self-rename) nor shadow another
		// field currently on the entity.
		if field.OldName != "" {
			if field.OldName == fieldName {
				errs = append(errs, invalid(entityName, fieldName,
					"old_name cannot equal the field name (it is the field's previous name)"))
			}
			if prev, exists := entity.Fields[field.OldName]; exists && prev != field {
				errs = append(errs, invalidf(entityName, fieldName,
					"old_name '%s' collides with an existing field '%s' on the same entity", field.OldName, prev.Name))
			}
		}

		// DB column collision.
		col := field.DatabaseColumnName
		if prev, exists := columnNames[col]; exists && prev != fieldName {
			errs = append(errs, invalidf(entityName, fieldName,
				"database column '%s' collides with field '%s'", col, prev))
		}
		columnNames[col] = fieldName

		// Navigation name collision: two relation fields resolving to the same
		// navigation property would produce duplicate properties in generated code.
		if field.IsRelation() {
			nav := field.NavigationName()
			if prev, exists := navigationNames[nav]; exists && prev != fieldName {
				errs = append(errs, invalidf(entityName, fieldName,
					"navigation name '%s' collides with field '%s'", nav, prev))
			}
			navigationNames[nav] = fieldName
		}
	}

	// Indexes.
	for i, idx := range entity.Indexes {
		errs = append(errs, v.validateIndex(entityName, i, idx, entity)...)
	}

	// Permissions.
	if entity.Permissions != nil {
		errs = append(errs, v.validatePermissions(entityName, entity.Permissions)...)
	}

	// Seed data.
	for i, seedEntry := range entity.Seed {
		errs = append(errs, v.validateSeedEntry(entityName, entity, i, seedEntry)...)
	}

	// Seed unique constraint: check that seed entries don't violate unique fields.
	if len(entity.Seed) > 1 {
		uniqueFields := make(map[string]map[string]bool) // field -> set of values
		for _, field := range entity.Fields {
			if field != nil && field.IsUnique {
				uniqueFields[field.Name] = make(map[string]bool)
			}
		}
		if len(uniqueFields) > 0 {
			for i, seedEntry := range entity.Seed {
				for _, fieldName := range slices.Sorted(maps.Keys(uniqueFields)) {
					values := uniqueFields[fieldName]
					seedVal, ok := seedEntry[fieldName]
					if !ok {
						continue
					}
					valStr := fmt.Sprintf("%v", seedVal)
					if values[valStr] {
						errs = append(errs, invalidf(entityName, fieldName,
							"seed entry %d: value %q for unique field '%s' is duplicated in seed data", i, valStr, fieldName))
					}
					values[valStr] = true
				}
			}
		}
	}

	return errs
}

// --- field-level ---

// validateField runs field rules in their canonical order so error ordering
// and messages stay stable.
func (v *Validator) validateField(entityName string, fieldName string, field *parser.ParsedField) []ValidationError {
	var errs []ValidationError
	errs = append(errs, validateFieldName(entityName, fieldName, field)...)
	errs = append(errs, v.validateRelationField(entityName, fieldName, field)...)
	errs = append(errs, validatePrimaryKeyField(entityName, fieldName, field)...)
	errs = append(errs, v.validateEnumField(entityName, fieldName, field)...)
	errs = append(errs, v.validateArrayField(entityName, fieldName, field)...)
	errs = append(errs, validateFieldValidations(entityName, fieldName, field)...)
	errs = append(errs, v.validateFieldDefault(entityName, fieldName, field)...)
	return errs
}

// validateFieldName checks the identifier and feature-field collisions.
func validateFieldName(entityName, fieldName string, field *parser.ParsedField) []ValidationError {
	var errs []ValidationError

	// Field name must be a valid identifier.
	if !specmeta.IsValidIdentifier(fieldName) {
		errs = append(errs, invalid(entityName, fieldName,
			"field name is not a valid identifier"))
	}

	// Feature field collision — warn only if user-defined type differs from auto-generated type.
	if fdef, isFeatureField := specmeta.FeatureFieldDefs[fieldName]; isFeatureField {
		userType := strings.ToLower(field.Type)
		if userType != "" && userType != fdef.Type {
			errs = append(errs, warnf(entityName, fieldName,
				"user-defined type '%s' overrides feature field '%s' (auto-generated type: '%s')", field.Type, fieldName,
				fdef.Type))
		}
	}

	return errs
}

// validateRelationField checks the cross-entity and semantic rules the lexer
// cannot perform.
func (v *Validator) validateRelationField(entityName, fieldName string, field *parser.ParsedField) []ValidationError {
	// Note: on_delete value, set_null+optional, many+on_delete, and many+relation
	// constraints are already enforced by the lexer. Only cross-entity and semantic
	// checks that the lexer cannot perform belong here.
	if !field.IsRelation() {
		return nil
	}

	var errs []ValidationError
	if field.TargetEntity == "" {
		errs = append(errs, invalid(entityName, fieldName,
			"relation field must specify a target entity"))
	}
	if field.TargetEntity != "" {
		target, ok := v.schema.Entities[field.TargetEntity]
		if !ok {
			errs = append(errs, invalidf(entityName, fieldName,
				"relation target '%s' does not exist", field.TargetEntity))
		} else if field.IsMany {
			// A [many] relation whose target also declares a single FK back to
			// this entity is reconciled into ONE one-to-many, not a join table.
			if back := v.singleFKBack(target, entityName); back != nil {
				errs = append(errs, warnf(entityName, fieldName,
					"[many] relation is reconciled with FK '%s' on %s into a one-to-many relationship (collection on %s); remove "+
						"one side if you meant a many-to-many join", back.Name, field.TargetEntity, entityName))
			}
		}
	}
	if field.IsMany && field.IsUnique {
		errs = append(errs, invalid(entityName, fieldName,
			"unique is contradictory on a many-to-many relation"))
	}
	// on_delete is meaningless on many-to-many relations (join table has no FK to delete).
	if field.IsMany && field.OnDelete != "" {
		errs = append(errs, warn(entityName, fieldName,
			"on_delete is meaningless on a many-to-many relation; remove it or use a many-to-one relation instead"))
	}
	// Self-referential required cascade is dangerous.
	if field.TargetEntity == entityName && !field.IsOptional && field.OnDelete == specmeta.OnDeleteCascade {
		errs = append(errs, warn(entityName, fieldName,
			"self-referential required cascade delete may cause recursive deletion"))
	}
	// Required relation without on_delete — warn.
	if !field.IsOptional && !field.IsMany && field.OnDelete == "" {
		errs = append(errs, warn(entityName, fieldName,
			"required relation has no on_delete specified; default behavior may not match expectations"))
	}
	return errs
}

// validatePrimaryKeyField checks constraints on a primary-key field.
func validatePrimaryKeyField(entityName, fieldName string, field *parser.ParsedField) []ValidationError {
	var errs []ValidationError

	// Primary key constraints.
	if field.IsPrimary {
		if field.Type == specmeta.FieldTypeArray {
			errs = append(errs, invalid(entityName, fieldName,
				"primary key cannot be an array type"))
		}
		if field.IsRelation() {
			errs = append(errs, invalidf(entityName, fieldName,
				"primary key cannot be a relation field (target '%s'); use a scalar id field", field.TargetEntity))
		}
	}

	return errs
}

// validateEnumField checks that an enum reference is declared.
func (v *Validator) validateEnumField(entityName, fieldName string, field *parser.ParsedField) []ValidationError {
	var errs []ValidationError

	// Enum reference.
	if field.Type == specmeta.FieldTypeEnum && field.TargetType != "" {
		if _, ok := v.schema.Enums[field.TargetType]; !ok {
			errs = append(errs, invalidf(entityName, fieldName,
				"enum '%s' is not defined in enums section", field.TargetType))
		}
	}

	return errs
}

// validateArrayField checks that an array element type is primitive or a
// declared enum.
func (v *Validator) validateArrayField(entityName, fieldName string, field *parser.ParsedField) []ValidationError {
	var errs []ValidationError

	// Array element type.
	if field.Type == specmeta.FieldTypeArray && field.TargetType != "" {
		inner := strings.ToLower(field.TargetType)
		if !specmeta.IsPrimitive(inner) {
			if _, ok := v.schema.Enums[field.TargetType]; !ok {
				errs = append(errs, invalidf(entityName, fieldName,
					"array element type '%s' is not a primitive or defined enum", field.TargetType))
			}
		}
	}

	return errs
}

// validateFieldValidations checks modifier applicability, numeric values and
// contradictory ranges.
func validateFieldValidations(entityName, fieldName string, field *parser.ParsedField) []ValidationError {
	var errs []ValidationError

	// Validation modifiers must be type-appropriate.
	ftype := strings.ToLower(field.Type)
	isNumeric := specmeta.IsNumeric(ftype)
	isString := specmeta.IsStringType(ftype)

	for _, mod := range slices.Sorted(maps.Keys(field.Validations)) {
		val := field.Validations[mod]
		if specmeta.IsStringOnlyValidationModifier(mod) && !isString {
			errs = append(errs, warnf(entityName, fieldName,
				"validation '%s' is only applicable to string/text fields, not %s", mod, ftype))
		}
		if specmeta.IsNumericValidationModifier(mod) && !isNumeric {
			errs = append(errs, warnf(entityName, fieldName,
				"validation '%s' is only applicable to numeric fields, not %s", mod, ftype))
		}
		if (mod == "min" || mod == "max") && !isString && !isNumeric {
			errs = append(errs, warnf(entityName, fieldName,
				"validation '%s' is only applicable to string/text or numeric fields, not %s", mod, ftype))
		}
		if val == "" {
			continue
		}
		switch {
		case mod == "min" || mod == "max":
			// String bounds are lengths and numeric bounds are values, but both
			// must be integers: IR emits min_value/max_value as numeric literals.
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				errs = append(errs, invalidf(entityName, fieldName,
					"validation '%s' value %q must be an integer", mod, val))
			} else if isString && n <= 0 {
				// String length bounds drive seed truncation; a non-positive
				// maximum would slice with a negative index.
				errs = append(errs, invalidf(entityName, fieldName,
					"validation '%s' must be positive for string length, got %q", mod, val))
			}
		case specmeta.IsNumericValidationModifier(mod):
			if _, err := strconv.ParseFloat(val, 64); err != nil {
				errs = append(errs, invalidf(entityName, fieldName,
					"validation '%s' value %q is not a valid number", mod, val))
			}
		}
	}

	// Check for contradictory range validations.
	errs = append(errs, validateContradictoryRanges(entityName, fieldName, field.Validations, isNumeric, isString)...)

	return errs
}

// validateFieldDefault checks default value types and enum membership.
func (v *Validator) validateFieldDefault(entityName, fieldName string, field *parser.ParsedField) []ValidationError {
	var errs []ValidationError

	ftype := strings.ToLower(field.Type)
	defaultType := ftype
	if ftype == specmeta.FieldTypeArray && field.TargetType != "" {
		// The element type drives the default check; specmeta owns extraction.
		defaultType = specmeta.FieldTypeArray + "(" + field.TargetType + ")"
	}

	// Default value type check.
	if field.DefaultValue != "" && field.DefaultIsFunc {
		errs = append(errs, validateFuncDefault(entityName, fieldName, ftype, field.DefaultValue)...)
	} else if field.DefaultValue != "" {
		errs = append(errs, validateDefaultValue(entityName, fieldName, defaultType, field.DefaultValue)...)
	}

	// Enum default must be one of the declared enum values.
	if field.Type == specmeta.FieldTypeEnum && field.TargetType != "" && field.DefaultValue != "" && !field.DefaultIsFunc {
		if enumValues, ok := v.schema.Enums[field.TargetType]; ok {
			found := slices.Contains(enumValues, field.DefaultValue)
			if !found {
				errs = append(errs, invalidf(entityName, fieldName,
					"default value '%s' is not a declared value of enum '%s'; valid values: %s", field.DefaultValue, field.TargetType,
					strings.Join(enumValues, ", ")))
			}
		}
	}

	return errs
}

// validateDefaultValue checks one scalar default, or every element of an array
// default, against the field's element type. Numeric defaults must parse as a
// number — the legacy true/false values no longer slip through.
func validateDefaultValue(entityName, fieldName, ftype, defaultVal string) []ValidationError {
	var errs []ValidationError
	inner := specmeta.ParseArrayInner(ftype)
	values := []string{defaultVal}
	if specmeta.IsArrayType(ftype) {
		values = specmeta.SplitArrayDefault(defaultVal)
	}

	for _, value := range values {
		switch {
		case specmeta.IsIntegralType(inner):
			if _, err := strconv.ParseInt(value, 10, 64); err != nil {
				errs = append(errs, invalidf(entityName, fieldName,
					"default value '%s' is not valid for numeric type %s", value, inner))
			}
		case specmeta.IsFloatType(inner):
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				errs = append(errs, invalidf(entityName, fieldName,
					"default value '%s' is not valid for numeric type %s", value, inner))
			}
		case inner == specmeta.FieldTypeBoolean:
			if !boolValues[value] {
				errs = append(errs, invalidf(entityName, fieldName,
					"default value '%s' is not valid for boolean (expected true/false)", value))
			}
		case inner == specmeta.FieldTypeDate:
			if !isValidDate(value) {
				errs = append(errs, invalidf(entityName, fieldName,
					"default value '%s' is not a valid date (expected YYYY-MM-DD)", value))
			}
		case inner == specmeta.FieldTypeDatetime:
			if !isValidDatetime(value) {
				errs = append(errs, invalidf(entityName, fieldName,
					"default value '%s' is not a valid datetime (expected ISO 8601 format, e.g. 2024-01-15T10:30:00Z)", value))
			}
		case inner == specmeta.FieldTypeUUID:
			if !isValidUUID(value) {
				errs = append(errs, invalidf(entityName, fieldName,
					"default value '%s' is not a valid UUID", value))
			}
		}
	}
	return errs
}

// validateFuncDefault checks that function-style defaults (e.g. now(), uuid()) are used on appropriate types.
func validateFuncDefault(entityName, fieldName, ftype, funcName string) []ValidationError {
	var errs []ValidationError
	allowedTypes, ok := specmeta.FuncDefaults[funcName]
	if !ok {
		errs = append(errs, invalidf(entityName, fieldName,
			"unknown default function '%s()'", funcName))
		return errs
	}
	allowed := slices.Contains(allowedTypes, ftype)
	if !allowed {
		errs = append(errs, invalidf(entityName, fieldName,
			"default '%s()' is not valid for type %s; allowed types: %s", funcName, ftype, strings.Join(allowedTypes, ", ")))
	}
	return errs
}

// validateContradictoryRanges checks for impossible validation ranges (e.g. min > max, gte > lt).
func validateContradictoryRanges(entityName, fieldName string, validations map[string]string,
	isNumeric, isString bool) []ValidationError {
	var errs []ValidationError

	getFloat := func(key string) (float64, bool) {
		val, ok := validations[key]
		if !ok || val == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}

	if isString {
		// String min/max are length bounds; keep their dedicated diagnostic.
		minVal, hasMin := getFloat("min")
		maxVal, hasMax := getFloat("max")
		if hasMin && hasMax && minVal > maxVal {
			errs = append(errs, invalidf(entityName, fieldName,
				"validation min (%g) is greater than max (%g)", minVal, maxVal))
		}
	}

	if isNumeric {
		// Compare the effective bounds, not just the four original pairs:
		// gte/lte, gt/lt and numeric min/max must be checked too, and one
		// contradictory range must produce one error.
		type bound struct {
			name   string
			value  float64
			strict bool
		}
		var lower, upper *bound
		addLower := func(b bound) {
			if lower == nil || b.value > lower.value || (b.value == lower.value && b.strict) {
				chosen := b
				lower = &chosen
			}
		}
		addUpper := func(b bound) {
			if upper == nil || b.value < upper.value || (b.value == upper.value && b.strict) {
				chosen := b
				upper = &chosen
			}
		}
		if value, ok := getFloat("gte"); ok {
			addLower(bound{name: "gte", value: value})
		}
		if value, ok := getFloat("gt"); ok {
			addLower(bound{name: "gt", value: value, strict: true})
		}
		// Numeric min/max are value bounds (IR maps them to min_value/max_value),
		// so they belong in the same effective-range check as gte/lte and gt/lt.
		if value, ok := getFloat("min"); ok {
			addLower(bound{name: "min", value: value})
		}
		if value, ok := getFloat("lte"); ok {
			addUpper(bound{name: "lte", value: value})
		}
		if value, ok := getFloat("lt"); ok {
			addUpper(bound{name: "lt", value: value, strict: true})
		}
		if value, ok := getFloat("max"); ok {
			addUpper(bound{name: "max", value: value})
		}
		if lower != nil && upper != nil &&
			(lower.value > upper.value || (lower.value == upper.value && (lower.strict || upper.strict))) {
			errs = append(errs, invalidf(entityName, fieldName,
				"validation %s (%g) must be less than %s (%g)", lower.name, lower.value, upper.name, upper.value))
		}
	}

	return errs
}

// missingFields returns a human-readable list of missing field names.
func missingFields(missingEmail, missingPassword bool) string {
	var parts []string
	if missingEmail {
		parts = append(parts, "email")
	}
	if missingPassword {
		parts = append(parts, "password")
	}
	return strings.Join(parts, " and ")
}

// --- index-level ---

func (v *Validator) validateIndex(entityName string, idxNum int, idx *parser.ParsedIndex,
	entity *parser.ParsedEntity) []ValidationError {
	var errs []ValidationError

	if len(idx.Fields) == 0 {
		errs = append(errs, invalidf(entityName, "",
			"index %d has no fields", idxNum))
	}

	// Duplicate fields in index.
	fieldSeen := make(map[string]bool)
	for _, f := range idx.Fields {
		if fieldSeen[f] {
			errs = append(errs, warnf(entityName, "",
				"index %d has duplicate field '%s'", idxNum, f))
		}
		fieldSeen[f] = true

		if _, ok := entity.Fields[f]; !ok {
			errs = append(errs, invalidf(entityName, f,
				"index %d references unknown field '%s'", idxNum, f))
		} else if entity.Fields[f].IsRelation() && !specmeta.HasIDSuffix(f) {
			errs = append(errs, warnf(entityName, f,
				"index %d references relation field '%s'; index the FK field instead (e.g. '%sId')", idxNum, f, f))
		}
	}

	// Index type.
	if idx.Type != "" && !specmeta.IsIndexType(idx.Type) {
		errs = append(errs, invalidf(entityName, "",
			"index %d has unknown type %q; allowed: %s", idxNum, idx.Type, strings.Join(specmeta.IndexTypes, ", ")))
	}

	// Sort array length must match fields length.
	if len(idx.Sort) > 0 && len(idx.Sort) != len(idx.Fields) {
		errs = append(errs, invalidf(entityName, "",
			"index %d: sort array length (%d) does not match fields length (%d)", idxNum, len(idx.Sort), len(idx.Fields)))
	}

	// Sort values must be asc or desc.
	for _, s := range idx.Sort {
		if !specmeta.IsSortDirection(s) {
			errs = append(errs, invalidf(entityName, "",
				"index %d: invalid sort value %q; allowed: %s", idxNum, s, strings.Join(specmeta.SortDirections, ", ")))
		}
	}

	return errs
}

// --- permission-level ---

func (v *Validator) validatePermissions(entityName string, perms *parser.ParsedPermissions) []ValidationError {
	var errs []ValidationError

	validateRoles := func(operation string, roles []string) {
		for _, role := range roles {
			if role == "" {
				errs = append(errs, invalidf(entityName, "",
					"permission '%s' has an empty role", operation))
				continue
			}
			// Skip special tokens: * (public) and @... (ownership tokens)
			if specmeta.IsReservedPermissionToken(role) {
				continue
			}
			if !specmeta.IsValidIdentifier(role) {
				errs = append(errs, warnf(entityName, "",
					"permission '%s' role %q is not a valid identifier", operation, role))
			}
		}
	}

	validateRoles("read", perms.Read)
	validateRoles("create", perms.Create)
	validateRoles("update", perms.Update)
	validateRoles("delete", perms.Delete)

	return errs
}

// --- seed-level ---

func (v *Validator) validateSeedEntry(entityName string, entity *parser.ParsedEntity,
	entryIdx int, entry map[string]any) []ValidationError {
	var errs []ValidationError

	// Check that seed fields exist.
	for _, seedField := range slices.Sorted(maps.Keys(entry)) {
		if _, ok := entity.Fields[seedField]; !ok {
			errs = append(errs, invalidf(entityName, seedField,
				"seed entry %d references unknown field '%s'", entryIdx, seedField))
		}
	}

	// Check seed value types against field types.
	for _, seedField := range slices.Sorted(maps.Keys(entry)) {
		seedValue := entry[seedField]
		field := entity.Fields[seedField]
		if field == nil {
			continue
		}
		errs = append(errs, v.validateSeedValueType(seedValueContext{
			entityName: entityName,
			fieldName:  seedField,
			entryIdx:   entryIdx,
			field:      field,
			seedValue:  seedValue,
		})...)
	}

	// Check required relation FK dependencies.
	for _, fieldName := range entity.FieldOrder {
		field := entity.Fields[fieldName]
		if field == nil || !field.IsRelation() || field.IsOptional || field.IsMany {
			continue
		}
		if _, inSeed := entry[fieldName]; inSeed {
			continue
		}
		if field.DefaultValue != "" {
			continue
		}
		target := v.schema.Entities[field.TargetEntity]
		if target == nil || len(target.Seed) == 0 {
			errs = append(errs, invalidf(entityName, fieldName,
				"seed entry %d: required relation '%s' -> '%s' is not seeded (add FK value to seed or add seed data to %s)",
				entryIdx, fieldName, field.TargetEntity, field.TargetEntity))
		}
	}

	return errs
}

// isNativeNumeric reports whether value has one of the numeric types YAML
// decoding produces. Shared by the integral and float seed checks so the
// accepted Go shapes cannot drift apart.
func isNativeNumeric(value any) bool {
	switch value.(type) {
	case int, int32, int64, float32, float64:
		return true
	default:
		return false
	}
}

// isFractionalFloat reports whether a numeric seed value is a float with a
// fractional part. Integral fields cannot hold such a value: the seed generator
// would truncate it (1.9 -> 1) without warning.
func isFractionalFloat(value any) bool {
	switch n := value.(type) {
	case float32:
		return math.Trunc(float64(n)) != float64(n)
	case float64:
		return math.Trunc(n) != n
	default:
		return false
	}
}

// seedValueContext groups the five inputs of one seed-value check. errf adds
// the shared "seed entry N:" prefix so each branch states only its own problem.
type seedValueContext struct {
	entityName string
	fieldName  string
	entryIdx   int
	field      *parser.ParsedField
	seedValue  any
}

func (c seedValueContext) errf(format string, args ...any) ValidationError {
	return invalidf(c.entityName, c.fieldName, "seed entry %d: "+format,
		append([]any{c.entryIdx}, args...)...)
}

// validateSeedValueType checks that a seed value is type-compatible with its field.
func (v *Validator) validateSeedValueType(c seedValueContext) []ValidationError {
	var errs []ValidationError
	add := func(e ValidationError) { errs = append(errs, e) }
	ftype := strings.ToLower(c.field.Type)

	switch {
	case specmeta.IsStringType(ftype):
		// String-like fields accept strings or JSON string values; numbers/bools are
		// accepted too (YAML coercion) but must not be a map/slice.
		switch c.seedValue.(type) {
		case map[string]any, []any:
			add(c.errf("value %v is not compatible with type %s", c.seedValue, ftype))
		}
	case specmeta.IsIntegralType(ftype):
		if s, ok := c.seedValue.(string); ok {
			// A string must parse; otherwise seed coercion silently yields 0.
			if _, err := strconv.ParseInt(s, 10, 64); err != nil {
				add(c.errf("value %q is not a valid %s", s, ftype))
			}
		} else if !isNativeNumeric(c.seedValue) {
			add(c.errf("value %v is not compatible with type %s", c.seedValue, ftype))
		} else if isFractionalFloat(c.seedValue) {
			// A fractional float would be silently truncated by the seed generator.
			add(c.errf("value %v is not compatible with type %s", c.seedValue, ftype))
		}
	case specmeta.IsFloatType(ftype):
		if s, ok := c.seedValue.(string); ok {
			if _, err := strconv.ParseFloat(s, 64); err != nil {
				add(c.errf("value %q is not a valid %s", s, ftype))
			}
		} else if !isNativeNumeric(c.seedValue) {
			add(c.errf("value %v is not compatible with type %s", c.seedValue, ftype))
		}
	case ftype == specmeta.FieldTypeBoolean:
		switch b := c.seedValue.(type) {
		case bool:
		case string:
			if s := strings.ToLower(b); s != "true" && s != "false" {
				add(c.errf("value %q is not valid for boolean type", c.seedValue))
			}
		default:
			add(c.errf("value %v is not compatible with boolean type", c.seedValue))
		}
	case ftype == specmeta.FieldTypeDate:
		s, ok := c.seedValue.(string)
		if !ok {
			add(c.errf("value %v is not compatible with date type (expected string YYYY-MM-DD)", c.seedValue))
		} else if !isValidDate(s) {
			add(c.errf("value %q is not a valid date (expected YYYY-MM-DD)", s))
		}
	case ftype == specmeta.FieldTypeUUID:
		s, ok := c.seedValue.(string)
		if !ok {
			add(c.errf("value %v is not compatible with uuid type (expected string)", c.seedValue))
		} else if !isValidUUID(s) {
			add(c.errf("value %q is not a valid UUID (expected xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)", s))
		}
	case ftype == specmeta.FieldTypeDatetime:
		s, ok := c.seedValue.(string)
		if !ok {
			add(c.errf("value %v is not compatible with datetime type (expected string)", c.seedValue))
		} else if !isValidDatetime(s) {
			add(c.errf("value %q is not a valid datetime (expected ISO 8601 format, e.g. 2024-01-15T10:30:00Z)", s))
		}
	case specmeta.FieldKind(ftype) == "json":
		// JSON values can be any valid JSON — accept any type.
		// String values are validated as parseable JSON.
		if s, ok := c.seedValue.(string); ok && !isValidJSON(s) {
			add(c.errf("value %q is not valid JSON", s))
		}
	case ftype == specmeta.FieldTypeEnum:
		// The value must be one of the declared enum values.
		sv, ok := c.seedValue.(string)
		if !ok {
			add(c.errf("value %v is not compatible with enum type (expected string)", c.seedValue))
			break
		}
		if values, defined := v.schema.Enums[c.field.TargetType]; defined && !slices.Contains(values, sv) {
			add(c.errf("value %q is not a declared value of enum '%s'; valid values: %s", sv, c.field.TargetType,
				strings.Join(values, ", ")))
		}
	case ftype == specmeta.FieldTypeArray:
		// Accept a YAML list. String values are accepted as JSON arrays.
		switch sv := c.seedValue.(type) {
		case []any, []string:
		case string:
			if !isValidJSONArray(sv) {
				add(c.errf("value %q is not a valid JSON array for array type", sv))
			}
		default:
			add(c.errf("value %v is not compatible with array type (expected a list)", c.seedValue))
		}
	case ftype == specmeta.FieldTypeRelation:
		// FK values reference the target entity's PK. Accept scalar values
		// (string/number/bool matching the target PK type); more precise
		// cross-entity type checking is left to the seed FK dependency check.
		switch c.seedValue.(type) {
		case string, int, int32, int64, float32, float64, bool:
		default:
			add(c.errf("value %v is not compatible with relation FK type", c.seedValue))
		}
	}

	return errs
}

// isValidDate is shared by default-value and seed validation. Each caller
// builds its own diagnostic, so the message stays out of the helper.
func isValidDate(s string) bool {
	t, err := time.Parse("2006-01-02", s)
	return err == nil && t.Year() >= 1970 && t.Year() <= 2100
}

func isValidJSONArray(s string) bool {
	var arr []any
	return json.Unmarshal([]byte(s), &arr) == nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isValidUUID is shared by default-value and seed validation; each caller
// keeps its own diagnostic text.
func isValidUUID(s string) bool {
	return uuidPattern.MatchString(s)
}

var datetimePatterns = []string{
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05-07:00",
	"2006-01-02T15:04:05.000Z",
	"2006-01-02T15:04:05.000-07:00",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// isValidDatetime is shared by default-value and seed validation; each caller
// keeps its own diagnostic text.
func isValidDatetime(s string) bool {
	for _, pattern := range datetimePatterns {
		if t, err := time.Parse(pattern, s); err == nil && t.Year() >= 1970 && t.Year() <= 2100 {
			return true
		}
	}
	return false
}

func isValidJSON(s string) bool {
	var v any
	return json.Unmarshal([]byte(s), &v) == nil
}

// --- helpers ---

// countPrimaryKeys returns the number of primary key fields in the entity.
func countPrimaryKeys(entity *parser.ParsedEntity) int {
	count := 0
	for _, fieldName := range entity.FieldOrder {
		field := entity.Fields[fieldName]
		if field != nil && field.IsPrimary {
			count++
		}
	}
	return count
}
