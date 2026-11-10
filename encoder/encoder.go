package encoder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"runtime/debug"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/hook"
)

var (
	// ErrEncode indicates a value that does not encode to YAML.
	// [Encoder.Encode] and [Marshal] return it. These errors match:
	//
	//   - A value of a type that go-yaml does not encode, such as a channel
	//     or a function.
	//   - The error that a value's own MarshalYAML or MarshalText method
	//     returns. That error stays in the chain.
	//   - A value that encodes to no YAML node, which [ErrNoNode] labels.
	//
	// The error of the context of the call does not match after that
	// context ended, even when a marshaler returns it. A context error
	// that a marshaler returns while that context lives matches. A
	// [*PanicError], an [ErrComment] error, and an [ErrWrite] error do
	// not match.
	ErrEncode = errors.New("value does not encode")

	// ErrNoNode is a label on an [ErrEncode] error for a value that go-yaml
	// does not encode to a YAML node. Such values include a marshaler that
	// does not return a YAML document and an AST node that holds only
	// comments. An error that matches ErrNoNode matches ErrEncode too.
	ErrNoNode = errors.New("value encodes to no YAML node")

	// ErrComment indicates a comment from [WithYAMLComments] that the
	// encoder cannot place in the document. [Encoder.Encode] and [Marshal]
	// return it for a key that is not a YAML path and for a comment
	// position that go-yaml does not define, whatever value they encode.
	// They return it for a node that cannot hold a comment in the position
	// it got. They return it for a path that names a group of nodes, such
	// as "$..name". They also return it for a mapping key that go-yaml
	// cannot read when it looks for the key that a path names.
	// [WithYAMLComments] describes such paths and keys. The error of
	// go-yaml stays in the chain, so [yaml.ErrInvalidPathString] matches
	// where it matches the error of [yaml.WithComment]. A path of keys and
	// indexes that does not select a node in a document does not cause the
	// error. The encoder skips its comments.
	ErrComment = errors.New("comment does not apply")

	// ErrWrite indicates a write that the writer of an [Encoder] refused.
	// The error of the writer stays in the chain. [Encoder.Encode] returns
	// it for the refused write and for every call after it.
	ErrWrite = errors.New("writer refused the document")
)

// PanicError is the error of a panic that the go-yaml printer raised while
// it laid out a node, such as one with a negative [yaml.Indent] from
// [WithYAMLOptions]. Value holds the value the code passed to panic, and
// Stack holds the stack of the goroutine at the panic, as [debug.Stack]
// formats it. The message leaves the stack out.
//
// A panic is a bug in code, not a problem with the value, so the error
// matches no sentinel of this package:
//
//	if p, ok := errors.AsType[*encoder.PanicError](err); ok {
//		slog.Error("printer panicked", "value", p.Value, "stack", string(p.Stack))
//	}
type PanicError struct {
	// Value is the value the code passed to panic.
	Value any
	// Stack is the stack of the goroutine at the panic.
	Stack []byte
}

// Error returns the value of the panic behind "printer panicked: ",
// formatted as the %v verb of [fmt.Sprintf] formats it.
func (e *PanicError) Error() string {
	return fmt.Sprintf("printer panicked: %v", e.Value)
}

// Unwrap returns Value when it is an error, and nil for any other value.
// [errors.Is] and [errors.As] thus reach an error the code panicked with.
func (e *PanicError) Unwrap() error {
	if err, ok := e.Value.(error); ok {
		return err
	}

	return nil
}

// labeled is an error that matches [ErrEncode] beside the label it holds,
// such as [ErrNoNode]. Its message is the message of the label.
type labeled struct {
	label error
}

// Error returns the message of the label.
func (e *labeled) Error() string {
	return e.label.Error()
}

// Unwrap returns the label.
func (e *labeled) Unwrap() error {
	return e.label
}

// Is reports whether target is [ErrEncode].
func (e *labeled) Is(target error) bool {
	return target == ErrEncode
}

