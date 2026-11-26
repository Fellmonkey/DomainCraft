package parser

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"go.yaml.in/yaml/v3"
)

// RawSchema represents the root structure of domain.yaml
type RawSchema struct {
	Project  ProjectConfig        `yaml:"project" json:"project"`
	Database string               `yaml:"database" json:"database,omitempty"`
	Auth     AuthConfig           `yaml:"auth" json:"auth"`
	APIStyle string               `yaml:"api_style" json:"apiStyle"`
	Entities map[string]RawEntity `yaml:"entities" json:"entities"`
	Enums    map[string][]string  `yaml:"enums" json:"enums,omitempty"`
}

// ProjectConfig contains project-level information
type ProjectConfig struct {
	Name         string              `yaml:"name" json:"name"`
	Description  string              `yaml:"description" json:"description,omitempty"`
	Version      string              `yaml:"version" json:"version,omitempty"`
	Platform     string              `yaml:"platform" json:"platform,omitempty"`
	MultiTenancy *MultiTenancyConfig `yaml:"multi_tenancy" json:"multi_tenancy,omitempty"`
	Cache        *CacheConfig        `yaml:"cache" json:"cache,omitempty"`
	CORS         *CORSConfig         `yaml:"cors" json:"cors,omitempty"`
	Deploy       *DeployConfig       `yaml:"deploy" json:"deploy,omitempty"`
	// Versioning controls API versioning. Language-agnostic: the bridge decides
	// how to express it (Asp.Versioning, Django REST framework versioning, etc.).
	Versioning *VersioningConfig `yaml:"versioning" json:"versioning,omitempty"`
	// RateLimit controls HTTP rate limiting policy (permit limit / window / algorithm).
	RateLimit *RateLimitConfig `yaml:"rate_limit" json:"rate_limit,omitempty"`
	// Pagination controls list endpoint page sizing (default page size + hard cap).
	Pagination *PaginationConfig `yaml:"pagination" json:"pagination,omitempty"`
	// Infrastructure declares the backing services (message broker, distributed
	// cache, secrets store) that addons such as Dapr wire up at runtime. Kept
	// purely declarative and provider-agnostic — no specific library/cloud terms.
	Infrastructure *InfrastructureConfig `yaml:"infrastructure" json:"infrastructure,omitempty"`
}

// InfrastructureConfig declares the provider-agnostic infrastructure backing a
// project. Bridges that ship an addon (e.g. Dapr) turn these into concrete
// components (pubsub/statestore/secrets) while the domain model stays clean.
type InfrastructureConfig struct {
	Queue   string `yaml:"queue" json:"queue,omitempty"`     // message broker: pubsub, rabbitmq, kafka, redis, nats, in-memory
	Cache   string `yaml:"cache" json:"cache,omitempty"`     // distributed cache store: redis, memcached, in-memory
	Secrets string `yaml:"secrets" json:"secrets,omitempty"` // secrets store: local, kubernetes, azure-keyvault, aws-secrets
	Storage string `yaml:"storage" json:"storage,omitempty"` // object/file storage: local, s3, azure-blob, gcs
}

// AuthConfig describes authentication configuration.
type AuthConfig struct {
	Type      string        `yaml:"type" json:"type"`
	Entity    string        `yaml:"entity" json:"entity,omitempty"`
	Roles     []string      `yaml:"roles" json:"roles,omitempty"`
	Endpoints AuthEndpoints `yaml:"endpoints" json:"endpoints"`
}

// AuthEndpoints controls which auth endpoints are generated.
type AuthEndpoints struct {
	Login    *bool `yaml:"login" json:"login"`
	Register *bool `yaml:"register" json:"register"`
	Me       *bool `yaml:"me" json:"me"`
	Setup    *bool `yaml:"setup" json:"setup"`
}

// HasLogin returns true if login endpoint is enabled (default: true).
func (e AuthEndpoints) HasLogin() bool { return e.Login == nil || *e.Login }

