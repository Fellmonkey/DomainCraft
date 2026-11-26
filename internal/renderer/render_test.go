package renderer

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/ir"
)

func TestRenderEntityTemplate(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	bridgeDir := writeBridge(t, map[string]string{
		"bridge.yaml": `name: demo
output_dir: generated
templates:
  - for: entity
    source: templates/entity.tmpl
    targets:
      - "{{ .Entity.Name }}.txt"
      - "nested/{{ .Entity.Name }}.txt"
`,
		"templates/entity.tmpl": `{{ .Entity.Name }} -> {{ .Project.Name }}`,
	})

	r, err := New(bridgeDir, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	project := &ir.Project{
		Name:     "TestProject",
		Entities: []ir.Entity{{Name: "User", NamePlural: "Users"}},
	}
	written, _, err := r.Render(project, filepath.Join(tmpDir, "out"))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("got %d files, want 2", len(written))
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "out", "User.txt")); err != nil {
		t.Fatalf("expected generated file User.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "out", "nested", "User.txt")); err != nil {
		t.Fatalf("expected generated file nested/User.txt: %v", err)
	}
}

// TestRenderTargetPathsUsePerEntityContext guards the target-template fast
// path: one parsed pattern must still be executed with each entity's context.
func TestRenderTargetPathsUsePerEntityContext(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	bridgeDir := writeBridge(t, map[string]string{
		"bridge.yaml": `name: demo
output_dir: generated
templates:
  - for: entity
    source: templates/entity.tmpl
    targets:
      - "{{ .Entity.Name }}.txt"
      - "nested/{{ lower .Entity.Name }}.txt"
`,
		"templates/entity.tmpl": `{{ .Entity.Name }}`,
	})

	r, err := New(bridgeDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	project := &ir.Project{
		Name:     "TestProject",
		Entities: []ir.Entity{{Name: "User"}, {Name: "Item"}},
	}
	written, _, err := r.Render(project, filepath.Join(tmpDir, "out"))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(written) != 4 {
		t.Fatalf("got %d files, want 4", len(written))
	}
	for _, name := range []string{"User.txt", "Item.txt", "nested/user.txt", "nested/item.txt"} {
		if _, err := os.Stat(filepath.Join(tmpDir, "out", name)); err != nil {
			t.Errorf("expected generated file %s: %v", name, err)
		}
	}
}

func TestComposeAttachBases(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	write := func(dir, name, content string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s/%s: %v", dir, name, err)
		}
	}

	// Base bridge: provides a helper (named template) + a type mapping + a template.
	baseDir := filepath.Join(tmpDir, "base")
	write(baseDir, "bridge.yaml", `name: base
helpers: helpers.tmpl
templates:
  - for: project
    source: templates/base.tmpl
    target: "base.txt"
`)
	write(baseDir, "helpers.tmpl", `{{ define "shout" }}{{ upper . }}{{ end }}`)
	write(filepath.Join(baseDir, "templates"), "base.tmpl", `base-{{ .Project.Name }}`)
	write(baseDir, "type_mappings.yaml", "types:\n  string: str\n")

	// Adapter bridge: extends the base, uses the base's helper and type mapping.
	adapterDir := filepath.Join(tmpDir, "adapter")
	write(adapterDir, "bridge.yaml", `name: adapter
templates:
  - for: project
    source: templates/adapter.tmpl
    target: "adapter.txt"
`)
	write(filepath.Join(adapterDir, "templates"), "adapter.tmpl", `{{ template "shout" "hi" }}/{{ languageType "string" false }}`)

	r, err := New(adapterDir, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := r.AttachBases([]string{baseDir}); err != nil {
		t.Fatalf("AttachBases() error = %v", err)
	}

	project := &ir.Project{Name: "TestProject"}
	written, _, err := r.Render(project, filepath.Join(tmpDir, "out"))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("got %d files, want 2 (base + adapter)", len(written))
	}

	baseOut, err := os.ReadFile(filepath.Join(tmpDir, "out", "base.txt"))
	if err != nil {
		t.Fatalf("read base.txt: %v", err)
	}
	if got, want := string(baseOut), "base-TestProject"; got != want {
		t.Errorf("base.txt = %q, want %q", got, want)
	}

	adapterOut, err := os.ReadFile(filepath.Join(tmpDir, "out", "adapter.txt"))
	if err != nil {
		t.Fatalf("read adapter.txt: %v", err)
	}
	if got, want := string(adapterOut), "HI/str"; got != want {
		t.Errorf("adapter.txt = %q, want %q (base helper + base type mapping must be visible to the adapter)", got, want)
	}
}

