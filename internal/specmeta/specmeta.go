// Package specmeta is the single source of truth for specification constants.
package specmeta

import "strings"

// FeatureFieldDef describes a field auto-injected by a feature macro.
type FeatureFieldDef struct {
	Feature       string `json:"feature"`
	Type          string `json:"type"`
	DBColumn      string `json:"dbColumn"`
	IsOptional    bool   `json:"isOptional"`
	IsFuncDefault bool   `json:"isFuncDefault"`
	DefaultValue  string `json:"defaultValue"`
}

var Databases = []string{"postgresql", "mysql", "sqlite", "mssql", "mongodb", "appwrite"}

var APIStyles = []string{"rest", "graphql", "grpc"}

var AuthTypes = []string{"jwt", "none"}

var Features = []string{"audit", "audit_log", "soft_delete", "optimistic_lock", "event_sourced", "cacheable"}

// Addons are optional infrastructure accelerators that a bridge can opt into
// via the CLI (e.g. `--addons dapr`). They are NOT declared in domain.yaml —
// bridges read them at render time to toggle templates. Kept here as the single
// source of truth so the CLI/interactive layer can validate and list them.
// Dapr is the canonical addon: it speaks to virtually any broker, state store,
// secrets provider and output binding, so bridges only need this one.
// Observability toggles structured logging (Serilog/Seq) + distributed tracing
// (OpenTelemetry/Jaeger) for bridges that opt in.
var Addons = []string{"dapr", "observability"}

// Infrastructure providers declared in project.infrastructure. These are the
// building blocks Dapr (and similar addons) wire up at runtime.
var InfraQueues = []string{"pubsub", "rabbitmq", "kafka", "redis", "nats", "in-memory"}
var InfraCacheStores = []string{"redis", "memcached", "in-memory"}
var InfraSecretStores = []string{"local", "kubernetes", "azure-keyvault", "aws-secrets"}
var InfraStores = []string{"local", "s3", "azure-blob", "gcs"}

var MetaFieldTypes = []string{"relation", "array", "enum"}

var FieldTypes = append(append([]string{}, PrimitiveFieldTypes...), MetaFieldTypes...)

var FeatureFieldDefs = map[string]FeatureFieldDef{
	"createdAt": {Feature: "audit", Type: "datetime", DBColumn: "created_at", IsFuncDefault: true, DefaultValue: "now"},
	"updatedAt": {Feature: "audit", Type: "datetime", DBColumn: "updated_at", IsFuncDefault: true, DefaultValue: "now"},
	"createdBy": {Feature: "audit_log", Type: "uuid", DBColumn: "created_by"},
	"updatedBy": {Feature: "audit_log", Type: "uuid", DBColumn: "updated_by"},
	"deletedAt": {Feature: "soft_delete", Type: "datetime", DBColumn: "deleted_at", IsOptional: true},
	"version":   {Feature: "optimistic_lock", Type: "int", DBColumn: "version", DefaultValue: "0"},
}

var OnDeleteValues = []string{"cascade", "set_null", "restrict", "no_action"}

var PrimitiveFieldTypes = []string{
	"string", "text", "int", "bigint", "float", "decimal",
	"boolean", "date", "datetime", "uuid", "json", "jsonb",
}

var NumericFieldTypes = []string{"int", "bigint", "float", "decimal"}

var StringFieldTypes = []string{"string", "text"}

var NumericValidationModifiers = []string{"gte", "gt", "lte", "lt"}

var StringValidationModifiers = []string{"min", "max", "email", "url", "ipv4", "regex"}

var IndexTypes = []string{"btree", "hash", "gist", "gin", "brin"}

var CacheProviders = []string{"redis", "memcached", "in-memory"}

var MultiTenancyModes = []string{"column", "schema", "database"}

var RateLimitPolicies = []string{"fixed", "sliding"}

var PermissionKeys = []string{"read", "create", "update", "delete"}

var SortDirections = []string{"asc", "desc"}

var BooleanFieldTypes = []string{"boolean"}

