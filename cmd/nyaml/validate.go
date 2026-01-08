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

			var v niceyaml.Validator
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
				err := validateFile(yamlPath, v)
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

func validateFile(yamlPath string, v niceyaml.Validator) error {
	yamlData, err := os.ReadFile(yamlPath) //nolint:gosec // User-provided file paths are intentional.
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	source := niceyaml.NewSourceFromString(string(yamlData))

	astFile, err := source.File()
	if err != nil {
		return err
	}

	if v != nil {
		decoder := niceyaml.NewDecoder(astFile)

		for _, doc := range decoder.Documents() {
			err := doc.Validate(v)
			if err != nil {
				return source.WrapError(err)
			}
		}
	}

	return nil
}
