package filepaths_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"jacobcolvin.com/niceyaml/internal/filepaths"
)

func TestNewPattern(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		pattern string
		err     error
	}{
		"simple wildcard": {
			pattern: "*.yaml",
		},
		"double star": {
			pattern: "**/*.yaml",
		},
		"question mark": {
			pattern: "file?.yaml",
		},
		"bracket range": {
			pattern: "file[0-9].yaml",
		},
		"exact match": {
			pattern: "config.yaml",
		},
		"empty pattern": {
			pattern: "",
		},
		"invalid bracket": {
			pattern: "[",
			err:     filepaths.ErrInvalidPattern,
		},
		"unclosed bracket": {
			pattern: "[abc",
			err:     filepaths.ErrInvalidPattern,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := filepaths.NewPattern(tc.pattern)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.pattern, p.String())
		})
	}
}

func TestMustPattern(t *testing.T) {
	t.Parallel()

	t.Run("valid pattern", func(t *testing.T) {
		t.Parallel()

		p := filepaths.MustPattern("**/*.yaml")
		assert.Equal(t, "**/*.yaml", p.String())
	})

	t.Run("panics on invalid", func(t *testing.T) {
		t.Parallel()

		assert.Panics(t, func() {
			filepaths.MustPattern("[")
		})
	})
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
			want:    false,
		},
		"wildcard in root": {
			pattern: "*.yaml",
			path:    "config.yaml",
			want:    true,
		},
		"wildcard does not match subdir": {
			pattern: "*.yaml",
			path:    "deep/path/config.yaml",
			want:    false,
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
			want:    false,
		},
		"question mark": {
			pattern: "file?.yaml",
			path:    "file1.yaml",
			want:    true,
		},
		"question mark no match": {
			pattern: "file?.yaml",
			path:    "file12.yaml",
			want:    false,
		},
		"bracket range": {
			pattern: "file[0-9].yaml",
			path:    "file5.yaml",
			want:    true,
		},
		"bracket range no match": {
			pattern: "file[0-9].yaml",
			path:    "filea.yaml",
			want:    false,
		},
		"empty path": {
			pattern: "*.yaml",
			path:    "",
			want:    false,
		},
		"empty pattern": {
			pattern: "",
			path:    "config.yaml",
			want:    false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := filepaths.MustPattern(tc.pattern)
			got := p.Match(tc.path)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMatchAnyWithBase(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path     string
		patterns []string
		want     bool
	}{
		"matches base name": {
			path:     "some/dir/config.yaml",
			patterns: []string{"*.yaml"},
			want:     true,
		},
		"matches full path pattern": {
			path:     ".github/workflows/ci.yaml",
			patterns: []string{".github/workflows/*.yaml"},
			want:     true,
		},
		"matches exact filename via base": {
			path:     "some/dir/docker-compose.yaml",
			patterns: []string{"docker-compose.yaml"},
			want:     true,
		},
		"no match": {
			path:     "config.txt",
			patterns: []string{"*.yaml"},
			want:     false,
		},
		"empty path": {
			path:     "",
			patterns: []string{"*.yaml"},
			want:     false,
		},
		"invalid pattern ignored": {
			path:     "config.yaml",
			patterns: []string{"[", "*.yaml"},
			want:     true,
		},
		"github workflow deep path": {
			path:     ".github/workflows/ci.yaml",
			patterns: []string{".github/workflows/*.yml", ".github/workflows/*.yaml"},
			want:     true,
		},
		"dependabot exact": {
			path:     ".github/dependabot.yaml",
			patterns: []string{".github/dependabot.yml", ".github/dependabot.yaml"},
			want:     true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := filepaths.MatchAnyWithBase(tc.path, tc.patterns)
			assert.Equal(t, tc.want, got)
		})
	}
}
