package finder_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/transform"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/normalizer"
	"go.jacobcolvin.com/niceyaml/position"
)

func TestFinder_Find(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input      string
		search     string
		normalizer finder.Normalizer
		want       position.Ranges
	}{
		"single token match": {
			input:  "key: value",
			search: "value",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 5),
					position.New(0, 10),
				),
			},
		},
		"match key": {
			input:  "key: value",
			search: "key",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 0),
					position.New(0, 3),
				),
			},
		},
		"cross-token match": {
			input:  "key: value",
			search: ": ",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 3),
					position.New(0, 5),
				),
			},
		},
		"multiple matches": {
			input:  "a: test\nb: test\nc: test",
			search: "test",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 3),
					position.New(0, 7),
				),
				position.NewRange(
					position.New(1, 3),
					position.New(1, 7),
				),
				position.NewRange(
					position.New(2, 3),
					position.New(2, 7),
				),
			},
		},
		"no match": {
			input:  "key: value",
			search: "notfound",
			want:   nil,
		},
		"empty search": {
			input:  "key: value",
			search: "",
			want:   nil,
		},
		"multi-line value": {
			input:  "text: |\n  line1\n  line2",
			search: "line2",
			want: position.Ranges{
				position.NewRange(
					position.New(2, 2),
					position.New(2, 7),
				),
			},
		},
		"match spans lines": {
			input:  "a: 1\nb: 2",
			search: "1\nb",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 3),
					position.New(1, 1),
				),
			},
		},
		"with normalizer - diacritic match": {
			input:      "name: Thaïs",
			search:     "Thais",
			normalizer: normalizer.New(),
			want: position.Ranges{
				position.NewRange(
					position.New(0, 6),
					position.New(0, 11),
				),
			},
		},
		"with normalizer - search has diacritic": {
			input:      "name: Thais",
			search:     "Thaïs",
			normalizer: normalizer.New(),
			want: position.Ranges{
				position.NewRange(
					position.New(0, 6),
					position.New(0, 11),
				),
			},
		},
		"case sensitive - no match": {
			input:  "key: VALUE",
			search: "value",
			want:   nil,
		},
		"case insensitive with normalizer": {
			input:      "key: VALUE",
			search:     "value",
			normalizer: yamltest.NewCustomNormalizer(strings.ToLower),
			want: position.Ranges{
				position.NewRange(
					position.New(0, 5),
					position.New(0, 10),
				),
			},
		},
		"single character match": {
			input:  "a: b",
			search: "a",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 0),
					position.New(0, 1),
				),
			},
		},
		"overlapping potential matches": {
			input:  "aaa",
			search: "aa",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 0),
					position.New(0, 2),
				),
			},
		},
		"utf8 - search text after multibyte char": {
			input:  "name: Thaïs test",
			search: "test",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 12),
					position.New(0, 16),
				),
			},
		},
		"utf8 - search for multibyte char": {
			input:  "name: Thaïs",
			search: "ï",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 9),
					position.New(0, 10),
				),
			},
		},
		"utf8 - search spanning multibyte char": {
			input:  "name: Thaïs",
			search: "ïs",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 9),
					position.New(0, 11),
				),
			},
		},
		"utf8 - multiple multibyte chars": {
			input:  "key: über öffentlich",
			search: "öffentlich",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 10),
					position.New(0, 20),
				),
			},
		},
		"utf8 - normalizer finds diacritic as ascii": {
			input:      "key: über öffentlich",
			search:     "o",
			normalizer: normalizer.New(),
			want: position.Ranges{
				position.NewRange(
					position.New(0, 10),
					position.New(0, 11),
				),
			},
		},
		"utf8 - combined normalizer case and diacritics": {
			input:      "name: THAÏS test",
			search:     "thais",
			normalizer: normalizer.New(),
			want: position.Ranges{
				position.NewRange(
					position.New(0, 6),
					position.New(0, 11),
				),
			},
		},
		"utf8 - japanese characters partial match": {
			input:  "key: 日本酒",
			search: "日本",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 5),
					position.New(0, 7),
				),
			},
		},
		"utf8 - japanese after other japanese": {
			input:  "- 寿司: 日本酒",
			search: "日本",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 6),
					position.New(0, 8),
				),
			},
		},
		"utf8 - multiline with japanese": {
			input:  "a: test\n- 寿司: 日本酒",
			search: "日本",
			want: position.Ranges{
				position.NewRange(
					position.New(1, 6),
					position.New(1, 8),
				),
			},
		},
		"utf8 - box drawing chars not matched by japanese": {
			input:  "# ───────────",
			search: "日本",
			want:   nil,
		},
		"emoji search": {
			input:  "icon: 🎉",
			search: "🎉",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 6),
					position.New(0, 7),
				),
			},
		},
		"search longer than input": {
			input:  "a: b",
			search: "this is a very long search string",
			want:   nil,
		},
		"search equals input": {
			input:  "key: value",
			search: "key: value",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 0),
					position.New(0, 10),
				),
			},
		},
		"whitespace only search": {
			input:  "key: value",
			search: " ",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 4),
					position.New(0, 5),
				),
			},
		},
		"consecutive matches": {
			input:  "aaa: bbb",
			search: "a",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 0),
					position.New(0, 1),
				),
				position.NewRange(
					position.New(0, 1),
					position.New(0, 2),
				),
				position.NewRange(
					position.New(0, 2),
					position.New(0, 3),
				),
			},
		},
		"special yaml chars in search": {
			input:  "text: \"[not] {a} list\"",
			search: "[not]",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 7),
					position.New(0, 12),
				),
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := niceyaml.NewSourceFromString(tc.input)

			var opts []finder.Option

			if tc.normalizer != nil {
				opts = append(opts, finder.WithNormalizer(tc.normalizer))
			}

			f := finder.New(opts...)

			idx := f.Load(lines.Lines())

			got := idx.Find(tc.search)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinder_Find_EdgeCases(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input  string
		search string
		want   position.Ranges
	}{
		"empty lines": {
			input:  "",
			search: "test",
			want:   nil,
		},
		"first character": {
			input:  "key: value",
			search: "k",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 0),
					position.New(0, 1),
				),
			},
		},
		"last character": {
			input:  "key: value",
			search: "e",
			want: position.Ranges{
				position.NewRange(
					position.New(0, 1),
					position.New(0, 2),
				),
				position.NewRange(
					position.New(0, 9),
					position.New(0, 10),
				),
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := niceyaml.NewSourceFromString(tc.input)
			f := finder.New()
			idx := f.Load(lines.Lines())

			got := idx.Find(tc.search)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinder_Find_Expansion(t *testing.T) {
	t.Parallel()

	// Case folding turns one source rune into two normalized chars, so a
	// one-char needle could match twice inside it and report the same range
	// twice. Only the match that begins at the source rune counts.
	tcs := map[string]struct {
		input  string
		search string
		want   position.Ranges
	}{
		"needle inside an expansion matches once": {
			input:  "x: ß",
			search: "s",
			want: position.Ranges{
				position.NewRange(position.New(0, 3), position.New(0, 4)),
			},
		},
		"needle covering the expansion": {
			input:  "x: ß",
			search: "ss",
			want: position.Ranges{
				position.NewRange(position.New(0, 3), position.New(0, 4)),
			},
		},
		"needle ending inside an expansion covers the rune": {
			input:  "x: aß",
			search: "as",
			want: position.Ranges{
				position.NewRange(position.New(0, 3), position.New(0, 5)),
			},
		},
		"needle starting inside an expansion does not match": {
			input:  "x: ßa",
			search: "sa",
			want:   nil,
		},
		"expansion next to a plain match": {
			input:  "x: ßs",
			search: "s",
			want: position.Ranges{
				position.NewRange(position.New(0, 3), position.New(0, 4)),
				position.NewRange(position.New(0, 4), position.New(0, 5)),
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := finder.New(finder.WithNormalizer(normalizer.New()))
			idx := f.Load(niceyaml.NewSourceFromString(tc.input).Lines())

			assert.Equal(t, tc.want, idx.Find(tc.search))
		})
	}
}

func TestFinder_Find_NormalizesToEmpty(t *testing.T) {
	t.Parallel()

	// A search of only combining marks normalizes to an empty string, which
	// must yield no matches rather than match at every offset.
	f := finder.New(finder.WithNormalizer(normalizer.New()))
	idx := f.Load(niceyaml.NewSourceFromString("key: value\n").Lines())

	got := idx.Find("́")
	assert.Nil(t, got)
}

func TestFinder_Find_ContextSensitiveNormalizer(t *testing.T) {
	t.Parallel()

	// Title casing depends on the preceding character, so it would produce
	// "Abc" for a whole string but "ABC" rune by rune. Both sides go through
	// the normalizer the same way, so the search still finds the text.
	n := normalizer.New(
		normalizer.WithCaseFold(false),
		normalizer.WithDiacriticFold(false),
		normalizer.WithTransformer(func() transform.Transformer {
			return cases.Title(language.Und)
		}),
	)
	f := finder.New(finder.WithNormalizer(n))
	idx := f.Load(niceyaml.NewSourceFromString("k: abc\n").Lines())

	want := position.Ranges{
		position.NewRange(position.New(0, 3), position.New(0, 6)),
	}

	assert.Equal(t, want, idx.Find("abc"))
	assert.Equal(t, want, idx.Find("ABC"))
}

func TestFinder_Find_InvalidUTF8(t *testing.T) {
	t.Parallel()

	// A stray continuation byte must not match inside a multi-byte rune and
	// then report the position of an unrelated rune.
	tcs := map[string]struct {
		input      string
		search     string
		normalizer finder.Normalizer
		want       position.Ranges
	}{
		"continuation byte without normalizer": {
			input:  "x: a\u00e9",
			search: "\xa9",
			want:   nil,
		},
		"continuation byte with normalizer": {
			input:      "x: a\u00e9",
			search:     "\xa9",
			normalizer: normalizer.New(),
			want:       nil,
		},
		"truncated rune": {
			input:  "x: a\u00e9",
			search: "a\xc3",
			want:   nil,
		},
		"replacement character in the source": {
			input:  "x: a\xa9b",
			search: "\xa9",
			want: position.Ranges{
				position.NewRange(position.New(0, 4), position.New(0, 5)),
			},
		},
		"normalizer emitting an invalid byte": {
			// The needle and the index both pass through the normalizer,
			// so an invalid byte it emits must read the same on both sides.
			input:      "a: x",
			search:     "x",
			normalizer: invalidByteNormalizer{},
			want: position.Ranges{
				position.NewRange(position.New(0, 3), position.New(0, 4)),
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []finder.Option

			if tc.normalizer != nil {
				opts = append(opts, finder.WithNormalizer(tc.normalizer))
			}

			idx := finder.New(opts...).Load(niceyaml.NewSourceFromString(tc.input).Lines())

			assert.Equal(t, tc.want, idx.Find(tc.search))
		})
	}
}

// invalidByteNormalizer maps "x" to a byte that is not valid UTF-8 and
// leaves every other string alone.
type invalidByteNormalizer struct{}

func (invalidByteNormalizer) Normalize(in string) string {
	if in == "x" {
		return "\xff"
	}

	return in
}

func TestFinder_Find_NilLines(t *testing.T) {
	t.Parallel()

	f := finder.New()
	idx := f.Load(line.Lines{})

	got := idx.Find("test")
	assert.Nil(t, got)
}

func TestFinder_Find_DiffBuiltLines(t *testing.T) {
	t.Parallel()

	// When searching Lines built from a diff, matches should be at the correct
	// visual line positions, not based on original source Position.Line.
	//
	// Diff produces:
	// Line 0 (idx=0): "key: old" (deleted, Position.Line=1)
	// Line 1 (idx=1): "key: new" (inserted, Position.Line=1)
	// Both have same source Position.Line, but different visual indices.

	before := "key: old\n"
	after := "key: new\n"

	beforeLines := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterLines := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	lines := diff.Diff(beforeLines.Lines(), afterLines.Lines()).Unified()

	tcs := map[string]struct {
		search string
		want   position.Ranges
	}{
		"search for 'old' finds match at visual line 0": {
			search: "old",
			want: position.Ranges{
				position.NewRange(position.New(0, 5), position.New(0, 8)),
			},
		},
		"search for 'new' finds match at visual line 1": {
			search: "new",
			want: position.Ranges{
				position.NewRange(position.New(1, 5), position.New(1, 8)),
			},
		},
		"search for 'key' finds matches at both visual lines": {
			search: "key",
			want: position.Ranges{
				position.NewRange(position.New(0, 0), position.New(0, 3)),
				position.NewRange(position.New(1, 0), position.New(1, 3)),
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := finder.New()
			idx := f.Load(lines.Lines())

			got := idx.Find(tc.search)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinder_Reload(t *testing.T) {
	t.Parallel()

	// Every Load builds its own Index, so one Finder serves any number of
	// sources and an earlier Index keeps working.
	t.Run("each load builds an independent index", func(t *testing.T) {
		t.Parallel()

		f := finder.New()

		first := f.Load(niceyaml.NewSourceFromString("first: 1").Lines())
		assert.Len(t, first.Find("first"), 1)

		second := f.Load(niceyaml.NewSourceFromString("second: 2").Lines())
		assert.Nil(t, second.Find("first"))
		assert.Len(t, second.Find("second"), 1)

		// The first index is unchanged by the second load.
		assert.Len(t, first.Find("first"), 1)
		assert.Nil(t, first.Find("second"))
	})

	t.Run("nil index finds nothing", func(t *testing.T) {
		t.Parallel()

		var idx *finder.Index

		assert.Nil(t, idx.Find("anything"))
	})
}

func TestFinder_Find_MultipleSearches(t *testing.T) {
	t.Parallel()

	// Test multiple Find calls on the same loaded source.
	lines := niceyaml.NewSourceFromString("key: value\nother: data")
	f := finder.New()
	idx := f.Load(lines.Lines())

	tcs := map[string]struct {
		search string
		want   int
	}{
		"find key":   {search: "key", want: 1},
		"find value": {search: "value", want: 1},
		"find other": {search: "other", want: 1},
		"find data":  {search: "data", want: 1},
		"find colon": {search: ":", want: 2},
		"not found":  {search: "missing", want: 0},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := idx.Find(tc.search)

			if tc.want == 0 {
				assert.Nil(t, got)
			} else {
				assert.Len(t, got, tc.want)
			}
		})
	}
}
