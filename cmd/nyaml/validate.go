package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/schema/validator"
)

func validateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate file.yaml [file.yaml...]",
		Short: "Validate YAML files",
		Long:  "Validate YAML files.\nOptionally validate against a JSON schema (local file or http/https URL).",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			schemaRef, err := cmd.Flags().GetString("schema")
			if err != nil {
				return fmt.Errorf("get schema flag: %w", err)
			}

			yamlPaths := args

			var v niceyaml.SchemaValidator
			if schemaRef != "" {
				// Load and compile schema.
				schemaData, err := loadSchema(cmd.Context(), schemaRef)
				if err != nil {
					return fmt.Errorf("load schema: %w", err)
				}

				v, err = validator.New(schemaRef, schemaData)
				if err != nil {
					return fmt.Errorf("compile schema: %w", err)
				}
			}

			for _, yamlPath := range yamlPaths {
				err := validateFile(cmd.Context(), yamlPath, v)
				if err != nil {
					return fmt.Errorf("%s: %w", yamlPath, err)
				} else if len(yamlPaths) > 1 {
					fmt.Printf("%s: valid\n", yamlPath)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringP("schema", "s", "", "JSON schema file path or URL")

	return cmd
}

func loadSchema(ctx context.Context, schemaRef string) ([]byte, error) {
	u, err := url.Parse(schemaRef)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return fetchSchema(ctx, schemaRef)
	}

	//nolint:gosec // User-provided file paths are intentional.
	return os.ReadFile(schemaRef)
}

func fetchSchema(ctx context.Context, schemaURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, schemaURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", schemaURL, err)
	}

	defer resp.Body.Close() //nolint:errcheck // Best-effort close.

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", schemaURL, resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func getTerminalWidth() int {
	if term.IsTerminal(os.Stderr.Fd()) {
		width, _, err := term.GetSize(os.Stderr.Fd())
		if err != nil {
			return max(0, width-4)
		}
	}

	return 0
}

func validateFile(ctx context.Context, yamlPath string, cliValidator niceyaml.SchemaValidator) error {
	yamlData, err := os.ReadFile(yamlPath) //nolint:gosec // User-provided file paths are intentional.
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	source := niceyaml.NewSourceFromString(
		string(yamlData),
		niceyaml.WithErrorOptions(
			niceyaml.WithWidthFunc(getTerminalWidth),
		),
	)

	astFile, err := source.File()
	if err != nil {
		return err
	}

	// Extract schema directives if no CLI schema provided.
	var docDirectives validator.DocumentDirectives
	if cliValidator == nil {
		docDirectives = validator.ParseDocumentDirectives(source.Tokens())
	}

	// Schema cache for reuse across documents with the same schema.
	cache := make(map[string]niceyaml.SchemaValidator)

	decoder := niceyaml.NewDecoder(astFile)
	docs := decoder.Documents()

	// Base directory for resolving relative schema paths.
	baseDir := filepath.Dir(yamlPath)

	for i, doc := range docs {
		v, err := resolveValidator(ctx, baseDir, cliValidator, docDirectives[i], cache)
		if err != nil {
			return fmt.Errorf("document %d: %w", i, err)
		}

		if v == nil {
			continue
		}

		err = doc.ValidateSchemaContext(ctx, v)
		if err != nil {
			return source.WrapError(err)
		}
	}

	return nil
}

// resolveValidator determines which validator to use for a document.
// Returns the CLI validator if provided, otherwise loads from directive cache.
// Returns nil with no error when no validator is configured for the document.
// The baseDir is used to resolve relative schema paths.
func resolveValidator(
	ctx context.Context,
	baseDir string,
	cliValidator niceyaml.SchemaValidator,
	directive *validator.Directive,
	cache map[string]niceyaml.SchemaValidator,
) (niceyaml.SchemaValidator, error) {
	// CLI flag takes precedence over inline directives.
	if cliValidator != nil {
		return cliValidator, nil
	}

	if directive == nil {
		return nil, nil //nolint:nilnil // nil validator means no validation needed.
	}

	// Resolve schema path relative to YAML file for non-URL paths.
	schemaRef := directive.Schema
	if !isURL(schemaRef) && !filepath.IsAbs(schemaRef) {
		schemaRef = filepath.Join(baseDir, schemaRef)
	}

	// Check cache first (use resolved path for cache key).
	if cached, ok := cache[schemaRef]; ok {
		return cached, nil
	}

	// Load and compile schema.
	schemaData, err := loadSchema(ctx, schemaRef)
	if err != nil {
		return nil, fmt.Errorf("load schema %q: %w", directive.Schema, err)
	}

	v, err := validator.New(schemaRef, schemaData)
	if err != nil {
		return nil, fmt.Errorf("compile schema %q: %w", directive.Schema, err)
	}

	cache[schemaRef] = v

	return v, nil
}

// isURL returns true if the path looks like an HTTP/HTTPS URL.
func isURL(path string) bool {
	u, err := url.Parse(path)

	return err == nil && (u.Scheme == "http" || u.Scheme == "https")
}
