package docparse_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/docparse"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The text of each document node of the file.
		want     []string
		failures int
	}{
		"one document": {
			input: "a: 1\n",
			want:  []string{"a: 1"},
		},
		"a header after a header starts a document": {
			input: stringtest.LinesLF("---", "---", "a: 1"),
			want:  []string{"---", stringtest.JoinLF("---", "a: 1")},
		},
		"a run the parser rejects keeps its place": {
			input:    stringtest.LinesLF("a: [", "---", "b: 1"),
			want:     []string{"", stringtest.JoinLF("---", "b: 1")},
			failures: 1,
		},
		"a run cut by a tab after a tab in a double-quoted scalar keeps the next document": {
			input:    "a: \"x\ty\n\t\n---\nb: 1\n",
			want:     []string{"", stringtest.JoinLF("---", "b: 1")},
			failures: 1,
		},
		"a tab before a '-' on the line after a tag": {
			input: "!t\n\t- a\n",
			want:  []string{stringtest.JoinLF("!t", " - a")},
		},
		"a tab before a '-' on the line after a comment that ends with a CRLF": {
			input: "# c\r\n\t- a\r\n",
			want:  []string{stringtest.JoinLF(" # c", " - a")},
		},
		"a tab before a key on the line after a comment that ends with a CRLF": {
			input: "# c\r\n\ta: 1\r\n",
			want:  []string{stringtest.JoinLF(" # c", " a: 1")},
		},
		"a tab before a key on the line after an invalid block scalar header keeps the next document": {
			input:    "|x\n\ta: |\n---\nc: 1\n",
			want:     []string{"", stringtest.JoinLF("---", "c: 1")},
			failures: 1,
		},
		"a stream of markers holds one document": {
			input: stringtest.LinesLF("...", "..."),
			want:  []string{"..."},
		},
		"an empty file holds one document": {
			input: "",
			want:  []string{""},
		},
		"a comment below a scalar root drops": {
			input: stringtest.LinesLF("a", "# c"),
			want:  []string{"a"},
		},
		"a comment between a directive and its header drops": {
			input: stringtest.LinesLF("%YAML 1.2", "# c", "---", "a: 1"),
			want:  []string{"%YAML 1.2", stringtest.JoinLF("---", "a: 1")},
		},
		"a comment before a ',' of a flow sequence drops": {
			input: stringtest.LinesLF("a: [1", "# c", ", 2]"),
			want:  []string{"a: [1, 2]"},
		},
		"a comment before the '}' of a flow mapping drops": {
			input: stringtest.LinesLF("{a: 1", "# c", "}"),
			want:  []string{"{a: 1}"},
		},
		"a comment where a flow mapping expects a key drops": {
			input: stringtest.LinesLF("a: {b: 1,", "# c", "  d: 2}"),
			want:  []string{"a: {b: 1, d: 2}"},
		},
		"a comment between the entries of a flow sequence stays": {
			input: stringtest.LinesLF("a: [1,", "# c", "  2]"),
			want:  []string{"a: [1, 2 # c]"},
		},
		"a comment after a '?' drops": {
			input: stringtest.LinesLF("? ", "# c", "  a", ": 1"),
			want:  []string{"? a: 1"},
		},
		"a comment before a ':' drops": {
			input: stringtest.LinesLF("? a", "# c", ": 1"),
			want:  []string{"? a: 1"},
		},
		"a comment left of the key of an anchor above its value drops": {
			input: stringtest.LinesLF("m:", "  a: &x", "# c", "    1"),
			want:  []string{stringtest.JoinLF("m:", "  a: &x 1")},
		},
		"a comment left of the key of an anchor above its sequence drops": {
			input: stringtest.LinesLF("m:", "  a: &x", "# c", "  - 1"),
			want:  []string{stringtest.JoinLF("m:", "  a: &x", "  - 1")},
		},
		"a comment at the key of an anchor above its value stays": {
			input: stringtest.LinesLF("a: &x", "# c", "  1"),
			want:  []string{"a: &x 1 # c"},
		},
		"a comment left of the key of an anchor above the next key stays": {
			input: stringtest.LinesLF("m:", "  a: &x", "# c", "  b: 1"),
			want:  []string{stringtest.JoinLF("m:", "  a: &x", "  # c", "  b: 1")},
		},
		"a comment above a key left of the key of an anchor drops": {
			input: stringtest.LinesLF("m:", "  a: &x", "  # c", "b: 1"),
			want:  []string{stringtest.JoinLF("m:", "  a: &x", "b: 1")},
		},
		"a comment between an anchor and the next key drops": {
			input: stringtest.LinesLF("a: &x", "# c", "b: 1"),
			want:  []string{stringtest.JoinLF("a: &x", "b: 1")},
		},
		"a comment on the line of an anchor stays as the one below drops": {
			input: stringtest.LinesLF("a: &x # c", "# d", "b: 1"),
			want:  []string{stringtest.JoinLF("a: &x # c", "b: 1")},
		},
		"a comment between an anchor in an entry and the next key drops": {
			input: stringtest.LinesLF("- a: &x", "  # c", "  b: 1"),
			want:  []string{stringtest.JoinLF("- a: &x", "  b: 1")},
		},
		"a comment between an anchor and the next '?' key drops": {
			input: stringtest.LinesLF("a: &x", "# c", "? b", ": 1"),
			want:  []string{stringtest.JoinLF("a: &x", "? b: 1")},
		},
		"a comment above a flow value at the key of an anchor stays": {
			input: stringtest.LinesLF("a: &x", "# c", "[1]"),
			want:  []string{"a: &x [1] # c"},
		},
		"a comment above a scalar at the key of an anchor stays": {
			input: stringtest.LinesLF("a: &x", "# c", "b"),
			want:  []string{"a: &x b # c"},
		},
		"a comment between an anchor and the next '-' drops": {
			input: stringtest.LinesLF("- &x", "# c", "- 1"),
			want:  []string{stringtest.JoinLF("- &x", "- 1")},
		},
		"a comment above a scalar at the '-' of an anchor stays": {
			input: stringtest.LinesLF("- &x", "# c", "a"),
			want:  []string{"- &x a # c"},
		},
		"a comment below a '&' with no name stays, and the run fails": {
			input:    stringtest.LinesLF("a: &", "# c", "b: 1"),
			want:     []string{""},
			failures: 1,
		},
		"a comment below an anchor at the root stays": {
			input: stringtest.LinesLF("&x", "# c", "a: 1"),
			want:  []string{stringtest.JoinLF("&x", "# c", "a: 1")},
		},
		"a comment below an anchor after a tag stays": {
			input: stringtest.LinesLF("a: !t &x", "# c", "  b"),
			want:  []string{"a: !t &x b # c"},
		},
		"a comment left of the root that closes the document of an anchor drops": {
			input: stringtest.LinesLF("  a: &x", "# c", "---", "b: 1"),
			want:  []string{"  a: &x", stringtest.JoinLF("---", "b: 1")},
		},
		// The parser rejects the anchor without the comment, which it takes
		// as the value of the anchor.
		"a comment left of the root below an anchor on a line of its own stays": {
			input: stringtest.LinesLF("  a:", "    &x", "# c", "---", "b: 1"),
			want:  []string{"  a: &x", stringtest.JoinLF("---", "b: 1")},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := docparse.Parse(tokens.Tokenize(tc.input))

			got := make([]string, len(r.File.Docs))
			for i, doc := range r.File.Docs {
				got[i] = doc.String()
			}

			assert.Equal(t, tc.want, got)
			assert.Len(t, r.Failures, tc.failures)

			for _, f := range r.Failures {
				require.Error(t, f.Err)
				assert.Nil(t, f.Panic)
				assert.NotEmpty(t, f.Run)
			}
		})
	}
}