func TestMergeTypeMappingsOverlay(t *testing.T) {
	t.Parallel()
	base := &typeMappings{
		Types:       map[string]string{"string": "str", "int": "num"},
		ArrayFormat: "%s[]",
		ValueTypes:  []string{"str", "num"},
	}
	overlay := &typeMappings{
		Types:          map[string]string{"string": "text"},
		NullableFormat: "?",
	}

	got := mergeTypeMappings(base, overlay)

	if got.Types["string"] != "text" {
		t.Errorf("Types[string] = %q, want %q (overlay wins)", got.Types["string"], "text")
	}
	if got.Types["int"] != "num" {
		t.Errorf("Types[int] = %q, want %q (base preserved)", got.Types["int"], "num")
	}
	if got.ArrayFormat != "%s[]" {
		t.Errorf("ArrayFormat = %q, want %q (base scalar preserved)", got.ArrayFormat, "%s[]")
	}
	if got.NullableFormat != "?" {
		t.Errorf("NullableFormat = %q, want %q (overlay scalar)", got.NullableFormat, "?")
	}
	if len(got.ValueTypes) != 2 || got.ValueTypes[0] != "str" || got.ValueTypes[1] != "num" {
		t.Errorf("ValueTypes = %v, want [str num] (base preserved)", got.ValueTypes)
	}
}

