package niceyaml_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macropower/niceyaml"
)

// testNormalizer wraps a function to implement [niceyaml.Normalizer].
type testNormalizer struct {
	fn func(string) string
}

func (n testNormalizer) Normalize(in string) string {
	return n.fn(in)
}

func TestFinder_Find(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input      string
		search     string
		normalizer niceyaml.Normalizer
		want       []niceyaml.PositionRange
	}{
		"single token match": {
			input:  "key: value",
			search: "value",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 5),
					niceyaml.NewPosition(0, 10),
				),
			},
		},
		"match key": {
			input:  "key: value",
			search: "key",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 0),
					niceyaml.NewPosition(0, 3),
				),
			},
		},
		"cross-token match": {
			input:  "key: value",
			search: ": ",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 3),
					niceyaml.NewPosition(0, 5),
				),
			},
		},
		"multiple matches": {
			input:  "a: test\nb: test\nc: test",
			search: "test",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 3),
					niceyaml.NewPosition(0, 7),
				),
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(1, 3),
					niceyaml.NewPosition(1, 7),
				),
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(2, 3),
					niceyaml.NewPosition(2, 7),
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
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(2, 2),
					niceyaml.NewPosition(2, 7),
				),
			},
		},
		"match spans lines": {
			input:  "a: 1\nb: 2",
			search: "1\nb",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 3),
					niceyaml.NewPosition(1, 1),
				),
			},
		},
		"with normalizer - diacritic match": {
			input:      "name: Thaïs",
			search:     "Thais",
			normalizer: niceyaml.StandardNormalizer{},
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 6),
					niceyaml.NewPosition(0, 11),
				),
			},
		},
		"with normalizer - search has diacritic": {
			input:      "name: Thais",
			search:     "Thaïs",
			normalizer: niceyaml.StandardNormalizer{},
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 6),
					niceyaml.NewPosition(0, 11),
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
			normalizer: testNormalizer{fn: strings.ToLower},
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 5),
					niceyaml.NewPosition(0, 10),
				),
			},
		},
		"single character match": {
			input:  "a: b",
			search: "a",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 0),
					niceyaml.NewPosition(0, 1),
				),
			},
		},
		"overlapping potential matches": {
			input:  "aaa",
			search: "aa",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 0),
					niceyaml.NewPosition(0, 2),
				),
			},
		},
		"utf8 - search text after multibyte char": {
			input:  "name: Thaïs test",
			search: "test",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 12),
					niceyaml.NewPosition(0, 16),
				),
			},
		},
		"utf8 - search for multibyte char": {
			input:  "name: Thaïs",
			search: "ï",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 9),
					niceyaml.NewPosition(0, 10),
				),
			},
		},
		"utf8 - search spanning multibyte char": {
			input:  "name: Thaïs",
			search: "ïs",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 9),
					niceyaml.NewPosition(0, 11),
				),
			},
		},
		"utf8 - multiple multibyte chars": {
			input:  "key: über öffentlich",
			search: "öffentlich",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 10),
					niceyaml.NewPosition(0, 20),
				),
			},
		},
		"utf8 - normalizer finds diacritic as ascii": {
			input:      "key: über öffentlich",
			search:     "o",
			normalizer: niceyaml.StandardNormalizer{},
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 10),
					niceyaml.NewPosition(0, 11),
				),
			},
		},
		"utf8 - combined normalizer case and diacritics": {
			input:      "name: THAÏS test",
			search:     "thais",
			normalizer: niceyaml.StandardNormalizer{},
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 6),
					niceyaml.NewPosition(0, 11),
				),
			},
		},
		"utf8 - japanese characters partial match": {
			input:  "key: 日本酒",
			search: "日本",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 5),
					niceyaml.NewPosition(0, 7),
				),
			},
		},
		"utf8 - japanese after other japanese": {
			input:  "- 寿司: 日本酒",
			search: "日本",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 6),
					niceyaml.NewPosition(0, 8),
				),
			},
		},
		"utf8 - multiline with japanese": {
			input:  "a: test\n- 寿司: 日本酒",
			search: "日本",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(1, 6),
					niceyaml.NewPosition(1, 8),
				),
			},
		},
		"utf8 - box drawing chars not matched by japanese": {
			input:  "# ───────────",
			search: "日本",
			want:   nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := niceyaml.NewLinesFromString(tc.input)

			var opts []niceyaml.FinderOption
			if tc.normalizer != nil {
				opts = append(opts, niceyaml.WithNormalizer(tc.normalizer))
			}

			finder := niceyaml.NewFinder(tc.search, opts...)

			got := finder.Find(lines)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinder_Find_EdgeCases(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input  string
		search string
		want   []niceyaml.PositionRange
	}{
		"empty lines": {
			input:  "",
			search: "test",
			want:   nil,
		},
		"first character": {
			input:  "key: value",
			search: "k",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 0),
					niceyaml.NewPosition(0, 1),
				),
			},
		},
		"last character": {
			input:  "key: value",
			search: "e",
			want: []niceyaml.PositionRange{
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 1),
					niceyaml.NewPosition(0, 2),
				),
				niceyaml.NewPositionRange(
					niceyaml.NewPosition(0, 9),
					niceyaml.NewPosition(0, 10),
				),
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := niceyaml.NewLinesFromString(tc.input)
			finder := niceyaml.NewFinder(tc.search)
			got := finder.Find(lines)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinder_Find_NilLines(t *testing.T) {
	t.Parallel()

	finder := niceyaml.NewFinder("test")
	got := finder.Find(nil)
	assert.Nil(t, got)
}

