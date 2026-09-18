// Package errchain walks the cause chain of an error that nests other errors
// and formats the lines the nested errors put in its message.
//
// The niceyaml package writes those lines when it binds an error to a
// source, putting the resolved position of each nested error in front, and
// the error tree the printer draws splits them off the message again. Both
// go through one [Funcs], so they never disagree about where a message ends
// and its nested lines begin. The package names no concrete error type; a
// caller describes its types with the functions of a Funcs.
//
// A cause chain follows a wrapper to the error it wraps and a node to its
// cause. A binding on the chain marks where another message begins: the
// lines below it carry the positions that binding resolved, unless the
// binding is rebound and left the message of a binding further in as it
// was. [Funcs.Split] walks the chain and [Funcs.Positioned] rewrites the
// nested lines with their positions.
package errchain

import (
	"fmt"
	"strings"

	"go.jacobcolvin.com/niceyaml/position"
)

// Funcs describes the error types of a caller to the walk. Every function
// receives errors the walk meets along a cause chain and answers for the
// caller's own types; a foreign error answers false everywhere.
type Funcs struct {
	// Nothing reports whether err is nil or a nil pointer to a node or a
	// binding, which carries no message and no location. The walk stops at
	// such an error, and a joined error skips it.
	Nothing func(err error) bool
	// Node returns the parts of err when err is a node, an error that holds
	// a cause and nested errors, and false otherwise.
	Node func(err error) (Node, bool)
	// Binding returns the parts of err when err is a binding, an error
	// bound to a named source, and false otherwise.
	Binding func(err error) (Binding, bool)
	// Position returns the position nested resolved to when binding was
	// bound, and false when it did not resolve. The walk passes only a
	// binding and a node it found along the chain.
	Position func(binding, nested error) (position.Position, bool)
}

// Node is the parts of a node error.
type Node struct {
	// Cause is the error the node was created from, or nil.
	Cause error
	// Nested are the errors nested in the node, in order.
	Nested []error
	// Located reports whether the node carries a location of its own.
	Located bool
}

// Binding is the parts of a binding error.
type Binding struct {
	// Inner is the error the binding binds.
	Inner error
	// Name is the name of the source the binding is bound to.
	Name string
}

// Nested is a nested error found along a cause chain.
type Nested struct {
	// Err is the nested error.
	Err error
	// Binding is the binding that resolved the position of Err, or nil
	// when none did.
	Binding error
	// Text is the message of Err as Err writes it, without the position
	// its binding adds.
	Text string
	// Positioned reports whether Binding wrote the line of Err in the
	// message the walk examined, with the position in front, rather than
	// the node that nests Err.
	Positioned bool
}

// Rebound reports whether the cause chain of the error binding binds
// reaches another binding before it reaches a node that carries a
// location. The message of such a binding is the message of the inner
// binding as it wrote it, positions included, so the outer binding adds
// none. The chain follows a joined error to its first branch that is not
// nothing. A binding that is not one reports false.
func (f Funcs) Rebound(binding error) bool {
	bound, ok := f.Binding(binding)
	if !ok {
		return false
	}

	for cur := bound.Inner; cur != nil && !f.Nothing(cur); {
		if _, ok := f.Binding(cur); ok {
			return true
		}

		if node, ok := f.Node(cur); ok {
			if node.Located {
				return false
			}

			cur = node.Cause

			continue
		}

		//nolint:errorlint // Walks the chain one node at a time; errors.As would skip ahead.
		switch x := cur.(type) {
		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			cur = nil

			for _, branch := range x.Unwrap() {
				if !f.Nothing(branch) {
					cur = branch

					break
				}
			}

		default:
			return false
		}
	}

	return false
}

