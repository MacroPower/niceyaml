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
	// Holds each stand-in among the Docs of File, which is nil when every
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
// [tokens.ForParser] makes. The go-yaml parser relinks Next and Prev
// while it moves comment tokens, and tks, which the caller shares, stay
// untouched.
//
// Each run of [splitDocumentRuns] parses on its own, so a run the parser
// rejects costs the file only the documents of that run. The file holds
// the stand-ins [Result.fail] makes in their place, and the parse goes on
// with the next run.
//
// ForParser cuts the blank lines in front of the first text down to their
// line breaks, so a blank line that holds a tab does not make the parser
// reject the first key. It also moves the blank content of a block scalar
// to the line after the header, so the parser attaches a comment below
// that content to the next node. Each copy gets the Origin and position
// of its own token back once the parser returns, so it matches the token
// of tks it copies.
func Parse(tks token.Tokens, opts ...parser.Option) *Result {
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
		f, failure := parseRun(dropStrandedComments(run), opts)
		if failure == nil {
			nullEmptyAnchors(f)

			r.File.Docs = append(r.File.Docs, f.Docs...)

			continue
		}

		failure.Run = run

		r.fail(*failure)
	}

	// The parser makes one empty document of an empty file, and none of a
	// stream of "..." markers alone. That stream holds one empty document
	// too, which the first marker ends, so every file holds a document and
	// a file of markers decodes to the zero value as an empty file does.
	// YAML holds no document there, so this departs from the spec on
	// purpose.
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

// fail records f, a run the parser rejected. It adds a stand-in to the
// file for each token group [tokens.SplitDocuments] cuts the run into,
// so the documents of the run keep their places among the documents of the
// file and their tokens. A group with a "---" header or with content
// stands for a document, and its stand-in carries f. Any other group
// holds what the parser cuts off as a node of its own, the comments and
// directives above a header or the comments after a "..." marker, and its
// stand-in folds into a document as that node does. A run without a
// document group, such as a directive that no document follows, takes its
// first group as the document, so f always has a document to name it.
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

// splitDocumentRuns cuts tks into runs that each parse on its own. It cuts
// before each "---" header that follows a token other than a comment, and
// after each "..." end marker that a token on a later line follows.
//
// The go-yaml parser (v1.19.3-0.20260407131736-edee2f91616c) groups the
// documents of a stream in a recursion that copies the rest of the list
// at each header (parser/token.go:662), so one run of many documents takes
// time quadratic in their number. It also mishandles some sequences of
// headers and markers. It stops at a header that directly follows another
// and drops every token after it (parser/token.go:637). It drops a "..."
// marker that directly follows a header and merges the header into what
// comes after the marker (parser/token.go:661). A second header there
// gives the merged document two headers, which fails with "unexpected
// scalar value type". A %YAML or %TAG directive or a document without a
// header there joins the empty document rather than starting the next
// one, and a %YAML directive there fails when another opens the empty
// document. The parser also rejects a scalar document below any marker
// (parser/token.go:682).
//
// A header stays in the run above it in two cases. A header that a
// directive precedes stays in the run of the directive, since the parser
// rejects a run that ends in a directive. YAML allows a directive only
// before the first document or after a marker, where a run starts anyway,
// and the parser rejects one anywhere else only while the document above
// it shares its run. A header that follows an anchor with no value stays in
// the run of the anchor. The parser takes the two tokens after the "&" as
// the name and the value of the anchor, and rejects a run that ends before
// them (parser/token.go:311).
//
// Each run parses with a parser of its own, so a %TAG or %YAML directive
// holds for the documents of its run. That is the one document the
// directive heads, as YAML defines, unless that document ends in an
// anchor with no value. The parser alone carries a %TAG directive for
// "!!" into every later document of its input.
//
// The look-back skips comments. The parser folds a comment on the line of
// a header or a marker into that token, so such a comment stays in the
// run of its token. A comment on a line of its own above a header parses
// to the same documents whether or not a run ends there. One below a
// marker starts the next run, as it starts a token group of
// [tokens.SplitDocuments]. Left in the run above, it would parse to a node
// that shares that group with the node below, and both would take the
// tokens of the group. A token on the line of a marker stays in its run,
// since YAML allows only a comment there and the parser rejects a scalar
// there.
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

