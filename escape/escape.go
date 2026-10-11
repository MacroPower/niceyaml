package escape

import internal "go.jacobcolvin.com/niceyaml/internal/escape"

// Control returns s with each control character replaced by one visible
// rune:
//
//   - A C0 control, U+0000 through U+001F, becomes its Unicode Control
//     Picture, U+2400 through U+241F.
//   - DEL, U+007F, becomes its picture, U+2421.
//   - A C1 control, U+0080 through U+009F, has no picture and becomes
//     the replacement character, U+FFFD.
//   - A byte that is not valid UTF-8 becomes U+FFFD too.
//
// The escape sequence "\x1b[31m" thus becomes "␛[31m". A line feed and a
// tab are C0 controls, so they become "␊" and "␉", and Control suits a
// text that takes one row.
//
// Control leaves every other rune as it is. Among them are the Unicode
// format characters, such as U+202E, and the line and paragraph
// separators, U+2028 and U+2029, which [strconv.Quote] escapes.
func Control(s string) string {
	return internal.Control(s)
}
