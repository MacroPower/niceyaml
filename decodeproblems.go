package niceyaml

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// bindDecodeProblems binds err, the rejection a decode of node into v
// with yamlOpts returned, to the source, with the other problems of that
// decode beside it.
//
// The go-yaml decoder decides whether a decode fails, and it returns one
// rejection for a document that holds several problems. That is the
// first value it rejects, in the order the target declares its fields,
// or an unknown field when it rejects no value. BindDecodeProblems runs
// only after the decoder rejected, and its result always holds that
// rejection as [Node.bindDecodeError] binds it, so no decode passes or
// fails because of it. A [problemCollector] finds the other problems.
//
// With no other problem, the result is the rejection as
// [Node.bindDecodeError] binds it. With more, it is a summary from
// [NewSummary] that heads the rejection and each other problem, in the
// order of the source. Its message counts them, as "3 problems" does,
// and reads "3 unknown fields" when each is the rejection of an unknown
// field. A path in a problem starts at `$` or reads from the scope of n,
// as a path in the rejection does, so the Node binds the summary as it
// binds the rejection alone.
//
// The collector marks each problem by a position, and the rejection
// stands for the problem at the position [Node.rejectedAt] gives it. A
// rejection with no such position comes back alone, since nothing tells
// it apart from a problem the collector finds. Every rejection comes
// back alone once ctx has ended, before the pass or during it, since the
// collector then stops before it has read the whole document. Returns
// nil for a nil err.
func (n *Node) bindDecodeProblems(
	ctx context.Context,
	err error,
	node ast.Node,
	v any,
	yamlOpts []yaml.DecodeOption,
) error {
	if err == nil {
		return nil
	}

	bound := n.bindDecodeError(err)

	at, ok := n.rejectedAt(err, bound)
	if !ok || ctx.Err() != nil {
		return bound
	}

	c := newProblemCollector(ctx, n, yamlOpts)
	c.collect(reflect.TypeOf(v).Elem(), node)

	delete(c.found, at)

	if len(c.found) == 0 || ctx.Err() != nil {
		return bound
	}

	unknown, ok := err.(*yaml.UnknownFieldError) //nolint:errorlint // A wrapped error is the unmarshaler's own.

	problems := make([]decodeProblem, 0, len(c.found)+1)
	problems = append(problems, decodeProblem{
		err:          n.decodeRejection(err),
		at:           at,
		unknownField: ok && n.holdsToken(unknown.Token),
	})

	for _, problem := range c.found {
		problems = append(problems, problem)
	}

	slices.SortFunc(problems, func(a, b decodeProblem) int {
		return cmp.Or(cmp.Compare(a.at.Line, b.at.Line), cmp.Compare(a.at.Col, b.at.Col))
	})

	errs := make([]error, 0, len(problems))
	unknownFields := true

	for _, problem := range problems {
		errs = append(errs, problem.err)
		unknownFields = unknownFields && problem.unknownField
	}

	msg := fmt.Sprintf("%d problems", len(errs))
	if unknownFields {
		msg = fmt.Sprintf("%d unknown fields", len(errs))
	}

	return n.bindOwn(NewSummary(msg, errs...))
}

// rejectedAt returns the position that marks err, a rejection of the
// decoder, among the problems a [problemCollector] finds, where bound is
// err as [Node.bindDecodeError] binds it. The decoder names a value it
// rejects by a token of the source, and the collector marks a problem it
// decodes by the same token. Any other rejection lies where its binding
// resolved it, which is where the collector marks a problem that takes a
// path.
//
// The bool result is false for a rejection that binds at no position,
// such as one the decoder reports with no token for a value that
// [Node.locateDecodeError] did not find. It is false too for a binding
// that n did not make, such as one an unmarshaler returns for a source
// of its own.
func (n *Node) rejectedAt(err, bound error) (position.Position, bool) {
	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if ok && n.holdsToken(yamlErr.GetToken()) {
		return position.NewFromToken(yamlErr.GetToken()), true
	}

	srcErr, ok := bound.(*SourceError) //nolint:errorlint // A binding below a wrapper is not one n made.
	if !ok || srcErr.Node() != n {
		return position.Position{}, false
	}

	return srcErr.Position()
}

