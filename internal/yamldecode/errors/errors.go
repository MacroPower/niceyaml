// Package errors stands in for go-yaml's internal/errors package. It builds
// go-yaml's own error values through the exported aliases of package yaml,
// so errors.As to a *yaml.TypeError matches an error of the copy.
package errors

import (
	"errors"
	"reflect"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

var (
	As     = errors.As
	Is     = errors.Is
	New    = errors.New
	Unwrap = errors.Unwrap
)

type (
	Error                   = yaml.Error
	SyntaxError             = yaml.SyntaxError
	TypeError               = yaml.TypeError
	OverflowError           = yaml.OverflowError
	DuplicateKeyError       = yaml.DuplicateKeyError
	UnknownFieldError       = yaml.UnknownFieldError
	UnexpectedNodeTypeError = yaml.UnexpectedNodeTypeError
)

// FormatError is go-yaml's formatter, which go-yaml exports as
// yaml.FormatErrorWithToken.
var FormatError = yaml.FormatErrorWithToken

func ErrSyntax(msg string, tk *token.Token) *SyntaxError {
	return &SyntaxError{Message: msg, Token: tk}
}

func ErrOverflow(dstType reflect.Type, num string, tk *token.Token) *OverflowError {
	return &OverflowError{DstType: dstType, SrcNum: num, Token: tk}
}

func ErrTypeMismatch(dstType, srcType reflect.Type, tk *token.Token) *TypeError {
	return &TypeError{DstType: dstType, SrcType: srcType, Token: tk}
}

func ErrDuplicateKey(msg string, tk *token.Token) *DuplicateKeyError {
	return &DuplicateKeyError{Message: msg, Token: tk}
}

func ErrUnknownField(msg string, tk *token.Token) *UnknownFieldError {
	return &UnknownFieldError{Message: msg, Token: tk}
}

func ErrUnexpectedNodeType(actual, expected ast.NodeType, tk *token.Token) *UnexpectedNodeTypeError {
	return &UnexpectedNodeTypeError{Actual: actual, Expected: expected, Token: tk}
}
