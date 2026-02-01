package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
	"go.jacobcolvin.com/niceyaml/schema/loader"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
	"go.jacobcolvin.com/niceyaml/schema/registry"
)

func validateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate file.yaml [file.yaml...]",
		Short: "Validate YAML files",
		Long:  "Validate YAML files.\nOptionally validate against a JSON schema (local file or http/https URL).\nSupports glob patterns like *.yaml.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			schemaRef, err := cmd.Flags().GetString("schema")
			if err != nil {
				return fmt.Errorf("get schema flag: %w", err)
			}

			// Expand glob patterns.
			yamlPaths, err := filepaths.Expand(args...)
			if err != nil {
				return err
			}

			// Build registry once for all files to enable cross-file schema caching.
			reg := buildRegistry(schemaRef)

			var errs []error

			for _, yamlPath := range yamlPaths {
				err := validateFile(cmd.Context(), yamlPath, reg)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", yamlPath, err))
				} else {
					fmt.Printf("%s: valid\n", yamlPath)
				}
			}

			return errors.Join(errs...)
		},
	}

	cmd.Flags().StringP("schema", "s", "", "JSON schema file path or URL")

	return cmd
}

func getTerminalWidth() int {
	width := 90

	if term.IsTerminal(os.Stderr.Fd()) {
		w, _, err := term.GetSize(os.Stderr.Fd())
		if err == nil {
			width = w
		}
	}

	return max(0, width-2)
}

func validateFile(ctx context.Context, yamlPath string, reg *registry.Registry) error {
	source, err := niceyaml.NewSourceFromFile(
		yamlPath,
		niceyaml.WithErrorOptions(
			niceyaml.WithWidthFunc(getTerminalWidth),
		),
	)
	if err != nil {
		return err
	}

	decoder, err := source.Decoder()
	if err != nil {
		return err
	}

	for i, doc := range decoder.Documents() {
		err = reg.ValidateDocument(ctx, doc)
		if err != nil {
			return source.WrapError(fmt.Errorf("document %d: %w", i, err))
		}
	}

	return nil
}

// buildRegistry creates a schema registry based on CLI flags.
//
// If schemaRef is provided, it's registered first with a matcher that always
// matches (CLI flag takes precedence). The schema ref is resolved relative to
// the current working directory.
//
// Otherwise, directive-based matching is enabled with per-file resolution
// (schemas referenced in directives are resolved relative to each YAML file).
func buildRegistry(schemaRef string) *registry.Registry {
	reg := registry.New()

	// CLI schema flag takes precedence - register first with always-matching.
	// Resolve relative to current working directory. If cwd fails, use ".".
	if schemaRef != "" {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}

		reg.RegisterFunc(
			matcher.Always(),
			loader.Ref(cwd, schemaRef),
		)

		return reg
	}

	// Directive-based matching when no CLI flag provided.
	// Directive resolves schemas relative to each YAML file.
	reg.Register(registry.Directive())

	return reg
}
