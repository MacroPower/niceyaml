package niceyaml_test

import (
	"strings"
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
)

func TestFinder_FindStringsInTokens(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input      string
		search     string
		normalizer func(string) string
		want       []niceyaml.PositionRange
	}{
		"single token match": {
			input:  "key: value",
			search: "value",
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 6}, End: niceyaml.Position{Line: 1, Col: 11}},
			},
		},
		"match key": {
			input:  "key: value",
			search: "key",
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 1}, End: niceyaml.Position{Line: 1, Col: 4}},
			},
		},
		"cross-token match": {
			input:  "key: value",
			search: ": ",
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 4}, End: niceyaml.Position{Line: 1, Col: 6}},
			},
		},
		"multiple matches": {
			input:  "a: test\nb: test\nc: test",
			search: "test",
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 4}, End: niceyaml.Position{Line: 1, Col: 8}},
				{Start: niceyaml.Position{Line: 2, Col: 4}, End: niceyaml.Position{Line: 2, Col: 8}},
				{Start: niceyaml.Position{Line: 3, Col: 4}, End: niceyaml.Position{Line: 3, Col: 8}},
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
				{Start: niceyaml.Position{Line: 3, Col: 3}, End: niceyaml.Position{Line: 3, Col: 8}},
			},
		},
		"match spans lines": {
			input:  "a: 1\nb: 2",
			search: "1\nb",
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 4}, End: niceyaml.Position{Line: 2, Col: 2}},
			},
		},
		"with normalizer - diacritic match": {
			input:      "name: Thaïs",
			search:     "Thais",
			normalizer: niceyaml.StandardNormalizer,
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 7}, End: niceyaml.Position{Line: 1, Col: 12}},
			},
		},
		"with normalizer - search has diacritic": {
			input:      "name: Thais",
			search:     "Thaïs",
			normalizer: niceyaml.StandardNormalizer,
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 7}, End: niceyaml.Position{Line: 1, Col: 12}},
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
			normalizer: strings.ToLower,
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 6}, End: niceyaml.Position{Line: 1, Col: 11}},
			},
		},
		"single character match": {
			input:  "a: b",
			search: "a",
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 1}, End: niceyaml.Position{Line: 1, Col: 2}},
			},
		},
		"overlapping potential matches": {
			input:  "aaa",
			search: "aa",
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 1}, End: niceyaml.Position{Line: 1, Col: 3}},
			},
		},
		"utf8 - search text after multibyte char": {
			input:  "name: Thaïs test",
			search: "test",
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 13}, End: niceyaml.Position{Line: 1, Col: 17}},
			},
		},
		"utf8 - search for multibyte char": {
			input:  "name: Thaïs",
			search: "ï",
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 10}, End: niceyaml.Position{Line: 1, Col: 11}},
			},
		},
		"utf8 - search spanning multibyte char": {
			input:  "name: Thaïs",
			search: "ïs",
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 10}, End: niceyaml.Position{Line: 1, Col: 12}},
			},
		},
		"utf8 - multiple multibyte chars": {
			input:  "key: über öffentlich",
			search: "öffentlich",
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 11}, End: niceyaml.Position{Line: 1, Col: 21}},
			},
		},
		"utf8 - normalizer finds diacritic as ascii": {
			input:      "key: über öffentlich",
			search:     "o",
			normalizer: niceyaml.StandardNormalizer,
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 11}, End: niceyaml.Position{Line: 1, Col: 12}},
			},
		},
		"utf8 - combined normalizer case and diacritics": {
			input:  "name: THAÏS test",
			search: "thais",
			normalizer: func(s string) string {
				return strings.ToLower(niceyaml.StandardNormalizer(s))
			},
			want: []niceyaml.PositionRange{
				{Start: niceyaml.Position{Line: 1, Col: 7}, End: niceyaml.Position{Line: 1, Col: 12}},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.input)
			require.NotEmpty(t, tokens)

			var opts []niceyaml.FinderOption
			if tc.normalizer != nil {
				opts = append(opts, niceyaml.WithNormalizer(tc.normalizer))
			}

			finder := niceyaml.NewFinder(opts...)

			got := finder.FindStringsInTokens(tc.search, tokens)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinder_FindStringsInTokens_EmptyTokens(t *testing.T) {
	t.Parallel()

	finder := niceyaml.NewFinder()
	got := finder.FindStringsInTokens("test", nil)
	assert.Nil(t, got)
}
