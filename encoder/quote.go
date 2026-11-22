package encoder

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// quoter is an [ast.Visitor] that double-quotes each string that go-yaml
// otherwise writes in a form that reads back as a different value or does
// not parse. It checks the unquoted, single-quoted, and literal block
// forms. It first replaces each literal or folded block from a
// marshaler's YAML with a string, as [quoter.unblock] describes. It then
// checks the text of such a block as it checks any other string. It
// leaves a block collection in a flow collection as it is. Go-yaml writes
// that collection in block style, and quoting does not make block style
// valid there. Only a marshaler's YAML puts a block collection there.
type quoter struct {
	// Collects the blocks that [quoter.unblock] replaces.
	blocks *[]block
	// Holds the spaces per indentation level of the encode.
	indent int
	// Reports whether the visited node sits in a flow collection.
	flow bool
	// Reports whether a tag stands on the visited node, as in a
	// marshaler's YAML. A tag decides how go-yaml reads the scalar under
	// it.
	tagged bool
}

// Visit implements [ast.Visitor].
func (q quoter) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.AnchorNode:
		// An anchor name is not a string value.
		ast.Walk(q, n.Value)

		return nil

	case *ast.AliasNode:
		// An alias holds an anchor name.
		return nil
	case *ast.TagNode:
		q.tagged = true

		return q

	case *ast.MappingNode:
		if q.flow && !n.IsFlowStyle {
			return nil
		}

		return q.within(n.IsFlowStyle)

	case *ast.SequenceNode:
		if q.flow && !n.IsFlowStyle {
			return nil
		}

		inner := q.within(n.IsFlowStyle)

		for i, value := range n.Values {
			n.Values[i] = inner.unblock(value, n.Start.Position.Column, func(v ast.Node) { n.Values[i] = v })

			if s, ok := n.Values[i].(*ast.StringNode); ok && !inner.flow {
				quoteEntry(s)
			}
		}

		return inner

	case *ast.MappingValueNode:
		n.Key = q.keySafe(n.Key)

		col := n.Key.GetToken().Position.Column
		if _, ok := n.Key.(*ast.MappingKeyNode); ok {
			// The token of an explicit key is its "?" indicator, and the
			// go-yaml printer writes the key after "? ".
			col += 2
		}

		n.Value = q.unblock(n.Value, col, func(v ast.Node) { n.Value = v })

	case *ast.StringNode:
		q.quote(n, false)
	}

	return q
}

// within returns the quoter for the nodes of a collection. Flow reports
// whether the collection is a flow collection. A tag on the collection
// does not stand on the nodes in it.
func (q quoter) within(flow bool) quoter {
	q.flow = flow
	q.tagged = false

	return q
}

// block is a literal or folded block of a marshaler's YAML that
// [quoter.unblock] replaces with a string.
type block struct {
	// Holds the string that replaces the block.
	str *ast.StringNode
	// Puts a node in place of str in the parent of str. It is nil for a
	// string at the root of the document.
	set func(ast.Node)
}

// unblock returns node, or a string in place of node when node is a
// literal or folded block. Such a block comes from a marshaler whose
// output go-yaml parses, as the YAML that go-yaml makes of the JSON of a
// MarshalJSON method. The go-yaml printer writes the lines of such a
// block with the indentation that they had in that output. The block
// then does not move with its key or its sequence entry. The string holds
// the text of the block, and it sits at col, the column of the key or the
// sequence that holds node. The go-yaml printer indents the string from
// there by the indent of the encode, as it indents a string that the
// encoder builds. The string thus loses the folded style and the
// indentation indicator of the block. Unblock looks through the tags and
// anchors that a marshaler's YAML can put on the block. Set puts a node
// in the place of node in its parent, and it is nil for the root of the
// document. Unblock records the string and set in q.blocks, so that
// [commentBlocks] can replace the string later.
func (q quoter) unblock(node ast.Node, col int, set func(ast.Node)) ast.Node {
	switch n := node.(type) {
	case *ast.LiteralNode:
		str := q.blockString(n, col)
		*q.blocks = append(*q.blocks, block{str: str, set: set})

		return str

	case *ast.TagNode:
		n.Value = q.unblock(n.Value, col, func(v ast.Node) { n.Value = v })
	case *ast.AnchorNode:
		n.Value = q.unblock(n.Value, col, func(v ast.Node) { n.Value = v })
	}

	return node
}

// blockString returns a string at the column col that holds the text of
// lit, as [quoter.unblock] describes. Like each string that go-yaml's
// encoder builds, the string has its text as its origin. A text without a
// line break, as of the block "|-\n  a: b", does not print as a block.
// Go-yaml's encoder double-quotes a Go string of such a text that does
// not read back unquoted, such as "a: b". BlockString double-quotes the
// string of such a block by the same rule. It double-quotes that string
// in a flow collection too.
func (q quoter) blockString(lit *ast.LiteralNode, col int) *ast.StringNode {
	pos := *lit.Start.Position
	pos.Column = col
	pos.IndentNum = q.indent
	s := lit.Value.Value
	tk := token.New(s, s, &pos)

	if !strings.ContainsAny(s, "\n\r") && (q.flow || token.IsNeedQuoted(s)) {
		tk.Type = token.DoubleQuoteType
	}

	return ast.String(tk)
}