// HasRegister returns true if register endpoint is enabled (default: true).
func (e AuthEndpoints) HasRegister() bool { return e.Register == nil || *e.Register }

// HasMe returns true if me endpoint is enabled (default: true).
func (e AuthEndpoints) HasMe() bool { return e.Me == nil || *e.Me }

// HasSetup returns true if setup endpoint is enabled (default: true).
func (e AuthEndpoints) HasSetup() bool { return e.Setup == nil || *e.Setup }

// DeployConfig represents deployment configuration.
type DeployConfig struct {
	Domain string `yaml:"domain" json:"domain,omitempty"`
	Port   int    `yaml:"port" json:"port,omitempty"`
	// portSet records whether "port" was present in YAML. An explicit 0 is a
	// value, not a missing key, so ParseRawSchema must not default it to 8080.
	portSet bool
}

// walkYAMLMerge is the single alias/sequence/mapping traversal for YAML merge
// values ("<<"). Callers supply what to collect and how to report an invalid
// node; dedupe selects YAML's "earlier entries win" rule.
func walkYAMLMerge(value *yaml.Node, dedupe bool, visit func(key, val *yaml.Node), invalid func(*yaml.Node) error) error {
	seen := make(map[string]bool)
	var collect func(*yaml.Node) error
	collect = func(node *yaml.Node) error {
		switch node.Kind {
		case yaml.AliasNode:
			if node.Alias == nil {
				if invalid != nil {
					return invalid(node)
				}
				return nil
			}
			return collect(node.Alias)
		case yaml.SequenceNode:
			for _, item := range node.Content {
				if err := collect(item); err != nil {
					return err
				}
			}
			return nil
		case yaml.MappingNode:
			for i := 0; i < len(node.Content)-1; i += 2 {
				key, val := node.Content[i], node.Content[i+1]
				if key.Value == "<<" && key.Tag != "!!str" {
					if err := collect(val); err != nil {
						return err
					}
					continue
				}
				if dedupe && seen[key.Value] {
					continue
				}
				seen[key.Value] = true
				visit(key, val)
			}
			return nil
		default:
			if invalid != nil {
				return invalid(node)
			}
			return nil
		}
	}
	return collect(value)
}

// yamlMergeKeyNames returns the key names contributed by a YAML merge value.
// The alias decode applies the merged values; this walk only records which
// keys were present so an explicit zero in a merged block survives.
func yamlMergeKeyNames(value *yaml.Node) []string {
	var names []string
	_ = walkYAMLMerge(value, false, func(key, _ *yaml.Node) {
		names = append(names, key.Value)
	}, nil)
	return names
}

// fieldDefPair is one field name together with its definition node, as
// contributed by a YAML merge ("<<") in an entity's `fields` mapping.
type fieldDefPair struct {
	name  string
	value *yaml.Node
}

// yamlMergeFieldPairs keeps both the key and the value node so each merged
// field definition can be decoded into a string. A merge value may be a
// mapping, an alias to a mapping, or a sequence of such nodes; nested merge
// keys are resolved recursively. Duplicate names keep the first occurrence.
func yamlMergeFieldPairs(value *yaml.Node, mergeKey *yaml.Node) ([]fieldDefPair, error) {
	var pairs []fieldDefPair
	err := walkYAMLMerge(value, true,
		func(key, val *yaml.Node) {
			pairs = append(pairs, fieldDefPair{name: key.Value, value: val})
		},
		func(node *yaml.Node) error {
			if node.Kind == yaml.AliasNode {
				return fmt.Errorf("line %d: '<<' in fields resolves to an undefined alias", mergeKey.Line)
			}
			return fmt.Errorf("line %d: '<<' in fields must be a mapping or a sequence of mappings, got %s",
				mergeKey.Line, strings.ToLower(strings.TrimPrefix(node.ShortTag(), "!!")))
		},
	)
	if err != nil {
		return nil, err
	}
	return pairs, nil
}

