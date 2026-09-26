package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/schemastore"
)

func validateCmd() *cobra.Command {
	var schemaRef string

	cmd := &cobra.Command{
		Use:   "validate file.yaml [file.yaml...]",
		Short: "Validate YAML files",
		Long:  "Validate YAML files.\nOptionally validate against a JSON schema (local file or http/https URL).\nSupports glob patterns like *.yaml.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Expand glob patterns.
			yamlPaths, err := expandPaths(args...)
			if err != nil {
				return err
			}

			// Build registry once for all files to enable cross-file schema caching.
			reg, err := buildRegistry(schemaRef)
			if err != nil {
				return err
			}

			var errs []error

			for _, yamlPath := range yamlPaths {
				err := validateFile(cmd.Context(), yamlPath, reg)
				if err != nil {
					errs = append(errs, err)

					continue
				}

				// A glob match takes its name from the file system, so
				// escape it the way the error handler renders one.
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: valid\n", escape.Control(yamlPath))
				if err != nil {
					errs = append(errs, fmt.Errorf("write the result of %s: %w", yamlPath, err))
				}
			}

			return errors.Join(errs...)
		},
	}

	cmd.Flags().StringVarP(&schemaRef, "schema", "s", "", "JSON schema file path or URL")

	return cmd
}

// validateFile validates every document of the file at yamlPath against the
// registry and joins what every document reports, so one run names each
// invalid document. Errors come back bound to the source, whose name is
// yamlPath as the user typed it, so each message opens with
// "path:line:col:" and the error handler in main renders the excerpt with
// the terminal width. The source's file path is absolute, so SchemaStore
// patterns that name parent directories, such as
// "**/.github/workflows/*.yml", match whatever the working directory is.
// When the read fails, no source exists to name the file, so validateFile
// puts the path in front of the error itself.
func validateFile(ctx context.Context, yamlPath string, reg *schema.Registry) error {
	absPath, err := filepath.Abs(yamlPath)
	if err != nil {
		// Abs fails only when the working directory is unreadable.
		absPath = yamlPath
	}

	// Resolvers route on the absolute path, and WithName keeps messages
	// naming the file as the user typed it. The read uses the typed path,
	// so a read error names the file that way too.
	source, err := niceyaml.NewSourceFromFile(yamlPath,
		niceyaml.WithName(yamlPath),
		niceyaml.WithFilePath(absPath),
	)
	if err != nil {
		return fmt.Errorf("%s: %w", yamlPath, err)
	}

	docs, err := source.Documents()
	if err != nil {
		return err
	}

	var errs []error

	for _, doc := range docs {
		err = doc.Validate(ctx, reg)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// buildRegistry creates a schema registry based on CLI flags.
//
// When schemaRef is set, it is the only resolver, so every document
// validates against it. The schema ref resolves relative to the current
// working directory.
//
// Otherwise the registry matches on schema directives first, and a
// directive's reference resolves relative to its own YAML file. A document
// that no directive claims falls through to SchemaStore's automatic
// discovery by file path. SchemaStore fetches its catalog on the first
// document that reaches it, and a file it cannot fetch the catalog for
// reports that in the file's error. Schema validation is optional here, so
// a document that no resolver claims passes rather than failing the file.
func buildRegistry(schemaRef string) (*schema.Registry, error) {
	// A loader applies to every document, so the CLI schema needs no matcher.
	// Resolve relative to current working directory. If cwd fails, use ".".
	if schemaRef != "" {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}

		ref, err := schema.FileOrURL(cwd, schemaRef)
		if err != nil {
			return nil, fmt.Errorf("--schema: %w", err)
		}

		return schema.NewRegistry(schema.WithResolvers(ref)), nil
	}

	return schema.NewRegistry(
		schema.WithResolvers(
			schema.Directive(), // Resolves schemas relative to each YAML file.
			schemastore.New(),  // Automatic discovery by absolute file path.
		),
		schema.WithRequireSchema(false),
	), nil
}
