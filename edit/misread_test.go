package edit_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/paths"
)

// misreadSource returns the first document of text, named test.yaml, with
// a reference document of each of refs, named ref0.yaml for the first.
func misreadSource(t *testing.T, text string, refs []string, more ...niceyaml.SourceOption) *niceyaml.Node {
	t.Helper()

	opts := append([]niceyaml.SourceOption{niceyaml.WithName("test.yaml")}, more...)
	for i, ref := range refs {
		name := niceyaml.WithName(fmt.Sprintf("ref%d.yaml", i))
		opts = append(opts, niceyaml.WithReferences(niceyaml.NewSourceFromString(ref, name)))
	}

	docs := niceyaml.NewSourceFromString(text, opts...).Documents()
	require.NotEmpty(t, docs)

	return docs[0]
}

// wholeValue returns the value at p of v, the decode of a whole document
// into any, and whether v holds p.
func wholeValue(v any, p paths.Path) (any, bool) {
	for sel := range p.Selectors() {
		switch sel.Kind {
		case paths.SelectorChild:
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}

			v, ok = m[sel.Name]
			if !ok {
				return nil, false
			}

		case paths.SelectorIndex:
			s, ok := v.([]any)
			if !ok || sel.Index < 0 || sel.Index >= len(s) {
				return nil, false
			}

			v = s[sel.Index]

		default:
			return nil, false
		}
	}

	return v, true
}

// wholePaths returns the path of each key and each index of v, the decode
// of a whole document into any, below p.
func wholePaths(p paths.Path, v any) []paths.Path {
	var out []paths.Path

	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			q := p.Child(k)
			out = append(out, q)
			out = append(out, wholePaths(q, e)...)
		}

	case []any:
		for i, e := range x {
			q := p.Index(i)
			out = append(out, q)
			out = append(out, wholePaths(q, e)...)
		}
	}

	return out
}

func TestNew_GoYAMLReading(t *testing.T) {
	t.Parallel()

	// Each text is valid YAML that go-yaml reads otherwise than YAML does.
	// New takes it, and each read at a path returns what niceyaml's decode
	// of the whole document holds at that path.
	tcs := map[string]struct {
		text string
		// The texts of the reference documents of the Source.
		refs []string
	}{
		"tab after a tag at the end of a mapping": {
			text: stringtest.LinesLF("z: 1", "a: !!int\t5"),
		},
		"tab after a tag before a line at its column": {
			// Go-yaml's lexer reads the tab and 5 into the tag, and nests z
			// in the value of a.
			text: stringtest.LinesLF("a: !!int\t5", "z: 1"),
		},
		"anchor after a tab after a tag": {
			// Go-yaml's lexer reads the tag "!t\t&x", so it does not find
			// the anchor x.
			text: stringtest.LinesLF("a: !t\t&x 1", "b: *x"),
		},
		"value after a tab after an anchor": {
			// Go-yaml's lexer reads the anchor "x1" on an empty value, so it
			// does not find the anchor x.
			text: stringtest.LinesLF("a: &x\t1", "b: *x"),
		},
		"dash that continues a plain value of a flow mapping": {
			// Go-yaml's lexer cuts the line out of the value, and its parser
			// refuses the "-".
			text: stringtest.LinesLF("a: {b: c", "  - d}", "e: 1"),
		},
		"dash that continues a plain value right of its key": {
			// Go-yaml's lexer reads the "-" and the rest of the text into the
			// value, so it does not find the end of the flow mapping.
			text: stringtest.LinesLF("a: {b: c", "     - d}", "e: 1"),
		},
		"block scalar header after a tab after a tag": {
			// Go-yaml's lexer reads the tag "!!str\t|" on the plain scalar
			// x.
			text: stringtest.LinesLF("a: !!str\t|", "  x", "b: 1"),
		},
		"tab inside a plain value": {
			// Go-yaml's lexer drops the tab and reads the integer 12.
			text: stringtest.LinesLF("a: 1\t2", "b: 1"),
		},
		"tag before an anchor on a number": {
			text: stringtest.LinesLF("a: !t &k 1", "b: *k"),
		},
		"tag before a comment and its scalar on the next line": {
			text: stringtest.LinesLF("a: !t # c", "  1"),
		},
		"verbatim tag of the core schema": {
			text: "a: !<tag:yaml.org,2002:int> 5\n",
		},
		"tag that a %TAG directive gives the prefix of the core schema": {
			text: stringtest.LinesLF("%TAG !e! tag:yaml.org,2002:", "---", "a: !e!int 5"),
		},
		"tag with an escape in its shorthand": {
			text: "a: !!b%6Fol \"true\"\n",
		},
		"core tag before an anchor that an alias reads": {
			text: stringtest.LinesLF("a: !!int &k 5", "b: *k"),
		},
		"core tag on a number with a leading zero": {
			text: "a: !!int 017\n",
		},
		"block scalar with strip chomping whose last line ends with spaces": {
			text: stringtest.LinesLF("m:", "  a: |-", "    x  ", "z: 1"),
		},
		"block scalar whose first line starts with a tab after its indentation": {
			text: stringtest.LinesLF("b: |", "  \tx", "   y", "z: 0"),
		},
		"folded block scalar at the end of a text without a line break": {
			text: "a: >\n  x\n  y",
		},
		"block scalar with keep chomping and no line at the end of the text": {
			text: "a: |+\n",
		},
		"double-quoted scalar over lines with \\r\\n line breaks": {
			text: "a: \"x\r\n  y\"\r\nz: 0\r\n",
		},
		"single-quoted scalar with a tab at the end of a line": {
			text: "a: 'x\t\n  y'\nz: 0\n",
		},
		"merge key with a tab after its colon": {
			// Go-yaml's decoder and the readers of niceyaml read the string
			// key "<<".
			text: stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<:\t*s", "  b: 2"),
		},
		"merge key with a tab before its colon": {
			text: stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<\t: *s", "  b: 2"),
		},
		"explicit merge key": {
			// Control: go-yaml's decoder and the readers of niceyaml read the
			// string key "<<".
			text: stringtest.LinesLF("s: &s {a: 2}", "m:", "  ? <<", "  : *s"),
		},
		"tag at the end of a text without a final line break": {
			text: "a: 1\nb: !t",
		},
		"core tag before an anchor with its scalar on the next line": {
			// The value comes last, because go-yaml's parser leaves out the
			// line after it.
			text: stringtest.LinesLF("b: 2", "a: !!int &k", "  1"),
		},
		"alias of a reference document that go-yaml misreads": {
			text: "m: *r\n",
			refs: []string{"r: !!int &r \"5\"\n"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := misreadSource(t, tc.text, tc.refs)

			whole, err := doc.Decode[any](t.Context())
			require.NoError(t, err)

			d, err := edit.New(doc)
			require.NoError(t, err)
			assert.Equal(t, tc.text, d.Text())

			ps := append([]paths.Path{paths.Doc()}, wholePaths(paths.Doc(), whole)...)

			err = d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
				for _, p := range ps {
					want, ok := wholeValue(whole, p)
					require.True(t, ok)

					n := root
					if !p.IsRoot() {
						n = root.At(p)
					}

					got, err := n.Decode[any](ctx)
					require.NoError(t, err, p.String())
					assert.Equal(t, want, got, p.String())
					assert.True(t, exists(t, n), p.String())
				}

				return nil
			})
			require.NoError(t, err)
		})
	}
}

