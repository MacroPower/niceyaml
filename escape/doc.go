// Package escape draws the control characters of a text as visible
// characters.
//
// A terminal acts on the control characters it receives. An escape
// sequence can clear the screen or move the cursor. A carriage return lets
// the text after it overwrite the start of its row. Text that a program
// did not write can hold either. The error of an unmarshaler that quotes a
// value of the document is such a text, and so is the name of a file.
//
// [go.jacobcolvin.com/niceyaml.FormatError] and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] escape the
// text they draw, with the functions of this package. A program that
// draws an error itself passes each text it shows through the function
// that fits the text, as a listing with one row per problem does:
//
//	for problem := range niceyaml.Problems(err) {
//		fmt.Println(escape.Line(problem.Message()))
//	}
//
// # Coverage
//
// [Control] replaces the C0 controls, U+0000 through U+001F, and DEL,
// U+007F, each with its Unicode Control Picture, so an escape reads as
// "␛" and DEL as "␡". The C1 controls, U+0080 through U+009F, do not
// have pictures, so each becomes the replacement character, U+FFFD. So
// does each byte that is not valid UTF-8. A C1 control written as one
// byte, such as 0x9B, is not valid UTF-8.
//
// Control leaves every other rune as it is. Among them are the Unicode
// format characters, such as the right-to-left override U+202E, and the
// line and paragraph separators, U+2028 and U+2029. [strconv.Quote]
// escapes those, and Control does not.
//
// # Rows and Tabs
//
// A line feed and a tab are C0 controls, so Control draws them as "␊"
// and "␉". The picture of a tab suits the content of a document, where a
// tab is a character the reader wants to see. In a name or a message, a
// tab is layout instead, because it aligns the text after it. A line feed
// in a message starts a new row.
//
// A program picks the function by the rows that its text takes. Text that
// takes one row, such as the name of a source or a message on a row of
// its own, goes through [Line]. Line draws each tab as spaces and each
// line feed as "␊", so the text stays on its row. Text that keeps its
// rows, such as each message of an error tree, goes through [Message].
// Message draws each tab as spaces too, but it keeps each line feed as a
// line break.
//
// FormatError draws each name with Line and each message of its tree with
// Message, so a renderer that calls the same functions draws the text
// as the library does.
package escape