func TestParse_TagSequence(t *testing.T) {
	t.Parallel()

	const tagMsg = "sequence entries are not allowed after tag on the same line"

	tcs := map[string]struct {
		input string
		// The line and column of the token that the failure binds at, or
		// nil when the text parses.
		at *position.Position
		// The message of the failure, when the text does not parse.
		msg string
	}{
		"after a tag on a value": {
			input: stringtest.LinesLF("a: !t - 1", "b: 1"),
			at:    new(position.New(0, 6)),
			msg:   tagMsg,
		},
		"after a tab after a tag": {
			input: stringtest.LinesLF("a: !t\t- 1", "b: 1"),
			at:    new(position.New(0, 6)),
			msg:   tagMsg,
		},
		"after a tag on an entry": {
			input: "- !t - 1\n",
			at:    new(position.New(0, 5)),
			msg:   tagMsg,
		},
		"after a tag at the root": {
			input: "!t - 1\n",
			at:    new(position.New(0, 3)),
			msg:   tagMsg,
		},
		"after a tag after an anchor": {
			input: "a: &x !t - 1\n",
			at:    new(position.New(0, 9)),
			msg:   tagMsg,
		},
		"after a scalar tag": {
			input: "a: !!str - 1\n",
			at:    new(position.New(0, 9)),
			msg:   tagMsg,
		},
		"after a tag in a flow sequence": {
			input: "[!t - 1]\n",
			at:    new(position.New(0, 4)),
			msg:   tagMsg,
		},
		// The run fails with its first error in file order.
		"after an anchor above a tag": {
			input: stringtest.LinesLF("a: &x - 1", "b: !t - 1"),
			at:    new(position.New(0, 6)),
			msg:   "sequence entries are not allowed after anchor on the same line",
		},
		"in a block mapping above a tag": {
			input: stringtest.LinesLF("a: - 1", "b: !t - 1"),
			at:    new(position.New(0, 3)),
			msg:   "block sequence entries are not allowed in this context",
		},
		"in an open flow sequence above a tag": {
			input: stringtest.LinesLF("a: [1", "b: !t - 1"),
			at:    new(position.New(1, 0)),
			msg:   "',' or ']' must be specified",
		},
		"after a tag above an anchor": {
			input: stringtest.LinesLF("a: !t - 1", "b: &x - 1"),
			at:    new(position.New(0, 6)),
			msg:   tagMsg,
		},
		"after a tag above an open flow sequence": {
			input: stringtest.LinesLF("a: !t - 1", "b: [1"),
			at:    new(position.New(0, 6)),
			msg:   tagMsg,
		},
		"on the line below a tag": {
			input: stringtest.LinesLF("a: !t", "  - 1"),
		},
		"in the column of the key below a tag": {
			input: stringtest.LinesLF("a: !t", "- 1"),
		},
		"below a comment after a tag": {
			input: stringtest.LinesLF("a: !t # c", "  - 1"),
		},
		"a negative number after a tag": {
			input: "a: !t -1\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := docparse.Parse(tokens.Tokenize(tc.input))

			if tc.at == nil {
				require.Empty(t, r.Failures)

				return
			}

			require.Len(t, r.Failures, 1)

			var syntaxErr *yaml.SyntaxError

			require.ErrorAs(t, r.Failures[0].Err, &syntaxErr)
			assert.Equal(t, tc.msg, syntaxErr.Message)
			require.NotNil(t, syntaxErr.Token)
			assert.Equal(t, *tc.at, position.NewFromToken(syntaxErr.Token))
		})
	}
}

