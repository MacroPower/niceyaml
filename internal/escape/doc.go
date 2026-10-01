// Package escape makes control characters visible in rendered text.
//
// # Control Characters
//
// A terminal interprets the ANSI escape sequences and other control
// characters in raw content rather than showing them.
//
// The [Control] function replaces control characters with visible Unicode
// representations. Printing the result leaves terminal state unchanged.
//
//	escaped := escape.Control("\x1b[31mRed\x1b[0m") // "␛[31mRed␛[0m"
//
// [Control] also replaces line feeds. For text that spans several rows,
// [Rows] keeps each line feed as a line break and replaces the other
// control characters.
//
// # Messages
//
// In the message of an error, a tab lays out the text after it, as the
// tab in front of each suggestion Cobra lists does. [Message] replaces
// each tab with four spaces and replaces the other control characters as
// [Rows] does:
//
//	escape.Message("did you mean this?\n\tvalidate") // "did you mean this?\n    validate"
//
// [Tabs] replaces the tabs alone, for a message that a renderer escapes
// later, such as the message an annotation carries.
package escape
