//go:build js && wasm

// Package main exposes the Go parser and validator to the browser GUI.
package main

import (
	"fmt"
	"syscall/js"
)

var version = "dev" // set via -ldflags "-X main.version=v0.2.0"

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
	return Version(version)
}

func specmetaFunc(_ js.Value, _ []js.Value) any {
	return Specmeta()
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
		return ValidationErrorResult(err.Error())
	}
	return ValidateYAML(raw)
}

func parseField(_ js.Value, args []js.Value) any {
	def, err := stringArg(args, 0, "field definition")
	if err != nil {
		return FieldErrorResult(err.Error())
	}
	name := ""
	if len(args) > 1 {
		name, err = stringArg(args, 1, "field name")
		if err != nil {
			return FieldErrorResult(err.Error())
		}
	}
	return ParseFieldDefinition(def, name)
}

func parseDomain(_ js.Value, args []js.Value) any {
	input, err := stringArg(args, 0, "YAML input")
	if err != nil {
		return FieldErrorResult(err.Error())
	}
	return ParseDomainYAML(input)
}
