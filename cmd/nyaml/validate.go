package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/schema/validate"
)

func validateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate schema file.yaml [file.yaml...]",
		Short: "Validate YAML files against a JSON schema",
		Long:  "Validate YAML files against a JSON schema.\nThe schema can be a local file path or a remote URL (http/https).",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			schemaRef := args[0]
			yamlPaths := args[1:]

			// Load and compile schema.
			schemaData, err := loadSchema(cmd.Context(), schemaRef)
			if err != nil {
				return fmt.Errorf("load schema: %w", err)
			}

			validator, err := validate.NewValidator(schemaRef, schemaData)
			if err != nil {
				return fmt.Errorf("compile schema: %w", err)
			}

			for _, yamlPath := range yamlPaths {
				err := validateFile(yamlPath, validator)
				if err != nil {
					return fmt.Errorf("%s: invalid: %w", yamlPath, err)
				} else if len(yamlPaths) > 1 {
					fmt.Printf("%s: valid\n", yamlPath)
				}
			}

			return nil
		},
	}

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

func validateFile(yamlPath string, validator *validate.Validator) error {
	yamlData, err := os.ReadFile(yamlPath) //nolint:gosec // User-provided file paths are intentional.
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	source := niceyaml.NewSourceFromString(string(yamlData))

	astFile, err := source.Parse()
	if err != nil {
		return err
	}

	decoder := niceyaml.NewDecoder(astFile)
	for _, doc := range decoder.Documents() {
		err := doc.Validate(validator)
		if err != nil {
			return err
		}
	}

	return nil
}
