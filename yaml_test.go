package niceyaml_test

import (
	"os"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/goccy/go-yaml/lexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
)

// testBracketStyle returns a style that wraps content in brackets for verification.
func testBracketStyle() lipgloss.Style {
	return lipgloss.NewStyle().Transform(func(s string) string {
		return "[" + s + "]"
	})
}

// testFinderPrinter returns a Finder and Printer configured for testing.
func testFinderPrinter(normalizer func(string) string) (*niceyaml.Finder, *niceyaml.Printer) {
	var opts []niceyaml.FinderOption
	if normalizer != nil {
		opts = append(opts, niceyaml.WithNormalizer(normalizer))
	}

	finder := niceyaml.NewFinder(opts...)
	printer := niceyaml.NewPrinter(
		niceyaml.WithColorScheme(niceyaml.ColorScheme{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithLinePrefix(""),
	)

	return finder, printer
}

func TestFinderPrinter_Integration(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input      string
		search     string
		normalizer func(string) string
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
			normalizer: niceyaml.StandardNormalizer,
			want:       "name: [Thaïs]",
		},
		"utf8 - case insensitive with diacritics": {
			input:  "name: THAÏS test",
			search: "thais",
			normalizer: func(s string) string {
				return strings.ToLower(niceyaml.StandardNormalizer(s))
			},
			want: "name: [THAÏS] test",
		},
		"utf8 - search ascii finds normalized diacritic": {
			input:      "key: über",
			search:     "u",
			normalizer: niceyaml.StandardNormalizer,
			want:       "key: [ü]ber",
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

			tokens := lexer.Tokenize(tc.input)
			finder, printer := testFinderPrinter(tc.normalizer)

			ranges := finder.FindStringsInTokens(tc.search, tokens)
			for _, rng := range ranges {
				printer.AddStyleToRange(testBracketStyle(), rng)
			}

			got := printer.PrintTokens(tokens)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinderPrinter_NoMatch(t *testing.T) {
	t.Parallel()

	tokens := lexer.Tokenize("key: value")
	finder, printer := testFinderPrinter(nil)

	ranges := finder.FindStringsInTokens("notfound", tokens)
	assert.Empty(t, ranges)

	// No styles applied, output unchanged.
	got := printer.PrintTokens(tokens)
	assert.Equal(t, "key: value", got)
}

func TestFinderPrinter_EmptySearch(t *testing.T) {
	t.Parallel()

	tokens := lexer.Tokenize("key: value")
	finder, printer := testFinderPrinter(nil)

	ranges := finder.FindStringsInTokens("", tokens)
	assert.Empty(t, ranges)

	got := printer.PrintTokens(tokens)
	assert.Equal(t, "key: value", got)
}

func TestPrinter_Golden_DefaultColors(t *testing.T) {
	t.Parallel()

	input, err := os.ReadFile("testdata/full.yaml")
	require.NoError(t, err)

	tokens := lexer.Tokenize(string(input))
	printer := niceyaml.NewPrinter(
		niceyaml.WithColorScheme(niceyaml.DefaultColorScheme()),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithLinePrefix(""),
	)

	output := printer.PrintTokens(tokens)
	golden.RequireEqual(t, output)
}

func TestPrinter_Golden_LineNumbers(t *testing.T) {
	t.Parallel()

	input, err := os.ReadFile("testdata/full.yaml")
	require.NoError(t, err)

	tokens := lexer.Tokenize(string(input))
	printer := niceyaml.NewPrinter(
		niceyaml.WithColorScheme(niceyaml.DefaultColorScheme()),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithLineNumbers(),
	)

	output := printer.PrintTokens(tokens)
	golden.RequireEqual(t, output)
}

func TestPrinter_Golden_NoColors(t *testing.T) {
	t.Parallel()

	input, err := os.ReadFile("testdata/full.yaml")
	require.NoError(t, err)

	tokens := lexer.Tokenize(string(input))
	printer := niceyaml.NewPrinter(
		niceyaml.WithColorScheme(niceyaml.ColorScheme{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithLinePrefix(""),
	)

	output := printer.PrintTokens(tokens)
	golden.RequireEqual(t, output)
}

func TestPrinter_Golden_SearchHighlight(t *testing.T) {
	t.Parallel()

	input, err := os.ReadFile("testdata/full.yaml")
	require.NoError(t, err)

	tokens := lexer.Tokenize(string(input))

	finder := niceyaml.NewFinder()
	printer := niceyaml.NewPrinter(
		niceyaml.WithColorScheme(niceyaml.DefaultColorScheme()),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithLinePrefix(""),
	)

	// Search for "日本" (Japan) which appears multiple times in full.yaml.
	highlightStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#FFFF00")).
		Foreground(lipgloss.Color("#000000"))

	ranges := finder.FindStringsInTokens("日本", tokens)
	for _, rng := range ranges {
		printer.AddStyleToRange(highlightStyle, rng)
	}

	output := printer.PrintTokens(tokens)
	golden.RequireEqual(t, output)
}
