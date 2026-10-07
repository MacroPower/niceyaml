package niceyaml_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// pathsAt lists what [niceyaml.Node.PathAt] reports for every position of
// source, one row per run of columns with one answer: the 0-indexed line
// and the first column of the run, then the path, or "-" where no
// document reports one. The columns of a line run one past its last
// character. A source of several documents puts the index of the document
// that reports the path in front of it.
//
// It fails the test when two documents report a path for one position,
// and when a path it lists does not resolve in its document.
func pathsAt(t *testing.T, source *niceyaml.Source) string {
	t.Helper()

	// A document that did not parse still has a Node, so the error is not
	// one of the test.
	docs := source.AllDocuments()

	var sb strings.Builder

	for i, l := range source.Lines().All() {
		prev := ""

		for col := range utf8.RuneCountInString(l.Content()) + 1 {
			pos := position.New(i, col)
			got := "-"

			for _, doc := range docs {
				path, ok := doc.PathAt(pos)
				if !ok {
					continue
				}

				require.Equal(t, "-", got, "two documents report a path at %s", pos)

				_, err := doc.At(path)
				require.NoError(t, err, "path %s at %s", path, pos)

				got = path.String()
				if len(docs) > 1 {
					got = fmt.Sprintf("#%d %s", doc.DocumentIndex(), path)
				}
			}

			if got != prev {
				fmt.Fprintf(&sb, "%s %s\n", pos, got)

				prev = got
			}
		}
	}

	return strings.TrimSuffix(sb.String(), "\n")
}

