package niceyaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
)

var (
	// The go-yaml decoder parses a [time.Duration] from the text of a
	// scalar and returns the error of [time.ParseDuration] as it is.
	durationType = reflect.TypeFor[time.Duration]()

	// The interface go-yaml decodes a value through under
	// [WithJSONUnmarshalers].
	jsonUnmarshalerType = reflect.TypeFor[interface{ UnmarshalJSON(data []byte) error }]()

	// The types [kindOfType] names apart from their kind. The go-yaml
	// decoder reads a !!timestamp tag as a [time.Time], a !!binary tag as
	// a byte slice, and a mapping as a [yaml.MapSlice] under
	// [WithYAMLOrderedMaps].
	timeType     = reflect.TypeFor[time.Time]()
	bytesType    = reflect.TypeFor[[]byte]()
	mapSliceType = reflect.TypeFor[yaml.MapSlice]()
)

// The names a rejection gives the kinds of YAML values, as
// [rejectionMessage] writes them.
const (
	kindMapping  = "mapping"
	kindSequence = "sequence"
	kindString   = "string"
	kindInteger  = "integer"
	kindFloat    = "float"
	kindBoolean  = "boolean"
	kindNull     = "null"

	// The name of a value whose kind its type does not tell, as for a
	// value an interface holds.
	kindValue = "value"

	// The name a target takes when no YAML value but a null decodes into
	// it, as for a channel.
	kindNone = "no value"
)

// rejectionMessage returns the message of err, a rejection the go-yaml
// decoder returned, worded for the document. The decoder words three of
// its errors for the Go target, and names the outermost struct field on
// the way to the value rather than the field that holds it.
// Those errors carry what they compare in typed fields, so
// rejectionMessage writes the message from the fields:
//
//   - A value of the wrong kind, a [yaml.TypeError] or a
//     [yaml.UnexpectedNodeTypeError], reads "expected integer, got
//     string", with the kinds [kindOfType] and [kindOfNode] name.
//   - A number that overflows an integer type, a [yaml.OverflowError],
//     reads "expected integer from -128 to 127, got 300", with the range
//     of the type.
//
// The decoder also rejects a value by its Go type alone, as when it hands
// the value of an anchor to an inline field with an alias option, or
// fills a pointer to a pointer. The two types can then hold one kind of
// value, as two struct types do, and an anchor the decoder read into an
// interface has a type that names no kind. Kinds alone would read
// "expected mapping, got mapping", so the message reads "expected
// mapping, got mapping of another type" and names neither type.
//
// Any other error keeps the message the decoder gave it, such as the one
// for an unknown field, which names the field as the document does.
func rejectionMessage(err yaml.Error) string {
	switch e := err.(type) { //nolint:errorlint // The decoder returns these types unwrapped.
	case *yaml.TypeError:
		want, got := kindOfType(e.DstType), kindOfType(e.SrcType)
		if want == got || got == kindValue {
			return fmt.Sprintf("expected %s, got %s of another type", want, got)
		}

		return fmt.Sprintf("expected %s, got %s", want, got)

	case *yaml.UnexpectedNodeTypeError:
		return fmt.Sprintf("expected %s, got %s", kindOfNode(e.Expected), kindOfNode(e.Actual))

	case *yaml.OverflowError:
		lo, hi, ok := integerRange(e.DstType)
		if !ok {
			return fmt.Sprintf("expected %s, got %s", kindOfType(e.DstType), e.SrcNum)
		}

		return fmt.Sprintf("expected %s from %s to %s, got %s", kindInteger, lo, hi, e.SrcNum)

	default:
		return err.GetMessage()
	}
}

// kindOfType returns the kind of YAML value that the Go type t holds, for
// the message of a rejection. The type is the target of a decode, or the
// type the go-yaml decoder read a value of the document as. A nil type
// is the null the decoder reads as a nil value. A pointer names what it
// points to.
//
// The result never names a Go type. An interface holds a value of any
// kind, so it reads [kindValue]. No YAML value but a null decodes into a
// channel, a function, a complex number, or an unsafe pointer, so a
// target of one of those kinds reads [kindNone].
func kindOfType(t reflect.Type) string {
	if t == nil {
		return kindNull
	}

	t = pointerBase(t)

	switch t {
	case timeType:
		return "timestamp"
	case durationType:
		return "duration"
	case bytesType:
		return "binary"
	case mapSliceType:
		return kindMapping
	}

	switch t.Kind() {
	case reflect.Bool:
		return kindBoolean
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return kindInteger
	case reflect.Float32, reflect.Float64:
		return kindFloat
	case reflect.String:
		return kindString
	case reflect.Slice, reflect.Array:
		return kindSequence
	case reflect.Map, reflect.Struct:
		return kindMapping
	case reflect.Interface:
		return kindValue
	default:
		return kindNone
	}
}

