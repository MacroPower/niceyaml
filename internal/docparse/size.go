package docparse

import (
	"strings"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/quantity"
)

// SizeError is the failure of a run that holds more bytes of text than
// the limit allows. [ParseLimits] reports it in place of a parse of the
// run, so the parser never reads such a run.
type SizeError struct {
	// Token is the first token of the run that has a position and is not
	// a comment. It is the first token with a position for a run of
	// comments alone, and nil for a run without any position.
	Token *token.Token
	// Limit is the number of bytes that a run can hold.
	Limit int
}

// Error returns the message of e, such as "document is larger than 256
// KiB".
func (e *SizeError) Error() string {
	return "document is larger than " + quantity.Bytes(e.Limit)
}

// Run is a run of documents that [ParseLimits] parses on its own.
type Run struct {
	// Tokens holds the tokens of the run, a part of the stream that
	// [Runs] cuts.
	Tokens token.Tokens
	// Size is the number of bytes of text that the run holds, which
	// ParseLimits checks against [Limits.Bytes].
	Size int
}

// Runs cuts tks into the runs of documents that [ParseLimits] parses one
// at a time, in file order. The Size of each [Run] counts as [Size]
// counts, with the token before the run as the token before its first
// token. A line ending that the lexer repeats at the start of a run thus
// counts in the run that the ending closes. Tks must not hold a nil token.
func Runs(tks token.Tokens) []Run {
	return sizedRuns(splitDocumentRuns(tks), tks)
}

// sizedRuns returns the [Run] of each of runs, the runs that
// [splitDocumentRuns] cut from a stream of the same length as tks. Each
// Run holds its part of runs and the Size of the same part of tks.
func sizedRuns(runs []token.Tokens, tks token.Tokens) []Run {
	result := make([]Run, 0, len(runs))
	start := 0

	for _, run := range runs {
		var prev *token.Token

		if start > 0 {
			prev = tks[start-1]
		}

		n := 0

		for _, tk := range tks[start : start+len(run)] {
			n += tokenSize(prev, tk)
			prev = tk
		}

		start += len(run)

		result = append(result, Run{Tokens: run, Size: n})
	}

	return result
}

// Size returns the number of bytes of text that tks holds, the sum of the
// lengths of the Origins of its tokens. The lexer repeats a line ending
// at the start of the token after a tag or an invalid token that ends a
// line, as [tokens.Tokenize] describes. The text holds that ending once,
// so Size counts it once. The Size of the stream of a text from
// [tokens.Tokenize] is thus the length of the text without its byte order
// marks. [ParseLimits] checks the size of each run of documents against
// its byte limit, as [Runs] counts it.
func Size(tks token.Tokens) int {
	var prev *token.Token

	n := 0

	for _, tk := range tks {
		if tk == nil {
			continue
		}

		n += tokenSize(prev, tk)
		prev = tk
	}

	return n
}

// tokenSize returns the number of bytes of text that tk adds to the text
// of the tokens up to prev, the token before it, or nil for the first
// token. It counts the Origin of tk without the line ending that the
// lexer repeats from prev, as [Size] describes.
func tokenSize(prev, tk *token.Token) int {
	n := len(tk.Origin)

	if prev == nil || prev.Type != token.TagType && prev.Type != token.InvalidType {
		return n
	}

	// The repeat can start partway into the ending of prev, as a "\n"
	// that repeats the end of a "\r\n".
	ending := lineEnding(prev.Origin)
	for i := range len(ending) {
		if strings.HasPrefix(tk.Origin, ending[i:]) {
			return n - (len(ending) - i)
		}
	}

	return n
}

// lineEnding returns the line ending at the end of s, or "" when s does
// not end with one.
func lineEnding(s string) string {
	for _, ending := range []string{"\r\n", "\n", "\r"} {
		if strings.HasSuffix(s, ending) {
			return ending
		}
	}

	return ""
}

// sizeToken returns the token that the [SizeError] of run binds at, as
// [SizeError.Token] describes it.
func sizeToken(run token.Tokens) *token.Token {
	var first *token.Token

	for _, tk := range run {
		if tk == nil || tk.Position == nil {
			continue
		}

		if tk.Type != token.CommentType {
			return tk
		}

		if first == nil {
			first = tk
		}
	}

	return first
}