// decodeRejection returns err, an error from the decoder, as
// [Node.bindDecodeError] binds it, before the binding. A [yaml.Error] at
// a token of the source comes back as [Node.tokenRejection] returns it.
// Any other error comes back as [asDecodeError] returns it, with a
// message that names each anchor as the document does, and keeps the
// location it carries.
func (n *Node) decodeRejection(err error) error {
	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if !ok || !n.holdsToken(yamlErr.GetToken()) {
		return asDecodeError(n.doc.decodeTree().restoreError(err))
	}

	return n.tokenRejection(yamlErr)
}

// tokenRejection returns err, a rejection the decoder reported at a
// token of the source, as an [*Error] that matches [ErrDecode]. The
// Error reads as [rejectionMessage] writes err, with each anchor named as
// the document names it, and carries the location
// [Node.rejectionLocation] gives the token. The path in that location
// starts at `$`.
func (n *Node) tokenRejection(err yaml.Error) error {
	msg := n.doc.decodeTree().rejectionText(err)
	rejected := decodeError{err: yamlMessageError{err: err, msg: msg}}

	return Invalid(rejected, n.rejectionLocation(err.GetToken())...)
}

// decodeProblem is one problem of a decode, before the Node binds it.
type decodeProblem struct {
	// The problem, located as [Node.decodeRejection] locates a rejection.
	err error
	// The position that marks the problem among the others, which is the
	// position of the value the problem is about.
	at position.Position
	// The problem is the rejection of an unknown field.
	unknownField bool
}

// problemCollector finds the problems of a decode beside the rejection
// the go-yaml decoder returned for it. The decoder reads the fields of a
// struct in the order the struct declares them and returns the first
// rejection among them, so a document that holds several problems shows
// one for each decode.
//
// The collector walks the type the decode filled beside the node the
// decode read. It pairs each field with the entry the decoder reads for
// it, by the rules an [unknownFieldFinder] follows, and each element and
// map value with its node. Where the walk reaches a leaf, it decodes the
// node of the leaf alone into a new value of the type of the leaf. A
// leaf is a value the walk reads nothing below. That is a scalar, or a
// node of a kind its type does not decode from, such as a sequence where
// a struct takes a mapping. The walk never decodes a mapping of the
// document into the struct or the map that reads it, or a sequence into
// its slice. A value thus costs the same however deep it lies, and the
// pass decodes no value below such a mapping or sequence a second time.
//
// A leaf adds a problem only for a rejection the decoder builds itself,
// with no code of the caller:
//
//   - A [yaml.TypeError], a [yaml.OverflowError], or a
//     [yaml.UnexpectedNodeTypeError] at a token of the source. Those are
//     a value of the wrong kind and a number out of range.
//   - The error of [time.ParseDuration] for a scalar in a
//     [time.Duration], which the decoder returns with no token.
//
// The walk reads nothing at or below a value that decodes itself, as
// [reportsOwnError] lists those types. Only the unmarshaler of such a
// value says what it accepts, and a second call can answer otherwise
// than the first, as one that counts the names it has seen does. A
// [time.Duration] and a [time.Time] are the exception, since the decoder
// parses both itself. The walk reads nothing below an interface or a
// [yaml.MapSlice] either, which take any value. It passes over a struct
// the decoder decodes no field of, as [unknownFieldFinder.fields]
// describes those.
//
// The walk cannot see every rule the decode applies, such as the types
// [yaml.CustomUnmarshaler] decodes, or the value the decoder holds for
// an anchor. So the value that reads a leaf confirms each problem, as
// [problemCollector.confirmed] describes. The collector keeps a problem
// only when the decoder rejects the leaf there as it rejected the leaf
// alone. A struct that a custom unmarshaler decodes thus judges its own
// fields. It judges nothing below them, so the collector can add a
// problem there that the unmarshaler accepts.
//
// The pass decodes one leaf at a time, so it calls no unmarshaler of a
// value the walk reads. A decode of the struct that reads a leaf can
// call code the walk does not pair with the leaf. That is a function
// from [yaml.CustomUnmarshaler], a [yaml.StructValidator], and the
// unmarshaler of another value that reads the entry of the leaf, as a
// field of the same name in an inline struct does. What those return
// adds no problem. A struct with no field that a probe decodes reaches a
// StructValidator too.
//
// When the decoder rejects unknown fields, an [unknownFieldFinder] adds
// the rejection of each one the document holds.
//
// The collector marks each problem by a position and keeps the first it
// finds there, so a value that several aliases reach is one problem.
//
// One go-yaml decoder runs every decode of the pass, so the pass applies
// the options once. The decoder holds the anchors of the node as the
// decode read them, as [problemCollector.register] leaves them.
//
// Create instances with [newProblemCollector].
type problemCollector struct {
	ctx      context.Context
	node     *Node
	resolver *paths.Resolver
	tree     *decodeTree
	decoder  *yaml.Decoder
	finder   *unknownFieldFinder
	// The problems found, by the position that marks each.
	found map[position.Position]decodeProblem
	// The mappings and sequences the walk has read, each as one type, so
	// it reads a value that several aliases reach once for each type.
	visited map[valueVisit]bool
}

