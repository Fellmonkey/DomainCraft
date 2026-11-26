package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression: a role declared as `string` used to render as `string.Admin`
// in generated C# tests, which does not compile.
func TestGeneratedCSharpTestsHandleStringRole(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"versions":["10.0.0"]}`))
	}))
	defer srv.Close()
	bridgeDir := copyCSharpBridges(t, srv.URL+"/{id}/index.json")

	const domain = `project:
  name: String Role Shop
database: postgresql
auth:
  type: jwt
  entity: User
  roles: [Admin, User]
entities:
  User:
    fields:
      id: uuid [primary]
      email: string [required, email]
      password: string [required, hidden]
      role: string [required, default:User]
  Product:
    fields:
      id: uuid [primary]
      title: string [required]
    permissions:
      read: [Admin]
      create: [Admin]
`
	output := t.TempDir()
	generateFixture(t, domain, bridgeDir, output)
	data, err := os.ReadFile(filepath.Join(output, "tests", "EntityStateTests.g.cs"))
	if err != nil {
		t.Fatalf("read generated tests: %v", err)
	}
	if strings.Contains(string(data), "string.Admin") {
		t.Fatal("generated C# tests still reference string.Admin")
	}
	if !strings.Contains(string(data), `await CreateUserWithRoleAndLoginAsync("Admin")`) {
		t.Fatal("generated C# tests do not use a string literal for the admin role")
	}
}
