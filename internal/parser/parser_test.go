package parser

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseStrictRejectsUnknownKeys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "unknown root key",
			yaml: `project:
  name: Test
database: postgresql
unknown_root: value
entities: {}
`,
			wantErr: "field unknown_root not found in type parser.RawSchema",
		},
		{
			name: "unknown entity key",
			yaml: `project:
  name: Test
entities:
  User:
    fields:
      id: uuid [primary]
    typo_key: value
`,
			wantErr: "unknown entity key",
		},
		{
			name: "unknown permission key",
			yaml: `project:
  name: Test
entities:
  User:
    fields:
      id: uuid [primary]
    permissions:
      read: ["*"]
      read_public: true
`,
			wantErr: "unknown permission key",
		},
		{
			name: "unknown index key",
			yaml: `project:
  name: Test
entities:
  User:
    fields:
      id: uuid [primary]
    indexes:
      - fields: [email]
        typo: true
`,
			wantErr: "unknown index key",
		},
		{
			name: "unknown project key",
			yaml: `project:
  name: Test
  typo_field: x
entities: {}
`,
			wantErr: "field typo_field not found in type parser.ProjectConfig",
		},
		{
			name: "unknown deploy key",
			yaml: `project:
  name: Test
  deploy:
    typo_field: 1
entities: {}
`,
			wantErr: "field typo_field not found in type parser.DeployConfig",
		},
		{
			name: "unknown rate limit key",
			yaml: `project:
  name: Test
  rate_limit:
    typo_field: 1
entities: {}
`,
			wantErr: "field typo_field not found in type parser.RateLimitConfig",
		},
		{
			name: "unknown pagination key",
			yaml: `project:
  name: Test
  pagination:
    typo_field: 1
entities: {}
`,
			wantErr: "field typo_field not found in type parser.PaginationConfig",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseRawSchema([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("ParseRawSchema() expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ParseRawSchema() error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestUnmarshalYAMLHelpersRejectUnknownKeysForAllConfigs pins the shared
// presence-aware UnmarshalYAML helper: every config block that opts into it
// must reject unknown keys with the same strict-decoding message, and must
// still accept its own keys (the latter is covered by the explicit-zero tests).
func TestUnmarshalYAMLHelpersRejectUnknownKeysForAllConfigs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		newValue func() any
		yaml     string
		wantErr  string
	}{
		{
			name:     "DeployConfig",
			newValue: func() any { return &DeployConfig{} },
			yaml:     "port: 8080\ntypo_field: 1\n",
			wantErr:  "field typo_field not found in type parser.DeployConfig",
		},
		{
			name:     "RateLimitConfig",
			newValue: func() any { return &RateLimitConfig{} },
			yaml:     "permit_limit: 100\nwindow_seconds: 60\ntypo_field: 1\n",
			wantErr:  "field typo_field not found in type parser.RateLimitConfig",
		},
		{
			name:     "PaginationConfig",
			newValue: func() any { return &PaginationConfig{} },
			yaml:     "default_page_size: 20\nmax_page_size: 200\ntypo_field: 1\n",
			wantErr:  "field typo_field not found in type parser.PaginationConfig",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := yaml.Unmarshal([]byte(tt.yaml), tt.newValue())
			if err == nil {
				t.Fatalf("yaml.Unmarshal() expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("yaml.Unmarshal() error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestParseRawSchema(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test Project
database: postgresql
auth:
  type: jwt
api_style: rest
entities:
  User:
    fields:
      id: uuid [primary]
      name: string
`)

	schema, err := ParseRawSchema(yamlData)
	if err != nil {
		t.Fatalf("ParseRawSchema() error = %v", err)
	}

	if schema.Project.Name != "Test Project" {
		t.Errorf("got project name %v, want Test Project", schema.Project.Name)
	}
	if schema.Database != "postgresql" {
		t.Errorf("got database %v, want postgresql", schema.Database)
	}
	if schema.Auth.Type != "jwt" {
		t.Errorf("got auth.type %v, want jwt", schema.Auth.Type)
	}
}

func TestParseDefaults(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities: {}
`)

	schema, err := ParseRawSchema(yamlData)
	if err != nil {
		t.Fatalf("ParseRawSchema() error = %v", err)
	}

	if schema.Database != "postgresql" {
		t.Errorf("got default database %v, want postgresql", schema.Database)
	}
	if schema.Auth.Type != "none" {
		t.Errorf("got default auth.type %v, want none", schema.Auth.Type)
	}
	if schema.APIStyle != "rest" {
		t.Errorf("got default api_style %v, want rest", schema.APIStyle)
	}
}

func TestParseEntity(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
database: postgresql
entities:
  User:
    features: [audit]
    fields:
      id: uuid [primary]
      email: string [required, unique, email]
      name: string
    permissions:
      read: [Admin, "*"]
      create: ["*"]
`)

	parsed, err := ParseYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}

	user, ok := parsed.Entities["User"]
	if !ok {
		t.Fatalf("User entity not found")
	}

	if user.Name != "User" {
		t.Errorf("got name %v, want User", user.Name)
	}
	if user.NamePlural != "Users" {
		t.Errorf("got plural %v, want Users", user.NamePlural)
	}

	if len(user.Fields) != 5 {
		t.Errorf("got %d fields, want 5", len(user.Fields))
	}

	if _, ok := user.Fields["createdAt"]; !ok {
		t.Errorf("createdAt field not added by audit feature")
	}
	if _, ok := user.Fields["updatedAt"]; !ok {
		t.Errorf("updatedAt field not added by audit feature")
	}

	if user.Features["audit"] != true {
		t.Errorf("audit feature not set")
	}
}

func TestParseFeatures(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities:
  Document:
    features: [audit_log, soft_delete, optimistic_lock]
    fields:
      id: uuid [primary]
`)

	parsed, err := ParseYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}

	doc := parsed.Entities["Document"]

	// Verify that required fields were added
	expectedFields := []string{
		"id", "createdBy", "updatedBy", "deletedAt", "version",
	}

	for _, fieldName := range expectedFields {
		if _, ok := doc.Fields[fieldName]; !ok && fieldName != "id" {
			t.Errorf("expected field %v not found", fieldName)
		}
	}

	// Verify that deletedAt is optional
	if deletedAt, ok := doc.Fields["deletedAt"]; ok && !deletedAt.IsOptional {
		t.Errorf("deletedAt should be optional")
	}
}

func TestParseRelations(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities:
  Product:
    fields:
      id: uuid [primary]
      categoryId: relation(Category) [optional, on_delete:set_null]
      supplierId: relation(User)
`)

	parsed, err := ParseYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}

	product := parsed.Entities["Product"]

	// Check categoryID
	categoryID := product.Fields["categoryId"]
	if categoryID.Type != "relation" {
		t.Errorf("categoryID type should be relation")
	}
	if categoryID.TargetEntity != "Category" {
		t.Errorf("categoryID target should be Category")
	}
	if !categoryID.IsOptional {
		t.Errorf("categoryID should be optional")
	}
	if categoryID.OnDelete != "set_null" {
		t.Errorf("categoryID on_delete should be set_null")
	}

	// Check supplierID
	supplierID := product.Fields["supplierId"]
	if supplierID.TargetEntity != "User" {
		t.Errorf("supplierID target should be User")
	}
	if supplierID.IsOptional {
		t.Errorf("supplierID should not be optional")
	}
}

func TestParseIndexes(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities:
  Product:
    fields:
      id: uuid [primary]
      status: string
      createdAt: datetime
    indexes:
      - fields: [status, createdAt]
        type: btree
      - fields: [id]
        unique: true
`)

	parsed, err := ParseYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}

	product := parsed.Entities["Product"]
	if len(product.Indexes) != 2 {
		t.Errorf("got %d indexes, want 2", len(product.Indexes))
	}

	if product.Indexes[0].Type != "btree" {
		t.Errorf("first index type should be btree")
	}
	if len(product.Indexes[0].Fields) != 2 {
		t.Errorf("first index should have 2 fields")
	}
	if !product.Indexes[1].Unique {
		t.Errorf("second index should be unique")
	}
}

func TestPluralizeNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		singular string
		plural   string
	}{
		{"User", "Users"},
		{"Category", "Categories"},
		{"Status", "Statuses"},
		{"Box", "Boxes"},
		{"Lady", "Ladies"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v", tt), func(t *testing.T) {
			t.Parallel()
			// Test via ParseYAML since pluralize is an internal function
			yamlData := []byte("project:\n  name: Test\nentities:\n  " + tt.singular + ":\n    fields:\n      id: uuid [primary]")
			parsed, _ := ParseYAML(yamlData)
			entity := parsed.Entities[tt.singular]
			if entity.NamePlural != tt.plural {
				t.Errorf("pluralize(%s) = %s, want %s", tt.singular, entity.NamePlural, tt.plural)
			}
		})
	}
}

