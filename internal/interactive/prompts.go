package interactive

import (
	"bufio"
	"fmt"

	"github.com/DomainCraft/DomainCraft/internal/bridge"
)

// runPrompt owns the input loop shared by every interactive prompt. It draws
// the initial state, reads keys in raw mode, lets onKey mutate state and decide
// whether the prompt is done, and redraws after every key that does not end the
// prompt. Enter and Esc are reported as done, so neither triggers a redraw.
func runPrompt(draw func() []string, onKey func(key) (done bool, err error)) error {
	f := &field{}
	f.draw(draw())

	reader := bufio.NewReader(promptInput)
	return rawMode(func() error {
		for {
			k, err := readKey(reader)
			if err != nil {
				return err
			}
			done, err := onKey(k)
			if done || err != nil {
				return err
			}
			f.draw(draw())
		}
	})
}

// runListNav is the single input loop for the numbered menus. Single-select
// prompts pass a nil onToggle: digits 1-9 move the cursor and Enter confirms.
// Multi-select passes onToggle: digits and Space toggle an item, Enter confirms.
func runListNav(count int, draw func(cursor int) []string, onToggle func(int)) (int, error) {
	cursor := 0
	render := func() []string { return draw(cursor) }
	onKey := func(k key) (bool, error) {
		switch k.kind {
		case keyUp:
			if cursor > 0 {
				cursor--
			}
		case keyDown:
			if cursor < count-1 {
				cursor++
			}
		case keySpace:
			if onToggle != nil {
				onToggle(cursor)
			}
		case keyEnter:
			return true, nil
		case keyEsc:
			return true, errAborted
		case keyRune:
			if k.r >= '1' && k.r <= '9' {
				if n := int(k.r - '1'); n < count {
					if onToggle != nil {
						onToggle(n)
					} else {
						cursor = n
					}
				}
			}
		}
		return false, nil
	}
	err := runPrompt(render, onKey)
	return cursor, err
}

// numberedMenu renders the shared shape of a numbered selection prompt: a
// bold title, an optional description, one row per choice with the focused row
// highlighted, and the navigation hint.
func numberedMenu(title, description string, rows []string, cursor int) []string {
	lines := []string{lineField(ansiBold + title)}
	if description != "" {
		lines = append(lines, lineField("  "+description))
	}
	for i, row := range rows {
		if i == cursor {
			lines = append(lines, lineFocus(row))
		} else {
			lines = append(lines, lineField(row))
		}
	}
	lines = append(lines, lineField(ansiDim+"  ↑/↓ or 1-9 to move, Enter to confirm"))
	return lines
}

