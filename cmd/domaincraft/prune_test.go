package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/DomainCraft/DomainCraft/internal/interactive"
	"github.com/DomainCraft/DomainCraft/internal/snapshot"
)

// TestApplyMigrationActionsRewritesFieldRenames covers the --prune branch that
// rewrites identifiers in developer-owned files. The CLI orchestration was
// previously untested, so a dropped field rename could leave stale names.
func TestApplyMigrationActionsRewritesFieldRenames(t *testing.T) {
	root := t.TempDir()
	oldOutput, oldPrune := outputDir, prune
	outputDir, prune = root, true
	t.Cleanup(func() {
		outputDir, prune = oldOutput, oldPrune
	})

	custom := filepath.Join(root, "src", "ItemService.cs")
	if err := os.MkdirAll(filepath.Dir(custom), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(custom, []byte("class ItemService { public string Title; var title = 1; }"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff := &snapshot.Diff{FieldRenames: []snapshot.FieldRename{{
		Entity:      "Item",
		OldField:    "title",
		NewField:    "name",
		CustomFiles: []string{"src/ItemService.cs"},
	}}}

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	applied, err := applyMigrationActions(diff, map[string]bool{}, nil, cmd)
	if err != nil || !applied {
		t.Fatalf("applyMigrationActions = %v, %v; want true, nil", applied, err)
	}
	data, err := os.ReadFile(custom)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "class ItemService { public string Name; var name = 1; }" {
		t.Fatalf("custom file not rewritten: %q", data)
	}
	if !strings.Contains(out.String(), "updated field title -> name") {
		t.Fatalf("missing report line: %q", out.String())
	}
}

func TestChooseMigrationFiles(t *testing.T) {
	files := []snapshot.FileRef{{Path: "src/A.cs"}, {Path: "src/B.cs"}}

	t.Run("prune selects every file without prompting", func(t *testing.T) {
		oldPrune := prune
		prune = true
		t.Cleanup(func() { prune = oldPrune })

		selected, err := chooseMigrationFiles(files, func([]interactive.FileChoice) ([]string, error) {
			t.Fatal("prompt must not be called in --prune mode")
			return nil, nil
		})
		if err != nil || len(selected) != 2 || !selected["src/A.cs"] || !selected["src/B.cs"] {
			t.Fatalf("selected = %v, %v", selected, err)
		}
	})

	t.Run("prompt result is respected", func(t *testing.T) {
		oldPrune := prune
		prune = false
		t.Cleanup(func() { prune = oldPrune })

		selected, err := chooseMigrationFiles(files, func(choices []interactive.FileChoice) ([]string, error) {
			if len(choices) != 2 {
				t.Fatalf("choices = %d, want 2", len(choices))
			}
			return []string{"src/B.cs"}, nil
		})
		if err != nil || len(selected) != 1 || !selected["src/B.cs"] {
			t.Fatalf("selected = %v, %v", selected, err)
		}
	})
}