func TestDatabaseColumnNames(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities:
  User:
    fields:
      id: uuid [primary]
      firstName: string
      lastName: string
      createdAt: datetime
`)

	parsed, err := ParseYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}

	user := parsed.Entities["User"]

	tests := []struct {
		fieldName  string
		wantDbName string
	}{
		{"id", "id"},
		{"firstName", "first_name"},
		{"lastName", "last_name"},
		{"createdAt", "created_at"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v", tt), func(t *testing.T) {
			t.Parallel()
			if field, ok := user.Fields[tt.fieldName]; ok {
				if field.DatabaseColumnName != tt.wantDbName {
					t.Errorf("field %s: got db name %v, want %v", tt.fieldName, field.DatabaseColumnName, tt.wantDbName)
				}
			}
		})
	}
}

func TestParsePermissions(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities:
  Document:
    fields:
      id: uuid [primary]
    permissions:
      read: [Admin, "@Owner"]
      create: [User, Admin]
      update: ["@Owner"]
      delete: [Admin]
`)

	parsed, err := ParseYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}

	doc := parsed.Entities["Document"]
	if doc.Permissions == nil {
		t.Fatalf("Permissions should not be nil")
	}

	if len(doc.Permissions.Read) != 2 {
		t.Errorf("got %d read permissions, want 2", len(doc.Permissions.Read))
	}
}