var FuncDefaults = map[string][]string{
	"now":  {"date", "datetime"},
	"uuid": {"uuid"},
}

const DefaultFieldType = "string"

var (
	primitiveSet            map[string]bool
	numericSet              map[string]bool
	stringSet               map[string]bool
	booleanSet              map[string]bool
	fieldTypeSet            map[string]bool
	onDeleteSet             map[string]bool
	featureSet              map[string]bool
	indexTypeSet            map[string]bool
	cacheProviderSet        map[string]bool
	multiTenancyModeSet     map[string]bool
	rateLimitPolicySet      map[string]bool
	permissionKeySet        map[string]bool
	stringValidationModSet  map[string]bool
	numericValidationModSet map[string]bool
	sortDirectionSet        map[string]bool
	databaseSet             map[string]bool
	apiStyleSet             map[string]bool
	authTypeSet             map[string]bool
	addonSet                map[string]bool
	infraQueueSet           map[string]bool
	infraCacheStoreSet      map[string]bool
	infraSecretStoreSet     map[string]bool
	infraStoreSet           map[string]bool
)

func init() {
	primitiveSet = SliceToSet(PrimitiveFieldTypes)
	numericSet = SliceToSet(NumericFieldTypes)
	stringSet = SliceToSet(StringFieldTypes)
	booleanSet = SliceToSet(BooleanFieldTypes)
	fieldTypeSet = SliceToSet(FieldTypes)
	onDeleteSet = SliceToSet(OnDeleteValues)
	featureSet = SliceToSet(Features)
	indexTypeSet = SliceToSet(IndexTypes)
	cacheProviderSet = SliceToSet(CacheProviders)
	multiTenancyModeSet = SliceToSet(MultiTenancyModes)
	rateLimitPolicySet = SliceToSet(RateLimitPolicies)
	permissionKeySet = SliceToSet(PermissionKeys)
	stringValidationModSet = SliceToSet(StringValidationModifiers)
	numericValidationModSet = SliceToSet(NumericValidationModifiers)
	sortDirectionSet = SliceToSet(SortDirections)
	databaseSet = SliceToSet(Databases)
	apiStyleSet = SliceToSet(APIStyles)
	authTypeSet = SliceToSet(AuthTypes)
	addonSet = SliceToSet(Addons)
	infraQueueSet = SliceToSet(InfraQueues)
	infraCacheStoreSet = SliceToSet(InfraCacheStores)
	infraSecretStoreSet = SliceToSet(InfraSecretStores)
	infraStoreSet = SliceToSet(InfraStores)
}

// IsPrimitive reports whether typeName is a built-in field type.
func IsPrimitive(typeName string) bool { return primitiveSet[typeName] }

// IsNumeric reports whether typeName is a numeric field type.
func IsNumeric(typeName string) bool { return numericSet[typeName] }

// IsStringType reports whether typeName is a string or text field type.
func IsStringType(typeName string) bool { return stringSet[typeName] }

// IsBooleanType reports whether typeName is a boolean field type.
func IsBooleanType(typeName string) bool { return booleanSet[typeName] }

// IsSortDirection reports whether value is a supported sort direction.
func IsSortDirection(value string) bool { return sortDirectionSet[value] }

// IsFieldType reports whether typeName is any allowed field type.
func IsFieldType(typeName string) bool { return fieldTypeSet[typeName] }

// IsOnDeleteValue reports whether value is an allowed on_delete behavior.
func IsOnDeleteValue(value string) bool { return onDeleteSet[value] }

// IsFeature reports whether name is an allowed entity feature.
func IsFeature(name string) bool { return featureSet[name] }

// IsIndexType reports whether value is an allowed index type.
func IsIndexType(value string) bool { return indexTypeSet[value] }

// IsCacheProvider reports whether value is an allowed cache provider.
func IsCacheProvider(value string) bool { return cacheProviderSet[value] }

// IsMultiTenancyMode reports whether value is an allowed multi-tenancy mode.
func IsMultiTenancyMode(value string) bool { return multiTenancyModeSet[value] }