// dropStrandedComments returns the tokens of run without the comments on
// lines of their own in places where the go-yaml parser can reject or
// misread valid YAML when it keeps comments, or run itself when it holds
// none.
//
// The parser (v1.19.3-0.20260407131736-edee2f91616c) attaches such a
// comment below the content of a document only when the root of the
// document is a block mapping or a block sequence. The comment must also
// start at or right of the column of the first key or "-" of that root
// (parser/parser.go:525, parser/parser.go:1117). Below a scalar or a flow
// collection, or left of that column, it leaves the comment unread and
// fails with "value is not allowed in this context" (parser/parser.go:171).
// It also requires the header to follow the line of a %YAML or %TAG
// directive at once, and fails with "document not started" when a comment
// sits between them (parser/token.go:598). The go-yaml decoder parses
// without comments and accepts both. The comments stay among the tokens of
// the input, where [tokens.SplitDocuments] still hands them to their
// documents.
//
// A comment sits on a line of its own when it starts on a line below the
// line where the last token before it starts. The parser attaches a
// comment to the token before it only when both start on one line
// (parser/token.go:254), so it reads a comment on the last line of a
// scalar that spans several lines as it reads one on a line of its own.
//
// Below a scalar or a flow collection, only the comments that close the
// document drop: those that the end of run, a "---" header, or a "..."
// marker follows. The parser rejects any other token after them, with or
// without the comments. Below a block mapping or a block sequence, only
// the comments that close the document and start left of its first key
// or "-" drop. The key starts at its anchor or tag when it has one, as
// [keyStart] finds it. A comment left of that column stays when more of
// the root follows it, since the parser looks past it to the next key or
// "-".
//
// Below an anchor with no value, the comments that close the document stay,
// since the parser rejects an anchor that no token follows past its name
// (parser/token.go:311). The parser takes them as the value of the anchor,
// and [nullEmptyAnchors] puts a null in their place once the parser
// returns. An anchor that directly follows a "-", or a key and its ":", on
// its line differs in two ways. The parser reads that anchor by the first
// comment on a line of its own that its input holds. When that comment
// starts left of the "-" or key, the parser itself gives the anchor a null
// value and reads the comments as it reads them below any other node. In a
// block mapping or a block sequence at the root, when a "---" header or a
// "..." marker follows the comments, the parser takes that anchor without
// them, as [takesNull] reports. Those that start left of the first key or
// "-" of the root then drop, as they do below any other node of such a
// root. The drop can change the first comment from one left of the "-" or
// key of the anchor to one at or right of it. The parser then takes the
// comments that stay as the value of the anchor, and [nullEmptyAnchors]
// puts a null in their place. At the end of run, those left of the root
// stay, and the parser rejects them unless it takes them as the value of
// the anchor. Above a node, the comments drop when [breaksAnchor] reports
// that they make the parser give the anchor the wrong value. Inside a flow
// collection, the anchor does not decide whether they drop. In a flow
// sequence, the parser still reads an anchor after the key of a pair by the
// column of the first comment. When that comment starts left of the key,
// the parser gives the anchor a null value and rejects the value below the
// comment.
//
// Below a "&" with no name, the comments stay wherever they sit, except
// between a directive and its header. The parser takes the token after a
// "&" as the name of the anchor (parser/token.go:314), so it rejects a "&"
// above a comment. Without the comments, it would name the anchor after the
// token below them.
//
// In block and flow style alike, the parser takes the token after a "?"
// as the key the "?" introduces, and the token before a ":" as the key of
// that ":" (parser/token.go:493, parser/token.go:513). With a comment in
// either place, it misreads the mapping or fails with "unexpected scalar
// value type" (parser/parser.go:326). Those comments drop.
//
// Inside a flow collection, the parser reads the other comments as the
// head comment of the node that follows them. It fails when a ",", "]",
// or "}" follows them instead, and when they sit after a "{" or "," of a
// flow mapping, where it looks for a key (parser/parser.go:349,
// parser/parser.go:407, parser/parser.go:1042). Those comments drop, and
// the comments before any other node stay.
func dropStrandedComments(run token.Tokens) token.Tokens {
	var kept token.Tokens

	s := scan{run: run, rootCol: -1}

	for i := 0; i < len(run); {
		if run[i].Type != token.CommentType {
			s.read(i)

			if kept != nil {
				kept = append(kept, run[i])
			}

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
				if kept == nil {
					kept = slices.Clone(run[:j])
				}

				continue
			}

			if kept != nil {
				kept = append(kept, run[j])
			}
		}

		i = next
	}

	if kept == nil {
		return run
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
// took as the value of an anchor of file. When a header or another entry
// follows an anchor with no value, the parser gives the anchor an
// implicit null, and this null takes the token type and position of that
// one. The go-yaml decoder reads the null as an empty node, where it
// would reject the comment in a typed target and give a pointer a value
// for it.
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

// breaksAnchor reports whether the comments between the anchor at run[at]
// and the node at run[next] make the parser give the anchor the wrong
// value where it gives the right one without them. The anchor has its
// name, and the comment at run[below] is the first of the comments on a
// line of its own.
//
// The comments change what the parser reads only below an anchor that
// directly follows a "-", or a key and its ":", on its line. The key must
// start on that line too, where [keyStart] finds it, and an explicit key
// starts at its "?" (parser/parser.go:730). The parser gives that anchor a
// null value when the first comment starts left of the "-" or key, and the
// node otherwise (parser/parser.go:787, parser/parser.go:1186). It reads
// any other anchor the same way with the comments as without them, so
// breaksAnchor reports false for one.
//
// Without the comments, the parser takes the node as the value of the
// anchor when the node starts right of the "-" or key, or is a "-" in the
// column of the key, where a block sequence may start. Above such a node,
// breaksAnchor reports whether the first comment starts left of the "-" or
// key, which makes the parser give the anchor a null value.
//
// Above any other node, YAML gives the anchor a null value, so
// breaksAnchor reports false when the first comment starts left of the "-"
// or key. Otherwise it reports true above a node left of the "-" or key, a
// key in the column of the key, and a "-" in the column of the "-". The
// parser gives the anchor a null value there without the comments
// (parser/parser.go:750, parser/parser.go:787, parser/parser.go:1149,
// parser/parser.go:1186). It reports false above any other node. One such
// node is a key in the column of the "-", which the parser takes as the
// value without the comments too.
//
// When a token it compares has no position, breaksAnchor reports false.
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
// it. It does so for an anchor that directly follows a "-", or a key and
// its ":", on its line (parser/parser.go:787, parser/parser.go:1186). The
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

// keyStart returns the index of the token on the line of the ":" at
// run[colon] where the key before that ":" starts. An explicit key starts
// at its "?". Any other key starts past any "-" or ":" on the line, at its
// anchor or tag when it has one. It returns -1 when no token of a key
// precedes the ":" on its line, as with a ":" that starts its line, and
// when the "?" of the key sits on a line above.
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