// kindOfNode returns the kind of YAML value a node of type t holds, for
// the message of a rejection. A type that holds no value, such as an
// anchor, has the name go-yaml gives it.
func kindOfNode(t ast.NodeType) string {
	switch t {
	case ast.MappingType, ast.MappingValueType:
		return kindMapping
	case ast.SequenceType:
		return kindSequence
	case ast.StringType, ast.LiteralType:
		return kindString
	case ast.IntegerType:
		return kindInteger
	case ast.FloatType, ast.InfinityType, ast.NanType:
		return kindFloat
	case ast.BoolType:
		return kindBoolean
	case ast.NullType:
		return kindNull
	default:
		return t.YAMLName()
	}
}

// integerRange returns the lowest and the highest value of the integer
// type t in decimal, or false for a type of any other kind.
func integerRange(t reflect.Type) (string, string, bool) {
	if t == nil {
		return "", "", false
	}

	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		shift := 64 - t.Bits()

		return strconv.FormatInt(math.MinInt64>>shift, 10), strconv.FormatInt(math.MaxInt64>>shift, 10), true

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return "0", strconv.FormatUint(math.MaxUint64>>(64-t.Bits()), 10), true

	default:
		return "", "", false
	}
}

// rejectionLocation returns the options that locate a rejection the
// go-yaml decoder reported at tk, a token of the source.
//
// The first is the path of the node the decoder names by tk, as
// [pathIndex.ownerPath] finds it, which starts at `$`. The
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
	path, selects, ok := n.doc.pathIndex().ownerPath(n.doc.pathResolver(), tk)

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
// from a method or from an option of cfg, as [reportsOwnError] lists
// them. To find that value, locateDecodeError walks the type of v beside
// node, as [errorLocator] describes, and decodes each such value again
// from its own node. The first one whose decode fails with err again, as
// [errorLocator.same] compares the two, reported it.
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
// the decoder refuses node for the type of v, as [refusesForText]
// reports, node is the value it refused.
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

	l := &errorLocator{
		ctx:     ctx,
		node:    n,
		decoder: yaml.NewDecoder(bytes.NewReader(nil), n.yamlOptions(cfg)...),
		msg:     err.Error(),
		anchor:  anchorOf(err),
		places:  placesOf(err),
		inlined: map[inlineVisit]bool{},

		unmarshalers: cfg.unmarshalers,
	}

	var sink any

	if l.reproduces(node, &sink) {
		return err
	}

	t := pointerBase(reflect.TypeOf(v).Elem())

	// With no value below node, found is the place of node itself.
	var found place

	if !l.refuses(t, node) {
		var ok bool

		found, ok = l.below(t, node, place{})
		if !ok && (!reportsOwnError(t, l.unmarshalers) || !isScalar(l.content(node))) {
			return err
		}
	}

	cause := l.refusal
	if cause == nil {
		cause = asDecodeError(n.doc.decodeTree().restoreError(err))
	}

	return Rebase(cause, found.path())
}

// unplaced reports whether the decode has yet to find the value err is
// about. It reports false for an err that matches [errPlaced], which
// [Node.rejection] or [decodeWithRecover] placed, for the error of a
// context that ended, and for a [yaml.Error] at a token of the source.
func (n *Node) unplaced(err error) bool {
	if errors.Is(err, errPlaced) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
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

		return x.hasLocation() || x.rebased ||
			slices.ContainsFunc(x.Unwrap(), holdsLocation) || slices.ContainsFunc(x.details, holdsLocation)

	case interface{ Unwrap() error }:
		return holdsLocation(x.Unwrap())

	case interface{ Unwrap() []error }:
		return slices.ContainsFunc(x.Unwrap(), holdsLocation)

	default:
		return false
	}
}

