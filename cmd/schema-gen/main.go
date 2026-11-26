// Package main implements schema-gen, which generates spec/domain.schema.json
// from specmeta.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DomainCraft/DomainCraft/internal/fsutil"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
)

func main() {
	outputPath := flag.String("o", filepath.Join("spec", "domain.schema.json"), "output path for the generated schema")
	flag.Parse()

	if err := fsutil.WriteJSONWithNewline(*outputPath, buildSchema(), true); err != nil {
		fmt.Fprintln(os.Stderr, "write schema:", err)
		os.Exit(1)
	}
}

// objectDef is the shape shared by every $defs entry: a closed object with
// named properties, an optional description and an optional required list.
func objectDef(title string, props map[string]any, required []string, description string) map[string]any {
	def := map[string]any{
		"title":                title,
		"type":                 "object",
		"additionalProperties": false,
		"properties":           props,
	}
	if description != "" {
		def["description"] = description
	}
	if len(required) > 0 {
		def["required"] = required
	}
	return def
}

func buildSchema() map[string]any {
	return map[string]any{
		"$schema":              "http://json-schema.org/draft-07/schema#",
		"$id":                  "domain.schema.json",
		"title":                "DomainCraft domain.yaml schema",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project":   map[string]any{"$ref": "#/$defs/Project"},
			"database":  map[string]any{"type": "string", "enum": specmeta.Databases},
			"auth":      map[string]any{"$ref": "#/$defs/AuthConfig"},
			"api_style": map[string]any{"type": "string", "enum": specmeta.APIStyles},
			"enums": map[string]any{
				"type": "object",
				"additionalProperties": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			},
			"entities": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"$ref": "#/$defs/EntityDefinition"},
			},
		},
		"required": []string{"project", "entities"},
		"$defs":    schemaDefs(),
	}
}