func TestParse_FlowSequence(t *testing.T) {
	t.Parallel()

	const (
		flowMsg   = "sequence entries are not allowed in a flow collection"
		anchorMsg = "sequence entries are not allowed after anchor on the same line"
		tagMsg    = "sequence entries are not allowed after tag on the same line"
		indMsg    = "a plain scalar cannot start with a \"-\" before a flow indicator"
		indentMsg = "a line of a flow collection must be indented past the block node that holds it"
		scalarMsg = "a plain scalar in a flow collection cannot hold a flow indicator"
	)

	tcs := map[string]struct {
		input string
		// The line and column of the token that the failure binds at, or
		// nil when the text parses.
		at *position.Position
		// The message of the failure, when the text does not parse.
		msg string
	}{
		"in a flow sequence": {
			input: "[- 1]\n",
			at:    new(position.New(0, 1)),
			msg:   flowMsg,
		},
		"in a flow mapping": {
			input: "{a: - 1}\n",
			at:    new(position.New(0, 4)),
			msg:   flowMsg,
		},
		"after an entry of a flow sequence": {
			input: "[a, - b]\n",
			at:    new(position.New(0, 4)),
			msg:   flowMsg,
		},
		"after a tab": {
			input: "[-\t1]\n",
			at:    new(position.New(0, 1)),
			msg:   flowMsg,
		},
		"on a line below the start of a flow sequence": {
			input: stringtest.LinesLF("[", "- 1]"),
			at:    new(position.New(1, 0)),
			msg:   flowMsg,
		},
		"before a nested flow sequence": {
			input: "[- [1]]\n",
			at:    new(position.New(0, 1)),
			msg:   flowMsg,
		},
		"after a nested flow sequence": {
			input: "{a: [1], b: - 2}\n",
			at:    new(position.New(0, 12)),
			msg:   flowMsg,
		},
		"in a flow sequence in a block sequence": {
			input: "- [- 1]\n",
			at:    new(position.New(0, 3)),
			msg:   flowMsg,
		},
		"in a flow sequence on a value": {
			input: stringtest.LinesLF("a: 1", "b: [- 1]"),
			at:    new(position.New(1, 4)),
			msg:   flowMsg,
		},
		// The parser refuses this "-" too, at the same token.
		"after a key of a flow sequence": {
			input: "[a: - 1]\n",
			at:    new(position.New(0, 4)),
			msg:   flowMsg,
		},
		// A "-" right after an anchor or a tag on its line keeps the
		// message of the anchor or the tag.
		"after an anchor in a flow sequence": {
			input: "[&x - 1]\n",
			at:    new(position.New(0, 4)),
			msg:   anchorMsg,
		},
		"after an anchor in a flow mapping": {
			input: "{a: &x - 1}\n",
			at:    new(position.New(0, 7)),
			msg:   anchorMsg,
		},
		"after an anchor after a tag in a flow sequence": {
			input: "[!t &x - 1]\n",
			at:    new(position.New(0, 7)),
			msg:   anchorMsg,
		},
		"after a tag after an anchor in a flow sequence": {
			input: "[&x !t - 1]\n",
			at:    new(position.New(0, 7)),
			msg:   tagMsg,
		},
		"on the line below an anchor in a flow sequence": {
			input: stringtest.LinesLF("[&x", "- 1]"),
			at:    new(position.New(1, 0)),
			msg:   flowMsg,
		},
		// The run fails with its first error in file order.
		"after an anchor above a flow sequence": {
			input: stringtest.LinesLF("a: &x - 1", "b: [- 1]"),
			at:    new(position.New(0, 6)),
			msg:   anchorMsg,
		},
		"in a flow sequence above an anchor": {
			input: stringtest.LinesLF("a: [- 1]", "b: &x - 1"),
			at:    new(position.New(0, 4)),
			msg:   flowMsg,
		},
		// The parser refuses the block mapping after the flow sequence ends.
		"in a block mapping after a flow sequence": {
			input: stringtest.LinesLF("a: [1, [2]]", "b: - 3"),
			at:    new(position.New(1, 3)),
			msg:   "block sequence entries are not allowed in this context",
		},
		// YAML folds a line that starts with a "-" into the plain scalar on
		// the line above, and so does the tokenizer.
		"on the line below a value of a flow mapping": {
			input: stringtest.LinesLF("{a: b", " - c}"),
		},
		"on the line below a value of a flow sequence": {
			input: stringtest.LinesLF("[a: b", " - c]"),
		},
		"on the line below an explicit key of a flow mapping": {
			input: stringtest.LinesLF("{? a", " - b}"),
		},
		"on the line below an explicit key after an entry of a flow mapping": {
			input: stringtest.LinesLF("{a: b, ? c", " - d}"),
		},
		"on the line below an explicit key of a flow sequence": {
			input: stringtest.LinesLF("[? a", " - b]"),
		},
		// The tokenizer cuts the "-" out of the scalar at a line that does
		// not pass the indent of the block node of the collection, and at a
		// line that holds a key. The run keeps the error of the parser.
		"at the indent of the key of a flow sequence": {
			input: stringtest.LinesLF("key: [a", "- c]"),
			at:    new(position.New(1, 0)),
			msg:   "',' or ']' must be specified",
		},
		"on a key line below a value of a flow mapping": {
			input: stringtest.LinesLF("{a: b", " - c: d}"),
			at:    new(position.New(1, 1)),
			msg:   "',' or '}' must be specified",
		},
		// The parser reads the "-" after an explicit key as the value of
		// the key, so the run fails at the "-".
		"at the indent of the key of a flow mapping after an explicit key": {
			input: stringtest.LinesLF("key: {? a", "- b}"),
			at:    new(position.New(1, 0)),
			msg:   indentMsg,
		},
		"before a flow indicator at the indent of the key of a flow mapping": {
			input: stringtest.LinesLF("key: {? a", "-}"),
			at:    new(position.New(1, 0)),
			msg:   indentMsg,
		},
		"at the indent of the entry of a flow sequence after an explicit key": {
			input: stringtest.LinesLF("- [", "? 1", "-]"),
			at:    new(position.New(2, 0)),
			msg:   indentMsg,
		},
		// A comment ends the plain scalar.
		"on the line below a comment in a flow mapping": {
			input: stringtest.LinesLF("{a: b # c", " - d}"),
			at:    new(position.New(1, 1)),
			msg:   flowMsg,
		},
		"on the line below an alias in a flow sequence": {
			input: stringtest.LinesLF("[*x", " - 1]"),
			at:    new(position.New(1, 1)),
			msg:   flowMsg,
		},
		// The lexer reads a "-" right before a flow indicator as a plain
		// scalar, which YAML refuses.
		"before the end of a flow sequence": {
			input: "[-]\n",
			at:    new(position.New(0, 1)),
			msg:   indMsg,
		},
		"before an entry of a flow sequence": {
			input: "[-, 1]\n",
			at:    new(position.New(0, 1)),
			msg:   indMsg,
		},
		"after an entry of a flow sequence before its end": {
			input: "[a, -]\n",
			at:    new(position.New(0, 4)),
			msg:   indMsg,
		},
		"before the end of a flow mapping": {
			input: "{a: -}\n",
			at:    new(position.New(0, 4)),
			msg:   indMsg,
		},
		"before an entry of a flow mapping": {
			input: "{a: -, b: 1}\n",
			at:    new(position.New(0, 4)),
			msg:   indMsg,
		},
		"as a key before the end of a flow mapping": {
			input: "{-}\n",
			at:    new(position.New(0, 1)),
			msg:   indMsg,
		},
		"before the end of a nested flow sequence": {
			input: "[[-]]\n",
			at:    new(position.New(0, 2)),
			msg:   indMsg,
		},
		"on a line below the start of a flow sequence before its end": {
			input: stringtest.LinesLF("[a,", "-]"),
			at:    new(position.New(1, 0)),
			msg:   indMsg,
		},
		"before a flow indicator after a tag": {
			input: "[!t -]\n",
			at:    new(position.New(0, 4)),
			msg:   indMsg,
		},
		"before a flow indicator after an anchor": {
			input: "[&x -]\n",
			at:    new(position.New(0, 4)),
			msg:   indMsg,
		},
		// The parser refuses the "[" after the "-", but the "-" comes first.
		"before the start of a nested flow sequence": {
			input: "[-[]]\n",
			at:    new(position.New(0, 1)),
			msg:   indMsg,
		},
		"before a flow indicator above a sequence entry": {
			input: stringtest.LinesLF("a: [-]", "b: [- 1]"),
			at:    new(position.New(0, 4)),
			msg:   indMsg,
		},
		// YAML folds the "-" into the plain scalar on the line above, and
		// so does the tokenizer.
		"before a flow indicator on the line below a value of a flow mapping": {
			input: stringtest.LinesLF("{a: b", " -}"),
		},
		"before a flow indicator on the line below an explicit key": {
			input: stringtest.LinesLF("{? a", " -}"),
		},
		// The lexer ends a plain scalar at a "}" only inside a flow mapping,
		// and at a "]" only inside a flow sequence. YAML ends it at each
		// flow indicator inside a flow collection.
		"brace in a plain scalar of a flow sequence": {
			input: "[a}]\n",
			at:    new(position.New(0, 1)),
			msg:   scalarMsg,
		},
		"brace inside a plain scalar of a flow sequence": {
			input: "[a, b}c]\n",
			at:    new(position.New(0, 4)),
			msg:   scalarMsg,
		},
		"dash before a brace in a flow sequence": {
			input: "[-}]\n",
			at:    new(position.New(0, 1)),
			msg:   scalarMsg,
		},
		"brace in a key of a flow sequence": {
			input: "[a}: b]\n",
			at:    new(position.New(0, 1)),
			msg:   scalarMsg,
		},
		"brace in a plain scalar of a nested flow sequence": {
			input: "[[a}]]\n",
			at:    new(position.New(0, 2)),
			msg:   scalarMsg,
		},
		"brace on the second line of a plain scalar of a flow sequence": {
			input: stringtest.LinesLF("[a", "}]"),
			at:    new(position.New(0, 1)),
			msg:   scalarMsg,
		},
		"brace in a plain scalar after a tag": {
			input: "[!t a}]\n",
			at:    new(position.New(0, 4)),
			msg:   scalarMsg,
		},
		"bracket in a value of a flow mapping": {
			input: "{a: b]}\n",
			at:    new(position.New(0, 4)),
			msg:   scalarMsg,
		},
		"bracket in a key of a flow mapping": {
			input: "{a]: 1}\n",
			at:    new(position.New(0, 1)),
			msg:   scalarMsg,
		},
		"brace in a plain scalar above a sequence entry": {
			input: stringtest.LinesLF("a: [b}]", "c: [- 1]"),
			at:    new(position.New(0, 4)),
			msg:   scalarMsg,
		},
		// The lexer reads the second "}" as a plain scalar. The parser
		// refuses that scalar too, but the error of the scalar wins at the
		// same token.
		"brace after the end of a flow mapping": {
			input: "[{a: b}}]\n",
			at:    new(position.New(0, 7)),
			msg:   scalarMsg,
		},
		// The parser refuses the "[" after the first scalar, before the "}".
		"brace in a plain scalar after a refused token": {
			input: "[a[b, c}]]\n",
			at:    new(position.New(0, 2)),
			msg:   "',' or ']' must be specified",
		},
		"brace in a quoted scalar of a flow sequence": {
			input: "[\"a}\", 'b]']\n",
		},
		"brace in a plain scalar of a block mapping": {
			input: stringtest.LinesLF("a: b}", "c]: d"),
		},
		// The content of a block scalar is not a plain scalar.
		"brace in a literal scalar of a flow sequence": {
			input: stringtest.LinesLF("[x, |", "  a}", "]"),
		},
		"bracket in a folded scalar of a flow mapping": {
			input: stringtest.LinesLF("{k: > # c", "  a,b]", "}"),
		},
		"dash in a literal scalar of a flow sequence": {
			input: stringtest.LinesLF("[x, |-", "  -", "]"),
		},
		"a negative number": {
			input: "[-1]\n",
		},
		"a plain scalar of two dashes": {
			input: "[--]\n",
		},
		"a key of a dash in a flow sequence": {
			input: "[-: 1]\n",
		},
		"an alias of the anchor named dash": {
			input: "[*-]\n",
		},
		// The "-" names the anchor, so the run keeps the error of the
		// parser.
		"an anchor named dash before a flow indicator": {
			input: "[&-, 1]\n",
			at:    new(position.New(0, 3)),
			msg:   "unexpected scalar value type",
		},
		"a quoted dash before a flow indicator": {
			input: "[\"-\", '-']\n",
		},
		"a plain scalar with a dash inside": {
			input: "[a -b]\n",
		},
		"a key of a dash": {
			input: "{-: 1}\n",
		},
		"a quoted dash": {
			input: "[\"- 1\"]\n",
		},
		"a block sequence below a flow sequence": {
			input: stringtest.LinesLF("a: [b]", "c:", "  - 1"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := docparse.Parse(tokens.Tokenize(tc.input))

			if tc.at == nil {
				require.Empty(t, r.Failures)

				return
			}

			require.Len(t, r.Failures, 1)

			var syntaxErr *yaml.SyntaxError

			require.ErrorAs(t, r.Failures[0].Err, &syntaxErr)
			assert.Equal(t, tc.msg, syntaxErr.Message)
			require.NotNil(t, syntaxErr.Token)
			assert.Equal(t, *tc.at, position.NewFromToken(syntaxErr.Token))
		})
	}
}

