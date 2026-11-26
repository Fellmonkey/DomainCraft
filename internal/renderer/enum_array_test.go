package renderer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/parser"
)

// TestTSCoreRendersEnumArrayType guards the bridge-owned array syntax. The core
// deliberately has no language-specific fallback: an enum array only renders as
// `PaymentMethod[]` when ts-core declares `array_format` in type_mappings.yaml.
// Without it the generated field is an empty expression (` | null`), which the
// TypeScript TCK catches at tck/type-contract.ts:70.
func TestTSCoreRendersEnumArrayType(t *testing.T) {
	t.Parallel()

	project := enumArrayProject(t)
	bridgeDir := filepath.Join("..", "..", "bridges", "typescript", "core")
	if _, err := os.Stat(filepath.Join(bridgeDir, "bridge.yaml")); err != nil {
		t.Fatalf("bundled ts-core bridge missing at %s: %v", bridgeDir, err)
	}

	got := renderTSCoreTypes(t, bridgeDir, project)
	const want = "supportedPaymentMethods: PaymentMethod[] | null;"
	if !strings.Contains(got, want) {
		t.Errorf("ts-core output has no %q line:\n%s", want, excerptAround(got, "supportedPaymentMethods"))
	}

	// Prove the assertion above is really driven by the bridge's array_format:
	// the same render against a copy with that single line removed must produce
	// the empty expression. This keeps the check self-contained — the repository
	// is never mutated — and fails if the core ever reintroduces a fallback.
	t.Run("without array format renders empty", func(t *testing.T) {
		withoutFormat := copyTSCoreWithoutArrayFormat(t, bridgeDir)
		got := renderTSCoreTypes(t, withoutFormat, project)
		const broken = "supportedPaymentMethods:  | null;"
		if !strings.Contains(got, broken) {
			t.Errorf("bridge without array_format should render %q, got:\n%s", broken, excerptAround(got, "supportedPaymentMethods"))
		}
		if strings.Contains(got, want) {
			t.Errorf("bridge without array_format still rendered %q (core fallback returned?):\n%s", want, excerptAround(got, "supportedPaymentMethods"))
		}
	})
}

func enumArrayProject(t *testing.T) *ir.Project {
	t.Helper()
	domain := []byte(`
project:
  name: EnumArrayRegression
database: postgresql
api_style: rest
enums:
  PaymentMethod:
    - CreditCard
    - PayPal
entities:
  Product:
    fields:
      id: uuid [primary]
      supportedPaymentMethods: array(PaymentMethod) [optional]
`)
	schema, err := parser.ParseYAML(domain)
	if err != nil {
		t.Fatalf("parse domain.yaml: %v", err)
	}
	project, err := ir.Build(schema)
	if err != nil {
		t.Fatalf("build IR: %v", err)
	}
	return project
}

func renderTSCoreTypes(t *testing.T, bridgeDir string, project *ir.Project) string {
	t.Helper()
	r, err := New(bridgeDir, nil)
	if err != nil {
		t.Fatalf("New(%s): %v", bridgeDir, err)
	}
	outDir := t.TempDir()
	if _, _, err := r.Render(project, outDir); err != nil {
		t.Fatalf("Render(%s): %v", bridgeDir, err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "src", "generated", "types.ts"))
	if err != nil {
		t.Fatalf("read generated types.ts: %v", err)
	}
	return string(data)
}

// copyTSCoreWithoutArrayFormat copies only the files the renderer loads
// (bridge.yaml, type_mappings.yaml, templates/) into a temp dir, then drops the
// array_format line from the copy. The tck/ tree with node_modules is skipped.
func copyTSCoreWithoutArrayFormat(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()

	copyFile := func(rel string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(src, rel))
		if err != nil {
			t.Fatalf("read bridge file %s: %v", rel, err)
		}
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Fatalf("write bridge file %s: %v", rel, err)
		}
	}

	copyFile("bridge.yaml")
	copyFile("type_mappings.yaml")
	templates, err := os.ReadDir(filepath.Join(src, "templates"))
	if err != nil {
		t.Fatalf("read bridge templates: %v", err)
	}
	for _, entry := range templates {
		if !entry.IsDir() {
			copyFile(filepath.Join("templates", entry.Name()))
		}
	}

	path := filepath.Join(dst, "type_mappings.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read copied type_mappings.yaml: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "array_format:") {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == len(lines) {
		t.Fatal("bundled ts-core type_mappings.yaml has no array_format line to remove")
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatalf("write type_mappings.yaml without array_format: %v", err)
	}
	return dst
}

// excerptAround keeps a failure readable when the generated file is large.
func excerptAround(content, needle string) string {
	lines := strings.Split(content, "\n")
	var out []string
	for _, line := range lines {
		if strings.Contains(line, needle) {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return "(no line containing " + needle + ")"
	}
	return strings.Join(out, "\n")
}
