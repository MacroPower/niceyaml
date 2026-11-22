package yamldecode

import (
	"context"
	"reflect"
	"sync"

	"github.com/goccy/go-yaml"
)

// The types and values below are go-yaml's own. A type that implements an
// unmarshaler interface of go-yaml thus decodes itself through the copy, and
// code that names a type of go-yaml keeps working with the copy.
type (
	CommentMap      = yaml.CommentMap
	Comment         = yaml.Comment
	CommentPosition = yaml.CommentPosition
	MapSlice        = yaml.MapSlice
	MapItem         = yaml.MapItem
	StructValidator = yaml.StructValidator
	FieldError      = yaml.FieldError

	BytesUnmarshaler            = yaml.BytesUnmarshaler
	BytesUnmarshalerContext     = yaml.BytesUnmarshalerContext
	InterfaceUnmarshaler        = yaml.InterfaceUnmarshaler
	InterfaceUnmarshalerContext = yaml.InterfaceUnmarshalerContext
	NodeUnmarshaler             = yaml.NodeUnmarshaler
	NodeUnmarshalerContext      = yaml.NodeUnmarshalerContext
)

var (
	ErrDecodeRequiredPointerType = yaml.ErrDecodeRequiredPointerType
	ErrExceededMaxDepth          = yaml.ErrExceededMaxDepth
	ErrInvalidCommentMapValue    = yaml.ErrInvalidCommentMapValue

	HeadComment = yaml.HeadComment
	LineComment = yaml.LineComment
	FootComment = yaml.FootComment
)

// Go-yaml keeps the functions of yaml.RegisterCustomUnmarshaler in
// unexported package state, which the copy cannot read. The copy reads this
// registry of its own in that place. Nothing adds to it, so a decode of the
// copy calls no function of yaml.RegisterCustomUnmarshaler.
var (
	globalCustomUnmarshalerMu  sync.Mutex
	globalCustomUnmarshalerMap = map[reflect.Type]func(context.Context, interface{}, []byte) error{}
)
