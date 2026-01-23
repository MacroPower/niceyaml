// Package ansi provides utilities for handling ANSI control characters.
package ansi

import "strings"

const (
	// NUL is the first C0 control character.
	NUL = 0x00
	// US is the last C0 control character.
	US = 0x1F
	// PAD is the first C1 control character.
	PAD = 0x80
	// APC is the last C1 control character.
	APC = 0x9F
	// DEL is the delete control character.
	DEL = 0x7F

	// NULPicture is the Unicode Control Picture for [NUL].
	// It starts the C0 control picture block.
	NULPicture = 0x2400
	// USPicture is the Unicode Control Picture for [US].
	// It ends the C0 control picture block.
	USPicture = 0x241F
	// DELPicture is the Unicode Control Picture for [DEL].
	DELPicture = 0x2421
	// ReplacementCharacter is the Unicode replacement character.
	// Used for C1 control characters which have no control pictures.
	ReplacementCharacter = 0xFFFD
)

// Escape replaces control characters with visible representations:
//   - C0 controls ([NUL]-[US]) -> Unicode Control Pictures ([NULPicture]-[USPicture])
//   - C1 controls ([PAD]-[APC]) -> [ReplacementCharacter]
//   - [DEL] -> [DELPicture]
//
// This makes invisible control characters visible in terminal output.
// For example, an ANSI escape sequence like "\x1b[31m" becomes "␛[31m".
func Escape(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))

	for _, r := range s {
		switch {
		case r >= NUL && r <= US:
			sb.WriteRune(r + NULPicture)
		case r == DEL:
			sb.WriteRune(DELPicture)
		case r >= PAD && r <= APC:
			sb.WriteRune(ReplacementCharacter)
		default:
			sb.WriteRune(r)
		}
	}

	return sb.String()
}
