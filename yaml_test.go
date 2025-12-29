package niceyaml_test

import (
	"os"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
)

// testBracketStyle returns a style that wraps content in brackets for verification.
func testBracketStyle() *lipgloss.Style {
	style := lipgloss.NewStyle().Transform(func(s string) string {
		return "[" + s + "]"
	})

	return &style
}

// testBasicPrinter returns a Printer configured for testing.
func testBasicPrinter() *niceyaml.Printer {
	return niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithGutter(niceyaml.NoGutter),
	)
}

// testFinder returns a Finder configured for testing.
func testFinder(search string, normalizer niceyaml.Normalizer) *niceyaml.Finder {
	var opts []niceyaml.FinderOption
	if normalizer != nil {
		opts = append(opts, niceyaml.WithNormalizer(normalizer))
	}

	return niceyaml.NewFinder(search, opts...)
}

func TestFinderPrinter_Integration(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input      string
		search     string
		normalizer niceyaml.Normalizer
		want       string
	}{
		"simple match": {
			input:  "key: value",
			search: "value",
			want:   "key: [value]",
		},
		"single character in word": {
			input:  "foobar",
			search: "o",
			// Adjacent styled ranges naturally merge in the Printer.
			want: "f[oo]bar",
		},
		"multiple matches same line": {
			input:  "key: abcabc",
			search: "abc",
			// Adjacent styled ranges naturally merge in the Printer.
			want: "key: [abcabc]",
		},
		"match at start": {
			input:  "key: value",
			search: "key",
			want:   "[key]: value",
		},
		"match spanning tokens": {
			input:  "key: value",
			search: ": v",
			want:   "key[:][ ][v]alue",
		},
		"multi-line matches": {
			input:  "a: test\nb: test",
			search: "test",
			want:   "a: [test]\nb: [test]",
		},
		"utf8 - search after multibyte char": {
			input:  "name: Thaïs test",
			search: "test",
			want:   "name: Thaïs [test]",
		},
		"utf8 - search for multibyte char": {
			input:  "name: Thaïs",
			search: "ï",
			want:   "name: Tha[ï]s",
		},
		"utf8 - search spanning multibyte": {
			input:  "name: Thaïs",
			search: "ïs",
			want:   "name: Tha[ïs]",
		},
		"utf8 - multiple multibyte chars": {
			input:  "key: über öffentlich",
			search: "ö",
			want:   "key: über [ö]ffentlich",
		},
		"utf8 - text after multiple multibyte": {
			input:  "key: über öffentlich test",
			search: "test",
			want:   "key: über öffentlich [test]",
		},
		"utf8 - normalizer diacritic to ascii": {
			input:      "name: Thaïs",
			search:     "Thais",
			normalizer: niceyaml.StandardNormalizer{},
			want:       "name: [Thaïs]",
		},
		"utf8 - case insensitive with diacritics": {
			input:  "name: THAÏS test",
			search: "thais",
			normalizer: testNormalizer{fn: func(s string) string {
				return strings.ToLower(niceyaml.StandardNormalizer{}.Normalize(s))
			}},
			want: "name: [THAÏS] test",
		},
		"utf8 - search ascii finds normalized diacritic": {
			input:      "key: über",
			search:     "u",
			normalizer: niceyaml.StandardNormalizer{},
			want:       "key: [ü]ber",
		},
		"utf8 - normalizer search after multiple multibyte": {
			input:      "key: über Yamüll test",
			search:     "ya",
			normalizer: niceyaml.StandardNormalizer{},
			want:       "key: über [Ya]müll test",
		},
		"utf8 - japanese characters": {
			input:  "名前: テスト value",
			search: "value",
			want:   "名前: テスト [value]",
		},
		"utf8 - emoji": {
			input:  "status: ✓ done",
			search: "done",
			want:   "status: ✓ [done]",
		},
		"complex yaml with utf8": {
			input:  "metadata:\n  name: Thaïs\n  namespace: über",
			search: "name",
			want:   "metadata:\n  [name]: Thaïs\n  [name]space: über",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := niceyaml.NewSourceFromString(tc.input)
			finder := testFinder(tc.search, tc.normalizer)
			printer := testBasicPrinter()

			ranges := finder.Find(lines)
			for _, rng := range ranges {
				printer.AddStyleToRange(testBracketStyle(), rng)
			}

			got := printer.Print(lines)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinderPrinter_EdgeCases(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input      string
		search     string
		wantOutput string
		wantRanges bool
	}{
		"no match": {
			input:      "key: value",
			search:     "notfound",
			wantRanges: false,
			wantOutput: "key: value",
		},
		"empty search": {
			input:      "key: value",
			search:     "",
			wantRanges: false,
			wantOutput: "key: value",
		},
		"empty file": {
			input:      "",
			search:     "test",
			wantRanges: false,
			wantOutput: "",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := niceyaml.NewSourceFromString(tc.input)
			finder := testFinder(tc.search, nil)
			printer := testBasicPrinter()

			ranges := finder.Find(lines)
			if tc.wantRanges {
				assert.NotEmpty(t, ranges)
			} else {
				assert.Empty(t, ranges)
			}

			got := printer.Print(lines)
			assert.Equal(t, tc.wantOutput, got)
		})
	}
}

