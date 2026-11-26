package main

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/DomainCraft/DomainCraft/internal/lexer"
)

// marshalFieldDefinition writes the JSON contract of lexer.FieldDefinition
// directly. goParseField runs on every Studio keystroke, and the generic
// encoder spends most of that call reflecting over the 17-field struct. The
// key order and escaping below are exactly encoding/json's: fields in
// declaration order (no JSON tags on FieldDefinition), map keys sorted by
// UTF-8 bytes, HTML characters and U+2028/U+2029 escaped, invalid UTF-8
// replaced with U+FFFD.
func marshalFieldDefinition(fd *lexer.FieldDefinition) string {
	var b strings.Builder
	size := 260 + len(fd.Name) + len(fd.Type) + len(fd.TargetEntity) + len(fd.TargetType) +
		len(fd.OnDelete) + len(fd.OldName) + len(fd.DefaultValue)
	for k, v := range fd.Validations {
		size += len(k) + len(v) + 8
	}
	b.Grow(size)

	b.WriteString(`{"Name":`)
	writeJSONString(&b, fd.Name)
	b.WriteString(`,"Type":`)
	writeJSONString(&b, fd.Type)
	b.WriteString(`,"TargetEntity":`)
	writeJSONString(&b, fd.TargetEntity)
	b.WriteString(`,"TargetType":`)
	writeJSONString(&b, fd.TargetType)
	b.WriteString(`,"IsPrimary":`)
	writeBool(&b, fd.IsPrimary)
	b.WriteString(`,"IsOptional":`)
	writeBool(&b, fd.IsOptional)
	b.WriteString(`,"IsUnique":`)
	writeBool(&b, fd.IsUnique)
	b.WriteString(`,"IsHidden":`)
	writeBool(&b, fd.IsHidden)
	b.WriteString(`,"IsReadonly":`)
	writeBool(&b, fd.IsReadonly)
	b.WriteString(`,"IsRequired":`)
	writeBool(&b, fd.IsRequired)
	b.WriteString(`,"OnDelete":`)
	writeJSONString(&b, fd.OnDelete)
	b.WriteString(`,"IsMany":`)
	writeBool(&b, fd.IsMany)
	b.WriteString(`,"OldName":`)
	writeJSONString(&b, fd.OldName)
	b.WriteString(`,"Validations":`)
	if fd.Validations == nil {
		b.WriteString(`null`)
	} else {
		b.WriteByte('{')
		// Most definitions have <= 8 validation modifiers. A fixed-size local
		// buffer keeps the common case stack-allocated; slices.Sorted(maps.Keys())
		// allocates an iterator closure and a slice on every call.
		var scratch [8]string
		keys := scratch[:0]
		for k := range fd.Validations {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(&b, k)
			b.WriteByte(':')
			writeJSONString(&b, fd.Validations[k])
		}
		b.WriteByte('}')
	}
	b.WriteString(`,"DefaultValue":`)
	writeJSONString(&b, fd.DefaultValue)
	b.WriteString(`,"DefaultIsFunc":`)
	writeBool(&b, fd.DefaultIsFunc)
	b.WriteByte('}')
	return b.String()
}

func writeBool(b *strings.Builder, v bool) {
	if v {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
}

const hexDigits = "0123456789abcdef"

// writeJSONString mirrors encoding/json's string escaping byte for byte.
func writeJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' && c != '<' && c != '>' && c != '&' {
				i++
				continue
			}
			b.WriteString(s[start:i])
			switch c {
			case '\\', '"':
				b.WriteByte('\\')
				b.WriteByte(c)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			default:
				b.WriteString(`\u00`)
				b.WriteByte(hexDigits[c>>4])
				b.WriteByte(hexDigits[c&0xf])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteString(s[start:i])
			b.WriteString("\xef\xbf\xbd")
			i++
			start = i
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			b.WriteString(s[start:i])
			b.WriteString(`\u202`)
			b.WriteByte(hexDigits[r&0xf])
			i += size
			start = i
			continue
		}
		i += size
	}
	b.WriteString(s[start:])
	b.WriteByte('"')
}
