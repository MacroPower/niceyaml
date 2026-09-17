package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestFilePath(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		pattern  string
		filePath string
		want     bool
	}{
		"exact match": {
			pattern:  "config.yaml",
			filePath: "config.yaml",
			want:     true,
		},
		"exact no match": {
			pattern:  "config.yaml",
			filePath: "settings.yaml",
			want:     false,
		},
		"match in root": {
			pattern:  "*.yaml",
			filePath: "config.yaml",
			want:     true,
		},
		"root pattern does not match deep path": {
			pattern:  "*.yaml",
			filePath: "deep/path/config.yaml",
			want:     false,
		},
		"root pattern matches dot slash prefix": {
			pattern:  "*.yaml",
			filePath: "./config.yaml",
			want:     true,
		},
		"pattern no match": {
			pattern:  "**/*.json",
			filePath: "config.yaml",
			want:     false,
		},
		"k8s directory match": {
			pattern:  "**/k8s/*.yaml",
			filePath: "deploy/k8s/app.yaml",
			want:     true,
		},
		"k8s deep match": {
			pattern:  "**/k8s/*.yaml",
			filePath: "a/b/c/k8s/app.yaml",
			want:     true,
		},
		"k8s no match": {
			pattern:  "**/k8s/*.yaml",
			filePath: "deploy/other/app.yaml",
			want:     false,
		},
		"any yaml file": {
			pattern:  "**/*.yaml",
			filePath: "any/path/file.yaml",
			want:     true,
		},
		"any yaml file in root": {
			pattern:  "**/*.yaml",
			filePath: "file.yaml",
			want:     true,
		},
		"empty file path": {
			pattern:  "**/*.yaml",
			filePath: "",
			want:     false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := matcher.MustFilePath(tc.pattern)
			doc := yamltest.FirstDocumentWithPath(t, "kind: Test", tc.filePath)

			got := m.Match(t.Context(), doc)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFilePath_InvalidPattern(t *testing.T) {
	t.Parallel()

	_, err := matcher.FilePath("[")
	require.Error(t, err)

	assert.Panics(t, func() {
		matcher.MustFilePath("[")
	})
}

func TestFilePath_EmptyPattern(t *testing.T) {
	t.Parallel()

	// An empty pattern matches nothing, so it is rejected rather than
	// silently disabling the matcher.
	_, err := matcher.FilePath("")
	require.Error(t, err)

	assert.Panics(t, func() {
		matcher.MustFilePath("")
	})
}
