// Package ir builds the resolved intermediate representation consumed by bridges.
package ir

import (
	"slices"
	"strings"

	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"github.com/DomainCraft/DomainCraft/pkg/textutil"
)

// Project represents the intermediate project model.
type Project struct {
	Name        string
	Description string
	Database    string
	Auth        *AuthConfig
	APIStyle    string
	Platform    string // target platform version (e.g. "net9.0"), passed through to templates
	// Enums maps enum name → ordered values. EnumOrder holds the sorted keys so
	// templates can iterate deterministically (Go map iteration order is random).
	Enums     map[string][]string
	EnumOrder []string
	Entities  []Entity
	Cache     *CacheConfig
	CORS      *CORSConfig
	Deploy    *DeployConfig
	// Versioning carries API versioning settings (enabled flag + default version).
	Versioning *VersioningConfig
	// RateLimit carries the HTTP request rate limiting policy.
	RateLimit *RateLimitConfig
	// Pagination carries list sizing defaults (page size + cap).
	Pagination *PaginationConfig
	// Addons contains the infrastructure accelerators requested via the CLI
	// (e.g. {"dapr": true}). It is not derived from domain.yaml — declared
	// infrastructure lives in Infrastructure, addon templates go on/off here.
	Addons map[string]bool
	// Infrastructure holds the provider-agnostic backing services declared in
	// project.infrastructure (queue / cache / secrets). It is the input that
	// addon bridges like Dapr consume to emit concrete component manifests.
	Infrastructure *Infrastructure
}

// Infrastructure represents the project's backing services in IR.
type Infrastructure struct {
	Queue   string // message broker: pubsub, rabbitmq, kafka, redis, nats, in-memory
	Cache   string // distributed cache store: redis, memcached, in-memory
	Secrets string // secrets store: local, kubernetes, azure-keyvault, aws-secrets
	Storage string // object/file storage: local, s3, azure-blob, gcs
}

// HasAddon returns true if the named infrastructure addon is enabled.
func (p *Project) HasAddon(name string) bool {
	if p.Addons == nil {
		return false
	}
	return p.Addons[name]
}

// AuthConfig represents authentication configuration in IR.
type AuthConfig struct {
	Type      string
	Entity    string // resolved entity name
	Roles     []string
	Endpoints AuthEndpoints
}

// AuthEndpoints controls which auth endpoints are generated.
type AuthEndpoints struct {
	HasLogin    bool
	HasRegister bool
	HasMe       bool
	HasSetup    bool
}

// HasAuth returns true if authentication is enabled.
func (p *Project) HasAuth() bool {
	return p.Auth != nil && specmeta.IsAuthEnabled(p.Auth.Type)
}

// AuthEntity returns the resolved authentication entity (the entity that owns
// email/password and drives the login/register/me/setup flow), or nil when auth
// is disabled or the entity could not be found. Bridges use this instead of
// scanning Project.Entities for the auth entity.
func (p *Project) AuthEntity() *Entity {
	if p == nil || !p.HasAuth() {
		return nil
	}
	for i := range p.Entities {
		if p.Entities[i].Name == p.Auth.Entity {
			return &p.Entities[i]
		}
	}
	return nil
}

// CacheConfig represents cache configuration in IR.
type CacheConfig struct {
	Enabled          bool
	Provider         string
	ConnectionString string
	TTLSeconds       int
}

// CORSConfig represents CORS configuration in IR.
type CORSConfig struct {
	Enabled bool
	Origins []string
}

// DeployConfig represents deployment configuration in IR.
type DeployConfig struct {
	Domain string // API domain (e.g. "localhost", "api.example.com")
	Port   int    // exposed port (default: 8080)
}

// VersioningConfig controls API versioning and the default version string.
type VersioningConfig struct {
	Enabled        bool
	DefaultVersion string
}

// RateLimitConfig represents the HTTP request rate limiting policy in IR.
type RateLimitConfig struct {
	Enabled       bool
	Policy        string // fixed | sliding
	PermitLimit   int
	WindowSeconds int
}

// PaginationConfig represents list pagination sizing in IR.
type PaginationConfig struct {
	DefaultPageSize int
	MaxPageSize     int
}

// HasVersioning returns true when API versioning is enabled.
func (p *Project) HasVersioning() bool {
	return p.Versioning != nil && p.Versioning.Enabled
}