func TestParse_ExplicitKey(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The value of the document, when the text parses.
		want any
		// The message of the failure, when the text does not parse.
		msg string
	}{
		"without a value in a flow sequence": {
			input: "[? a]\n",
			want:  []any{map[string]any{"a": nil}},
		},
		"without a value after its colon in a flow sequence": {
			input: "[? a : , b]\n",
			want:  []any{map[string]any{"a": nil}, "b"},
		},
		"without a value in a flow sequence in a flow mapping": {
			input: "{k: [? a]}\n",
			want:  map[string]any{"k": []any{map[string]any{"a": nil}}},
		},
		// The null after the ":" sits on the line of the "?", so the parser
		// reads it as the value of the key at any indent.
		"without a value after its colon left of the key": {
			input: stringtest.LinesLF("[  ? a", " :", " ]"),
			want:  []any{map[string]any{"a": nil}},
		},
		"without a value at the indent of its key": {
			input: stringtest.LinesLF("[", "? a", "]"),
			want:  []any{map[string]any{"a": nil}},
		},
		"with a value in a flow sequence": {
			input: "[? a: b]\n",
			want:  []any{map[string]any{"a": "b"}},
		},
		"without a value in a flow mapping": {
			input: "{? a}\n",
			want:  map[string]any{"a": nil},
		},
		// The parser reads a tag at the end of a line as the tag of a
		// null, so the parse does not add a null after the key.
		"with a tag above its node without a value in a flow sequence": {
			input: stringtest.LinesLF("[? !t", " a: ]"),
			msg:   "could not find '[' character corresponding to ']'",
		},
		// The parser refuses a key without a node.
		"without a node in a flow sequence": {
			input: "[? ]\n",
			msg:   "unexpected scalar value type",
		},
		"without a node before its colon in a flow sequence": {
			input: "[? :]\n",
			msg:   "unexpected scalar value type",
		},
		// The parser refuses a flow collection as a key.
		"of a flow sequence without a value in a flow sequence": {
			input: "[? [a]]\n",
			msg:   "unexpected scalar value type",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := docparse.Parse(tokens.Tokenize(tc.input))

			if tc.msg != "" {
				require.Len(t, r.Failures, 1)

				var syntaxErr *yaml.SyntaxError

				require.ErrorAs(t, r.Failures[0].Err, &syntaxErr)
				assert.Equal(t, tc.msg, syntaxErr.Message)

				return
			}

			require.Empty(t, r.Failures)
			require.Len(t, r.File.Docs, 1)

			var got any

			require.NoError(t, yaml.NodeToValue(r.File.Docs[0].Body, &got))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNullFlowPairs(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The index of each null in the result, and its position.
		want map[int]position.Position
	}{
		"after the key": {
			input: "[? a]\n",
			want:  map[int]position.Position{3: position.New(0, 4)},
		},
		"after the colon": {
			input: "[? a : ]\n",
			want:  map[int]position.Position{4: position.New(0, 6)},
		},
		"after a key that goes on below the question mark": {
			input: stringtest.LinesLF("[? a", " b]"),
			want:  map[int]position.Position{3: position.New(0, 4)},
		},
		"after a colon right after the key": {
			input: "[? a:]\n",
			want:  map[int]position.Position{4: position.New(0, 5)},
		},
		"after a merge key": {
			input: "[? <<: ]\n",
			want:  map[int]position.Position{4: position.New(0, 6)},
		},
		"after a colon below the question mark": {
			input: stringtest.LinesLF("[  ? a", " :", " ]"),
			want:  map[int]position.Position{4: position.New(0, 6)},
		},
		"after the keys of nested flow sequences": {
			input: "[? a, [? b], ? c: d]\n",
			want: map[int]position.Position{
				3: position.New(0, 4),
				8: position.New(0, 10),
			},
		},
		"after a key inside a key": {
			input: "[? [? a]]\n",
			want: map[int]position.Position{
				5: position.New(0, 7),
				7: position.New(0, 8),
			},
		},
		"after the properties and the key": {
			input: "[? &x !t a # c\n]\n",
			want:  map[int]position.Position{6: position.New(0, 10)},
		},
		"not after a key with a value": {
			input: "[? a: b]\n",
		},
		"not after a key without a node": {
			input: "[? ]\n",
		},
		"not after a key that holds two nodes": {
			input: stringtest.LinesLF("- [", "? 1", "-]"),
		},
		"not after a key with a tag above its node": {
			input: stringtest.LinesLF("[? !t", " a: ]"),
		},
		"not after a key in a flow mapping": {
			input: "{? a}\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			got := docparse.NullFlowPairs(tks)

			require.Len(t, got, len(tks)+len(tc.want))

			nulls := map[int]position.Position{}

			for i, tk := range got {
				if tk.Type != token.ImplicitNullType {
					continue
				}

				assert.Equal(t, "null", tk.Value)

				nulls[i] = position.NewFromToken(tk)
			}

			if len(tc.want) == 0 {
				assert.Empty(t, nulls)

				return
			}

			assert.Equal(t, tc.want, nulls)
		})
	}
}

