package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainsGlobChars(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
		want  bool
	}{
		"asterisk": {
			input: "*.yaml",
			want:  true,
		},
		"question mark": {
			input: "file?.yaml",
			want:  true,
		},
		"bracket": {
			input: "file[0-9].yaml",
			want:  true,
		},
		"multiple globs": {
			input: "**/[a-z]*.yaml",
			want:  true,
		},
		"no glob chars": {
			input: "file.yaml",
			want:  false,
		},
		"empty string": {
			input: "",
			want:  false,
		},
		"path without globs": {
			input: "/path/to/file.yaml",
			want:  false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := containsGlobChars(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestExpandGlobs(t *testing.T) {
	t.Parallel()

	// Create a temporary directory with test files.
	tmpDir := t.TempDir()

	// Create test files with predictable names for sorting.
	files := []struct {
		name    string
		content string
	}{
		{"002.yaml", "c: 3"},
		{"000.yaml", "a: 1"},
		{"001.yaml", "b: 2"},
	}
	for _, f := range files {
		err := os.WriteFile(filepath.Join(tmpDir, f.name), []byte(f.content), 0o644)
		require.NoError(t, err)
	}

	tests := map[string]struct {
		args      []string
		wantNames []string
		err       string
	}{
		"single file": {
			args:      []string{filepath.Join(tmpDir, "000.yaml")},
			wantNames: []string{"000.yaml"},
		},
		"multiple explicit files": {
			args:      []string{filepath.Join(tmpDir, "002.yaml"), filepath.Join(tmpDir, "000.yaml")},
			wantNames: []string{"000.yaml", "002.yaml"},
		},
		"glob pattern": {
			args:      []string{filepath.Join(tmpDir, "*.yaml")},
			wantNames: []string{"000.yaml", "001.yaml", "002.yaml"},
		},
		"glob with question mark": {
			args:      []string{filepath.Join(tmpDir, "00?.yaml")},
			wantNames: []string{"000.yaml", "001.yaml", "002.yaml"},
		},
		"glob with bracket": {
			args:      []string{filepath.Join(tmpDir, "00[01].yaml")},
			wantNames: []string{"000.yaml", "001.yaml"},
		},
		"mixed glob and explicit": {
			args:      []string{filepath.Join(tmpDir, "00[01].yaml"), filepath.Join(tmpDir, "002.yaml")},
			wantNames: []string{"000.yaml", "001.yaml", "002.yaml"},
		},
		"no matches": {
			args: []string{filepath.Join(tmpDir, "*.json")},
			err:  "no matching files",
		},
		"file not found": {
			args: []string{filepath.Join(tmpDir, "nonexistent.yaml")},
			err:  "read file",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			entries, err := expandGlobs(tc.args)

			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.err)

				return
			}

			require.NoError(t, err)
			require.Len(t, entries, len(tc.wantNames))

			for i, entry := range entries {
				assert.Equal(t, tc.wantNames[i], filepath.Base(entry.path))
			}
		})
	}
}

func TestExpandGlobs_PreservesContent(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	content := "key: value\nlist:\n  - item1\n  - item2"
	err := os.WriteFile(filepath.Join(tmpDir, "test.yaml"), []byte(content), 0o644)
	require.NoError(t, err)

	entries, err := expandGlobs([]string{filepath.Join(tmpDir, "test.yaml")})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, content, string(entries[0].content))
}