func TestRenderOverwriteFalseScaffoldsOnce(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	bridgeDir := filepath.Join(tmpDir, "bridge")
	if err := os.MkdirAll(filepath.Join(bridgeDir, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir bridge: %v", err)
	}

	bridgeYAML := []byte(`name: demo
output_dir: generated
templates:
  - for: entity
    source: templates/entity.tmpl
    target: "{{ .Entity.Name }}.cs"
    overwrite: true
  - for: entity
    source: templates/service.tmpl
    target: "{{ .Entity.Name }}Service.cs"
    overwrite: false
`)
	templateBytes := []byte(`{{ .Entity.Name }}`)
	if err := os.WriteFile(filepath.Join(bridgeDir, "bridge.yaml"), bridgeYAML, 0o644); err != nil {
		t.Fatalf("write bridge: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bridgeDir, "templates", "entity.tmpl"), templateBytes, 0o644); err != nil {
		t.Fatalf("write entity template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bridgeDir, "templates", "service.tmpl"), templateBytes, 0o644); err != nil {
		t.Fatalf("write service template: %v", err)
	}

	r, err := New(bridgeDir, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	project := &ir.Project{
		Name:     "TestProject",
		Entities: []ir.Entity{{Name: "User", NamePlural: "Users"}},
	}
	outDir := filepath.Join(tmpDir, "out")

	// First run: both files are written.
	written, manifest, err := r.Render(project, outDir)
	if err != nil {
		t.Fatalf("Render() 1 error = %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("run 1: got %d written files, want 2", len(written))
	}
	if len(manifest) != 2 {
		t.Fatalf("run 1: got %d manifest entries, want 2", len(manifest))
	}

	// Second run: overwrite:false file is skipped but still in the manifest.
	written, manifest, err = r.Render(project, outDir)
	if err != nil {
		t.Fatalf("Render() 2 error = %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("run 2: got %d written files, want 1 (custom file skipped)", len(written))
	}
	var custom *RenderedFile
	for i := range manifest {
		if manifest[i].Custom {
			custom = &manifest[i]
		}
	}
	if custom == nil {
		t.Fatal("expected a custom file in the manifest")
	}
	if custom.Written {
		t.Error("custom file should be marked Written=false on the second run")
	}
	if custom.Path != "UserService.cs" {
		t.Errorf("custom path = %q, want UserService.cs", custom.Path)
	}
}

func TestRenderContext_HasAudit(t *testing.T) {
	t.Parallel()
	ctx := RenderContext{
		Entity: &ir.Entity{Features: map[string]bool{"audit": true}},
	}
	if !ctx.HasAudit() {
		t.Error("HasAudit() = false, want true")
	}

	ctx2 := RenderContext{Entity: nil}
	if ctx2.HasAudit() {
		t.Error("HasAudit() = true on nil entity, want false")
	}
}

func TestRenderContext_HasSoftDelete(t *testing.T) {
	t.Parallel()
	ctx := RenderContext{
		Entity: &ir.Entity{Features: map[string]bool{"soft_delete": true}},
	}
	if !ctx.HasSoftDelete() {
		t.Error("HasSoftDelete() = false, want true")
	}
}

func TestRenderContext_HasOptimisticLock(t *testing.T) {
	t.Parallel()
	ctx := RenderContext{
		Entity: &ir.Entity{Features: map[string]bool{"optimistic_lock": true}},
	}
	if !ctx.HasOptimisticLock() {
		t.Error("HasOptimisticLock() = false, want true")
	}
}

func TestRenderContext_HasAuditLog(t *testing.T) {
	t.Parallel()
	ctx := RenderContext{
		Entity: &ir.Entity{Features: map[string]bool{"audit_log": true}},
	}
	if !ctx.HasAuditLog() {
		t.Error("HasAuditLog() = false, want true")
	}
}

func TestRenderContext_Permissions(t *testing.T) {
	t.Parallel()
	ctx := RenderContext{Entity: nil}
	if ctx.Permissions() != nil {
		t.Error("Permissions() should return nil for nil entity")
	}

	perms := &ir.Permissions{Read: []string{"Admin"}}
	ctx2 := RenderContext{
		Entity: &ir.Entity{Permissions: perms},
	}
	if ctx2.Permissions() != perms {
		t.Error("Permissions() should return entity permissions")
	}
}

func TestRenderContext_PrimaryKey(t *testing.T) {
	t.Parallel()
	ctx := RenderContext{Entity: nil}
	if ctx.PrimaryKey() != nil {
		t.Error("PrimaryKey() should return nil for nil entity")
	}

	ctx2 := RenderContext{
		Entity: &ir.Entity{
			Fields: []ir.Field{
				{Name: "id", IsPrimary: true},
				{Name: "name"},
			},
		},
	}
	pk := ctx2.PrimaryKey()
	if pk == nil || pk.Name != "id" {
		t.Errorf("PrimaryKey() = %v, want field 'id'", pk)
	}
}

// The core must not bake in a language-specific TFM: the project declares its
// platform, otherwise the bridge chain declares it in bridge.yaml.
func TestResolvePackagesHasNoLanguageSpecificFrameworkDefault(t *testing.T) {
	t.Parallel()
	r := &Renderer{}
	if got := r.targetFramework(nil); got != "" {
		t.Errorf("targetFramework(nil) = %q, want empty (no core-side default)", got)
	}
	if got := r.targetFramework(&ir.Project{}); got != "" {
		t.Errorf("targetFramework(empty project) = %q, want empty (no core-side default)", got)
	}
	if got := r.targetFramework(&ir.Project{Platform: "net11.0"}); got != "net11.0" {
		t.Errorf("targetFramework(net11.0) = %q, want the declared platform", got)
	}

	bridge := &Renderer{sources: []sourceConfig{
		{config: BridgeConfig{TargetFramework: "net10.0"}}, // base
		{config: BridgeConfig{}},                           // adapter, silent
	}}
	if got := bridge.targetFramework(&ir.Project{}); got != "net10.0" {
		t.Errorf("targetFramework() = %q, want the bridge-declared net10.0", got)
	}
	// Project platform wins over the bridge declaration.
	if got := bridge.targetFramework(&ir.Project{Platform: "net11.0"}); got != "net11.0" {
		t.Errorf("targetFramework() = %q, want the project platform", got)
	}
}

func TestResolvePackagesNilLoggerDoesNotPanic(t *testing.T) {
	t.Parallel()
	// A nil logger is valid (silent mode). A failed registry lookup must not
	// panic or abort the render. Drive this through the public New()/Render()
	// API so the test covers the same path a CLI run takes.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	tmp := t.TempDir()
	bridgeDir := filepath.Join(tmp, "bridge")
	if err := os.MkdirAll(filepath.Join(bridgeDir, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir bridge: %v", err)
	}
	bridgeYAML := fmt.Sprintf(`name: demo
registry_url: %s/{id}/index.json
registry_packages:
  jwt_bearer: Microsoft.AspNetCore.Authentication.JwtBearer
templates:
  - for: project
    source: templates/pkg.tmpl
    target: pkg.txt
`, server.URL)
	if err := os.WriteFile(filepath.Join(bridgeDir, "bridge.yaml"), []byte(bridgeYAML), 0o644); err != nil {
		t.Fatalf("write bridge: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bridgeDir, "templates", "pkg.tmpl"), []byte(`{{ printf "%v" .Packages }}`), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	r, err := New(bridgeDir, nil) // nil logger is intentional
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	written, _, err := r.Render(&ir.Project{Name: "P"}, filepath.Join(tmp, "out"))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("got %d written files, want 1", len(written))
	}
	data, err := os.ReadFile(filepath.Join(tmp, "out", "pkg.txt"))
	if err != nil {
		t.Fatalf("read pkg.txt: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "map[]" {
		t.Fatalf("Packages = %q, want %q (failed resolution yields no versions)", got, "map[]")
	}
}

func TestRenderHasMigrationCondition(t *testing.T) {
	t.Parallel()
	newRenderer := func(t *testing.T, when string) *Renderer {
		t.Helper()
		tmpDir := t.TempDir()
		bridgeDir := filepath.Join(tmpDir, "bridge")
		if err := os.MkdirAll(filepath.Join(bridgeDir, "templates"), 0o755); err != nil {
			t.Fatalf("mkdir bridge: %v", err)
		}
		if err := os.WriteFile(filepath.Join(bridgeDir, "bridge.yaml"), []byte(`name: demo
templates:
  - for: project
    source: templates/migration.cs.tmpl
    target: "migration.cs"
    when: `+when+`
`), 0o644); err != nil {
			t.Fatalf("write bridge: %v", err)
		}
		if err := os.WriteFile(filepath.Join(bridgeDir, "templates", "migration.cs.tmpl"), []byte(`migration`), 0o644); err != nil {
			t.Fatalf("write template: %v", err)
		}
		r, err := New(bridgeDir, nil)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		return r
	}

	project := &ir.Project{Name: "P"}

	// No migration plan set -> hasMigration is false, no file written.
	r := newRenderer(t, "hasMigration")
	written, _, err := r.Render(project, filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(written) != 0 {
		t.Fatalf("no migration plan: want 0 files, got %d", len(written))
	}

	// A non-empty migration plan -> file is written.
	r2 := newRenderer(t, "hasMigration")
	r2.SetMigration(&ir.MigrationPlan{Operations: []ir.MigrationOp{{Kind: ir.OpCreateTable, Table: "orders"}}})
	written, _, err = r2.Render(project, filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("with migration plan: want 1 file, got %d", len(written))
	}
}

func TestRenderHasMockDataCondition(t *testing.T) {
	t.Parallel()
	newRenderer := func(t *testing.T) *Renderer {
		t.Helper()
		tmpDir := t.TempDir()
		bridgeDir := filepath.Join(tmpDir, "bridge")
		if err := os.MkdirAll(filepath.Join(bridgeDir, "templates"), 0o755); err != nil {
			t.Fatalf("mkdir bridge: %v", err)
		}
		if err := os.WriteFile(filepath.Join(bridgeDir, "bridge.yaml"), []byte(`name: demo
templates:
  - for: project
    source: templates/mock.cs.tmpl
    target: "mock.cs"
    when: hasMockData
`), 0o644); err != nil {
			t.Fatalf("write bridge: %v", err)
		}
		if err := os.WriteFile(filepath.Join(bridgeDir, "templates", "mock.cs.tmpl"), []byte(`mock`), 0o644); err != nil {
			t.Fatalf("write template: %v", err)
		}
		r, err := New(bridgeDir, nil)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		return r
	}

	project := &ir.Project{Name: "P", Entities: []ir.Entity{{Name: "Order", NamePlural: "Orders"}}}

	// No mock data -> hasMockData is false.
	r := newRenderer(t)
	written, _, err := r.Render(project, filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(written) != 0 {
		t.Fatalf("no mock data: want 0 files, got %d", len(written))
	}

	// With mock data -> file is written.
	r2 := newRenderer(t)
	r2.SetMockData(&ir.SeedDataset{Entities: []ir.SeedEntity{{Name: "Order", Records: []ir.SeedRecord{{}}}}})
	written, _, err = r2.Render(project, filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("with mock data: want 1 file, got %d", len(written))
	}
}

func TestRenderHasSeedCondition(t *testing.T) {
	t.Parallel()
	newRenderer := func(t *testing.T) *Renderer {
		t.Helper()
		tmpDir := t.TempDir()
		bridgeDir := filepath.Join(tmpDir, "bridge")
		if err := os.MkdirAll(filepath.Join(bridgeDir, "templates"), 0o755); err != nil {
			t.Fatalf("mkdir bridge: %v", err)
		}
		if err := os.WriteFile(filepath.Join(bridgeDir, "bridge.yaml"), []byte(`name: demo
templates:
  - for: project
    source: templates/seed.cs.tmpl
    target: "seed.cs"
    when: hasSeed
`), 0o644); err != nil {
			t.Fatalf("write bridge: %v", err)
		}
		if err := os.WriteFile(filepath.Join(bridgeDir, "templates", "seed.cs.tmpl"), []byte(`seed`), 0o644); err != nil {
			t.Fatalf("write template: %v", err)
		}
		r, err := New(bridgeDir, nil)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		return r
	}

	project := &ir.Project{Name: "P", Entities: []ir.Entity{{Name: "Order", NamePlural: "Orders"}}}

	// No seed -> hasSeed is false.
	r := newRenderer(t)
	written, _, err := r.Render(project, filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(written) != 0 {
		t.Fatalf("no seed: want 0 files, got %d", len(written))
	}

	// Normalized explicit seed -> file is written.
	r2 := newRenderer(t)
	r2.SetSeedData(&ir.SeedDataset{Entities: []ir.SeedEntity{{Name: "Order", Records: []ir.SeedRecord{{}}}}})
	written, _, err = r2.Render(project, filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("with seed data: want 1 file, got %d", len(written))
	}
}

func TestRenderContext_SeedAccessor(t *testing.T) {
	t.Parallel()
	ctx := RenderContext{
		Entity:   &ir.Entity{Name: "Order"},
		SeedData: &ir.SeedDataset{Entities: []ir.SeedEntity{{Name: "Order", Records: []ir.SeedRecord{{Fields: []ir.SeedValue{{Field: "id", Value: "x"}}}}}}},
	}
	got := ctx.Seed()
	if len(got) != 1 || len(got[0].Fields) != 1 || got[0].Fields[0].Value != "x" {
		t.Errorf("Seed() = %+v, want one normalized record", got)
	}
	if empty := (RenderContext{Entity: &ir.Entity{Name: "Order"}}).Seed(); empty != nil {
		t.Errorf("Seed() with no dataset should be nil, got %+v", empty)
	}
}

func TestRenderContext_DTOAccessors(t *testing.T) {
	t.Parallel()
	e := &ir.Entity{Name: "User", Fields: []ir.Field{
		{Name: "id", DatabaseType: "uuid", IsPrimary: true},
		{Name: "name", DatabaseType: "string"},
		{Name: "password", DatabaseType: "string"},
		{Name: "balance", DatabaseType: "decimal", IsReadonly: true},
		{Name: "createdAt", DatabaseType: "datetime"},
	}}
	ctx := RenderContext{Entity: e}

	if got := ctx.ReadFields(); len(got) != 4 {
		t.Errorf("ReadFields() = %d fields, want 4 (id, name, balance, createdAt)", len(got))
	}
	if got := ctx.CreateFields(); len(got) != 1 || got[0].Name != "name" {
		t.Errorf("CreateFields() = %+v, want only [name]", got)
	}
	if got := ctx.UpdateFields(); len(got) != 1 || got[0].Name != "name" {
		t.Errorf("UpdateFields() = %+v, want only [name] (no version token present)", got)
	}

	// Nil entity -> nil projections (never panics).
	nilCtx := RenderContext{Entity: nil}
	if nilCtx.ReadFields() != nil || nilCtx.CreateFields() != nil || nilCtx.UpdateFields() != nil {
		t.Error("DTO accessors should return nil for a nil entity")
	}
}