// HasRateLimit returns true when request rate limiting is enabled.
func (p *Project) HasRateLimit() bool {
	return p.RateLimit != nil && p.RateLimit.Enabled
}

// Entity represents an entity in IR.
type Entity struct {
	Name         string
	OldName      string // previous entity name (rename hint for the migration engine)
	NamePlural   string
	Features     map[string]bool
	Fields       []Field
	RelationsOut []Relation
	RelationsIn  []Relation
	Indexes      []Index
	Seed         []map[string]any
	Permissions  *Permissions
}

// Field represents a field in IR.
type Field struct {
	Name               string
	OldName            string // previous field name (rename hint from `old_name:` modifier)
	DatabaseType       string
	DatabaseColumnName string // snake_case column name (computed once, used by templates)
	// OldDatabaseColumnName is the snake_case column name of OldName. Bridges use
	// it to emit a safe RenameColumn instead of DropColumn + AddColumn.
	OldDatabaseColumnName string
	NavigationName        string // resolved navigation property name (for relation fields)
	IsPrimary             bool
	IsNullable            bool
	IsUnique              bool
	IsHidden              bool
	IsReadonly            bool
	IsRelation            bool
	IsMany                bool
	RelationTarget        string
	DefaultValue          string
	DefaultIsFunc         bool
	Validations           []Validation
}

// fieldsWhere is the single ordered field filter behind the relation views.
func (e Entity) fieldsWhere(pred func(Field) bool) []Field {
	var result []Field
	for _, f := range e.Fields {
		if pred(f) {
			result = append(result, f)
		}
	}
	return result
}

// NonRelationFields returns only scalar/non-relation fields (excludes relation FK fields).
func (e Entity) NonRelationFields() []Field {
	return e.fieldsWhere(func(f Field) bool { return !f.IsRelation })
}

// RelationFields returns only relation fields.
func (e Entity) RelationFields() []Field {
	return e.fieldsWhere(func(f Field) bool { return f.IsRelation })
}

// EagerLoadRelations returns the relations a bridge should eager-load when
// fetching a full entity graph (list / by-id): every outgoing relation. Bridges
// iterate this instead of re-deriving the Include set, and call
// EagerLoadNavigation() on each relation for the property to Include.
func (e Entity) EagerLoadRelations() []Relation {
	return slices.Clone(e.RelationsOut)
}

// EagerLoadNavigation returns the navigation property name used to Include a
// relation: the collection property for a [many] relation, the navigation
// property for a single foreign key. Bridges apply their own casing
// (pascalcase, camelcase, ...) to the returned name.
func (r Relation) EagerLoadNavigation() string {
	if r.IsMany {
		return r.FieldName
	}
	return r.NavigationName
}

// TableNameFor returns the snake_case database table name for an entity name.
// It is the single source of truth for table names — bridges and the migration
// engine must use it instead of re-deriving the name with their own snake_case
// (the template `snakecase` diverges from textutil.ToDatabaseColumnName on
// acronyms, e.g. `IPv4Address` -> `i_pv4address`).
func TableNameFor(name string) string {
	return textutil.ToDatabaseColumnName(textutil.Pluralize(name))
}

// TableName returns the entity's snake_case database table name. It stays based
// on the parser-provided NamePlural (which may be an irregular plural); callers
// that only have a singular name use TableNameFor.
func (e Entity) TableName() string {
	return textutil.ToDatabaseColumnName(e.NamePlural)
}

// ForeignKeyColumnName returns the snake_case DB column name of this relation's
// foreign key (fieldName + "Id", snake_cased). Bridges must print this instead
// of `fkName .FieldName | snakecase`, which diverges from the core's column-name
// algorithm on acronyms.
func (r Relation) ForeignKeyColumnName() string {
	return textutil.ToDatabaseColumnName(textutil.FKName(r.FieldName))
}

// ColumnName returns the field's canonical snake_case DB column name: the FK
// column name for a relation field, the parser-computed DatabaseColumnName for a
// scalar field (falling back to ToDatabaseColumnName(Name) when empty). This is
// the single source of truth a bridge or the migration engine uses so it never
// re-derives a column name with its own snake_case function.
func (f Field) ColumnName() string {
	if f.IsRelation {
		return textutil.ToDatabaseColumnName(textutil.FKName(f.Name))
	}
	if f.DatabaseColumnName != "" {
		return f.DatabaseColumnName
	}
	return textutil.ToDatabaseColumnName(f.Name)
}