func TestNode_PathAt(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		opts  []niceyaml.SourceOption
	}{
		"block mapping and sequence": {
			input: "# head\nservers:\n  - host: h  # note\n    port: 1\n\n  - host: g\nname: x",
			want: stringtest.JoinLF(
				"0:0 -",
				"1:0 $.servers~",
				"1:7 $.servers",
				"1:8 -",
				"2:0 $.servers[0]",
				"2:3 $.servers[0].host~",
				"2:8 $.servers[0].host",
				"2:13 -",
				"3:0 $.servers[0].port~",
				"3:8 $.servers[0].port",
				"3:11 -",
				"4:0 -",
				"5:0 $.servers[1]",
				"5:3 $.servers[1].host~",
				"5:8 $.servers[1].host",
				"5:11 -",
				"6:0 $.name~",
				"6:4 $.name",
				"6:7 -",
			),
		},
		"flow collections": {
			input: "x: [a: 1, {b: 2}, [c],]\ny: {p: 1, q: 2,}",
			want: stringtest.JoinLF(
				"0:0 $.x~",
				"0:1 $.x",
				"0:4 $.x[0].a~",
				"0:5 $.x[0].a",
				"0:8 $.x",
				"0:9 $.x[1]",
				"0:11 $.x[1].b~",
				"0:12 $.x[1].b",
				"0:15 $.x[1]",
				"0:16 $.x",
				"0:17 $.x[2]",
				"0:19 $.x[2][0]",
				"0:20 $.x[2]",
				"0:21 $.x",
				"0:23 -",
				"1:0 $.y~",
				"1:1 $.y",
				"1:4 $.y.p~",
				"1:5 $.y.p",
				"1:8 $.y",
				"1:9 $.y.q~",
				"1:11 $.y.q",
				"1:14 $.y",
				"1:16 -",
			),
		},
		"explicit key, anchor, tag, and alias": {
			input: "? k\n: &anc !!str w\nz: *anc",
			want: stringtest.JoinLF(
				"0:0 $.k~",
				"0:3 -",
				"1:0 $.k",
				"1:14 -",
				"2:0 $.z~",
				"2:1 $.z",
				"2:7 -",
			),
		},
		"scalars over several lines": {
			input: "a: |\nb:\nc: |\n  text\n  more\nd: \"q\n  r\"",
			want: stringtest.JoinLF(
				"0:0 $.a~",
				"0:1 $.a",
				"0:4 -",
				"1:0 $.b~",
				"1:1 $.b",
				"1:2 -",
				"2:0 $.c~",
				"2:1 $.c",
				"2:4 -",
				"3:0 $.c",
				"3:6 -",
				"4:0 $.c",
				"4:6 -",
				"5:0 $.d~",
				"5:1 $.d",
				"5:5 -",
				"6:0 $.d",
				"6:4 -",
			),
		},
		"scalar at the root": {
			input: "abc",
			want: stringtest.JoinLF(
				"0:0 $",
				"0:3 -",
			),
		},
		"sequence at the root": {
			input: "- a\n-\n- b: 1\n  c: 2\n- - x\n  - y",
			want: stringtest.JoinLF(
				"0:0 $[0]",
				"0:3 -",
				"1:0 $[1]",
				"1:1 -",
				"2:0 $[2]",
				"2:1 $[2].b~",
				"2:3 $[2].b",
				"2:6 -",
				"3:0 $[2].c~",
				"3:3 $[2].c",
				"3:6 -",
				"4:0 $[3]",
				"4:1 $[3][0]",
				"4:5 -",
				"5:0 $[3][1]",
				"5:5 -",
			),
		},
		"tag at the root between markers": {
			input: "--- !!map\na: 1\n...",
			want: stringtest.JoinLF(
				"0:0 -",
				"0:3 $",
				"0:9 -",
				"1:0 $.a~",
				"1:1 $.a",
				"1:4 -",
				"2:0 -",
			),
		},
		"directive and comments": {
			input: "%YAML 1.2\n---\na: 1 # c\n# foot",
			want: stringtest.JoinLF(
				"0:0 -",
				"1:0 -",
				"2:0 $.a~",
				"2:1 $.a",
				"2:5 -",
				"3:0 -",
			),
		},
		"merge key and the content of its anchor": {
			input: "defs: &d\n  port: 1\nservers:\n  - <<: *d\n    host: &h x\n    other: *h",
			want: stringtest.JoinLF(
				"0:0 $.defs~",
				"0:4 $.defs",
				"0:8 -",
				"1:0 $.defs.port~",
				"1:6 $.defs.port",
				"1:9 -",
				"2:0 $.servers~",
				"2:7 $.servers",
				"2:8 -",
				"3:0 $.servers[0]",
				"3:3 $.servers[0].<<~",
				"3:6 $.servers[0].<<",
				"3:10 -",
				"4:0 $.servers[0].host~",
				"4:8 $.servers[0].host",
				"4:14 -",
				"5:0 $.servers[0].other~",
				"5:9 $.servers[0].other",
				"5:13 -",
			),
		},
		"entry a later merge key overrides": {
			input: "base: &b\n  k: 1\nm:\n  k: 2\n  <<: *b\n  j: 3",
			want: stringtest.JoinLF(
				"0:0 $.base~",
				"0:4 $.base",
				"0:8 -",
				"1:0 $.base.k~",
				"1:3 $.base.k",
				"1:6 -",
				"2:0 $.m~",
				"2:1 $.m",
				"2:2 -",
				"3:0 -",
				"4:0 $.m.<<~",
				"4:4 $.m.<<",
				"4:8 -",
				"5:0 $.m.j~",
				"5:3 $.m.j",
				"5:6 -",
			),
		},
		"entry after the merge key that holds its name": {
			input: "base: &b\n  k: 1\nm:\n  <<: *b\n  k: 2",
			want: stringtest.JoinLF(
				"0:0 $.base~",
				"0:4 $.base",
				"0:8 -",
				"1:0 $.base.k~",
				"1:3 $.base.k",
				"1:6 -",
				"2:0 $.m~",
				"2:1 $.m",
				"2:2 -",
				"3:0 $.m.<<~",
				"3:4 $.m.<<",
				"3:8 -",
				"4:0 $.m.k~",
				"4:3 $.m.k",
				"4:6 -",
			),
		},
		"earlier of two entries with one key": {
			input: "a:\n  x: 1\na:\n  x: 2\nb: 3",
			opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			want: stringtest.JoinLF(
				"0:0 -",
				"1:0 -",
				"2:0 $.a~",
				"2:1 $.a",
				"2:2 -",
				"3:0 $.a.x~",
				"3:3 $.a.x",
				"3:6 -",
				"4:0 $.b~",
				"4:1 $.b",
				"4:4 -",
			),
		},
		"earlier of two merge keys": {
			input: "k: v\n<<: {a: 1}\n<<: {b: 2}",
			opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			want: stringtest.JoinLF(
				"0:0 $.k~",
				"0:1 $.k",
				"0:4 -",
				"1:0 -",
				"2:0 $.<<~",
				"2:2 $.<<",
				"2:5 $.<<.b~",
				"2:6 $.<<.b",
				"2:9 $.<<",
				"2:10 -",
			),
		},
		"alias key": {
			input: "a: &k name\n*k : w",
			want: stringtest.JoinLF(
				"0:0 $.a~",
				"0:1 $.a",
				"0:10 -",
				"1:0 $.name~",
				"1:2 $.name",
				"1:6 -",
			),
		},
		"key with no name": {
			input: "b: 2\n*nope : 1",
			want: stringtest.JoinLF(
				"0:0 $.b~",
				"0:1 $.b",
				"0:4 -",
				"1:0 -",
			),
		},
		"keys a path quotes": {
			input: "0x10: a\n'a.b': {\"c d\": 1}\n\"\": e",
			want: stringtest.JoinLF(
				"0:0 $.0x10~",
				"0:4 $.0x10",
				"0:7 -",
				"1:0 $.'a.b'~",
				"1:5 $.'a.b'",
				"1:8 $.'a.b'.'c d'~",
				"1:13 $.'a.b'.'c d'",
				"1:16 $.'a.b'",
				"1:17 -",
				"2:0 $.''~",
				"2:2 $.''",
				"2:5 -",
			),
		},
		"several documents": {
			input: "a: 1\n---\nb: 2\n---\n- c",
			want: stringtest.JoinLF(
				"0:0 #0 $.a~",
				"0:1 #0 $.a",
				"0:4 -",
				"1:0 -",
				"2:0 #1 $.b~",
				"2:1 #1 $.b",
				"2:4 -",
				"3:0 -",
				"4:0 #2 $[0]",
				"4:3 -",
			),
		},
		"document that does not parse": {
			input: "a: [1, 2\n---\nb: 2",
			want: stringtest.JoinLF(
				"0:0 -",
				"1:0 -",
				"2:0 #1 $.b~",
				"2:1 #1 $.b",
				"2:4 -",
			),
		},
		"comment alone": {
			input: "# only",
			want: stringtest.JoinLF(
				"0:0 -",
			),
		},
		"empty source": {
			input: "",
		},
		"whitespace alone": {
			input: "   ",
			want: stringtest.JoinLF(
				"0:0 -",
			),
		},
		"header alone": {
			input: "---",
			want: stringtest.JoinLF(
				"0:0 -",
			),
		},
		"characters wider than a byte": {
			input: "名前: 値\né: [ü]",
			want: stringtest.JoinLF(
				"0:0 $.名前~",
				"0:2 $.名前",
				"0:5 -",
				"1:0 $.é~",
				"1:1 $.é",
				"1:4 $.é[0]",
				"1:5 $.é",
				"1:6 -",
			),
		},
		"CRLF line endings": {
			input: "a: 1\r\nb:\r\n  - c\r\n",
			want: stringtest.JoinLF(
				"0:0 $.a~",
				"0:1 $.a",
				"0:4 -",
				"1:0 $.b~",
				"1:1 $.b",
				"1:2 -",
				"2:0 $.b[0]",
				"2:5 -",
			),
		},
		"tab after a value": {
			input: "a: 1\t# c\nb: 2",
			want: stringtest.JoinLF(
				"0:0 $.a~",
				"0:1 $.a",
				"0:5 -",
				"1:0 $.b~",
				"1:1 $.b",
				"1:4 -",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input, tc.opts...)

			assert.Equal(t, tc.want, pathsAt(t, source))
		})
	}
}

