package format

import (
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// FormatNodeWithResolvedAlias formats n with each alias written out as the
// content of its anchor. The names in open are the anchors whose values hold
// n. An alias to one of them, or to an anchor that the formatter is writing
// out, reads as null, as the decoder reads it.
//
// The function key returns the key of anchorNodeMap and of open for the
// anchor that an alias reads. An alias that key binds to a null node that
// holds the token of the alias reads as null.
//
// The count budget, when it is not nil, holds the bytes that the formatter
// may still write for aliases. The formatter takes the text of each alias
// off the count, and writes no further alias once the count is below zero.
//
// The formatter reads every token of the parse of n once and keeps what it
// read in origins, when origins is not nil. A caller that formats many nodes
// of one parse hands in the same Origins each time.
func FormatNodeWithResolvedAlias(n ast.Node, anchorNodeMap map[string]ast.Node, open map[string]struct{}, key func(*ast.AliasNode) string, budget *int, origins *Origins) string {
	tk := getFirstToken(n)
	if tk == nil {
		return ""
	}
	formatter := origins.formatter(tk, hasComment(n))
	formatter.anchorNodeMap = anchorNodeMap
	formatter.aliasKey = key
	formatter.budget = budget
	formatter.expanding = make(map[string]bool, len(open))
	for name := range open {
		formatter.expanding[name] = true
	}
	return formatter.format(n)
}

func FormatNode(n ast.Node) string {
	tk := getFirstToken(n)
	if tk == nil {
		return ""
	}
	return newFormatter(tk, hasComment(n)).format(n)
}

func FormatFile(file *ast.File) string {
	if len(file.Docs) == 0 {
		return ""
	}
	tk := getFirstToken(file.Docs[0])
	if tk == nil {
		return ""
	}
	return newFormatter(tk, hasCommentFile(file)).formatFile(file)
}

func hasCommentFile(f *ast.File) bool {
	for _, doc := range f.Docs {
		if hasComment(doc.Body) {
			return true
		}
	}
	return false
}

func hasComment(n ast.Node) bool {
	if n == nil {
		return false
	}
	switch nn := n.(type) {
	case *ast.DocumentNode:
		return hasComment(nn.Body)
	case *ast.NullNode:
		return nn.Comment != nil
	case *ast.BoolNode:
		return nn.Comment != nil
	case *ast.IntegerNode:
		return nn.Comment != nil
	case *ast.FloatNode:
		return nn.Comment != nil
	case *ast.StringNode:
		return nn.Comment != nil
	case *ast.InfinityNode:
		return nn.Comment != nil
	case *ast.NanNode:
		return nn.Comment != nil
	case *ast.LiteralNode:
		return nn.Comment != nil
	case *ast.DirectiveNode:
		if nn.Comment != nil {
			return true
		}
		for _, value := range nn.Values {
			if hasComment(value) {
				return true
			}
		}
	case *ast.TagNode:
		if nn.Comment != nil {
			return true
		}
		return hasComment(nn.Value)
	case *ast.MappingNode:
		if nn.Comment != nil || nn.FootComment != nil {
			return true
		}
		for _, value := range nn.Values {
			if value.Comment != nil || value.FootComment != nil {
				return true
			}
			if hasComment(value.Key) {
				return true
			}
			if hasComment(value.Value) {
				return true
			}
		}
	case *ast.MappingKeyNode:
		return nn.Comment != nil
	case *ast.MergeKeyNode:
		return nn.Comment != nil
	case *ast.SequenceNode:
		if nn.Comment != nil || nn.FootComment != nil {
			return true
		}
		for _, entry := range nn.Entries {
			if entry.Comment != nil || entry.HeadComment != nil || entry.LineComment != nil {
				return true
			}
			if hasComment(entry.Value) {
				return true
			}
		}
	case *ast.AnchorNode:
		if nn.Comment != nil {
			return true
		}
		if hasComment(nn.Name) || hasComment(nn.Value) {
			return true
		}
	case *ast.AliasNode:
		if nn.Comment != nil {
			return true
		}
		if hasComment(nn.Value) {
			return true
		}
	}
	return false
}

func getFirstToken(n ast.Node) *token.Token {
	if n == nil {
		return nil
	}
	switch nn := n.(type) {
	case *ast.DocumentNode:
		if nn.Start != nil {
			return nn.Start
		}
		return getFirstToken(nn.Body)
	case *ast.NullNode:
		return nn.Token
	case *ast.BoolNode:
		return nn.Token
	case *ast.IntegerNode:
		return nn.Token
	case *ast.FloatNode:
		return nn.Token
	case *ast.StringNode:
		return nn.Token
	case *ast.InfinityNode:
		return nn.Token
	case *ast.NanNode:
		return nn.Token
	case *ast.LiteralNode:
		return nn.Start
	case *ast.DirectiveNode:
		return nn.Start
	case *ast.TagNode:
		return nn.Start
	case *ast.MappingNode:
		if nn.IsFlowStyle {
			return nn.Start
		}
		if len(nn.Values) == 0 {
			return nn.Start
		}
		return getFirstToken(nn.Values[0].Key)
	case *ast.MappingKeyNode:
		return nn.Start
	case *ast.MergeKeyNode:
		return nn.Token
	case *ast.SequenceNode:
		return nn.Start
	case *ast.AnchorNode:
		return nn.Start
	case *ast.AliasNode:
		return nn.Start
	}
	return nil
}

type Formatter struct {
	existsComment    bool
	tokenToOriginMap map[*token.Token]string
	anchorNodeMap    map[string]ast.Node
	expanding        map[string]bool
	aliasKey         func(*ast.AliasNode) string
	budget           *int
	// aliases counts the aliases that the formatter is writing out.
	aliases int
	// flow counts the flow collections that hold the node that the
	// formatter writes.
	flow int
}

// Origins holds the text that a [Formatter] writes for each token of the
// parses it has read.
type Origins struct {
	texts map[*token.Token]string
}

// formatter returns a Formatter for the parse that holds tk. It reads the
// tokens of that parse only when o does not hold them yet.
func (o *Origins) formatter(tk *token.Token, existsComment bool) *Formatter {
	if o == nil {
		return newFormatter(tk, existsComment)
	}
	if o.texts == nil {
		o.texts = make(map[*token.Token]string)
	}
	readParse(o.texts, tk)
	return &Formatter{existsComment: existsComment, tokenToOriginMap: o.texts}
}

// readParse adds to texts the text of each token of the parse that holds
// tk, when texts does not hold tk yet.
func readParse(texts map[*token.Token]string, tk *token.Token) {
	if tk == nil {
		return
	}
	if _, held := texts[tk]; held {
		return
	}
	read := newFormatter(tk, false).tokenToOriginMap
	for t, text := range read {
		texts[t] = text
	}
	// A comment token has no text of its own in the map.
	if _, held := texts[tk]; !held {
		texts[tk] = read[tk]
	}
}

func newFormatter(tk *token.Token, existsComment bool) *Formatter {
	tokenToOriginMap := make(map[*token.Token]string)
	for tk.Prev != nil {
		tk = tk.Prev
	}
	tokenToOriginMap[tk] = tk.Origin

	var origin string
	for tk.Next != nil {
		tk = tk.Next
		if tk.Type == token.CommentType {
			origin += strings.Repeat("\n", strings.Count(normalizeNewLineChars(tk.Origin), "\n"))
			continue
		}
		origin += tk.Origin
		tokenToOriginMap[tk] = origin
		origin = ""
	}
	return &Formatter{
		existsComment:    existsComment,
		tokenToOriginMap: tokenToOriginMap,
	}
}

func getIndentNumByFirstLineToken(tk *token.Token) int {
	defaultIndent := tk.Position.Column - 1

	// key: value
	//    ^
	//   next
	if tk.Type == token.SequenceEntryType {
		// If the current token is the sequence entry.
		// the indent is calculated from the column value of the current token.
		return defaultIndent
	}

	// key: value
	//    ^
	//   next
	if tk.Next != nil && tk.Next.Type == token.MappingValueType {
		// If the current token is the key in the mapping-value,
		// the indent is calculated from the column value of the current token.
		return defaultIndent
	}

	if tk.Prev == nil {
		return defaultIndent
	}
	prev := tk.Prev

	// key: value
	//    ^
	//   prev
	if prev.Type == token.MappingValueType {
		// If the current token is the value in the mapping-value,
		// the indent is calculated from the column value of the key two steps back.
		if prev.Prev == nil {
			return defaultIndent
		}
		return prev.Prev.Position.Column - 1
	}

	// - value
	// ^
	// prev
	if prev.Type == token.SequenceEntryType {
		// If the value is not a mapping-value and the previous token was a sequence entry,
		// the indent is calculated using the column value of the sequence entry token.
		return prev.Position.Column - 1
	}

	return defaultIndent
}

func (f *Formatter) format(n ast.Node) string {
	return f.trimSpacePrefix(
		f.trimIndentSpace(
			getIndentNumByFirstLineToken(getFirstToken(n)),
			f.trimNewLineCharPrefix(f.formatNode(n)),
		),
	)
}

func (f *Formatter) formatFile(file *ast.File) string {
	if len(file.Docs) == 0 {
		return ""
	}
	var ret string
	for _, doc := range file.Docs {
		ret += f.formatDocument(doc)
	}
	return ret
}

func (f *Formatter) origin(tk *token.Token) string {
	if tk == nil {
		return ""
	}
	text := f.tokenToOriginMap[tk]
	if f.existsComment {
		text = tk.Origin
	}
	if f.aliases > 0 && endsParse(tk) {
		// The last token of a parse holds the space and the line breaks
		// that end the text of the parse. They belong to no node, so the
		// text of an alias leaves them out.
		text = strings.TrimRight(text, " \t\r\n")
	}
	return text
}

// endsParse reports whether tk is the last token of its parse. It reports
// false for the content of a block scalar, because the line breaks of that
// content belong to its value. A comment can sit between the header of a
// block scalar and its content.
func endsParse(tk *token.Token) bool {
	if tk.Next != nil {
		return false
	}
	prev := tk.Prev
	for prev != nil && prev.Type == token.CommentType {
		prev = prev.Prev
	}
	return prev == nil || (prev.Type != token.LiteralType && prev.Type != token.FoldedType)
}

func (f *Formatter) formatDocument(n *ast.DocumentNode) string {
	return f.origin(n.Start) + f.formatNode(n.Body) + f.origin(n.End)
}

func (f *Formatter) formatNull(n *ast.NullNode) string {
	return f.origin(n.Token) + f.formatCommentGroup(n.Comment)
}

func (f *Formatter) formatString(n *ast.StringNode) string {
	return f.origin(n.Token) + f.formatCommentGroup(n.Comment)
}

func (f *Formatter) formatInteger(n *ast.IntegerNode) string {
	return f.origin(n.Token) + f.formatCommentGroup(n.Comment)
}

func (f *Formatter) formatFloat(n *ast.FloatNode) string {
	return f.origin(n.Token) + f.formatCommentGroup(n.Comment)
}

func (f *Formatter) formatBool(n *ast.BoolNode) string {
	return f.origin(n.Token) + f.formatCommentGroup(n.Comment)
}

func (f *Formatter) formatInfinity(n *ast.InfinityNode) string {
	return f.origin(n.Token) + f.formatCommentGroup(n.Comment)
}

func (f *Formatter) formatNan(n *ast.NanNode) string {
	return f.origin(n.Token) + f.formatCommentGroup(n.Comment)
}

func (f *Formatter) formatLiteral(n *ast.LiteralNode) string {
	return f.origin(n.Start) + f.formatCommentGroup(n.Comment) + f.origin(n.Value.Token)
}

func (f *Formatter) formatMergeKey(n *ast.MergeKeyNode) string {
	return f.origin(n.Token)
}

func (f *Formatter) formatMappingValue(n *ast.MappingValueNode) string {
	return f.formatCommentGroup(n.Comment) +
		f.origin(n.Key.GetToken()) + ":" + f.formatCommentGroup(n.Key.GetComment()) + f.formatNode(n.Value) +
		f.formatCommentGroup(n.FootComment)
}

func (f *Formatter) formatDirective(n *ast.DirectiveNode) string {
	ret := f.origin(n.Start) + f.formatNode(n.Name)
	for _, val := range n.Values {
		ret += f.formatNode(val)
	}
	return ret
}

func (f *Formatter) formatMapping(n *ast.MappingNode) string {
	var ret string
	if n.IsFlowStyle {
		f.flow++
		defer func() { f.flow-- }()
		ret = f.origin(n.Start)
	} else {
		ret += f.formatCommentGroup(n.Comment)
	}
	for _, value := range n.Values {
		if value.CollectEntry != nil {
			ret += f.origin(value.CollectEntry)
		}
		ret += f.formatMappingValue(value)
	}
	if n.IsFlowStyle {
		ret += f.origin(n.End)
		ret += f.formatCommentGroup(n.Comment)
	}
	return ret
}

func (f *Formatter) formatTag(n *ast.TagNode) string {
	return f.origin(n.Start) + f.formatNode(n.Value)
}

func (f *Formatter) formatMappingKey(n *ast.MappingKeyNode) string {
	return f.origin(n.Start) + f.formatNode(n.Value)
}

func (f *Formatter) formatSequence(n *ast.SequenceNode) string {
	var ret string
	if n.IsFlowStyle {
		f.flow++
		defer func() { f.flow-- }()
		ret = f.origin(n.Start)
	} else {
		// add head comment.
		ret += f.formatCommentGroup(n.Comment)
	}
	for _, entry := range n.Entries {
		ret += f.formatNode(entry)
	}
	if n.IsFlowStyle {
		ret += f.origin(n.End)
		ret += f.formatCommentGroup(n.Comment)
	}
	ret += f.formatCommentGroup(n.FootComment)
	return ret
}

func (f *Formatter) formatSequenceEntry(n *ast.SequenceEntryNode) string {
	return f.formatCommentGroup(n.HeadComment) + f.origin(n.Start) + f.formatCommentGroup(n.LineComment) + f.formatNode(n.Value)
}

func (f *Formatter) formatAnchor(n *ast.AnchorNode) string {
	return f.origin(n.Start) + f.formatNode(n.Name) + f.formatNode(n.Value)
}

func (f *Formatter) formatAlias(n *ast.AliasNode) string {
	if f.anchorNodeMap != nil {
		anchorName := f.aliasKey(n)
		node := f.anchorNodeMap[anchorName]
		if null, ok := node.(*ast.NullNode); ok && null.Token == n.Start {
			org := f.origin(n.Start)
			return org[:len(org)-len(strings.TrimLeft(org, " \t\r\n"))] + "null"
		}
		if node != nil && f.expanding[anchorName] {
			org := f.origin(n.Start)
			return org[:len(org)-len(strings.TrimLeft(org, " \t\r\n"))] + "null"
		}
		if node != nil && f.budget != nil && *f.budget < 0 {
			return ""
		}
		if node != nil {
			// The anchor can sit in another parse than n, such as a
			// document that a resolver from ResolveAlias reads.
			if !f.existsComment {
				readParse(f.tokenToOriginMap, getFirstToken(node))
			}
			f.expanding[anchorName] = true
			f.aliases++
			var formatted string
			if f.flow > 0 {
				// The layout of the anchor can be one that a flow
				// collection does not allow, such as a block mapping, so
				// the alias writes the anchor in flow style.
				org := f.origin(n.Start)
				formatted = org[:len(org)-len(strings.TrimLeft(org, " \t\r\n"))] + f.formatFlow(node)
			} else {
				formatted = f.formatNode(node)
			}
			f.aliases--
			delete(f.expanding, anchorName)
			if f.budget != nil {
				*f.budget -= len(formatted)
			}
			if f.flow > 0 {
				return formatted
			}
			// If formatted text contains newline characters, indentation needs to be considered.
			if strings.Contains(formatted, "\n") {
				// If the first character is not a newline, the first line should be output without indentation.
				isIgnoredFirstLine := !strings.HasPrefix(formatted, "\n")
				formatted = f.addIndentSpace(n.GetToken().Position.IndentNum, formatted, isIgnoredFirstLine)
			}
			return formatted
		}
	}
	return f.origin(n.Start) + f.formatNode(n.Value)
}

// formatFlow writes n in flow style on one line, so the text reads as the
// value of n inside a flow collection. It writes each collection in flow
// style and leaves out every comment. It keeps the text of each scalar that
// reads as the same value inside a flow collection, and writes any other
// string as a double-quoted string.
func (f *Formatter) formatFlow(n ast.Node) string {
	switch nn := n.(type) {
	case *ast.NullNode:
		if text := f.flowText(nn.Token); text != "" {
			return text
		}
		return "null"
	case *ast.BoolNode:
		return f.flowText(nn.Token)
	case *ast.IntegerNode:
		return f.flowText(nn.Token)
	case *ast.FloatNode:
		return f.flowText(nn.Token)
	case *ast.InfinityNode:
		return f.flowText(nn.Token)
	case *ast.NanNode:
		return f.flowText(nn.Token)
	case *ast.StringNode:
		return f.formatFlowString(nn)
	case *ast.LiteralNode:
		return strconv.Quote(nn.Value.Value)
	case *ast.MergeKeyNode:
		return f.flowText(nn.Token)
	case *ast.TagNode:
		return strings.Trim(f.flowText(nn.Start)+" "+f.formatFlow(nn.Value), " \t\r\n")
	case *ast.AnchorNode:
		return strings.Trim(f.flowText(nn.Start)+f.flowText(getFirstToken(nn.Name))+" "+f.formatFlow(nn.Value), " \t\r\n")
	case *ast.AliasNode:
		return strings.Trim(f.formatAlias(nn), " \t\r\n")
	case *ast.MappingKeyNode:
		return f.formatFlow(nn.Value)
	case *ast.MappingNode:
		entries := make([]string, 0, len(nn.Values))
		for _, value := range nn.Values {
			entries = append(entries, f.formatFlow(value))
		}
		return "{" + strings.Join(entries, ", ") + "}"
	case *ast.MappingValueNode:
		return f.formatFlow(nn.Key) + ": " + f.formatFlow(nn.Value)
	case *ast.SequenceNode:
		entries := make([]string, 0, len(nn.Values))
		for _, value := range nn.Values {
			entries = append(entries, f.formatFlow(value))
		}
		return "[" + strings.Join(entries, ", ") + "]"
	case *ast.SequenceEntryNode:
		return f.formatFlow(nn.Value)
	}
	return ""
}

// flowText returns the text of tk without the spaces, the tabs, and the
// line breaks around it. It keeps any other white space, such as a
// no-break space, because a plain scalar keeps it in its value.
func (f *Formatter) flowText(tk *token.Token) string {
	return strings.Trim(f.origin(tk), " \t\r\n")
}

// formatFlowString writes n as text that reads as the same string inside a
// flow collection. A quoted string on one line keeps its text, and so does a
// plain string on one line without a flow indicator or a colon.
func (f *Formatter) formatFlowString(n *ast.StringNode) string {
	text := f.flowText(n.Token)
	if strings.ContainsAny(text, "\r\n") {
		return strconv.Quote(n.Value)
	}
	switch n.Token.Type {
	case token.SingleQuoteType, token.DoubleQuoteType:
		return text
	}
	if text == "" || strings.ContainsAny(text, ",[]{}:") {
		return strconv.Quote(n.Value)
	}
	return text
}

func (f *Formatter) formatNode(n ast.Node) string {
	switch nn := n.(type) {
	case *ast.DocumentNode:
		return f.formatDocument(nn)
	case *ast.NullNode:
		return f.formatNull(nn)
	case *ast.BoolNode:
		return f.formatBool(nn)
	case *ast.IntegerNode:
		return f.formatInteger(nn)
	case *ast.FloatNode:
		return f.formatFloat(nn)
	case *ast.StringNode:
		return f.formatString(nn)
	case *ast.InfinityNode:
		return f.formatInfinity(nn)
	case *ast.NanNode:
		return f.formatNan(nn)
	case *ast.LiteralNode:
		return f.formatLiteral(nn)
	case *ast.DirectiveNode:
		return f.formatDirective(nn)
	case *ast.TagNode:
		return f.formatTag(nn)
	case *ast.MappingNode:
		return f.formatMapping(nn)
	case *ast.MappingKeyNode:
		return f.formatMappingKey(nn)
	case *ast.MappingValueNode:
		return f.formatMappingValue(nn)
	case *ast.MergeKeyNode:
		return f.formatMergeKey(nn)
	case *ast.SequenceNode:
		return f.formatSequence(nn)
	case *ast.SequenceEntryNode:
		return f.formatSequenceEntry(nn)
	case *ast.AnchorNode:
		return f.formatAnchor(nn)
	case *ast.AliasNode:
		return f.formatAlias(nn)
	}
	return ""
}

func (f *Formatter) formatCommentGroup(g *ast.CommentGroupNode) string {
	if g == nil {
		return ""
	}
	var ret string
	for _, cm := range g.Comments {
		ret += f.formatComment(cm)
	}
	return ret
}

func (f *Formatter) formatComment(n *ast.CommentNode) string {
	if n == nil {
		return ""
	}
	return n.Token.Origin
}

// nolint: unused
func (f *Formatter) formatIndent(col int) string {
	if col <= 1 {
		return ""
	}
	return strings.Repeat(" ", col-1)
}

func (f *Formatter) trimNewLineCharPrefix(v string) string {
	return strings.TrimLeftFunc(v, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
}

func (f *Formatter) trimSpacePrefix(v string) string {
	return strings.TrimLeftFunc(v, func(r rune) bool {
		return r == ' '
	})
}

func (f *Formatter) trimIndentSpace(trimIndentNum int, v string) string {
	if trimIndentNum == 0 {
		return v
	}
	lines := strings.Split(normalizeNewLineChars(v), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		var cnt int
		out = append(out, strings.TrimLeftFunc(line, func(r rune) bool {
			cnt++
			return r == ' ' && cnt <= trimIndentNum
		}))
	}
	return strings.Join(out, "\n")
}

func (f *Formatter) addIndentSpace(indentNum int, v string, isIgnoredFirstLine bool) string {
	if indentNum == 0 {
		return v
	}
	indent := strings.Repeat(" ", indentNum)
	lines := strings.Split(normalizeNewLineChars(v), "\n")
	out := make([]string, 0, len(lines))
	for idx, line := range lines {
		if line == "" || (isIgnoredFirstLine && idx == 0) {
			out = append(out, line)
			continue
		}
		out = append(out, indent+line)
	}
	return strings.Join(out, "\n")
}

// normalizeNewLineChars normalize CRLF and CR to LF.
func normalizeNewLineChars(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(v, "\r\n", "\n"), "\r", "\n")
}