// SelectBridge shows a bridge selection menu. ↑/↓ or 1-9 moves, Enter picks,
// Esc aborts.
func SelectBridge(registry *bridge.Registry) (*bridge.RegistryEntry, error) {
	// Hide deprecated bridges from interactive selection; they remain resolvable
	// for existing `extends:` chains and via explicit --bridge IDs.
	entries := make([]bridge.RegistryEntry, 0, len(registry.All()))
	for _, e := range registry.All() {
		if !e.Deprecated {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no bridges available")
	}

	rows := make([]string, 0, len(entries))
	for i, e := range entries {
		rows = append(rows, fmt.Sprintf("  %2d) %s (%s) — %s", i+1, e.Name, e.Language, e.Description))
	}
	cursor, err := runListNav(len(entries), func(cursor int) []string {
		return numberedMenu("Select a bridge (target language/framework for code generation)", "", rows, cursor)
	}, nil)
	if err != nil {
		return nil, err
	}
	return &entries[cursor], nil
}

// Option is one choice in SelectOption.
type Option struct {
	Label       string
	Description string
}

// SelectOption shows a numbered menu and returns the index of the chosen
// option. ↑/↓ or 1-9 moves, Enter picks, Esc aborts. The first option is the
// default. Unlike SelectBridge it carries no bridge-specific types, so CLI
// commands can use it for their own choices.
func SelectOption(title, description string, options []Option) (int, error) {
	if len(options) == 0 {
		return 0, fmt.Errorf("no options available")
	}

	rows := make([]string, 0, len(options))
	for i, o := range options {
		row := fmt.Sprintf("  %2d) %s", i+1, o.Label)
		if o.Description != "" {
			row += " — " + o.Description
		}
		rows = append(rows, row)
	}
	cursor, err := runListNav(len(options), func(cursor int) []string {
		return numberedMenu(title, description, rows, cursor)
	}, nil)
	if err != nil {
		return 0, err
	}
	return cursor, nil
}

// PromptGenerateAdmin asks whether to generate an admin panel alongside the backend.
func PromptGenerateAdmin() (bool, error) {
	return confirm(
		"Generate admin panel?",
		"Create an Alpine.js admin panel (single-page HTML) for managing your data",
	)
}

// PromptBridgeUpdate asks whether to update a cached bridge to the newer
// upstream version. Returns true to download the update, false to keep the
// cached copy. The version is intentionally omitted: bridge.Update carries
// the cached (old) version, not the available upstream one.
func PromptBridgeUpdate(entry bridge.RegistryEntry, _ *bridge.Update) (bool, error) {
	return confirm(
		fmt.Sprintf("Update bridge %q?", entry.ID),
		"A newer version is available for the cached bridge. Update the cached copy?",
	)
}

// PromptYesNo asks a yes/no question with the default focus on "No".
func PromptYesNo(title, description string) (bool, error) {
	return confirm(title, description)
}

// confirm asks a yes/no question with two buttons. ←/→ switches the green
// focus, Enter confirms, Esc aborts. Letter shortcuts (y/n) are intentionally
// absent — they break on non-Latin keyboard layouts.
func confirm(title, description string) (bool, error) {
	cursor := 1 // default: No
	render := func() []string {
		lines := []string{lineField(ansiBold + title)}
		if description != "" {
			lines = append(lines, lineField("  "+description))
		}
		buttons := button("Yes", cursor == 0) + " " + button("No", cursor == 1)
		lines = append(lines, lineField("  "+buttons+"   "+ansiDim+"←/→ to switch, Enter to confirm"))
		return lines
	}
	var result bool
	onKey := func(k key) (bool, error) {
		switch k.kind {
		case keyEnter:
			result = cursor == 0
			return true, nil
		case keyEsc:
			return true, errAborted
		case keyLeft:
			if cursor != 0 {
				cursor = 0
			}
		case keyRight:
			if cursor != 1 {
				cursor = 1
			}
		}
		return false, nil
	}
	err := runPrompt(render, onKey)
	return result, err
}

// FileChoice is a selectable file with a display label.
type FileChoice struct {
	Path  string // value returned when selected
	Label string // display text (may annotate e.g. "[custom]")
}

// PromptDeleteFiles asks which orphaned files of a deleted entity should be
// removed. Returns the selected relative paths (empty = keep everything).
func PromptDeleteFiles(entityName string, files []FileChoice) ([]string, error) {
	return promptFiles(
		fmt.Sprintf("Entity %q was removed from domain.yaml", entityName),
		"The following files are no longer generated. Select files to delete:",
		files,
	)
}

// PromptRenameFiles asks which files of a renamed entity should be renamed.
// Returns the selected relative paths (empty = keep everything).
func PromptRenameFiles(oldName, newName string, files []FileChoice) ([]string, error) {
	return promptFiles(
		fmt.Sprintf("Entity %q was renamed to %q", oldName, newName),
		"Select files to rename (old name -> new name in the path):",
		files,
	)
}

// promptFiles skips the prompt entirely when there are no files to choose
// from, so callers don't have to repeat the empty check.
func promptFiles(title, description string, files []FileChoice) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	return multiSelect(title, description, files)
}

// multiSelect shows a numbered list with every item preselected. ↑/↓ moves
// the green focus, Space toggles the focused item, 1-9 toggle directly,
// Enter confirms, Esc aborts. Returns selected paths in original order.
func multiSelect(title, description string, files []FileChoice) ([]string, error) {
	selected := make([]bool, len(files))
	for i := range files {
		selected[i] = true
	}
	build := func(cursor int) []string {
		lines := []string{lineField(ansiBold + title)}
		if description != "" {
			lines = append(lines, lineField("  "+description))
		}
		for i, f := range files {
			marker := " "
			if selected[i] {
				if i == cursor {
					marker = "\u2713"
				} else {
					marker = fgGreen("\u2713")
				}
			}
			s := fmt.Sprintf("  [%s] %2d) %s", marker, i+1, f.Label)
			if i == cursor {
				lines = append(lines, lineFocus(s))
			} else {
				lines = append(lines, lineField(s))
			}
		}
		lines = append(lines, lineField(ansiDim+"  ↑/↓ to move, Space or 1-9 to toggle, Enter to confirm"))
		return lines
	}

	toggle := func(i int) { selected[i] = !selected[i] }
	if _, err := runListNav(len(files), build, toggle); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(files))
	for i, f := range files {
		if selected[i] {
			result = append(result, f.Path)
		}
	}
	return result, nil
}