func TestPrinter_Golden(t *testing.T) {
	t.Parallel()

	type goldenTest struct {
		setupFunc func(*niceyaml.Printer, *niceyaml.Source)
		opts      []niceyaml.PrinterOption
	}

	tcs := map[string]goldenTest{
		"default colors": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.DefaultStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter),
			},
		},
		"default colors with line numbers": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.DefaultStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
			},
		},
		"no colors": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter),
			},
		},
		"no colors with line numbers": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
			},
		},
		"find and highlight": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.DefaultStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter),
			},
			setupFunc: func(p *niceyaml.Printer, lines *niceyaml.Source) {
				// Search for "日本" (Japan) which appears multiple times in full.yaml.
				finder := niceyaml.NewFinder("日本")
				highlightStyle := lipgloss.NewStyle().
					Background(lipgloss.Color("#FFFF00")).
					Foreground(lipgloss.Color("#000000"))

				ranges := finder.Find(lines)
				for _, rng := range ranges {
					p.AddStyleToRange(&highlightStyle, rng)
				}
			},
		},
	}

	input, err := os.ReadFile("testdata/full.yaml")
	require.NoError(t, err)

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := niceyaml.NewSourceFromString(string(input))
			printer := niceyaml.NewPrinter(tc.opts...)

			if tc.setupFunc != nil {
				tc.setupFunc(printer, lines)
			}

			output := printer.Print(lines)
			golden.RequireEqual(t, output)
		})
	}
}

