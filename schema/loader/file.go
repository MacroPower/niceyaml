package loader

import (
	"context"
	"fmt"
	"os"

	"jacobcolvin.com/niceyaml"
)

// File creates a [Loader] that reads schema data from a local file.
//
// The file path is used directly without validation. Callers should ensure
// paths come from trusted sources or are validated before use to prevent
// path traversal attacks.
//
//	l := loader.File("./schemas/config.json")
func File(path string) Loader {
	return Func(func(_ context.Context, _ *niceyaml.DocumentDecoder) (Result, error) {
		data, err := os.ReadFile(path) //nolint:gosec // User-provided file paths are intentional.
		if err != nil {
			return Result{}, fmt.Errorf("read %s: %w", path, err)
		}

		return NewResult(path, data), nil
	})
}
