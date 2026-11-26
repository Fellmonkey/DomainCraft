// Package interactive provides the dependency-free ANSI terminal prompts.
package interactive

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// The prompts mimic the look of a TUI form library (the one they replaced):
// every field is a box with a neutral background, the focused row or active
// button is filled green (neutral → green when you switch), and selected
// multi-select items get a green checkmark. Navigation is keyboard driven in
// raw mode (arrows, Space, Enter); a plain line fallback is used when stdin is
// not a terminal (tests, pipes).
//
// One color scheme serves both terminal themes: white text on a dark gray box
// keeps full contrast on dark terminals, and on light terminals the box reads
// as a dark chip with white text — the same contrast either way. The green
// focus uses black text, which reads on any background.
//
// Every rendered line is truncated to the terminal width before it is drawn,
// so no line ever wraps. Wrapping would break in-place redraw: the cursor
// moves by logical lines while the terminal counts wrapped rows, and stale
// fragments (a duplicated title) stay on screen.
const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiBlack = "\x1b[30m"
	ansiWhite = "\x1b[37m"
	ansiGreen = "\x1b[32m"

	bgField = "\x1b[48;5;236m" // neutral box: dark gray, white text
	bgFocus = "\x1b[42m"       // active row / focused button: green, black text
)

// fgGreen paints s green without touching the surrounding background.
func fgGreen(s string) string { return ansiGreen + s + "\x1b[39m" }

// lineField renders a normal box row; lineFocus renders the active row.
func lineField(s string) string { return bgField + ansiWhite + s + ansiReset }
func lineFocus(s string) string { return bgFocus + ansiBlack + ansiBold + s + ansiReset }

// button renders a [label] button; the focused one is filled green.
func button(label string, focused bool) string {
	s := " " + label + " "
	if focused {
		return bgFocus + ansiBlack + ansiBold + s + bgField + ansiWhite
	}
	return bgField + ansiWhite + s
}

// errAborted is returned when the user cancels a prompt with Esc.
var errAborted = errors.New("interactive prompt aborted")

// IsTerminal returns true if stdin is a terminal (interactive mode).
func IsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// promptInput and promptOutput are the streams prompts read from and write to.
// They are variables so tests can swap them; the reader is created lazily over
// the current promptInput.
var (
	promptInput  io.Reader = os.Stdin
	promptOutput io.Writer = os.Stdout
)

// realTTY reports whether prompts run against a real terminal (raw mode,
// in-place redraw). Tests swap promptInput, which disables both.
func realTTY() bool {
	return promptInput == os.Stdin && IsTerminal()
}

// rawMode switches stdin to raw mode for the duration of fn on a real
// terminal, so arrows/Space register without Enter.
func rawMode(fn func() error) error {
	if !realTTY() {
		return fn()
	}
	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer func() {
		// Best effort: a failed restore leaves the terminal in raw mode, but the
		// prompt's own result must not be replaced by a cleanup error.
		_ = term.Restore(int(os.Stdin.Fd()), state)
	}()
	return fn()
}

// termWidth returns the terminal width in columns, or 80 when unknown.
func termWidth() int {
	if !realTTY() {
		return 80
	}
	w, _, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

// runeWidth is the number of terminal columns a rune occupies. East Asian
// wide/fullwidth characters and emoji take two columns; counting one column
// per rune let a line "fit" by the metric and then wrap in the terminal, which
// breaks in-place redraw — the reason fitVisible exists.
func runeWidth(r rune) int {
	switch {
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		return 0
	case r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x1f300 && r <= 0x1faff) ||
		(r >= 0x20000 && r <= 0x3fffd)):
		return 2
	default:
		return 1
	}
}

// fitVisible truncates s so its visible width fits in width columns. ANSI
// escape sequences are zero-width and are preserved; a trailing ellipsis
// replaces the last column of truncated text. When the truncation cut through
// a styled line, a reset sequence is re-appended so the color cannot leak.
func fitVisible(s string, width int) string {
	toks := tokenize(s)
	visible := 0
	for _, t := range toks {
		if t.esc == "" {
			visible += runeWidth(t.r)
		}
	}
	if visible <= width {
		return s
	}
	// Keep width-1 columns and let the ellipsis take the last one.
	keep := width - 1
	var out strings.Builder
	hasANSI := false
	written := 0
	for _, t := range toks {
		if t.esc != "" {
			hasANSI = true
			out.WriteString(t.esc)
			continue
		}
		w := runeWidth(t.r)
		if written+w <= keep {
			out.WriteRune(t.r)
			written += w
		} else {
			out.WriteString("\u2026")
			break
		}
	}
	if hasANSI {
		out.WriteString(ansiReset)
	}
	return out.String()
}

