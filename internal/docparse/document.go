package docparse

import (
	"slices"
	"sort"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/lineend"
	"go.jacobcolvin.com/niceyaml/internal/preamble"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Document is one YAML document of a parsed file.
type Document struct {
	// Root is the node the document parsed into. For a document that did
	// not parse, it is a stand-in with no body.
	Root *ast.DocumentNode
	// Tokens holds the tokens of the whole document.
	Tokens token.Tokens
	// Positioned holds the tokens that carry a position, in the order of
	// their offsets.
	Positioned token.Tokens
	// Span holds the lines the document covers.
	Span position.Span
	// FileIndex is the index of Root in the Docs of the file, which can
	// differ from the index of the document, since preamble nodes fold
	// into documents.
	FileIndex int
	// Preamble is the number of tokens at the start of Tokens before the
	// content.
	Preamble int
	// Failure is the index in [Result.Failures] of the run the document
	// failed in, or -1 for a document that parsed.
	Failure int
}

// Documents returns each YAML document of the file r holds, in file
// order. The file holds a stand-in for each document the parser rejected,
// so that document is one too, and its Failure names its run. Tks holds
// the tokens r parsed, and lines is the number of lines they cover. See
// alignDocumentTokens for how each document node finds its tokens and
// foldPreambles for which nodes become documents.
func (r *Result) Documents(tks token.Tokens, lines int) []Document {
	docs := foldPreambles(r.File.Docs, alignDocumentTokens(r.File, tks, r.standIns), r.standIns)
	liftHeaderComments(docs)
	returnSameLineComments(docs)

	groups := make([]token.Tokens, len(docs))
	for i, doc := range docs {
		groups[i] = doc.Tokens
	}

	spans := documentSpans(groups, lines)

	out := make([]Document, len(docs))

	for i, doc := range docs {
		doc.Span = spans[i]
		doc.Preamble = preamble.Len(doc.Tokens)
		doc.Positioned = slices.DeleteFunc(slices.Clone(doc.Tokens), func(tk *token.Token) bool {
			return tk == nil || tk.Position == nil
		})
		out[i] = *doc
	}

	return out
}

// foldPreambles pairs each document node with its token group and folds
// the nodes that are not YAML documents into the ones that are. The parser
// makes a node with no header and no content from the comments and %YAML
// or %TAG directives above a "---" header, and from the comments after a
// "..." marker. Such a node joins the next document as its preamble,
// whichever of the two it holds, or the last document when no document
// follows it. A file that holds only such nodes, such as a file of
// comments, keeps the first as its one document, which decodes to
// nothing as an empty file does.
//
// A stand-in of standIns that carries a failure is a document that did not
// parse, whatever it holds, and its document takes the failure. Any other
// stand-in holds what the parser would have cut off, and it folds as a
// node of the parser does.
func foldPreambles(
	nodes []*ast.DocumentNode,
	groups []token.Tokens,
	standIns map[*ast.DocumentNode]standIn,
) []*Document {
	var (
		docs    []*Document
		pending token.Tokens
	)

	for i, node := range nodes {
		failure := -1
		if in, ok := standIns[node]; ok {
			failure = in.failure
		}

		if failure < 0 && isPreambleNode(node) {
			pending = append(pending, groups[i]...)

			continue
		}

		docs = append(
			docs,
			&Document{Root: node, FileIndex: i, Tokens: slices.Concat(pending, groups[i]), Failure: failure},
		)
		pending = nil
	}

	switch {
	case len(docs) > 0 && len(pending) > 0:
		last := docs[len(docs)-1]
		last.Tokens = append(last.Tokens, pending...)

	case len(docs) == 0 && len(nodes) > 0:
		docs = append(docs, &Document{Root: nodes[0], FileIndex: 0, Tokens: pending, Failure: -1})
	}

	return docs
}

// liftHeaderComments moves the whole-line comments that close the tokens
// of each document to the front of the document below when that one has
// a "---" header. The parser leaves such comments with the document above
// as trailing comments of its content, while the same comments above the
// first header of a file, or after a "..." marker, are the preamble of the
// document below. Lifting them puts a schema directive written above any
// header in the preamble of the document it describes. A comment on the
// last line of content stays with the content, and a comment below the
// header of a document without content stays with that document.
func liftHeaderComments(docs []*Document) {
	for i := 1; i < len(docs); i++ {
		if docs[i].Root.Start == nil {
			continue
		}

		prev := docs[i-1]

		cut := trailingCommentsStart(prev.Tokens)
		if cut == len(prev.Tokens) {
			continue
		}

		docs[i].Tokens = slices.Concat(prev.Tokens[cut:], docs[i].Tokens)
		prev.Tokens = prev.Tokens[:cut]
	}
}

// returnSameLineComments moves the comments that open the tokens of each
// document back to the document above when they sit on the line of its
// last token. A comment on the line of a "..." marker starts the token
// group after the marker. A document below without a "---" header would
// take that comment, and the line of the marker with it, while a document
// with a header leaves the comment with the marker. The function runs
// after liftHeaderComments, which moves only comments on lines of their
// own.
func returnSameLineComments(docs []*Document) {
	for i := 1; i < len(docs); i++ {
		prev, cur := docs[i-1], docs[i]

		// The lexer gives an empty block scalar a token with no text at the
		// position of the token after it, so the last token that holds
		// text sets the line.
		var last *token.Token

		for _, tk := range slices.Backward(prev.Tokens) {
			if tk != nil && tk.Position != nil && strings.Trim(tk.Origin, " \t\r\n") != "" {
				last = tk

				break
			}
		}

		if last == nil {
			continue
		}

		end := textEndLine(last)

		cut := 0
		for cut < len(cur.Tokens) {
			tk := cur.Tokens[cut]
			if tk == nil || tk.Type != token.CommentType || tk.Position == nil || tk.Position.Line != end {
				break
			}

			cut++
		}

		if cut == 0 {
			continue
		}

		prev.Tokens = slices.Concat(prev.Tokens, cur.Tokens[:cut])
		cur.Tokens = cur.Tokens[cut:]
	}
}

// trailingCommentsStart returns the index of the first token in the run of
// comments that closes tks, where each comment sits on a line below the
// last token of any other type that holds text. Returns len(tks) when no
// comment closes tks, or when the token before the run is a "---" header,
// whose document the comments below it belong to.
func trailingCommentsStart(tks token.Tokens) int {
	last := -1

	for i, tk := range tks {
		// The lexer gives an empty block scalar a token with no text at
		// the position of the token after it, which can be a comment.
		if tk.Type != token.CommentType && strings.Trim(tk.Origin, " \t\r\n") != "" {
			last = i
		}
	}

	if last < 0 || tks[last].Type == token.DocumentHeaderType || tks[last].Position == nil {
		return len(tks)
	}

	end := textEndLine(tks[last])
	start := len(tks)

	for i := len(tks) - 1; i > last; i-- {
		if tks[i].Type != token.CommentType || tks[i].Position == nil || tks[i].Position.Line <= end {
			break
		}

		start = i
	}

	return start
}

// textEndLine returns the line where the text of tk ends, which is the
// line where its text starts plus the line breaks within its text. The
// spaces, tabs, and line breaks around the text do not count, because the
// lexer can end a token with a line break and the indentation of the next
// line. YAML reads other Unicode spaces, such as U+00A0, as text. The
// position of tk must not be nil.
func textEndLine(tk *token.Token) int {
	return tk.Position.Line + lineend.CountBreaks(strings.Trim(tk.Origin, " \t\r\n"))
}

// isPreambleNode reports whether node is one the parser cut off from the
// document it belongs to: a node with no "---" header whose body holds no
// value. A node with a header and a body of comments is an explicit empty
// document and stays one.
func isPreambleNode(node *ast.DocumentNode) bool {
	return node.Start == nil && !astnode.HasContent(node.Body)
}

// documentSpans returns the lines of a view of total lines that each token
// group covers. The groups partition the file in order, so a group runs
// from the line its first token starts on to the line the next group
// starts on. The first group runs from the top of the view, and the last
// group runs to the end of it. A group after the first with no tokens
// covers no lines and sits where the next group starts.
func documentSpans(groups []token.Tokens, total int) []position.Span {
	spans := make([]position.Span, len(groups))

	end := total
	for i := len(groups) - 1; i >= 0; i-- {
		start := end
		if len(groups[i]) > 0 {
			start = min(end, max(0, groups[i][0].Position.Line-1))
		}

		spans[i] = position.NewSpan(start, end)
		end = start
	}

	// The lines above the first group's first token, such as blank lines or
	// a comment the lexer hangs off a later token, belong to no later group.
	// The first group takes them the way the last group takes the lines
	// below its last token. A first group with no tokens takes them too,
	// which keeps the spans covering every line of the view.
	if len(groups) > 0 {
		spans[0] = position.NewSpan(0, spans[0].End)
	}

	return spans
}

// alignDocumentTokens pairs every document in file with the token group it
// starts in, and returns one entry per document in file order.
//
// The groups come from [tokens.SplitDocuments]. Each document anchors at
// the offset of its header token, or of its body's first token when it has
// no header. It takes the groups from the first one no earlier document
// claimed up to the one the next document anchors in. A group that starts
// ahead of the first anchor, such as a leading "..." marker, thus joins
// the document below it. Matching by offset rather than by index keeps a
// document paired with its own tokens when the parser and the splitter
// disagree on boundaries, as they do for such a marker. A document with no
// anchor gets nil tokens. A stand-in of standIns has no body to anchor
// at, so it anchors at the token [Result.fail] gave it.
func alignDocumentTokens(file *ast.File, tks token.Tokens, standIns map[*ast.DocumentNode]standIn) []token.Tokens {
	var (
		groups []token.Tokens
		starts []int
	)

	for _, group := range tokens.SplitDocuments(tks) {
		if len(group) == 0 || group[0].Position == nil {
			continue
		}

		groups = append(groups, group)
		starts = append(starts, group[0].Position.Offset)
	}

	anchors := make([]int, len(file.Docs))
	anchored := make([]bool, len(file.Docs))

	for i, doc := range file.Docs {
		if in, ok := standIns[doc]; ok {
			if in.anchor != nil {
				anchors[i], anchored[i] = in.anchor.Position.Offset, true
			}

			continue
		}

		anchors[i], anchored[i] = documentOffset(doc)
	}

	result := make([]token.Tokens, len(file.Docs))

	// The first group no document has claimed, so a group that no anchor
	// reaches back to still joins a document rather than falling out.
	next := 0

	for i := range file.Docs {
		if !anchored[i] {
			continue
		}

		// Index of the last group that starts at or before the anchor.
		idx := sort.Search(len(starts), func(j int) bool { return starts[j] > anchors[i] }) - 1
		if idx < 0 {
			continue
		}

		idx = min(idx, next)

		// The groups before the one the next anchored document starts in
		// belong to this one, and the last document takes the rest.
		end := len(starts)

		for j := i + 1; j < len(file.Docs); j++ {
			if anchored[j] {
				end = sort.Search(len(starts), func(k int) bool { return starts[k] > anchors[j] }) - 1

				break
			}
		}

		next = max(end, idx+1)
		result[i] = slices.Concat(groups[idx:next]...)
	}

	return result
}

// documentOffset returns the offset of the token that anchors doc: its header
// token, its body's first token when it has no header, or the "..." marker
// that ends it when it has neither, as the empty document of a stream of
// markers alone does. The boolean is false when doc has none of them.
func documentOffset(doc *ast.DocumentNode) (int, bool) {
	if doc.Start != nil && doc.Start.Position != nil {
		return doc.Start.Position.Offset, true
	}

	if doc.Body != nil {
		if tk := doc.Body.GetToken(); tk != nil && tk.Position != nil {
			return tk.Position.Offset, true
		}
	}

	if doc.End != nil && doc.End.Position != nil {
		return doc.End.Position.Offset, true
	}

	return 0, false
}
