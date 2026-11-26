// Package lexer parses field definition strings into structured definitions.
package lexer

import (
	"fmt"
	"regexp"
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

// callType describes one `kind(Target)` field declaration. The three
// variants differ only in kind, destination field and diagnostics.
type callType struct {
	kind          string
	prefix        string
	missingParen  string
	missingTarget string
	isRelation    bool
}

var callTypes = []callType{
	{specmeta.FieldTypeRelation, specmeta.FieldTypeRelation + "(", "relation type requires a closing parenthesis", "relation type requires a target entity name", true},
	{specmeta.FieldTypeArray, specmeta.FieldTypeArray + "(", "array type requires a closing parenthesis", "array type requires an element type", false},
	{specmeta.FieldTypeEnum, specmeta.FieldTypeEnum + "(", "enum type requires a closing parenthesis", "enum type requires a name", false},
}

// fieldFlags are the bare modifiers that set a boolean on the field. Keeping
// them in one table lets validation reuse the same vocabulary.
var fieldFlags = map[string]func(*FieldDefinition){
	"primary":  func(f *FieldDefinition) { f.IsPrimary = true },
	"optional": func(f *FieldDefinition) { f.IsOptional = true },
	"required": func(f *FieldDefinition) { f.IsRequired = true },
	"unique":   func(f *FieldDefinition) { f.IsUnique = true },
	"hidden":   func(f *FieldDefinition) { f.IsHidden = true },
	"readonly": func(f *FieldDefinition) { f.IsReadonly = true },
	"many":     func(f *FieldDefinition) { f.IsMany = true },
}

// parseType parses the type portion (e.g. "string", "relation(User)", "array(int)", "enum(Status)")
func parseType(typeStr string, fd *FieldDefinition) error {
	typeStr = strings.TrimSpace(typeStr)

	for _, spec := range callTypes {
		if !strings.HasPrefix(typeStr, spec.prefix) {
			continue
		}
		// A missing closing paren is an error, not a prefix to trim away.
		if !strings.HasSuffix(typeStr, ")") {
			return fmt.Errorf("%s", spec.missingParen)
		}
		target := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(typeStr, spec.prefix), ")"))
		if target == "" {
			return fmt.Errorf("%s", spec.missingTarget)
		}
		fd.Type = spec.kind
		if spec.isRelation {
			fd.TargetEntity = target
		} else {
			fd.TargetType = target
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
			case specmeta.IsValidationModifier(key, extraStringMods):
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
		if set, ok := fieldFlags[mod]; ok {
			set(fd)
			continue
		}
		return fmt.Errorf("unknown modifier flag: %s", mod)
	}

	return nil
}

// splitModifiers splits a modifier clause on commas while respecting square
// brackets (array default values like "[a, b, c]") and single/double quotes.
// Without this, "default:[a, b, c]" would be chopped at every comma and the
// default value would never round-trip through parse → serialize → parse.
func splitModifiers(s string) []string {
	return specmeta.SplitRespectingQuotes(s)
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
