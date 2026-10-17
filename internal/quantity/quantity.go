// Package quantity writes counts and sizes as the messages of niceyaml
// name them, such as "2 errors" and "256 KiB".
package quantity

import "strconv"

// Bytes returns n bytes as text such as "256 KiB", "1 MiB", "1 byte", or
// "1000 bytes". A size that holds a whole number of mebibytes or
// kibibytes takes that unit.
func Bytes(n int) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return strconv.Itoa(n>>20) + " MiB"
	case n >= 1<<10 && n%(1<<10) == 0:
		return strconv.Itoa(n>>10) + " KiB"
	case n == 1:
		return "1 byte"
	default:
		return strconv.Itoa(n) + " bytes"
	}
}

// Plural returns n before noun, with an "s" after noun for any n other
// than 1, as in "2 errors".
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return strconv.Itoa(n) + " " + noun + "s"
}
