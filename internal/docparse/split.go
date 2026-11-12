package docparse

import (
	"iter"

	"github.com/goccy/go-yaml/token"
)

// SplitGroups cuts a token stream into groups at its document markers. A
// group is not a document. [Result.Documents] folds and moves groups into
// the documents it returns.
//
// A "---" header that follows any token starts a new group and opens it.
// A "..." marker closes the current group and ends it, so the tokens after
// it start a new group. These are the cuts the go-yaml parser makes in a
// well-formed stream. The comments and directives above a first "---",
// and the comments after a "...", thus form groups of their own.
//
// Each group holds its tokens in their original order and with their
// original positions. The tokens are the caller's, not copies. They keep
// the Next and Prev links of the whole stream, so the first token of a
// group still links back to the group before it. SplitGroups skips nil
// tokens in the stream.
func SplitGroups(tks token.Tokens) iter.Seq2[int, token.Tokens] {
	return func(yield func(int, token.Tokens) bool) {
		var (
			idx int
			// The index of tks where the group starts, and the number of
			// its tokens that are not nil.
			start, n int
		)

		// A cut yields the tokens of tks before end that the group holds.
		cut := func(end int) bool {
			group := make(token.Tokens, 0, n)

			for _, tk := range tks[start:end] {
				if tk != nil {
					group = append(group, tk)
				}
			}

			start, n = end, 0
			idx++

			return yield(idx-1, group)
		}

		for i, tk := range tks {
			if tk == nil {
				continue
			}

			if tk.Type == token.DocumentHeaderType && n > 0 && !cut(i) {
				return
			}

			n++

			if tk.Type == token.DocumentEndType && !cut(i+1) {
				return
			}
		}

		if n > 0 {
			cut(len(tks))
		}
	}
}
