package niceyaml

import (
	"context"
	"errors"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/decodewalk"
	"go.jacobcolvin.com/niceyaml/internal/rejection"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/paths"
)

// rejectionLabel returns the sentinel a rejection of the given label
// matches, as [rejection.Classify] labels it: [ErrWrongKind],
// [ErrOverflow], or [ErrUnknownField], or nil for [rejection.None].
func rejectionLabel(label rejection.Label) error {
	switch label {
	case rejection.WrongKind:
		return ErrWrongKind
	case rejection.Overflow:
		return ErrOverflow
	case rejection.UnknownField:
		return ErrUnknownField
	default:
		return nil
	}
}

// rejectionLocation returns the options that locate a rejection the
// go-yaml decoder reported at tk, a token of the source.
//
// The first is the path of the node the decoder names by tk, as
// [pathindex.Index.OwnerPath] finds it, which starts at `$`. The
// error then binds where that path resolves, which is where an error at
// the same path from a [Validator] or a [SelfValidator] binds.
//
// A path can name a node it does not select, as the path of the earlier
// of two entries with one key does. A second option then gives the
// position of tk. The error binds there, and the path only names the
// node in the message.
//
// A node under a key with no name has no path, so the position of tk
// alone locates it.
func (n *Node) rejectionLocation(tk *token.Token) []ErrorOption {
	path, selects, ok := n.doc.pathIndex().OwnerPath(n.doc.pathResolver(), tk)

	switch {
	case !ok:
		return []ErrorOption{atToken(tk)}
	case selects:
		return []ErrorOption{AtPath(path)}
	default:
		return []ErrorOption{AtPath(path), atToken(tk)}
	}
}

// locateDecodeError returns err, the error a decode of node into v with
// cfg returned, at the path of the value that reported it. The go-yaml
// decoder returns the error of a value that decodes itself with no token
// of the source. Such a value is a [time.Duration] or has an unmarshaler,
// from a method or from an option of cfg, as
// [unmarshaler.Options.ReportsOwnError] lists them. To find that value,
// locateDecodeError walks the type of v beside node, as [errorLocator]
// describes, and decodes each such value again from its own node. The
// first one whose decode fails with err again, as [errorLocator.same]
// compares the two, reported it. The walk reads the entry the decoder
// reads for each field, so a field the decoder never read reports
// nothing.
//
// The result holds err under that path, as [Rebase] returns it, so the
// [SourceError] that binds the result reports the path and marks the
// value. The result matches [ErrDecode], and err stays in its chain, so
// the error of a value's own unmarshaler matches what it matched before
// too.
//
// An unmarshaler holds no [Node], so an [*Error] it returns writes an
// `@` path that reads from its own value, as the error of a
// [SelfValidator] does. The Rebase puts that path under the path of the
// value. It leaves a `$` path, a position, and a range as they are, so
// an err that carries one of those binds where it did before.
//
// The path of a value under the node the decode reads is never the root,
// with two exceptions. When node holds a scalar and the type of v
// decodes itself, the scalar is the value, so err points at node. When
// the decoder refuses node for the type of v, as
// [unmarshaler.Options.RefusesForText] reports, node is the value it
// refused.
//
// The decoder words that refusal for neither the value nor its kind, so
// the result then holds the rejection [textRejection] writes in place of
// err.
//
// An err that [Node.unplaced] reports false for comes back as it is,
// such as one the decode placed already. So does a binding, as [isBound]
// reports one, since it resolved its location in a source already. So
// does an err no value reproduces, and an `@` path in it then reads from
// node. A decode of node into any value reads no unmarshaler, so an err
// that such a decode returns too is the decoder's own, and it comes back
// as it is as well.
func (n *Node) locateDecodeError(
	ctx context.Context,
	err error,
	node ast.Node,
	v any,
	cfg decodeConfig,
) error {
	if err == nil || !n.unplaced(err) || isBound(err) {
		return err
	}

	dec := n.newDecoder(cfg)
	l := &errorLocator{
		ctx:     ctx,
		node:    n,
		decoder: dec,
		msg:     err.Error(),
		anchor:  anchorOf(err),
		places:  placesOf(err),

		unmarshalers: cfg.unmarshalers,
	}

	var sink any

	if l.reproduces(node, &sink) {
		return err
	}

	t := unmarshaler.Base(reflect.TypeOf(v).Elem())

	decodewalk.Walk(ctx, n.walkRules(ctx, dec, cfg.unmarshalers), t, node, astnode.Parent{}, l.visit)

	// With no value below node, the value is node itself.
	path := paths.Current()

	switch {
	case l.found != nil:
		path = l.found.Path()

	case !cfg.unmarshalers.ReportsOwnError(t) || !astnode.IsScalar(datapath.Deref(n.doc.pathResolver(), node)):
		return err
	}

	cause := l.refusal
	if cause == nil {
		cause = asDecodeError(n.doc.restoreError(err))
	}

	return Rebase(cause, path)
}

