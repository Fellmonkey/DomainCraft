// Package compliance verifies that the regression table names real checks.
package compliance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegressionsTable guards the regression table format:
// every non-comment line must have exactly three tab-separated fields
// (check name, issue/PR reference, a line on what broke), the check name
// must be non-empty and unique. A `file::TestX` name must still point at
// an existing test file that defines TestX with at least one assertion, so a
// removed, renamed or emptied check cannot silently pass. This is the
// enforcement for the rule "one row per test file; a removed check name is an
// error".
func TestRegressionsTable(t *testing.T) {
	data, err := os.ReadFile("regressions.tsv")
	if err != nil {
		t.Fatalf("read regressions.tsv: %v", err)
	}

	seen := make(map[string]bool)
	// Read each referenced test file once.
	sources := make(map[string]string)
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			t.Errorf("line %d: want 3 tab-separated fields, got %d: %q", i+1, len(fields), line)
			continue
		}

		name := strings.TrimSpace(fields[0])
		issue := strings.TrimSpace(fields[1])
		why := strings.TrimSpace(fields[2])

		if name == "" {
			t.Errorf("line %d: empty check name", i+1)
			continue
		}
		if issue == "" {
			t.Errorf("line %d: empty issue/PR reference for %q", i+1, name)
		}
		if why == "" {
			t.Errorf("line %d: empty description for %q", i+1, name)
		}
		if seen[name] {
			t.Errorf("duplicate check name %q (line %d)", name, i+1)
		}
		seen[name] = true

		ref, testName, ok := strings.Cut(name, "::")
		if !ok || ref == "" || testName == "" {
			continue // Not a file::TestX reference.
		}
		path := resolveRegressionTestFile(t, ref, i+1)
		if path == "" {
			continue
		}
		src, cached := sources[path]
		if !cached {
			body, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Errorf("line %d: read %s: %v", i+1, path, rerr)
				sources[path] = ""
				continue
			}
			src = string(body)
			sources[path] = src
		}
		if src == "" {
			continue // Read already reported as an error.
		}
		defined, hasAssert := regressionFuncHasAssert(src, testName)
		if !defined {
			t.Errorf("line %d: %s does not define func %s(", i+1, path, testName)
			continue
		}
		if !hasAssert {
			t.Errorf("line %d: %s defines func %s( without an assertion: a check with no assert proves nothing", i+1, path, testName)
		}
	}
}

// regressionFuncHasAssert reports whether src defines func testName(...) and,
// if so, whether that function's body contains at least one assertion. A body
// with no assertion would let an empty stub satisfy the table. The body ends
// at the first line starting with "}" in column zero, which in gofmt'd Go is
// the function's closing brace.
func regressionFuncHasAssert(src, testName string) (defined, hasAssert bool) {
	sig := "func " + testName + "("
	idx := strings.Index(src, sig)
	if idx < 0 {
		return false, false
	}
	open := strings.IndexByte(src[idx:], '{')
	if open < 0 {
		return false, false
	}
	body := src[idx+open+1:]
	if end := strings.Index(body, "\n}"); end >= 0 {
		body = body[:end]
	}
	for _, assert := range []string{"t.Fatal", "t.Error", "assert.", "require."} {
		if strings.Contains(body, assert) {
			return true, true
		}
	}
	return true, false
}

// TestRegressionsTableRejectsEmptyBody guards the guard: a listed function
// whose body is empty, or only computes values without asserting them, must
// be rejected by regressionFuncHasAssert. The source is written to a real
// temp file so the check sees exactly the bytes it sees for table entries.
func TestRegressionsTableRejectsEmptyBody(t *testing.T) {
	src := `package sample

import "testing"

func TestEmptyBody(t *testing.T) {
}

func TestNoAssert(t *testing.T) {
	got := 1 + 1
	_ = got
}

func TestWithAssert(t *testing.T) {
	if 1+1 != 2 {
		t.Fatal("bad math")
	}
}
`
	path := filepath.Join(t.TempDir(), "sample_test.go")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp file: %v", err)
	}
	for _, tc := range []struct {
		name      string
		defined   bool
		hasAssert bool
	}{
		{name: "TestEmptyBody", defined: true, hasAssert: false},
		{name: "TestNoAssert", defined: true, hasAssert: false},
		{name: "TestWithAssert", defined: true, hasAssert: true},
		{name: "TestMissing", defined: false, hasAssert: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defined, hasAssert := regressionFuncHasAssert(string(data), tc.name)
			if defined != tc.defined || hasAssert != tc.hasAssert {
				t.Errorf("regressionFuncHasAssert(%q) = (%v, %v), want (%v, %v)", tc.name, defined, hasAssert, tc.defined, tc.hasAssert)
			}
		})
	}
}

// resolveRegressionTestFile turns a check's file reference into a repo-root
// relative path. Paths with a separator are used as-is; bare names are
// resolved against *_test.go files under the repo root (the test runs from
// compliance-suite/, one level below it).
func resolveRegressionTestFile(t *testing.T, ref string, line int) string {
	t.Helper()

	if strings.ContainsAny(ref, `/\`) {
		// Table paths are repo-root relative; tests run one level below it.
		return filepath.Join("..", filepath.FromSlash(ref))
	}
	if !strings.HasSuffix(ref, "_test.go") {
		ref += "_test.go"
	}

	root := ".."
	found := ""
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != ref {
			return nil
		}
		if found != "" {
			return fmt.Errorf("ambiguous file %q: %s and %s", ref, found, path)
		}
		found = filepath.ToSlash(path)
		return nil
	})
	if err != nil {
		t.Errorf("line %d: resolve %q: %v", line, ref, err)
		return ""
	}
	if found == "" {
		t.Errorf("line %d: no %s under %s", line, ref, root)
	}
	return found
}
