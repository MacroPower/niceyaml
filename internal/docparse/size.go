package docparse

import (
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
// counts. Tks must not hold a nil token.
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
		end := start + len(run)

		result = append(result, Run{Tokens: run, Size: Size(tks[start:end])})

		start = end
	}

	return result
}

// Size returns the number of bytes of text that tks holds, the sum of the
// lengths of the Origins of its tokens. The Size of the stream of a text
// from [tokens.Tokenize] is thus the length of the text without its byte
// order marks. [ParseLimits] checks the size of each run of documents
// against its byte limit, as [Runs] counts it.
func Size(tks token.Tokens) int {
	n := 0

	for _, tk := range tks {
		if tk != nil {
			n += len(tk.Origin)
		}
	}

	return n
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
