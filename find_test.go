package niceyaml_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"jacobcolvin.com/niceyaml"
	"jacobcolvin.com/niceyaml/internal/yamltest"
	"jacobcolvin.com/niceyaml/position"
)

func TestFinder_Find(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input      string
		search     string
		normalizer niceyaml.Normalizer
		want       []position.Range
	}{
		"single token match": {
			input:  "key: value",
			search: "value",
			want: []position.Range{
				position.NewRange(
					position.New(0, 5),
					position.New(0, 10),
				),
			},
		},
		"match key": {
			input:  "key: value",
			search: "key",
			want: []position.Range{
				position.NewRange(
					position.New(0, 0),
					position.New(0, 3),
				),
			},
		},
		"cross-token match": {
			input:  "key: value",
			search: ": ",
			want: []position.Range{
				position.NewRange(
					position.New(0, 3),
					position.New(0, 5),
				),
			},
		},
		"multiple matches": {
			input:  "a: test\nb: test\nc: test",
			search: "test",
			want: []position.Range{
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
			want: []position.Range{
				position.NewRange(
					position.New(2, 2),
					position.New(2, 7),
				),
			},
		},
		"match spans lines": {
			input:  "a: 1\nb: 2",
			search: "1\nb",
			want: []position.Range{
				position.NewRange(
					position.New(0, 3),
					position.New(1, 1),
				),
			},
		},
		"with normalizer - diacritic match": {
			input:      "name: Thaïs",
			search:     "Thais",
			normalizer: niceyaml.NewStandardNormalizer(),
			want: []position.Range{
				position.NewRange(
					position.New(0, 6),
					position.New(0, 11),
				),
			},
		},
		"with normalizer - search has diacritic": {
			input:      "name: Thais",
			search:     "Thaïs",
			normalizer: niceyaml.NewStandardNormalizer(),
			want: []position.Range{
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
			want: []position.Range{
				position.NewRange(
					position.New(0, 5),
					position.New(0, 10),
				),
			},
		},
		"single character match": {
			input:  "a: b",
			search: "a",
			want: []position.Range{
				position.NewRange(
					position.New(0, 0),
					position.New(0, 1),
				),
			},
		},
		"overlapping potential matches": {
			input:  "aaa",
			search: "aa",
			want: []position.Range{
				position.NewRange(
					position.New(0, 0),
					position.New(0, 2),
				),
			},
		},
		"utf8 - search text after multibyte char": {
			input:  "name: Thaïs test",
			search: "test",
			want: []position.Range{
				position.NewRange(
					position.New(0, 12),
					position.New(0, 16),
				),
			},
		},
		"utf8 - search for multibyte char": {
			input:  "name: Thaïs",
			search: "ï",
			want: []position.Range{
				position.NewRange(
					position.New(0, 9),
					position.New(0, 10),
				),
			},
		},
		"utf8 - search spanning multibyte char": {
			input:  "name: Thaïs",
			search: "ïs",
			want: []position.Range{
				position.NewRange(
					position.New(0, 9),
					position.New(0, 11),
				),
			},
		},
		"utf8 - multiple multibyte chars": {
			input:  "key: über öffentlich",
			search: "öffentlich",
			want: []position.Range{
				position.NewRange(
					position.New(0, 10),
					position.New(0, 20),
				),
			},
		},
		"utf8 - normalizer finds diacritic as ascii": {
			input:      "key: über öffentlich",
			search:     "o",
			normalizer: niceyaml.NewStandardNormalizer(),
			want: []position.Range{
				position.NewRange(
					position.New(0, 10),
					position.New(0, 11),
				),
			},
		},
		"utf8 - combined normalizer case and diacritics": {
			input:      "name: THAÏS test",
			search:     "thais",
			normalizer: niceyaml.NewStandardNormalizer(),
			want: []position.Range{
				position.NewRange(
					position.New(0, 6),
					position.New(0, 11),
				),
			},
		},
		"utf8 - japanese characters partial match": {
			input:  "key: 日本酒",
			search: "日本",
			want: []position.Range{
				position.NewRange(
					position.New(0, 5),
					position.New(0, 7),
				),
			},
		},
		"utf8 - japanese after other japanese": {
			input:  "- 寿司: 日本酒",
			search: "日本",
			want: []position.Range{
				position.NewRange(
					position.New(0, 6),
					position.New(0, 8),
				),
			},
		},
		"utf8 - multiline with japanese": {
			input:  "a: test\n- 寿司: 日本酒",
			search: "日本",
			want: []position.Range{
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
			want: []position.Range{
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
			want: []position.Range{
				position.NewRange(
					position.New(0, 0),
					position.New(0, 10),
				),
			},
		},
		"whitespace only search": {
			input:  "key: value",
			search: " ",
			want: []position.Range{
				position.NewRange(
					position.New(0, 4),
					position.New(0, 5),
				),
			},
		},
		"consecutive matches": {
			input:  "aaa: bbb",
			search: "a",
			want: []position.Range{
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
			want: []position.Range{
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

			var opts []niceyaml.FinderOption
			if tc.normalizer != nil {
				opts = append(opts, niceyaml.WithNormalizer(tc.normalizer))
			}

			finder := niceyaml.NewFinder(opts...)

			finder.Load(lines)

			got := finder.Find(tc.search)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinder_Find_EdgeCases(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input  string
		search string
		want   []position.Range
	}{
		"empty lines": {
			input:  "",
			search: "test",
			want:   nil,
		},
		"first character": {
			input:  "key: value",
			search: "k",
			want: []position.Range{
				position.NewRange(
					position.New(0, 0),
					position.New(0, 1),
				),
			},
		},
		"last character": {
			input:  "key: value",
			search: "e",
			want: []position.Range{
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
			finder := niceyaml.NewFinder()
			finder.Load(lines)

			got := finder.Find(tc.search)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinder_Find_NilLines(t *testing.T) {
	t.Parallel()

	finder := niceyaml.NewFinder()
	finder.Load(nil)

	got := finder.Find("test")
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

	revBefore := niceyaml.NewRevision(beforeLines)
	revAfter := niceyaml.NewRevision(afterLines)

	diff := niceyaml.NewFullDiff(revBefore, revAfter)
	lines := diff.Build()

	tcs := map[string]struct {
		search string
		want   []position.Range
	}{
		"search for 'old' finds match at visual line 0": {
			search: "old",
			want: []position.Range{
				position.NewRange(position.New(0, 5), position.New(0, 8)),
			},
		},
		"search for 'new' finds match at visual line 1": {
			search: "new",
			want: []position.Range{
				position.NewRange(position.New(1, 5), position.New(1, 8)),
			},
		},
		"search for 'key' finds matches at both visual lines": {
			search: "key",
			want: []position.Range{
				position.NewRange(position.New(0, 0), position.New(0, 3)),
				position.NewRange(position.New(1, 0), position.New(1, 3)),
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			finder := niceyaml.NewFinder()
			finder.Load(lines)

			got := finder.Find(tc.search)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinder_Reload(t *testing.T) {
	t.Parallel()

	// Test that a Finder can be reloaded with different sources.
	t.Run("reload clears previous data", func(t *testing.T) {
		t.Parallel()

		finder := niceyaml.NewFinder()

		// Load first source.
		lines1 := niceyaml.NewSourceFromString("first: 1")
		finder.Load(lines1)

		got1 := finder.Find("first")
		assert.Len(t, got1, 1)

		// Load second source - should clear previous data.
		lines2 := niceyaml.NewSourceFromString("second: 2")
		finder.Load(lines2)

		// Old search should not find anything.
		gotOld := finder.Find("first")
		assert.Nil(t, gotOld)

		// New search should work.
		gotNew := finder.Find("second")
		assert.Len(t, gotNew, 1)
	})
}

func TestFinder_Find_MultipleSearches(t *testing.T) {
	t.Parallel()

	// Test multiple Find calls on the same loaded source.
	lines := niceyaml.NewSourceFromString("key: value\nother: data")
	finder := niceyaml.NewFinder()
	finder.Load(lines)

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

			got := finder.Find(tc.search)

			if tc.want == 0 {
				assert.Nil(t, got)
			} else {
				assert.Len(t, got, tc.want)
			}
		})
	}
}
