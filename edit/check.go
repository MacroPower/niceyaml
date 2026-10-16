package edit

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// begin starts the calls of an Apply that messages name label, or the
// edit of a type field.
func (d *Doc) begin(label string) {
	d.callErr = nil
	d.label = label
	d.written = nil
	d.editedPlaces = nil
	d.merges = d.mergeViews(nil)
	d.lastEdit = nil
}

// endCall ends one call of a [Func] on the node n, and takes err, the
// error that the Func returned. It returns the problem that fails the
// Apply. It binds err at n and returns it, or else returns the problem
// of a method that could not make its edit. Without either, it
// returns nil.
func (d *Doc) endCall(n *Node, err error) error {
	callErr := d.callErr
	d.callErr = nil

	if err != nil {
		return n.bind(err)
	}

	return callErr
}

// check checks the text after the calls of an Apply return. Each mapping
// that merges or repeats a key must read the entries that it read before.
// The text must decode, and each value that a call wrote must read back as
// written. A difference in the decode shows a bug in the engine, and the
// problem names the value.
func (d *Doc) check(ctx context.Context) error {
	written := d.written
	d.written = nil

	err := d.checkMerges()
	if err != nil {
		return err
	}

	_, err = d.node.Decode[any](ctx, niceyaml.WithSelfValidation(false))
	if err != nil {
		return d.bug(d.rootNode(), err)
	}

	for _, w := range written {
		n := &Node{d: d, path: w.path}

		v, err := d.node.At(w.path)
		if err != nil {
			continue
		}

		got, err := v.Decode[any](ctx, niceyaml.WithSelfValidation(false))
		if err != nil {
			return d.bug(n, err)
		}

		want, err := canonical(ctx, w.value)
		if err != nil {
			continue
		}

		if !reflect.DeepEqual(got, want) {
			return d.bug(n, fmt.Errorf("%s reads %v, not %v", w.path, got, want))
		}
	}

	return nil
}

// invalidate drops each value that an earlier call wrote and that an edit
// at path changes: a value at path, below it, or above it.
func (d *Doc) invalidate(path paths.Path) {
	d.written = slices.DeleteFunc(d.written, func(w writtenValue) bool {
		_, below := w.path.CutPrefix(path)
		_, above := path.CutPrefix(w.path)

		return below || above
	})
}

// rootNode returns a node on the root, which binds a problem of the whole
// text.
func (d *Doc) rootNode() *Node {
	return &Node{d: d, path: paths.Doc()}
}

// canonical returns v as a decode into any reads the text that the encoder
// writes for it.
func canonical(ctx context.Context, v any) (any, error) {
	data, err := encoder.Marshal(ctx, v)
	if err != nil {
		return nil, fmt.Errorf("encode the value: %w", err)
	}

	var out any

	err = yaml.Unmarshal(data, &out)
	if err != nil {
		return nil, fmt.Errorf("decode the value: %w", err)
	}

	return out, nil
}

// editedPlace is a place that a call of the Apply running edited.
type editedPlace struct {
	// The alias or the merge key that the call went through, as the text
	// writes it, or "" for a call that reached the place directly.
	via string
	// The path of the place, and its range in the user's text.
	path paths.Path
	at   position.Range
	// The range of via in the user's text.
	viaAt position.Range
}

// editOnce records an edit of p through n. It returns a problem when an
// earlier call of the Apply edited the place too, and one of the two
// calls reached it through an alias or a merge key. An Apply that reaches
// a shared value from several calls thus never edits it twice.
func (d *Doc) editOnce(n *Node, p *place) error {
	start, ok := d.startOf(p)
	if !ok || start >= len(d.origin) {
		return nil
	}

	key := d.origin[start]
	cur := d.editedPlace(n, p)

	prev, seen := d.editedPlaces[key]
	if !seen {
		if d.editedPlaces == nil {
			d.editedPlaces = make(map[int]editedPlace)
		}

		d.editedPlaces[key] = cur

		return nil
	}

	if prev.via == "" && cur.via == "" {
		return nil
	}

	at := cur
	if at.via == "" {
		at = prev
	}

	first := d.user.Bind(niceyaml.NewError(d.subject()+" changes it here first",
		niceyaml.AtPath(prev.path), niceyaml.AtRange(prev.at)))

	err := d.user.Bind(niceyaml.NewError(d.subject()+" changes this value a second time",
		niceyaml.AtPath(cur.path), niceyaml.AtRange(at.viaAt),
		niceyaml.WithDetails(first, fmt.Errorf("write the value out in place of %s", at.via)),
	))
	d.record(err)

	return err //nolint:wrapcheck // The binding is the problem of the Apply.
}

// editedPlace returns the record of an edit of p through n.
func (d *Doc) editedPlace(n *Node, p *place) editedPlace {
	vs, ve := d.valueStart(p), d.valueEnd(p)
	at := d.userRange(vs, max(vs, ve))
	e := editedPlace{path: d.userPath(n.path, at.Start), at: at}

	if p.via == nil {
		return e
	}

	// The value lies under another path in the text, so the path of the
	// node names the place that the call reached.
	e.path = n.path

	if s, ok := d.nodeSpan(p.via); ok {
		e.via = strings.TrimSpace(d.cur[s.start:s.end])
		e.viaAt = d.userRange(s.start, s.end)
	}

	return e
}
