package niceyaml_test

import (
	"os"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
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

// testParseFile parses tokens into an ast.File for testing PrintFile.
func testParseFile(t *testing.T, tokens token.Tokens) *ast.File {
	t.Helper()

	file, err := parser.Parse(tokens, 0)
	require.NoError(t, err)

	return file
}

// testBasicPrinter returns a Printer configured for testing.
func testBasicPrinter() *niceyaml.Printer {
	return niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithLinePrefix(""),
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
			finder := testFinder(tc.search, tc.normalizer)
			printer := testBasicPrinter()

			ranges := finder.FindTokens(tokens)
			for _, rng := range ranges {
				printer.AddStyleToRange(testBracketStyle(), rng)
			}

			got := printer.PrintTokens(tokens)
			assert.Equal(t, tc.want, got)

			file := testParseFile(t, tokens)
			gotFile := printer.PrintFile(file)
			assert.Equal(t, got, gotFile)
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

			tokens := lexer.Tokenize(tc.input)
			finder := testFinder(tc.search, nil)
			printer := testBasicPrinter()

			ranges := finder.FindTokens(tokens)
			if tc.wantRanges {
				assert.NotEmpty(t, ranges)
			} else {
				assert.Empty(t, ranges)
			}

			got := printer.PrintTokens(tokens)
			assert.Equal(t, tc.wantOutput, got)

			file := testParseFile(t, tokens)
			gotFile := printer.PrintFile(file)
			assert.Equal(t, got, gotFile)
		})
	}
}

func TestPrinter_Golden(t *testing.T) {
	t.Parallel()

	type goldenTest struct {
		setupFunc func(*niceyaml.Printer, token.Tokens)
		opts      []niceyaml.PrinterOption
	}

	tcs := map[string]goldenTest{
		"DefaultColors": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.DefaultStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithLinePrefix(""),
			},
		},
		"LineNumbers": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.DefaultStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithLineNumbers(),
			},
		},
		"NoColors": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithLinePrefix(""),
			},
		},
		"SearchHighlight": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.DefaultStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithLinePrefix(""),
			},
			setupFunc: func(p *niceyaml.Printer, tokens token.Tokens) {
				// Search for "日本" (Japan) which appears multiple times in full.yaml.
				finder := niceyaml.NewFinder("日本")
				highlightStyle := lipgloss.NewStyle().
					Background(lipgloss.Color("#FFFF00")).
					Foreground(lipgloss.Color("#000000"))

				ranges := finder.FindTokens(tokens)
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

			tokens := lexer.Tokenize(string(input))
			printer := niceyaml.NewPrinter(tc.opts...)

			if tc.setupFunc != nil {
				tc.setupFunc(printer, tokens)
			}

			output := printer.PrintTokens(tokens)
			golden.RequireEqual(t, output)

			file := testParseFile(t, tokens)
			outputFile := printer.PrintFile(file)
			assert.Equal(t, output, outputFile)
		})
	}
}

func TestNewPositionTrackerFromTokens(t *testing.T) {
	t.Parallel()

	t.Run("empty tokens returns position 1,1", func(t *testing.T) {
		t.Parallel()

		tracker := niceyaml.NewPositionTrackerFromTokens(nil)
		pos := tracker.Position()
		assert.Equal(t, niceyaml.Position{Line: 1, Col: 1}, pos)
	})

	t.Run("with tokens returns first token position", func(t *testing.T) {
		t.Parallel()

		tokens := lexer.Tokenize("key: value")
		require.NotEmpty(t, tokens)

		tracker := niceyaml.NewPositionTrackerFromTokens(tokens)
		pos := tracker.Position()
		assert.Equal(t, 1, pos.Line)
		assert.Equal(t, 1, pos.Col)
	})
}
