package yamlscan

// Option configures a [Scanner].
//
// Available options:
//   - [WithCompat]
type Option func(*Scanner)

// WithCompat is an [Option] that keeps the token types and values of
// go-yaml where a patch of the copy changes what the go-yaml parser reads.
// The Origins of the tokens still join to the source. When enabled is
// true:
//
//   - A tag that ends the input is not a token. Its text ends the Origin of
//     the last token.
//   - The scan steps one rune past the text that it reads in a
//     double-quoted scalar for each tab between its text. The token of the
//     scalar ends with those runes, and the next scan starts after them.
//     When the last of those runes is the "\r" of a CRLF, the "\r" opens
//     the Origin of the next token instead, so the CRLF stays whole.
//   - A tag without a value types the first key of the next line as a
//     string. So does a tag on a mapping for the first key of the mapping.
//   - A tab in front of a ":" or "-" does not make an invalid token on the
//     line after a tag, after a comment that ends with a CRLF, or after a
//     block scalar header that an invalid token ends.
func WithCompat(enabled bool) Option {
	return func(s *Scanner) {
		s.compat = enabled
	}
}
