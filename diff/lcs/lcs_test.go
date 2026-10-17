package lcs_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/diff/lcs"
)

func TestOpKind_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		kind lcs.OpKind
	}{
		"equal":        {kind: lcs.OpEqual, want: "equal"},
		"delete":       {kind: lcs.OpDelete, want: "delete"},
		"insert":       {kind: lcs.OpInsert, want: "insert"},
		"out of range": {kind: lcs.OpKind(-1), want: "OpKind(-1)"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.kind.String())
		})
	}
}