func TestParsePrimaryImpliesRequired(t *testing.T) {
	t.Parallel()
	// Parser normalizes primary→required (lexer no longer does this).
	yamlData := []byte(`
project:
  name: Test
entities:
  User:
    fields:
      id: uuid [primary]
`)
	parsed, err := ParseYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}
	user := parsed.Entities["User"]
	id := user.Fields["id"]
	if !id.IsPrimary {
		t.Error("expected IsPrimary=true")
	}
	if !id.IsRequired {
		t.Error("parser should normalize primary→required")
	}
}

func TestParseOptionalImpliesNotRequired(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities:
  User:
    fields:
      id: uuid [primary]
      bio: string [optional]
`)
	parsed, err := ParseYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}
	user := parsed.Entities["User"]
	bio := user.Fields["bio"]
	if !bio.IsOptional {
		t.Error("expected IsOptional=true")
	}
	if bio.IsRequired {
		t.Error("parser should normalize optional→not required")
	}
}

func TestParseRelationTypeInference(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities:
  Order:
    fields:
      id: uuid [primary]
      userId: relation(User)
      tagId: relation(Tag) [unique]
      categoryIds: relation(Category) [many]
  User:
    fields:
      id: uuid [primary]
  Tag:
    fields:
      id: uuid [primary]
  Category:
    fields:
      id: uuid [primary]
`)
	parsed, err := ParseYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}
	order := parsed.Entities["Order"]

	tests := []struct {
		field    string
		wantType string
	}{
		{"userId", "many-to-one"},
		{"tagId", "one-to-one"},
		{"categoryIds", "many-to-many"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v", tt), func(t *testing.T) {
			t.Parallel()
			f := order.Fields[tt.field]
			if f.RelationType != tt.wantType {
				t.Errorf("field %s: RelationType = %q, want %q", tt.field, f.RelationType, tt.wantType)
			}
		})
	}
}

