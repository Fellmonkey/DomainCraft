package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	t.Run("no input", func(t *testing.T) {
		var got validationResult
		if err := json.Unmarshal([]byte(ValidationErrorResult("no YAML input provided")), &got); err != nil {
			t.Fatal(err)
		}
		if !got.Parse || len(got.Errors) != 1 || got.Errors[0].Message != "no YAML input provided" {
			t.Fatalf("ValidationErrorResult() = %+v", got)
		}
	})
	t.Run("valid", func(t *testing.T) {
		got := ValidateYAML(validYAML)
		if got != `{"errors":[],"parse":false}` {
			t.Fatalf("ValidateYAML() = %s", got)
		}
	})
	t.Run("parse error", func(t *testing.T) {
		var got validationResult
		if err := json.Unmarshal([]byte(ValidateYAML("entities: [")), &got); err != nil {
			t.Fatal(err)
		}
		if !got.Parse || len(got.Errors) != 1 {
			t.Fatalf("ValidateYAML() = %+v", got)
		}
	})
	t.Run("logical error", func(t *testing.T) {
		var got validationResult
		if err := json.Unmarshal([]byte(ValidateYAML("project:\n  name: X\nentities: {}\n")), &got); err != nil {
			t.Fatal(err)
		}
		if got.Parse || len(got.Errors) == 0 {
			t.Fatalf("ValidateYAML() = %+v", got)
		}
	})
}

func TestParseField(t *testing.T) {
	t.Run("no input", func(t *testing.T) {
		if got := FieldErrorResult("no field definition provided"); got != `{"error":"no field definition provided"}` {
			t.Fatalf("FieldErrorResult() = %s", got)
		}
	})
	t.Run("named field", func(t *testing.T) {
		got := ParseFieldDefinition("string [required, max:255]", "title")
		var fd struct {
			Name        string
			Type        string
			IsRequired  bool
			Validations map[string]string
		}
		if err := json.Unmarshal([]byte(got), &fd); err != nil {
			t.Fatal(err)
		}
		if fd.Name != "title" || fd.Type != "string" || !fd.IsRequired || fd.Validations["max"] != "255" {
			t.Fatalf("ParseFieldDefinition() = %+v", got)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		if got := ParseFieldDefinition("no_such_type", "title"); !strings.Contains(got, "unknown type") {
			t.Fatalf("ParseFieldDefinition() = %s", got)
		}
	})
}

func TestParseDomain(t *testing.T) {
	t.Run("no input", func(t *testing.T) {
		if got := FieldErrorResult("no YAML input provided"); got != `{"error":"no YAML input provided"}` {
			t.Fatalf("FieldErrorResult() = %s", got)
		}
	})
	t.Run("valid", func(t *testing.T) {
		got := ParseDomainYAML(validYAML)
		var raw struct {
			Project  struct{ Name string }
			Entities map[string]json.RawMessage
		}
		if err := json.Unmarshal([]byte(got), &raw); err != nil {
			t.Fatal(err)
		}
		if raw.Project.Name != "Bench" || raw.Entities["User"] == nil {
			t.Fatalf("ParseDomainYAML() = %s", got)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		if got := ParseDomainYAML("entities: ["); !strings.Contains(got, "error") {
			t.Fatalf("ParseDomainYAML() = %s", got)
		}
	})
}

func TestSpecmeta(t *testing.T) {
	got := Specmeta()
	var meta map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &meta); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"fieldTypes", "features", "databases", "apiStyles", "authTypes", "featureFieldDefs"} {
		if _, ok := meta[key]; !ok {
			t.Errorf("Specmeta() missing %q", key)
		}
	}
}

func TestVersion(t *testing.T) {
	if got := Version("v0.2.0"); got != "v0.2.0" {
		t.Fatalf("Version() = %q", got)
	}
}

func TestGenerateYAML(t *testing.T) {
	result := GenerateYAML(validYAML, "admin-alpine", "")
	var got generateResult
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" {
		t.Fatalf("GenerateYAML() error = %q", got.Error)
	}
	if len(got.Files) < 4 {
		t.Fatalf("GenerateYAML() returned %d files, want at least 4", len(got.Files))
	}
	found := false
	for _, f := range got.Files {
		if f.Path == "admin/index.html" && strings.Contains(f.Content, "Bench") {
			found = true
		}
	}
	if !found {
		t.Fatalf("GenerateYAML() did not render admin/index.html: %d files", len(got.Files))
	}
}

func TestGenerateYAMLRejectsInvalidModel(t *testing.T) {
	result := GenerateYAML("project:\n  name: Bad\nentities:\n  Broken:\n    fields:\n      title: string\n", "admin-alpine", "")
	var got generateResult
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error == "" || !strings.Contains(got.Error, "validation failed") {
		t.Fatalf("GenerateYAML generated files for an invalid model: %s", result)
	}
}

