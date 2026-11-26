package main

import (
	"encoding/json"
	"testing"

	"github.com/DomainCraft/DomainCraft/internal/lexer"
)

// TestMarshalFieldDefinitionMatchesEncodingJSON guards the hand-written writer
// in fieldjson.go. goParseField returns this JSON to the Studio, so escaping,
// field order and sorted map keys must stay byte-identical to encoding/json.
func TestMarshalFieldDefinitionMatchesEncodingJSON(t *testing.T) {
	cases := []struct {
		name string
		fd   lexer.FieldDefinition
	}{
		{
			name: "empty",
			fd:   lexer.FieldDefinition{},
		},
		{
			name: "all fields",
			fd: lexer.FieldDefinition{
				Name: "title", Type: "string", TargetEntity: "User", TargetType: "array(string)",
				IsPrimary: true, IsOptional: true, IsUnique: true, IsHidden: true,
				IsReadonly: true, IsRequired: true, OnDelete: "set_null", IsMany: true,
				OldName: "name", DefaultValue: "now()", DefaultIsFunc: true,
				Validations: map[string]string{"min": "2", "max": "255", "regex": "^[a-z]+$"},
			},
		},
		{
			name: "escaping",
			fd: lexer.FieldDefinition{
				Name:         "<tag> & \"quoted\" \\ path",
				Type:         "string",
				DefaultValue: "line\nbreak\ttab\r\x01\u2028\u2029\xff",
				Validations: map[string]string{
					"a": "1", "b": "2", "c": "3", "d": "4", "e": "5", "f": "6", "g": "7", "h": "8",
					"i": "9", "j": "10", "emoji": "🙂", "invalid": string([]byte{0xff}),
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := json.Marshal(&tc.fd)
			if err != nil {
				t.Fatal(err)
			}
			got := marshalFieldDefinition(&tc.fd)
			if got != string(want) {
				t.Fatalf("marshalFieldDefinition mismatch\n got: %s\nwant: %s", got, want)
			}
		})
	}
}
