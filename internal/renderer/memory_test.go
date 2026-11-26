package renderer

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/parser"
)

func memoryTestProject(t testing.TB) *ir.Project {
	t.Helper()
	schema, err := parser.ParseYAML([]byte(`project:
  name: Shop
database: postgresql
entities:
  User:
    fields:
      id: uuid [primary]
      email: string [required, unique, email]
      password: string [required, hidden]
  Product:
    fields:
      id: uuid [primary]
      title: string [required, max:255]
auth:
  type: jwt
  roles: [Admin, User]
  entity: User
`))
	if err != nil {
		t.Fatal(err)
	}
	project, err := ir.Build(schema)
	if err != nil {
		t.Fatal(err)
	}
	return project
}

func TestRenderMemoryMatchesDisk(t *testing.T) {
	project := memoryTestProject(t)
	fsys := os.DirFS(filepath.Join("..", "..", "bridges"))

	memory, err := NewFromFS(fsys, []string{"admin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	files, err := memory.RenderMemory(project, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 4 {
		t.Fatalf("RenderMemory returned %d files, want at least 4", len(files))
	}

	disk, err := New(filepath.Join("..", "..", "bridges", "admin"), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	_, _, err = disk.Render(project, out)
	if err != nil {
		t.Fatal(err)
	}

	for _, file := range files {
		got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Errorf("disk render missing %s: %v", file.Path, err)
			continue
		}
		if string(got) != file.Content {
			t.Errorf("%s differs: memory %d bytes, disk %d bytes", file.Path, len(file.Content), len(got))
		}
	}
}

func TestRenderMemoryComposesBaseFirst(t *testing.T) {
	project := memoryTestProject(t)
	fsys := os.DirFS(filepath.Join("..", "..", "bridges"))

	// react-rest extends ts-client extends ts-core. All three manifests must
	// load from the embedded-style tree without cloning anything.
	r, err := NewFromFS(fsys, []string{"typescript/core", "typescript/client", "typescript/react"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	files, err := r.RenderMemory(project, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("RenderMemory returned no files for the TS chain")
	}
	seen := make(map[string]bool)
	for _, f := range files {
		seen[f.Path] = true
		if f.Content == "" {
			t.Errorf("empty generated file %s", f.Path)
		}
	}
	if !seen["package.json"] {
		t.Errorf("adapter did not render package.json: %v", seen)
	}
}

func TestRenderMemoryRejectsEscapingTarget(t *testing.T) {
	fsys := fstest.MapFS{
		"bridge/bridge.yaml": &fstest.MapFile{Data: []byte(`name: evil
contract_version: 2
templates:
  - for: project
    source: templates/evil.tmpl
    target: "../evil.txt"
`)},
		"bridge/templates/evil.tmpl": &fstest.MapFile{Data: []byte("bad")},
	}
	r, err := NewFromFS(fsys, []string{"bridge"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RenderMemory(memoryTestProject(t), nil); err == nil {
		t.Fatal("RenderMemory accepted a target outside the project root")
	}
}