// checkStrictKeys is the single strict-key rule for every nested config block.
// It rejects unknown keys, including ones contributed by a YAML merge ("<<"),
// and optionally records which allowed keys were present. errFn carries the
// block-specific wording; line preserves the source location from the node.
func checkStrictKeys(value *yaml.Node, allowed []string, errFn func(string, int) error, mark func(string)) error {
	if value.Kind != yaml.MappingNode {
		return nil
	}
	valid := specmeta.SliceToSet(allowed)
	for i := 0; i < len(value.Content)-1; i += 2 {
		keyNode := value.Content[i]
		key := keyNode.Value
		if key == "<<" && keyNode.Tag != "!!str" {
			for _, name := range yamlMergeKeyNames(value.Content[i+1]) {
				if !valid[name] {
					return errFn(name, keyNode.Line)
				}
				if mark != nil {
					mark(name)
				}
			}
			continue
		}
		if !valid[key] {
			return errFn(key, keyNode.Line)
		}
		if mark != nil {
			mark(key)
		}
	}
	return nil
}

// decodeWithStrictKeys is the shared decode skeleton: non-mapping values are
// decoded as-is, mapping values are checked first. Callers only supply the
// rejection message and an optional presence marker.
func decodeWithStrictKeys(value *yaml.Node, target any, allowed []string, errFn func(string, int) error, mark func(string)) error {
	if value.Kind != yaml.MappingNode {
		return value.Decode(target)
	}
	if err := checkStrictKeys(value, allowed, errFn, mark); err != nil {
		return err
	}
	return value.Decode(target)
}

// decodeStrict decodes a block into an alias target, rejecting unknown keys.
func decodeStrict(value *yaml.Node, target any, allowed []string, reject func(string, int) error) error {
	return decodeWithStrictKeys(value, target, allowed, reject, nil)
}

// unmarshalYAMLWithPresence decodes a config block through target — an alias
// of the concrete config type, so yaml.v3 does not re-enter UnmarshalYAML —
// while recording which of the keys in presence were present. Presence is
// tracked for direct keys and for keys contributed by a YAML merge ("<<"),
// so an explicit zero survives the defaulting pass in ParseRawSchema.
//
// Every key must be listed in allowed. Node.Decode uses a fresh decoder without
// KnownFields(true), so strict decoding is enforced here to keep the root
// decoder's unknown-key behavior inside the block.
func unmarshalYAMLWithPresence(value *yaml.Node, target any, typeName string, allowed []string, presence map[string]func()) error {
	errFn := func(key string, line int) error {
		return fmt.Errorf("line %d: field %s not found in type parser.%s", line, key, typeName)
	}
	mark := func(key string) {
		if fn, tracked := presence[key]; tracked {
			fn()
		}
	}
	return decodeWithStrictKeys(value, target, allowed, errFn, mark)
}

type deployConfigAlias DeployConfig

// UnmarshalYAML records key presence before decoding the block. Decoding goes
// through an alias so yaml.v3 does not call this method recursively.
func (d *DeployConfig) UnmarshalYAML(value *yaml.Node) error {
	return unmarshalYAMLWithPresence(value, (*deployConfigAlias)(d), "DeployConfig",
		[]string{"domain", "port"},
		map[string]func(){"port": func() { d.portSet = true }})
}

// CacheConfig represents cache configuration (agnostic — no language/platform specifics).
type CacheConfig struct {
	Enabled          bool   `yaml:"enabled" json:"enabled"`
	Provider         string `yaml:"provider" json:"provider,omitempty"`
	ConnectionString string `yaml:"connection_string" json:"connection_string,omitempty"`
	TTLSeconds       int    `yaml:"ttl_seconds" json:"ttl_seconds,omitempty"`
}

// CORSConfig represents CORS configuration.
type CORSConfig struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Origins []string `yaml:"origins" json:"origins,omitempty"`
}

