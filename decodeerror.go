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
	// [yaml.UseJSONUnmarshaler].
	jsonUnmarshalerType = reflect.TypeFor[interface{ UnmarshalJSON(data []byte) error }]()

	// The result of [reportsOwnError] for each type it has read.
	ownErrors typeCache[bool]

	// The types [kindOfType] names apart from their kind. The go-yaml
	// decoder reads a !!timestamp tag as a [time.Time], a !!binary tag as
	// a byte slice, and a mapping as a [yaml.MapSlice] under
	// [yaml.UseOrderedMap].
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
// Any other error keeps the message the decoder gave it, such as the one
// for an unknown field, which names the field as the document does.
func rejectionMessage(err yaml.Error) string {
	switch e := err.(type) { //nolint:errorlint // The decoder returns these types unwrapped.
	case *yaml.TypeError:
		return fmt.Sprintf("expected %s, got %s", kindOfType(e.DstType), kindOfType(e.SrcType))

	case *yaml.UnexpectedNodeTypeError:
		return fmt.Sprintf("expected %s, got %s", kindOfNode(e.Expected), kindOfNode(e.Actual))

	case *yaml.OverflowError:
		lo, hi, ok := integerRange(e.DstType)
		if !ok {
			return err.GetMessage()
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
// points to. A type of a kind no YAML value decodes into names itself.
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
	default:
		return t.String()
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
// [pathIndex.ownerPath] finds it, from the root of the document. The
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
// yamlOpts returned, at the path of the value that reported it. The
// go-yaml decoder returns the error of a value that decodes itself with
// no token of the source. Such a value is a [time.Duration] or has an
// unmarshaler method, as [reportsOwnError] lists them. To find that
// value, locateDecodeError walks the type of v beside node, as
// [errorLocator] describes, and decodes each such value again from its
// own node. The first one whose decode fails with the message of err
// reported it.
//
// The result holds err under that path, as [Rebase] returns it, so the
// [SourceError] that binds the result reports the path and marks the
// value. The error of a [time.Duration] also matches
// [ErrDecodeRejected], since the decoder rejected the value. The error
// of any other value is the value's own, so it matches what it matched
// before.
//
// The path of a value under the node the decode reads is never the root,
// with one exception. When node holds a scalar and the type of v
// decodes itself, the scalar is the value, so err points at node.
//
// An err that names a place already comes back as it is. So does one
// that holds a location or a binding anywhere in its tree, one of a
// context that ended, and one no value reproduces. A decode of node into
// any value reads no unmarshaler, so an err that such a decode returns
// too is the decoder's own, and it comes back as it is as well.
func (n *Node) locateDecodeError(
	ctx context.Context,
	err error,
	node ast.Node,
	v any,
	yamlOpts []yaml.DecodeOption,
) error {
	if err == nil || !n.lacksLocation(err) {
		return err
	}

	l := &errorLocator{
		ctx:     ctx,
		node:    n,
		decoder: yaml.NewDecoder(bytes.NewReader(nil), yamlOpts...),
		msg:     err.Error(),
		inlined: map[inlineVisit]bool{},
	}

	var sink any

	if l.reproduces(node, &sink) {
		return err
	}

	t := pointerBase(reflect.TypeOf(v).Elem())

	found, ok := l.below(t, node, place{})
	if !ok {
		if !reportsOwnError(t) || !isScalar(l.content(node)) {
			return err
		}

		found = located{typ: t}
	}

	cause := n.doc.decodeTree().restoreError(err)
	if found.typ == durationType {
		cause = rejectedDurationError{err: cause}
	}

	return Rebase(cause, found.at.path())
}

// lacksLocation reports whether err names no place in the source, so the
// decode has to find one. It reports false for an err that already
// matches [ErrDecodeRejected], which [Node.rejection] located, for the
// error of a context that ended, and for a [yaml.Error] at a token of
// the source. It also reports false when an error in the tree of err is
// a [*SourceError] or an [*Error] with a location, since those say where
// they point.
func (n *Node) lacksLocation(err error) bool {
	if errors.Is(err, ErrDecodeRejected) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if ok && n.holdsToken(yamlErr.GetToken()) {
		return false
	}

	return !holdsLocation(err)
}

// holdsLocation reports whether err, or an error anywhere in the tree it
// unwraps to, is a [*SourceError] or an [*Error] that carries a location
// or comes from [Rebase].
func holdsLocation(err error) bool {
	switch x := err.(type) { //nolint:errorlint // Walks the tree one node at a time.
	case *SourceError:
		return x != nil

	case *Error:
		if x == nil {
			return false
		}

		return x.hasLocation() || x.rebased || slices.ContainsFunc(x.Unwrap(), holdsLocation)

	case interface{ Unwrap() error }:
		return holdsLocation(x.Unwrap())

	case interface{ Unwrap() []error }:
		return slices.ContainsFunc(x.Unwrap(), holdsLocation)

	default:
		return false
	}
}

// rejectedDurationError is the error of [time.ParseDuration] that the
// go-yaml decoder returned for a [time.Duration], which matches
// [ErrDecodeRejected]. It unwraps to the error the decoder returned.
type rejectedDurationError struct {
	err error
}

func (e rejectedDurationError) Error() string {
	return e.err.Error()
}

func (e rejectedDurationError) Unwrap() error {
	return e.err
}

// Is reports whether target is [ErrDecodeRejected].
func (e rejectedDurationError) Is(target error) bool {
	return target == ErrDecodeRejected
}

// reportsOwnError reports whether the decoder can return an error for a
// value of type t with no token of the source. It can for a
// [time.Duration], for a type that decodes itself, as [decodesItself]
// reads it, and for a type with an UnmarshalJSON method, which the
// decoder calls under [yaml.UseJSONUnmarshaler].
//
// The decoder gives no way to learn whether that option is set, so
// [errorLocator] decodes a value with an UnmarshalJSON method again
// either way. Without the option the decoder reads such a value field by
// field, and the second decode fails only where a value below it fails.
// The [yaml.CustomUnmarshaler] option and [yaml.RegisterCustomUnmarshaler]
// name a type the decoder never shows, so reportsOwnError is false for a
// type only they decode.
func reportsOwnError(t reflect.Type) bool {
	return ownErrors.get(t, func(t reflect.Type) bool {
		return t == durationType || decodesItself(t) || reflect.PointerTo(t).Implements(jsonUnmarshalerType)
	})
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
// no location. It walks the type the decode filled beside the node the
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
// with the message the locator looks for reproduces the error. The walk
// then reads the fields, elements, or entries of that value the same
// way, as if they mirrored the document. A value that decodes itself
// through a second type with the same fields, such as an UnmarshalYAML
// that decodes into `type plain T`, returns the error of a field as it
// is, so the walk narrows to that field. The walk stops at the first
// value that reproduces the error with nothing below it that does.
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
}

// located is the value an [errorLocator] found: its place under the node
// the decode read, and its type with no pointer on it.
type located struct {
	typ reflect.Type
	at  place
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
func (l *errorLocator) find(t reflect.Type, node ast.Node, at place) (located, bool) {
	t = pointerBase(t)

	if !reportsOwnError(t) {
		return l.below(t, node, at)
	}

	if !l.reproduces(node, reflect.New(t).Interface()) {
		return located{}, false
	}

	if found, ok := l.below(t, node, at); ok {
		return found, true
	}

	return located{typ: t, at: at}, true
}

// below returns the first value below node, read as type t at the place
// at, that reproduces the error.
func (l *errorLocator) below(t reflect.Type, node ast.Node, at place) (located, bool) {
	switch t.Kind() {
	case reflect.Struct:
		return l.fields(t, node, at)

	case reflect.Slice, reflect.Array:
		return l.elements(t, node, at)

	case reflect.Map:
		mapping, ok := l.content(node).(*ast.MappingNode)
		if !ok {
			return located{}, false
		}

		return l.entries(t, node, mapping, at, map[*ast.MappingNode]bool{})

	default:
		return located{}, false
	}
}

// fields returns the first field of t, a struct type, that reproduces
// the error when it reads the mapping at node. An inline field reads
// that mapping itself, at the place of the struct.
func (l *errorLocator) fields(t reflect.Type, node ast.Node, at place) (located, bool) {
	mapping, ok := l.content(node).(*ast.MappingNode)
	if !ok {
		return located{}, false
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

	return located{}, false
}

// elements returns the first element of t, a slice or array type, that
// reproduces the error when it reads the sequence at node.
func (l *errorLocator) elements(t reflect.Type, node ast.Node, at place) (located, bool) {
	seq, ok := l.content(node).(*ast.SequenceNode)
	if !ok {
		return located{}, false
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

	return located{}, false
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
) (located, bool) {
	if seen[mapping] {
		return located{}, false
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
		if reportsOwnError(key) && l.reproduces(entry.Key, reflect.New(key).Interface()) {
			return located{typ: key, at: child.key()}, true
		}

		value, ok := l.value(entry)
		if !ok {
			continue
		}

		if found, ok := l.find(t.Elem(), value, child); ok {
			return found, true
		}
	}

	return located{}, false
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
// value, fails with the message the locator looks for. The decode reads
// node as a decode of that node alone does, with the anchors outside it
// that it refers to. A context that has ended reproduces nothing, so the
// walk ends with no location.
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

	return err != nil && err.Error() == l.msg
}
