package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestAlways(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
	}{
		"simple document": {
			input: stringtest.Input(`kind: Test`),
		},
		"complex document": {
			input: stringtest.Input(`
				apiVersion: v1
				kind: ConfigMap
				metadata:
				  name: test
				data:
				  key: value
			`),
		},
		"empty document": {
			input: stringtest.Input(`{}`),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := matcher.Always()
			doc := yamltest.FirstDocument(t, tc.input)

			got := m.Match(t.Context(), doc)
			assert.True(t, got)
		})
	}
}