// Pretty is an [Option] that makes the encoder write the layout that
// prettier writes. That layout indents by two spaces and indents each
// sequence one level below its parent key. A sequence at the root of a
// document starts at the first column, as [WithIndentSequence] describes.
// The YAML a marshaler returns keeps the indentation it had in that text,
// so a sequence in it might sit at the indent of its parent key.
//
// Pretty applies [WithIndent] with 2 and [WithIndentSequence] with true.
// Options apply in order, and the last one to set a value wins.
// New(w, Pretty(), WithIndent(4)) thus indents by four spaces, and
// New(w, WithIndent(4), Pretty()) indents by two.
func Pretty() Option {
	return func(c *config) {
		WithIndent(2)(c)
		WithIndentSequence(true)(c)
	}
}

// Encoder writes values as YAML.
//
// Create instances with [New].
type Encoder struct {
	w io.Writer
	// Holds the first error w returned.
	err error
	// Holds the comments for every document, keyed by YAML path.
	comments yaml.CommentMap
	opts     []yaml.EncodeOption
	// Reports whether a document has reached w.
	written bool
}

// Option configures an [Encoder].
//
// Available options:
//   - [Pretty]
//   - [WithIndent]
//   - [WithIndentSequence]
//   - [WithYAMLComments]
//   - [WithYAMLOptions]
type Option func(*config)

// config collects the go-yaml options and the comments that build an
// [Encoder].
type config struct {
	comments yaml.CommentMap
	opts     []yaml.EncodeOption
}

// WithIndent is an [Option] that sets the number of spaces per indentation
// level. It panics when spaces is less than 1, because the encoder then
// writes nested values at the indent of their parent. That YAML reads back
// as a different value.
func WithIndent(spaces int) Option {
	if spaces < 1 {
		panic(fmt.Sprintf("encoder.WithIndent: %d spaces is below 1", spaces))
	}

	return func(c *config) {
		c.opts = append(c.opts, yaml.Indent(spaces))
	}
}

// WithIndentSequence is an [Option] that sets whether the encoder indents
// sequence entries one level below their parent key. The default is false,
// and entries then sit at the indent of their parent key. A sequence at
// the root of a document does not have a parent key, so it starts at the
// first column either way. The exception is a root sequence that holds
// YAML a marshaler returned. Go-yaml leaves that YAML at the columns it had
// in the marshaler's text, so the encoder keeps such a sequence one level
// in, where go-yaml puts it.
func WithIndentSequence(indent bool) Option {
	return func(c *config) {
		c.opts = append(c.opts, yaml.IndentSequence(indent))
	}
}