// valueVisit names a mapping or a sequence the walk has read, by the
// type it read the node as. The fields together form the key of the set
// the [problemCollector] keeps.
//
//nolint:unused // The fields tell the keys of the set apart.
type valueVisit struct {
	typ  reflect.Type
	node ast.Node
}

// holder names the value that reads a leaf, which confirms a problem of
// the leaf, as [problemCollector.confirmed] describes. The zero value
// stands for the value the walk starts at, which nothing holds.
type holder struct {
	// The type of the struct that reads the leaf as a field, or nil. For
	// a field of an inline struct, it is the type of the struct the
	// decoder decodes the mapping into, which holds the inline struct.
	typ reflect.Type
	// The entry of the mapping of that struct the field reads.
	entry *ast.MappingValueNode
	// A sequence or a map reads the leaf, as an element or a value.
	element bool
	// The struct holds an inline field that decodes itself, as
	// [inlinesUnmarshaler] reports, so no decode of it confirms the leaf.
	unmarshals bool
}

// The result of [inlinesUnmarshaler] for each struct type it has read.
var inlineUnmarshalers typeCache[bool]

// inlinesUnmarshaler reports whether t, a struct type, holds an inline
// field that decodes itself, as [reportsOwnError] lists those types, in
// its own fields or in those of a struct it holds inline. A decode of t
// calls the unmarshaler of that field whatever the mapping holds.
func inlinesUnmarshaler(t reflect.Type) bool {
	return inlineUnmarshalers.get(t, func(t reflect.Type) bool {
		return holdsInlineUnmarshaler(t, map[reflect.Type]bool{})
	})
}

// holdsInlineUnmarshaler is [inlinesUnmarshaler] without its cache. The
// seen set holds the structs the walk is inside of, so a struct that
// holds itself inline ends the walk.
func holdsInlineUnmarshaler(t reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[t] {
		return false
	}

	seen[t] = true

	for field := range t.Fields() {
		_, inline, skip := yamlfield.Name(field)
		if skip || !inline || yamlfield.ReadsAnchor(field) {
			continue
		}

		inner := pointerBase(field.Type)
		if reportsOwnError(inner) || inner.Kind() == reflect.Struct && holdsInlineUnmarshaler(inner, seen) {
			return true
		}
	}

	return false
}

// newProblemCollector creates a new [*problemCollector] for a decode of
// n with yamlOpts.
func newProblemCollector(ctx context.Context, n *Node, yamlOpts []yaml.DecodeOption) *problemCollector {
	dec := yaml.NewDecoder(bytes.NewReader(nil), yamlOpts...)

	return &problemCollector{
		ctx:      ctx,
		node:     n,
		resolver: n.doc.pathResolver(),
		tree:     n.doc.decodeTree(),
		decoder:  dec,
		finder:   newUnknownFieldFinder(ctx, n, dec),
		found:    map[position.Position]decodeProblem{},
		visited:  map[valueVisit]bool{},
	}
}

// collect finds the problems of a decode of node into a value of type
// t: the leaves the decoder rejects, and the unknown fields, when the
// decoder rejects those.
func (c *problemCollector) collect(t reflect.Type, node ast.Node) {
	c.register(node)
	c.walk(t, node, place{}, holder{})

	if !c.rejectsUnknownFields() {
		return
	}

	c.finder.walk(t, node, nil)

	for _, field := range c.finder.found {
		c.add(decodeProblem{
			err:          c.node.tokenRejection(field),
			at:           position.NewFromToken(field.Token),
			unknownField: true,
		})
	}
}

