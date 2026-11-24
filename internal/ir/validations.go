package ir

import (
	"cmp"
	"slices"
	"strings"
)

// ValidationRule is a normalized, language-agnostic validation rule. The core
// derives these from the field's type, nullability and the declared validation
// modifiers, so a bridge never re-parses modifier names or re-derives which
// modifier is a length bound vs a numeric bound. It just maps each Kind (and its
// stable Code) to its own syntax (DataAnnotations, FluentValidation,
// class-validator, etc.).
type ValidationRule struct {
	// Kind is the semantic rule kind: required, email, url, ipv4, regex,
	// min_length, max_length, min_value, max_value.
	Kind string
	// Code is the stable, machine-readable identifier for the rule, shared
	// across bridges (REQUIRED, EMAIL, URL, IPV4, REGEX, MIN_LENGTH, MAX_LENGTH,
	// MIN_VALUE, MAX_VALUE). Bridges that carry per-rule codes natively
	// (FluentValidation .WithErrorCode, class-validator) use this directly.
	Code string
	// Value is the rule parameter ("" for flags like required/email/url/ipv4):
	//   regex      -> the pattern
	//   min_length -> the minimum string length
	//   max_length -> the maximum string length
	//   min_value  -> the lower numeric bound (inclusive unless Exclusive)
	//   max_value  -> the upper numeric bound (inclusive unless Exclusive)
	Value string
	// Exclusive marks a non-inclusive numeric bound (the `gt`/`lt` modifiers).
	// min_value/max_value are inclusive by default; Exclusive flips the bound so a
	// consumer (seed generator, FluentValidation, class-validator, ...) can express
	// a strict greater-than / less-than without re-deriving it from the modifier name.
	Exclusive bool
	// Message is a stable, human-readable default message with the rule's value
	// already interpolated. Bridges use it verbatim (e.g. DataAnnotations
	// ErrorMessage) so the same rule renders the same text on every backend.
	Message string
}

// validationRuleMeta returns the stable wire code and default message for a
// normalized rule kind. The value is interpolated into length/numeric messages.
func validationRuleMeta(kind, value string, exclusive bool) (code, message string) {
	switch kind {
	case "required":
		return "REQUIRED", "is required"
	case "email":
		return "EMAIL", "must be a valid email address"
	case "url":
		return "URL", "must be a valid URL"
	case "ipv4":
		return "IPV4", "must be a valid IPv4 address"
	case "regex":
		return "REGEX", "must match the required format"
	case "min_length":
		return "MIN_LENGTH", "must be at least " + value + " characters"
	case "max_length":
		return "MAX_LENGTH", "must be at most " + value + " characters"
	case "min_value":
		if exclusive {
			return "MIN_VALUE", "must be greater than " + value
		}
		return "MIN_VALUE", "must be greater than or equal to " + value
	case "max_value":
		if exclusive {
			return "MAX_VALUE", "must be less than " + value
		}
		return "MAX_VALUE", "must be less than or equal to " + value
	}
	return strings.ToUpper(kind), ""
}

// newRule builds a normalized rule with its stable code and default message.
func newRule(kind, value string, exclusive bool) ValidationRule {
	code, message := validationRuleMeta(kind, value, exclusive)
	return ValidationRule{Kind: kind, Code: code, Value: value, Exclusive: exclusive, Message: message}
}

// validationRuleOrder is the semantic rank used to order normalized rules.
// The order is required → format (email/url/ipv4/regex) → length bounds
// (min→max) → numeric bounds (min→max), independent of the order in which the
// modifiers were declared (convertValidations sorts keys alphabetically, so
// declaration order cannot be relied on here).
func validationRuleOrder(kind string) int {
	switch kind {
	case "required":
		return 0
	case "email":
		return 1
	case "url":
		return 2
	case "ipv4":
		return 3
	case "regex":
		return 4
	case "min_length":
		return 5
	case "max_length":
		return 6
	case "min_value":
		return 7
	case "max_value":
		return 8
	default:
		return 9
	}
}

// ValidationRules returns the field's validations as a normalized, ordered list
// of rules. The order is stable and semantic: required, then format validators
// (email/url/ipv4/regex), then length bounds (min_length → max_length), then
// numeric bounds (min_value → max_value, inclusive or exclusive). Rules of the
// same kind keep their relative input order.
//
// Mapping from the raw modifiers:
//
//	required (from the field not being nullable / the "required" modifier)
//	email / url / ipv4 / regex   (string validators, unchanged)
//	min / max                    -> min_length / max_length on string types,
//	                               min_value / max_value on numeric types
//	gte / lte                   -> inclusive min_value / max_value
//	gt / lt                     -> min_value / max_value with Exclusive=true
//	min_length / max_length /   -> the canonical rule kind, as-is
//	min_value / max_value
func (f Field) ValidationRules() []ValidationRule {
	rules := make([]ValidationRule, 0, len(f.Validations)+1)

	if !f.IsNullable && !f.IsPrimary {
		// Non-nullable fields are required by construction. (A nullable field
		// may still declare an explicit `required` modifier, handled below.)
		rules = append(rules, newRule("required", "", false))
	}

	for _, v := range f.Validations {
		name := strings.ToLower(v.Name)
		switch name {
		case "required":
			// Deduplicate: a non-nullable field already carries required.
			if !f.IsNullable && !f.IsPrimary {
				continue
			}
			rules = append(rules, newRule("required", "", false))
		case "email", "url", "ipv4":
			rules = append(rules, newRule(name, "", false))
		case "regex":
			rules = append(rules, newRule("regex", v.Value, false))
		case "min_length", "max_length", "min_value", "max_value":
			// Canonical rule names are accepted as-is so IR fields built
			// directly (tests, bridge-side helpers) use the same vocabulary.
			rules = append(rules, newRule(name, v.Value, false))
		case "min", "max":
			if f.IsNumeric() {
				rules = append(rules, newRule(name+"_value", v.Value, false))
			} else {
				rules = append(rules, newRule(name+"_length", v.Value, false))
			}
		case "gte":
			rules = append(rules, newRule("min_value", v.Value, false))
		case "gt":
			rules = append(rules, newRule("min_value", v.Value, true))
		case "lte":
			rules = append(rules, newRule("max_value", v.Value, false))
		case "lt":
			rules = append(rules, newRule("max_value", v.Value, true))
		}
	}

	// Stable sort keeps same-rank rules in declaration order while enforcing
	// the semantic contract above.
	slices.SortStableFunc(rules, func(a, b ValidationRule) int {
		return cmp.Compare(validationRuleOrder(a.Kind), validationRuleOrder(b.Kind))
	})

	return rules
}