// WithYAMLComments is an [Option] that adds the comments in cm to every
// document the encoder writes. Each key of cm is a YAML path, such as
// "$.a.b" or "$.items[0]", and its comments go on the node that path
// selects. Build each comment with [yaml.HeadComment], [yaml.LineComment],
// or [yaml.FootComment]. The encoder ignores a nil comment. The last
// WithYAMLComments option replaces the earlier ones.
//
// A path of keys and indexes names one node. So does a path that ends in
// "[*]", which names the sequence it steps into. The encoder skips the
// comments of such a path when it does not select a node in the document
// the encoder writes. These paths select no node:
//
//   - A path to a key that its mapping does not have.
//   - A path to an index past the end of its sequence.
//   - A path that steps into a node of another kind, such as an index
//     into a mapping or a key of a scalar or a null.
//   - A path that steps into a value that has an anchor or a tag, or
//     into an alias. A go-yaml path selects such a value and does not
//     select the keys and items in it. A struct field with the anchor
//     option in its yaml tag encodes to such a value.
//
// One comment map thus serves documents with different contents, and a
// value that lost a commented entry still encodes. An index path names a
// position in a sequence, so its comment goes to whatever item holds
// that position in the document.
//
// [Encoder.Encode] returns an error that matches [ErrComment] and does
// not write the document when any of these holds:
//
//   - A key is not a YAML path.
//   - A comment has a position that go-yaml does not define.
//   - The selected node cannot hold a comment in that position.
//   - Go-yaml looks for the mapping key that a path names and cannot
//     read a key of that mapping. A marshaler's YAML can hold such a
//     key, for example the single-quoted key `'"x'`. With
//     [yaml.UseLiteralStyleIfMultiline], a string key that starts with
//     a double quote and holds a line break is such a key too. Go-yaml
//     reads the keys from the first one, so a path to a later key
//     fails. A path to a key that the mapping does not have fails too.
//   - A path names a group of nodes. A recursive step names a group, as
//     in "$..name". So does "[*]" with a step after it, as in
//     "$.items[*].name". No position holds a comment on a group, so the
//     encode fails even when the group is empty.
//
// The encoder skips a path that names a group in a document where a step
// before the group selects no node. It also skips the path where "[*]"
// or a step after it steps into a node of another kind, such as "[*]"
// into a scalar or a null. It skips the path where a step after "[*]"
// steps to an index past the end of a sequence. A step after "[*]" to a
// key that an item does not have is not such a step, so that encode
// fails.
//
// Encode checks the keys of cm and the positions before it encodes the
// value, so a comment map with either fault fails every document.
//
// The paths and the placement of each comment are those of
// [yaml.WithComment], and the error of go-yaml stays in the chain. Of the
// paths that select no node, that option skips only a path to a key that
// its mapping does not have. It fails on the others. As with that option,
// a line comment on a string that go-yaml writes as a literal block does
// not reach the output.
func WithYAMLComments(cm yaml.CommentMap) Option {
	return func(c *config) {
		c.comments = cm
	}
}

// WithYAMLOptions is an [Option] that passes [yaml.EncodeOption]
// values straight to go-yaml. It covers the encoder settings that do not
// have an option of their own. Go-yaml adds the comments from
// [yaml.WithComment] only when it writes the document itself, so the
// encoder does not write them. Pass that comment map to [WithYAMLComments]
// instead.
//
// With [yaml.UseSingleQuote], go-yaml writes a tab, a line break, or
// another unprintable character in a single-quoted string as a Go escape,
// such as \t for a tab. YAML does not process backslash escapes in single
// quotes, so the string reads back with the escape's characters. The
// encoder leaves such a string as go-yaml wrote it.
func WithYAMLOptions(opts ...yaml.EncodeOption) Option {
	return func(c *config) {
		c.opts = append(c.opts, opts...)
	}
}

// New creates a new [*Encoder] that writes to w.
func New(w io.Writer, opts ...Option) *Encoder {
	var c config

	for _, opt := range opts {
		opt(&c)
	}

	return &Encoder{
		w:        w,
		opts:     c.opts,
		comments: c.comments,
	}
}

