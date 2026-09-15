package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
	"go.jacobcolvin.com/niceyaml/schema/loader"
	"go.jacobcolvin.com/niceyaml/schema/registry"
	"go.jacobcolvin.com/niceyaml/schema/registry/schemastore"
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

// validateFile validates every document of the file at yamlPath against the
// registry. Errors come back bound to the source, and the error handler in
// main renders them with the terminal width.
func validateFile(ctx context.Context, yamlPath string, reg *registry.Registry) error {
	source, err := niceyaml.NewSourceFromFile(yamlPath)
	if err != nil {
		return err
	}

	docs, err := source.Documents()
	if err != nil {
		return source.WrapError(err)
	}

	for i, doc := range docs.All() {
		err = reg.Validate(ctx, doc)
		if err != nil {
			return source.WrapError(fmt.Errorf("document %d: %w", i, err))
		}
	}

	return nil
}

// buildRegistry creates a schema registry based on CLI flags.
//
// When schemaRef is set, it is the only registration, so every document
// validates against it (the CLI flag takes precedence). The schema ref
// resolves relative to the current working directory.
//
// Otherwise, directive-based matching is enabled with per-file resolution
// (schemas referenced in directives are resolved relative to each YAML file),
// followed by SchemaStore automatic discovery. The SchemaStore catalog is
// fetched on the first document that reaches it, and a file it cannot
// match, or cannot fetch the catalog for, reports that in the file's error.
func buildRegistry(schemaRef string) *registry.Registry {
	reg := registry.New()

	// A loader applies to every document, so the CLI schema needs no matcher.
	// Resolve relative to current working directory. If cwd fails, use ".".
	if schemaRef != "" {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}

		reg.Register(loader.FileOrURL(cwd, schemaRef))

		return reg
	}

	reg.Register(
		registry.Directive(), // Resolves schemas relative to each YAML file.
		schemastore.New(),    // Automatic discovery by file path.
	)

	return reg
}
