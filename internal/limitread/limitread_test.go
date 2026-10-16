package limitread_test

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/limitread"
)

func TestReadAll(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		text  string
		limit int
		want  int
	}{
		"text within the limit": {text: "abc", limit: 4, want: 3},
		"text at the limit":     {text: "abcd", limit: 4, want: 4},
		"text past the limit":   {text: "abcdefg", limit: 4, want: 5},
		"largest int":           {text: "abcdefg", limit: math.MaxInt, want: 7},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := limitread.ReadAll(strings.NewReader(tc.text), tc.limit)
			require.NoError(t, err)
			assert.Equal(t, tc.text[:tc.want], string(data))
		})
	}
}
