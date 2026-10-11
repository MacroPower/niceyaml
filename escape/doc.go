// Package escape draws the control characters of a text as visible
// characters.
//
// A terminal acts on the control characters it receives. An escape
// sequence can clear the screen or move the cursor, and a carriage return
// lets the text after it overwrite the start of its row. Text a program
// did not write can hold either. The error of an unmarshaler that quotes
// a value of the document is such a text, and so is the name of a file.
//
// [go.jacobcolvin.com/niceyaml.FormatError] and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] escape the
// text they draw. A program that draws an error itself passes each text
// it shows through [Control], as a listing with one row per problem does:
//
//	for problem := range niceyaml.Problems(err) {
//		fmt.Println(escape.Control(problem.Message()))
//	}
//
// # Coverage
//
// [Control] replaces the C0 controls, U+0000 through U+001F, and DEL,
// U+007F, each with its Unicode Control Picture, so an escape reads as
// "␛" and DEL as "␡". The C1 controls, U+0080 through U+009F, have no
// pictures, and each becomes the replacement character, U+FFFD. So does
// each byte that is not valid UTF-8, which covers a C1 control written as
// one byte, such as 0x9B.
//
// Control leaves every other rune as it is. Among them are the Unicode
// format characters, such as the right-to-left override U+202E, and the
// line and paragraph separators, U+2028 and U+2029. [strconv.Quote]
// escapes those, and Control does not.
//
// # Rows
//
// A line feed and a tab are C0 controls, so they become "␊" and "␉".
// Control thus suits a text that takes one row, such as a name or a row
// of a listing. A program that keeps the rows of a message splits the
// message at its line feeds and passes each row to Control.
package escape
