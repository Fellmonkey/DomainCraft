//go:build js && wasm

// Package main exposes the Go parser and validator to the browser GUI.
package main

import (
	"encoding/json"
	"fmt"
	"syscall/js"

	"github.com/DomainCraft/DomainCraft/internal/lexer"
	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"github.com/DomainCraft/DomainCraft/internal/validator"
)

var version = "dev" // set via -ldflags "-X main.version=v0.2.0"

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

func main() {
	c := make(chan struct{})
	js.Global().Set("goValidate", js.FuncOf(validate))
	js.Global().Set("goParseField", js.FuncOf(parseField))
	js.Global().Set("goParseDomain", js.FuncOf(parseDomain))
	js.Global().Set("goSpecmeta", js.FuncOf(specmetaFunc))
	js.Global().Set("goVersion", js.FuncOf(versionFunc))
	<-c
}

func versionFunc(_ js.Value, _ []js.Value) any {
	return version
}

func specmetaFunc(_ js.Value, _ []js.Value) any {
	return marshal(specmeta.JSON())
}

// stringArg validates the JS API contract. Without a type check an object
// becomes "[object Object]" and surfaces as a confusing parse error in the GUI.
func stringArg(args []js.Value, i int, label string) (string, error) {
	if len(args) <= i {
		return "", fmt.Errorf("no %s provided", label)
	}
	if args[i].Type() != js.TypeString {
		return "", fmt.Errorf("%s must be a string", label)
	}
	return args[i].String(), nil
}

func validate(_ js.Value, args []js.Value) any {
	raw, err := stringArg(args, 0, "YAML input")
	if err != nil {
		return marshal(validationResult{
			Errors: []validationError{{Message: err.Error()}},
			Parse:  true,
		})
	}
	schema, err := parser.ParseYAML([]byte(raw))
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
}

func parseField(_ js.Value, args []js.Value) any {
	def, err := stringArg(args, 0, "field definition")
	if err != nil {
		return marshal(map[string]string{"error": err.Error()})
	}
	name := ""
	if len(args) > 1 {
		name, err = stringArg(args, 1, "field name")
		if err != nil {
			return marshal(map[string]string{"error": err.Error()})
		}
	}
	fd, err := lexer.ParseFieldString(def)
	if err != nil {
		return marshal(map[string]string{"error": err.Error()})
	}
	fd.Name = name
	return marshal(fd)
}

func parseDomain(_ js.Value, args []js.Value) any {
	input, err := stringArg(args, 0, "YAML input")
	if err != nil {
		return marshal(map[string]string{"error": err.Error()})
	}
	raw, err := parser.ParseRawSchema([]byte(input))
	if err != nil {
		return marshal(map[string]string{"error": err.Error()})
	}
	return marshal(raw)
}

func marshal(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		data, _ = json.Marshal(map[string]string{"error": "marshal failed: " + err.Error()})
	}
	return string(data)
}
