package loader

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
)

// Custom creates a [Loader] from a custom function that returns schema bytes
// and URL separately.
//
// Errors returned by fn are passed through directly without wrapping, allowing
// the caller to provide their own contextual error messages.
//
// Use this for custom dynamic loading logic:
//
//	kindPath := paths.Root().Child("kind").Path()
//	l := loader.Custom(func(ctx context.Context, doc *niceyaml.DocumentDecoder) ([]byte, string, error) {
//	    kind, _ := doc.GetValue(kindPath)
//	    schemaPath := fmt.Sprintf("schemas/%s.json", strings.ToLower(kind))
//	    data, err := schemaFS.ReadFile(schemaPath)
//	    if err != nil {
//	        return nil, "", fmt.Errorf("load schema for kind %q: %w", kind, err)
//	    }
//	    return data, schemaPath, nil
//	})
func Custom(fn func(ctx context.Context, doc *niceyaml.DocumentDecoder) ([]byte, string, error)) Loader {
	return Func(func(ctx context.Context, doc *niceyaml.DocumentDecoder) (Result, error) {
		data, schemaURL, err := fn(ctx, doc)
		if err != nil {
			return Result{}, err //nolint:wrapcheck // Custom function provides its own context.
		}

		return NewResult(schemaURL, data), nil
	})
}