// reportsOwnError reports whether the decoder can return an error for a
// value of type t with no token of the source, in a decode whose options
// name the unmarshalers u. It can for a [time.Duration], for a type that
// decodes itself, as [decodesItself] reads it, and for a type u gives an
// unmarshaler: one a [WithCustomUnmarshaler] function decodes, and one
// with an UnmarshalJSON method under [WithJSONUnmarshalers].
//
// A function from [yaml.RegisterCustomUnmarshaler] names a type the
// decoder never shows, so reportsOwnError is false for a type only such
// a function decodes.
func reportsOwnError(t reflect.Type, u optionUnmarshalers) bool {
	return t == durationType || decodesItself(t) || u.decodes(t)
}

// refusesForText reports whether the decoder refuses content for a value
// of type t and calls no unmarshaler. The content argument is the
// mapping, sequence, or scalar the value reads, as [contentNode] returns
// it. The u argument holds the unmarshalers the options of the decode
// name.
//
// The decoder hands an UnmarshalText method the string it reads from a
// scalar. It reads no string from a mapping or a sequence, so it calls
// no method for one and returns an error of its own, with no token of
// the source. A type that [decodesFromText] reports thus takes a scalar
// alone. Two such types take a mapping or a sequence another way. The
// decoder parses a [time.Time] itself, and under [WithJSONUnmarshalers]
// it hands the node to the UnmarshalJSON method of a type that has one.
func refusesForText(t reflect.Type, u optionUnmarshalers, content ast.Node) bool {
	if astnode.IsNil(content) || isScalar(content) || t == timeType || !decodesFromText(t, u) {
		return false
	}

	return !u.json || !reflect.PointerTo(t).Implements(jsonUnmarshalerType)
}

