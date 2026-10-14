package line_test

import (
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// newToken returns a token of the given type and value with no neighbors.
func newToken(typ token.Type, value string) *token.Token {
	return yamltest.NewTokenBuilder().Type(typ).Value(value).Origin(value).Build()
}

func TestTokenKind(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		tk   *token.Token
		prev token.Tokens // The tokens linked ahead of tk, in order.
		next token.Tokens // The tokens linked after tk, in order.
		want kind.Kind
	}{
		"basic string type":    {tk: newToken(token.StringType, ""), want: kind.LiteralString},
		"bool type":            {tk: newToken(token.BoolType, ""), want: kind.LiteralBoolean},
		"comment type":         {tk: newToken(token.CommentType, ""), want: kind.Comment},
		"mapping value type":   {tk: newToken(token.MappingValueType, ""), want: kind.PunctuationMappingValue},
		"anchor type itself":   {tk: newToken(token.AnchorType, "&"), want: kind.NameAnchor},
		"alias type itself":    {tk: newToken(token.AliasType, "*"), want: kind.NameAlias},
		"integer type":         {tk: newToken(token.IntegerType, "42"), want: kind.LiteralNumberInteger},
		"null type":            {tk: newToken(token.NullType, "null"), want: kind.LiteralNull},
		"float type":           {tk: newToken(token.FloatType, "3.14"), want: kind.LiteralNumberFloat},
		"double quote type":    {tk: newToken(token.DoubleQuoteType, "quoted"), want: kind.LiteralStringDouble},
		"single quote type":    {tk: newToken(token.SingleQuoteType, "quoted"), want: kind.LiteralStringSingle},
		"sequence entry type":  {tk: newToken(token.SequenceEntryType, "-"), want: kind.PunctuationSequenceEntry},
		"sequence start type":  {tk: newToken(token.SequenceStartType, "["), want: kind.PunctuationSequenceStart},
		"sequence end type":    {tk: newToken(token.SequenceEndType, "]"), want: kind.PunctuationSequenceEnd},
		"mapping start type":   {tk: newToken(token.MappingStartType, "{"), want: kind.PunctuationMappingStart},
		"mapping end type":     {tk: newToken(token.MappingEndType, "}"), want: kind.PunctuationMappingEnd},
		"tag type":             {tk: newToken(token.TagType, "!mytag"), want: kind.NameDecorator},
		"directive type":       {tk: newToken(token.DirectiveType, "%YAML"), want: kind.CommentPreproc},
		"document header type": {tk: newToken(token.DocumentHeaderType, "---"), want: kind.PunctuationHeading},
		"document end type":    {tk: newToken(token.DocumentEndType, "..."), want: kind.PunctuationHeading},
		"literal block type":   {tk: newToken(token.LiteralType, "|"), want: kind.PunctuationBlockLiteral},
		"folded block type":    {tk: newToken(token.FoldedType, ">"), want: kind.PunctuationBlockFolded},
		"hex integer type":     {tk: newToken(token.HexIntegerType, "0xFF"), want: kind.LiteralNumberHex},
		"octet integer type":   {tk: newToken(token.OctetIntegerType, "0o777"), want: kind.LiteralNumberOct},
		"binary integer type":  {tk: newToken(token.BinaryIntegerType, "0b1010"), want: kind.LiteralNumberBin},
		"infinity type":        {tk: newToken(token.InfinityType, ".inf"), want: kind.LiteralNumberInfinity},
		"nan type":             {tk: newToken(token.NanType, ".nan"), want: kind.LiteralNumberNaN},
		"merge key type":       {tk: newToken(token.MergeKeyType, "<<"), want: kind.NameAliasMerge},
		"collect entry type":   {tk: newToken(token.CollectEntryType, ","), want: kind.PunctuationCollectEntry},
		"implicit null type":   {tk: newToken(token.ImplicitNullType, ""), want: kind.LiteralNullImplicit},
		"space type":           {tk: newToken(token.SpaceType, " "), want: kind.Text},
		"invalid type":         {tk: newToken(token.InvalidType, "???"), want: kind.GenericErrorInvalid},
		"unknown type":         {tk: newToken(token.UnknownType, "???"), want: kind.GenericErrorUnknown},
		"mapping key type":     {tk: newToken(token.MappingKeyType, "?"), want: kind.NameTag},
		// A type the kind lookup does not hold.
		"unknown type returns text style": {tk: newToken(token.Type(999), ""), want: kind.Text},
		"string followed by colon becomes mapping key": {
			tk:   newToken(token.StringType, "key"),
			next: token.Tokens{newToken(token.MappingValueType, ":")},
			want: kind.NameTag,
		},
		"string not followed by colon stays string": {
			tk:   newToken(token.StringType, "value"),
			next: token.Tokens{newToken(token.StringType, "other")},
			want: kind.LiteralString,
		},
		"token preceded by anchor inherits anchor style": {
			prev: token.Tokens{newToken(token.AnchorType, "&")},
			tk:   newToken(token.StringType, "myanchor"),
			want: kind.NameAnchor,
		},
		"token preceded by alias inherits alias style": {
			prev: token.Tokens{newToken(token.AliasType, "*")},
			tk:   newToken(token.StringType, "myalias"),
			want: kind.NameAlias,
		},
		"merge key followed by colon stays a merge key": {
			tk:   newToken(token.MergeKeyType, "<<"),
			next: token.Tokens{newToken(token.MappingValueType, ":")},
			want: kind.NameAliasMerge,
		},
		"comment followed by colon stays a comment": {
			tk:   newToken(token.CommentType, "# c"),
			next: token.Tokens{newToken(token.MappingValueType, ":")},
			want: kind.Comment,
		},
		"sequence end followed by colon stays punctuation": {
			tk:   newToken(token.SequenceEndType, "]"),
			next: token.Tokens{newToken(token.MappingValueType, ":")},
			want: kind.PunctuationSequenceEnd,
		},
		"integer followed by colon becomes mapping key": {
			tk:   newToken(token.IntegerType, "1"),
			next: token.Tokens{newToken(token.MappingValueType, ":")},
			want: kind.NameTag,
		},
		"string followed by a comment then a colon becomes mapping key": {
			tk: newToken(token.StringType, "key"),
			next: token.Tokens{
				newToken(token.CommentType, "# c"),
				newToken(token.MappingValueType, ":"),
			},
			want: kind.NameTag,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Add links each token to its neighbors, which TokenKind reads
			// through tc.tk.
			var chain token.Tokens

			chain.Add(tc.prev...)
			chain.Add(tc.tk)
			chain.Add(tc.next...)

			assert.Equal(t, tc.want, line.TokenKind(tc.tk))
		})
	}
}

