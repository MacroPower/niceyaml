package yamlscan_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamlscan"
)

// fields holds every field of a [*token.Token]. Prev and Next hold the index
// of the linked token in its stream, or -1 for nil.
type fields struct {
	Position      *token.Position
	Type          token.Type
	CharacterType token.CharacterType
	Indicator     token.Indicator
	Value         string
	Origin        string
	Error         string
	Prev          int
	Next          int
}

// tokenFields returns the fields of each token of tks.
func tokenFields(tks token.Tokens) []fields {
	indexes := make(map[*token.Token]int, len(tks))
	for i, tk := range tks {
		indexes[tk] = i
	}

	index := func(tk *token.Token) int {
		i, ok := indexes[tk]
		if !ok {
			return -1
		}

		return i
	}

	out := make([]fields, len(tks))
	for i, tk := range tks {
		out[i] = fields{
			Position:      tk.Position,
			Type:          tk.Type,
			CharacterType: tk.CharacterType,
			Indicator:     tk.Indicator,
			Value:         tk.Value,
			Origin:        tk.Origin,
			Error:         tk.Error,
			Prev:          index(tk.Prev),
			Next:          index(tk.Next),
		}
	}

	return out
}

// TestTokenize compares the copy with go-yaml's lexer on every input of the
// corpus, and finds every field of every token equal.
func TestTokenize(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join("testdata", "corpus", "*", "*.yaml"))
	require.NoError(t, err)
	require.Len(t, files, 1580)

	for _, file := range files {
		t.Run(filepath.ToSlash(file), func(t *testing.T) {
			t.Parallel()

			src, err := os.ReadFile(file)
			require.NoError(t, err)

			want := tokenFields(lexer.Tokenize(string(src)))
			got := tokenFields(yamlscan.Tokenize(string(src)))

			// A diff of the whole stream of a large input takes minutes,
			// so the test reports the first token that differs.
			for i := range min(len(want), len(got)) {
				if !assert.Equal(t, want[i], got[i], "token %d", i) {
					return
				}
			}

			assert.Len(t, got, len(want))
		})
	}
}
