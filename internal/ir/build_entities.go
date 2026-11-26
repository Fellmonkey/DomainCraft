package ir

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"github.com/DomainCraft/DomainCraft/pkg/textutil"
)

// copySeed detaches the IR from ParsedSchema: a caller mutating seed rows after
// Build must not silently change the contract templates render from.
func copySeed(rows []map[string]any) []map[string]any {
	if rows == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, maps.Clone(row))
	}
	return out
}

// oldDatabaseColumnName returns the snake_case DB column name of a field's
// old_name hint (previous field name), or "" when the field has no rename hint.
func oldDatabaseColumnName(field *parser.ParsedField) string {
	if field == nil || field.OldName == "" {
		return ""
	}
	return textutil.ToDatabaseColumnName(field.OldName)
}

// applyProjectConfig copies the optional project blocks into their IR shape.
// A nil parser block means "absent"; a non-nil block is copied even when all of
// its values are zero — presence flags stay in the parser and are resolved
// before Build, so the IR needs only the values.
func applyProjectConfig(target *Project, p parser.ProjectConfig) {
	target.Cache = convertCacheConfig(p.Cache)
	target.CORS = convertCORSConfig(p.CORS)
	target.Deploy = convertDeployConfig(p.Deploy)
	target.Versioning = convertVersioningConfig(p.Versioning)
	target.RateLimit = convertRateLimitConfig(p.RateLimit)
	target.Pagination = convertPaginationConfig(p.Pagination)
	target.Infrastructure = convertInfrastructure(p.Infrastructure)
}

func convertCacheConfig(src *parser.CacheConfig) *CacheConfig {
	if src == nil {
		return nil
	}
	return &CacheConfig{
		Enabled:          src.Enabled,
		Provider:         src.Provider,
		ConnectionString: src.ConnectionString,
		TTLSeconds:       src.TTLSeconds,
	}
}

func convertCORSConfig(src *parser.CORSConfig) *CORSConfig {
	if src == nil {
		return nil
	}
	return &CORSConfig{
		Enabled: src.Enabled,
		Origins: append([]string(nil), src.Origins...),
	}
}

func convertDeployConfig(src *parser.DeployConfig) *DeployConfig {
	if src == nil {
		return nil
	}
	return &DeployConfig{Domain: src.Domain, Port: src.Port}
}

func convertVersioningConfig(src *parser.VersioningConfig) *VersioningConfig {
	if src == nil {
		return nil
	}
	return &VersioningConfig{
		Enabled:        src.Enabled,
		DefaultVersion: src.DefaultVersion,
	}
}

func convertRateLimitConfig(src *parser.RateLimitConfig) *RateLimitConfig {
	if src == nil {
		return nil
	}
	return &RateLimitConfig{
		Enabled:       src.Enabled,
		Policy:        src.Policy,
		PermitLimit:   src.PermitLimit,
		WindowSeconds: src.WindowSeconds,
	}
}

func convertPaginationConfig(src *parser.PaginationConfig) *PaginationConfig {
	if src == nil {
		return nil
	}
	return &PaginationConfig{
		DefaultPageSize: src.DefaultPageSize,
		MaxPageSize:     src.MaxPageSize,
	}
}

func convertInfrastructure(src *parser.InfrastructureConfig) *Infrastructure {
	if src == nil {
		return nil
	}
	return &Infrastructure{
		Queue:   src.Queue,
		Cache:   src.Cache,
		Secrets: src.Secrets,
		Storage: src.Storage,
	}
}

