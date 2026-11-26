package lexer

import (
	"strings"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/specmeta"
)

func TestParseArrayTypeUsesSpecmetaInner(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "primitive element", in: "array(int)", want: "int"},
		{name: "enum element", in: "array(Status)", want: "Status"},
		{name: "whitespace is trimmed", in: "array( uuid )", want: "uuid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fd, err := ParseFieldString(c.in)
			if err != nil {
				t.Fatalf("ParseFieldString(%q) error = %v", c.in, err)
			}
			if fd.Type != "array" {
				t.Errorf("Type = %q, want array", fd.Type)
			}
			if fd.TargetType != c.want {
				t.Errorf("TargetType = %q, want %q", fd.TargetType, c.want)
			}
			canonicalInner := strings.TrimSpace(specmeta.ParseArrayInner(strings.TrimSpace(c.in)))
			if fd.TargetType != canonicalInner {
				t.Errorf("TargetType = %q, want canonical specmeta inner %q", fd.TargetType, canonicalInner)
			}
		})
	}
}

func TestLexerErrorMessagesUseSpecmetaLists(t *testing.T) {
	t.Parallel()
	t.Run("unknown type lists every declared field type", func(t *testing.T) {
		t.Parallel()
		_, err := ParseFieldString("nope")
		if err == nil {
			t.Fatal("expected error for unknown type")
		}
		want := strings.Join(specmeta.FieldTypes, ", ")
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	})
	t.Run("unknown on_delete lists every declared behavior", func(t *testing.T) {
		t.Parallel()
		_, err := ParseFieldString("relation(User) [on_delete:nope]")
		if err == nil {
			t.Fatal("expected error for unknown on_delete value")
		}
		want := strings.Join(specmeta.OnDeleteValues, ", ")
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	})
}

func TestLexerOldNameUsesSharedIdentifierRule(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		oldName string
		wantErr bool
	}{
		{name: "valid old name", oldName: "previousTitle"},
		{name: "leading digit rejected", oldName: "1Title", wantErr: true},
		{name: "hyphen rejected", oldName: "old-title", wantErr: true},
		{name: "non-ASCII rejected", oldName: "староеИмя", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseFieldString("string [old_name:" + c.oldName + "]")
			if (err != nil) != c.wantErr {
				t.Fatalf("ParseFieldString(old_name:%q) error = %v, wantErr %v", c.oldName, err, c.wantErr)
			}
			if specmeta.IsValidIdentifier(c.oldName) == c.wantErr {
				t.Errorf("shared identifier rule disagrees for %q", c.oldName)
			}
		})
	}
}