// unplaced reports whether the decode has yet to find the value err is
// about. It reports false for an err that matches [errPlaced], which
// [Node.rejection] or [decodeWithRecover] placed, for the error of a
// context that ended, and for a [yaml.Error] at a token of the source.
func (n *Node) unplaced(err error) bool {
	if errors.Is(err, errPlaced) || contextEnded(err) {
		return false
	}

	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.

	return !ok || !n.holdsToken(yamlErr.GetToken())
}

// lacksLocation reports whether err names no place at all, so it points
// at the value that reported it and nowhere else. It reports false for
// an err the decode placed, as [Node.unplaced] reports one. It also
// reports false when an error in the tree of err is a [*SourceError] or
// an [*Error] with a location, since those say where they point.
func (n *Node) lacksLocation(err error) bool {
	return n.unplaced(err) && !holdsLocation(err)
}

// holdsLocation reports whether err, or an error anywhere in the tree it
// unwraps to, is a [*SourceError] or an [*Error] that carries a location
// or comes from [Rebase]. The details of an Error count as part of that
// tree, though [Error.Unwrap] leaves them out.
func holdsLocation(err error) bool {
	switch x := err.(type) { //nolint:errorlint // Walks the tree one node at a time.
	case *SourceError:
		return x != nil

	case *Error:
		if x == nil {
			return false
		}

		return x.hasLocation() || x.move != nil ||
			slices.ContainsFunc(x.Unwrap(), holdsLocation) || slices.ContainsFunc(x.details, holdsLocation)

	case interface{ Unwrap() error }:
		return holdsLocation(x.Unwrap())

	case interface{ Unwrap() []error }:
		return slices.ContainsFunc(x.Unwrap(), holdsLocation)

	default:
		return false
	}
}

// textRejection returns the rejection of content, a mapping or a
// sequence the decoder refuses as [unmarshaler.Options.RefusesForText]
// reports. The error the decoder returns for it names neither the value
// nor its kind. The rejection matches [ErrDecode] and [ErrWrongKind] and
// reads "expected string, got sequence", as [rejection.ExpectedString]
// words it.
func textRejection(content ast.Node) error {
	return decodeError{
		err:   errors.New(rejection.ExpectedString(content.Type())),
		label: ErrWrongKind,
	}
}

// valueError returns err as an error about one value of a document, for
// the decode of that document to place. The err argument is what a
// decode of the text of the value returned before any Node bound it, as
// [Node.decodeValue] runs one. It is one problem, or the summary of
// several that [Node.decodeProblems] builds, and each problem comes back
// as [valueProblem] writes it.
func valueError(err error) error {
	summary, ok := err.(*Error) //nolint:errorlint // The decode returns its summary unwrapped.
	if !ok || summary == nil || len(summary.errors) == 0 {
		return valueProblem(err)
	}

	problems := make([]error, 0, len(summary.errors))
	for _, problem := range summary.errors {
		problems = append(problems, valueProblem(problem))
	}

	return NewSummary(summary.Error(), problems...)
}