func TestParse_EmptyAnchor(t *testing.T) {
	t.Parallel()

	// The parser takes the comment below the anchor as its value.
	r := docparse.Parse(tokens.Tokenize(stringtest.LinesLF("a: &x", "# c")))
	require.Empty(t, r.Failures)

	mapping, ok := r.File.Docs[0].Body.(*ast.MappingNode)
	require.True(t, ok, "got %T", r.File.Docs[0].Body)
	require.Len(t, mapping.Values, 1)

	anchor, ok := mapping.Values[0].Value.(*ast.AnchorNode)
	require.True(t, ok, "got %T", mapping.Values[0].Value)
	assert.IsType(t, &ast.NullNode{}, anchor.Value)
}

func TestParse_Index(t *testing.T) {
	t.Parallel()

	tks := tokens.Tokenize(stringtest.LinesLF("a: 1", "# c", "b: [x, y]"))

	r := docparse.Parse(tks)
	yamltest.RequireTokensEqual(t, tks, r.Tokens)
	require.Len(t, r.Index, len(tks))

	for i, tk := range r.Tokens {
		assert.NotSame(t, tks[i], tk)
		assert.Equal(t, i, r.Index[tk])

		_, ok := r.Index[tks[i]]
		assert.False(t, ok, "token %d of the input", i)
	}
}