// register has the decoder of the pass read the anchors of node, the
// node the decode read, as the decoder of the decode read them.
//
// The go-yaml decoder reads a node as plain values before it decodes it
// into a target. It keeps the value of each anchor, and assigns that
// value to the target of an alias that takes it, with no decode. So the
// pass reads node into an interface once, after the anchors outside node
// that node refers to, as [Node.decodeNode] reads them. Every later
// decode of the pass then reads an alias as the decode did.
//
// The decoder stops that read at a node it cannot read as a plain value,
// such as a tag that does not convert its value, and the decode returned
// that rejection. The anchors from that node on stay unknown to the
// decoder of the pass, as they did to the decoder of the decode. A value
// that reads one of them through an alias adds no problem, since its
// decode fails for the alias.
func (c *problemCollector) register(node ast.Node) {
	view := c.tree.view(node)

	if node != c.node.doc.root.Body && c.node.primeAnchors(c.ctx, c.decoder, view) != nil {
		return
	}

	var sink any

	_ = decodeWithRecover(c.ctx, c.decoder, view, &sink) //nolint:errcheck // The decode returned this rejection.
}

// rejectsUnknownFields reports whether the decoder of the pass rejects a
// key that no field of a struct reads, as [yaml.DisallowUnknownField]
// makes it. An option tells nothing about itself, so the collector asks
// the decoder, with a decode of a mapping of one key into a struct with
// no field. The key is the empty string. Only the empty prefix from
// [yaml.AllowFieldPrefixes] allows that key, and it allows every other
// key too.
func (c *problemCollector) rejectsUnknownFields() bool {
	tk := token.String("", `""`, &token.Position{Line: 1, Column: 1})
	probe := ast.Mapping(tk, false, ast.MappingValue(tk, ast.String(tk), ast.Null(tk)))

	var shell struct{}

	err := decodeWithRecover(c.ctx, c.decoder, probe, &shell)

	_, unknown := err.(*yaml.UnknownFieldError) //nolint:errorlint // The decoder returns it unwrapped.

	return unknown
}

// add keeps problem, unless the collector holds one at the same position
// already.
func (c *problemCollector) add(problem decodeProblem) {
	if _, held := c.found[problem.at]; !held {
		c.found[problem.at] = problem
	}
}

// visit records that the walk reads node, a mapping or a sequence, as a
// value of type t, and reports whether it had not read it so before.
func (c *problemCollector) visit(t reflect.Type, node ast.Node) bool {
	visit := valueVisit{typ: t, node: node}
	if c.visited[visit] {
		return false
	}

	c.visited[visit] = true

	return true
}

// walk reads held, the node the document holds for a value of type t at
// the place at, and adds the problems at and below that value. The in
// argument names the struct that reads the value as a field, if any.
// The decoder reads nothing from a null, as [readNode] describes, so the
// walk stops there. It stops too at an alias the document cannot follow,
// such as one to an anchor of a reference document, since the document
// holds no node for the value behind it.
func (c *problemCollector) walk(t reflect.Type, held ast.Node, at place, in holder) {
	if c.ctx.Err() != nil {
		return
	}

	node, ok := readNode(c.resolver, held)
	if !ok {
		return
	}

	t = pointerBase(t)

	if t == durationType || t == timeType {
		c.leaf(t, held, node, at, in)

		return
	}

	if reportsOwnError(t) || t.Kind() == reflect.Interface || t == mapSliceType {
		return
	}

	switch content := contentNode(c.resolver, node).(type) {
	case *ast.MappingNode:
		switch {
		case t.Kind() == reflect.Struct:
			c.fields(t, content, at)

			return

		case t.Kind() == reflect.Map:
			c.entries(t, content, at)

			return
		}

	case *ast.SequenceNode:
		if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
			c.elements(t, content, at)

			return
		}
	}

	c.leaf(t, held, node, at, in)
}

// fields walks the value of each field of t, a struct type that decodes
// from mapping at the place at. It reads no field where the decoder
// decodes none, as [unknownFieldFinder.fields] describes.
func (c *problemCollector) fields(t reflect.Type, mapping *ast.MappingNode, at place) {
	if !c.visit(t, mapping) || !c.finder.readsAsStruct(mapping) {
		return
	}

	merges := !yamlfield.IgnoresMerges(t)
	if merges && c.finder.mergeRefused(mapping) {
		return
	}

	in := holder{typ: t, unmarshals: inlinesUnmarshaler(t)}

	c.below(in, t, mapping, merges, at, map[reflect.Type]bool{})
}