// valueProblem returns problem as a problem of one value of a document,
// where problem is a problem of a decode of the text of that value.
//
// The decode locates a rejection by a token of the text. The path of
// that token reads from the root of the text, which is the value, so
// the result writes it as an `@` path. The decode of the document puts
// that path under the path of the value, as [Node.locateDecodeError]
// describes, and the path then resolves in the document, through each
// alias and `<<` merge key the text wrote out. A position counts the
// lines of the text and not of the document, so the result keeps none.
// A rejection of the value itself thus comes back with no location, and
// the decode of the document points it at the value it finds. So does an
// error the decode placed in the text, such as a panic, which the decode
// of the document has yet to place.
//
// Any other problem is the error of an unmarshaler below the value. Its
// `@` paths read from the value already, so it comes back as it is.
// When it names a position, as an unmarshaler that takes its node can
// build one, it comes back under the path it names, or under the path
// of the value, which stands over that position.
func valueProblem(problem error) error {
	x, ok := problem.(*Error) //nolint:errorlint // The decode returns the Error it located unwrapped.
	if !ok || x == nil || x.move != nil || !x.hasLocation() {
		at := anchorOf(problem)
		if at.loc == nil {
			return problem
		}

		return Place(problem, AtPath(at.path))
	}

	located := *x
	located.loc = nil

	switch placed := x.err.(type) { //nolint:errorlint // The decode placed the error it built.
	case decodeError:
		placed.placed = false
		located.err = placed

	case recoveredError:
		placed.placed = false
		located.err = placed
	}

	rel, _ := x.path.CutPrefix(paths.Doc())
	located.path, located.hasPath = rel, !rel.Equal(paths.Current())

	if located.addsNothing() {
		return located.err
	}

	return &located
}

// errorLocator finds the value that reported an error of a decode with
// no token. It walks the type the decode filled beside the node the
// decode read, as [decodewalk.Walk] pairs them, in the order the decoder
// reads them. The walk reads nothing below an interface, since the
// decoder calls no unmarshaler for the values it puts there.
//
// At each value whose type [unmarshaler.Options.ReportsOwnError] names,
// the locator decodes the node of the value into a new value of that
// type. A decode that fails with the error the locator looks for
// reproduces the error, as [errorLocator.same] compares the two. The
// walk then reads the fields, elements, or entries of that value the
// same way, as if they mirrored the document. A value that decodes
// itself through a second type with the same fields, such as an
// UnmarshalYAML that decodes into `type plain T`, returns the error of a
// field as it is, so the locator narrows to that field. The walk stops
// at the first value that reproduces the error with nothing below it
// that does. It stops too at a value whose node the decoder refuses, as
// [unmarshaler.Options.RefusesForText] reports, since the decoder reads
// nothing below that node.
//
// A path names an entry of a map by the text of its key, and the last
// entry with that text wins the path. The walk passes over an entry that
// loses its path to a later one, since no path points at it.
//
// The second decode runs the unmarshaler of each value the walk reaches
// again, on a value of its own, so the first decode keeps what it set.
// An unmarshaler that returns the same error for the same node gets the
// place of its node. One that reads state the first decode changed, such
// as a set of the names it has seen, may fail at another node or pass,
// so its error may come back at the wrong value or with no location. One
// that reads the value the caller put in the target before the decode
// decodes a zero value here, so its error may come back with no location
// too.
//
// One go-yaml decoder runs every decode of the walk, so the walk applies
// the options once.
type errorLocator struct {
	ctx     context.Context
	node    *Node
	decoder *yaml.Decoder
	// The value the walk found, or nil.
	found *decodewalk.Value
	// The message of the error to locate.
	msg string
	// The rejection of the value the walk found, as [textRejection]
	// writes it, or nil when that value reported the error itself.
	refusal error
	// The places the error to locate names, as [placesOf] lists them.
	places []locus
	// The types the options of the decode give an unmarshaler.
	unmarshalers unmarshaler.Options
	// The [*Error] in the error to locate that carries its location, and
	// that location, or the zero anchor when the error names no place.
	anchor anchor
}

