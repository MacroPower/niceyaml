package finder_test

import (
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/normalizer"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// noSpace returns a normalizer that drops whitespace.
func noSpace() *normalizer.Normalizer {
	return normalizer.New(normalizer.WithTransformer(func() transform.Transformer {
		return runes.Remove(runes.In(unicode.White_Space))
	}))
}

func TestFinder_Find(t *testing.T) {
	t.Parallel()

	// Title casing depends on the preceding character.
	titleCase := normalizer.New(
		normalizer.WithCaseFold(false),
		normalizer.WithDiacriticFold(false),
		normalizer.WithTransformer(func() transform.Transformer {
			return cases.Title(language.Und)
		}),
	)

	tcs := map[string]struct {
		input      string
		search     string
		normalizer finder.Normalizer
		// Matches characters exactly, with no normalizer at all.
		exact bool
		want  position.Ranges
	}{
		"single token match": {
			input:  "key: value",
			search: "value",
			want:   position.Ranges{yamltest.LineRange(0, 5, 10)},
		},
		"match key": {
			input:  "key: value",
			search: "key",
			want:   position.Ranges{yamltest.LineRange(0, 0, 3)},
		},
		"cross-token match": {
			input:  "key: value",
			search: ": ",
			want:   position.Ranges{yamltest.LineRange(0, 3, 5)},
		},
		"multiple matches": {
			input:  "a: test\nb: test\nc: test",
			search: "test",
			want: position.Ranges{
				yamltest.LineRange(0, 3, 7),
				yamltest.LineRange(1, 3, 7),
				yamltest.LineRange(2, 3, 7),
			},
		},
		"no match": {
			input:  "key: value",
			search: "notfound",
		},
		"empty search": {
			input:  "key: value",
			search: "",
		},
		"multi-line value": {
			input:  "text: |\n  line1\n  line2",
			search: "line2",
			want:   position.Ranges{yamltest.LineRange(2, 2, 7)},
		},
		"match spans lines": {
			input:  "a: 1\nb: 2",
			search: "1\nb",
			want:   position.Ranges{position.NewRange(position.New(0, 3), position.New(1, 1))},
		},
		"with normalizer - diacritic match": {
			input:      "name: Thaïs",
			search:     "Thais",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 6, 11)},
		},
		"with normalizer - search has diacritic": {
			input:      "name: Thais",
			search:     "Thaïs",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 6, 11)},
		},
		"with normalizer - kana voicing mark distinguishes": {
			input:      "name: ガ",
			search:     "カ",
			normalizer: normalizer.New(),
		},
		"with normalizer - kana voicing mark distinguishes decomposed source": {
			input:      "name: ガ",
			search:     "カ",
			normalizer: normalizer.New(),
		},
		"with normalizer - katakana precomposed source, decomposed search": {
			input:      "name: ガ",
			search:     "ガ",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 6, 7)},
		},
		"with normalizer - katakana decomposed source, precomposed search": {
			input:      "name: ガ",
			search:     "ガ",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 6, 8)},
		},
		"with normalizer - hiragana decomposed source, precomposed search": {
			input:      "name: が",
			search:     "が",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 6, 8)},
		},
		"with normalizer - hangul precomposed source, decomposed search": {
			input:      "name: 한국",
			search:     "한국",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 6, 8)},
		},
		"with normalizer - hangul decomposed source, precomposed search": {
			input:      "name: 한국",
			search:     "한국",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 6, 12)},
		},
		"with normalizer - width fold matches halfwidth voiced katakana": {
			input:      "name: ガ",
			search:     "ｶﾞ",
			normalizer: normalizer.New(normalizer.WithWidthFold(true)),
			want:       position.Ranges{yamltest.LineRange(0, 6, 7)},
		},
		"with normalizer - width fold finds halfwidth voiced katakana": {
			input:      "name: ｶﾞ",
			search:     "ガ",
			normalizer: normalizer.New(normalizer.WithWidthFold(true)),
			want:       position.Ranges{yamltest.LineRange(0, 6, 8)},
		},
		"with normalizer - cherokee uppercase search": {
			input:      "name: \uab70",
			search:     "\u13a0",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 6, 7)},
		},
		"with normalizer - cherokee lowercase search": {
			input:      "name: \u13a0",
			search:     "\uab70",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 6, 7)},
		},
		"exact matching - no match": {
			input:  "key: VALUE",
			search: "value",
			exact:  true,
		},
		"exact matching - match covers a trailing combining mark": {
			input:  "name: café",
			search: "cafe",
			exact:  true,
			want:   position.Ranges{yamltest.LineRange(0, 6, 11)},
		},
		"nil normalizer pointer - no match": {
			input:      "key: VALUE",
			search:     "value",
			normalizer: (*normalizer.Normalizer)(nil),
		},
		"nil normalizer pointer - exact match": {
			input:      "key: VALUE",
			search:     "VALUE",
			normalizer: (*normalizer.Normalizer)(nil),
			want:       position.Ranges{yamltest.LineRange(0, 5, 10)},
		},
		"case insensitive with normalizer": {
			input:      "key: VALUE",
			search:     "value",
			normalizer: yamltest.NormalizerFunc(strings.ToLower),
			want:       position.Ranges{yamltest.LineRange(0, 5, 10)},
		},
		"normalizer dropping whitespace joins lines": {
			input:      "a: 1\nb: 2\n",
			search:     "1b",
			normalizer: noSpace(),
			want:       position.Ranges{position.NewRange(position.New(0, 3), position.New(1, 1))},
		},
		"normalizer dropping whitespace covers the dropped space": {
			input:      "k: ab cd",
			search:     "ab",
			normalizer: noSpace(),
			want:       position.Ranges{yamltest.LineRange(0, 3, 6)},
		},
		"single character match": {
			input:  "a: b",
			search: "a",
			want:   position.Ranges{yamltest.LineRange(0, 0, 1)},
		},
		"overlapping potential matches": {
			input:  "aaa",
			search: "aa",
			want:   position.Ranges{yamltest.LineRange(0, 0, 2)},
		},
		"utf8 - search text after multibyte char": {
			input:  "name: Thaïs test",
			search: "test",
			want:   position.Ranges{yamltest.LineRange(0, 12, 16)},
		},
		"utf8 - search for multibyte char": {
			input:  "name: Thaïs",
			search: "ï",
			want:   position.Ranges{yamltest.LineRange(0, 9, 10)},
		},
		"utf8 - search spanning multibyte char": {
			input:  "name: Thaïs",
			search: "ïs",
			want:   position.Ranges{yamltest.LineRange(0, 9, 11)},
		},
		"utf8 - multiple multibyte chars": {
			input:  "key: über öffentlich",
			search: "öffentlich",
			want:   position.Ranges{yamltest.LineRange(0, 10, 20)},
		},
		"utf8 - normalizer finds diacritic as ascii": {
			input:      "key: über öffentlich",
			search:     "o",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 10, 11)},
		},
		"utf8 - combined normalizer case and diacritics": {
			input:      "name: THAÏS test",
			search:     "thais",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 6, 11)},
		},
		"utf8 - japanese characters partial match": {
			input:  "key: 日本酒",
			search: "日本",
			want:   position.Ranges{yamltest.LineRange(0, 5, 7)},
		},
		"utf8 - japanese after other japanese": {
			input:  "- 寿司: 日本酒",
			search: "日本",
			want:   position.Ranges{yamltest.LineRange(0, 6, 8)},
		},
		"utf8 - multiline with japanese": {
			input:  "a: test\n- 寿司: 日本酒",
			search: "日本",
			want:   position.Ranges{yamltest.LineRange(1, 6, 8)},
		},
		"utf8 - box drawing chars not matched by japanese": {
			input:  "# ───────────",
			search: "日本",
		},
		"emoji search": {
			input:  "icon: 🎉",
			search: "🎉",
			want:   position.Ranges{yamltest.LineRange(0, 6, 7)},
		},
		"search longer than input": {
			input:  "a: b",
			search: "this is a very long search string",
		},
		"search equals input": {
			input:  "key: value",
			search: "key: value",
			want:   position.Ranges{yamltest.LineRange(0, 0, 10)},
		},
		"whitespace only search": {
			input:  "key: value",
			search: " ",
			want:   position.Ranges{yamltest.LineRange(0, 4, 5)},
		},
		"consecutive matches": {
			input:  "aaa: bbb",
			search: "a",
			want: position.Ranges{
				yamltest.LineRange(0, 0, 1),
				yamltest.LineRange(0, 1, 2),
				yamltest.LineRange(0, 2, 3),
			},
		},
		"special yaml chars in search": {
			input:  "text: \"[not] {a} list\"",
			search: "[not]",
			want:   position.Ranges{yamltest.LineRange(0, 7, 12)},
		},
		"empty input": {
			input:  "",
			search: "test",
		},
		"last character": {
			input:  "key: value",
			search: "e",
			want:   position.Ranges{yamltest.LineRange(0, 1, 2), yamltest.LineRange(0, 9, 10)},
		},
		// The loaded text reads every line ending as "\n", so a search string
		// copied out of a CRLF file, line ending included, still matches.
		"CRLF source - CRLF search": {
			input:  "a: 1\r\nb: 2\r\n",
			search: "1\r\nb",
			want:   position.Ranges{position.NewRange(position.New(0, 3), position.New(1, 1))},
		},
		"CRLF source - LF search": {
			input:  "a: 1\r\nb: 2\r\n",
			search: "1\nb",
			want:   position.Ranges{position.NewRange(position.New(0, 3), position.New(1, 1))},
		},
		// A search of only combining marks normalizes to an empty string, which
		// must yield no matches rather than match at every offset.
		"search that normalizes to empty": {
			input:      "key: value\n",
			search:     "́",
			normalizer: normalizer.New(),
		},
		// The accent is a combining mark that belongs to the "e" before it, and
		// the normalizer drops it. A match ending at that "e" covers the accent,
		// as a match ending inside an expansion covers the whole character, so
		// the highlight ends where the next character begins.
		"match ending at a dropped combining mark": {
			input:      "k: caféx\n",
			search:     "café",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 3, 8)},
		},
		"match starting at a character with a dropped combining mark": {
			input:      "k: caféx\n",
			search:     "ex",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 6, 9)},
		},
		// Title casing would produce "Abc" for a whole string but "ABC" rune
		// by rune. Both sides go through the normalizer the same way, so the
		// search still finds the text.
		"context-sensitive normalizer - lowercase search": {
			input:      "k: abc\n",
			search:     "abc",
			normalizer: titleCase,
			want:       position.Ranges{yamltest.LineRange(0, 3, 6)},
		},
		"context-sensitive normalizer - uppercase search": {
			input:      "k: abc\n",
			search:     "ABC",
			normalizer: titleCase,
			want:       position.Ranges{yamltest.LineRange(0, 3, 6)},
		},
		// Case folding turns one source rune into two normalized chars, so a
		// one-char needle could match twice inside it and report the same range
		// twice. Only the match that begins at the source rune counts.
		"needle inside an expansion matches once": {
			input:      "x: ß",
			search:     "s",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 3, 4)},
		},
		"needle covering the expansion": {
			input:      "x: ß",
			search:     "ss",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 3, 4)},
		},
		"needle ending inside an expansion covers the rune": {
			input:      "x: aß",
			search:     "as",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 3, 5)},
		},
		"needle starting inside an expansion does not match": {
			input:      "x: ßa",
			search:     "sa",
			normalizer: normalizer.New(),
		},
		"expansion next to a plain match": {
			input:      "x: ßs",
			search:     "s",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 3, 4), yamltest.LineRange(0, 4, 5)},
		},
		"multibyte character between expansions": {
			input:      "x: ß日 ß",
			search:     "ss",
			normalizer: normalizer.New(),
			want:       position.Ranges{yamltest.LineRange(0, 3, 4), yamltest.LineRange(0, 6, 7)},
		},
		// A stray continuation byte must not match inside a multi-byte rune and
		// then report the position of an unrelated rune.
		"continuation byte without normalizer": {
			input:  "x: a\u00e9",
			search: "\xa9",
		},
		"continuation byte with normalizer": {
			input:      "x: a\u00e9",
			search:     "\xa9",
			normalizer: normalizer.New(),
		},
		"truncated rune": {
			input:  "x: a\u00e9",
			search: "a\xc3",
		},
		"replacement character in the source": {
			input:  "x: a\xa9b",
			search: "\xa9",
			want:   position.Ranges{yamltest.LineRange(0, 4, 5)},
		},
		"other invalid byte with exact matching": {
			// Exact matching compares characters, so any invalid byte
			// reads as U+FFFD and matches any other.
			input:  "a: x\xfey\n",
			search: "\xff",
			exact:  true,
			want:   position.Ranges{yamltest.LineRange(0, 4, 5)},
		},
		"U+FFFD needle with exact matching": {
			input:  "a: x\xfey\n",
			search: "\ufffd",
			exact:  true,
			want:   position.Ranges{yamltest.LineRange(0, 4, 5)},
		},
		"normalizer emitting an invalid byte": {
			// The needle and the index both pass through the normalizer,
			// so an invalid byte it emits must read the same on both sides.
			input:  "a: x",
			search: "x",
			normalizer: yamltest.NormalizerFunc(func(in string) string {
				if in == "x" {
					return "\xff"
				}

				return in
			}),
			want: position.Ranges{yamltest.LineRange(0, 3, 4)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []finder.Option

			if tc.normalizer != nil || tc.exact {
				opts = append(opts, finder.WithNormalizer(tc.normalizer))
			}

			lines := niceyaml.NewSourceFromString(tc.input).Lines()
			assert.Equal(t, tc.want, finder.New(opts...).Load(lines).Find(tc.search))
		})
	}
}