func TestNode_PathAt_File(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("testdata", "full.yaml"))
	require.NoError(t, err)

	source := niceyaml.NewSourceFromString(string(data))

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	// A directive holds plain tokens after its "%", which belong to no
	// node either.
	directives := map[int]bool{}

	for _, tk := range source.Tokens() {
		if tk.Type == token.DirectiveType {
			directives[tk.Position.Line] = true
		}
	}

	found := 0

	for _, tk := range source.Tokens() {
		pos := position.NewFromToken(tk)

		var got []string

		for _, doc := range docs {
			path, ok := doc.PathAt(pos)
			if !ok {
				continue
			}

			_, err := doc.At(path)
			require.NoError(t, err, "path %s at %s", path, pos)

			got = append(got, path.String())
		}

		switch tk.Type {
		case token.CommentType, token.DirectiveType, token.DocumentHeaderType, token.DocumentEndType:
			assert.Empty(t, got, "%s token at %s", tk.Type, pos)

		default:
			if directives[tk.Position.Line] {
				assert.Empty(t, got, "directive token at %s", pos)

				continue
			}

			// One document holds every other token, as a node or
			// between the nodes of a collection.
			assert.Len(t, got, 1, "%s token %q at %s", tk.Type, tk.Value, pos)

			found++
		}
	}

	assert.Greater(t, found, 1000)
}

func TestNode_PathAt_OutsideLines(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "a: 1\nb: 2\n")

	tcs := map[string]struct {
		line int
		col  int
	}{
		"negative line":        {line: -1, col: 0},
		"negative column":      {line: 0, col: -1},
		"line past the last":   {line: 2, col: 0},
		"line far past":        {line: 100, col: 0},
		"column past the line": {line: 0, col: 100},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := doc.PathAt(position.New(tc.line, tc.col))
			assert.False(t, ok)
			assert.Equal(t, paths.Current(), got)
		})
	}
}