// below walks the value of each field of t. That is the struct type in
// names, which decodes from mapping at the place at, or a struct that
// one holds inline. A field reads the entry [unknownFieldFinder.entry]
// finds for its name, and an inline field reads mapping itself, as
// [unknownFieldFinder.below] describes. An inline struct reads its own
// fields from it, and an inline map reads every entry, as
// [problemCollector.inlineMap] walks them. The inlined set holds the
// inline structs the walk is inside of, so a struct that holds itself
// inline ends the walk.
func (c *problemCollector) below(
	in holder, t reflect.Type, mapping *ast.MappingNode, merges bool, at place, inlined map[reflect.Type]bool,
) {
	for field := range t.Fields() {
		name, inline, skip := yamlfield.Name(field)
		if skip || yamlfield.ReadsAnchor(field) {
			continue
		}

		if !inline {
			if entry := c.finder.entry(mapping, name, merges); entry != nil {
				in.entry = entry
				c.walk(field.Type, entry.Value, at.child(name), in)
			}

			continue
		}

		inner := pointerBase(field.Type)

		switch {
		case reportsOwnError(inner):
			// The unmarshaler of the field reads the mapping.

		case inner.Kind() == reflect.Map:
			c.inlineMap(in, inner, mapping, merges, at)

		case inner.Kind() == reflect.Struct && !inlined[inner]:
			inlined[inner] = true
			c.below(in, inner, mapping, merges, at, inlined)
			delete(inlined, inner)
		}
	}
}

// inlineMap walks the value of each entry that the struct in names reads
// from mapping at the place at, as a value of t, the type of a map that
// struct holds inline. The decoder hands an inline map every entry it
// gathers for the struct, the entries of the other fields among them, in
// no fixed order. So one decode rejects one of the values the map does
// not take, and not always the same one.
func (c *problemCollector) inlineMap(in holder, t reflect.Type, mapping *ast.MappingNode, merges bool, at place) {
	c.finder.eachEntry(mapping, merges, map[*ast.MappingNode]bool{}, func(entry *ast.MappingValueNode) {
		name, ok := c.resolver.KeyName(entry.Key)
		if ok && c.finder.entry(mapping, name, merges) == entry {
			in.entry = entry
			c.walk(t.Elem(), entry.Value, at.child(name), in)
		}
	})
}

// elements walks each element of seq, which a slice or an array of type
// t decodes from at the place at.
func (c *problemCollector) elements(t reflect.Type, seq *ast.SequenceNode, at place) {
	if !c.visit(t, seq) {
		return
	}

	for i, element := range seq.Values {
		c.walk(t.Elem(), element, at.index(i), holder{element: true})
	}
}

// entries walks the value of each entry of mapping, which a map of type
// t decodes from at the place at, and of each entry its `<<` merge keys
// bring in. It passes over an entry whose key has no name, as
// [paths.Resolver.KeyName] reports, since no path spells its place.
func (c *problemCollector) entries(t reflect.Type, mapping *ast.MappingNode, at place) {
	if !c.visit(t, mapping) {
		return
	}

	c.finder.eachEntry(mapping, true, map[*ast.MappingNode]bool{}, func(entry *ast.MappingValueNode) {
		if name, ok := c.resolver.KeyName(entry.Key); ok {
			c.walk(t.Elem(), entry.Value, at.child(name), holder{element: true})
		}
	})
}

