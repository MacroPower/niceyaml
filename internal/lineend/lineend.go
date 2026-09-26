// Package lineend holds the rule for where the go-yaml lexer ends a line.
//
// The lexer advances Position.Line on "\n", "\r\n", and a bare "\r", and
// counts a CRLF as one line break. Code that cuts text into lines or counts
// its line breaks to match the lexer's positions calls this package rather
// than repeating the rule.
//
//	for ln := range lineend.Lines("a\r\nb\rc") {
//		fmt.Printf("%q ", ln) // "a\r\n" "b\r" "c"
//	}
package lineend

import (
	"iter"
	"strings"
)

// Lines yields the lines of s, each cut after its line ending: "\n",
// "\r\n", or a bare "\r". Each line keeps its line ending, and the last
// line has none when s does not end with one. An empty s yields nothing.
func Lines(s string) iter.Seq[string] {
	return func(yield func(string) bool) {
		start := 0

		for i := range len(s) {
			switch s[i] {
			case '\n':
			case '\r':
				if i+1 < len(s) && s[i+1] == '\n' {
					continue // The "\n" of a CRLF ends the line.
				}

			default:
				continue
			}

			if !yield(s[start : i+1]) {
				return
			}

			start = i + 1
		}

		if start < len(s) {
			yield(s[start:])
		}
	}
}

// CountBreaks returns the number of line breaks in s, counting "\r\n",
// "\n", and a bare "\r" as one each.
func CountBreaks(s string) int {
	return strings.Count(s, "\n") + strings.Count(s, "\r") - strings.Count(s, "\r\n")
}
