package paths_test

import (
	"cmp"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/paths"
)

func TestParse(t *testing.T) {
	t.Parallel()

	// TestPath_Build and TestPath_YAMLPath_Wildcards parse back each
	// expression they write. A case that leaves want empty reads back as
	// the expression it parses.
	tcs := map[string]struct {
		expr string
		want string
	}{
		"simple path": {expr: "$.foo"},
		"nested path": {expr: "$.foo.bar.baz"},
		"root index":  {expr: "$[2]"},
		"quoted key without reserved characters is unquoted": {
			expr: "$.'plain'",
			want: "$.plain",
		},
		"quoted key with escaped backslash": {expr: `$.'a\\b.c'`},
		"backslash before another character is dropped": {
			expr: `$.'C:\docs'`,
			want: `$.'C:docs'`,
		},
		"unicode escape of a control character": {expr: `$.'a\u001bb'`},
		"unicode escape in upper case": {
			expr: `$.'a\u001Bb'`,
			want: `$.'a\u001bb'`,
		},
		"unicode escape of a plain character": {
			expr: `$.'\u0041bc'`,
			want: "$.Abc",
		},
		"raw line feed in quotes": {
			expr: "$.'a\nb'",
			want: `$.'a\nb'`,
		},
		"escaped backslash before n": {
			expr: `$.'a\\nb'`,
			want: `$.a\nb`,
		},
		"quoted key followed by index":  {expr: "$.'a.b'[1].c"},
		"empty quoted recursive key":    {expr: "$..''[0]"},
		"key selector on the root":      {expr: "$~"},
		"key selector after an index":   {expr: "$.items[0]~"},
		"mapping wildcard then index":   {expr: "$.*[0]"},
		"quoted star is a name":         {expr: "$.'*'"},
		"recursive wildcard then child": {expr: "$.spec..*.name"},
		"relative child":                {expr: "@.foo.bar"},
		"relative index":                {expr: "@[2]"},
		"relative key":                  {expr: "@~"},
		"relative wildcards":            {expr: "@.jobs.*..name[*]"},
		"at sign in a name":             {expr: "$.@ref"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := paths.Parse(tc.expr)

			require.NoError(t, err)
			assert.Equal(t, cmp.Or(tc.want, tc.expr), p.String())
			assert.Equal(t, tc.expr[0] == '$', p.IsAbsolute())
		})
	}
}

func TestParse_Invalid(t *testing.T) {
	t.Parallel()

	// A case that sets want pins the whole message of the error.
	tcs := map[string]struct {
		expr string
		want string
	}{
		"empty":                      {expr: ""},
		"no anchor":                  {expr: "foo"},
		"anchor after a selector":    {expr: ".foo$"},
		"two anchors":                {expr: "$@"},
		"two anchors the other way":  {expr: "@$"},
		"trailing dot after current": {expr: "@."},
		"bare text after current": {
			expr: "@foo",
			want: `parse path "@foo": invalid path: unexpected 'f' at 1`,
		},
		"leading dot without root": {
			expr: ".foo",
			want: `parse path ".foo": invalid path: expression must start with $ or @`,
		},
		"trailing dot":        {expr: "$."},
		"unclosed bracket":    {expr: "$["},
		"empty index":         {expr: "$[]"},
		"negative index":      {expr: "$[-1]"},
		"negative zero index": {expr: "$[-0]"},
		"index with a leading zero": {
			expr: "$[01]",
			want: `parse path "$[01]": invalid path: index "01": not a canonical non-negative integer`,
		},
		"index with a plus sign": {expr: "$[+1]"},
		"non-numeric index":      {expr: "$[a]"},
		"index overflows int": {
			expr: "$[99999999999999999999]",
			want: `parse path "$[99999999999999999999]": invalid path: ` +
				`index "99999999999999999999": out of range`,
		},
		"star before a name": {
			expr: "$.*a",
			want: `parse path "$.*a": invalid path: unexpected '*' in selector "*a"`,
		},
		"star after a name": {expr: "$.a*"},
		"two stars":         {expr: "$.**"},
		"recursive star before a name": {
			expr: "$..*a",
			want: `parse path "$..*a": invalid path: unexpected '*' in selector "*a"`,
		},
		"recursive star after a name":         {expr: "$..a*"},
		"two recursive stars":                 {expr: "$..**"},
		"recursive star before a quoted name": {expr: "$..*'a'"},
		"star before a quoted name":           {expr: "$.*'a'"},
		"unterminated quote":                  {expr: "$.'foo"},
		"unterminated escape":                 {expr: `$.'a\`},
		"unicode escape with too few digits": {
			expr: `$.'a\u00'`,
			want: `parse path "$.'a\\u00'": invalid path: ` +
				`\u escape in quoted selector needs four hexadecimal digits`,
		},
		"unicode escape at the end":                           {expr: `$.'a\u`},
		"unicode escape with a digit that is not hexadecimal": {expr: `$.'a\u00zz'`},
		"unicode escape of a surrogate half": {
			expr: `$.'a\ud800'`,
			want: `parse path "$.'a\\ud800'": invalid path: ` +
				`\ud800 in quoted selector is a surrogate half, which names no character`,
		},
		"bare text after root": {
			expr: "$foo",
			want: `parse path "$foo": invalid path: unexpected 'f' at 1`,
		},
		"bare text after key selector": {expr: "$.a~b"},
		"non-ASCII after root": {
			expr: "$é",
			want: `parse path "$é": invalid path: unexpected 'é' at 1`,
		},
		"non-ASCII after index": {
			expr: "$[0]日",
			want: `parse path "$[0]日": invalid path: unexpected '日' at 4`,
		},
		"invalid UTF-8 after quoted name": {
			expr: "$.'a'\xff",
			want: `parse path "$.'a'\xff": invalid path: unexpected "\xff" at 5`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := paths.Parse(tc.expr)

			require.ErrorIs(t, err, paths.ErrInvalidPath)
			assert.Equal(t, paths.Path{}, p)
			assert.Contains(t, err.Error(), "parse path")

			if tc.want != "" {
				require.EqualError(t, err, tc.want)
			}
		})
	}
}