// Build converts ParsedSchema into Project.
func Build(schema *parser.ParsedSchema) (*Project, error) {
	if schema == nil {
		return nil, fmt.Errorf("parsed schema is nil")
	}

	irProject := &Project{
		Name:        schema.Project.Name,
		Description: schema.Project.Description,
		Database:    schema.Database,
		Auth:        convertAuth(schema.Auth, schema),
		APIStyle:    schema.APIStyle,
		Platform:    schema.Project.Platform,
		Enums:       copyEnumsSorted(schema.Enums),
		EnumOrder:   sortedEnumKeys(schema.Enums),
		Entities:    make([]Entity, 0, len(schema.Entities)),
	}

	applyProjectConfig(irProject, schema.Project)

	// Pass 1: build entities
	entityIndex := make(map[string]*Entity, len(schema.Entities))
	for _, entityName := range schema.EntityOrder {
		sourceEntity := schema.Entities[entityName]
		if sourceEntity == nil {
			continue
		}

		irEntity := Entity{
			Name:         sourceEntity.Name,
			OldName:      sourceEntity.OldName,
			NamePlural:   sourceEntity.NamePlural,
			Features:     copyFeatureMap(sourceEntity.Features),
			Fields:       make([]Field, 0, len(sourceEntity.FieldOrder)),
			RelationsOut: make([]Relation, 0, len(sourceEntity.Fields)),
			RelationsIn:  make([]Relation, 0, len(sourceEntity.Fields)),
			Indexes:      make([]Index, 0, len(sourceEntity.Indexes)),
			Seed:         copySeed(sourceEntity.Seed),
			Permissions:  convertPermissions(sourceEntity.Permissions),
		}

		for _, fieldName := range sourceEntity.FieldOrder {
			field := sourceEntity.Fields[fieldName]
			if field == nil {
				continue
			}

			databaseType, err := resolveDatabaseType(field, schema, make(map[string]bool))
			if err != nil {
				return nil, fmt.Errorf("field '%s.%s': %w", sourceEntity.Name, field.Name, err)
			}

			irEntity.Fields = append(irEntity.Fields, Field{
				Name:                  field.Name,
				OldName:               field.OldName,
				DatabaseType:          databaseType,
				DatabaseColumnName:    field.DatabaseColumnName,
				OldDatabaseColumnName: oldDatabaseColumnName(field),
				NavigationName:        field.NavigationName(),
				IsPrimary:             field.IsPrimary,
				IsNullable:            field.IsOptional,
				IsUnique:              field.IsUnique,
				IsHidden:              field.IsHidden,
				IsReadonly:            field.IsReadonly,
				IsRelation:            field.IsRelation(),
				IsMany:                field.IsMany,
				RelationTarget:        field.TargetEntity,
				DefaultValue:          field.DefaultValue,
				DefaultIsFunc:         field.DefaultIsFunc,
				Validations:           convertValidations(field.Validations),
			})
		}

		for _, idx := range sourceEntity.Indexes {
			irEntity.Indexes = append(irEntity.Indexes, Index{
				Name:   idx.Name,
				Fields: append([]string(nil), idx.Fields...),
				Type:   idx.Type,
				Sort:   append([]string(nil), idx.Sort...),
				Unique: idx.Unique,
			})
		}

		irProject.Entities = append(irProject.Entities, irEntity)
		entityIndex[irEntity.Name] = &irProject.Entities[len(irProject.Entities)-1]
	}

	// Pass 2a: forward relations
	if err := buildForwardRelations(irProject.Entities, schema, entityIndex); err != nil {
		return nil, err
	}

	// One index serves all three relation passes.
	relationIndex := indexRelationsOut(irProject.Entities)

	// Pass 2b: reconcile one-to-many
	reconcileOneToMany(irProject.Entities, relationIndex)

	// Pass 2c: resolve inverse navigation names
	resolveInverseNavs(irProject.Entities, relationIndex)

	// Pass 3d: incoming relations
	buildIncomingRelations(irProject.Entities, relationIndex)

	// Topologically sort entities by FK dependencies (parents before children).
	irProject.Entities = sortEntities(irProject.Entities)

	return irProject, nil
}

func convertValidations(source map[string]string) []Validation {
	if len(source) == 0 {
		return nil
	}
	result := make([]Validation, 0, len(source))
	for _, key := range slices.Sorted(maps.Keys(source)) {
		result = append(result, Validation{Name: key, Value: source[key]})
	}
	return result
}

