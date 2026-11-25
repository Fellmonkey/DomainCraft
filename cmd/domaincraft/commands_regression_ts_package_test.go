package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Regression: a project name with spaces used to land in package.json raw.
func TestGeneratedTSPackageNameIsKebabCase(t *testing.T) {
	bridgeDir, err := filepath.Abs(filepath.Join("..", "..", "bridges", "typescript", "react"))
	if err != nil {
		t.Fatal(err)
	}
	const domain = `project:
  name: Test Shop
database: postgresql
entities:
  Product:
    fields:
      id: uuid [primary]
      title: string [required]
`
	output := t.TempDir()
	generateFixture(t, domain, bridgeDir, output)
	data, err := os.ReadFile(filepath.Join(output, "package.json"))
	if err != nil {
		t.Fatalf("read package.json: %v", err)
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "test-shop-frontend" {
		t.Fatalf("package name = %q, want test-shop-frontend", pkg.Name)
	}
}
