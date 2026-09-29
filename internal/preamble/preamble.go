// Package preamble holds the rule for where the preamble of a YAML
// document ends and its content begins.
//
// The preamble is the run of tokens before the first token of content:
// comments, the tokens of each %YAML or %TAG directive line, and the "---"
// and "..." markers. Package niceyaml cuts the preamble of each document
// with [Len], and the schema directive scan reads the comments of the
// same run, so both agree on which comments come before content.
//
//	content := tks[preamble.Len(tks):]
package preamble

import "github.com/goccy/go-yaml/token"

// Len returns the number of tokens at the start of tks before the first
// token of content. Comments, the tokens of each %YAML or %TAG directive
// line, and the "---" and "..." markers count as preamble, and Len passes
// over a nil token. A stream without content is all preamble.
//
// The lexer splits a directive line into a directive token and the tokens
// holding its value, which would read as content, so Len counts every
// token on the line of a directive as preamble. The first token on a
// later line ends the directive line. A token without a position leaves
// the line open, but Len cannot place it on the line, so it counts as
// preamble only when it is a comment or a marker. A directive token
// without a position opens no line.
func Len(tks token.Tokens) int {
	inDirective, directiveLine := false, 0

	for i, tk := range tks {
		if tk == nil {
			continue
		}

		if tk.Type == token.DirectiveType {
			inDirective = tk.Position != nil
			if inDirective {
				directiveLine = tk.Position.Line
			}

			continue
		}

		if inDirective && tk.Position != nil {
			if tk.Position.Line == directiveLine {
				continue
			}

			inDirective = false
		}

		switch tk.Type {
		case token.CommentType, token.DocumentHeaderType, token.DocumentEndType:
			continue

		default:
			return i
		}
	}

	return len(tks)
}
