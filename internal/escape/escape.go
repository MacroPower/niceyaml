package escape

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
	// [Control] uses it for C1 control characters, which have no
	// control pictures.
	ReplacementCharacter = 0xFFFD
)

// Control replaces control characters with visible representations:
//   - C0 controls ([NUL]-[US]) become Unicode Control Pictures
//     ([NULPicture]-[USPicture]).
//   - C1 controls ([PAD]-[APC]) become [ReplacementCharacter].
//   - [DEL] becomes [DELPicture].
//
// For example, an ANSI escape sequence like "\x1b[31m" becomes "␛[31m".
func Control(s string) string {
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

// Rows is like [Control], but it keeps each line feed in s as a line
// break and replaces the control characters of the rows between them.
//
// For example, "\x1b[31m\nred" becomes "␛[31m\nred".
func Rows(s string) string {
	rows := strings.Split(s, "\n")
	for i, row := range rows {
		rows[i] = Control(row)
	}

	return strings.Join(rows, "\n")
}

// TabWidth is the number of spaces [Tabs] puts in place of a tab.
const TabWidth = 4

// Tabs replaces each tab in s with [TabWidth] spaces, as a lipgloss
// style does, and leaves every other rune of s as it is. A tab takes
// that width wherever it falls, so a text reads the same after a prefix
// of any width, such as the name of a source.
//
// For example, "ab\tc" becomes "ab    c".
func Tabs(s string) string {
	return strings.ReplaceAll(s, "\t", strings.Repeat(" ", TabWidth))
}

// Message is like [Rows], but it lays s out as the text of a message,
// such as the message of an error. A tab in a message lays out the text
// after it, so Message replaces each tab as [Tabs] does.
//
// For example, "did you mean this?\n\tvalidate" becomes
// "did you mean this?\n    validate".
func Message(s string) string {
	return Rows(Tabs(s))
}