// visit reads v, a value the walk reaches, and tells the walk where to
// go next. A value that decodes itself and reproduces the error is the
// one the locator found, unless a value below it reproduces the error
// too, so the walk goes below it. The walk ends at a value the decoder
// refuses, as [errorLocator.refuses] reports, and once it has left the
// values below the one it found. The value the walk starts at decodes
// in the decode itself, so only a refusal finds it there.
//
// A key of a map is the value when its type decodes itself and its
// decode reproduces the error. The walk reads nothing below a key, and
// passes over an entry no path points at.
func (l *errorLocator) visit(v *decodewalk.Value) decodewalk.Action {
	u := l.unmarshalers

	switch {
	case l.found != nil && v.Depth <= l.found.Depth:
		return decodewalk.Stop

	case v.Kind == decodewalk.Root:
		if l.refuses(v) {
			l.found = v

			return decodewalk.Stop
		}

		return decodewalk.Descend

	case v.Kind == decodewalk.Key:
		if v.Selected() && u.ReportsOwnError(v.Type) && l.reproduces(v.Node, reflect.New(v.Type).Interface()) {
			l.found = v

			return decodewalk.Stop
		}

		return decodewalk.Skip

	case !v.Named, v.Kind == decodewalk.Member && !v.Selected():
		return decodewalk.Skip

	case !u.ReportsOwnError(v.Type):
		return decodewalk.Descend

	case !l.reproduces(v.Node, reflect.New(v.Type).Interface()):
		return decodewalk.Skip
	}

	l.found = v

	if l.refuses(v) {
		return decodewalk.Stop
	}

	return decodewalk.Descend
}

// refuses reports whether the decoder refuses the node of v for its
// type, as [unmarshaler.Options.RefusesForText] reports. The value is
// then the one the walk looks for, since its decode calls no unmarshaler
// and reads nothing below the node, so refuses keeps the rejection
// [textRejection] writes for it.
func (l *errorLocator) refuses(v *decodewalk.Value) bool {
	content := datapath.Deref(l.node.doc.pathResolver(), v.Node)
	if !l.unmarshalers.RefusesForText(v.Type, content) {
		return false
	}

	l.refusal = textRejection(content)

	return true
}

// reproduces reports whether a decode of node into v, a pointer to a new
// value, fails with the error the locator looks for, as
// [errorLocator.same] compares the two. The decode reads node as a
// decode of that node alone does, with the anchors outside it that it
// refers to. A context that has ended reproduces nothing, so the walk
// ends with no location.
func (l *errorLocator) reproduces(node ast.Node, v any) bool {
	if l.ctx.Err() != nil {
		return false
	}

	view, err := l.node.prime(l.ctx, l.decoder, node)
	if err != nil {
		return false
	}

	err = decodeWithRecover(l.ctx, l.decoder, view, v)

	return err != nil && l.same(err)
}

// same reports whether again, the error of a second decode, is the error
// the locator looks for. An error that names no place is the same when
// it reads the same.
//
// An [*Error] an unmarshaler returns writes an `@` path that reads from
// the value of that unmarshaler, so the path goes under the value that
// wrote it and no other. Two errors that name a place are thus the same
// only when they name the same places, as [placesOf] lists them. A value
// that names one of its fields in an error of its own writes a path the
// field never wrote, so the walk does not narrow to the field, and the
// path goes under the value once.
//
// A value that puts text in front of the error of a value below it
// leaves that path as it is, and the path still reads from the value
// below. Two errors that name the same places are therefore the same too
// when the Error that carries the place reads the same, whatever text a
// wrapper puts around it. The walk then narrows through the wrapper to
// the value that wrote the path.
func (l *errorLocator) same(again error) bool {
	if !slices.EqualFunc(placesOf(again), l.places, sameLocus) {
		return false
	}

	if again.Error() == l.msg {
		return true
	}

	found := anchorOf(again)

	return found.err != nil && l.anchor.err != nil && found.err.Error() == l.anchor.err.Error()
}

// placesOf returns the place each error in the tree of err names: the
// one of err, of each problem it heads, and of each detail, in the order
// [ErrorTree.All] yields them. A place is the location [anchorOf] finds
// for the error, with the base of every [Rebase] above it in front of
// its path, and the zero locus for an error that names none. An error
// that heads several problems, such as a join, names no place itself, so
// the places of its problems tell it from another.
func placesOf(err error) []locus {
	var places []locus

	for node := range NewErrorTree(err).All() {
		places = append(places, anchorOf(node.Err).locus)
	}

	return places
}

// sameLocus reports whether a and b name the same place: the same path,
// or none, and the same position or range, or none.
func sameLocus(a, b locus) bool {
	return a.hasPath == b.hasPath && a.path.Equal(b.path) && a.loc == b.loc && a.ambiguous == b.ambiguous
}
