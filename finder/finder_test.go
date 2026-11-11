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

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// noSpace is a fold that drops whitespace.
func noSpace(char string) string {
	return strings.TrimFunc(char, unicode.IsSpace)
}

func TestFinder_Find(t *testing.T) {
	t.Parallel()

	// Title casing depends on the preceding character.
	titleCase := func(char string) string {
		return cases.Title(language.Und).String(char)
	}

	tcs := map[string]struct {
		input  string
		search string
		opts   []finder.Option
		want   position.Ranges
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
			input: stringtest.JoinLF(
				"a: test",
				"b: test",
				"c: test",
			),
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
			input: stringtest.JoinLF(
				"text: |",
				"  line1",
				"  line2",
			),
			search: "line2",
			want:   position.Ranges{yamltest.LineRange(2, 2, 7)},
		},
		"match spans lines": {
			input:  "a: 1\nb: 2",
			search: "1\nb",
			want:   position.Ranges{position.NewRange(position.New(0, 3), position.New(1, 1))},
		},
		"default folds - diacritic match": {
			input:  "name: Thaïs",
			search: "Thais",
			want:   position.Ranges{yamltest.LineRange(0, 6, 11)},
		},
		"default folds - search has diacritic": {
			input:  "name: Thais",
			search: "Thaïs",
			want:   position.Ranges{yamltest.LineRange(0, 6, 11)},
		},
		"default folds - kana voicing mark distinguishes": {
			input:  "name: \u30ac",
			search: "\u30ab",
		},
		"default folds - kana voicing mark distinguishes decomposed source": {
			input:  "name: \u30ab\u3099",
			search: "\u30ab",
		},
		"default folds - katakana precomposed source, decomposed search": {
			input:  "name: \u30ac",
			search: "\u30ab\u3099",
			want:   position.Ranges{yamltest.LineRange(0, 6, 7)},
		},
		"default folds - katakana decomposed source, precomposed search": {
			input:  "name: \u30ab\u3099",
			search: "\u30ac",
			want:   position.Ranges{yamltest.LineRange(0, 6, 8)},
		},
		"default folds - hiragana decomposed source, precomposed search": {
			input:  "name: \u304b\u3099",
			search: "\u304c",
			want:   position.Ranges{yamltest.LineRange(0, 6, 8)},
		},
		"default folds - hangul precomposed source, decomposed search": {
			input:  "name: \ud55c\uad6d",
			search: "\u1112\u1161\u11ab\u1100\u116e\u11a8",
			want:   position.Ranges{yamltest.LineRange(0, 6, 8)},
		},
		"default folds - hangul decomposed source, precomposed search": {
			input:  "name: \u1112\u1161\u11ab\u1100\u116e\u11a8",
			search: "\ud55c\uad6d",
			want:   position.Ranges{yamltest.LineRange(0, 6, 12)},
		},
		"default folds - width fold matches halfwidth voiced katakana": {
			input:  "name: ガ",
			search: "ｶﾞ",
			opts:   []finder.Option{finder.WithWidthFold(true)},
			want:   position.Ranges{yamltest.LineRange(0, 6, 7)},
		},
		"default folds - width fold finds halfwidth voiced katakana": {
			input:  "name: ｶﾞ",
			search: "ガ",
			opts:   []finder.Option{finder.WithWidthFold(true)},
			want:   position.Ranges{yamltest.LineRange(0, 6, 8)},
		},
		"default folds - cherokee uppercase search": {
			input:  "name: \uab70",
			search: "\u13a0",
			want:   position.Ranges{yamltest.LineRange(0, 6, 7)},
		},
		"default folds - cherokee lowercase search": {
			input:  "name: \u13a0",
			search: "\uab70",
			want:   position.Ranges{yamltest.LineRange(0, 6, 7)},
		},
		"exact matching - no match": {
			input:  "key: VALUE",
			search: "value",
			opts:   []finder.Option{finder.Exact()},
		},
		"exact matching - match covers a trailing combining mark": {
			input:  "name: cafe\u0301",
			search: "cafe",
			opts:   []finder.Option{finder.Exact()},
			want:   position.Ranges{yamltest.LineRange(0, 6, 11)},
		},
		"nil fold keeps the default folds": {
			input:  "key: VALUE",
			search: "value",
			opts:   []finder.Option{finder.WithFold(nil)},
			want:   position.Ranges{yamltest.LineRange(0, 5, 10)},
		},
		"case fold off keeps case": {
			input:  "name: Café",
			search: "cafe",
			opts:   []finder.Option{finder.WithCaseFold(false)},
		},
		"case fold off still ignores diacritics": {
			input:  "name: Café",
			search: "Cafe",
			opts:   []finder.Option{finder.WithCaseFold(false)},
			want:   position.Ranges{yamltest.LineRange(0, 6, 10)},
		},
		"diacritic fold off keeps diacritics": {
			input:  "name: Café",
			search: "cafe",
			opts:   []finder.Option{finder.WithDiacriticFold(false)},
		},
		"diacritic fold off - katakana precomposed source, decomposed search": {
			input:  "name: ガ",
			search: "ガ",
			opts:   []finder.Option{finder.WithDiacriticFold(false)},
		},
		"diacritic fold off - katakana decomposed source, precomposed search": {
			input:  "name: ガ",
			search: "ガ",
			opts:   []finder.Option{finder.WithDiacriticFold(false)},
		},
		"diacritic fold off - latin precomposed source, decomposed search": {
			input:  "name: café",
			search: "café",
			opts:   []finder.Option{finder.WithDiacriticFold(false)},
		},
		"diacritic fold off still folds case": {
			input:  "name: Café",
			search: "CAFÉ",
			opts:   []finder.Option{finder.WithDiacriticFold(false)},
			want:   position.Ranges{yamltest.LineRange(0, 6, 10)},
		},
		"case fold after exact folds case": {
			input:  "name: Café",
			search: "CAFÉ",
			opts:   []finder.Option{finder.Exact(), finder.WithCaseFold(true)},
			want:   position.Ranges{yamltest.LineRange(0, 6, 10)},
		},
		"case fold after exact keeps diacritics": {
			input:  "name: Café",
			search: "cafe",
			opts:   []finder.Option{finder.Exact(), finder.WithCaseFold(true)},
		},
		"exact after case fold folds nothing": {
			input:  "name: Café",
			search: "CAFÉ",
			opts:   []finder.Option{finder.WithCaseFold(true), finder.Exact()},
		},
		"exact turns width fold off": {
			input:  "k: ａｂｃ",
			search: "abc",
			opts:   []finder.Option{finder.WithWidthFold(true), finder.Exact()},
		},
		"fold runs after the default folds": {
			input:  "key:    Value",
			search: "KEY:value",
			opts:   []finder.Option{finder.WithFold(noSpace)},
			want:   position.Ranges{yamltest.LineRange(0, 0, 13)},
		},
		// The fold maps only the lowercase "a", so the search "A" reaches it
		// as "a" only when case folding runs first.
		"fold receives the case-folded character": {
			input:  "k: b",
			search: "A",
			opts:   []finder.Option{finder.WithFold(strings.NewReplacer("a", "b").Replace)},
			want:   position.Ranges{yamltest.LineRange(0, 3, 4)},
		},
		// Case folding expands "ß" to "ss", and the fold receives that whole
		// expansion as one character.
		"fold receives the expansion of one character": {
			input:  "x: ß",
			search: "z",
			opts: []finder.Option{finder.WithFold(func(char string) string {
				if char == "ss" {
					return "z"
				}

				return char
			})},
			want: position.Ranges{yamltest.LineRange(0, 3, 4)},
		},
		"fold with exact runs alone": {
			input:  "key: VALUE",
			search: "value",
			opts:   []finder.Option{finder.Exact(), finder.WithFold(strings.ToLower)},
			want:   position.Ranges{yamltest.LineRange(0, 5, 10)},
		},
		"folds run in the order they were added": {
			input:  "k: z",
			search: "x",
			opts: []finder.Option{
				finder.WithFold(strings.NewReplacer("x", "y").Replace),
				finder.WithFold(strings.NewReplacer("y", "z").Replace),
			},
			want: position.Ranges{yamltest.LineRange(0, 3, 4)},
		},
		// The default folds drop a lone combining mark, and a fold never
		// receives an empty string.
		"fold does not receive a character the default folds drop": {
			input:  "key: value\n",
			search: "\u0301",
			opts: []finder.Option{finder.WithFold(func(char string) string {
				if char == "" {
					panic("fold received an empty character")
				}

				return char
			})},
		},
		"fold dropping whitespace joins lines": {
			input:  "a: 1\nb: 2\n",
			search: "1b",
			opts:   []finder.Option{finder.WithFold(noSpace)},
			want:   position.Ranges{position.NewRange(position.New(0, 3), position.New(1, 1))},
		},
		"fold dropping whitespace covers the dropped space": {
			input:  "k: ab cd",
			search: "ab",
			opts:   []finder.Option{finder.WithFold(noSpace)},
			want:   position.Ranges{yamltest.LineRange(0, 3, 6)},
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
		"utf8 - default folds find diacritic as ascii": {
			input:  "key: über öffentlich",
			search: "o",
			want:   position.Ranges{yamltest.LineRange(0, 10, 11)},
		},
		"utf8 - default folds combine case and diacritics": {
			input:  "name: THAÏS test",
			search: "thais",
			want:   position.Ranges{yamltest.LineRange(0, 6, 11)},
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
			input:  `text: "[not] {a} list"`,
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
			input:  stringtest.LinesCRLF("a: 1", "b: 2"),
			search: "1\r\nb",
			want:   position.Ranges{position.NewRange(position.New(0, 3), position.New(1, 1))},
		},
		"CRLF source - LF search": {
			input:  stringtest.LinesCRLF("a: 1", "b: 2"),
			search: "1\nb",
			want:   position.Ranges{position.NewRange(position.New(0, 3), position.New(1, 1))},
		},
		// A search of only combining marks folds to an empty string, which
		// must yield no matches rather than match at every offset.
		"search that folds to empty": {
			input:  "key: value\n",
			search: "\u0301",
		},
		// The accent is a combining mark that belongs to the "e" before it, and
		// the default folds drop it. A match ending at that "e" covers the accent,
		// as a match ending inside an expansion covers the whole character, so
		// the highlight ends where the next character begins.
		"match ending at a dropped combining mark": {
			input:  "k: cafe\u0301x\n",
			search: "caf\u00e9",
			want:   position.Ranges{yamltest.LineRange(0, 3, 8)},
		},
		"match starting at a character with a dropped combining mark": {
			input:  "k: cafe\u0301x\n",
			search: "ex",
			want:   position.Ranges{yamltest.LineRange(0, 6, 9)},
		},
		// Title casing would produce "Abc" for a whole string but "ABC" rune
		// by rune. Both sides go through the fold the same way, so the
		// search still finds the text.
		"context-sensitive fold - lowercase search": {
			input:  "k: abc\n",
			search: "abc",
			opts:   []finder.Option{finder.Exact(), finder.WithFold(titleCase)},
			want:   position.Ranges{yamltest.LineRange(0, 3, 6)},
		},
		"context-sensitive fold - uppercase search": {
			input:  "k: abc\n",
			search: "ABC",
			opts:   []finder.Option{finder.Exact(), finder.WithFold(titleCase)},
			want:   position.Ranges{yamltest.LineRange(0, 3, 6)},
		},
		// Case folding turns one source rune into two folded chars, so a
		// one-char needle could match twice inside it and report the same range
		// twice. Only the match that begins at the source rune counts.
		"needle inside an expansion matches once": {
			input:  "x: ß",
			search: "s",
			want:   position.Ranges{yamltest.LineRange(0, 3, 4)},
		},
		"needle covering the expansion": {
			input:  "x: ß",
			search: "ss",
			want:   position.Ranges{yamltest.LineRange(0, 3, 4)},
		},
		"needle ending inside an expansion covers the rune": {
			input:  "x: aß",
			search: "as",
			want:   position.Ranges{yamltest.LineRange(0, 3, 5)},
		},
		"needle starting inside an expansion does not match": {
			input:  "x: ßa",
			search: "sa",
		},
		"expansion next to a plain match": {
			input:  "x: ßs",
			search: "s",
			want:   position.Ranges{yamltest.LineRange(0, 3, 4), yamltest.LineRange(0, 4, 5)},
		},
		"multibyte character between expansions": {
			input:  "x: ß日 ß",
			search: "ss",
			want:   position.Ranges{yamltest.LineRange(0, 3, 4), yamltest.LineRange(0, 6, 7)},
		},
		// A stray continuation byte must not match inside a multi-byte rune and
		// then report the position of an unrelated rune.
		"continuation byte with exact matching": {
			input:  "x: a\u00e9",
			search: "\xa9",
			opts:   []finder.Option{finder.Exact()},
		},
		"continuation byte with the default folds": {
			input:  "x: a\u00e9",
			search: "\xa9",
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
			opts:   []finder.Option{finder.Exact()},
			want:   position.Ranges{yamltest.LineRange(0, 4, 5)},
		},
		"U+FFFD needle with exact matching": {
			input:  "a: x\xfey\n",
			search: "\ufffd",
			opts:   []finder.Option{finder.Exact()},
			want:   position.Ranges{yamltest.LineRange(0, 4, 5)},
		},
		"fold emitting an invalid byte": {
			// The needle and the index both pass through the fold, so an
			// invalid byte it emits must read the same on both sides.
			input:  "a: x",
			search: "x",
			opts: []finder.Option{finder.Exact(), finder.WithFold(func(char string) string {
				if char == "x" {
					return "\xff"
				}

				return char
			})},
			want: position.Ranges{yamltest.LineRange(0, 3, 4)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := niceyaml.NewSourceFromString(tc.input).Lines()
			assert.Equal(t, tc.want, finder.New(tc.opts...).Load(lines).Find(tc.search))
		})
	}
}