func TestParse_Panic(t *testing.T) {
	t.Parallel()

	// The go-yaml parser dereferences the position of this token.
	tks := tokens.Tokenize(stringtest.LinesLF("a: 1", "b: 2"))
	tks[2].Position = nil

	r := docparse.Parse(tks)
	require.Len(t, r.Failures, 1)

	f := r.Failures[0]
	require.NoError(t, f.Err)
	assert.NotNil(t, f.Panic)
	assert.Contains(t, string(f.Stack), "go-yaml/parser")
	assert.Len(t, f.Run, len(tks))

	// The stand-in of the run takes the place of its document.
	require.Len(t, r.File.Docs, 1)
	assert.Nil(t, r.File.Docs[0].Body)
}

func TestParse_PanicTagSequence(t *testing.T) {
	t.Parallel()

	// The go-yaml parser dereferences the position of this token, above
	// the "-" after the tag.
	tks := tokens.Tokenize(stringtest.LinesLF("a: 1", "b: !t - 1"))
	tks[2].Position = nil

	r := docparse.Parse(tks)
	require.Len(t, r.Failures, 1)

	// The error of the tag takes the place of the panic.
	f := r.Failures[0]
	assert.Nil(t, f.Panic)

	// The Error method of the error panics on the token without a position,
	// so the test reads the fields of the error alone.
	var syntaxErr *yaml.SyntaxError

	require.ErrorAs(t, f.Err, &syntaxErr)
	assert.Equal(t, docparse.TagSequenceMessage, syntaxErr.Message)
	require.NotNil(t, syntaxErr.Token)
	assert.Equal(t, position.New(1, 6), position.NewFromToken(syntaxErr.Token))
}