func convertPermissions(source *parser.ParsedPermissions) *Permissions {
	if source == nil {
		return nil
	}
	return &Permissions{
		Read:   append([]string(nil), source.Read...),
		Create: append([]string(nil), source.Create...),
		Update: append([]string(nil), source.Update...),
		Delete: append([]string(nil), source.Delete...),
	}
}

// resolveDatabaseType resolves a field's database type. Relation fields inherit
// the type of the target entity's primary key, which is itself a relation in a
// malformed model; visiting guards that chain against infinite recursion.
func resolveDatabaseType(field *parser.ParsedField, schema *parser.ParsedSchema, visiting map[string]bool) (string, error) {
	if field == nil || field.FieldDefinition == nil {
		return "", fmt.Errorf("field definition is missing")
	}

	if field.IsRelation() {
		key := field.TargetEntity + "." + field.Name
		if visiting[key] {
			return "", fmt.Errorf("cyclic relation primary key at '%s'", key)
		}
		visiting[key] = true
		defer delete(visiting, key)

		target, ok := schema.Entities[field.TargetEntity]
		if !ok {
			return "", fmt.Errorf("relation target '%s' does not exist", field.TargetEntity)
		}
		for _, targetFieldName := range target.FieldOrder {
			targetField := target.Fields[targetFieldName]
			if targetField != nil && targetField.IsPrimary {
				return resolveDatabaseType(targetField, schema, visiting)
			}
		}
		return "", fmt.Errorf("relation target '%s' has no primary key", field.TargetEntity)
	}

	if field.Type == specmeta.FieldTypeArray {
		return resolveArrayType(field.TargetType), nil
	}

	if field.Type == specmeta.FieldTypeEnum {
		if field.TargetType == "" {
			return "", fmt.Errorf("enum field has no target type")
		}
		return field.TargetType, nil
	}

	if specmeta.IsPrimitive(field.Type) {
		return field.Type, nil
	}
	return "", fmt.Errorf("unknown field type %q", field.Type)
}

func resolveArrayType(targetType string) string {
	inner := strings.ToLower(targetType)
	if specmeta.IsPrimitive(inner) {
		return "array(" + inner + ")"
	}
	if targetType != "" {
		return "array(" + targetType + ")"
	}
	return "array(string)"
}

func convertAuth(source *parser.AuthConfig, schema *parser.ParsedSchema) *AuthConfig {
	if source == nil || !specmeta.IsAuthEnabled(source.Type) {
		return nil
	}

	entity := source.Entity
	if entity == "" {
		entity = specmeta.FindAuthEntity(schema.EntityOrder, specmeta.BuildEntityFields(schema.EntityOrder, func(name string) []string {
			if e := schema.Entities[name]; e != nil {
				return e.FieldOrder
			}
			return nil
		}))
	}

	login := source.Endpoints.HasLogin()
	register := source.Endpoints.HasRegister()
	me := source.Endpoints.HasMe()
	setup := source.Endpoints.HasSetup()

	return &AuthConfig{
		Type:   source.Type,
		Entity: entity,
		Roles:  append([]string(nil), source.Roles...),
		Endpoints: AuthEndpoints{
			HasLogin:    login,
			HasRegister: register,
			HasMe:       me,
			HasSetup:    setup,
		},
	}
}

func copyFeatureMap(source map[string]bool) map[string]bool {
	return maps.Clone(source)
}

// copyEnumsSorted returns a deep copy of the enums map with sorted keys for deterministic iteration.
func copyEnumsSorted(source map[string][]string) map[string][]string {
	if source == nil {
		return nil
	}
	result := make(map[string][]string, len(source))
	for _, k := range slices.Sorted(maps.Keys(source)) {
		v := source[k]
		cp := make([]string, len(v))
		copy(cp, v)
		result[k] = cp
	}
	return result
}

// sortedEnumKeys returns a sorted slice of enum names for deterministic iteration.
func sortedEnumKeys(source map[string][]string) []string {
	return slices.Sorted(maps.Keys(source))
}