// commentBlocks keeps the line comment of each string in blocks that the
// go-yaml printer writes as a literal block. It returns root, or the node
// that takes the place of root. The printer drops the line comment of
// such a string. So commentBlocks puts a literal block in place of the
// string, with the text that the printer writes for the string. The
// printer writes the comment of a literal block on the line of its
// header, after the header. The printer trims every space and line break
// from the end of the text of a literal block. So the block cannot hold
// the text of a string that keeps more than one final line break, and
// commentBlocks double-quotes such a string instead. The printer writes
// the comment after the quotes.
func commentBlocks(root ast.Node, blocks []block) ast.Node {
	for _, b := range blocks {
		s := b.str
		if s.GetComment() == nil || s.Token.Type == token.DoubleQuoteType || !strings.Contains(s.Value, "\n") {
			continue
		}

		header, lines, _ := strings.Cut(s.String(), "\n")
		if strings.TrimRight(strings.TrimRight(lines, " "), "\n") != lines {
			s.Token.Type = token.DoubleQuoteType

			continue
		}

		pos := *s.Token.Position
		lit := ast.Literal(token.Literal(header, header, &pos))
		lit.Value = ast.String(&token.Token{
			Type:     token.StringType,
			Value:    s.Value,
			Origin:   lines,
			Position: &pos,
		})

		lit.Comment = s.GetComment()

		if b.set == nil {
			root = lit
		} else {
			b.set(lit)
		}
	}

	return root
}

// keySafe returns key, or a string in place of key when key is a literal
// block, as [quoter.unblock] describes. It quotes a string in key under
// the rules of an implicit key, which double-quote the string of a block
// whose text holds a line break. It looks through the tags and anchors
// that a marshaler's YAML can put on a key. It replaces a literal block
// under them the same way.
func (q quoter) keySafe(key ast.MapKeyNode) ast.MapKeyNode {
	switch k := key.(type) {
	case *ast.StringNode:
		q.quote(k, true)
	case *ast.LiteralNode:
		s := q.blockString(k, k.Start.Position.Column)
		q.quote(s, true)

		return s

	case *ast.TagNode:
		if inner, ok := k.Value.(ast.MapKeyNode); ok {
			tagged := q
			tagged.tagged = true
			k.Value = tagged.keySafe(inner)
		}

	case *ast.AnchorNode:
		if inner, ok := k.Value.(ast.MapKeyNode); ok {
			k.Value = q.keySafe(inner)
		}
	}

	return key
}

// quote double-quotes n when go-yaml otherwise writes it in a form that
// does not read back as its value. It checks the unquoted, single-quoted,
// and literal block forms. A mapping key follows the stricter rules of an
// implicit key.
func (q quoter) quote(n *ast.StringNode, key bool) {
	if n.Token.Type == token.SingleQuoteType && strings.ContainsAny(n.Value, "\n\r") {
		// Only a marshaler's YAML holds a single-quoted string, and the
		// go-yaml printer writes its line breaks as is. YAML folds a line
		// break in single quotes, so a single one reads back as a space.
		n.Token.Type = token.DoubleQuoteType
	}

	if unprintable(n.Value) {
		// YAML does not allow such a character in a text, and the go-yaml
		// printer writes the characters of a string as they are in every
		// other form. In double quotes, it writes the escapes of Go, and
		// YAML reads each of those as the character.
		n.Token.Type = token.DoubleQuoteType
	}

	if n.Token.Type == token.SingleQuoteType || n.Token.Type == token.DoubleQuoteType {
		return
	}

	var safe bool

	switch {
	case strings.ContainsRune(n.Value, '\uFEFF'):
		// YAML allows a byte order mark only in a quoted scalar, and a
		// parser drops one that opens a document.
		safe = false
	case strings.ContainsAny(n.Value, "\n\r"):
		// The go-yaml printer writes a string with a line break as a
		// literal block, which cannot be an implicit key or sit in a
		// flow collection. The printer indents the block from the
		// position of the string. The encoder builds a string at the
		// position it writes it, with its value as its origin. So does
		// [quoter.unblock] for a block from a marshaler's YAML. A plain
		// string from a marshaler's YAML keeps its position in that
		// YAML, and its origin differs from its value, since only a
		// blank line puts a line break in a plain value.
		safe = !key && !q.flow && n.Token.Origin == n.Value && literalSafe(n.Value)
	default:
		// The go-yaml encoder types the token of a string by its text,
		// so a type other than string marks a keyword such as ".inf" or
		// ".nan", which reads back as a float. Go-yaml types some texts
		// as strings that YAML 1.2 reads as other values, such as 1e3. In
		// a marshaler's YAML, the go-yaml parser gives the string type to
		// the plain scalar after a custom tag, whatever its text, and that
		// scalar stays as the marshaler wrote it.
		safe = n.Token.Type == token.StringType && (q.tagged || !coreScalar.MatchString(n.Value)) &&
			plainSafe(n.Value, key, q.flow)
	}

	if !safe {
		n.Token.Type = token.DoubleQuoteType
	}
}

