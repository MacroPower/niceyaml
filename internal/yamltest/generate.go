package yamltest

import (
	"fmt"
	"strings"
)

// GenerateYAML creates YAML content with the given number of lines. Each
// line is a simple key-value pair: "key_N: value_N". A count of zero or
// less yields the empty string.
func GenerateYAML(lines int) string {
	if lines <= 0 {
		return ""
	}

	var sb strings.Builder

	sb.Grow(lines * 25) // Approximate bytes per line.

	for i := range lines {
		fmt.Fprintf(&sb, "key_%d: value_%d\n", i, i)
	}

	return sb.String()
}