func TestParse_PanicAnchorSequence(t *testing.T) {
	t.Parallel()

	// The go-yaml parser dereferences the position of this token, above
	// the "-" after the anchor.
	tks := tokens.Tokenize(stringtest.LinesLF("a: 1", "b: &x - 1"))
	tks[2].Position = nil

	r := docparse.Parse(tks)
	require.Len(t, r.Failures, 1)

	// The error of the anchor takes the place of the panic.
	f := r.Failures[0]
	assert.Nil(t, f.Panic)

	// The Error method of the error panics on the token without a position,
	// so the test reads the fields of the error alone.
	var syntaxErr *yaml.SyntaxError

	require.ErrorAs(t, f.Err, &syntaxErr)
	assert.Equal(t, docparse.AnchorSequenceMessage, syntaxErr.Message)
	require.NotNil(t, syntaxErr.Token)
	assert.Equal(t, position.New(1, 6), position.NewFromToken(syntaxErr.Token))
}

func TestParse_PanicFlowSequence(t *testing.T) {
	t.Parallel()

	// The go-yaml parser dereferences the position of this token, above
	// the "-" in the flow sequence.
	tks := tokens.Tokenize(stringtest.LinesLF("a: 1", "b: [- 1]"))
	tks[2].Position = nil

	r := docparse.Parse(tks)
	require.Len(t, r.Failures, 1)

	// The error of the flow sequence takes the place of the panic.
	f := r.Failures[0]
	assert.Nil(t, f.Panic)

	// The Error method of the error panics on the token without a position,
	// so the test reads the fields of the error alone.
	var syntaxErr *yaml.SyntaxError

	require.ErrorAs(t, f.Err, &syntaxErr)
	assert.Equal(t, docparse.FlowSequenceMessage, syntaxErr.Message)
	require.NotNil(t, syntaxErr.Token)
	assert.Equal(t, position.New(1, 4), position.NewFromToken(syntaxErr.Token))
}

func TestParse_PanicFlowIndent(t *testing.T) {
	t.Parallel()

	// The go-yaml parser dereferences the position of this token, above
	// the "-" at the indent of the key of the flow mapping.
	tks := tokens.Tokenize(stringtest.LinesLF("a: 1", "b: {? a", "- c}"))
	tks[2].Position = nil

	r := docparse.Parse(tks)
	require.Len(t, r.Failures, 1)

	// The error of the "-" takes the place of the panic.
	f := r.Failures[0]
	assert.Nil(t, f.Panic)

	// The Error method of the error panics on the token without a position,
	// so the test reads the fields of the error alone.
	var syntaxErr *yaml.SyntaxError

	require.ErrorAs(t, f.Err, &syntaxErr)
	assert.Equal(t, docparse.FlowIndentMessage, syntaxErr.Message)
	require.NotNil(t, syntaxErr.Token)
	assert.Equal(t, position.New(2, 0), position.NewFromToken(syntaxErr.Token))
}

