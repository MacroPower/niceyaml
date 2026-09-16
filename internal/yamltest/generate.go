package yamltest

import (
	"fmt"
	"strings"
)

// GenerateYAML creates YAML content with the given number of lines. Each
// line is a simple key-value pair: "key_N: value_N".
func GenerateYAML(lines int) string {
	var sb strings.Builder

	sb.Grow(lines * 25) // Approximate bytes per line.

	for i := range lines {
		fmt.Fprintf(&sb, "key_%d: value_%d\n", i, i)
	}

	return sb.String()
}
