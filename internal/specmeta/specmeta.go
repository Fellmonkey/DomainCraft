// Package specmeta is the single source of truth for specification constants.
package specmeta

import (
	"strings"
	"unicode"
)

// FeatureFieldDef describes a field auto-injected by a feature macro.
type FeatureFieldDef struct {
	Feature       string `json:"feature"`
	Type          string `json:"type"`
	DBColumn      string `json:"dbColumn"`
	IsOptional    bool   `json:"isOptional"`
	IsFuncDefault bool   `json:"isFuncDefault"`
	DefaultValue  string `json:"defaultValue"`
}

// Databases lists the supported database backends.
var Databases = []string{"postgresql", "mysql", "sqlite", "mssql", "mongodb", "appwrite"}

// APIStyles lists the supported API styles.
var APIStyles = []string{"rest", "graphql", "grpc"}

// AuthTypes lists the supported authentication types.
var AuthTypes = []string{"jwt", "none"}

// Features lists the entity features the core injects fields for.
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

// InfraQueues lists supported queue providers declared in project.infrastructure.
var InfraQueues = []string{"pubsub", "rabbitmq", "kafka", "redis", "nats", "in-memory"}

// InfraCacheStores lists supported cache providers.
var InfraCacheStores = []string{"redis", "memcached", "in-memory"}

// InfraSecretStores lists supported secret stores.
var InfraSecretStores = []string{"local", "kubernetes", "azure-keyvault", "aws-secrets"}

// InfraStores lists supported object stores.
var InfraStores = []string{"local", "s3", "azure-blob", "gcs"}

// MetaFieldTypes lists the non-primitive field types.
var MetaFieldTypes = []string{"relation", "array", "enum"}

// FieldTypes lists every valid field type.
var FieldTypes = append(append([]string{}, PrimitiveFieldTypes...), MetaFieldTypes...)

// FeatureFieldDefs maps injected field names to their feature definition.
var FeatureFieldDefs = map[string]FeatureFieldDef{
	"createdAt": {Feature: "audit", Type: "datetime", DBColumn: "created_at", IsFuncDefault: true, DefaultValue: "now"},
	"updatedAt": {Feature: "audit", Type: "datetime", DBColumn: "updated_at", IsFuncDefault: true, DefaultValue: "now"},
	"createdBy": {Feature: "audit_log", Type: "uuid", DBColumn: "created_by"},
	"updatedBy": {Feature: "audit_log", Type: "uuid", DBColumn: "updated_by"},
	"deletedAt": {Feature: "soft_delete", Type: "datetime", DBColumn: "deleted_at", IsOptional: true},
	"version":   {Feature: "optimistic_lock", Type: "int", DBColumn: "version", DefaultValue: "0"},
}

// OnDeleteValues lists the valid on_delete modifier values.
var OnDeleteValues = []string{"cascade", "set_null", "restrict", "no_action"}

// PrimitiveFieldTypes lists the scalar field types.
var PrimitiveFieldTypes = []string{
	"string", "text", "int", "bigint", "float", "decimal",
	"boolean", "date", "datetime", "uuid", "json", "jsonb",
}

// IntegralFieldTypes lists the integral scalar types.
var IntegralFieldTypes = []string{"int", "bigint"}

// FloatFieldTypes lists the floating-point scalar types.
var FloatFieldTypes = []string{"float", "decimal"}

// NumericFieldTypes lists the numeric scalar types.
var NumericFieldTypes = append(append([]string{}, IntegralFieldTypes...), FloatFieldTypes...)

// StringFieldTypes lists the string scalar types.
var StringFieldTypes = []string{"string", "text"}

// NumericValidationModifiers lists numeric validation modifier names.
var NumericValidationModifiers = []string{"gte", "gt", "lte", "lt"}

// StringValidationModifiers lists string validation modifier names.
var StringValidationModifiers = []string{"min", "max", "email", "url", "ipv4", "regex"}

// IndexTypes lists the supported database index types.
var IndexTypes = []string{"btree", "hash", "gist", "gin", "brin"}

// CacheProviders lists the supported cache providers.
var CacheProviders = []string{"redis", "memcached", "in-memory"}

// MultiTenancyModes lists the supported multi-tenancy modes.
var MultiTenancyModes = []string{"column", "schema", "database"}

// RateLimitPolicies lists the supported rate-limit policies.
var RateLimitPolicies = []string{"fixed", "sliding"}

// PermissionKeys lists the four permission operations.
var PermissionKeys = []string{"read", "create", "update", "delete"}

// SortDirections lists the valid sort directions.
var SortDirections = []string{"asc", "desc"}

// BooleanFieldTypes lists the boolean scalar types.
var BooleanFieldTypes = []string{"boolean"}

// FuncDefaults maps default functions to their compatible field types.
var FuncDefaults = map[string][]string{
	"now":  {"date", "datetime"},
	"uuid": {"uuid"},
}

// DefaultFieldType is the fallback type for unresolvable fields.
const DefaultFieldType = "string"

// ContractVersion is the bridge-contract version this core implements. Bridges
// declare their own version via `contract_version` in bridge.yaml; a declared
// version above this constant is not supported.
const ContractVersion = 1

// EffectiveContractVersion returns the contract version to use for a declared
// value. An absent or zero declaration (the legacy default) resolves to
// ContractVersion; any positive declaration is taken as-is, so callers can still
// reject a bridge that declares a newer contract than this core implements.
func EffectiveContractVersion(declared int) int {
	if declared > 0 {
		return declared
	}
	return ContractVersion
}

var (
	primitiveSet            map[string]bool
	numericSet              map[string]bool
	integralSet             map[string]bool
	floatSet                map[string]bool
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
	integralSet = SliceToSet(IntegralFieldTypes)
	floatSet = SliceToSet(FloatFieldTypes)
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

// IsIntegralType reports whether typeName is an integral field type (int or bigint).
func IsIntegralType(typeName string) bool { return integralSet[typeName] }

// IsFloatType reports whether typeName is a floating-point field type (float or decimal).
func IsFloatType(typeName string) bool { return floatSet[typeName] }

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

// IsValidIdentifier reports whether s is a valid domain.yaml identifier. The
// rule is the one shared by entity, field, enum and role names: a non-empty
// ASCII string starting with a letter or underscore, followed by letters,
// digits or underscores. It is the single source of truth for this rule — the
// parser, lexer and validator must not re-derive it.
func IsValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if i == 0 {
			if !letter && c != '_' {
				return false
			}
			continue
		}
		if !letter && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// IsIdentifierRune reports whether r can appear inside an identifier in source
// text. It is intentionally wider than IsValidIdentifier: rename rewriting must
// recognise Unicode identifiers that domain.yaml itself does not allow.
func IsIdentifierRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
