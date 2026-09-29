package yamltest

import (
	"fmt"
	"strings"
)

// GenerateYAML creates YAML content with the given number of lines. Each
// line is a key-value pair: "key_N: value_N". A count of zero or less
// yields the empty string.
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

// AliasLevels creates YAML content whose key a lists the items l0 through
// l<levels>, each under an anchor of the same name. Item l0 is [x], and
// each later item lists ten aliases of the item before it, so a copy of
// *l<levels> with every alias written out holds 10^levels scalars.
func AliasLevels(levels int) string {
	var sb strings.Builder

	sb.WriteString("a:\n  - &l0 [x]\n")

	for level := 1; level <= levels; level++ {
		aliases := strings.Repeat(fmt.Sprintf("*l%d, ", level-1), 10)
		fmt.Fprintf(&sb, "  - &l%d [%s]\n", level, strings.TrimSuffix(aliases, ", "))
	}

	return sb.String()
}

// MergeLevels creates YAML content with the keys m0 through m<levels>,
// each under an anchor of the same name. Key m0 is {a: x}, and each later
// key merges ten aliases of the key before it, so a decode reads m0
// 10^levels times.
func MergeLevels(levels int) string {
	var sb strings.Builder

	sb.WriteString("m0: &m0 {a: x}\n")

	for level := 1; level <= levels; level++ {
		aliases := strings.Repeat(fmt.Sprintf("*m%d, ", level-1), 10)
		fmt.Fprintf(&sb, "m%d: &m%d\n  <<: [%s]\n", level, level, strings.TrimSuffix(aliases, ", "))
	}

	return sb.String()
}
