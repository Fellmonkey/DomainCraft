package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyCSharpBridges copies the bundled C# chain into a temp tree and rewrites
// its extends refs and registry URL to local fixtures. Template regression
// tests then exercise the real templates without network or cache state.
func copyCSharpBridges(t *testing.T, registryURL string) string {
	t.Helper()
	src := filepath.Join("..", "..", "bridges", "csharp")
	dst := filepath.Join(t.TempDir(), "bridges", "csharp")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatalf("copy bridges: %v", err)
	}
	extends := map[string]string{
		"csharp-core":   filepath.Join(dst, "core"),
		"csharp-domain": filepath.Join(dst, "domain"),
		"csharp-efcore": filepath.Join(dst, "persistence", "efcore"),
		"csharp-rest":   filepath.Join(dst, "transport", "rest"),
	}
	err := filepath.WalkDir(dst, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "bridge.yaml" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		for id, dir := range extends {
			text = strings.ReplaceAll(text, "extends: "+id, "extends: "+filepath.ToSlash(dir))
		}
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "registry_url:") {
				lines[i] = "registry_url: \"" + registryURL + "\""
			}
		}
		return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
	})
	if err != nil {
		t.Fatalf("rewrite bridges: %v", err)
	}
	return filepath.Join(dst, "transport", "rest")
}

func generateFixture(t *testing.T, domain, bridge, output string) string {
	t.Helper()
	domainPath := filepath.Join(t.TempDir(), "domain.yaml")
	if err := os.WriteFile(domainPath, []byte(domain), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newRootCommand()
	out := &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"generate", "--domain", domainPath, "--bridge", bridge, "--output", output, "--non-interactive"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("generate failed: %v\n%s", err, out.String())
	}
	return out.String()
}
