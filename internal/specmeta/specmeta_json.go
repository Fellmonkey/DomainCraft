package specmeta

import (
	"maps"
	"slices"
)

// JSON returns every specmeta constant as a JSON-ready map. This is the
// single source of the shape exposed to the GUI via the WASM bridge
// (cmd/wasm-validator -> goSpecmeta). The GUI loads it once at boot into its
// live SPECMETA object — no other copy is shipped.
//
// Keep the keys in sync with the Specmeta type in
// domaincraft-studio/src/lib/specmeta.ts.
func JSON() map[string]any {
	strings := func(s []string) []string { return slices.Clone(s) }
	return map[string]any{
		"primitiveFieldTypes":        strings(PrimitiveFieldTypes),
		"stringFieldTypes":           strings(StringFieldTypes),
		"numericFieldTypes":          strings(NumericFieldTypes),
		"booleanFieldTypes":          strings(BooleanFieldTypes),
		"metaFieldTypes":             strings(MetaFieldTypes),
		"fieldTypes":                 strings(FieldTypes),
		"onDeleteValues":             strings(OnDeleteValues),
		"features":                   strings(Features),
		"addons":                     strings(Addons),
		"indexTypes":                 strings(IndexTypes),
		"databases":                  strings(Databases),
		"apiStyles":                  strings(APIStyles),
		"authTypes":                  strings(AuthTypes),
		"cacheProviders":             strings(CacheProviders),
		"multiTenancyModes":          strings(MultiTenancyModes),
		"rateLimitPolicies":          strings(RateLimitPolicies),
		"permissionKeys":             strings(PermissionKeys),
		"sortDirections":             strings(SortDirections),
		"stringValidationModifiers":  strings(StringValidationModifiers),
		"numericValidationModifiers": strings(NumericValidationModifiers),
		"infraQueues":                strings(InfraQueues),
		"infraCacheStores":           strings(InfraCacheStores),
		"infraSecretStores":          strings(InfraSecretStores),
		"infraStores":                strings(InfraStores),
		"featureFieldDefs":           maps.Clone(FeatureFieldDefs),
	}
}
