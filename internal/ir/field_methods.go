package ir

import "strings"

// validation is the single case-insensitive lookup behind both public helpers.
func (f Field) validation(name string) (string, bool) {
	for _, validation := range f.Validations {
		if strings.EqualFold(validation.Name, name) {
			return validation.Value, true
		}
	}
	return "", false
}

// HasValidation reports whether the field has the named validation.
func (f Field) HasValidation(name string) bool {
	_, ok := f.validation(name)
	return ok
}

// ValidationValue returns the first validation value for the given name.
func (f Field) ValidationValue(name string) string {
	value, _ := f.validation(name)
	return value
}