// VersioningConfig represents API versioning configuration.
type VersioningConfig struct {
	Enabled        bool   `yaml:"enabled" json:"enabled"`
	DefaultVersion string `yaml:"default_version" json:"default_version,omitempty"`
}

// RateLimitConfig represents HTTP request rate limiting configuration.
type RateLimitConfig struct {
	Enabled       bool   `yaml:"enabled" json:"enabled"`
	Policy        string `yaml:"policy" json:"policy,omitempty"` // fixed | sliding
	PermitLimit   int    `yaml:"permit_limit" json:"permit_limit,omitempty"`
	WindowSeconds int    `yaml:"window_seconds" json:"window_seconds,omitempty"`
	// Presence flags distinguish an explicit 0 (deny all / no window) from an
	// omitted key that must receive the documented default.
	permitLimitSet   bool
	windowSecondsSet bool
}

type rateLimitConfigAlias RateLimitConfig

// UnmarshalYAML records key presence before decoding the block. Decoding goes
// through an alias so yaml.v3 does not call this method recursively.
func (r *RateLimitConfig) UnmarshalYAML(value *yaml.Node) error {
	return unmarshalYAMLWithPresence(value, (*rateLimitConfigAlias)(r), "RateLimitConfig",
		[]string{"enabled", "policy", "permit_limit", "window_seconds"},
		map[string]func(){
			"permit_limit":   func() { r.permitLimitSet = true },
			"window_seconds": func() { r.windowSecondsSet = true },
		})
}

// PaginationConfig represents list pagination sizing.
type PaginationConfig struct {
	DefaultPageSize int `yaml:"default_page_size" json:"default_page_size,omitempty"`
	MaxPageSize     int `yaml:"max_page_size" json:"max_page_size,omitempty"`
	// Presence flags distinguish an explicit 0 from an omitted key that must
	// receive the documented default.
	defaultPageSizeSet bool
	maxPageSizeSet     bool
}

type paginationConfigAlias PaginationConfig

// UnmarshalYAML records key presence before decoding the block. Decoding goes
// through an alias so yaml.v3 does not call this method recursively.
func (p *PaginationConfig) UnmarshalYAML(value *yaml.Node) error {
	return unmarshalYAMLWithPresence(value, (*paginationConfigAlias)(p), "PaginationConfig",
		[]string{"default_page_size", "max_page_size"},
		map[string]func(){
			"default_page_size": func() { p.defaultPageSizeSet = true },
			"max_page_size":     func() { p.maxPageSizeSet = true },
		})
}

// MultiTenancyConfig holds multi-tenancy settings
type MultiTenancyConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Mode    string `yaml:"mode" json:"mode,omitempty"`
}

// RawEntity represents an unprocessed entity from YAML
type RawEntity struct {
	OldName     string            `yaml:"old_name" json:"old_name,omitempty"` // hint for the migration engine: previous entity name (rename detection)
	Features    []string          `yaml:"features" json:"features,omitempty"`
	Fields      map[string]string `yaml:"fields" json:"fields"`
	FieldOrder  []string          `yaml:"-" json:"fieldOrder"` // preserved from YAML — not a yaml tag
	Indexes     []RawIndex        `yaml:"indexes" json:"indexes,omitempty"`
	Permissions *RawPermissions   `yaml:"permissions" json:"permissions,omitempty"`
	Seed        []map[string]any  `yaml:"seed" json:"seed,omitempty"`
}

// RawPermissions represents entity permissions before parsing.
type RawPermissions struct {
	Read   []string `yaml:"read" json:"read,omitempty"`
	Create []string `yaml:"create" json:"create,omitempty"`
	Update []string `yaml:"update" json:"update,omitempty"`
	Delete []string `yaml:"delete" json:"delete,omitempty"`
}