// Encode encodes v as YAML and writes it to the underlying writer as one
// document. Every document after the first starts with a "---" separator.
// Encode makes one Write call for each document, separator included, and
// does not buffer output, so the document has reached the writer when
// Encode returns. Each document defines its own anchors, so a later
// document never aliases an anchor from an earlier one.
//
// When the writer refuses a write, Encode returns an error that matches
// [ErrWrite], and every later call returns that same error without
// encoding v. The encoder passes ctx to every marshaler that accepts one,
// such as a [yaml.InterfaceMarshalerContext] or a marshaler registered
// with [yaml.RegisterCustomMarshalerContext].
//
// Encode double-quotes each string that go-yaml otherwise writes unquoted
// or as a literal block in a form that reads back as a different value or
// does not parse. Such strings include ".inf", a one-line string with a
// tab, and any string with a carriage return. Encode also double-quotes a
// string without a tag that the core schema of YAML 1.2 reads as another
// value when it is unquoted, such as "1e3", which go-yaml reads as a string
// and YAML 1.2 reads as a float. The tag of a scalar in the YAML that a
// marshaler returns decides how a reader reads the scalar, so Encode leaves
// such a scalar unquoted.
//
// A literal block in the YAML that a marshaler returns is the exception.
// Encode writes such a block with the indentation it had in that YAML, so
// the block might fail to parse in a nested collection. Encode
// double-quotes it only where YAML forbids a literal block: as a key or in
// a flow collection. Encode also double-quotes a single-quoted string with
// a line break in a marshaler's YAML, because YAML folds a line break in
// single quotes.
//
// A block collection in a marshaler's YAML is the other exception. In a
// flow collection, such as one that [yaml.Flow] produces, go-yaml writes
// the block collection in block style. YAML forbids block style there, and
// Encode leaves the collection and the strings in it as go-yaml writes
// them.
//
// Encode returns an error that matches [ErrEncode] when v does not encode
// to YAML. A value of a type go-yaml does not encode and a marshaler that
// returns an error are such values. The error matches [ErrNoNode] too
// when a value in v does not encode to a YAML node. That value is v
// itself, an entry of a sequence in v, or the value of a map key in v.
// After ctx ended, Encode returns its error as it is. A marshaler can
// return a context error of its own while ctx lives, and that error
// matches ErrEncode. Encode returns a
// [*PanicError] when go-yaml cannot print the node, such as with a
// negative [yaml.Indent] from [WithYAMLOptions]. A comment from
// [WithYAMLComments] that the encoder cannot place returns an error that
// matches [ErrComment]. In every case but a refused write, Encode does
// not write to the writer.
func (e *Encoder) Encode(ctx context.Context, v any) error {
	_, err := e.encode(ctx, v)

	return err
}

// encode is [Encoder.Encode], and it also returns the node that it printed
// for v. The scalars of the node hold the values that go-yaml's encoder
// gave them.
func (e *Encoder) encode(ctx context.Context, v any) (ast.Node, error) {
	// After a refused write, v never reaches go-yaml, so its marshalers do
	// not run and an encoding error cannot hide the write error.
	err := e.writeErr()
	if err != nil {
		return nil, err
	}

	// A comment key that is not a path is an error before v reaches
	// go-yaml, as it is with the WithComment option of go-yaml. So is a
	// comment position that go-yaml does not define. The comment map alone
	// decides both errors, so every document gets the same answer.
	comments, err := parseComments(e.comments)
	if err != nil {
		return nil, err
	}

	// A go-yaml encoder keeps every anchor it has seen, even from a call
	// that failed, so each call marshals with a fresh one.
	node, err := yaml.NewEncoder(io.Discard, e.opts...).EncodeToNodeContext(ctx, v)
	if err != nil {
		// The value is not at fault when its context ended, so that error
		// comes back as it is.
		if contextEnded(ctx, err) {
			return nil, err //nolint:wrapcheck // Return the original error.
		}

		return nil, fmt.Errorf("%w: %w", ErrEncode, err)
	}

	// The go-yaml encoder leaves a nil node where a value encodes to no
	// node. Its printer writes a nil root as "<nil>" and drops a nil
	// sequence entry, so Encode refuses the whole tree.
	var holes holeFinder

	ast.Walk(&holes, node)

	if holes.found {
		return nil, &labeled{label: ErrNoNode}
	}

	// The go-yaml encoder leaves some strings unquoted in a form that
	// reads back as a different value, so Encode quotes them before it
	// prints the node.
	ast.Walk(quoter{}, node)

	// Prettier starts a sequence at the root of a document in the first
	// column, where go-yaml indents it under IndentSequence.
	outdentRoot(node)

	// The comments go on after the quoting, which can replace a node.
	err = setComments(node, comments)
	if err != nil {
		return nil, err
	}

	b, err := render(node)
	if err != nil {
		return nil, err
	}

	if e.written {
		b = append([]byte("---\n"), b...)
	}

	_, err = e.w.Write(b)
	if err != nil {
		e.err = err

		return nil, e.writeErr()
	}

	e.written = true

	return node, nil
}

