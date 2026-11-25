// Pure WASM API: no syscall/js here, so the exact export logic can be tested
// and benchmarked natively. main.go is only the JS glue.
package main

import (
	"encoding/json"
	"sync"

	"github.com/DomainCraft/DomainCraft/internal/lexer"
	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"github.com/DomainCraft/DomainCraft/internal/validator"
)

type validationError struct {
	Entity  string `json:"entity,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
	Warning bool   `json:"warning"`
}

type validationResult struct {
	Errors []validationError `json:"errors"`
	Parse  bool              `json:"parse"`
}

// resultCache memoizes the last (input, JSON) pair. Studio re-runs validation
// on an unchanged model, so the second call is free. Only strings are retained:
// an earlier cache of *parser.RawSchema kept a large object graph alive and
// raised GC pressure for every other WASM export.
type resultCache struct {
	mu    sync.Mutex
	valid bool
	key   string
	value string
}

func (c *resultCache) get(key string, compute func() string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.valid && c.key == key {
		return c.value
	}
	value := compute()
	c.valid, c.key, c.value = true, key, value
	return value
}

var (
	validateCache resultCache
	domainCache   resultCache
)

// ValidationErrorResult is the JS-input-contract result for goValidate.
func ValidationErrorResult(message string) string {
	return marshal(validationResult{
		Errors: []validationError{{Message: message}},
		Parse:  true,
	})
}

// FieldErrorResult is the JS-input-contract result for goParseField and
// goParseDomain.
func FieldErrorResult(message string) string {
	return marshal(map[string]string{"error": message})
}

// ValidateYAML is the production path: main.go converts the js.Value once and
// calls this function directly, without a temporary []string.
func ValidateYAML(yaml string) string {
	return validateCache.get(yaml, func() string {
		schema, err := parser.ParseYAML([]byte(yaml))
		if err != nil {
			return marshal(validationResult{
				Errors: []validationError{{Message: err.Error()}},
				Parse:  true,
			})
		}
		errs := validator.New(schema).Validate()
		result := make([]validationError, 0, len(errs))
		for _, e := range errs {
			result = append(result, validationError{
				Entity:  e.Entity,
				Field:   e.Field,
				Message: e.Error(),
				Warning: e.Warning,
			})
		}
		return marshal(validationResult{Errors: result})
	})
}

// ParseFieldDefinition is the production path for goParseField.
func ParseFieldDefinition(def, name string) string {
	fd, err := lexer.ParseFieldString(def)
	if err != nil {
		return marshal(map[string]string{"error": err.Error()})
	}
	fd.Name = name
	// The Studio bridge expects Validations to be an object, never null.
	if fd.Validations == nil {
		fd.Validations = make(map[string]string)
	}
	return marshal(fd)
}

// ParseDomainYAML is the production path for goParseDomain.
func ParseDomainYAML(yaml string) string {
	return domainCache.get(yaml, func() string {
		raw, err := parser.ParseRawSchema([]byte(yaml))
		if err != nil {
			return marshal(map[string]string{"error": err.Error()})
		}
		return marshal(raw)
	})
}

// specmetaJSON is computed once: the constants are static, but marshaling the
// whole table on every call cost ~41 us and 160 allocations in the benchmark.
var specmetaJSON = sync.OnceValue(func() string {
	return marshal(specmeta.JSON())
})

// Specmeta mirrors window.goSpecmeta(): the full spec constant table as JSON.
func Specmeta() string {
	return specmetaJSON()
}

// Version mirrors window.goVersion(): the version stamped by -ldflags.
func Version(v string) string {
	return v
}

func marshal(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		data, _ = json.Marshal(map[string]string{"error": "marshal failed: " + err.Error()})
	}
	return string(data)
}