// token is one piece of a rendered line: an ANSI escape sequence or a rune.
type token struct {
	esc string // non-empty for escape sequences
	r   rune
}

// tokenize splits s into escape-sequence and rune tokens. CSI sequences
// (ESC [ params final) are recognized; anything else after ESC is consumed
// as a two-byte sequence.
func tokenize(s string) []token {
	var toks []token
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && s[j] >= 0x20 && s[j] < 0x40 {
					j++
				}
				if j < len(s) {
					j++
				}
			} else if j < len(s) {
				j++
			}
			toks = append(toks, token{esc: s[i:j]})
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		toks = append(toks, token{r: r})
		i += size
	}
	return toks
}

type keyKind int

const (
	keyOther keyKind = iota
	keyUp
	keyDown
	keyLeft
	keyRight
	keyEnter
	keySpace
	keyEsc
	keyRune
)

type key struct {
	kind keyKind
	r    rune
}

// readKey reads one keypress from the reader created for this prompt. Arrow
// keys arrive as ESC [ X or ESC O X (some terminals use application cursor
// mode); a bare Esc (no '['/'O' after it) aborts the prompt and the extra byte,
// if any, is pushed back so it isn't lost.
func readKey(r *bufio.Reader) (key, error) {
	b, err := r.ReadByte()
	if err != nil {
		return key{}, err
	}
	switch b {
	case '\r', '\n':
		return key{kind: keyEnter}, nil
	case ' ':
		return key{kind: keySpace}, nil
	case 27:
		next, err := r.ReadByte()
		if err != nil {
			return key{kind: keyEsc}, nil
		}
		if next != '[' && next != 'O' {
			_ = r.UnreadByte()
			return key{kind: keyEsc}, nil
		}
		seq, err := r.ReadByte()
		if err != nil {
			return key{kind: keyEsc}, nil
		}
		switch seq {
		case 'A':
			return key{kind: keyUp}, nil
		case 'B':
			return key{kind: keyDown}, nil
		case 'C':
			return key{kind: keyRight}, nil
		case 'D':
			return key{kind: keyLeft}, nil
		}
		return key{kind: keyOther}, nil
	default:
		if b < utf8.RuneSelf {
			return key{kind: keyRune, r: rune(b)}, nil
		}
		// Buffer the continuation bytes of a multi-byte UTF-8 rune instead of
		// emitting one keyRune per byte.
		buf := []byte{b}
		for !utf8.FullRune(buf) && len(buf) < utf8.UTFMax {
			next, err := r.ReadByte()
			if err != nil {
				break
			}
			buf = append(buf, next)
		}
		decoded, _ := utf8.DecodeRune(buf)
		return key{kind: keyRune, r: decoded}, nil
	}
}

// field renders a block of lines and, on a real terminal, redraws it in place
// when the state changes.
type field struct{ lines []string }

func (f *field) draw(lines []string) {
	width := termWidth()
	fitted := make([]string, len(lines))
	for i, l := range lines {
		fitted[i] = fitVisible(l, width)
	}
	if realTTY() && len(f.lines) > 0 {
		n := len(f.lines)
		// Move up to the first line of the block, clear every line while
		// walking down to the last one, then move back up so the new lines
		// are printed over the old ones in place. Skipping the final "move
		// back up" would shift the block down by n-1 lines on every redraw
		// and make the terminal scroll ("run away").
		_, _ = fmt.Fprintf(promptOutput, "\x1b[%dA", n)
		for i := range n {
			if i > 0 {
				_, _ = fmt.Fprint(promptOutput, "\n")
			}
			_, _ = fmt.Fprint(promptOutput, "\x1b[2K")
		}
		_, _ = fmt.Fprintf(promptOutput, "\r\x1b[%dA", n-1)
	}
	for _, l := range fitted {
		_, _ = fmt.Fprintln(promptOutput, l)
	}
	// Clear the leftover lines below when the new block is shorter.
	if old := len(f.lines); old > len(fitted) {
		for range old - len(fitted) {
			_, _ = fmt.Fprint(promptOutput, "\x1b[1B\x1b[2K")
		}
		_, _ = fmt.Fprintf(promptOutput, "\x1b[%dA", old-len(fitted))
	}
	f.lines = fitted
}
