package lexer

import (
	"strings"
	"testing"
)

func FuzzParseFieldString(f *testing.F) {
	for _, seed := range []string{
		"string [required, max:255]",
		"uuid [primary",
		"relation(User) [many, on_delete:cascade]",
		"array(int) [default:[1, 2]]",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		field, err := New(input).Parse()
		if err == nil && field == nil {
			t.Fatalf("Parse(%q) returned neither a field nor an error", input)
		}
	})
}

func TestUnterminatedModifierList(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "missing closing bracket", input: "uuid [primary", wantErr: true},
		{name: "closed bracket", input: "uuid [primary]"},
		{name: "array default keeps its brackets", input: `string [default:[a, b]]`},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(c.input).Parse()
			if c.wantErr {
				if err == nil || !strings.Contains(err.Error(), "unterminated modifier list") {
					t.Fatalf("%q error = %v, want unterminated modifier list", c.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%q unexpected error: %v", c.input, err)
			}
		})
	}
}
