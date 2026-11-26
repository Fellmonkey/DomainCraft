package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/parser"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"github.com/DomainCraft/DomainCraft/internal/validator"
	"github.com/DomainCraft/DomainCraft/pkg/logger"

	"github.com/spf13/cobra"
)

func applyAddons(opts *cliOptions, project *ir.Project, log *logger.Logger) {
	if strings.TrimSpace(opts.addons) == "" {
		return
	}
	if project.Addons == nil {
		project.Addons = make(map[string]bool)
	}
	for addon := range strings.SplitSeq(opts.addons, ",") {
		addon = strings.TrimSpace(addon)
		if addon == "" {
			continue
		}
		if specmeta.IsAddon(addon) {
			project.Addons[addon] = true
			continue
		}
		if log != nil {
			log.Warn("unknown addon %q ignored; valid addons: %s", addon, strings.Join(specmeta.Addons, ", "))
		}
	}
}

// loadAndValidate reports warnings on the command's error stream and returns
// hard validation errors as one aggregated error. The caller prints that error
// once; printing each error here as well would duplicate every line.
func loadAndValidate(opts *cliOptions, cmd *cobra.Command) (*parser.ParsedSchema, error) {
	data, err := os.ReadFile(opts.domainFile)
	if err != nil {
		return nil, fmt.Errorf("read domain file: %w", err)
	}
	schema, err := parser.ParseYAML(data)
	if err != nil {
		return nil, err
	}

	log := commandLogger(cmd)
	hardErrors := make([]error, 0)
	for _, e := range validator.New(schema).Validate() {
		if e.Warning {
			log.Warn("%s", e.Error())
			continue
		}
		hardErrors = append(hardErrors, e)
	}
	if len(hardErrors) > 0 {
		return nil, fmt.Errorf("validation failed with %d error(s): %w", len(hardErrors), errors.Join(hardErrors...))
	}

	return schema, nil
}