func TestFinder_Find_DiffBuiltLines(t *testing.T) {
	t.Parallel()

	// When searching Lines built from a diff, matches should be at the correct
	// visual line positions, not based on original source Position.Line.

	before := "key: old\n"
	after := "key: new\n"

	beforeLines := niceyaml.NewLinesFromString(before, niceyaml.WithName("before"))
	afterLines := niceyaml.NewLinesFromString(after, niceyaml.WithName("after"))

	revBefore := niceyaml.NewRevision(beforeLines)
	revAfter := niceyaml.NewRevision(afterLines)

	diff := niceyaml.NewFullDiff(revBefore, revAfter)
	lines := diff.Lines()

	// Diff produces:
	// Line 0 (idx=0): "key: old" (deleted, Position.Line=1)
	// Line 1 (idx=1): "key: new" (inserted, Position.Line=1)
	// Both have same source Position.Line, but different visual indices.

	t.Run("search for 'old' finds match at visual line 0", func(t *testing.T) {
		t.Parallel()

		finder := niceyaml.NewFinder("old")
		matches := finder.Find(lines)

		want := []niceyaml.PositionRange{
			niceyaml.NewPositionRange(niceyaml.NewPosition(0, 5), niceyaml.NewPosition(0, 8)),
		}
		assert.Equal(t, want, matches)
	})

	t.Run("search for 'new' finds match at visual line 1", func(t *testing.T) {
		t.Parallel()

		finder := niceyaml.NewFinder("new")
		matches := finder.Find(lines)

		want := []niceyaml.PositionRange{
			niceyaml.NewPositionRange(niceyaml.NewPosition(1, 5), niceyaml.NewPosition(1, 8)),
		}
		assert.Equal(t, want, matches)
	})

	t.Run("search for 'key' finds matches at both visual lines", func(t *testing.T) {
		t.Parallel()

		finder := niceyaml.NewFinder("key")
		matches := finder.Find(lines)

		want := []niceyaml.PositionRange{
			niceyaml.NewPositionRange(niceyaml.NewPosition(0, 0), niceyaml.NewPosition(0, 3)),
			niceyaml.NewPositionRange(niceyaml.NewPosition(1, 0), niceyaml.NewPosition(1, 3)),
		}
		assert.Equal(t, want, matches)
	})
}
