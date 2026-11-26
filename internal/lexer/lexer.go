// Package lexer parses field definition strings into structured definitions.
package lexer

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/DomainCraft/DomainCraft/internal/specmeta"
)

// FieldDefinition represents a parsed field definition
type FieldDefinition struct {
	Name         string // field name (set by the entity parser)
	Type         string // string, int, uuid, relation, enum, array, json, text, etc.
	TargetEntity string // for relation(EntityName)
	TargetType   string // for array(Type) or enum(Name)

	// Modifiers
	IsPrimary  bool
	IsOptional bool
	IsUnique   bool
	IsHidden   bool
	IsReadonly bool
	IsRequired bool

	// Relations
	OnDelete string // cascade, set_null, restrict, no_action
	IsMany   bool   // for many-to-many (parser infers RelationType from this)

	// OldName is a rename hint for the migration engine: the field was previously
	// named OldName (e.g. `name: string [required, old_name: title]`). Bridges use
	// it to generate a safe RenameColumn instead of DropColumn + AddColumn, which
	// would destroy the column's data on a real database.
	OldName string

	// Validation
	Validations   map[string]string // min, max, email, url, regex, gte, lt, lte, gt
	DefaultValue  string
	DefaultIsFunc bool // true if default:now()
}

// Parse parses a full field definition: "string [required, max:255]"
func Parse(input string) (*FieldDefinition, error) {
	return parse(input, nil)
}

// ParseWithStringModifiers parses input with additional string validation
// modifier names. It is the explicit injection point for a caller that extends
// the spec; no package-level state is mutated.
func ParseWithStringModifiers(input string, extraStringMods []string) (*FieldDefinition, error) {
	return parse(input, extraStringMods)
}

func parse(input string, extraStringMods []string) (*FieldDefinition, error) {
	input = strings.TrimSpace(input)
	fd := &FieldDefinition{
		Validations: make(map[string]string),
	}

	// Split into type and modifiers: "string [required, max:255]". Split at the
	// first "[" only; the rest (modifiers, which may themselves contain brackets,
	// e.g. default values like "[a, b, c]") is handled by parseModifiers.
	parts := strings.SplitN(input, "[", 2)

	typePart := strings.TrimSpace(parts[0])
	if typePart == "" {
		return nil, fmt.Errorf("empty field type: %s", input)
	}

	// Parse the type
	if err := parseType(typePart, fd); err != nil {
		return nil, err
	}

	// Parse modifiers if present
	if len(parts) > 1 {
		modifierStr := strings.TrimSpace(parts[1])
		if !strings.HasSuffix(modifierStr, "]") {
			return nil, fmt.Errorf("unterminated modifier list in %q: expected ']'", input)
		}
		modifierStr = strings.TrimSuffix(modifierStr, "]")
		if err := parseModifiers(modifierStr, fd, extraStringMods); err != nil {
			return nil, err
		}
	}

	// Validate logic
	if err := fd.Validate(); err != nil {
		return nil, err
	}

	return fd, nil
}

// parseType parses the type portion (e.g. "string", "relation(User)", "array(int)", "enum(Status)")
func parseType(typeStr string, fd *FieldDefinition) error {
	typeStr = strings.TrimSpace(typeStr)

	// Check for relation(EntityName). A missing closing paren is an error, not
	// a prefix to be trimmed away.
	if strings.HasPrefix(typeStr, specmeta.FieldTypeRelation+"(") {
		if !strings.HasSuffix(typeStr, ")") {
			return fmt.Errorf("relation type requires a closing parenthesis")
		}
		fd.Type = specmeta.FieldTypeRelation
		targetEntity := strings.TrimSuffix(strings.TrimPrefix(typeStr, specmeta.FieldTypeRelation+"("), ")")
		fd.TargetEntity = strings.TrimSpace(targetEntity)
		if fd.TargetEntity == "" {
			return fmt.Errorf("relation type requires a target entity name")
		}
		return nil
	}

	// Check for array(Type). specmeta owns the element-type extraction; the
	// closing parenthesis is checked here once so an unclosed declaration
	// gets its own diagnostic instead of falling through to "unknown type".
	if strings.HasPrefix(typeStr, specmeta.FieldTypeArray+"(") {
		if !strings.HasSuffix(typeStr, ")") {
			return fmt.Errorf("array type requires a closing parenthesis")
		}
		fd.Type = specmeta.FieldTypeArray
		fd.TargetType = strings.TrimSpace(specmeta.ParseArrayInner(typeStr))
		if fd.TargetType == "" {
			return fmt.Errorf("array type requires an element type")
		}
		return nil
	}

	// Check for enum(Name)
	if strings.HasPrefix(typeStr, specmeta.FieldTypeEnum+"(") {
		if !strings.HasSuffix(typeStr, ")") {
			return fmt.Errorf("enum type requires a closing parenthesis")
		}
		fd.Type = specmeta.FieldTypeEnum
		enumName := strings.TrimSuffix(strings.TrimPrefix(typeStr, specmeta.FieldTypeEnum+"("), ")")
		fd.TargetType = strings.TrimSpace(enumName)
		if fd.TargetType == "" {
			return fmt.Errorf("enum type requires a name")
		}
		return nil
	}

	// Built-in types
	if !specmeta.IsFieldType(typeStr) {
		return fmt.Errorf("unknown type %q; valid types: %s", typeStr, strings.Join(specmeta.FieldTypes, ", "))
	}

	fd.Type = typeStr
	return nil
}