func TestParse_RoundTrip(t *testing.T) {
	t.Parallel()

	tcs := map[string]paths.Path{
		"root":              paths.Doc(),
		"children":          paths.Doc().Child("a", "b"),
		"index":             paths.Doc().Child("items").Index(3),
		"wildcards":         paths.Doc().Child("items").IndexAll().Recursive("name"),
		"dotted name":       paths.Doc().Child("kubernetes.io/name"),
		"quote in name":     paths.Doc().Child("it's"),
		"backslash":         paths.Doc().Child(`a\b.c`),
		"brackets":          paths.Doc().Child("a[0]"),
		"dollar":            paths.Doc().Child("$ref"),
		"star":              paths.Doc().Child("*"),
		"numeric name":      paths.Doc().Child("1"),
		"space in name":     paths.Doc().Child("has space"),
		"colon in name":     paths.Doc().Child("x: y"),
		"trailing space":    paths.Doc().Child("name "),
		"line break":        paths.Doc().Child("a\nb"),
		"tab":               paths.Doc().Child("a\tb"),
		"carriage return":   paths.Doc().Child("a\rb"),
		"escape character":  paths.Doc().Child("a\x1b[31mb"),
		"delete":            paths.Doc().Child("a\x7fb"),
		"C1 control":        paths.Doc().Child("a\u0085b"),
		"line separator":    paths.Doc().Child("a\u2028b\u2029c"),
		"backslash and n":   paths.Doc().Child(`a\nb`),
		"only reserved":     paths.Doc().Child("."),
		"tilde in name":     paths.Doc().Child("a~b"),
		"goccy compatible":  paths.Doc().Child("a.b").Index(1).Child("c"),
		"current":           paths.Current(),
		"relative":          paths.Current().Child("items").Index(3).Child("a.b"),
		"at sign in name":   paths.Doc().Child("@"),
		"key of root":       paths.Doc().Key(),
		"key of element":    paths.Doc().Child("items").Index(0).Key(),
		"quoted name key":   paths.Doc().Child("a.b").Key(),
		"recursive key":     paths.Doc().Recursive("name").Key(),
		"empty name key":    paths.Doc().Child("").Key(),
		"tilde in name key": paths.Doc().Child("a~b").Key(),
		"key of current":    paths.Current().Key(),
		"relative key":      paths.Current().Child("a").Key().Child("b"),
	}

	for name, want := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := paths.Parse(want.String())
			require.NoError(t, err)

			assert.Equal(t, want, got)

			// The goccy parser accepts the same expression, with `$` in
			// place of `@`, unless the path holds a key selector, which
			// goccy has no form for.
			for sel := range want.Selectors() {
				if sel.Kind == paths.SelectorKey {
					return
				}
			}

			expr := want.String()
			if !want.IsAbsolute() {
				expr = "$" + expr[1:]
			}

			_, err = yaml.PathString(expr)
			require.NoError(t, err)
		})
	}
}

func TestMustParse(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		paths.MustParse("not a valid path")
	})
}
