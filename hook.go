package niceyaml

import (
	"context"
	"reflect"
	"slices"

	"go.jacobcolvin.com/niceyaml/internal/excerpt"
	"go.jacobcolvin.com/niceyaml/internal/hook"
)

// The calls that niceyaml hands to its manifest and schema packages, as
// the package [hook] describes.
//
//nolint:gochecknoinits // The hooks reach no API, so only init can set them.
func init() {
	hook.Tally = newTally
	hook.MarkWarning = func(err error) error { return remark(err, severityWarning) }
	hook.Warn = func(ctx context.Context, err error) {
		if !isNothing(err) {
			deliver(ctx, remark(err, severityWarning))
		}
	}
	hook.Rewrap = rewrap
	hook.WorkingSource = func(text string, home, us any, locate func(hook.Located) hook.Place) any {
		n, _ := home.(*Node)       //nolint:errcheck // The hook takes a *Node.
		u, _ := us.([]Unmarshaler) //nolint:errcheck // The hook takes a []Unmarshaler.

		return newWorkingSource(text, n, u, locate)
	}
	hook.Reread = func(src any, text string) any {
		s, _ := src.(*Source) //nolint:errcheck // The hook takes a *Source.

		return newStringSource(text, s.sourceConfig)
	}
	hook.Limits = func(src any) (int, int) {
		s, _ := src.(*Source) //nolint:errcheck // The hook takes a *Source.

		return s.bytes(), s.depth()
	}
	hook.Defaults = func(t reflect.Type, opts any) reflect.Value {
		o, _ := opts.([]DecodeOption) //nolint:errcheck // The hook takes the options of a decode.

		return newDecodeConfig(nil, o).defaults.value(t)
	}
	hook.Layers = func(src any) []any {
		s, _ := src.(*Source) //nolint:errcheck // The hook takes a *Source.

		return s.layerNodes()
	}
}

// relocation sends each error bound in a working [Source] to the document
// that the working text stands for.
type relocation struct {
	// The document the working text stands for, where each error binds.
	home *Node
	// Gives the place in home of a location in the working text.
	locate func(hook.Located) hook.Place
}

// newWorkingSource returns a [Source] of text that stands for home, with
// the unmarshalers us ahead of those of home, as [hook.WorkingSource]
// describes.
func newWorkingSource(text string, home *Node, us []Unmarshaler, locate func(hook.Located) hook.Place) *Source {
	cfg := home.source.sourceConfig
	cfg.noLimits = true
	cfg.unmarshalers = slices.Concat(us, cfg.unmarshalers)

	src := newStringSource(text, cfg)
	src.relocation = &relocation{home: home, locate: locate}

	return src
}

// relocate moves e, bound in a working Source at the location that found
// gave it, to the home document of r. It returns the details that the
// place of the location adds.
func (e *SourceError) relocate(r *relocation, found anchor) []error {
	var at hook.Located

	if found.hasPath {
		at.Path, at.HasPath = found.path, true
	}

	if e.locErr == nil && checkInRange(e.loc, e.source.lines) == nil {
		at.Range, at.HasRange = excerpt.Cover(e.loc.highlight(e.source.lines), e.loc.pos), true
	}

	place := r.locate(at)

	e.source, e.node = r.home.source, r.home

	switch {
	case place.HasRange:
		rng := place.Range
		e.loc, e.locErr = location{pos: rng.Start, rng: &rng}, nil

	case e.locErr == nil:
		e.loc, e.locErr = location{}, errUnlocated
	}

	// The path that the message shows names the value as the home
	// document spells it.
	if place.HasPath && found.hasPath {
		moved := &Error{err: e.err}
		moved.path, moved.hasPath = place.Path, true
		e.err = moved
	}

	return place.Details
}

// addRelocatedDetails binds each of details in the home document of r and
// adds it to e as a detail.
func (e *SourceError) addRelocatedDetails(r *relocation, details []error) {
	for _, d := range details {
		e.addChild(d, binder{src: r.home.source, node: r.home, locate: true}, childBase{}, true)
	}
}

// rewrap returns err, a binding, with wrap applied to the error it binds,
// as [hook.Rewrap] describes.
func rewrap(err error, wrap func(error) error) error {
	x, ok := err.(*SourceError) //nolint:errorlint // The node itself, not a chain search.
	if !ok || x == nil {
		return wrap(err)
	}

	c := &SourceError{
		err:     wrap(x.err),
		locErr:  x.locErr,
		source:  x.source,
		node:    x.node,
		loc:     x.loc,
		end:     x.end,
		ranges:  x.ranges,
		below:   x.below,
		rng:     x.rng,
		adopted: x.adopted,
	}

	if x.free != nil {
		c.free = wrap(x.free)
	}

	return c
}

// layerNodes returns the document of each layer that s merges, lowest
// first, or nil for a Source that merges none.
func (s *Source) layerNodes() []any {
	if s == nil || s.layers == nil {
		return nil
	}

	nodes := make([]any, 0, len(s.layers.layers))
	for _, l := range s.layers.layers {
		if l.node != nil {
			nodes = append(nodes, l.node)
		}
	}

	return nodes
}