func TestParseDeployDefaults(t *testing.T) {
	t.Parallel()
	// Parser should set deploy defaults (port=8080, domain=localhost).
	yamlData := []byte(`
project:
  name: Test
entities:
  User:
    fields:
      id: uuid [primary]
`)
	parsed, err := ParseRawSchema(yamlData)
	if err != nil {
		t.Fatalf("ParseRawSchema() error = %v", err)
	}
	if parsed.Project.Deploy == nil {
		t.Fatal("Deploy config should not be nil (defaults should be set)")
	}
	if parsed.Project.Deploy.Port != 8080 {
		t.Errorf("Deploy.Port = %d, want 8080", parsed.Project.Deploy.Port)
	}
	if parsed.Project.Deploy.Domain != "localhost" {
		t.Errorf("Deploy.Domain = %q, want localhost", parsed.Project.Deploy.Domain)
	}
}

func TestParseDeployExplicitValues(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
  deploy:
    port: 3000
    domain: api.example.com
entities:
  User:
    fields:
      id: uuid [primary]
`)
	parsed, err := ParseRawSchema(yamlData)
	if err != nil {
		t.Fatalf("ParseRawSchema() error = %v", err)
	}
	if parsed.Project.Deploy.Port != 3000 {
		t.Errorf("Deploy.Port = %d, want 3000", parsed.Project.Deploy.Port)
	}
	if parsed.Project.Deploy.Domain != "api.example.com" {
		t.Errorf("Deploy.Domain = %q, want api.example.com", parsed.Project.Deploy.Domain)
	}
}

func TestParseVersioningRateLimitPaginationDefaults(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities:
  User:
    fields:
      id: uuid [primary]
`)
	parsed, err := ParseRawSchema(yamlData)
	if err != nil {
		t.Fatalf("ParseRawSchema() error = %v", err)
	}

	if parsed.Project.Versioning == nil || !parsed.Project.Versioning.Enabled {
		t.Fatal("Versioning should default to enabled")
	}
	if parsed.Project.Versioning.DefaultVersion != "1.0" {
		t.Errorf("Versioning.DefaultVersion = %q, want 1.0", parsed.Project.Versioning.DefaultVersion)
	}

	if parsed.Project.RateLimit == nil || !parsed.Project.RateLimit.Enabled {
		t.Fatal("RateLimit should default to enabled")
	}
	if parsed.Project.RateLimit.Policy != "fixed" {
		t.Errorf("RateLimit.Policy = %q, want fixed", parsed.Project.RateLimit.Policy)
	}
	if parsed.Project.RateLimit.PermitLimit != 100 {
		t.Errorf("RateLimit.PermitLimit = %d, want 100", parsed.Project.RateLimit.PermitLimit)
	}
	if parsed.Project.RateLimit.WindowSeconds != 60 {
		t.Errorf("RateLimit.WindowSeconds = %d, want 60", parsed.Project.RateLimit.WindowSeconds)
	}

	if parsed.Project.Pagination == nil {
		t.Fatal("Pagination should not be nil (defaults set)")
	}
	if parsed.Project.Pagination.DefaultPageSize != 20 {
		t.Errorf("Pagination.DefaultPageSize = %d, want 20", parsed.Project.Pagination.DefaultPageSize)
	}
	if parsed.Project.Pagination.MaxPageSize != 200 {
		t.Errorf("Pagination.MaxPageSize = %d, want 200", parsed.Project.Pagination.MaxPageSize)
	}
}