// Split returns msg, the message of err, less the lines of the errors
// nested along its cause chain, and those nested errors, the innermost
// node's first, as the lines end the message. It reports false, with msg
// as it is and no nested errors, when msg does not end with the lines, as
// it does not behind a wrapper that rewrote the message. A message with no
// nested lines is its own head.
//
// The chain follows a wrapper to the error it wraps and a node to its
// cause, and does not follow a joined error. The binding given resolved
// the positions of the nested errors the chain meets before it reaches a
// binding of its own, or is nil when none did; msg is the message a node
// wrote, so those lines carry no positions. A binding on the chain
// positions the lines below it, unless it is rebound, in which case they
// carry none.
func (f Funcs) Split(err error, msg string, binding error) (string, []Nested, bool) {
	nested := f.nested(err, binding)

	lines := make([]string, 0, len(nested))
	kept := make([]Nested, 0, len(nested))

	for _, n := range nested {
		n.Text = n.Err.Error()
		if n.Text == "" {
			continue
		}

		line := n.Text
		if n.Positioned {
			line = f.line(n.Binding, n.Err, n.Text)
		}

		lines = append(lines, line)
		kept = append(kept, n)
	}

	if len(lines) == 0 {
		return msg, nil, true
	}

	joined := strings.Join(lines, "\n")

	switch {
	case msg == joined:
		return "", kept, true

	case strings.HasSuffix(msg, "\n"+joined):
		return strings.TrimSuffix(msg, "\n"+joined), kept, true

	default:
		return msg, nil, false
	}
}

// Positioned returns msg, the message of err along the cause chain of
// binding, with the line of each nested error replaced by the line its
// binding writes: the line as it is when a binding of its own wrote it,
// and otherwise with the position binding resolved for it in front. A
// message that does not end with its nested lines comes back as it is.
func (f Funcs) Positioned(binding, err error, msg string) string {
	head, nested, ok := f.Split(err, msg, binding)
	if !ok || len(nested) == 0 {
		return msg
	}

	lines := make([]string, 0, len(nested))

	for _, n := range nested {
		if n.Binding == nil {
			lines = append(lines, n.Text)
		} else {
			lines = append(lines, f.line(n.Binding, n.Err, n.Text))
		}
	}

	joined := strings.Join(lines, "\n")
	if head == "" {
		return joined
	}

	return head + "\n" + joined
}

// Line returns the line binding writes for the nested error n: the message
// of n with its own nested lines positioned, as [Funcs.Positioned] writes
// them, and the position n resolved to in front as "name:line:col:", where
// name is the source of binding. A nested error whose location did not
// resolve gets no position.
func (f Funcs) Line(binding, n error) string {
	return f.line(binding, n, n.Error())
}

// line is [Funcs.Line] for a nested error whose message text is known.
func (f Funcs) line(binding, n error, text string) string {
	text = f.Positioned(binding, n, text)

	if pos, ok := f.Position(binding, n); ok {
		bound, _ := f.Binding(binding)
		text = Prefix(FormatPosition(bound.Name, pos), text)
	}

	return text
}

// nested returns the nested errors along the cause chain of err, the
// innermost node's first, each with the binding that positions it, as
// [Funcs.Split] describes.
func (f Funcs) nested(err, binding error) []Nested {
	var (
		out        []Nested
		positioned bool
	)

	for cur := err; cur != nil && !f.Nothing(cur); {
		if bound, ok := f.Binding(cur); ok {
			binding, positioned = nil, false
			if !f.Rebound(cur) {
				binding, positioned = cur, true
			}

			cur = bound.Inner

			continue
		}

		if node, ok := f.Node(cur); ok {
			refs := make([]Nested, 0, len(node.Nested))

			for _, n := range node.Nested {
				if !f.Nothing(n) {
					refs = append(refs, Nested{Err: n, Binding: binding, Positioned: positioned})
				}
			}

			// The inner node's nested lines come first in the message.
			out = append(refs, out...)
			cur = node.Cause

			continue
		}

		wrapper, ok := cur.(interface{ Unwrap() error }) //nolint:errorlint // Walks the chain one node at a time.
		if !ok {
			break
		}

		cur = wrapper.Unwrap()
	}

	return out
}

// Errors returns errs as a slice of the error interface, for a
// [Funcs.Node] whose nested errors are of a concrete type.
func Errors[T error](errs []T) []error {
	out := make([]error, 0, len(errs))

	for _, err := range errs {
		out = append(out, err)
	}

	return out
}

// FormatPosition returns pos as "name:line:col:", the shape editors and
// build tools read, or "line:col:" when name is empty. Editors count from
// 1, so the coordinates are 1-indexed.
func FormatPosition(name string, pos position.Position) string {
	if name == "" {
		return fmt.Sprintf("%d:%d:", pos.Line+1, pos.Col+1)
	}

	return fmt.Sprintf("%s:%d:%d:", name, pos.Line+1, pos.Col+1)
}

// Prefix returns prefix and msg separated by a space, or prefix alone when
// msg is empty.
func Prefix(prefix, msg string) string {
	if msg == "" {
		return prefix
	}

	return prefix + " " + msg
}