func TestFinderPrinter_JapaneseMatch(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input  string
		search string
		want   string
	}{
		"japanese partial match": {
			input:  "key: 日本酒",
			search: "日本",
			want:   "key: [日本]酒",
		},
		"japanese after other japanese": {
			input:  "- 寿司: 日本酒",
			search: "日本",
			want:   "- 寿司: [日本]酒",
		},
		"multiline with japanese": {
			input:  "a: test\n- 寿司: 日本酒",
			search: "日本",
			want:   "a: test\n- 寿司: [日本]酒",
		},
		"multiple japanese on different lines": {
			input:  "a: 日本\nb: 日本酒",
			search: "日本",
			want:   "a: [日本]\nb: [日本]酒",
		},
		"box drawing not matched": {
			input:  "# ─────",
			search: "日本",
			want:   "# ─────",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := niceyaml.NewSourceFromString(tc.input)
			finder := testFinder(tc.search, nil)
			printer := testBasicPrinter()

			ranges := finder.Find(lines)
			for _, rng := range ranges {
				printer.AddStyleToRange(testBracketStyle(), rng)
			}

			got := printer.Print(lines)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinderPrinter_LargeDocument(t *testing.T) {
	t.Parallel()

	// Simulate the failing scenario: Japanese text followed by box drawing.
	input := `# ─────────────────────────
menu:
  - 寿司: 日本酒
# ─────────────────────────`

	lines := niceyaml.NewSourceFromString(input)
	finder := testFinder("日本", nil)
	printer := testBasicPrinter()

	ranges := finder.Find(lines)
	require.Len(t, ranges, 1, "should find exactly one match")

	// Verify the range is on line 2 (0-indexed, which is the 3rd line).
	assert.Equal(t, 2, ranges[0].Start.Line, "match should be on line 2 (0-indexed)")
	assert.Equal(t, 2, ranges[0].End.Line, "match end should be on line 2 (0-indexed)")

	for _, rng := range ranges {
		printer.AddStyleToRange(testBracketStyle(), rng)
	}

	got := printer.Print(lines)

	// The box drawing characters on lines 1 and 4 should NOT be highlighted
	// Only "日本" on line 3 should be highlighted.
	want := `# ─────────────────────────
menu:
  - 寿司: [日本]酒
# ─────────────────────────`

	assert.Equal(t, want, got)
}

func TestFinderPrinter_BoxDrawingNotMatched(t *testing.T) {
	t.Parallel()

	// Test that box drawing characters in comments aren't matched when searching for Japanese text.
	input := `menu:
  - 寿司: 日本酒
# ┌─────────────────────────────────────────────────────────────┐
# │  SPECIAL SECTION                                             │
# └─────────────────────────────────────────────────────────────┘`

	lines := niceyaml.NewSourceFromString(input)
	finder := testFinder("日本", nil)
	printer := testBasicPrinter()

	ranges := finder.Find(lines)
	require.Len(t, ranges, 1, "should find exactly one match")

	for _, rng := range ranges {
		printer.AddStyleToRange(testBracketStyle(), rng)
	}

	got := printer.Print(lines)

	// Verify the match is properly bracketed.
	assert.Contains(t, got, "[日本]酒", "日本 should be highlighted")
	assert.NotContains(t, got, "[─", "box drawing should not be highlighted")
	assert.NotContains(t, got, "─]", "box drawing should not be highlighted")
}

func TestPositionRange_Contains(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		rng  niceyaml.PositionRange
		pos  niceyaml.Position
		want bool
	}{
		"within single line range": {
			rng: niceyaml.NewPositionRange(
				niceyaml.NewPosition(0, 5),
				niceyaml.NewPosition(0, 10),
			),
			pos:  niceyaml.NewPosition(0, 7),
			want: true,
		},
		"at start (inclusive)": {
			rng: niceyaml.NewPositionRange(
				niceyaml.NewPosition(0, 5),
				niceyaml.NewPosition(0, 10),
			),
			pos:  niceyaml.NewPosition(0, 5),
			want: true,
		},
		"at end (exclusive)": {
			rng: niceyaml.NewPositionRange(
				niceyaml.NewPosition(0, 5),
				niceyaml.NewPosition(0, 10),
			),
			pos:  niceyaml.NewPosition(0, 10),
			want: false,
		},
		"before start": {
			rng: niceyaml.NewPositionRange(
				niceyaml.NewPosition(0, 5),
				niceyaml.NewPosition(0, 10),
			),
			pos:  niceyaml.NewPosition(0, 4),
			want: false,
		},
		"multi-line range - middle line": {
			rng: niceyaml.NewPositionRange(
				niceyaml.NewPosition(1, 5),
				niceyaml.NewPosition(3, 10),
			),
			pos:  niceyaml.NewPosition(2, 0),
			want: true,
		},
		"first line (0-indexed)": {
			rng: niceyaml.NewPositionRange(
				niceyaml.NewPosition(0, 0),
				niceyaml.NewPosition(0, 5),
			),
			pos:  niceyaml.NewPosition(0, 0),
			want: true,
		},
		"after end line": {
			rng: niceyaml.NewPositionRange(
				niceyaml.NewPosition(0, 0),
				niceyaml.NewPosition(1, 5),
			),
			pos:  niceyaml.NewPosition(2, 0),
			want: false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.rng.Contains(tc.pos)
			assert.Equal(t, tc.want, got)
		})
	}
}
