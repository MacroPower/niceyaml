package docparse

import (
	"runtime/debug"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/preamble"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Result is what [Parse] gives.
type Result struct {
	// File holds the documents of the runs that parsed, with the
	// stand-ins of the runs that did not, in file order.
	File *ast.File
	// Index maps each copy of a token that Parse hands the parser to the
	// index of the token in the input.
	Index map[*token.Token]int
	// Holds each stand-in among the Docs of File. The map is nil when every
	// run parsed.
	standIns map[*ast.DocumentNode]standIn
	// Tokens holds the copies of the input that Parse hands the parser,
	// each at the index of its own token.
	Tokens token.Tokens
	// Failures holds each run that did not parse, in file order.
	Failures []Failure
}

// Failure is a run of tokens the parser rejected or panicked on.
type Failure struct {
	// Err is the error the parser returned, or nil after a panic.
	Err error
	// Panic is the value the parser panicked with, or nil.
	Panic any
	// Stack is the stack of the goroutine at the panic, as [debug.Stack]
	// formats it.
	Stack []byte
	// Run holds the tokens of the run.
	Run token.Tokens
}

// standIn describes a node that [Result.fail] puts in the file in place of
// the nodes the parser would have made of a run it rejected.
type standIn struct {
	// The token that places the stand-in in the file, which is nil when
	// its tokens carry no position.
	anchor *token.Token
	// The index in Failures of the run, for a stand-in that stands for a
	// document. It is -1 for one that stands for the comments and
	// directives the parser cuts off above a header.
	failure int
}

// Parse parses tks, a stream of tokens, with [parser.ParseComments] and
// opts. It hands the parser the copies of the tokens that
// [tokens.ForParser] makes, so tks, which the caller shares, stays
// untouched while the parser relinks comment tokens. Once the parser
// returns, each copy takes back the Origin and position of its own token.
//
// Each run of documents parses on its own, so a run the parser rejects
// costs the file only its own documents. [Result.File] holds stand-ins in
// their place, and [Result.Failures] holds the run.
func Parse(tks token.Tokens, opts ...parser.Option) *Result {
	return ParseDepth(tks, 0, opts...)
}

// ParseDepth is [Parse] with a limit on the levels a document may nest. A
// run that holds a document past the limit fails with a [*DepthError] in
// place of a parse, so the parser, whose memory grows with the square of
// the depth, never reads it. A depth of zero or less sets no limit.
func ParseDepth(tks token.Tokens, depth int, opts ...parser.Option) *Result {
	copies := tokens.ForParser(tks)

	index := make(map[*token.Token]int, len(copies))
	for i, tk := range copies {
		index[tk] = i
	}

	// Tks holds no nil tokens, so each copy sits at the index of its own
	// token.
	defer func() {
		for i, tk := range copies {
			tk.Origin = tks[i].Origin

			// Clone gives each copy a Position of its own, so the write
			// leaves the token of tks alone.
			if tk.Position != nil && tks[i].Position != nil {
				*tk.Position = *tks[i].Position
			}
		}
	}()

	r := &Result{File: &ast.File{Docs: []*ast.DocumentNode{}}, Index: index, Tokens: copies}

	for _, run := range splitDocumentRuns(copies) {
		if at, deep := tooDeep(run, depth); deep {
			r.fail(Failure{Err: &DepthError{Token: at, Limit: depth}, Run: run})

			continue
		}

		f, failure := parseRun(dropStrandedComments(run), opts)
		if failure == nil {
			nullEmptyAnchors(f)

			r.File.Docs = append(r.File.Docs, f.Docs...)

			continue
		}

		failure.Run = run

		r.fail(*failure)
	}

	// The parser makes one empty document of an empty file but none of a
	// stream of "..." markers alone. That stream gets one too, which the
	// first marker ends, so it decodes to the zero value as an empty file
	// does, although YAML holds no document there.
	if len(r.File.Docs) == 0 {
		doc := ast.Document(nil, nil)

		if i := slices.IndexFunc(
			copies,
			func(tk *token.Token) bool { return tk.Type == token.DocumentEndType },
		); i >= 0 {
			doc.End = copies[i]
		}

		r.File.Docs = append(r.File.Docs, doc)
	}

	return r
}

// Failed returns the [Result] of a text that fails whole with err before
// any parse, such as one past a byte limit. Its file holds one stand-in
// document that carries err.
func Failed(err error) *Result {
	r := &Result{File: &ast.File{}, Index: map[*token.Token]int{}}
	r.fail(Failure{Err: err})

	return r
}

// fail records f, a run the parser rejected, and adds a stand-in to the
// file for each token group [tokens.SplitDocuments] cuts the run into, so
// the documents of the run keep their places and tokens. The stand-in of
// a group [isDocumentGroup] reports carries f, and any other folds into a
// document as the node the parser cuts off does. A run with no document
// group carries f on its first group, so f always names a document.
func (r *Result) fail(f Failure) {
	r.Failures = append(r.Failures, f)

	failure := len(r.Failures) - 1

	var groups []token.Tokens

	for _, group := range tokens.SplitDocuments(f.Run) {
		groups = append(groups, group)
	}

	// A run without tokens still gets one stand-in to carry f.
	if len(groups) == 0 {
		groups = []token.Tokens{nil}
	}

	holdsDocument := slices.ContainsFunc(groups, isDocumentGroup)

	if r.standIns == nil {
		r.standIns = map[*ast.DocumentNode]standIn{}
	}

	for i, group := range groups {
		in := standIn{failure: -1}

		if j := slices.IndexFunc(group, func(tk *token.Token) bool { return tk.Position != nil }); j >= 0 {
			in.anchor = group[j]
		}

		node := ast.Document(nil, nil)

		if isDocumentGroup(group) || !holdsDocument && i == 0 {
			in.failure = failure

			// SplitDocuments starts a group at each header, so a header
			// is the first token of its group.
			if len(group) > 0 && group[0].Type == token.DocumentHeaderType {
				node.Start = group[0]
			}
		}

		r.File.Docs = append(r.File.Docs, node)
		r.standIns[node] = in
	}
}

// parseRun parses run, one run of [splitDocumentRuns], with opts. It
// returns the [Failure] of a run the parser rejects or panics on. The
// deferred function that recovers a panic takes the stack, so the stack
// still holds the frames of the parser.
func parseRun(run token.Tokens, opts []parser.Option) (*ast.File, *Failure) {
	var (
		f       *ast.File
		failure *Failure
	)

	func() {
		defer func() {
			p := recover()
			if p == nil {
				return
			}

			failure = &Failure{Panic: p, Stack: debug.Stack()}
		}()

		var err error

		f, err = parser.Parse(run, parser.ParseComments, opts...)
		if err != nil {
			failure = &Failure{Err: err}
		}
	}()

	return f, failure
}

// isDocumentGroup reports whether group, a token group of
// [tokens.SplitDocuments], holds a YAML document, which it does when it
// opens with a "---" header or holds content. A group of comments,
// directives, and "..." markers alone holds none.
func isDocumentGroup(group token.Tokens) bool {
	if len(group) > 0 && group[0].Type == token.DocumentHeaderType {
		return true
	}

	return preamble.Len(group) < len(group)
}

// splitDocumentRuns cuts tks into runs that each parse on their own. The
// createDocumentTokens step of the go-yaml parser
// (v1.19.3-0.20260407131736-edee2f91616c) takes quadratic time over the
// documents of one run, and it mishandles a scalar document below a "..."
// marker and a "---" header right after a header or a marker. A run ends
// after each marker that a token on a later line follows, and before each
// header that follows a token other than a comment. A header stays in the
// run of a directive or an anchor with no value before it, since the
// parser rejects a run that ends in either.
func splitDocumentRuns(tks token.Tokens) []token.Tokens {
	var (
		runs  []token.Tokens
		start int
		// Whether a directive follows the last header.
		directive bool
	)

	// The indexes of the last two tokens that are not comments.
	prev, prevprev := -1, -1

	for i, tk := range tks {
		if tk.Type == token.CommentType {
			continue
		}

		if prev >= 0 {
			last := tks[prev]

			switch {
			case last.Type == token.DocumentEndType && startsBelow(tk, last):
				// The comments on lines below the marker open the next run.
				cut := i
				for cut > prev+1 && startsBelow(tks[cut-1], last) {
					cut--
				}

				runs = append(runs, tks[start:cut])
				start = cut

			case tk.Type == token.DocumentHeaderType && last.Type != token.DocumentEndType:
				anchored := last.Type == token.AnchorType ||
					prevprev >= 0 && tks[prevprev].Type == token.AnchorType

				if last.Type == token.DocumentHeaderType || !directive && !anchored {
					runs = append(runs, tks[start:i])
					start = i
				}
			}
		}

		switch tk.Type {
		case token.DirectiveType:
			directive = true

		case token.DocumentHeaderType:
			directive = false

		default:
		}

		prevprev, prev = prev, i
	}

	return append(runs, tks[start:])
}

// dropStrandedComments returns run without the whole-line comments that
// make the go-yaml parser reject or misread valid YAML, which the decoder
// reads without comments. It drops those after a directive or a "?",
// before a ":", ",", "]", or "}", and where a flow mapping expects a key.
// It drops those that close a document below a scalar or flow collection,
// or left of the first key or "-" of a block root. Below a "&" with no
// name, only those after a directive drop. Outside a flow collection,
// [breaksAnchor] and [takesNull] decide below an anchor with no value.
// The dropped comments stay among the tokens of the input, where
// [tokens.SplitDocuments] still hands them to their documents.
func dropStrandedComments(run token.Tokens) token.Tokens {
	kept := make(token.Tokens, 0, len(run))

	s := scan{run: run, rootCol: -1}

	for i := 0; i < len(run); {
		if run[i].Type != token.CommentType {
			s.read(i)

			kept = append(kept, run[i])

			i++

			continue
		}

		next := i
		for next < len(run) && run[next].Type == token.CommentType {
			next++
		}

		stranded, leftDrops := s.drops(i, next)

		for j := i; j < next; j++ {
			left := leftDrops && run[j].Position != nil && run[j].Position.Column < s.rootCol

			if (stranded || left) && startsBelow(run[j], s.last) {
				continue
			}

			kept = append(kept, run[j])
		}

		i = next
	}

	return kept
}

// scan holds what [dropStrandedComments] has read of run so far.
type scan struct {
	// The last two tokens other than comments that hold text.
	last, beforeLast *token.Token
	run              token.Tokens
	// The types of the "[" and "{" tokens of the flow collections that
	// hold the scan, the innermost last.
	flows []token.Type
	// The column where the first key or "-" of the block mapping or block
	// sequence at the root of the document starts, or -1.
	rootCol int
	// The index of the last anchor in run.
	anchor int
	// What the document the scan is in has shown so far: a token of its
	// content, an indicator of a block mapping or a block sequence at its
	// top level, and a directive that awaits its header.
	content, block, directive bool
}

// read reads run[i], a token other than a comment.
func (s *scan) read(i int) {
	tk := s.run[i]

	switch tk.Type {
	case token.DocumentHeaderType, token.DocumentEndType:
		s.content, s.block, s.directive, s.flows = false, false, false, s.flows[:0]
		s.rootCol = -1

	case token.DirectiveType:
		s.directive = true

	case token.SequenceStartType, token.MappingStartType:
		s.content = true

		s.flows = append(s.flows, tk.Type)

	case token.SequenceEndType, token.MappingEndType:
		if len(s.flows) > 0 {
			s.flows = s.flows[:len(s.flows)-1]
		}

	case token.MappingKeyType, token.MappingValueType, token.SequenceEntryType:
		if !s.block && len(s.flows) == 0 {
			s.rootCol = rootColumn(s.run, i)
		}

		s.content = true
		s.block = s.block || len(s.flows) == 0

	case token.AnchorType:
		s.content = true
		s.anchor = i

	default:
		s.content = true
	}

	if strings.Trim(tk.Origin, " \t\r\n") != "" {
		s.beforeLast, s.last = s.last, tk
	}
}

// drops reports which of the comments at run[i:next] drop when they sit
// on lines of their own. The first result reports that all of them drop,
// and the second that those left of rootCol drop.
func (s *scan) drops(i, next int) (bool, bool) {
	run := s.run

	// A "&" with no name ends the content.
	bare := s.last != nil && s.last.Type == token.AnchorType

	// The name of an anchor ends the content when the anchor has no
	// value.
	anchored := s.beforeLast != nil && s.beforeLast.Type == token.AnchorType

	// Whether the comments close the document.
	closes := next == len(run) ||
		run[next].Type == token.DocumentHeaderType || run[next].Type == token.DocumentEndType

	// The parser takes a comment after a "?" or before a ":" as a key.
	stranded := s.directive || s.last != nil && s.last.Type == token.MappingKeyType ||
		next < len(run) && run[next].Type == token.MappingValueType

	switch {
	case bare:
		// Without the comments, the parser would take the token below
		// them as the name of the anchor.
		return s.directive, false

	case len(s.flows) > 0:
		return stranded || next < len(run) && endsFlowEntry(run[next]) ||
			startsFlowKey(s.last, s.flows[len(s.flows)-1]), false

	case anchored:
		// The first comment on a line of its own, past any comment on
		// the line where the last token starts.
		below := i
		for below < next && !startsBelow(run[below], s.last) {
			below++
		}

		return stranded || below < next && !closes && breaksAnchor(run, s.anchor, below, next),
			closes && next < len(run) && s.rootCol >= 0 && takesNull(run, s.anchor)

	case s.content && !s.block:
		return stranded || closes, false

	case s.block:
		return stranded, closes && s.rootCol >= 0

	default:
		return stranded, false
	}
}

// nullEmptyAnchors puts a null in place of each comment that the parser
// took as the value of an anchor of file, with the token type and position
// of the implicit null the parser gives an anchor with no value. The
// go-yaml decoder would reject the comment in a typed target and give a
// pointer a value for it.
func nullEmptyAnchors(file *ast.File) {
	for _, doc := range file.Docs {
		for _, node := range astnode.Nodes(doc) {
			anchor, ok := node.(*ast.AnchorNode)
			if !ok || astnode.HasContent(anchor.Value) {
				continue
			}

			var pos *token.Position

			if anchor.Start != nil && anchor.Start.Position != nil {
				p := *anchor.Start.Position
				p.Column++
				pos = &p
			}

			tk := token.New("null", " null", pos)
			tk.Type = token.ImplicitNullType
			anchor.Value = ast.Null(tk)
		}
	}
}

// endsFlowEntry reports whether tk is a ",", "]", or "}", which ends an
// entry or a collection in flow style.
func endsFlowEntry(tk *token.Token) bool {
	switch tk.Type {
	case token.CollectEntryType, token.SequenceEndType, token.MappingEndType:
		return true

	default:
		return false
	}
}

// startsFlowKey reports whether tk is the "{" or a "," of a flow mapping,
// which a key follows. A token of type flow opens the innermost flow
// collection around tk. It reports false when tk is nil.
func startsFlowKey(tk *token.Token, flow token.Type) bool {
	if tk == nil || flow != token.MappingStartType {
		return false
	}

	return tk.Type == token.MappingStartType || tk.Type == token.CollectEntryType
}

// breaksAnchor reports whether the comments between the anchor at run[at],
// which has its name, and the node at run[next] change the value the
// parser gives the anchor, or false when a token it compares has no
// position. The parseMapValue and parseSequenceValue steps of the parser
// give an anchor right after a "-" or a key's ":" a null value when the
// first whole-line comment, at run[below], starts left of the "-" or key.
func breaksAnchor(run token.Tokens, at, below, next int) bool {
	comment, tk := run[below], run[next]
	if comment.Position == nil || tk.Position == nil {
		return false
	}

	// The index of the "-" or the start of the key.
	holder, mapping := anchorHolder(run, at)
	if holder < 0 || run[holder].Position == nil {
		return false
	}

	var (
		col   = run[holder].Position.Column
		entry = tk.Type == token.SequenceEntryType
		// Whether the parser gives the anchor a null value with the
		// comments in the tree.
		null = comment.Position.Column < col
	)

	switch {
	case tk.Position.Column > col, mapping && entry && tk.Position.Column == col:
		return null

	case null:
		return false

	case tk.Position.Column < col:
		return true

	case mapping:
		return startsKey(run, next)

	default:
		return entry
	}
}

// startsKey reports whether a key of a block mapping starts at run[at],
// the first token on its line. That token is a "?" or a ":", or a ":"
// follows it on its line. A "[" or "{" before that ":" makes the line a
// flow collection, which the parser does not read as a key, so startsKey
// reports false for it.
func startsKey(run token.Tokens, at int) bool {
	if run[at].Type == token.MappingKeyType {
		return true
	}

	for j := at; j < len(run) && sameLine(run[j], run[at]); j++ {
		switch run[j].Type {
		case token.MappingValueType:
			return true

		case token.SequenceStartType, token.MappingStartType:
			return false

		default:
		}
	}

	return false
}

// takesNull reports whether the parser gives the anchor at run[at], which
// has its name, a null value when a token left of its key or "-" follows
// it. It does so in parseMapValue and parseSequenceValue for an anchor
// that directly follows a "-", or a key and its ":", on its line. The
// parser rejects any other anchor that no node follows.
func takesNull(run token.Tokens, at int) bool {
	holder, _ := anchorHolder(run, at)

	return holder >= 0
}

// anchorHolder returns the index of the "-" that the anchor at run[at]
// directly follows on its line, or of the start of the key whose ":" it
// directly follows there, as [keyStart] finds it. The boolean reports a
// key. It returns -1 when the anchor follows neither.
func anchorHolder(run token.Tokens, at int) (int, bool) {
	i := at - 1
	if i < 0 || !sameLine(run[i], run[at]) {
		return -1, false
	}

	switch run[i].Type {
	case token.SequenceEntryType:
		return i, false

	case token.MappingValueType:
		return keyStart(run, i), true

	default:
		return -1, false
	}
}

// rootColumn returns the column where the entry of the "-", "?", or ":"
// at run[at] starts, or -1 when the token there has no position. An
// entry starts at its "-" or "?". For a ":", it starts where [keyStart]
// finds the start of the key, or at the ":" itself when no token of a
// key precedes the ":" on its line.
func rootColumn(run token.Tokens, at int) int {
	tk := run[at]

	if tk.Type == token.MappingValueType {
		if k := keyStart(run, at); k >= 0 {
			tk = run[k]
		}
	}

	if tk.Position == nil {
		return -1
	}

	return tk.Position.Column
}

// keyStart returns the index of the token where the key of the ":" at
// run[colon] starts on its line: its "?", or else the first token past any
// "-" or ":", which may be an anchor or a tag. It returns -1 when no token
// of a key precedes the ":" on its line, or when the "?" of the key sits
// on a line above.
func keyStart(run token.Tokens, colon int) int {
	start := -1

	k := colon - 1
	for ; k >= 0 && sameLine(run[k], run[colon]); k-- {
		switch run[k].Type {
		case token.MappingKeyType:
			return k

		case token.SequenceEntryType, token.MappingValueType, token.DocumentHeaderType:
			return start

		default:
		}

		start = k
	}

	// The token before the line of the ":", past any comments.
	for k >= 0 && run[k].Type == token.CommentType {
		k--
	}

	if k >= 0 && run[k].Type == token.MappingKeyType {
		return -1
	}

	return start
}

// sameLine reports whether a and b start on the same line. It reports
// false when either token has no position.
func sameLine(a, b *token.Token) bool {
	return a.Position != nil && b.Position != nil && a.Position.Line == b.Position.Line
}

// startsBelow reports whether tk starts on a line below the line mark
// starts on. It reports false when mark is nil or either token has no
// position.
func startsBelow(tk, mark *token.Token) bool {
	return mark != nil && tk.Position != nil && mark.Position != nil &&
		tk.Position.Line > mark.Position.Line
}
