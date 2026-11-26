package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRenderScenarioRejectsControlCharacters(t *testing.T) {
	for _, name := range []string{"bad\nname", "bad\rname", "bad\x00name"} {
		if _, err := renderScenario("shop", name); err == nil {
			t.Errorf("renderScenario(%q) accepted a control character", name)
		}
	}
	if _, err := renderScenario("shop", "O'Brien"); err != nil {
		t.Errorf("valid name with an apostrophe was rejected: %v", err)
	}
}

func runInit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(append([]string{"init", "--non-interactive"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func TestInitScenariosProduceValidDomains(t *testing.T) {
	for _, s := range scenarios {
		t.Run(s.id, func(t *testing.T) {
			domainPath := filepath.Join(t.TempDir(), "domain.yaml")
			out, err := runInit(t, "--scenario", s.id, "--name", "Test "+s.id, "--domain", domainPath)
			if err != nil {
				t.Fatalf("init %s failed: %v\noutput: %s", s.id, err, out)
			}
			if !strings.Contains(out, "Created "+domainPath) {
				t.Fatalf("expected created message, got: %s", out)
			}

			data, err := os.ReadFile(domainPath)
			if err != nil {
				t.Fatalf("read domain: %v", err)
			}
			var root struct {
				Project struct {
					Name string `yaml:"name"`
				} `yaml:"project"`
			}
			if err := yaml.Unmarshal(data, &root); err != nil {
				t.Fatalf("rendered scenario is not valid YAML: %v", err)
			}
			if want := "Test " + s.id; root.Project.Name != want {
				t.Errorf("project.name = %q, want %q", root.Project.Name, want)
			}

			validate := newRootCommand()
			vout := &bytes.Buffer{}
			validate.SetOut(vout)
			validate.SetErr(vout)
			validate.SetArgs([]string{"validate", "--domain", domainPath, "--non-interactive"})
			if err := validate.Execute(); err != nil {
				t.Fatalf("scenario %s failed validation: %v\noutput: %s", s.id, err, vout.String())
			}
			if !strings.Contains(vout.String(), "Schema valid") {
				t.Fatalf("expected Schema valid, got: %s", vout.String())
			}
		})
	}
}

func TestInitRefusesToOverwriteWithoutForce(t *testing.T) {
	domainPath := filepath.Join(t.TempDir(), "domain.yaml")
	if err := os.WriteFile(domainPath, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runInit(t, "--scenario", "shop", "--domain", domainPath)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected overwrite refusal, got %v", err)
	}
	data, _ := os.ReadFile(domainPath)
	if string(data) != "keep me\n" {
		t.Fatalf("existing file was modified: %q", data)
	}

	out, err := runInit(t, "--scenario", "shop", "--domain", domainPath, "--force")
	if err != nil {
		t.Fatalf("--force failed: %v\n%s", err, out)
	}
	data, _ = os.ReadFile(domainPath)
	if string(data) == "keep me\n" {
		t.Fatal("--force did not overwrite the file")
	}
}

func TestInitUnknownScenario(t *testing.T) {
	_, err := runInit(t, "--scenario", "game", "--domain", filepath.Join(t.TempDir(), "domain.yaml"))
	if err == nil || !strings.Contains(err.Error(), "unknown scenario") {
		t.Fatalf("expected unknown scenario error, got %v", err)
	}
}

// The C# test templates emit `<RoleType>.<Role>` for role-based checks. A plain
// `string` role renders as `string.Admin` and does not compile, so every starter
// model must declare the auth role as an enum matching auth.roles.
func TestInitScenariosUseEnumAuthRole(t *testing.T) {
	for _, s := range scenarios {
		t.Run(s.id, func(t *testing.T) {
			content, err := renderScenario(s.id, "Test")
			if err != nil {
				t.Fatal(err)
			}
			var root struct {
				Auth struct {
					Roles []string `yaml:"roles"`
				} `yaml:"auth"`
				Enums    map[string][]string `yaml:"enums"`
				Entities map[string]struct {
					Fields map[string]string `yaml:"fields"`
				} `yaml:"entities"`
			}
			if err := yaml.Unmarshal([]byte(content), &root); err != nil {
				t.Fatal(err)
			}
			roleField, ok := root.Entities["User"].Fields["role"]
			if !ok || !strings.Contains(roleField, "enum(Role)") {
				t.Fatalf("auth role field = %q, want enum(Role)", roleField)
			}
			enumRoles, ok := root.Enums["Role"]
			if !ok {
				t.Fatal("missing Role enum")
			}
			if len(enumRoles) != len(root.Auth.Roles) {
				t.Fatalf("Role enum = %v, auth.roles = %v", enumRoles, root.Auth.Roles)
			}
			for _, role := range root.Auth.Roles {
				if !slices.Contains(enumRoles, role) {
					t.Errorf("auth role %q is missing from Role enum %v", role, enumRoles)
				}
			}
		})
	}
}

func TestInitDefaultsProjectName(t *testing.T) {
	domainPath := filepath.Join(t.TempDir(), "domain.yaml")
	out, err := runInit(t, "--scenario", "shop", "--domain", domainPath)
	if err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(domainPath)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Project struct {
			Name string `yaml:"name"`
		} `yaml:"project"`
	}
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if root.Project.Name == "" {
		t.Fatal("project.name is empty when --name is omitted")
	}
}

func TestRenderScenarioEscapesProjectName(t *testing.T) {
	content, err := renderScenario("blog", "Bob's Blog")
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Project struct {
			Name string `yaml:"name"`
		} `yaml:"project"`
	}
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		t.Fatalf("escaped name broke YAML: %v", err)
	}
	if root.Project.Name != "Bob's Blog" {
		t.Fatalf("project.name = %q, want %q", root.Project.Name, "Bob's Blog")
	}
}