func TestParseVersioningRateLimitExplicit(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
  versioning:
    enabled: false
    default_version: "2.0"
  rate_limit:
    enabled: true
    policy: sliding
    permit_limit: 250
    window_seconds: 120
entities:
  User:
    fields:
      id: uuid [primary]
`)
	parsed, err := ParseRawSchema(yamlData)
	if err != nil {
		t.Fatalf("ParseRawSchema() error = %v", err)
	}
	if parsed.Project.Versioning.Enabled {
		t.Error("Versioning.Enabled = true, want false")
	}
	if parsed.Project.Versioning.DefaultVersion != "2.0" {
		t.Errorf("Versioning.DefaultVersion = %q, want 2.0", parsed.Project.Versioning.DefaultVersion)
	}
	if parsed.Project.RateLimit.Policy != "sliding" {
		t.Errorf("RateLimit.Policy = %q, want sliding", parsed.Project.RateLimit.Policy)
	}
	if parsed.Project.RateLimit.PermitLimit != 250 {
		t.Errorf("RateLimit.PermitLimit = %d, want 250", parsed.Project.RateLimit.PermitLimit)
	}
	if parsed.Project.RateLimit.WindowSeconds != 120 {
		t.Errorf("RateLimit.WindowSeconds = %d, want 120", parsed.Project.RateLimit.WindowSeconds)
	}
}

func TestParseUnknownEntityKey(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities:
  User:
    feilds:
      id: uuid [primary]
`)
	_, err := ParseYAML(yamlData)
	if err == nil {
		t.Fatal("expected error for unknown entity key 'feilds'")
	}
	if !strings.Contains(err.Error(), "unknown entity key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseOldName(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities:
  Item:
    old_name: Product
    fields:
      id: uuid [primary]
`)
	schema, err := ParseYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}
	entity := schema.Entities["Item"]
	if entity == nil {
		t.Fatal("expected entity Item")
	}
	if entity.OldName != "Product" {
		t.Errorf("OldName = %q, want Product", entity.OldName)
	}
}

func TestParseDuplicateField(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities:
  User:
    features: [audit]
    fields:
      id: uuid [primary]
      email: string [required, unique]
      isSystemTag: boolean [default:false]
      isSystemTag: boolean [default:true]
`)
	_, err := ParseYAML(yamlData)
	if err == nil {
		t.Fatal("expected error for duplicate field 'isSystemTag'")
	}
	if !strings.Contains(err.Error(), `duplicate field "isSystemTag"`) {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestParseDuplicateEntity verifies that a duplicate entity key in the
// `entities` mapping is rejected by the YAML decoder (gopkg.in/yaml.v3 reports
// "mapping key ... already defined") rather than silently overwriting the
// earlier definition. Unlike field keys (decoded by the custom RawEntity
// UnmarshalYAML, which used to drop duplicates into FieldOrder), entity keys
// are decoded into a plain map[string]RawEntity and the library enforces
// uniqueness itself — this test pins that contract so a future refactor of
// RawSchema decoding cannot silently reintroduce the overwrite-on-duplicate
// behaviour.
func TestParseDuplicateEntity(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities:
  User:
    features: [audit]
    fields:
      id: uuid [primary]
      email: string [required, unique]
      password: string [required, hidden]
  User:
    fields:
      id: int [primary]
      name: string [required]
`)
	_, err := ParseYAML(yamlData)
	if err == nil {
		t.Fatal("expected error for duplicate entity 'User'")
	}
	if !strings.Contains(err.Error(), `"User" already defined`) {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestParseDuplicateFeature pins that a feature listed twice is rejected at
// parse time — the Features map collapses duplicates, so a validator-side
// check could never see them.
func TestParseDuplicateFeature(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		features string
	}{
		{name: "adjacent", features: "[audit, audit]"},
		{name: "separated", features: "[audit, soft_delete, audit]"},
		{name: "whitespace", features: "[audit, ' audit ']"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			yamlData := []byte(`
project:
  name: Test
entities:
  User:
    features: ` + tt.features + `
    fields:
      id: uuid [primary]
      email: string [required, unique]
`)
			_, err := ParseYAML(yamlData)
			if err == nil {
				t.Fatal("expected error for duplicate feature")
			}
			if !strings.Contains(err.Error(), "duplicate feature: audit") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestParseRawSchemaExplicitZerosPreserved pins the contract that an explicit
// zero in domain.yaml survives parsing. The old defaults treated 0 as "unset",
// silently replacing permit_limit: 0 with 100 (and likewise for the other
// numeric project settings).
func TestParseRawSchemaExplicitZerosPreserved(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
  deploy:
    port: 0
  rate_limit:
    permit_limit: 0
    window_seconds: 0
  pagination:
    default_page_size: 0
    max_page_size: 0
entities: {}
`)

	parsed, err := ParseRawSchema(yamlData)
	if err != nil {
		t.Fatalf("ParseRawSchema() error = %v", err)
	}
	if parsed.Project.Deploy.Port != 0 {
		t.Errorf("Deploy.Port = %d, want explicit 0", parsed.Project.Deploy.Port)
	}
	if parsed.Project.RateLimit.PermitLimit != 0 {
		t.Errorf("RateLimit.PermitLimit = %d, want explicit 0", parsed.Project.RateLimit.PermitLimit)
	}
	if parsed.Project.RateLimit.WindowSeconds != 0 {
		t.Errorf("RateLimit.WindowSeconds = %d, want explicit 0", parsed.Project.RateLimit.WindowSeconds)
	}
	if parsed.Project.Pagination.DefaultPageSize != 0 {
		t.Errorf("Pagination.DefaultPageSize = %d, want explicit 0", parsed.Project.Pagination.DefaultPageSize)
	}
	if parsed.Project.Pagination.MaxPageSize != 0 {
		t.Errorf("Pagination.MaxPageSize = %d, want explicit 0", parsed.Project.Pagination.MaxPageSize)
	}
}

