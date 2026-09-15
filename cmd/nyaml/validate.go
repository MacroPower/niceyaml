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
			reg := buildRegistry(cmd.Context(), schemaRef)

			// Build the error printer once: it carries the terminal width and
			// a full theme, neither of which changes between files.
			errPrinter := niceyaml.NewPrinter(niceyaml.WithWidth(getTerminalWidth()))

			var errs []error

			for _, yamlPath := range yamlPaths {
				err := validateFile(cmd.Context(), yamlPath, reg, errPrinter)
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

func validateFile(
	ctx context.Context,
	yamlPath string,
	reg *registry.Registry,
	errPrinter *niceyaml.Printer,
) error {
	source, err := niceyaml.NewSourceFromFile(
		yamlPath,
		niceyaml.WithErrorOptions(
			niceyaml.WithPrinter(errPrinter),
		),
	)
	if err != nil {
		return err
	}

	decoder, err := source.Decoder()
	if err != nil {
		return source.WrapError(err)
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
// When schemaRef is set, it is the only registration, so every document
// validates against it (the CLI flag takes precedence). The schema ref
// resolves relative to the current working directory.
//
// Otherwise, directive-based matching is enabled with per-file resolution
// (schemas referenced in directives are resolved relative to each YAML file),
// followed by SchemaStore automatic discovery.
func buildRegistry(ctx context.Context, schemaRef string) *registry.Registry {
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

	// Directive-based matching when no CLI flag provided.
	// Directive resolves schemas relative to each YAML file.
	reg.Register(registry.Directive())

	// SchemaStore automatic discovery (best-effort).
	store, err := schemastore.New(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: schemastore unavailable: %v\n", err)
	} else {
		reg.Register(store)
	}

	return reg
}