// UnmarshalYAML rejects unknown permission keys instead of silently ignoring them.
func (p *RawPermissions) UnmarshalYAML(value *yaml.Node) error {
	// Unknown keys become errors, including ones smuggled through a YAML merge.
	reject := func(key string, _ int) error {
		return fmt.Errorf("unknown permission key %q; valid keys: %s", key, strings.Join(specmeta.PermissionKeys, ", "))
	}
	return decodeStrict(value, (*rawPermissionsAlias)(p), specmeta.PermissionKeys, reject)
}

type rawPermissionsAlias RawPermissions

// UnmarshalYAML preserves field order from the YAML mapping node.
func (e *RawEntity) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		// Fallback for non-mapping nodes
		type rawEntityAlias RawEntity
		return value.Decode((*rawEntityAlias)(e))
	}

	// Extract ordered keys and decode fields in order
	for i := 0; i < len(value.Content)-1; i += 2 {
		keyNode := value.Content[i]
		valNode := value.Content[i+1]
		key := keyNode.Value

		switch key {
		case "old_name":
			if err := valNode.Decode(&e.OldName); err != nil {
				return err
			}
		case "features":
			if err := valNode.Decode(&e.Features); err != nil {
				return err
			}
		case "fields":
			if valNode.Kind != yaml.MappingNode {
				return fmt.Errorf("line %d: fields must be a mapping of field name to definition", valNode.Line)
			}
			// `fields` stays a mapping of field name → definition string. An
			// unquoted `<<` merges fields from an alias to a mapping or from a
			// sequence of such mappings/aliases; a quoted `"<<"` is an ordinary
			// field name and is not a merge.
			//
			// Ordering rule: merged fields come first, in merge order (for a
			// sequence the earlier entry wins on duplicate names), followed by
			// the entity's own declared fields. A direct field overrides a
			// merged definition but keeps the merged position, so generated
			// field order stays stable when a definition is customized locally.
			fields := make(map[string]string)
			order := make([]string, 0, len(valNode.Content)/2)
			merged := make(map[string]bool)
			addField := func(name string, defNode *yaml.Node, fromMerge bool) error {
				// A plain string scalar is already decoded in Node.Value. Going
				// through Node.Decode allocates a fresh decoder per field.
				var def string
				if defNode.Kind == yaml.ScalarNode && defNode.Tag == "!!str" {
					def = defNode.Value
				} else if err := defNode.Decode(&def); err != nil {
					return fmt.Errorf("field %q: %w", name, err)
				}
				if _, exists := fields[name]; exists {
					if fromMerge {
						return nil // first occurrence wins
					}
					if !merged[name] {
						return fmt.Errorf("duplicate field %q in entity fields", name)
					}
					fields[name] = def // explicit field wins, position is unchanged
					merged[name] = false
					return nil
				}
				fields[name] = def
				merged[name] = fromMerge
				order = append(order, name)
				return nil
			}
			// Two passes: merges always contribute their fields before the
			// entity's own fields, regardless of where `<<` appears in YAML.
			for j := 0; j < len(valNode.Content)-1; j += 2 {
				key, val := valNode.Content[j], valNode.Content[j+1]
				if key.Value != "<<" || key.Tag == "!!str" {
					continue
				}
				pairs, err := yamlMergeFieldPairs(val, key)
				if err != nil {
					return err
				}
				for _, pair := range pairs {
					if err := addField(pair.name, pair.value, true); err != nil {
						return err
					}
				}
			}
			for j := 0; j < len(valNode.Content)-1; j += 2 {
				key, val := valNode.Content[j], valNode.Content[j+1]
				if key.Value == "<<" && key.Tag != "!!str" {
					continue
				}
				if err := addField(key.Value, val, false); err != nil {
					return err
				}
			}
			e.Fields = fields
			e.FieldOrder = order
		case "indexes":
			if err := valNode.Decode(&e.Indexes); err != nil {
				return err
			}
		case "permissions":
			if err := valNode.Decode(&e.Permissions); err != nil {
				return err
			}
		case "seed":
			if err := valNode.Decode(&e.Seed); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown entity key %q; valid keys: old_name, features, fields, indexes, permissions, seed", key)
		}
	}

	// Ensure non-nil slices
	if e.FieldOrder == nil {
		e.FieldOrder = []string{}
	}

	return nil
}

