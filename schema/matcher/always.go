package matcher

import (
	"context"

	"jacobcolvin.com/niceyaml"
)

// Always creates a new [Matcher] that always matches.
func Always() Matcher {
	return Func(func(_ context.Context, _ *niceyaml.DocumentDecoder) bool {
		return true
	})
}
