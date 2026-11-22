package astnode_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

func TestMisnested(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The token value of each misnested line and of its property.
		want []string
	}{
		"tag on an empty value": {
			input: stringtest.LinesLF("a: !t", "b: 1", "c: 2"),
			want:  []string{"b !t", "c !t"},
		},
		"tag on an empty value in a mapping": {
			input: stringtest.LinesLF("a:", "  b: !t", "c: 1"),
			want:  []string{"c !t"},
		},
		"tag on an empty item": {
			input: stringtest.LinesLF("- !t", "- 1"),
			want:  []string{"- !t"},
		},
		"anchor on an empty item": {
			input: stringtest.LinesLF("r0:", "- &x0", "r1: 1"),
			want:  []string{"r1 &"},
		},
		"anchor on an empty value": {
			input: stringtest.LinesLF("a: &x", "b: 1"),
		},
		"tag on a mapping": {
			input: stringtest.LinesLF("a: !t", "  b: 1"),
		},
		"tag on a sequence at the column of its key": {
			input: stringtest.LinesLF("a: !t", "- 1"),
		},
		"tag on a flow mapping": {
			input: stringtest.LinesLF("a: !t {b: 1}", "c: 2"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []string

			stopped := astnode.Misnested(parse(t, tc.input), func(tk *token.Token, props ast.Node) bool {
				got = append(got, tk.Value+" "+props.GetToken().Value)

				return true
			})

			assert.False(t, stopped)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("stop", func(t *testing.T) {
		t.Parallel()

		calls := 0

		stopped := astnode.Misnested(parse(t, stringtest.LinesLF("a: !t", "b: 1", "c: 2")),
			func(*token.Token, ast.Node) bool {
				calls++

				return false
			})

		assert.True(t, stopped)
		assert.Equal(t, 1, calls)
	})
}