// OldColumnName returns the previous column name of a renamed field. It is the
// single source of truth for the migration report and the migration plan: a
// missing OldDatabaseColumnName falls back to the canonical snake_case of the
// declared old_name, the same way ColumnName treats the current field name.
func (f Field) OldColumnName() string {
	if f.OldDatabaseColumnName != "" {
		return f.OldDatabaseColumnName
	}
	return textutil.ToDatabaseColumnName(f.OldName)
}

// HasFeature returns true if the entity has the named feature enabled.
func (e Entity) HasFeature(name string) bool {
	if e.Features != nil {
		return e.Features[name]
	}
	return false
}

// PrimaryKey returns the entity's primary key field, or nil when none is marked.
func (e Entity) PrimaryKey() *Field {
	for i := range e.Fields {
		if e.Fields[i].IsPrimary {
			return &e.Fields[i]
		}
	}
	return nil
}

// FieldByName returns the field with the given name (case-insensitive), or nil.
// Bridges use this to resolve a seed/column name back to its Field without
// re-implementing the lookup.
func (e Entity) FieldByName(name string) *Field {
	for i := range e.Fields {
		if strings.EqualFold(e.Fields[i].Name, name) {
			return &e.Fields[i]
		}
	}
	return nil
}

// HasAudit returns true if the entity has the audit feature enabled.
func (e Entity) HasAudit() bool { return e.HasFeature("audit") }

// HasAuditLog returns true if the entity has the audit_log feature enabled.
func (e Entity) HasAuditLog() bool { return e.HasFeature("audit_log") }

// HasSoftDelete returns true if the entity has the soft_delete feature enabled.
func (e Entity) HasSoftDelete() bool { return e.HasFeature("soft_delete") }

// HasOptimisticLock returns true if the entity has the optimistic_lock feature enabled.
func (e Entity) HasOptimisticLock() bool { return e.HasFeature("optimistic_lock") }

// HasEventSourced returns true if the entity emits domain events (event_sourced feature).
func (e Entity) HasEventSourced() bool { return e.HasFeature("event_sourced") }

// HasCacheable returns true if the entity should use the distributed cache (cacheable feature).
func (e Entity) HasCacheable() bool { return e.HasFeature("cacheable") }

// IsArray returns true if the field's DatabaseType is an array type (e.g. "array(int)").
func (f Field) IsArray() bool {
	return specmeta.IsArrayType(f.DatabaseType)
}

// ArrayElementType returns the inner type of an array field (e.g. "int" from "array(int)").
// Returns empty string if the field is not an array.
func (f Field) ArrayElementType() string {
	if !f.IsArray() {
		return ""
	}
	return specmeta.ParseArrayInner(f.DatabaseType)
}

// Validation represents one validation rule.
type Validation struct {
	Name  string
	Value string
}

// Relation represents a relation between entities.
type Relation struct {
	FieldName        string
	TargetEntity     *Entity
	NavigationName   string
	InverseNavName   string
	OnDeleteBehavior string
	IsNullable       bool
	// IsMany describes the relation itself, not its inverse collection. For a
	// many-to-many inverse in RelationsIn it is false; bridges detect that case
	// through RelationType, not by reading IsMany.
	IsMany       bool
	RelationType string

	// PairFieldName is set when a `[many]` relation is reconciled with a single
	// (FK) relation declared on the target entity: the two declarations describe
	// one one-to-many relationship. It holds the FK field name on the target
	// (e.g. "wallet" for Wallet.Transactions <-> WalletTransaction.Wallet), so
	// bridges can render WithOne(...).HasForeignKey(...) instead of a join table.
	PairFieldName string

	// PairNavigationName is the resolved navigation property name for the paired
	// FK relation on the target entity. For Order.Items <-> OrderItem.OrderId the
	// target's field is "orderId" so the navigation is "Order" (not "OrderId").
	// Bridges use this to render WithOne(e => e.Order) correctly.
	PairNavigationName string
}

// Index represents an index.
type Index struct {
	Name   string
	Fields []string
	Type   string
	Sort   []string
	Unique bool
}

// Permissions represents entity permissions in IR.
type Permissions struct {
	Read   []string
	Create []string
	Update []string
	Delete []string
}