func TestDoc_Apply_Misread(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		fn     edit.Func
		before string
		// A line of the error of the Apply, which matches edit.ErrEngine.
		err string
		// The detail of that error, which tells what to write, or "".
		fix string
		// The detail of that error that names the call, which comes last.
		call string
	}{
		"removal that leaves a byte order mark at the start of the text": {
			// The tokenizer of the Source drops the mark that the removal leaves
			// at the start of the text, so the key would read as b.
			before: stringtest.LinesLF("a: 1", "\ufeffb: 2"),
			fn: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.a")).Remove()
			},
			err: "test.yaml:2:1: $: go-yaml reads the byte order mark at 2:1 as text of the document, but the " +
				"tokenizer of the Source drops it in the edited text, and the edit removes $.a",
			fix:  "remove the byte order mark",
			call: "the edit removes $.a",
		},
		"removal that leaves a byte order mark at the start of a later document": {
			before: stringtest.LinesLF("---", "a: 1", "\ufeffb: 2"),
			fn: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.a")).Remove()
			},
			err: "test.yaml:3:1: $: go-yaml reads the byte order mark at 3:1 as text of the document, but the " +
				"tokenizer of the Source drops it in the edited text, and the edit removes $.a",
			fix:  "remove the byte order mark",
			call: "the edit removes $.a",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := d.Apply(t.Context(), paths.Doc(), tc.fn)

			// The engine wrote the text that go-yaml misreads, and the call
			// leaves the text as it found it.
			require.ErrorIs(t, err, edit.ErrEngine)
			assert.Contains(t, niceyaml.FormatError(err), tc.err)
			assert.Equal(t, tc.before, d.Text())

			if tc.fix != "" {
				assert.Contains(t, niceyaml.FormatError(err), "\n|-- "+tc.fix+"\n")
			}

			assert.Contains(t, niceyaml.FormatError(err), "\n`-- "+tc.call+"\n")
		})
	}
}