func TestGenerateYAMLAlternatingModels(t *testing.T) {
	for _, name := range []string{"Alpha", "Beta", "Alpha"} {
		yaml := strings.Replace(validYAML, "Bench", name, 1)
		result := GenerateYAML(yaml, "admin-alpine", "")
		var got generateResult
		if err := json.Unmarshal([]byte(result), &got); err != nil {
			t.Fatal(err)
		}
		if got.Error != "" {
			t.Fatalf("GenerateYAML(%s) error = %q", name, got.Error)
		}
		found := false
		for _, f := range got.Files {
			if f.Path == "admin/index.html" && strings.Contains(f.Content, name) {
				found = true
			}
		}
		if !found {
			t.Fatalf("GenerateYAML(%s) returned stale files", name)
		}
	}
}

func TestGenerateYAMLRejectsBadBridge(t *testing.T) {
	result := GenerateYAML(validYAML, "does-not-exist", "")
	var got generateResult
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error == "" {
		t.Fatal("GenerateYAML accepted an unknown bridge")
	}
}

// TestWasmApiContracts pins the exact JSON returned by every WASM export.
// A shape change here breaks the Studio bridge even when tests that unmarshal
// loosely still pass.
func TestWasmApiContracts(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, got, want string }{
		{
			name: "goValidate",
			got:  ValidateYAML(validYAML),
			want: `{"errors":[],"parse":false}`,
		},
		{
			name: "goValidate-no-input",
			got:  ValidationErrorResult("no YAML input provided"),
			want: `{"errors":[{"message":"no YAML input provided","warning":false}],"parse":true}`,
		},
		{
			name: "goValidate-type-error",
			got:  ValidationErrorResult("YAML input must be a string"),
			want: `{"errors":[{"message":"YAML input must be a string","warning":false}],"parse":true}`,
		},
		{
			name: "goParseField-no-input",
			got:  FieldErrorResult("no field definition provided"),
			want: `{"error":"no field definition provided"}`,
		},
		{
			name: "goParseField-no-validations",
			got:  ParseFieldDefinition("string", "title"),
			want: `{"Name":"title","Type":"string","TargetEntity":"","TargetType":"","IsPrimary":false,"IsOptional":false,"IsUnique":false,"IsHidden":false,"IsReadonly":false,"IsRequired":false,"OnDelete":"","IsMany":false,"OldName":"","Validations":{},"DefaultValue":"","DefaultIsFunc":false}`,
		},
		{
			name: "goParseField",
			got:  ParseFieldDefinition("string [required, max:255]", "title"),
			want: `{"Name":"title","Type":"string","TargetEntity":"","TargetType":"","IsPrimary":false,"IsOptional":false,"IsUnique":false,"IsHidden":false,"IsReadonly":false,"IsRequired":true,"OnDelete":"","IsMany":false,"OldName":"","Validations":{"max":"255"},"DefaultValue":"","DefaultIsFunc":false}`,
		},
		{
			name: "goParseDomain",
			got:  ParseDomainYAML("project:\n  name: Bench\nentities: {}\n"),
			want: `{"project":{"name":"Bench","deploy":{"domain":"localhost","port":8080},"versioning":{"enabled":true,"default_version":"1.0"},"rate_limit":{"enabled":true,"policy":"fixed","permit_limit":100,"window_seconds":60},"pagination":{"default_page_size":20,"max_page_size":200}},"database":"postgresql","auth":{"type":"none","endpoints":{"login":null,"register":null,"me":null,"setup":null}},"apiStyle":"rest","entities":{}}`,
		},
		{
			name: "goValidate-bad-yaml",
			got:  ValidateYAML("entities: ["),
			want: `{"errors":[{"message":"error parsing YAML: yaml: line 1: did not find expected node content","warning":false}],"parse":true}`,
		},
		{
			name: "goParseDomain-bad-yaml",
			got:  ParseDomainYAML("entities: ["),
			want: `{"error":"yaml: line 1: did not find expected node content"}`,
		},
		{name: "goVersion", got: Version("dev"), want: "dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Fatalf("got  %s\nwant %s", tc.got, tc.want)
			}
		})
	}
	if n := len(Specmeta()); n < 2000 {
		t.Fatalf("goSpecmeta returned %d bytes, want >= 2000", n)
	}
}

func TestResultCacheAlternatingInputs(t *testing.T) {
	t.Parallel()
	other := strings.Replace(validYAML, "Bench", "Other", 1)
	for i := range 3 {
		for _, tc := range []struct{ yaml, name string }{
			{validYAML, "Bench"},
			{other, "Other"},
		} {
			if got := ValidateYAML(tc.yaml); !strings.Contains(got, `"errors":[],"parse":false`) {
				t.Fatalf("iteration %d: ValidateYAML(%s) = %s", i, tc.name, got)
			}
			domain := ParseDomainYAML(tc.yaml)
			if !strings.Contains(domain, `"name":"`+tc.name+`"`) {
				t.Fatalf("iteration %d: ParseDomainYAML(%s) = %s", i, tc.name, domain)
			}
		}
	}
}

const validYAML = `project:
  name: Bench
entities:
  User:
    fields:
      id: uuid [primary]
      email: string [required, unique, email]
      password: string [required, hidden]
auth:
  type: jwt
  roles: [Admin, User]
  entity: User
`