func TestLine_Kind(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		line  int
		want  []kind.Kind
	}{
		"mapping entry": {
			input: "key: value\n",
			want:  []kind.Kind{kind.NameTag, kind.PunctuationMappingValue, kind.LiteralString},
		},
		"anchor and alias take the kind of their marker": {
			input: stringtest.LinesLF("a: &x 1", "b: *x"),
			line:  1,
			want:  []kind.Kind{kind.NameTag, kind.PunctuationMappingValue, kind.NameAlias, kind.NameAlias},
		},
		"key whose colon sits on the next line": {
			input: stringtest.LinesLF("? key", ": value"),
			want:  []kind.Kind{kind.NameTag, kind.NameTag},
		},
		"merge key keeps its kind": {
			input: "<<: *base\n",
			want:  []kind.Kind{kind.NameAliasMerge, kind.PunctuationMappingValue, kind.NameAlias, kind.NameAlias},
		},
		"comment between explicit key and colon": {
			input: stringtest.LinesLF("? a # c", ": b"),
			want:  []kind.Kind{kind.NameTag, kind.NameTag, kind.Comment},
		},
		"flow sequence key keeps its closing bracket": {
			input: "[a]: v\n",
			want: []kind.Kind{
				kind.PunctuationSequenceStart,
				kind.LiteralString,
				kind.PunctuationSequenceEnd,
				kind.PunctuationMappingValue,
				kind.LiteralString,
			},
		},
		"tag before an empty key stays a decorator": {
			input: "!!null : v\n",
			want:  []kind.Kind{kind.NameDecorator, kind.PunctuationMappingValue, kind.LiteralString},
		},
		"sequence entry before an empty key stays punctuation": {
			input: "- : v\n",
			want:  []kind.Kind{kind.PunctuationSequenceEntry, kind.PunctuationMappingValue, kind.LiteralString},
		},
		"block scalar continuation is a string": {
			input: stringtest.LinesLF(
				"text: |",
				"  hello",
			),
			line: 1,
			want: []kind.Kind{kind.LiteralString},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := newLines(tc.input)
			ln := lines.Line(tc.line)

			got := make([]kind.Kind, 0, len(ln.Tokens()))
			for i := range ln.Tokens() {
				got = append(got, ln.Kind(i))
			}

			assert.Equal(t, tc.want, got)
		})
	}
}