// textRejection returns the rejection of content, a mapping or a
// sequence the decoder refuses as [refusesForText] reports. The error
// the decoder returns for it names neither the value nor its kind. The
// rejection matches [ErrDecode] and reads "expected string, got
// sequence", as [rejectionMessage] writes the rejection of a sequence
// that a string field reads.
func textRejection(content ast.Node) error {
	return decodeError{err: fmt.Errorf("expected %s, got %s", kindString, kindOfNode(content.Type()))}
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
	if !ok || x == nil || x.rebased || !x.hasLocation() {
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

// pointerBase returns the type t points to through every pointer on it,
// since the decoder decodes a pointer as the value it points to.
func pointerBase(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	return t
}

// isScalar reports whether node holds a value with nothing below it,
// which is any node but a mapping, a sequence, or nil.
func isScalar(node ast.Node) bool {
	switch node.(type) {
	case nil, *ast.MappingNode, *ast.SequenceNode:
		return false
	default:
		return true
	}
}

// errorLocator finds the value that reported an error of a decode with
// no token. It walks the type the decode filled beside the node the
// decode read, in the order the decoder reads them. The decoder reads
// the fields of a struct in the order the struct declares them, the
// elements of a sequence in order, and the entries of a mapping in
// document order, each key before its value. A field reads the entry of
// its name, an inline field reads the mapping of its struct, and a `<<`
// merge key brings in the entries of its sources. The walk reads
// nothing below an interface, since the decoder calls no unmarshaler
// for the values it puts there.
//
// At each value whose type [reportsOwnError] names, the walk decodes the
// node of the value into a new value of that type. A decode that fails
// with the error the locator looks for reproduces the error, as
// [errorLocator.same] compares the two. The walk then reads the fields,
// elements, or entries of that value the same way, as if they mirrored
// the document. A value that decodes itself through a second type with
// the same fields, such as an UnmarshalYAML that decodes into `type
// plain T`, returns the error of a field as it is, so the walk narrows
// to that field. The walk stops at the first value that reproduces the
// error with nothing below it that does. It stops too at a value whose
// node the decoder refuses, as [refusesForText] reports, since the
// decoder reads nothing below that node.
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
	// The inline fields the walk is inside of, so a struct that holds
	// itself inline ends the walk.
	inlined map[inlineVisit]bool
	// The message of the error to locate.
	msg string
	// The rejection of the value the walk found, as [textRejection]
	// writes it, or nil when that value reported the error itself.
	refusal error
	// The places the error to locate names, as [placesOf] lists them.
	places []locus
	// The types the options of the decode give an unmarshaler.
	unmarshalers optionUnmarshalers
	// The [*Error] in the error to locate that carries its location, and
	// that location, or the zero anchor when the error names no place.
	anchor anchor
}

// inlineVisit names an inline field the walk is inside of, by the type
// of the field and the mapping it reads. The fields together form the
// key of the set the [errorLocator] keeps.
//
//nolint:unused // The fields tell the keys of the set apart.
type inlineVisit struct {
	typ  reflect.Type
	node *ast.MappingNode
}

// find returns the value at or below node, read as type t at the place
// at, that reproduces the error. When t is a type [reportsOwnError]
// names, a decode of node into t has to reproduce it, and the value
// itself is the result unless a value below it reproduces the error too.
// The value is the result as well when the decoder refuses node for it,
// as [errorLocator.refuses] reports.
func (l *errorLocator) find(t reflect.Type, node ast.Node, at place) (place, bool) {
	t = pointerBase(t)

	if !reportsOwnError(t, l.unmarshalers) {
		return l.below(t, node, at)
	}

	if !l.reproduces(node, reflect.New(t).Interface()) {
		return place{}, false
	}

	if l.refuses(t, node) {
		return at, true
	}

	if found, ok := l.below(t, node, at); ok {
		return found, true
	}

	return at, true
}

// refuses reports whether the decoder refuses node for a value of type
// t, as [refusesForText] reports. The value is then the one the walk
// looks for, since its decode calls no unmarshaler and reads nothing
// below node, so refuses keeps the rejection [textRejection] writes for
// it.
func (l *errorLocator) refuses(t reflect.Type, node ast.Node) bool {
	content := l.content(node)
	if !refusesForText(t, l.unmarshalers, content) {
		return false
	}

	l.refusal = textRejection(content)

	return true
}

// below returns the first value below node, read as type t at the place
// at, that reproduces the error.
func (l *errorLocator) below(t reflect.Type, node ast.Node, at place) (place, bool) {
	switch t.Kind() {
	case reflect.Struct:
		return l.fields(t, node, at)

	case reflect.Slice, reflect.Array:
		return l.elements(t, node, at)

	case reflect.Map:
		mapping, ok := l.content(node).(*ast.MappingNode)
		if !ok {
			return place{}, false
		}

		return l.entries(t, node, mapping, at, map[*ast.MappingNode]bool{})

	default:
		return place{}, false
	}
}

// fields returns the first field of t, a struct type, that reproduces
// the error when it reads the mapping at node. An inline field reads
// that mapping itself, at the place of the struct.
func (l *errorLocator) fields(t reflect.Type, node ast.Node, at place) (place, bool) {
	mapping, ok := l.content(node).(*ast.MappingNode)
	if !ok {
		return place{}, false
	}

	for field := range t.Fields() {
		name, inline, skip := yamlfield.Name(field)
		if skip || yamlfield.ReadsAnchor(field) {
			continue
		}

		if inline {
			visit := inlineVisit{typ: pointerBase(field.Type), node: mapping}
			if l.inlined[visit] {
				continue
			}

			l.inlined[visit] = true
			found, ok := l.find(field.Type, node, at)
			delete(l.inlined, visit)

			if ok {
				return found, true
			}

			continue
		}

		entry, err := l.node.doc.pathResolver().Entry(node, name)
		if err != nil {
			continue
		}

		value, ok := l.value(entry)
		if !ok {
			continue
		}

		if found, ok := l.find(field.Type, value, at.child(name)); ok {
			return found, true
		}
	}

	return place{}, false
}

// elements returns the first element of t, a slice or array type, that
// reproduces the error when it reads the sequence at node.
func (l *errorLocator) elements(t reflect.Type, node ast.Node, at place) (place, bool) {
	seq, ok := l.content(node).(*ast.SequenceNode)
	if !ok {
		return place{}, false
	}

	for i, element := range seq.Values {
		value, ok := l.read(element)
		if !ok {
			continue
		}

		if found, ok := l.find(t.Elem(), value, at.index(i)); ok {
			return found, true
		}
	}

	return place{}, false
}

// entries returns the first key or value of t, a map type, that
// reproduces the error when it reads mapping, the mapping of the map at
// node or one a `<<` merge key of it brings in. The seen set holds the
// mappings the walk has read for the map, so a mapping that merges
// itself ends the walk.
//
// A path names an entry by the text of its key, and the last entry with
// that text wins the path. The walk passes over an entry that loses its
// path to a later one, since no path points at it.
func (l *errorLocator) entries(
	t reflect.Type,
	node ast.Node,
	mapping *ast.MappingNode,
	at place,
	seen map[*ast.MappingNode]bool,
) (place, bool) {
	if seen[mapping] {
		return place{}, false
	}

	seen[mapping] = true
	resolver := l.node.doc.pathResolver()

	for _, entry := range mapping.Values {
		if entry == nil || entry.Key == nil {
			continue
		}

		if entry.Key.IsMergeKey() {
			sources, err := resolver.MergeSources(&ast.MappingNode{
				Values: []*ast.MappingValueNode{entry},
			})
			if err != nil {
				continue
			}

			for _, src := range sources {
				merged, ok := l.content(src).(*ast.MappingNode)
				if !ok {
					continue
				}

				if found, ok := l.entries(t, node, merged, at, seen); ok {
					return found, true
				}
			}

			continue
		}

		name, ok := resolver.KeyName(entry.Key)
		if !ok {
			continue
		}

		named, err := resolver.Entry(node, name)
		if err != nil || named != ast.Node(entry) {
			continue
		}

		child := at.child(name)

		key := pointerBase(t.Key())
		if reportsOwnError(key, l.unmarshalers) && l.reproduces(entry.Key, reflect.New(key).Interface()) {
			return child.key(), true
		}

		value, ok := l.value(entry)
		if !ok {
			continue
		}

		if found, ok := l.find(t.Elem(), value, child); ok {
			return found, true
		}
	}

	return place{}, false
}

// value returns the node the decoder reads the value of entry from, an
// entry of a mapping, as [errorLocator.read] returns it.
func (l *errorLocator) value(entry ast.Node) (ast.Node, bool) {
	mv, ok := entry.(*ast.MappingValueNode)
	if !ok || mv == nil {
		return nil, false
	}

	return l.read(mv.Value)
}

// read returns the node the decoder reads a field, an element, or a map
// value from, as [readNode] finds it in the document of the walk.
func (l *errorLocator) read(node ast.Node) (ast.Node, bool) {
	return readNode(l.node.doc.pathResolver(), node)
}

// readNode returns the node the decoder reads a field, an element, or a
// map value from, where node is the value the document holds for it and
// resolver binds the aliases of that document. It returns node itself
// without the anchors on it, or the content of the anchor an alias
// refers to. The bool result is false when the decoder reads no value
// there. The decoder reads none from a null, or from an alias to one,
// and leaves the target as it is, with no call to an unmarshaler. An
// alias that does not resolve gives no node either.
func readNode(resolver *paths.Resolver, node ast.Node) (ast.Node, bool) {
	if astnode.IsNil(node) {
		return nil, false
	}

	held := node
	if alias, ok := node.(*ast.AliasNode); ok {
		anchor, err := resolver.Anchor(alias)
		if err != nil {
			return nil, false
		}

		held = astnode.Content(anchor)
	}

	if astnode.IsNil(held) || held.Type() == ast.NullType {
		return nil, false
	}

	content, err := resolver.Deref(node)
	if err != nil || astnode.IsNil(content) {
		return nil, false
	}

	return content, true
}

// content returns the mapping, sequence, or scalar that node holds, as
// [contentNode] finds it in the document of the walk.
func (l *errorLocator) content(node ast.Node) ast.Node {
	return contentNode(l.node.doc.pathResolver(), node)
}

// contentNode returns the mapping, sequence, or scalar that node holds,
// behind the anchors, tags, and aliases on it, or nil when an alias on
// the way does not resolve. The resolver binds the aliases of the
// document of node.
func contentNode(resolver *paths.Resolver, node ast.Node) ast.Node {
	for {
		node = astnode.Content(node)

		alias, ok := node.(*ast.AliasNode)
		if !ok {
			return node
		}

		next, err := resolver.Deref(alias)
		if err != nil {
			return nil
		}

		node = next
	}
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

	n := l.node
	view := n.doc.decodeTree().view(node)

	if node != n.doc.root.Body {
		err := n.primeAnchors(l.ctx, l.decoder, view)
		if err != nil {
			return false
		}
	}

	err := decodeWithRecover(l.ctx, l.decoder, view, v)

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
