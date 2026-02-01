package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"jacobcolvin.com/niceyaml/internal/filepaths"
	"jacobcolvin.com/niceyaml/internal/yamltest"
	"jacobcolvin.com/niceyaml/schema/matcher"
)

func TestFilePath(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		pattern  filepaths.Pattern
		filePath string
		want     bool
	}{
		"exact match": {
			pattern:  filepaths.MustPattern("config.yaml"),
			filePath: "config.yaml",
			want:     true,
		},
		"exact no match": {
			pattern:  filepaths.MustPattern("config.yaml"),
			filePath: "settings.yaml",
			want:     false,
		},
		"match in root": {
			pattern:  filepaths.MustPattern("*.yaml"),
			filePath: "config.yaml",
			want:     true,
		},
		"root pattern does not match deep path": {
			pattern:  filepaths.MustPattern("*.yaml"),
			filePath: "deep/path/config.yaml",
			want:     false,
		},
		"pattern no match": {
			pattern:  filepaths.MustPattern("**/*.json"),
			filePath: "config.yaml",
			want:     false,
		},
		"k8s directory match": {
			pattern:  filepaths.MustPattern("**/k8s/*.yaml"),
			filePath: "deploy/k8s/app.yaml",
			want:     true,
		},
		"k8s deep match": {
			pattern:  filepaths.MustPattern("**/k8s/*.yaml"),
			filePath: "a/b/c/k8s/app.yaml",
			want:     true,
		},
		"k8s no match": {
			pattern:  filepaths.MustPattern("**/k8s/*.yaml"),
			filePath: "deploy/other/app.yaml",
			want:     false,
		},
		"any yaml file": {
			pattern:  filepaths.MustPattern("**/*.yaml"),
			filePath: "any/path/file.yaml",
			want:     true,
		},
		"any yaml file in root": {
			pattern:  filepaths.MustPattern("**/*.yaml"),
			filePath: "file.yaml",
			want:     true,
		},
		"empty file path": {
			pattern:  filepaths.MustPattern("**/*.yaml"),
			filePath: "",
			want:     false,
		},
		"empty pattern": {
			pattern:  filepaths.Pattern{},
			filePath: "config.yaml",
			want:     false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := matcher.FilePath(tc.pattern)
			doc := yamltest.FirstDocumentWithPath(t, "kind: Test", tc.filePath)

			got := m.Match(t.Context(), doc)
			assert.Equal(t, tc.want, got)
		})
	}
}
