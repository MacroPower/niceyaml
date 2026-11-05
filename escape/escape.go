package escape

import "strings"

// The C0 control characters run from nul through us, the C1 control
// characters from pad through apc, and del is the delete character. The
// Unicode Control Pictures for the C0 controls start at nulPicture, and
// delPicture is the picture for del. The C1 controls do not have
// pictures, so replacementCharacter stands in for each of them, and for
// each byte that is not valid UTF-8.
const (
	nul = 0x00
	us  = 0x1F
	pad = 0x80
	apc = 0x9F
	del = 0x7F

	nulPicture           = 0x2400
	delPicture           = 0x2421
	replacementCharacter = 0xFFFD
)

// TabWidth is the number of spaces [Tabs] puts in place of a tab. It is
// the default tab width of a lipgloss style. A renderer that lays its
// text out with such a style thus draws a tab as Tabs does.
const TabWidth = 4

// Control returns s with each control character replaced by one visible
// rune:
//
//   - A C0 control, U+0000 through U+001F, becomes its Unicode Control
//     Picture, U+2400 through U+241F.
//   - DEL, U+007F, becomes its picture, U+2421.
//   - A C1 control, U+0080 through U+009F, does not have a picture, so
//     it becomes the replacement character, U+FFFD.
//   - A byte that is not valid UTF-8 becomes U+FFFD too.
//
// The escape sequence "\x1b[31m" thus becomes "␛[31m". A line feed and a
// tab are C0 controls, so they become "␊" and "␉", and Control suits a
// text that takes one row. [Line] draws a tab as spaces instead, and
// [Rows] keeps each line feed.
//
// Control leaves every other rune as it is. Among them are the Unicode
// format characters, such as U+202E, and the line and paragraph
// separators, U+2028 and U+2029. [strconv.Quote] escapes those runes, and
// Control does not.
func Control(s string) string {
	var sb strings.Builder

	sb.Grow(len(s))

	for _, r := range s {
		switch {
		case r >= nul && r <= us:
			sb.WriteRune(r + nulPicture)
		case r == del:
			sb.WriteRune(delPicture)
		case r >= pad && r <= apc:
			sb.WriteRune(replacementCharacter)
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

// Tabs replaces each tab in s with [TabWidth] spaces, as a lipgloss
// style does, and leaves every other rune of s as it is. A tab takes
// that width wherever it falls, so a text reads the same after a prefix
// of any width, such as the name of a source.
//
// For example, "ab\tc" becomes "ab    c".
func Tabs(s string) string {
	return strings.ReplaceAll(s, "\t", strings.Repeat(" ", TabWidth))
}

// Message is like [Rows], but it formats s as the text of a message, such
// as the message of an error. A tab in a message aligns the text after
// it, so Message replaces each tab as [Tabs] does.
// [go.jacobcolvin.com/niceyaml.FormatError] draws each message of its
// tree this way.
//
// For example, "did you mean this?\n\tvalidate" becomes
// "did you mean this?\n    validate".
func Message(s string) string {
	return Rows(Tabs(s))
}

// Line is like [Control], but it formats s as one line of text, such as
// the name of a source or a message on a row of its own. It first replaces
// each tab as [Tabs] does. [*go.jacobcolvin.com/niceyaml.SourceError] draws
// the name of its source this way.
//
// For example, "a\tb\n" becomes "a    b␊".
func Line(s string) string {
	return Control(Tabs(s))
}