// TestParseYAMLPreservesExplicitZeros checks that ParseYAML (the entry point
// used by the CLI and WASM validator) carries the preserved zeros into the
// ParsedSchema the validator sees.
func TestParseYAMLPreservesExplicitZeros(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
  deploy:
    port: 0
  rate_limit:
    permit_limit: 0
    window_seconds: 0
  pagination:
    default_page_size: 0
    max_page_size: 0
entities:
  User:
    fields:
      id: uuid [primary]
`)

	parsed, err := ParseYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}
	if parsed.Project.Deploy.Port != 0 {
		t.Errorf("Deploy.Port = %d, want explicit 0", parsed.Project.Deploy.Port)
	}
	if parsed.Project.RateLimit.PermitLimit != 0 {
		t.Errorf("RateLimit.PermitLimit = %d, want explicit 0", parsed.Project.RateLimit.PermitLimit)
	}
	if parsed.Project.RateLimit.WindowSeconds != 0 {
		t.Errorf("RateLimit.WindowSeconds = %d, want explicit 0", parsed.Project.RateLimit.WindowSeconds)
	}
	if parsed.Project.Pagination.DefaultPageSize != 0 {
		t.Errorf("Pagination.DefaultPageSize = %d, want explicit 0", parsed.Project.Pagination.DefaultPageSize)
	}
	if parsed.Project.Pagination.MaxPageSize != 0 {
		t.Errorf("Pagination.MaxPageSize = %d, want explicit 0", parsed.Project.Pagination.MaxPageSize)
	}
}

// TestParseRawSchemaOmittedKeysUseDefaults pins the other half of the
// presence contract: a config block with omitted numeric keys still receives
// the documented defaults.
func TestParseRawSchemaOmittedKeysUseDefaults(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
  deploy: {}
  rate_limit: {}
  pagination: {}
entities: {}
`)

	parsed, err := ParseRawSchema(yamlData)
	if err != nil {
		t.Fatalf("ParseRawSchema() error = %v", err)
	}
	if parsed.Project.Deploy.Port != 8080 {
		t.Errorf("Deploy.Port = %d, want default 8080", parsed.Project.Deploy.Port)
	}
	if parsed.Project.RateLimit.PermitLimit != 100 {
		t.Errorf("RateLimit.PermitLimit = %d, want default 100", parsed.Project.RateLimit.PermitLimit)
	}
	if parsed.Project.RateLimit.WindowSeconds != 60 {
		t.Errorf("RateLimit.WindowSeconds = %d, want default 60", parsed.Project.RateLimit.WindowSeconds)
	}
	if parsed.Project.Pagination.DefaultPageSize != 20 {
		t.Errorf("Pagination.DefaultPageSize = %d, want default 20", parsed.Project.Pagination.DefaultPageSize)
	}
	if parsed.Project.Pagination.MaxPageSize != 200 {
		t.Errorf("Pagination.MaxPageSize = %d, want default 200", parsed.Project.Pagination.MaxPageSize)
	}
}

