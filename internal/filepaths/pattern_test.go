package filepaths_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/filepaths"
)

func TestNewPattern(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		pattern string
		err     error
	}{
		"empty pattern":                     {pattern: "", err: filepaths.ErrInvalidPattern},
		"empty brace group":                 {pattern: "{}", err: filepaths.ErrInvalidPattern},
		"empty alternatives":                {pattern: "{,}", err: filepaths.ErrInvalidPattern},
		"nested empty alternatives":         {pattern: "{,{}}", err: filepaths.ErrInvalidPattern},
		"invalid bracket":                   {pattern: "[", err: filepaths.ErrInvalidPattern},
		"unclosed bracket":                  {pattern: "[abc", err: filepaths.ErrInvalidPattern},
		"braces within the expansion limit": {pattern: strings.Repeat("{a,b}", 10) + ".yaml"},
		"braces past the expansion limit": {
			pattern: strings.Repeat("{,}", 30) + "x.yaml",
			err:     filepaths.ErrBraceLimit,
		},
		"braces past the work limit": {
			pattern: strings.Repeat("{a}", 20000) + ".yaml",
			err:     filepaths.ErrBraceLimit,
		},
		"leading parent elements": {pattern: "../../*.yaml"},
		"parent after a wildcard element": {
			pattern: "*/../x.yaml",
			err:     filepaths.ErrInvalidPattern,
		},
		"parent after a double star after a name": {
			pattern: "a/**/../x.yaml",
			err:     filepaths.ErrInvalidPattern,
		},
		"parent after a double star after the root": {
			pattern: "/**/../x.yaml",
			err:     filepaths.ErrInvalidPattern,
		},
		"parent after a double star after a name after a double star": {
			pattern: "**/a/**/../x.yaml",
			err:     filepaths.ErrInvalidPattern,
		},
		"parent after a class element": {
			pattern: "a/[bc]/../x.yaml",
			err:     filepaths.ErrInvalidPattern,
		},
		"parent after a wildcard in one alternative": {
			pattern: "{a,*}/../x.yaml",
			err:     filepaths.ErrInvalidPattern,
		},
		"parent after a wildcard and an escaped separator": {
			pattern: `*\/../x.yaml`,
			err:     filepaths.ErrInvalidPattern,
		},
		"parent after a double star and an escaped separator": {
			pattern: `a/**\/../x.yaml`,
			err:     filepaths.ErrInvalidPattern,
		},
		"escaped parent after a wildcard element": {
			pattern: `*/\.\./x.yaml`,
			err:     filepaths.ErrInvalidPattern,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := filepaths.NewPattern(tc.pattern)
			if tc.err != nil {
				require.ErrorIs(t, err, filepaths.ErrInvalidPattern)
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestPattern_Match(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		pattern string
		path    string
		want    bool
	}{
		"exact match": {
			pattern: "config.yaml",
			path:    "config.yaml",
			want:    true,
		},
		"exact no match": {
			pattern: "config.yaml",
			path:    "settings.yaml",
		},
		"wildcard in root": {
			pattern: "*.yaml",
			path:    "config.yaml",
			want:    true,
		},
		"wildcard does not match subdir": {
			pattern: "*.yaml",
			path:    "deep/path/config.yaml",
		},
		"brace in a class matches a single segment": {
			pattern: "[^a{]b",
			path:    "xb",
			want:    true,
		},
		"brace in a class matches no multi-segment path": {
			// The pattern validates, but doublestar cannot interpret it
			// against a path with a separator, so it matches no such path.
			pattern: "[^a{]b",
			path:    "a/b",
		},
		"wildcard in root with dot slash prefix": {
			pattern: "*.yaml",
			path:    "./config.yaml",
			want:    true,
		},
		"pattern with dot slash prefix matches a bare path": {
			pattern: "./configs/*.yaml",
			path:    "configs/a.yaml",
			want:    true,
		},
		"pattern with dot slash prefix matches a dot slash path": {
			pattern: "./configs/*.yaml",
			path:    "./configs/a.yaml",
			want:    true,
		},
		"pattern with repeated separators": {
			pattern: "configs//*.yaml",
			path:    "configs/a.yaml",
			want:    true,
		},
		"pattern with trailing separator": {
			pattern: "configs/",
			path:    "configs",
			want:    true,
		},
		"pattern with an inner dot element": {
			pattern: "k8s/./*.yaml",
			path:    "k8s/app.yaml",
			want:    true,
		},
		"pattern with a trailing dot element": {
			pattern: "k8s/.",
			path:    "k8s",
			want:    true,
		},
		"dot slash pattern reads as dot": {
			pattern: "./",
			path:    ".",
			want:    true,
		},
		"dot slash pattern matches no file": {
			pattern: "./",
			path:    "x.yaml",
		},
		"dot alternative in braces": {
			pattern: "{.,configs}/*.yaml",
			path:    "values.yaml",
			want:    true,
		},
		"directory alternative beside a dot alternative": {
			pattern: "{.,configs}/*.yaml",
			path:    "configs/values.yaml",
			want:    true,
		},
		"dot element inside braces": {
			pattern: "{./a,b}.yaml",
			path:    "a.yaml",
			want:    true,
		},
		"repeated separators inside braces": {
			pattern: "{configs//x,y}.yaml",
			path:    "configs/x.yaml",
			want:    true,
		},
		"class holding separator and dot": {
			pattern: "a[/./]b",
			path:    "a.b",
			want:    true,
		},
		"pattern with dot slash prefix keeps its depth": {
			pattern: "./*.yaml",
			path:    "configs/a.yaml",
		},
		"wildcard in root after parent traversal": {
			pattern: "*.yaml",
			path:    "sub/../config.yaml",
			want:    true,
		},
		"parent after a literal matches the cleaned path": {
			pattern: "configs/../*.yaml",
			path:    "x.yaml",
			want:    true,
		},
		"parent after a literal matches a path spelled the same": {
			pattern: "configs/../*.yaml",
			path:    "./configs/../x.yaml",
			want:    true,
		},
		"parent after a literal keeps its depth": {
			pattern: "configs/../*.yaml",
			path:    "configs/x.yaml",
		},
		"nested parents after literals": {
			pattern: "a/b/../../c/*.yaml",
			path:    "c/x.yaml",
			want:    true,
		},
		"parent after an escaped wildcard literal": {
			pattern: `a\*/../x.yaml`,
			path:    "x.yaml",
			want:    true,
		},
		"parent above the root": {
			pattern: "/../x.yaml",
			path:    "/x.yaml",
			want:    true,
		},
		"escaped separator before a parent": {
			pattern: `a\/b/../*.yaml`,
			path:    "a/x.yaml",
			want:    true,
		},
		"escaped separator before a parent keeps its depth": {
			pattern: `a\/b/../*.yaml`,
			path:    "x.yaml",
		},
		"escaped leading separator is rooted": {
			pattern: `\/a/../b`,
			path:    "/b",
			want:    true,
		},
		"escaped leading separator matches no relative path": {
			pattern: `\/a/../b`,
			path:    "b",
		},
		"escaped dot element": {
			pattern: `\./*.yaml`,
			path:    "x.yaml",
			want:    true,
		},
		"escaped inner dot element": {
			pattern: `k8s/\./*.yaml`,
			path:    "k8s/x.yaml",
			want:    true,
		},
		"escaped parent element": {
			pattern: `a/\.\./x.yaml`,
			path:    "x.yaml",
			want:    true,
		},
		"escaped backslash before a separator": {
			pattern: `a\\/../x.yaml`,
			path:    "x.yaml",
			want:    true,
		},
		"escaped separator before a trailing double star": {
			pattern: `a\/**`,
			path:    "a",
			want:    true,
		},
		"double star before an escaped separator matches one directory": {
			pattern: `a/**\/x.yaml`,
			path:    "a/q/x.yaml",
			want:    true,
		},
		"double star before an escaped separator is a single star": {
			pattern: `a/**\/x.yaml`,
			path:    "a/q/r/x.yaml",
		},
		"double star after an escaped separator": {
			pattern: `a\/**/x.yaml`,
			path:    "a/q/r/x.yaml",
			want:    true,
		},
		"leading parent elements stay": {
			pattern: "../*.yaml",
			path:    "../x.yaml",
			want:    true,
		},
		"parent after a literal after leading parents": {
			pattern: "../a/../*.yaml",
			path:    "../x.yaml",
			want:    true,
		},
		"parent after a double star matching no directory": {
			pattern: "**/../x.yaml",
			path:    "../x.yaml",
			want:    true,
		},
		"parent after a double star after leading parents": {
			pattern: "../**/../x.yaml",
			path:    "../../x.yaml",
			want:    true,
		},
		"star matches no parent element": {
			pattern: "*/x.yaml",
			path:    "../x.yaml",
		},
		"stars match no parent elements": {
			pattern: "*/*/x.yaml",
			path:    "../a/x.yaml",
		},
		"question marks match no parent element": {
			pattern: "??/x.yaml",
			path:    "../x.yaml",
		},
		"classes match no parent element": {
			pattern: "[.][.]/x.yaml",
			path:    "../x.yaml",
		},
		"star after a parent matches a name": {
			pattern: "../*/x.yaml",
			path:    "../a/x.yaml",
			want:    true,
		},
		"star after a parent matches no second parent": {
			pattern: "../*/x.yaml",
			path:    "../../x.yaml",
		},
		"double star matches parent elements": {
			pattern: "**/x.yaml",
			path:    "../../x.yaml",
			want:    true,
		},
		"double star after a parent matches a second parent": {
			pattern: "../**/x.yaml",
			path:    "../../a/x.yaml",
			want:    true,
		},
		"rooted double star matches no parent element": {
			pattern: "/**/x.yaml",
			path:    "../x.yaml",
		},
		"parent pattern matches the parent itself": {
			pattern: "..",
			path:    "..",
			want:    true,
		},
		"star matches no parent itself": {
			pattern: "*",
			path:    "..",
		},
		"more parents than the pattern names": {
			pattern: "../x.yaml",
			path:    "../../x.yaml",
		},
		"repeated separators are collapsed": {
			pattern: "deep/*.yaml",
			path:    "deep//config.yaml",
			want:    true,
		},
		"trailing separator is dropped": {
			pattern: "config.yaml",
			path:    "config.yaml/",
			want:    true,
		},
		"double star recursive": {
			pattern: "**/*.yaml",
			path:    "deep/path/config.yaml",
			want:    true,
		},
		"double star root": {
			pattern: "**/*.yaml",
			path:    "config.yaml",
			want:    true,
		},
		"double star specific dir": {
			pattern: "**/k8s/*.yaml",
			path:    "deploy/k8s/app.yaml",
			want:    true,
		},
		"double star specific dir deep": {
			pattern: "**/k8s/*.yaml",
			path:    "a/b/c/k8s/app.yaml",
			want:    true,
		},
		"double star specific dir no match": {
			pattern: "**/k8s/*.yaml",
			path:    "deploy/other/app.yaml",
		},
		"question mark": {
			pattern: "file?.yaml",
			path:    "file1.yaml",
			want:    true,
		},
		"question mark no match": {
			pattern: "file?.yaml",
			path:    "file12.yaml",
		},
		"bracket range": {
			pattern: "file[0-9].yaml",
			path:    "file5.yaml",
			want:    true,
		},
		"bracket range no match": {
			pattern: "file[0-9].yaml",
			path:    "filea.yaml",
		},
		"empty path": {
			pattern: "*.yaml",
			path:    "",
		},
		"one empty alternative": {
			pattern: "{,a.yaml}",
			path:    "a.yaml",
			want:    true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := filepaths.NewPattern(tc.pattern)
			require.NoError(t, err)

			got := p.Match(tc.path)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPattern_Relative(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		pattern string
		want    bool
	}{
		"relative":                        {pattern: "configs/*.yaml", want: true},
		"double star":                     {pattern: "**/*.yaml", want: true},
		"leading dot element":             {pattern: "./configs/*.yaml", want: true},
		"rooted":                          {pattern: "/etc/app/*.yaml"},
		"rooted with repeated separators": {pattern: "//etc/app/*.yaml"},
		"braces around rooted patterns":   {pattern: "/{etc,opt}/app/*.yaml"},
		"braces around a rooted and a relative pattern": {pattern: "{/etc,configs}/*.yaml", want: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := filepaths.NewPattern(tc.pattern)
			require.NoError(t, err)

			assert.Equal(t, tc.want, p.Relative())
		})
	}
}

func TestAnyDepthPatterns_SpecificityClean(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path     string
		patterns []string
		// The specificity of the most specific pattern that matches the
		// path. A case checks it when it sets it, and every case of a path
		// that no pattern matches checks that it is 0.
		want int
		ok   bool
	}{
		"matches base name": {
			path:     "some/dir/config.yaml",
			patterns: []string{"*.yaml"},
			ok:       true,
		},
		"matches full path pattern": {
			path:     ".github/workflows/ci.yaml",
			patterns: []string{".github/workflows/*.yaml"},
			ok:       true,
		},
		"matches full path pattern with dot slash prefix": {
			path:     "./.github/workflows/ci.yaml",
			patterns: []string{".github/workflows/*.yaml"},
			ok:       true,
		},
		"matches exact filename via base": {
			path:     "some/dir/docker-compose.yaml",
			patterns: []string{"docker-compose.yaml"},
			ok:       true,
		},
		"no match": {
			path:     "config.txt",
			patterns: []string{"*.yaml"},
		},
		"empty path": {
			path:     "",
			patterns: []string{"*.yaml"},
		},
		"invalid pattern ignored": {
			path:     "config.yaml",
			patterns: []string{"[", "*.yaml"},
			ok:       true,
		},
		"pattern that fails validation still matches": {
			// The "[" alternative fails doublestar.ValidatePattern, but
			// doublestar.Match reads the pattern for this path.
			path:     "x/a.yaml",
			patterns: []string{"{a.yaml,[}"},
			ok:       true,
		},
		"dependabot exact": {
			path:     ".github/dependabot.yaml",
			patterns: []string{".github/dependabot.yml", ".github/dependabot.yaml"},
			ok:       true,
		},
		"directory pattern matches an absolute path": {
			path:     "/repo/.github/workflows/ci.yml",
			patterns: []string{".github/workflows/*.yml"},
			ok:       true,
		},
		"directory pattern matches a nested path": {
			path:     "repo/.github/workflows/ci.yml",
			patterns: []string{".github/workflows/*.yml"},
			ok:       true,
		},
		"directory pattern does not match another directory": {
			path:     "repo/.circleci/workflows/ci.yml",
			patterns: []string{".github/workflows/*.yml"},
		},
		"directory pattern does not match a partial directory name": {
			path:     "repo/my.github/workflows/ci.yml",
			patterns: []string{".github/workflows/*.yml"},
		},
		"leading slash is dropped": {
			path:     "repo/.github/workflows/ci.yml",
			patterns: []string{"/.github/workflows/*.yml"},
			ok:       true,
		},
		"leading dot slash is dropped": {
			path:     "repo/.github/workflows/ci.yml",
			patterns: []string{"./.github/workflows/*.yml"},
			ok:       true,
		},
		"repeated separators in a pattern are collapsed": {
			path:     "repo/.github/workflows/ci.yml",
			patterns: []string{".github//workflows/*.yml"},
			ok:       true,
		},
		"dot elements in a pattern are dropped": {
			path:     "repo/k8s/x.yaml",
			patterns: []string{"k8s/./x.yaml"},
			ok:       true,
		},
		"escaped dot elements in a pattern are dropped": {
			path:     "repo/k8s/x.yaml",
			patterns: []string{`k8s/\./*.yaml`},
			ok:       true,
		},
		"dot alternative in braces matches at any depth": {
			path:     "repo/values.yaml",
			patterns: []string{"{.,configs}/*.yaml"},
			ok:       true,
		},
		"class holding separator and dot matches at any depth": {
			path:     "repo/a.b",
			patterns: []string{"a[/./]b"},
			ok:       true,
		},
		"negation with a dot alternative in braces": {
			path:     "repo/values.yaml",
			patterns: []string{"*.yaml", "!{.,configs}/values.yaml"},
		},
		"double star prefix is kept": {
			path:     "repo/.github/workflows/ci.yml",
			patterns: []string{"**/.github/workflows/*.yml"},
			ok:       true,
		},
		"base name pattern matches an absolute path": {
			path:     "/srv/app/docker-compose.yaml",
			patterns: []string{"docker-compose.yaml"},
			ok:       true,
		},
		"negation excludes file": {
			path:     "/x/docker-compose.yml",
			patterns: []string{"*.yml", "!docker-compose.yml"},
		},
		"negation leaves other files": {
			path:     "/x/app.yml",
			patterns: []string{"*.yml", "!docker-compose.yml"},
			ok:       true,
		},
		"negation before the include still excludes": {
			path:     "/x/docker-compose.yml",
			patterns: []string{"!docker-compose.yml", "*.yml"},
		},
		"negation only matches nothing": {
			path:     "/x/other.yml",
			patterns: []string{"!docker-compose.yml"},
		},
		"negation does not match its literal spelling": {
			path:     "/x/!docker-compose.yml",
			patterns: []string{"!docker-compose.yml"},
		},
		"negation at depth": {
			path:     "/r/.github/workflows/ci.yml",
			patterns: []string{"**/*.yml", "!.github/**"},
		},
		"negation with a leading slash": {
			path:     "/r/.github/workflows/ci.yml",
			patterns: []string{"*.yml", "!/.github/workflows/*.yml"},
		},
		"parent after a literal at depth": {
			path:     "/r/x.yaml",
			patterns: []string{"configs/../*.yaml"},
			ok:       true,
		},
		"parent after a wildcard is kept and matches nothing": {
			path:     "/r/a/x.yaml",
			patterns: []string{"*/../x.yaml"},
		},
		"bare negation is skipped": {
			path:     "/x/app.yml",
			patterns: []string{"*.yml", "!"},
			ok:       true,
		},
		"braces past the expansion limit match nothing": {
			path:     "a.yaml",
			patterns: []string{strings.Repeat("{,}", 30) + "*.yaml"},
		},
		"braces past the expansion limit exclude nothing": {
			path:     "a.yaml",
			patterns: []string{"*.yaml", "!" + strings.Repeat("{,}", 30) + "a.yaml"},
			ok:       true,
		},
		"braces past the expansion limit leave other patterns": {
			path:     "a.yaml",
			patterns: []string{strings.Repeat("{,}", 30) + "*.yaml", "a.yaml"},
			ok:       true,
		},
		"counts literal characters of names": {
			path:     "/repo/.moon/tasks/node.yml",
			patterns: []string{"**/.moon/tasks/**/*.yml"},
			want:     14,
			ok:       true,
		},
		"specificity of a broad pattern": {
			path:     "/repo/.moon/tasks/node.yml",
			patterns: []string{"**/tasks/*.yml"},
			want:     9,
			ok:       true,
		},
		"most specific matching pattern": {
			path:     "/repo/packages/foo/package.yaml",
			patterns: []string{"package.yaml", "**/packages/*/package.yaml"},
			want:     20,
			ok:       true,
		},
		"most specific brace alternative that matches": {
			path:     "/repo/migrations/x.vespertide.yml",
			patterns: []string{"**/migrations/{*.yml,**/*.vespertide.yml}"},
			want:     25,
			ok:       true,
		},
		"pattern that does not match is ignored": {
			path:     "/repo/config.yaml",
			patterns: []string{"*.yaml", "**/other/config.yaml"},
			want:     5,
			ok:       true,
		},
		"specificity of a question mark and a class": {
			path:     "/repo/a1.yaml",
			patterns: []string{"?[0-9].yaml"},
			want:     5,
			ok:       true,
		},
		"specificity of an escaped character": {
			path:     "/repo/a*.yaml",
			patterns: []string{`a\*.yaml`},
			want:     7,
			ok:       true,
		},
		"multibyte characters count once": {
			path:     "/repo/日本/a.yml",
			patterns: []string{"日本/*.yml"},
			want:     6,
			ok:       true,
		},
		"specificity of an escaped multibyte character": {
			path:     "/repo/é.yml",
			patterns: []string{`\é.yml`},
			want:     5,
			ok:       true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := filepaths.NewAnyDepthPatterns(tc.patterns)
			got, ok := p.SpecificityClean(filepaths.CleanPath(tc.path))
			assert.Equal(t, tc.ok, ok)

			if tc.want != 0 || !tc.ok {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestUnclosedClasses(t *testing.T) {
	t.Parallel()

	// Each "[" in a long run opens a class that nothing closes, so each
	// reads as a literal. The scans must stay linear in the pattern
	// length rather than look for a "]" again at every "[".
	run := strings.Repeat("[", 200000)

	tcs := map[string]struct {
		pattern string
		want    []string
	}{
		"run of unclosed classes": {
			pattern: run + ".yaml",
			want:    []string{run + ".yaml"},
		},
		"run of unclosed classes before braces": {
			pattern: run + ".{a,b}",
			want:    []string{run + ".a", run + ".b"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			start := time.Now()

			got, err := filepaths.ExpandBraces(tc.pattern)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)

			p := filepaths.NewAnyDepthPatterns([]string{tc.pattern})
			_, ok := p.SpecificityClean("x.yaml")
			assert.False(t, ok)

			assert.Less(t, time.Since(start), 2*time.Second)
		})
	}
}

func TestExpandBraces(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err     error
		pattern string
		want    []string
		// The number of patterns, for a case that names no want.
		wantLen int
	}{
		"no braces":        {pattern: "*.yaml", want: []string{"*.yaml"}},
		"empty pattern":    {pattern: "", want: []string{""}},
		"two alternatives": {pattern: "*.{yml,yaml}", want: []string{"*.yml", "*.yaml"}},
		"directory and alternatives": {
			pattern: "**/.github/workflows/*.{yml,yaml}",
			want:    []string{"**/.github/workflows/*.yml", "**/.github/workflows/*.yaml"},
		},
		"two groups multiply out": {
			pattern: "{a,b}.{x,y}",
			want:    []string{"a.x", "a.y", "b.x", "b.y"},
		},
		"nested group":                             {pattern: "a{b,c{d,e}}f", want: []string{"abf", "acdf", "acef"}},
		"empty alternative":                        {pattern: "a{,b}", want: []string{"a", "ab"}},
		"single alternative":                       {pattern: "a{b}c", want: []string{"abc"}},
		"unclosed group is kept":                   {pattern: "a{b,c", want: []string{"a{b,c"}},
		"stray closing brace is kept":              {pattern: "a}b{c,d}", want: []string{"a}bc", "a}bd"}},
		"escaped braces are kept":                  {pattern: `a\{b,c\}`, want: []string{`a\{b,c\}`}},
		"escaped comma stays in the alternative":   {pattern: `{a\,b,c}`, want: []string{`a\,b`, "c"}},
		"braces inside a character class are kept": {pattern: "x.[{a,b}]c", want: []string{"x.[{a,b}]c"}},
		"class inside a group keeps its comma":     {pattern: "{a,[,]b}", want: []string{"a", "[,]b"}},
		"closing bracket first in a negated class": {
			pattern: "[!]{]{a,b}",
			want:    []string{"[!]{]a", "[!]{]b"},
		},
		"unclosed class reads as a literal": {pattern: "a[{b,c}", want: []string{"a[b", "a[c"}},
		// Ten binary groups stand for 1024 patterns, the most ExpandBraces
		// produces.
		"binary groups at the expansion limit": {
			pattern: strings.Repeat("{a,b}", 10),
			wantLen: filepaths.MaxBraceExpansions,
		},
		// One more group would double the patterns of ten, so ExpandBraces
		// reports the limit.
		"binary groups past the expansion limit": {
			pattern: strings.Repeat("{a,b}", 11),
			err:     filepaths.ErrBraceLimit,
		},
		// Each of the next three patterns expands to few patterns, but
		// ExpandBraces would rebuild a long pattern once per brace group
		// to get there, so it reports the limit.
		"many single-alternative groups": {
			pattern: strings.Repeat("{a}", 20000) + ".yaml",
			err:     filepaths.ErrBraceLimit,
		},
		"deeply nested group": {
			pattern: strings.Repeat("{", 20000) + "a" + strings.Repeat("}", 20000) + ".yaml",
			err:     filepaths.ErrBraceLimit,
		},
		"long chain after branching groups": {
			pattern: strings.Repeat("{a,b}", 10) + strings.Repeat("{a}", 1300) + ".yaml",
			err:     filepaths.ErrBraceLimit,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := filepaths.ExpandBraces(tc.pattern)
			require.ErrorIs(t, err, tc.err)

			if tc.wantLen > 0 {
				assert.Len(t, got, tc.wantLen)

				return
			}

			assert.Equal(t, tc.want, got)
		})
	}
}