// RawIndex represents an index definition
type RawIndex struct {
	Fields []string `yaml:"fields" json:"fields"`
	Type   string   `yaml:"type" json:"type,omitempty"`
	Sort   []string `yaml:"sort" json:"sort,omitempty"`
	Unique bool     `yaml:"unique" json:"unique,omitempty"`
}

// UnmarshalYAML rejects unknown index keys instead of silently ignoring them.
func (r *RawIndex) UnmarshalYAML(value *yaml.Node) error {
	reject := func(key string, _ int) error {
		return fmt.Errorf("unknown index key %q; valid keys: %s", key, strings.Join(specmeta.IndexKeys, ", "))
	}
	return decodeStrict(value, (*rawIndexAlias)(r), specmeta.IndexKeys, reject)
}

type rawIndexAlias RawIndex

// ParseRawSchema reads YAML bytes and converts them to RawSchema.
func ParseRawSchema(data []byte) (*RawSchema, error) {
	return parseRawSchema(bytes.NewReader(data))
}

// ParseRawSchemaString decodes YAML text without copying it into a []byte.
// WASM exports receive a Go string from JS; copying multi-hundred-kilobyte
// models on every keystroke showed up as a measurable share of total bytes.
func ParseRawSchemaString(data string) (*RawSchema, error) {
	return parseRawSchema(strings.NewReader(data))
}

func parseRawSchema(reader io.Reader) (*RawSchema, error) {
	schema := &RawSchema{}
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	if err := decoder.Decode(schema); err != nil {
		return nil, err
	}

	// Set defaults
	if schema.Database == "" {
		schema.Database = specmeta.DefaultDatabase
	}
	if schema.Auth.Type == "" {
		schema.Auth.Type = specmeta.AuthTypeNone
	}
	if schema.APIStyle == "" {
		schema.APIStyle = specmeta.DefaultAPIStyle
	}
	// Deploy defaults — consistent with parser setting other defaults.
	if schema.Project.Deploy == nil {
		schema.Project.Deploy = &DeployConfig{}
	}
	if !schema.Project.Deploy.portSet {
		schema.Project.Deploy.Port = 8080
	}
	if schema.Project.Deploy.Domain == "" {
		schema.Project.Deploy.Domain = "localhost"
	}

	// Versioning defaults — enabled with default version "1.0".
	if schema.Project.Versioning == nil {
		schema.Project.Versioning = &VersioningConfig{Enabled: true}
	}
	if schema.Project.Versioning.DefaultVersion == "" {
		schema.Project.Versioning.DefaultVersion = "1.0"
	}

	// Rate limit defaults — enabled, fixed window, 100 req / 60s.
	if schema.Project.RateLimit == nil {
		schema.Project.RateLimit = &RateLimitConfig{Enabled: true}
	}
	if schema.Project.RateLimit.Policy == "" {
		schema.Project.RateLimit.Policy = "fixed"
	}
	if !schema.Project.RateLimit.permitLimitSet {
		schema.Project.RateLimit.PermitLimit = 100
	}
	if !schema.Project.RateLimit.windowSecondsSet {
		schema.Project.RateLimit.WindowSeconds = 60
	}

	// Pagination defaults — 20 per page, capped at 200.
	if schema.Project.Pagination == nil {
		schema.Project.Pagination = &PaginationConfig{}
	}
	if !schema.Project.Pagination.defaultPageSizeSet {
		schema.Project.Pagination.DefaultPageSize = 20
	}
	if !schema.Project.Pagination.maxPageSizeSet {
		schema.Project.Pagination.MaxPageSize = 200
	}

	return schema, nil
}