// leaf decodes node into a new value of type t and adds the rejection
// the decoder builds for it as a problem. The walk reads nothing below
// the value, and held is the node the document holds for it, which node
// is the content of.
//
// A rejection at a token of the source takes the location of that token,
// as [Node.tokenRejection] gives it. The error of a parse has no token,
// so it takes the path the walk reached the value by, as
// [Node.locateDecodeError] puts one under the path of its value. It adds
// a problem only when that path selects held, which the path of an entry
// that a later one hides does not.
//
// Any other error adds nothing. That is another rejection of the
// decoder, such as one for a tag that does not convert its value, the
// error of a type that only [yaml.CustomUnmarshaler] decodes, or a panic
// that [decodeWithRecover] placed.
func (c *problemCollector) leaf(t reflect.Type, held, node ast.Node, at place, in holder) {
	err := c.decode(node, reflect.New(t).Interface())
	if err == nil {
		return
	}

	if rejected, ok := builtRejection(err); ok {
		tk := rejected.GetToken()
		if c.node.holdsToken(tk) && c.confirmed(err, t, held, in) {
			c.add(decodeProblem{err: c.node.tokenRejection(rejected), at: position.NewFromToken(tk)})
		}

		return
	}

	// The decoder parses the text of a scalar as a duration, and returns
	// the error of that parse as it is.
	_, isYAML := err.(yaml.Error) //nolint:errorlint // The decoder returns it unwrapped.
	if isYAML || errors.Is(err, errPlaced) || t != durationType || !isScalar(contentNode(c.resolver, node)) {
		return
	}

	path := at.path()

	loc, lerr := c.node.pathLocation(path)
	if lerr != nil || loc.tk != astnode.FirstToken(held) || !c.confirmed(err, t, held, in) {
		return
	}

	c.add(decodeProblem{err: Rebase(asDecodeError(c.tree.restoreError(err)), path), at: loc.pos})
}

// builtRejection returns err as a rejection the decoder builds itself
// for a value it reads with no code of the caller: a value of the wrong
// kind, or a number out of range. The bool result is false for any other
// error.
func builtRejection(err error) (yaml.Error, bool) {
	switch rejected := err.(type) { //nolint:errorlint // The decoder returns these types unwrapped.
	case *yaml.TypeError:
		return rejected, true
	case *yaml.OverflowError:
		return rejected, true
	case *yaml.UnexpectedNodeTypeError:
		return rejected, true
	default:
		return nil, false
	}
}

// decode decodes node, a node of the document, into v with the decoder
// of the pass, as the [decodeTree] of the document reads the node.
func (c *problemCollector) decode(node ast.Node, v any) error {
	return decodeWithRecover(c.ctx, c.decoder, c.tree.view(node), v)
}

// confirmed reports whether the value that reads a leaf rejects it as
// err, the rejection a decode of the leaf alone into a value of type t
// returned, where held is the node the document holds for the leaf.
//
// A struct reads the leaf from a mapping that holds the entry of its
// field alone, into a new value of the type of that struct, as the
// [decodeTree] of the document reads the entry. The decoder thus applies
// what the walk cannot see, such as a [yaml.CustomUnmarshaler] for the
// struct. A struct that holds an inline field that decodes itself
// confirms nothing, since any decode of it calls that unmarshaler.
//
// A sequence or a map reads the leaf as the one element of a sequence,
// which decodes into a slice of the type of the leaf. The decoder reads
// a field, an element, and a map value alike, and it reads an alias
// otherwise than the content of its anchor. Where the value it holds for
// the anchor fits the target, it assigns that value and decodes nothing.
// A leaf that fails alone can thus pass through its alias.
//
// The value the walk starts at decodes alone in the decode itself, so
// nothing more confirms it.
func (c *problemCollector) confirmed(err error, t reflect.Type, held ast.Node, in holder) bool {
	var (
		node   ast.Node
		target reflect.Value
	)

	switch {
	case in.unmarshals:
		return false

	case in.typ != nil:
		view := c.finder.view(in.entry)
		if view == nil || view.Key == nil {
			return false
		}

		node, target = ast.Mapping(view.Start, false, view), reflect.New(in.typ)

	case in.element:
		elements := ast.Sequence(held.GetToken(), false)
		elements.Values = append(elements.Values, c.tree.view(held))

		node, target = elements, reflect.New(reflect.SliceOf(t))

	default:
		return true
	}

	return sameRejection(err, decodeWithRecover(c.ctx, c.decoder, node, target.Interface()))
}

// sameRejection reports whether again, the error of a second decode, is
// err, the rejection of a first one. Two rejections at a token are the
// same when they have one type and name one token. Two errors with no
// token are the same when they read the same.
func sameRejection(err, again error) bool {
	if again == nil {
		return false
	}

	want, tokened := err.(yaml.Error)      //nolint:errorlint // The decoder returns it unwrapped.
	got, alsoTokened := again.(yaml.Error) //nolint:errorlint // The decoder returns it unwrapped.

	if !tokened || !alsoTokened {
		return tokened == alsoTokened && err.Error() == again.Error()
	}

	return reflect.TypeOf(want) == reflect.TypeOf(got) && want.GetToken() == got.GetToken()
}