func TestFinder_Find_NilLines(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		lines line.Sequence
	}{
		"zero lines":              {lines: line.Lines{}},
		"nil sequence":            {lines: nil},
		"nil view":                {lines: (*line.View)(nil)},
		"nil pointer to lines":    {lines: (*line.Lines)(nil)},
		"view that holds no line": {lines: niceyaml.NewSourceFromString("test: 1\n").View().Slice(position.Span{})},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			idx := finder.New().Load(tc.lines)

			assert.Nil(t, idx.Find("test"))
		})
	}
}

func TestFinder_Find_View(t *testing.T) {
	t.Parallel()

	// The first document holds lines 0 and 1 of the file, and the second
	// holds lines 2 through 4.
	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n---\nname: x\nport: 80\n")

	docs := source.Documents()
	require.Len(t, docs, 2)

	// The view skips lines 1 and 2.
	gapped := source.View().Slice(position.NewSpan(0, 1), position.NewSpan(3, 5))

	// The view skips line 1, so "a\na" matches on lines 2 and 3 alone.
	repeated := niceyaml.NewSourceFromString("a\nz\na\na\n").View().
		Slice(position.NewSpan(0, 1), position.NewSpan(2, 4))

	// The unified diff holds nine rows, and its hunks hold rows 0 and 1,
	// where k1 changed, and rows 7 and 8, where k7 changed.
	hunks := diff.Diff(
		niceyaml.NewSourceFromString("k1: a\nk2: b\nk3: c\nk4: d\nk5: e\nk6: f\nk7: g\n").Lines(),
		niceyaml.NewSourceFromString("k1: A\nk2: b\nk3: c\nk4: d\nk5: e\nk6: f\nk7: G\n").Lines(),
	).Hunks(0)
	require.Equal(t, 4, hunks.Count())

	// The host shares line 2 of the file with the port beside it.
	flow := niceyaml.NewSourceFromString("top: 1\n---\nserver: {host: h, port: 80}\nport: 1\n").Documents()
	require.Len(t, flow, 2)

	host := yamltest.At(t, flow[1], paths.Doc().Child("server", "host"))

	// The unified diff holds "key: old" on row 0 and "key: new" on row 1.
	// Both lines are line 1 of their revision, and a match names the row.
	unified := diff.Diff(
		niceyaml.NewSourceFromString("key: old\n").Lines(),
		niceyaml.NewSourceFromString("key: new\n").Lines(),
	).Unified()

	tcs := map[string]struct {
		view       *line.View
		normalizer finder.Normalizer
		search     string
		want       position.Ranges
	}{
		"view of every line matches as its lines do": {
			view:   source.View(),
			search: "2\n---",
			want:   position.Ranges{position.NewRange(position.New(1, 3), position.New(2, 3))},
		},
		"document reports the lines of the file": {
			view:   docs[1].View(),
			search: "port",
			want:   position.Ranges{yamltest.LineRange(4, 0, 4)},
		},
		"line the view does not hold": {
			view:   docs[1].View(),
			search: "a: 1",
		},
		"match ends with the last held line": {
			view:   docs[0].View(),
			search: "2\n",
			want:   position.Ranges{yamltest.LineRange(1, 3, 5)},
		},
		"match does not run past the last held line": {
			view:   docs[0].View(),
			search: "2\n---",
		},
		"match does not start above the first held line": {
			view:   docs[1].View(),
			search: "2\n---",
		},
		"match spans held lines in a row": {
			view:   gapped,
			search: "x\nport",
			want:   position.Ranges{position.NewRange(position.New(3, 6), position.New(4, 4))},
		},
		"match does not cross a gap": {
			view:   gapped,
			search: "1\nname",
		},
		"normalizer dropping whitespace joins held lines in a row": {
			view:       gapped,
			normalizer: noSpace(),
			search:     "xport",
			want:       position.Ranges{position.NewRange(position.New(3, 6), position.New(4, 4))},
		},
		"normalizer dropping whitespace does not join text across a gap": {
			view:       gapped,
			normalizer: noSpace(),
			search:     "1name",
		},
		"match that overlaps one crossing a gap": {
			view:   repeated,
			search: "a\na",
			want:   position.Ranges{position.NewRange(position.New(2, 0), position.New(3, 1))},
		},
		"hunks report the rows of the unified diff": {
			view:   hunks,
			search: "k7",
			want:   position.Ranges{yamltest.LineRange(7, 0, 2), yamltest.LineRange(8, 0, 2)},
		},
		"unified diff reports the row of a deleted line": {
			view:   unified,
			search: "old",
			want:   position.Ranges{yamltest.LineRange(0, 5, 8)},
		},
		"unified diff reports the row of an inserted line": {
			view:   unified,
			search: "new",
			want:   position.Ranges{yamltest.LineRange(1, 5, 8)},
		},
		"unified diff reports both rows of a change": {
			view:   unified,
			search: "key",
			want:   position.Ranges{yamltest.LineRange(0, 0, 3), yamltest.LineRange(1, 0, 3)},
		},
		"match spans the rows of one hunk": {
			view:   hunks,
			search: "a\nk1",
			want:   position.Ranges{position.NewRange(position.New(0, 4), position.New(1, 2))},
		},
		"match does not run from one hunk into the next": {
			view:   hunks,
			search: "A\nk7",
		},
		"node holds the whole of its line": {
			view:   host.View(),
			search: "port",
			want:   position.Ranges{yamltest.LineRange(2, 18, 22)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []finder.Option

			if tc.normalizer != nil {
				opts = append(opts, finder.WithNormalizer(tc.normalizer))
			}

			got := finder.New(opts...).Load(tc.view).Find(tc.search)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinder_Find_ViewMarksItself(t *testing.T) {
	t.Parallel()

	docs := niceyaml.NewSourceFromString("a: 1\nb: 2\n---\nname: x\nport: 80\n").Documents()
	require.Len(t, docs, 2)

	// The matches of a view are in the coordinates of its content, so they
	// mark the view they came from.
	view := docs[1].View()
	view.AddOverlay(kind.GenericHighlight, finder.New().Load(view).Find("port")...)

	assert.Equal(t, stringtest.JoinLF(
		"   3 | ---",
		"   4 | name: x",
		"   5 | port: 80",
		"     | ^^^^",
	), view.String())
}

func TestFinder_Find_LineWithoutEnding(t *testing.T) {
	t.Parallel()

	// A diff view can hold a line with no line ending before another line,
	// such as the last line of a revision or a placeholder row. The loaded
	// text still ends every line but the last with "\n", so no match joins
	// the text of two lines.
	noEnding := diff.Diff(
		niceyaml.NewSourceFromString("x: 1").Lines(),
		niceyaml.NewSourceFromString("x: 2\n").Lines(),
	)
	placeholders := diff.Diff(
		niceyaml.NewSourceFromString("a: 1\nb: 2\n").Lines(),
		niceyaml.NewSourceFromString("a: 1\nn: 1\nm: 1\nb: 2\n").Lines(),
	)

	tcs := map[string]struct {
		lines  line.Sequence
		search string
		want   position.Ranges
	}{
		"unified view keeps the deleted line apart from the next": {
			lines:  noEnding.Unified(),
			search: "1x",
		},
		"unified view matches across the line break": {
			lines:  noEnding.Unified(),
			search: "1\nx",
			want:   position.Ranges{position.NewRange(position.New(0, 3), position.New(1, 1))},
		},
		"hunks view keeps the deleted line apart from the next": {
			lines:  noEnding.Hunks(3),
			search: "1x",
		},
		"placeholder rows keep their neighbors apart": {
			lines:  placeholders.Before(),
			search: "1\nb",
		},
		"placeholder rows each read as a line break": {
			lines:  placeholders.Before(),
			search: "1\n\n\nb",
			want:   position.Ranges{position.NewRange(position.New(0, 3), position.New(3, 1))},
		},
		"last line without an ending reads no line break": {
			lines:  niceyaml.NewSourceFromString("a: 1").Lines(),
			search: "1\n",
		},
		"placeholder rows a view holds each read as a line break": {
			lines:  placeholders.Before().Slice(position.NewSpan(1, 4)),
			search: "\n\nb",
			want:   position.Ranges{position.NewRange(position.New(1, 0), position.New(3, 1))},
		},
		"last held line without an ending reads no line break": {
			lines:  noEnding.Unified().Slice(position.NewSpan(0, 1)),
			search: "1\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := finder.New().Load(tc.lines).Find(tc.search)

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

		// The second load leaves the first index unchanged.
		assert.Len(t, first.Find("first"), 1)
		assert.Nil(t, first.Find("second"))
	})

	t.Run("nil index finds nothing", func(t *testing.T) {
		t.Parallel()

		var idx *finder.Index

		assert.Nil(t, idx.Find("anything"))
	})
}

func TestIndex_Find_Concurrent(t *testing.T) {
	t.Parallel()

	// The default normalizer is safe for concurrent use, so goroutines that
	// share one Index get the ranges a single goroutine gets.
	source := niceyaml.NewSourceFromString("name: Café\nstreet: Straße\nnote: café au lait\n")
	idx := finder.New().Load(source.Lines())

	searches := []string{"cafe", "STRASSE", "note", "au"}
	want := make([]position.Ranges, len(searches))

	for i, search := range searches {
		want[i] = idx.Find(search)
		assert.NotEmpty(t, want[i], search)
	}

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			for range 50 {
				for i, search := range searches {
					assert.Equal(t, want[i], idx.Find(search), search)
				}
			}
		})
	}

	wg.Wait()
}