func TestFinder_Load(t *testing.T) {
	t.Parallel()

	// The first document holds lines 0 and 1 of the file, and the second
	// holds lines 2 through 4.
	source := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"b: 2",
		"---",
		"name: x",
		"port: 80",
	))
	docs := yamltest.Documents(t, source, 2)

	// The view skips lines 1 and 2.
	gapped := source.View().Slice(position.NewSpan(0, 1), position.NewSpan(3, 5))

	// The view skips line 1, so "a\na" matches on lines 2 and 3 alone.
	repeated := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a",
		"z",
		"a",
		"a",
	)).View().
		Slice(position.NewSpan(0, 1), position.NewSpan(2, 4))

	// The unified diff holds nine rows, and its hunks hold rows 0 and 1,
	// where k1 changed, and rows 7 and 8, where k7 changed.
	hunks := yamltest.Diff(
		stringtest.LinesLF(
			"k1: a",
			"k2: b",
			"k3: c",
			"k4: d",
			"k5: e",
			"k6: f",
			"k7: g",
		),
		stringtest.LinesLF(
			"k1: A",
			"k2: b",
			"k3: c",
			"k4: d",
			"k5: e",
			"k6: f",
			"k7: G",
		),
	).Hunks(0)
	require.Equal(t, 4, hunks.Count())

	// The host shares line 2 of the file with the port beside it.
	flow := yamltest.Documents(t, niceyaml.NewSourceFromString(stringtest.LinesLF(
		"top: 1",
		"---",
		"server: {host: h, port: 80}",
		"port: 1",
	)), 2)

	host := yamltest.At(t, flow[1], paths.Doc().Child("server", "host"))

	// The unified diff holds "key: old" on row 0 and "key: new" on row 1.
	// Both lines are line 1 of their revision, and a match names the row.
	unified := yamltest.Diff("key: old\n", "key: new\n").Unified()

	// A diff view can hold a line with no line ending before another line,
	// such as the last line of a revision or a placeholder row. The loaded
	// text still ends every line but the last with "\n", so no match joins
	// the text of two lines.
	noEnding := yamltest.Diff("x: 1", "x: 2\n")
	placeholders := yamltest.Diff(
		stringtest.LinesLF("a: 1", "b: 2"),
		stringtest.LinesLF(
			"a: 1",
			"n: 1",
			"m: 1",
			"b: 2",
		),
	)

	tcs := map[string]struct {
		lines  line.Sequence
		opts   []finder.Option
		search string
		want   position.Ranges
	}{
		"zero lines":           {lines: line.Lines{}, search: "test"},
		"nil sequence":         {lines: nil, search: "test"},
		"nil view":             {lines: (*line.View)(nil), search: "test"},
		"nil pointer to lines": {lines: (*line.Lines)(nil), search: "test"},
		"view that holds no line": {
			lines:  niceyaml.NewSourceFromString("test: 1\n").View().Slice(position.Span{}),
			search: "test",
		},
		"view of every line matches as its lines do": {
			lines:  source.View(),
			search: "2\n---",
			want:   position.Ranges{position.NewRange(position.New(1, 3), position.New(2, 3))},
		},
		"document reports the lines of the file": {
			lines:  docs[1].View(),
			search: "port",
			want:   position.Ranges{yamltest.LineRange(4, 0, 4)},
		},
		"line the view does not hold": {
			lines:  docs[1].View(),
			search: "a: 1",
		},
		"match ends with the last held line": {
			lines:  docs[0].View(),
			search: "2\n",
			want:   position.Ranges{yamltest.LineRange(1, 3, 5)},
		},
		"match does not run past the last held line": {
			lines:  docs[0].View(),
			search: "2\n---",
		},
		"match does not start above the first held line": {
			lines:  docs[1].View(),
			search: "2\n---",
		},
		"match spans held lines in a row": {
			lines:  gapped,
			search: "x\nport",
			want:   position.Ranges{position.NewRange(position.New(3, 6), position.New(4, 4))},
		},
		"match does not cross a gap": {
			lines:  gapped,
			search: "1\nname",
		},
		"fold dropping whitespace joins held lines in a row": {
			lines:  gapped,
			opts:   []finder.Option{finder.WithFold(noSpace)},
			search: "xport",
			want:   position.Ranges{position.NewRange(position.New(3, 6), position.New(4, 4))},
		},
		"fold dropping whitespace does not join text across a gap": {
			lines:  gapped,
			opts:   []finder.Option{finder.WithFold(noSpace)},
			search: "1name",
		},
		"match that overlaps one crossing a gap": {
			lines:  repeated,
			search: "a\na",
			want:   position.Ranges{position.NewRange(position.New(2, 0), position.New(3, 1))},
		},
		"hunks report the rows of the unified diff": {
			lines:  hunks,
			search: "k7",
			want:   position.Ranges{yamltest.LineRange(7, 0, 2), yamltest.LineRange(8, 0, 2)},
		},
		"unified diff reports the row of a deleted line": {
			lines:  unified,
			search: "old",
			want:   position.Ranges{yamltest.LineRange(0, 5, 8)},
		},
		"unified diff reports the row of an inserted line": {
			lines:  unified,
			search: "new",
			want:   position.Ranges{yamltest.LineRange(1, 5, 8)},
		},
		"unified diff reports both rows of a change": {
			lines:  unified,
			search: "key",
			want:   position.Ranges{yamltest.LineRange(0, 0, 3), yamltest.LineRange(1, 0, 3)},
		},
		"match spans the rows of one hunk": {
			lines:  hunks,
			search: "a\nk1",
			want:   position.Ranges{position.NewRange(position.New(0, 4), position.New(1, 2))},
		},
		"match does not run from one hunk into the next": {
			lines:  hunks,
			search: "A\nk7",
		},
		"node holds the whole of its line": {
			lines:  host.View(),
			search: "port",
			want:   position.Ranges{yamltest.LineRange(2, 18, 22)},
		},
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

			got := finder.New(tc.opts...).Load(tc.lines).Find(tc.search)

			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("matches mark the view they came from", func(t *testing.T) {
		t.Parallel()

		// The matches of a view are in the coordinates of its content.
		view := docs[1].View()
		view.AddOverlay(kind.GenericHighlight, finder.New().Load(view).Find("port")...)

		assert.Equal(t, line.Overlays{{Kind: kind.GenericHighlight, Cols: position.NewSpan(0, 4)}}, view.Overlays(4))
	})
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

func TestFinder_ZeroValue(t *testing.T) {
	t.Parallel()

	// A zero Finder folds case and ignores diacritics, as one from New
	// without options does.
	lines := niceyaml.NewSourceFromString("name: Café").Lines()
	want := position.Ranges{yamltest.LineRange(0, 6, 10)}

	assert.Equal(t, want, new(finder.Finder).Load(lines).Find("CAFE"))
	assert.Equal(t, want, finder.New().Load(lines).Find("CAFE"))
}

func TestIndex_Find_Concurrent(t *testing.T) {
	t.Parallel()

	// The default folds are safe for concurrent use, so goroutines that
	// share one Index get the ranges a single goroutine gets.
	source := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"name: Café",
		"street: Straße",
		"note: café au lait",
	))
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
