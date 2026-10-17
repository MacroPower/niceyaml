package encoder

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// quoter is an [ast.Visitor] that double-quotes each string that go-yaml
// otherwise writes in a form that reads back as a different value or does
// not parse. It checks the unquoted, single-quoted, and literal block
// forms. It also double-quotes a literal block from a marshaler's YAML
// that sits where YAML forbids one. It leaves a block collection in a flow
// collection as it is. Go-yaml writes that collection in block style, and
// quoting does not make block style valid there. Only a marshaler's YAML
// puts a block collection there.
type quoter struct {
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
		n.Value = q.flowSafe(n.Value)
		ast.Walk(q, n.Value)

		return nil

	case *ast.AliasNode, *ast.LiteralNode:
		// An alias holds an anchor name, and a literal block holds the
		// text it writes.
		return nil
	case *ast.TagNode:
		n.Value = q.flowSafe(n.Value)

		return quoter{flow: q.flow, tagged: true}

	case *ast.MappingNode:
		if q.flow && !n.IsFlowStyle {
			return nil
		}

		return quoter{flow: n.IsFlowStyle}

	case *ast.SequenceNode:
		if q.flow && !n.IsFlowStyle {
			return nil
		}

		inner := quoter{flow: n.IsFlowStyle}

		for i, value := range n.Values {
			n.Values[i] = inner.flowSafe(value)

			if s, ok := value.(*ast.StringNode); ok && !inner.flow {
				quoteEntry(s)
			}
		}

		return inner

	case *ast.MappingValueNode:
		n.Key = q.keySafe(n.Key)
		n.Value = q.flowSafe(n.Value)

	case *ast.StringNode:
		q.quote(n, false)
	}

	return q
}

// flowSafe returns n, or a double-quoted string in place of n when n is a
// literal block in a flow collection. A flow collection cannot hold a
// literal block. Such a block comes from a marshaler whose output go-yaml
// parses.
func (q quoter) flowSafe(n ast.Node) ast.Node {
	lit, ok := n.(*ast.LiteralNode)
	if !ok || !q.flow {
		return n
	}

	return doubleQuoted(lit)
}

// keySafe returns key, or a double-quoted string in place of key when key
// is a literal block. A literal block cannot be an implicit key. It quotes
// a string in key under the rules of an implicit key. It looks through
// the tags and anchors that a marshaler's YAML can put on a key. It
// replaces a literal block under them the same way.
func (q quoter) keySafe(key ast.MapKeyNode) ast.MapKeyNode {
	switch k := key.(type) {
	case *ast.StringNode:
		q.quote(k, true)
	case *ast.LiteralNode:
		return doubleQuoted(k)
	case *ast.TagNode:
		if inner, ok := k.Value.(ast.MapKeyNode); ok {
			k.Value = quoter{flow: q.flow, tagged: true}.keySafe(inner)
		}

	case *ast.AnchorNode:
		if inner, ok := k.Value.(ast.MapKeyNode); ok {
			k.Value = q.keySafe(inner)
		}
	}

	return key
}

// doubleQuoted returns a double-quoted string that holds the text of lit.
func doubleQuoted(lit *ast.LiteralNode) *ast.StringNode {
	s := lit.Value.Value

	return ast.String(token.DoubleQuote(s, strconv.Quote(s), lit.Start.Position))
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
		// position it writes it, with its value as its origin. A plain
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