// parseModifiers parses modifiers inside square brackets
func parseModifiers(modStr string, fd *FieldDefinition, extraStringMods []string) error {
	modifiers := splitModifiers(modStr)

	for _, mod := range modifiers {
		mod = strings.TrimSpace(mod)
		if mod == "" {
			continue
		}

		// Key:value modifiers
		if strings.Contains(mod, ":") {
			parts := strings.SplitN(mod, ":", 2)
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			// Strip quotes if present
			value = strings.Trim(value, `"'`)

			switch {
			case specmeta.IsStringValidationModifier(key) || slices.Contains(extraStringMods, key) || specmeta.IsNumericValidationModifier(key):
				fd.Validations[key] = value
			case key == "default":
				// Check for function-style defaults like now(), uuid()
				if strings.HasSuffix(value, "()") {
					fd.DefaultIsFunc = true
					fd.DefaultValue = value[:len(value)-2] // strip ()
				} else {
					fd.DefaultValue = value
				}
			case key == "on_delete":
				if !specmeta.IsOnDeleteValue(value) {
					return fmt.Errorf("unknown on_delete value: %s. valid: %s", value, strings.Join(specmeta.OnDeleteValues, ", "))
				}
				fd.OnDelete = value
			case key == "old_name":
				// Rename hint: `name: string [required, old_name: title]`. The value is
				// the field's previous name; the migration engine + bridges turn it into
				// a safe column rename instead of a destructive drop + add.
				if value == "" {
					return fmt.Errorf("old_name requires a previous field name")
				}
				fd.OldName = value
			default:
				return fmt.Errorf("unknown modifier: %s", key)
			}
			continue
		}

		// Bare string flags live in specmeta alongside their key:value forms.
		if specmeta.IsStringFlagValidationModifier(mod) {
			fd.Validations[mod] = "true"
			continue
		}

		// Flag modifiers (no value)
		switch mod {
		case "primary":
			fd.IsPrimary = true
		case "optional":
			fd.IsOptional = true
		case "required":
			fd.IsRequired = true
		case "unique":
			fd.IsUnique = true
		case "hidden":
			fd.IsHidden = true
		case "readonly":
			fd.IsReadonly = true
		case "many":
			fd.IsMany = true
		default:
			return fmt.Errorf("unknown modifier flag: %s", mod)
		}
	}

	return nil
}

// splitModifiers splits a modifier clause on commas while respecting square
// brackets (array default values like "[a, b, c]") and single/double quotes.
// Without this, "default:[a, b, c]" would be chopped at every comma and the
// default value would never round-trip through parse → serialize → parse.
func splitModifiers(s string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			cur.WriteByte(c)
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			cur.WriteByte(c)
		case '[':
			depth++
			cur.WriteByte(c)
		case ']':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
		case ',':
			if depth == 0 {
				out = append(out, cur.String())
				cur.Reset()
				continue
			}
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, cur.String())
	return out
}

// Validate checks the logical consistency of a FieldDefinition
func (fd *FieldDefinition) Validate() error {
	// Cannot be both primary and optional
	if fd.IsPrimary && fd.IsOptional {
		return fmt.Errorf("field cannot be both primary and optional")
	}

	// Cannot be both required and optional
	if fd.IsRequired && fd.IsOptional {
		return fmt.Errorf("field cannot be both required and optional")
	}

	// on_delete is only valid on relation fields
	if fd.OnDelete != "" && fd.Type != specmeta.FieldTypeRelation {
		return fmt.Errorf("on_delete is only valid on relation fields")
	}

	// many modifier is only valid on relation fields
	if fd.IsMany && fd.Type != specmeta.FieldTypeRelation {
		return fmt.Errorf("many modifier is only valid on relation fields")
	}

	// on_delete:set_null is only valid for optional relation fields
	if fd.Type == specmeta.FieldTypeRelation && fd.OnDelete == "set_null" && !fd.IsOptional {
		return fmt.Errorf("on_delete:set_null is only allowed for optional relation fields")
	}

	// Validate regex if present
	if regex, ok := fd.Validations["regex"]; ok {
		if _, err := regexp.Compile(regex); err != nil {
			return fmt.Errorf("invalid regex %q: %w", regex, err)
		}
	}

	// old_name must be a valid identifier (like the field name itself).
	if fd.OldName != "" && !specmeta.IsValidIdentifier(fd.OldName) {
		return fmt.Errorf("old_name %q is not a valid field name", fd.OldName)
	}

	return nil
}

// ParseFieldString is a backwards-compatible alias for Parse.
func ParseFieldString(fieldString string) (*FieldDefinition, error) {
	return Parse(fieldString)
}
