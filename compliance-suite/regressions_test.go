package compliance

import (
	"os"
	"strings"
	"testing"
)

// TestRegressionsTable guards the regression table format:
// every non-comment line must have exactly three tab-separated fields
// (check name, issue/PR reference, a line on what broke), the check name
// must be non-empty and unique. This is the enforcement for the rule
// "every bug fix adds its check here; a removed name is an error".
func TestRegressionsTable(t *testing.T) {
	data, err := os.ReadFile("regressions.tsv")
	if err != nil {
		t.Fatalf("read regressions.tsv: %v", err)
	}

	seen := make(map[string]bool)
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
	}
}
