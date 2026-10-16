package yamltest

import (
	"fmt"
	"slices"
	"strings"
)

// GenerateYAML creates YAML content with the given number of lines. Each
// line is a key-value pair: "key_N: value_N". A count of zero or less
// yields the empty string.
func GenerateYAML(lines int) string {
	return GenerateLines(lines, "key_%[1]d: value_%[1]d")
}

// GenerateLines creates content with the given number of lines. Each line
// is format with the index of the line, counted from 0, as its one
// argument, so "k%d: v" yields the lines "k0: v", "k1: v", and so on.
// A count of zero or less yields the empty string.
func GenerateLines(lines int, format string) string {
	var sb strings.Builder

	for i := range lines {
		fmt.Fprintf(&sb, format+"\n", i)
	}

	return sb.String()
}

// FlowSequence creates a YAML flow sequence that lists item count times
// and then each element of tail, such as "[*a, *a, x]".
func FlowSequence(item string, count int, tail ...string) string {
	return "[" + strings.Join(append(slices.Repeat([]string{item}, count), tail...), ", ") + "]"
}

// AliasLevels creates YAML content whose key a lists the items l0 through
// l<levels>, each under an anchor of the same name. Item l0 is [x], and
// each later item lists ten aliases of the item before it, so a copy of
// *l<levels> with every alias written out holds 10^levels scalars.
func AliasLevels(levels int) string {
	var sb strings.Builder

	sb.WriteString("a:\n  - &l0 [x]\n")

	for level := 1; level <= levels; level++ {
		fmt.Fprintf(&sb, "  - &l%d %s\n", level, FlowSequence(fmt.Sprintf("*l%d", level-1), 10))
	}

	return sb.String()
}

// AliasChain creates YAML content with the keys c0 through c<links>, each
// under an anchor of the same name. Key c0 is [x], and each later key
// lists one alias of the key before it. No line of the content nests more
// than two levels, but *c<links> written out in full nests links+1 levels
// of sequences.
func AliasChain(links int) string {
	var sb strings.Builder

	sb.WriteString("c0: &c0 [x]\n")

	for link := 1; link <= links; link++ {
		fmt.Fprintf(&sb, "c%d: &c%d [*c%d]\n", link, link, link-1)
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
		fmt.Fprintf(&sb, "m%d: &m%d\n  <<: %s\n", level, level, FlowSequence(fmt.Sprintf("*m%d", level-1), 10))
	}

	return sb.String()
}

// WideMerge creates YAML content whose mapping m holds members keys,
// each spelled in hexadecimal over the value x, and then a merge key that
// lists as many aliases to the mapping a. To tell which entry the
// spelling of one of those keys selects, a lookup reads that whole list.
func WideMerge(members int) string {
	return WideMergeOf(members, "*a")
}

// WideMergeOf creates the YAML content of [WideMerge] with another merge
// list. The merge key of m lists item members times and then each element
// of tail, as [FlowSequence] writes them.
func WideMergeOf(members int, item string, tail ...string) string {
	var sb strings.Builder

	sb.WriteString("a: &a {z: 0}\nm:\n")

	for i := range members {
		fmt.Fprintf(&sb, "  %#x: x\n", 0x1000+i)
	}

	sb.WriteString("  <<: " + FlowSequence(item, members, tail...) + "\n")

	return sb.String()
}

// AliasedMapping creates YAML content whose key defaults holds a mapping
// of keys entries under the anchor d, and whose key services holds
// aliases entries, each an alias to d. A decode reads every entry of the
// mapping once for each alias, so the two counts set how far the aliases
// multiply the document.
func AliasedMapping(keys, aliases int) string {
	var sb strings.Builder

	sb.WriteString("defaults: &d\n")

	for i := range keys {
		fmt.Fprintf(&sb, "  k%d: v%d\n", i, i)
	}

	sb.WriteString("services:\n")

	for i := range aliases {
		fmt.Fprintf(&sb, "  s%d: *d\n", i)
	}

	return sb.String()
}

// AliasedScalar creates YAML content whose key a holds a scalar of size
// bytes under the anchor a. Its key l lists aliases elements, each an
// alias to a, in a block sequence that starts on the third line. A writer
// that writes each alias out in full repeats the scalar at every element.
func AliasedScalar(size, aliases int) string {
	return "a: &a " + strings.Repeat("x", size) + "\nl:\n" + strings.Repeat("  - *a\n", aliases)
}

// AliasKey creates YAML content whose key holds a mapping with the alias
// *l<level> as its one key and v as its value. A decode writes out an
// alias in a mapping key in full. After the content of [AliasLevels] for
// level or more levels, the key therefore holds an alias bomb that a
// decode expands:
//
//	input := yamltest.AliasLevels(7) + yamltest.AliasKey("kind", 7)
func AliasKey(key string, level int) string {
	return fmt.Sprintf("%s:\n  ? *l%d\n  : v\n", key, level)
}

// SchemaDirective returns body under the comment line
// "# yaml-language-server: $schema=<ref>", which names ref as the schema
// of the document. An empty body leaves the comment line alone.
func SchemaDirective(ref, body string) string {
	return "# yaml-language-server: $schema=" + ref + "\n" + body
}
