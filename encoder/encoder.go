package encoder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

// ErrNoNode indicates a value that go-yaml encodes to no YAML node, such
// as a marshaler that returns no YAML document or an AST node that holds
// only comments. [Encoder.Encode] and [Marshal] return it.
var ErrNoNode = errors.New("value encodes to no YAML node")

// Pretty is an [Option] that makes the encoder write the layout prettier
// writes, with two-space indentation and each sequence indented one level
// below its parent key. A sequence at the root of a document starts at the
// first column, as [WithIndentSequence] describes. The YAML a marshaler
// returns keeps the indentation it had in that text, so a sequence in it
// can sit at the indent of its parent key.
//
// Pretty applies [WithIndent] with 2 and [WithIndentSequence] with true.
// Options apply in order and the last one to set a value wins, so
// New(w, Pretty(), WithIndent(4)) indents by four spaces and
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
// level. It panics when spaces is below 1, since the encoder would then
// write nested values at the indent of their parent, which reads back as a
// different value.
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
// the root of a document has no parent key, so it starts at the first
// column either way. The exception is a root sequence that holds YAML a
// marshaler returned, which go-yaml leaves at the columns it had in that
// text. The encoder writes such a sequence one level in, as go-yaml lays
// it out.
func WithIndentSequence(indent bool) Option {
	return func(c *config) {
		c.opts = append(c.opts, yaml.IndentSequence(indent))
	}
}

// WithYAMLComments is an [Option] that adds the comments in cm to every
// document the encoder writes. Each key of cm is a YAML path, such as
// "$.a.b" or "$.items[0]", and its comments go on the node that path
// selects. Build each comment with [yaml.HeadComment], [yaml.LineComment],
// or [yaml.FootComment]. A path that selects no node in a document adds
// nothing to it. The last WithYAMLComments option replaces the earlier
// ones.
//
// [Encoder.Encode] returns an error and writes nothing when a key is not a
// YAML path, when a path does not fit the document, as when it indexes a
// mapping, or when the selected node cannot hold a comment in that
// position. The paths, the placement of each comment, and the errors are
// those of [yaml.WithComment]. As with that option, a line comment on a
// string that go-yaml writes as a literal block does not reach the output.
func WithYAMLComments(cm yaml.CommentMap) Option {
	return func(c *config) {
		c.comments = cm
	}
}

// WithYAMLOptions is an [Option] that passes [yaml.EncodeOption]
// values straight to go-yaml. It is the escape hatch for encoder settings
// that have no option of their own. The encoder writes no comments from
// [yaml.WithComment], because go-yaml adds them only when it writes the
// document itself. Pass that comment map to [WithYAMLComments] instead.
// With [yaml.UseSingleQuote], go-yaml writes a tab, a line break, or
// another unprintable character in a single-quoted string as a Go escape,
// such as \t for a tab. YAML has no escapes in single quotes, so the
// string reads back with the escape's characters. The encoder leaves such
// a string as go-yaml wrote it.
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
// buffers nothing, so the document has reached the writer when Encode
// returns. Each document defines its own anchors, so a later document
// never aliases an anchor from an earlier one. A write the writer refuses
// is an error, and every later call returns that same error without
// encoding v. The encoder passes ctx to every marshaler that accepts one,
// such as a [yaml.InterfaceMarshalerContext] or a marshaler registered
// with [yaml.RegisterCustomMarshalerContext].
//
// Encode double-quotes every string that go-yaml would write unquoted or
// as a literal block in a form that reads back as a different value or
// does not parse. Such strings include ".inf", a one-line string with a
// tab, and any string with a carriage return. A literal block in the YAML
// that a marshaler returns is the exception. Encode writes such a block
// with the indentation it had in that YAML, so the block can fail to parse
// in a nested collection. Encode double-quotes it only where YAML forbids
// a literal block: as a key or in a flow collection. Encode also
// double-quotes a single-quoted string with a line break in a marshaler's
// YAML, since YAML folds a line break in single quotes. A block collection
// in a marshaler's YAML is the other exception. In a flow collection, as
// under [yaml.Flow], go-yaml writes it in block style, which YAML forbids
// there, and Encode leaves it and the strings in it as go-yaml writes
// them.
//
// Encode returns [ErrNoNode] when v, an entry of a sequence in v, or the
// value of a map key in v encodes to no YAML node. Encode also returns an
// error when go-yaml cannot print the node, as with a negative
// [yaml.Indent] from [WithYAMLOptions]. In both cases, Encode writes
// nothing.
func (e *Encoder) Encode(ctx context.Context, v any) error {
	// After a refused write, v never reaches go-yaml, so its marshalers do
	// not run and an encoding error cannot hide the write error.
	err := e.writeErr()
	if err != nil {
		return err
	}

	// A comment path that does not parse is an error before v reaches
	// go-yaml, as it is with the WithComment option of go-yaml.
	comments, err := parseComments(e.comments)
	if err != nil {
		return err
	}

	// A go-yaml encoder keeps every anchor it has seen, even from a call
	// that failed, so each call marshals with a fresh one.
	node, err := yaml.NewEncoder(io.Discard, e.opts...).EncodeToNodeContext(ctx, v)
	if err != nil {
		return err //nolint:wrapcheck // Return the original error.
	}

	// The go-yaml encoder leaves a nil node where a value encodes to no
	// node. Its printer writes a nil root as "<nil>" and drops a nil
	// sequence entry, so Encode refuses the whole tree.
	var holes holeFinder

	ast.Walk(&holes, node)

	if holes.found {
		return ErrNoNode
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
		return err
	}

	b, err := render(node)
	if err != nil {
		return err
	}

	if e.written {
		b = append([]byte("---\n"), b...)
	}

	_, err = e.w.Write(b)
	if err != nil {
		e.err = err

		return e.writeErr()
	}

	e.written = true

	return nil
}

// Marshal returns v as one YAML document that ends in a line break. The
// document is what a new [Encoder] with opts writes for v, and
// [Encoder.Encode] describes how it encodes v, where it passes ctx, and
// the errors it returns. Marshal returns nil bytes with an error.
func Marshal(ctx context.Context, v any, opts ...Option) ([]byte, error) {
	var buf bytes.Buffer

	err := New(&buf, opts...).Encode(ctx, v)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// render returns the YAML text of node, or an error when the go-yaml
// printer panics on a node it cannot lay out, such as one with a negative
// indent. The PrintNode method of that printer formats the node with fmt,
// which writes the panic into the text instead.
func render(node ast.Node) (_ []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("print YAML: %v", r)
		}
	}()

	return []byte(node.String() + "\n"), nil
}

// outdentRoot moves a block sequence at the root of a document, with
// every node in it, to the first column. Under [yaml.IndentSequence],
// go-yaml indents every sequence one level, the root one included, which
// has no parent key to indent below. Go-yaml leaves the YAML a marshaler
// returns at the columns it had in that text, left of the sequence that
// holds it. The move would put that YAML left of the first column, so a
// sequence that holds such YAML stays where go-yaml put it.
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

// writeErr returns the first error the writer returned, wrapped with the
// "write YAML" prefix, or nil when every write succeeded.
func (e *Encoder) writeErr() error {
	if e.err != nil {
		return fmt.Errorf("write YAML: %w", e.err)
	}

	return nil
}