// TestParseRawSchemaPartialExplicitZeros checks that presence is tracked per
// key: an explicit zero does not suppress defaults for its siblings.
func TestParseRawSchemaPartialExplicitZeros(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
  deploy:
    port: 0
    domain: api.example.com
  rate_limit:
    permit_limit: 0
  pagination:
    max_page_size: 0
entities: {}
`)

	parsed, err := ParseRawSchema(yamlData)
	if err != nil {
		t.Fatalf("ParseRawSchema() error = %v", err)
	}
	if parsed.Project.Deploy.Port != 0 {
		t.Errorf("Deploy.Port = %d, want explicit 0", parsed.Project.Deploy.Port)
	}
	if parsed.Project.Deploy.Domain != "api.example.com" {
		t.Errorf("Deploy.Domain = %q, want api.example.com", parsed.Project.Deploy.Domain)
	}
	if parsed.Project.RateLimit.PermitLimit != 0 {
		t.Errorf("RateLimit.PermitLimit = %d, want explicit 0", parsed.Project.RateLimit.PermitLimit)
	}
	if parsed.Project.RateLimit.WindowSeconds != 60 {
		t.Errorf("RateLimit.WindowSeconds = %d, want default 60", parsed.Project.RateLimit.WindowSeconds)
	}
	if parsed.Project.Pagination.DefaultPageSize != 20 {
		t.Errorf("Pagination.DefaultPageSize = %d, want default 20", parsed.Project.Pagination.DefaultPageSize)
	}
	if parsed.Project.Pagination.MaxPageSize != 0 {
		t.Errorf("Pagination.MaxPageSize = %d, want explicit 0", parsed.Project.Pagination.MaxPageSize)
	}
}

// TestNavigationNameStripsIdAndIDSuffixOnly pins the FK convention shared with
// the validator: only Id and ID are stripped. Lowercase id is not an FK format,
// so categoryid keeps its final two letters in the navigation name.
func TestNavigationNameStripsIdAndIDSuffixOnly(t *testing.T) {
	t.Parallel()
	yamlData := []byte(`
project:
  name: Test
entities:
  Category:
    fields:
      id: uuid [primary]
  Order:
    fields:
      id: uuid [primary]
      categoryId: relation(Category)
      categoryID: relation(Category)
      categoryid: relation(Category)
`)
	parsed, err := ParseYAML(yamlData)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}
	order := parsed.Entities["Order"]
	if order == nil {
		t.Fatal("Order entity not parsed")
	}
	for _, tt := range []struct {
		fieldName string
		want      string
	}{
		{fieldName: "categoryId", want: "Category"},
		{fieldName: "categoryID", want: "Category"},
		{fieldName: "categoryid", want: "Categoryid"},
	} {
		field := order.Fields[tt.fieldName]
		if field == nil {
			t.Fatalf("field %q not parsed", tt.fieldName)
		}
		if got := field.NavigationName(); got != tt.want {
			t.Errorf("%s.NavigationName() = %q, want %q", tt.fieldName, got, tt.want)
		}
	}
}