// unprintable reports whether s, a UTF-8 string, holds a character that
// YAML does not allow in a text, such as U+0001. The printable characters
// of YAML 1.2 include the tab, the line breaks, and U+0085. A string that
// is not UTF-8 does not count. In double quotes, the go-yaml printer
// writes each byte that is not UTF-8 as an escape that YAML reads as
// another character, as YAML reads \xff as U+00FF.
func unprintable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}

	return strings.ContainsFunc(s, func(r rune) bool {
		switch {
		case r == '\t', r == '\n', r == '\r', r == 0x85:
			return false
		case r < 0x20, r >= 0x7f && r < 0xa0:
			return true
		default:
			return r >= 0xfffe && r <= 0xffff
		}
	})
}

// coreScalar matches the plain scalars that the core schema of YAML 1.2
// reads as a null, a bool, an int, or a float, as its section 10.3.2 lists
// them. A string of such a text reads as another value when it stays
// plain.
var coreScalar = regexp.MustCompile(`^(?:null|Null|NULL|~|true|True|TRUE|false|False|FALSE|` +
	`[-+]?[0-9]+|0o[0-7]+|0x[0-9a-fA-F]+|[-+]?(?:\.[0-9]+|[0-9]+(?:\.[0-9]*)?)(?:[eE][-+]?[0-9]+)?|` +
	`[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))$`)

// quoteEntry double-quotes n, an entry of a block sequence, when go-yaml
// otherwise writes it as a literal block that does not read back as its
// value. The go-yaml printer removes two spaces from the start of each
// line of such a block. With an indent of one space, the printer also
// removes the first space of a line that starts with one.
func quoteEntry(n *ast.StringNode) {
	if n.Token.Type == token.SingleQuoteType || n.Token.Type == token.DoubleQuoteType {
		return
	}

	lines := strings.Contains(n.Value, "\n")
	space := strings.HasPrefix(n.Value, " ") || strings.Contains(n.Value, "\n ")

	if lines && space && n.Token.Position.IndentNum < 2 {
		n.Token.Type = token.DoubleQuoteType
	}
}

// plainSafe reports whether s reads back as s when go-yaml writes it as
// is. The caller passes a string that does not hold a line break or a
// keyword for another type.
func plainSafe(s string, key, flow bool) bool {
	switch {
	case s == "":
		// An empty plain scalar reads back as null.
		return false
	case s[0] == '"' || s[0] == '\'':
		// A plain scalar never starts with a quote, so go-yaml has
		// already quoted s.
		return true
	case strings.HasPrefix(s, "---"), strings.HasPrefix(s, "..."):
		// A leading "---" or "..." can read as a document marker.
		return false
	case key && strings.HasSuffix(s, "<<"):
		// The go-yaml parser reads a plain key that ends in "<<" as a
		// merge key.
		return false
	case strings.ContainsRune(s, '\t'):
		// The go-yaml parser drops a tab from a plain scalar.
		return false
	case flow && strings.ContainsAny(s, "[]{},"):
		// A flow indicator ends a plain scalar in a flow collection.
		return false
	case s[0] == '?' || s[0] == '-':
		// A leading "?" or "-" is an indicator before the end, a space,
		// or a NUL byte, which the go-yaml parser reads as a space.
		return len(s) > 1 && s[1] != ' ' && s[1] != 0
	}

	return true
}

// literalSafe reports whether s, a string with a line break, is safe to
// write as a literal block. It reports false when go-yaml might write s as
// a block that reads back as a different value or does not parse. It
// leaves the sequence entries that [quoteEntry] checks to that function.
func literalSafe(s string) bool {
	// A literal block reads every line break back as "\n".
	if strings.ContainsRune(s, '\r') {
		return false
	}

	// A literal block takes its indentation from its first line with
	// content, so that line cannot start with a space. The go-yaml
	// parser also misreads or rejects many blocks whose first line with
	// content starts with a tab. A block of line breaks alone has no
	// line with content. From such a block, the parser reads "\n" back
	// as "", and it rejects any run of line breaks that another sequence
	// entry follows.
	body := strings.TrimLeft(s, "\n")
	if body == "" || body[0] == ' ' || body[0] == '\t' {
		return false
	}

	// The go-yaml parser and printer can lose spaces at the end of the
	// last line of a block, so s cannot end in a space, or in a space and
	// one line break. The parser drops those spaces when s has no final
	// line break, and the printer trims as many spaces as the block
	// indent from the end of the block. Other spaces and tabs at the end
	// of a line read back as written.
	return !strings.HasSuffix(strings.TrimSuffix(s, "\n"), " ")
}