// IsRateLimitPolicy reports whether value is an allowed rate-limit policy.
func IsRateLimitPolicy(value string) bool { return rateLimitPolicySet[value] }

// IsPermissionKey reports whether value is an allowed permission operation.
func IsPermissionKey(value string) bool { return permissionKeySet[value] }

// IsDatabase reports whether value is a supported database.
func IsDatabase(value string) bool { return databaseSet[value] }

// IsAPIStyle reports whether value is a supported API style.
func IsAPIStyle(value string) bool { return apiStyleSet[value] }

// IsAuthType reports whether value is a supported auth type.
func IsAuthType(value string) bool { return authTypeSet[value] }

// IsAddon reports whether value is a known infrastructure addon.
func IsAddon(value string) bool { return addonSet[value] }

// IsInfraQueue reports whether value is a supported queue backend.
func IsInfraQueue(value string) bool { return infraQueueSet[value] }

// IsInfraCacheStore reports whether value is a supported cache store.
func IsInfraCacheStore(value string) bool { return infraCacheStoreSet[value] }

// IsInfraSecretStore reports whether value is a supported secret store.
func IsInfraSecretStore(value string) bool { return infraSecretStoreSet[value] }

// IsInfraStore reports whether value is a supported object store.
func IsInfraStore(value string) bool { return infraStoreSet[value] }

// IsStringValidationModifier reports whether mod applies to string fields.
func IsStringValidationModifier(mod string) bool { return stringValidationModSet[mod] }

// IsNumericValidationModifier reports whether mod applies to numeric fields.
func IsNumericValidationModifier(mod string) bool { return numericValidationModSet[mod] }

// SliceToSet converts a slice to a set for constant-time membership checks.
func SliceToSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}

// IsArrayType reports whether dbType is an array declaration.
func IsArrayType(dbType string) bool {
	return strings.HasPrefix(dbType, "array(")
}

// ParseArrayInner returns the element type of an array declaration, or dbType unchanged.
func ParseArrayInner(dbType string) string {
	if !IsArrayType(dbType) {
		return dbType
	}
	return strings.TrimSuffix(strings.TrimPrefix(dbType, "array("), ")")
}

// FindAuthEntity returns the first entity with email and password fields.
func FindAuthEntity(entityOrder []string, entityFields map[string][]string) string {
	for _, name := range entityOrder {
		if HasEmailAndPassword(entityFields[name]) {
			return name
		}
	}
	return ""
}

// HasEmailAndPassword reports whether the field list contains both auth fields.
func HasEmailAndPassword(fieldNames []string) bool {
	hasEmail, hasPassword := AuthFieldState(fieldNames)
	return hasEmail && hasPassword
}

// AuthFieldState reports whether the field list contains "email" and "password"
// (case-insensitive). Used to build helpful messages and to auto-detect auth entities.
func AuthFieldState(fieldNames []string) (hasEmail, hasPassword bool) {
	for _, f := range fieldNames {
		switch strings.ToLower(f) {
		case "email":
			hasEmail = true
		case "password":
			hasPassword = true
		}
	}
	return hasEmail, hasPassword
}

// BuildEntityFields maps each entity name to its field names in declaration order.
func BuildEntityFields(entityOrder []string, getFields func(name string) []string) map[string][]string {
	result := make(map[string][]string, len(entityOrder))
	for _, name := range entityOrder {
		result[name] = getFields(name)
	}
	return result
}

// IsPublicPermission reports whether the role is the "*" wildcard (public access).
func IsPublicPermission(role string) bool { return role == "*" }

// IsOwnershipToken reports whether the role is an "@..." ownership token (e.g. "@Owner").
func IsOwnershipToken(role string) bool { return strings.HasPrefix(role, "@") }

// IsReservedPermissionToken reports whether the role is a special token
// ("*" public wildcard or "@..." ownership token) that is not a declared role.
func IsReservedPermissionToken(role string) bool {
	return IsPublicPermission(role) || IsOwnershipToken(role)
}
