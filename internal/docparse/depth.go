package docparse

import (
	"strconv"

	"github.com/goccy/go-yaml/token"
)

// DepthError is the failure of a run that holds a document that nests
// more levels than the limit allows. [Parse] reports it in place of a
// parse of the run, so the parser never reads such a document.
type DepthError struct {
	// Token is the token that opens the first level past the limit.
	Token *token.Token
	// Limit is the number of levels that a document can nest.
	Limit int
}

// Error returns the message of e, such as "document nests more than 100
// levels".
func (e *DepthError) Error() string {
	return "document nests more than " + strconv.Itoa(e.Limit) + " levels"
}

// blockLevel is one block collection that the scan in [TooDeep] is inside
// of.
type blockLevel struct {
	col int
	seq bool
}

// TooDeep returns the first token of run that opens a level past limit,
// and true, or nil and false when the documents of run do not nest that
// deep. A limit of zero or less disables the check. [ParseDepth] runs the
// check on each run of documents.
//
// A flow collection opens a level at its "[" or "{". A block collection
// opens one at its first key or "-" when that token stands in a column
// deeper than the block collection that holds it. A sequence that is the
// value of a mapping entry also opens one in the column of its key. The
// aliasing package bounds the levels that aliases add where a reader
// expands them, so an alias does not open a level. Each document counts
// from zero.
func TooDeep(run token.Tokens, limit int) (*token.Token, bool) {
	if limit <= 0 {
		return nil, false
	}

	var (
		stack []blockLevel
		flow  int
		prev  = -1
	)

	for i, tk := range run {
		if tk == nil || tk.Type == token.CommentType {
			continue
		}

		var entry *blockLevel

		at := tk

		switch tk.Type {
		case token.DocumentHeaderType, token.DocumentEndType:
			stack, flow = stack[:0], 0

		case token.SequenceStartType, token.MappingStartType:
			flow++

			if len(stack)+flow > limit {
				return tk, true
			}

		case token.SequenceEndType, token.MappingEndType:
			flow = max(0, flow-1)

		case token.SequenceEntryType:
			if flow == 0 && tk.Position != nil {
				entry = &blockLevel{col: tk.Position.Column, seq: true}
			}

		case token.MappingKeyType:
			if flow == 0 && tk.Position != nil {
				entry = &blockLevel{col: tk.Position.Column}
			}

		case token.MappingValueType:
			if flow == 0 && tk.Position != nil {
				at = blockKeyStart(run, prev, tk)
				entry = &blockLevel{col: at.Position.Column}
			}

		default:
			// Every other token, a scalar, an alias, or a tag among them,
			// opens no level.
		}

		if entry != nil {
			var deeper bool

			stack, deeper = enterBlock(stack, *entry)
			if deeper && len(stack) > limit {
				return at, true
			}
		}

		prev = i
	}

	return nil, false
}

// enterBlock returns stack after a block entry at level opens, and whether
// the entry opened a level of its own rather than joining the collection
// that holds it. An entry at a column left of a collection ends that
// collection, and so does a key in the column of a sequence that is the
// value of a mapping entry.
func enterBlock(stack []blockLevel, level blockLevel) ([]blockLevel, bool) {
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		if top.col > level.col || top.col == level.col && top.seq && !level.seq {
			stack = stack[:len(stack)-1]

			continue
		}

		break
	}

	if len(stack) > 0 {
		top := stack[len(stack)-1]
		if top.col == level.col && top.seq == level.seq {
			return stack, false
		}
	}

	return append(stack, level), true
}

// blockKeyStart returns the token that starts the key of colon, a block
// mapping value indicator. The argument prev is the index in run of the
// token before colon, or -1. The token is the first anchor or tag in front
// of the key on its line, or the key itself. A colon that opens its line,
// as after an explicit key, starts its own entry.
func blockKeyStart(run token.Tokens, prev int, colon *token.Token) *token.Token {
	if prev < 0 || run[prev].Position == nil || run[prev].Position.Line != colon.Position.Line {
		return colon
	}

	start := run[prev]

	for j := prev - 1; j >= 0; j-- {
		tk := run[j]
		if tk == nil || tk.Position == nil || tk.Position.Line != start.Position.Line {
			break
		}

		if tk.Type != token.AnchorType && tk.Type != token.TagType {
			break
		}

		start = tk
	}

	return start
}
