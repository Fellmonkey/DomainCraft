package interactive

import (
	"strings"
	"testing"
)

// The single color scheme must wrap box rows in the neutral background with
// white text and reset, and the focus states in green with black text.
func TestLineFieldColors(t *testing.T) {
	row := lineField("x")
	if !strings.Contains(row, "\x1b[48;5;236m") || !strings.Contains(row, "\x1b[37m") || !strings.Contains(row, "\x1b[0m") {
		t.Errorf("lineField missing background/foreground/reset: %q", row)
	}
	if !strings.Contains(lineFocus("x"), "\x1b[42m") || !strings.Contains(lineFocus("x"), "\x1b[30m") {
		t.Errorf("lineFocus missing green background/black text: %q", lineFocus("x"))
	}
	if !strings.Contains(button("Yes", true), "\x1b[42m") {
		t.Errorf("focused button missing green background: %q", button("Yes", true))
	}
}

func TestFitVisible(t *testing.T) {
	cases := []struct {
		name  string
		s     string
		width int
		want  string
	}{
		{"fits", "short", 80, "short"},                 // fits
		{"exact_fit", "12345", 5, "12345"},             // exact fit
		{"truncated", "123456", 5, "1234…"},            // truncated with ellipsis
		{"rune_aware", "Привет мир", 5, "Прив…"},       // rune-aware, not byte-aware
		{"ansi_preserved", "\x1b[42mhello\x1b[0m", 4, "\x1b[42mhel…\x1b[0m"}, // ANSI preserved, text truncated
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fitVisible(c.s, c.width); got != c.want {
				t.Errorf("fitVisible(%q, %d) = %q, want %q", c.s, c.width, got, c.want)
			}
		})
	}
}
