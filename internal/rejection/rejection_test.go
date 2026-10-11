package rejection_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/rejection"
)

// pair is a struct the decoder reads from a mapping.
type pair struct{ A, B int }

func TestClassify(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err   yaml.Error
		want  string
		label rejection.Label
	}{
		"wrong kind": {
			err:   &yaml.TypeError{DstType: reflect.TypeFor[int](), SrcType: reflect.TypeFor[string]()},
			want:  "expected integer, got string",
			label: rejection.WrongKind,
		},
		"pointer names what it points to": {
			err:   &yaml.TypeError{DstType: reflect.TypeFor[*[]int](), SrcType: reflect.TypeFor[bool]()},
			want:  "expected sequence, got boolean",
			label: rejection.WrongKind,
		},
		"null": {
			err:   &yaml.TypeError{DstType: reflect.TypeFor[float64](), SrcType: nil},
			want:  "expected float, got null",
			label: rejection.WrongKind,
		},
		"types of one kind": {
			err:   &yaml.TypeError{DstType: reflect.TypeFor[pair](), SrcType: reflect.TypeFor[map[string]int]()},
			want:  "expected mapping, got mapping of another type",
			label: rejection.WrongKind,
		},
		"value of an interface": {
			err:   &yaml.TypeError{DstType: reflect.TypeFor[pair](), SrcType: reflect.TypeFor[any]()},
			want:  "expected mapping, got value of another type",
			label: rejection.WrongKind,
		},
		"types named apart from their kind": {
			err:   &yaml.TypeError{DstType: reflect.TypeFor[time.Duration](), SrcType: reflect.TypeFor[time.Time]()},
			want:  "expected duration, got timestamp",
			label: rejection.WrongKind,
		},
		"binary and ordered map": {
			err:   &yaml.TypeError{DstType: reflect.TypeFor[[]byte](), SrcType: reflect.TypeFor[yaml.MapSlice]()},
			want:  "expected binary, got mapping",
			label: rejection.WrongKind,
		},
		"target that takes no value": {
			err:   &yaml.TypeError{DstType: reflect.TypeFor[chan int](), SrcType: reflect.TypeFor[string]()},
			want:  "expected no value, got string",
			label: rejection.WrongKind,
		},
		"node of the wrong kind": {
			err:   &yaml.UnexpectedNodeTypeError{Expected: ast.MappingType, Actual: ast.SequenceType},
			want:  "expected mapping, got sequence",
			label: rejection.WrongKind,
		},
		"node that holds no value": {
			err:   &yaml.UnexpectedNodeTypeError{Expected: ast.StringType, Actual: ast.AnchorType},
			want:  "expected string, got anchor",
			label: rejection.WrongKind,
		},
		"signed overflow": {
			err:   &yaml.OverflowError{DstType: reflect.TypeFor[int8](), SrcNum: "300"},
			want:  "expected integer from -128 to 127, got 300",
			label: rejection.Overflow,
		},
		"unsigned overflow": {
			err:   &yaml.OverflowError{DstType: reflect.TypeFor[uint16](), SrcNum: "-1"},
			want:  "expected integer from 0 to 65535, got -1",
			label: rejection.Overflow,
		},
		"overflow of another kind": {
			err:   &yaml.OverflowError{DstType: reflect.TypeFor[float32](), SrcNum: "1e99"},
			want:  "expected float, got 1e99",
			label: rejection.Overflow,
		},
		"unknown field": {
			err:   &yaml.UnknownFieldError{Message: `unknown field "x"`},
			want:  `unknown field "x"`,
			label: rejection.UnknownField,
		},
		"other": {
			err:   &yaml.DuplicateKeyError{Message: `mapping key "a" already defined`},
			want:  `mapping key "a" already defined`,
			label: rejection.None,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, label := rejection.Classify(tc.err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.label, label)
		})
	}
}

func TestExpectedString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "expected string, got sequence", rejection.ExpectedString(ast.SequenceType))
	assert.Equal(t, "expected string, got mapping", rejection.ExpectedString(ast.MappingValueType))
}