func schemaDefs() map[string]any {
	return map[string]any{
		"Project": objectDef("Project", map[string]any{
			"name":        map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"version":     map[string]any{"type": "string"},
			"platform":    map[string]any{"type": "string", "description": "Target platform version (e.g. net10.0). Defaults to net10.0 for the csharp bridge."},
			"multi_tenancy": map[string]any{
				"$ref": "#/$defs/MultiTenancy",
			},
			"cache": map[string]any{
				"$ref": "#/$defs/CacheConfig",
			},
			"cors": map[string]any{
				"$ref": "#/$defs/CORSConfig",
			},
			"deploy": map[string]any{
				"$ref": "#/$defs/DeployConfig",
			},
			"versioning": map[string]any{
				"$ref": "#/$defs/VersioningConfig",
			},
			"rate_limit": map[string]any{
				"$ref": "#/$defs/RateLimitConfig",
			},
			"pagination": map[string]any{
				"$ref": "#/$defs/PaginationConfig",
			},
			"infrastructure": map[string]any{
				"$ref": "#/$defs/InfrastructureConfig",
			},
		}, []string{"name"}, ""),
		"CacheConfig": objectDef("CacheConfig", map[string]any{
			"enabled":           map[string]any{"type": "boolean"},
			"provider":          map[string]any{"type": "string", "enum": specmeta.CacheProviders, "description": "Cache provider"},
			"connection_string": map[string]any{"type": "string"},
			"ttl_seconds":       map[string]any{"type": "integer", "minimum": 0},
		}, []string{"enabled"}, ""),
		"CORSConfig": objectDef("CORSConfig", map[string]any{
			"enabled": map[string]any{"type": "boolean"},
			"origins": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		}, []string{"enabled"}, ""),
		"MultiTenancy": objectDef("MultiTenancy", map[string]any{
			"enabled": map[string]any{"type": "boolean"},
			"mode":    map[string]any{"type": "string", "enum": specmeta.MultiTenancyModes},
		}, []string{"enabled"}, ""),
		"DeployConfig": objectDef("DeployConfig", map[string]any{
			"domain": map[string]any{"type": "string", "description": "API domain (e.g. localhost, api.example.com)"},
			"port":   map[string]any{"type": "integer", "minimum": 0, "maximum": 65535, "description": "Exposed application port (default: 8080)"},
		}, nil, ""),
		"VersioningConfig": objectDef("VersioningConfig", map[string]any{
			"enabled":         map[string]any{"type": "boolean", "description": "Enable API versioning (default: true)"},
			"default_version": map[string]any{"type": "string", "description": "Default API version (default: 1.0)"},
		}, nil, ""),
		"RateLimitConfig": objectDef("RateLimitConfig", map[string]any{
			"enabled":        map[string]any{"type": "boolean", "description": "Enable request rate limiting (default: true)"},
			"policy":         map[string]any{"type": "string", "enum": specmeta.RateLimitPolicies, "description": "Limiter algorithm (default: fixed)"},
			"permit_limit":   map[string]any{"type": "integer", "minimum": 0, "description": "Max requests (default: 100)"},
			"window_seconds": map[string]any{"type": "integer", "minimum": 0, "description": "Window duration in seconds (default: 60)"},
		}, nil, ""),
		"PaginationConfig": objectDef("PaginationConfig", map[string]any{
			"default_page_size": map[string]any{"type": "integer", "minimum": 0, "description": "Default page size (default: 20)"},
			"max_page_size":     map[string]any{"type": "integer", "minimum": 0, "description": "Hard cap on page size (default: 200)"},
		}, nil, ""),
		"InfrastructureConfig": objectDef("InfrastructureConfig", map[string]any{
			"queue":   map[string]any{"type": "string", "enum": specmeta.InfraQueues, "description": "Message broker"},
			"cache":   map[string]any{"type": "string", "enum": specmeta.InfraCacheStores, "description": "Distributed cache store"},
			"secrets": map[string]any{"type": "string", "enum": specmeta.InfraSecretStores, "description": "Secrets store"},
			"storage": map[string]any{"type": "string", "enum": specmeta.InfraStores, "description": "Object/file storage"},
		}, nil, ""),
		"EntityDefinition": objectDef("EntityDefinition", map[string]any{
			"old_name": map[string]any{"type": "string", "description": "Previous entity name — a hint for the migration engine to detect renames"},
			"features": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string", "enum": specmeta.Features},
			},
			"fields": map[string]any{
				"type":                 "object",
				"description":          "Field definitions. Each value is a definition string: `type [modifiers]` (e.g. `string [required, max:255]`). Relations are fields, not a separate key: `relation(Target) [many]` for many-to-many, `relation(Target) [required, on_delete:cascade]`, `relation(Target) [optional, on_delete:set_null]`. `on_delete` accepts cascade|restrict|set_null|no_action. Flag modifiers: `required`, `optional`, `unique`, `hidden`, `readonly`, `primary`, `many`, `email`, `url`, `ipv4`. Key:value modifiers: `min:N`, `max:N`, `gte:N`, `lte:N`, `gt:N`, `lt:N`, `regex:\"...\"`, `default:...`, `old_name:<previousName>`. `old_name` is a rename hint for the migration engine: the field was previously named `<previousName>`, so bridges can generate a safe `RenameColumn` instead of a destructive drop + add. `hidden` excludes a field from API responses; `readonly` keeps it in responses but excludes it from create/update/patch requests (server-owned). Do not put a space after `:` inside the definition string (`default:5`, not `default: 5`); quoted string defaults are allowed (`default:\"pending\"`).",
				"additionalProperties": map[string]any{"type": "string"},
			},
			"indexes": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/$defs/IndexDefinition"},
			},
			"permissions": map[string]any{
				"$ref": "#/$defs/EntityPermissions",
			},
			"seed": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "object", "additionalProperties": true},
			},
		}, []string{"fields"}, "An entity. Valid keys: old_name, features, fields, indexes, permissions, seed. There is no `relations:` key — relations are declared as fields of type `relation(Target)` (see the fields description)."),
		"IndexDefinition": objectDef("IndexDefinition", map[string]any{
			"fields": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"type":   map[string]any{"type": "string", "enum": specmeta.IndexTypes},
			"sort":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"unique": map[string]any{"type": "boolean"},
		}, []string{"fields"}, ""),
		"EntityPermissions": objectDef("EntityPermissions", map[string]any{
			"read":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"create": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"update": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"delete": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}, nil, ""),
		"AuthConfig": objectDef("AuthConfig", map[string]any{
			"type":      map[string]any{"type": "string", "enum": specmeta.AuthTypes},
			"entity":    map[string]any{"type": "string", "description": "Entity with email+password fields (auto-detected if omitted)"},
			"roles":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"endpoints": map[string]any{"$ref": "#/$defs/AuthEndpoints"},
		}, []string{"type"}, ""),
		"AuthEndpoints": objectDef("AuthEndpoints", map[string]any{
			"login":    map[string]any{"type": "boolean", "description": "Generate login endpoint (default: true)"},
			"register": map[string]any{"type": "boolean", "description": "Generate register endpoint (default: true)"},
			"me":       map[string]any{"type": "boolean", "description": "Generate /me endpoint (default: true)"},
			"setup":    map[string]any{"type": "boolean", "description": "Generate /setup endpoint that bootstraps the first Admin (default: true)"},
		}, nil, ""),
	}
}
