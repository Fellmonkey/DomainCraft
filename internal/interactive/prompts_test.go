package interactive

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
)

// setInput replaces the prompt input with the given script of keypresses and
// silences prompt output. Arrow keys are written as their escape sequences.
func setInput(s string) {
	promptInput = strings.NewReader(s)
	promptOutput = io.Discard
}

func TestConfirm(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
		err   error
	}{
		{"default_no", "\n", false, nil},              // Enter on default (No)
		{"left_to_yes", "\x1b[D\n", true, nil},        // ← moves to Yes
		{"right_stays_no", "\x1b[C\n", false, nil},    // → stays on No
		{"yes_then_no", "\x1b[D\x1b[C\n", false, nil}, // Yes then back to No
		{"esc_aborts", "\x1b", false, errAborted},     // Esc cancels
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setInput(c.input)
			got, err := confirm("?", "")
			if !errors.Is(err, c.err) {
				t.Fatalf("confirm(%q) error = %v, want %v", c.input, err, c.err)
			}
			if err != nil {
				return
			}
			if got != c.want {
				t.Errorf("confirm(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

func TestSelectBridge(t *testing.T) {
	reg := bridge.NewRegistry([]bridge.RegistryEntry{
		{ID: "a", Name: "Alpha", Language: "Go", Description: "first"},
		{ID: "b", Name: "Beta", Language: "C#", Description: "second"},
	})

	// explicit choice
	setInput("2\n")
	got, err := SelectBridge(reg)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "b" {
		t.Errorf("SelectBridge(2) = %q, want b", got.ID)
	}

	// default = first
	setInput("\n")
	got, err = SelectBridge(reg)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "a" {
		t.Errorf("SelectBridge(default) = %q, want a", got.ID)
	}

	// out-of-range digit is ignored, then 1 moves to the first
	setInput("91\n")
	got, err = SelectBridge(reg)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "a" {
		t.Errorf("SelectBridge(9 then 1) = %q, want a", got.ID)
	}

	// arrow down then Enter picks the second
	setInput("\x1b[B\n")
	got, err = SelectBridge(reg)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "b" {
		t.Errorf("SelectBridge(down) = %q, want b", got.ID)
	}

	// Esc aborts
	setInput("\x1b")
	if _, err = SelectBridge(reg); !errors.Is(err, errAborted) {
		t.Errorf("SelectBridge(esc) error = %v, want errAborted", err)
	}
}

func TestSelectOption(t *testing.T) {
	options := []Option{
		{Label: "shop", Description: "products and orders"},
		{Label: "saas", Description: "workspaces"},
		{Label: "crm"},
	}

	setInput("2\n")
	if got, err := SelectOption("?", "", options); err != nil || got != 1 {
		t.Fatalf("SelectOption(2) = %d, %v; want 1, nil", got, err)
	}
	setInput("\n")
	if got, err := SelectOption("?", "", options); err != nil || got != 0 {
		t.Fatalf("SelectOption(default) = %d, %v; want 0, nil", got, err)
	}
	setInput("\x1b[B\n")
	if got, err := SelectOption("?", "", options); err != nil || got != 1 {
		t.Fatalf("SelectOption(down) = %d, %v; want 1, nil", got, err)
	}
	setInput("\x1b")
	if _, err := SelectOption("?", "", options); !errors.Is(err, errAborted) {
		t.Fatalf("SelectOption(esc) error = %v, want errAborted", err)
	}
}

func TestPromptYesNo(t *testing.T) {
	setInput("\x1b[D\n")
	if got, err := PromptYesNo("?", ""); err != nil || !got {
		t.Fatalf("PromptYesNo(yes) = %v, %v; want true, nil", got, err)
	}
}

func TestMultiSelect(t *testing.T) {
	files := []FileChoice{
		{Path: "p1", Label: "f1"},
		{Path: "p2", Label: "f2"},
		{Path: "p3", Label: "f3"},
	}

	// Enter confirms everything (all preselected)
	setInput("\n")
	got, err := multiSelect("t", "", files)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("default selection = %v, want all 3", got)
	}

	// toggle off item 2
	setInput("2\n")
	got, err = multiSelect("t", "", files)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "p1" || got[1] != "p3" {
		t.Errorf("after toggle 2 = %v, want [p1 p3]", got)
	}

	// toggle off 1 and 3, leaving only item 2
	setInput("13\n")
	got, err = multiSelect("t", "", files)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "p2" {
		t.Errorf("13 = %v, want [p2]", got)
	}

	// out-of-range '9' is ignored; 1,2 toggle the first two off
	setInput("912\n")
	got, err = multiSelect("t", "", files)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "p3" {
		t.Errorf("9 then 1,2 = %v, want [p3]", got)
	}

	// Space toggles the focused item, Enter confirms
	setInput(" \n") // toggle item 1 off
	got, err = multiSelect("t", "", files)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "p2" || got[1] != "p3" {
		t.Errorf("space = %v, want [p2 p3]", got)
	}

	// Esc aborts
	setInput("\x1b")
	if _, err = multiSelect("t", "", files); !errors.Is(err, errAborted) {
		t.Errorf("multiSelect(esc) error = %v, want errAborted", err)
	}
}

func TestPromptDeleteFilesEmpty(t *testing.T) {
	got, err := PromptDeleteFiles("Gone", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("PromptDeleteFiles(nil) = %v, want nil", got)
	}
}

func TestRunPrompt(t *testing.T) {
	var keys []keyKind
	draws := 0
	draw := func() []string { draws++; return []string{"line"} }
	onKey := func(k key) (bool, error) {
		keys = append(keys, k.kind)
		if k.kind == keyEsc {
			return true, errAborted
		}
		return k.kind == keyEnter, nil
	}

	// Initial draw, one redraw after Down, none after Enter.
	setInput("\x1b[B\n")
	if err := runPrompt(draw, onKey); err != nil {
		t.Fatal(err)
	}
	if draws != 2 {
		t.Errorf("draws = %d, want 2 (initial + after Down)", draws)
	}
	if want := []keyKind{keyDown, keyEnter}; !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}

	// Esc aborts before a redraw.
	keys = nil
	draws = 0
	setInput("\x1b")
	if err := runPrompt(draw, onKey); !errors.Is(err, errAborted) {
		t.Fatalf("runPrompt(esc) error = %v, want errAborted", err)
	}
	if draws != 1 || !slices.Equal(keys, []keyKind{keyEsc}) {
		t.Errorf("esc: draws = %d, keys = %v; want 1, [keyEsc]", draws, keys)
	}
}

func TestNumberedMenu(t *testing.T) {
	got := numberedMenu("Title", "desc", []string{"  1) one", "  2) two"}, 1)
	want := []string{
		lineField(ansiBold + "Title"),
		lineField("  desc"),
		lineField("  1) one"),
		lineFocus("  2) two"),
		lineField(ansiDim + "  ↑/↓ or 1-9 to move, Enter to confirm"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("numberedMenu() = %q, want %q", got, want)
	}
}

func TestPromptFilesEmpty(t *testing.T) {
	for _, call := range []struct {
		name string
		fn   func() ([]string, error)
	}{
		{"promptFiles", func() ([]string, error) { return promptFiles("t", "d", nil) }},
		{"PromptRenameFiles", func() ([]string, error) { return PromptRenameFiles("Old", "New", nil) }},
	} {
		t.Run(call.name, func(t *testing.T) {
			got, err := call.fn()
			if err != nil || got != nil {
				t.Errorf("empty files = %v, %v; want nil, nil", got, err)
			}
		})
	}
}
