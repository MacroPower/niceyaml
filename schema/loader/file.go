package loader

import (
	"context"
	"fmt"
	"os"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/schema"
)

// File creates a [schema.Resolver] that reads schema data from a local file.
// The path doubles as the schema's URL, so the registry reads the file once
// and reuses the compiled validator for every document that names it.
//
// The file path is used directly without validation. Callers should ensure
// paths come from trusted sources or are validated before use to prevent
// path traversal attacks.
//
//	r := loader.File("./schemas/config.json")
func File(path string) schema.Resolver {
	return schema.ResolverFunc(func(_ context.Context, _ *niceyaml.DocumentDecoder) (schema.Ref, error) {
		return schema.Ref{
			URL: path,
			Load: func(_ context.Context) ([]byte, error) {
				data, err := os.ReadFile(path) //nolint:gosec // User-provided file paths are intentional.
				if err != nil {
					return nil, fmt.Errorf("read %s: %w", path, err)
				}

				return data, nil
			},
		}, nil
	})
}
