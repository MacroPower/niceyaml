package edit

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/encoder"
)

// rendered is the text of a value that an edit writes.
type rendered struct {
	// The text in flow style, on one line, which a flow collection holds.
	flow string
	// The lines in block style, without a line break at the end, for a
	// collection or a block scalar. A plain or quoted scalar does not have
	// block lines.
	block []string
	// Whether the value is a mapping or a sequence.
	collection bool
	// Whether the value is a mapping.
	mapping bool
	// Whether the value is an empty mapping or sequence.
	empty bool
}

// render returns the text of v. A block collection indents by step, and
// a block sequence under a key indents its "-" by step when seqIndent is
// true.
func render(ctx context.Context, v any, step int, seqIndent bool) (rendered, error) {
	flow, err := encoder.Marshal(ctx, v, encoder.WithYAMLOptions(yaml.Flow(true)))
	if err != nil {
		return rendered{}, fmt.Errorf("encode the value: %w", err)
	}

	block, err := encoder.Marshal(ctx, v, encoder.WithIndent(step), encoder.WithIndentSequence(seqIndent))
	if err != nil {
		return rendered{}, fmt.Errorf("encode the value: %w", err)
	}

	r := rendered{flow: strings.TrimSuffix(string(flow), "\n")}

	switch {
	case strings.HasPrefix(r.flow, "{"):
		r.collection, r.mapping = true, true
	case strings.HasPrefix(r.flow, "["):
		r.collection = true
	}

	r.empty = r.flow == "{}" || r.flow == "[]"

	text := strings.TrimSuffix(string(block), "\n")
	if (r.collection || strings.Contains(text, "\n")) && !r.empty {
		r.block = strings.Split(text, "\n")
	}

	// A flow collection cannot hold a block scalar, so a string with a line
	// break takes double quotes there.
	if !r.collection && (strings.HasPrefix(r.flow, "|") || strings.HasPrefix(r.flow, ">")) {
		if s, ok := v.(string); ok {
			r.flow = doubleQuoted(s)
		}
	}

	return r, nil
}

// scalarStyle returns the text of s, a Go string that replaces a scalar of
// the style of old, in that style when the text allows it. It reports
// false when the style of old does not apply.
func scalarStyle(s string, old *token.Token) (string, bool) {
	if old == nil {
		return "", false
	}

	switch old.Type {
	case token.DoubleQuoteType:
		return doubleQuoted(s), true

	case token.SingleQuoteType:
		if strings.ContainsAny(s, "\r\n") {
			return doubleQuoted(s), true
		}

		return "'" + strings.ReplaceAll(s, "'", "''") + "'", true

	default:
		return "", false
	}
}

// doubleQuoted returns s as a double-quoted YAML scalar.
func doubleQuoted(s string) string {
	var b strings.Builder

	b.WriteByte('"')

	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			// A \u escape takes four hex digits, so a rune past U+FFFF
			// takes the \U escape of eight.
			if !unicode.IsPrint(r) && r != utf8.RuneError {
				if r > 0xffff {
					fmt.Fprintf(&b, `\U%08x`, r)
				} else {
					fmt.Fprintf(&b, `\u%04x`, r)
				}

				continue
			}

			b.WriteRune(r)
		}
	}

	b.WriteByte('"')

	return b.String()
}

// renderKey returns the text of key as a key of a mapping. The argument
// old is the token of the key that key replaces, or nil for a new key. A
// plain key takes double quotes when it might read back as another value,
// and inside a flow collection when it holds a flow indicator.
func renderKey(ctx context.Context, key string, old *token.Token, flow bool) (string, error) {
	if s, ok := scalarStyle(key, old); ok {
		return s, nil
	}

	opts := []encoder.Option{}
	if flow {
		opts = append(opts, encoder.WithYAMLOptions(yaml.Flow(true)))
	}

	data, err := encoder.Marshal(ctx, map[string]any{key: nil}, opts...)
	if err != nil {
		return "", fmt.Errorf("encode the key: %w", err)
	}

	text := strings.TrimSuffix(string(data), "\n")
	text = strings.TrimPrefix(text, "{")
	text = strings.TrimSuffix(text, "}")

	// The key itself might hold ": null", so the cut takes the suffix.
	head, ok := strings.CutSuffix(text, ": null")
	if !ok {
		return doubleQuoted(key), nil
	}

	if flow && strings.ContainsAny(head, ",[]{}") && !strings.HasPrefix(head, `"`) {
		return doubleQuoted(key), nil
	}

	return head, nil
}

// keyName returns the name that a decode into a map[string]any gives the
// key whose text is text, or text itself when it does not decode.
func keyName(text string) string {
	var m map[string]any

	err := yaml.Unmarshal([]byte("{"+text+": 0}"), &m)
	if err != nil || len(m) != 1 {
		return text
	}

	for k := range m {
		return k
	}

	return text
}

// plainText reports whether text reads back as a plain scalar with the
// same text. A type field thus keeps the plain style of the text that it
// replaces.
func plainText(text string) bool {
	if text == "" || strings.TrimSpace(text) != text {
		return false
	}

	tks := lexer.Tokenize(text)
	if len(tks) != 1 {
		return false
	}

	tk := tks[0]
	if tk.Value != text {
		return false
	}

	switch tk.Type {
	case token.SingleQuoteType, token.DoubleQuoteType, token.LiteralType, token.FoldedType,
		token.CommentType, token.AnchorType, token.AliasType, token.TagType:
		return false
	default:
		return tk.Type != token.InvalidType
	}
}

// fieldText returns the text of the value of a type field that replaces
// old, or of a new one for a nil old.
func fieldText(ctx context.Context, text string, old *token.Token) (string, error) {
	if s, ok := scalarStyle(text, old); ok {
		return s, nil
	}

	if plainText(text) {
		return text, nil
	}

	r, err := render(ctx, text, 2, false)
	if err != nil {
		return "", err
	}

	if r.block != nil {
		return strconv.Quote(text), nil
	}

	return r.flow, nil
}