// Marshal returns v as one YAML document that ends in a line break. The
// document is what a new [Encoder] with opts writes for v. [Encoder.Encode]
// describes how Marshal encodes v, where it passes ctx, and which errors it
// returns. Marshal returns nil bytes with an error.
func Marshal(ctx context.Context, v any, opts ...Option) ([]byte, error) {
	var buf bytes.Buffer

	err := New(&buf, opts...).Encode(ctx, v)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// init sets the call that the encoder hands to the edit package, as the
// package [hook] describes.
//
//nolint:gochecknoinits // The hook reaches no API, so only init can set it.
func init() {
	hook.Marshal = func(ctx context.Context, v, opts any) ([]byte, ast.Node, error) {
		o, _ := opts.([]Option) //nolint:errcheck // The hook takes the options of Marshal.

		var buf bytes.Buffer

		node, err := New(&buf, o...).encode(ctx, v)
		if err != nil {
			return nil, nil, err
		}

		return buf.Bytes(), node, nil
	}
}

// render returns the YAML text of node, or a [*PanicError] when the
// go-yaml printer panics on a node it cannot lay out, such as one with a
// negative indent. The PrintNode method of that printer formats the node
// with fmt, which writes the panic into the text instead.
func render(node ast.Node) (_ []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r, Stack: debug.Stack()}
		}
	}()

	return []byte(node.String() + "\n"), nil
}

// outdentRoot moves a block sequence at the root of a document, with
// every node in it, to the first column. Under [yaml.IndentSequence],
// go-yaml indents every sequence one level, including the root sequence,
// which does not have a parent key to indent below. Go-yaml leaves the
// YAML a marshaler returns at the columns it had in that text, left of the
// sequence that holds it. Moving such a sequence puts that YAML left of the
// first column, so outdentRoot leaves the sequence where go-yaml put it.
func outdentRoot(node ast.Node) {
	seq, ok := node.(*ast.SequenceNode)
	if !ok || seq.IsFlowStyle {
		return
	}

	shift := seq.Start.Position.Column - 1
	if shift <= 0 {
		return
	}

	lowest := lowestColumn{column: math.MaxInt}

	ast.Walk(&lowest, seq)

	if lowest.column > shift {
		seq.AddColumn(-shift)
	}
}

// lowestColumn is an [ast.Visitor] that finds the lowest column of the
// nodes in a tree.
type lowestColumn struct {
	column int
}

// Visit implements [ast.Visitor].
func (l *lowestColumn) Visit(node ast.Node) ast.Visitor {
	if node == nil {
		return nil
	}

	if tk := node.GetToken(); tk != nil {
		l.column = min(l.column, tk.Position.Column)
	}

	return l
}

// holeFinder is an [ast.Visitor] that reports whether a tree holds a nil
// node.
type holeFinder struct {
	found bool
}

// Visit implements [ast.Visitor].
func (h *holeFinder) Visit(node ast.Node) ast.Visitor {
	if node == nil {
		h.found = true

		return nil
	}

	return h
}

// writeErr returns the first error the writer returned, wrapped with
// [ErrWrite], or nil when every write succeeded.
func (e *Encoder) writeErr() error {
	if e.err != nil {
		return fmt.Errorf("%w: %w", ErrWrite, e.err)
	}

	return nil
}

// contextEnded reports whether ctx ended and err is, or wraps, the error
// of a context that ended: [context.Canceled] or
// [context.DeadlineExceeded]. A marshaler can return such an error from a
// context of its own while ctx lives. That error is a failure of the
// value, so contextEnded reports false for it.
func contextEnded(ctx context.Context, err error) bool {
	if ctx.Err() == nil {
		return false
	}

	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
