package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/schemastore"
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
			yamlPaths, err := expandPaths(args...)
			if err != nil {
				return err
			}

			// Build registry once for all files to enable cross-file schema caching.
			reg := buildRegistry(schemaRef)

			var errs []error

			for _, yamlPath := range yamlPaths {
				err := validateFile(cmd.Context(), yamlPath, reg)
				if err != nil {
					errs = append(errs, err)
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
// registry and joins what every document reports, so one run names each
// invalid document. Errors come back bound to the source, whose name is the
// file path, so each message opens with "path:line:col:" and the error
// handler in main renders the excerpt with the terminal width. A file that
// cannot be read has no source to name it, so the path goes in front here.
func validateFile(ctx context.Context, yamlPath string, reg *schema.Registry) error {
	source, err := niceyaml.NewSourceFromFile(yamlPath)
	if err != nil {
		return fmt.Errorf("%s: %w", yamlPath, err)
	}

	docs, err := source.Documents()
	if err != nil {
		return err
	}

	var errs []error

	for _, doc := range docs {
		err = reg.Validate(ctx, doc)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
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
func buildRegistry(schemaRef string) *schema.Registry {
	reg := schema.NewRegistry()

	// A loader applies to every document, so the CLI schema needs no matcher.
	// Resolve relative to current working directory. If cwd fails, use ".".
	if schemaRef != "" {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}

		reg.Register(schema.FileOrURL(cwd, schemaRef))

		return reg
	}

	reg.Register(
		schema.Directive(), // Resolves schemas relative to each YAML file.
		schemastore.New(),  // Automatic discovery by file path.
	)

	return reg
}