func TestNode_PathAt_SelectsNode(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		defs: &d
		  port: 8080
		servers:
		  - <<: *d
		    host: "a.example"
		  - name: [x, yy]
	`))

	tcs := map[string]struct {
		line int
		col  int
		want string
	}{
		"key":                         {line: 1, col: 3, want: "$.defs.port~"},
		"scalar value":                {line: 1, col: 10, want: "$.defs.port"},
		"key under an element":        {line: 4, col: 5, want: "$.servers[0].host~"},
		"quoted value":                {line: 4, col: 12, want: "$.servers[0].host"},
		"element of a flow sequence":  {line: 5, col: 15, want: "$.servers[1].name[1]"},
		"key of a merge key":          {line: 3, col: 4, want: "$.servers[0].<<~"},
		"alias under a merge key":     {line: 3, col: 8, want: "$.servers[0].<<"},
		"first key of a root mapping": {line: 0, col: 0, want: "$.defs~"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pos := position.New(tc.line, tc.col)

			got, ok := doc.PathAt(pos)
			require.True(t, ok)
			assert.Equal(t, tc.want, got.String())
			assert.True(t, paths.MustParse(tc.want).Equal(got))

			// The path resolves to the token at the position.
			ranges, err := doc.Ranges(got)
			require.NoError(t, err)
			require.Len(t, ranges, 1)
			assert.True(t, ranges[0].Contains(pos), "ranges %s", ranges)
		})
	}
}

func TestNode_PathAt_AnchorContent(t *testing.T) {
	t.Parallel()

	// The content of an anchor has one place in the document, where the
	// anchor defines it, whatever alias or merge key a path reaches it
	// through.
	doc := yamltest.FirstDocument(t, stringtest.Input(`
		defs: &d
		  port: 8080
		servers:
		  - <<: *d
		copy: *d
	`))

	for _, through := range []string{"$.defs.port", "$.servers[0].port", "$.copy.port"} {
		ranges, err := doc.Ranges(paths.MustParse(through))
		require.NoError(t, err)
		require.Len(t, ranges, 1)
		assert.Equal(t, position.New(1, 8), ranges[0].Start, through)

		got, ok := doc.PathAt(ranges[0].Start)
		require.True(t, ok, through)
		assert.Equal(t, "$.defs.port", got.String(), through)
	}
}

func TestNode_PathAt_ScopedNode(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		spec:
		  hours:
		    open: 9
		name: x
	`))
	hours := yamltest.At(t, doc, paths.Doc().Child("spec", "hours"))

	tcs := map[string]struct {
		line int
		col  int
		want string
	}{
		"inside the scope":  {line: 2, col: 10, want: "$.spec.hours.open"},
		"key of the scope":  {line: 1, col: 2, want: "$.spec.hours~"},
		"outside the scope": {line: 3, col: 6, want: "$.name"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pos := position.New(tc.line, tc.col)

			// A scoped Node answers for its document, from the root.
			got, ok := hours.PathAt(pos)
			require.True(t, ok)
			assert.Equal(t, tc.want, got.String())

			want, ok := doc.PathAt(pos)
			require.True(t, ok)
			assert.Equal(t, want, got)

			fromRoot, ok := hours.Document().PathAt(pos)
			require.True(t, ok)
			assert.Equal(t, want, fromRoot)

			// The path starts at `$`, so it goes back through the scoped
			// Node and selects the node at pos.
			back, err := hours.At(got)
			require.NoError(t, err)
			assert.Equal(t, got, back.Path())

			ranges, err := hours.Ranges(got)
			require.NoError(t, err)
			require.NotEmpty(t, ranges)
			assert.Equal(t, tc.line, ranges[0].Start.Line)
		})
	}

	t.Run("the path of the scoped Node selects the Node", func(t *testing.T) {
		t.Parallel()

		self, err := hours.At(hours.Path())
		require.NoError(t, err)
		assert.Equal(t, hours.Path(), self.Path())
		assert.Equal(t, hours.Span(), self.Span())
		assert.Same(t, hours.AST(), self.AST())
	})
}

func TestNode_PathAt_NilNode(t *testing.T) {
	t.Parallel()

	var n *niceyaml.Node

	got, ok := n.PathAt(position.New(0, 0))
	assert.False(t, ok)
	assert.Equal(t, paths.Current(), got)
}

func TestNode_PathAt_Concurrent(t *testing.T) {
	t.Parallel()

	// Every Node of a document shares what the first call finds, so
	// several goroutines ask Nodes of one document at once.
	doc := yamltest.FirstDocument(t, stringtest.Input(`
		base: &b
		  name: x
		items: [*b, *b]
		spec:
		  replicas: 1
	`))
	spec := yamltest.At(t, doc, paths.Current().Child("spec"))

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			got, ok := doc.PathAt(position.New(1, 8))
			if assert.True(t, ok) {
				assert.Equal(t, "$.base.name", got.String())
			}

			got, ok = spec.PathAt(position.New(4, 2))
			if assert.True(t, ok) {
				assert.Equal(t, "$.spec.replicas~", got.String())
			}

			_, ok = doc.PathAt(position.New(9, 0))
			assert.False(t, ok)
		})
	}

	wg.Wait()
}