func TestFail(t *testing.T) {
	t.Parallel()

	errRefused := errors.New("refused")

	tcs := map[string]struct {
		input string
	}{
		"one document":      {input: stringtest.LinesLF("# c", "a: 1")},
		"several documents": {input: stringtest.LinesLF("a: 1", "---", "b: [", "---", "c: 3")},
		"no text":           {input: ""},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			lines := strings.Count(tc.input, "\n") + 1

			r := docparse.Fail(tks, errRefused)

			require.Len(t, r.Failures, 1)
			require.ErrorIs(t, r.Failures[0].Err, errRefused)
			assert.Nil(t, r.Failures[0].Panic)
			assert.Empty(t, r.Tokens)
			assert.Empty(t, r.Index)

			// The one stand-in holds every token and every line of the
			// stream.
			require.Len(t, r.File.Docs, 1)
			assert.Nil(t, r.File.Docs[0].Body)

			docs := r.Documents(tks, lines)
			require.Len(t, docs, 1)

			assert.Same(t, r.File.Docs[0], docs[0].Root)
			assert.Equal(t, 0, docs[0].Failure)
			assert.Equal(t, position.NewSpan(0, lines), docs[0].Span)
			assert.Equal(t, tc.input, yamltest.DumpTokenOrigins(docs[0].Tokens))

			for i, tk := range docs[0].Tokens {
				assert.Same(t, tks[i], tk, "token %d", i)
			}
		})
	}
}

func TestParseLimits(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input  string
		limits docparse.Limits
		// The message of each failure, in file order.
		want []string
		// The line and column of the token each failure binds at.
		at []position.Position
	}{
		"a run past the byte limit": {
			input:  stringtest.LinesLF("a: 1", "---", "# c", "b: "+strings.Repeat("x", 20), "---", "c: 3"),
			limits: docparse.Limits{Bytes: 20, Depth: math.MaxInt},
			want:   []string{"document is larger than 20 bytes"},
			at:     []position.Position{position.New(1, 0)},
		},
		"a run of exactly the byte limit": {
			// The line break before the header ends the last token of the
			// first run, so the second run holds "---\nb: 2\n".
			input:  stringtest.LinesLF("a: 1", "---", "b: 2"),
			limits: docparse.Limits{Bytes: len("---\nb: 2\n"), Depth: math.MaxInt},
		},
		"a run one byte past the byte limit": {
			input:  stringtest.LinesLF("a: 1", "---", "b: 2"),
			limits: docparse.Limits{Bytes: len("---\nb: 2\n") - 1, Depth: math.MaxInt},
			want:   []string{"document is larger than 8 bytes"},
			at:     []position.Position{position.New(1, 0)},
		},
		"the byte limit goes before the depth limit": {
			input:  "a: [[[" + strings.Repeat("x", 20) + "]]]\n",
			limits: docparse.Limits{Bytes: 20, Depth: 2},
			want:   []string{"document is larger than 20 bytes"},
			at:     []position.Position{position.New(0, 0)},
		},
		"a run past the depth limit": {
			input:  "a: [[[1]]]\n",
			limits: docparse.Limits{Bytes: math.MaxInt, Depth: 2},
			want:   []string{"document nests more than 2 levels"},
			at:     []position.Position{position.New(0, 4)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := docparse.ParseLimits(tokens.Tokenize(tc.input), tc.limits)

			require.Len(t, r.Failures, len(tc.want))

			for i, f := range r.Failures {
				assert.Equal(t, tc.want[i], f.Err.Error())

				var tk *token.Token

				{
					var (
						err  *docparse.SizeError
						err1 *docparse.DepthError
					)

					switch {
					case errors.As(f.Err, &err):
						tk = err.Token
					case errors.As(f.Err, &err1):
						tk = err1.Token
					default:
						require.Failf(t, "unexpected failure", "%T", f.Err)
					}
				}

				require.NotNil(t, tk)
				assert.Equal(t, tc.at[i], position.NewFromToken(tk))
			}
		})
	}
}

func TestParseWithoutComments(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The keys of the root mapping of the first document.
		want []string
	}{
		"a text without comments parses as Parse parses it": {
			input: stringtest.LinesLF("a: 1", "b: [x, y]"),
			want:  []string{"a", "b"},
		},
		"a comment line after a converting tag before an anchor": {
			// Go-yaml's parser drops the entry after the collection without the
			// comment line, as yaml.Unmarshal reads the text.
			input: stringtest.LinesLF("p: !!str &k [1]", "# l", "q: 1"),
			want:  []string{"p"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := docparse.ParseWithoutComments(tokens.Tokenize(tc.input))
			require.Empty(t, r.Failures)

			mapping, ok := r.File.Docs[0].Body.(*ast.MappingNode)
			require.True(t, ok, "got %T", r.File.Docs[0].Body)

			got := make([]string, len(mapping.Values))
			for i, e := range mapping.Values {
				got[i] = e.Key.String()
			}

			assert.Equal(t, tc.want, got)
		})
	}
}
