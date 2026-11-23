package yamldecode

import (
	"bytes"
	"context"
	"encoding"
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml/ast"
	"go.jacobcolvin.com/niceyaml/internal/yamldecode/errors"
	"go.jacobcolvin.com/niceyaml/internal/yamldecode/format"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
)

// Decoder reads and decodes YAML values from an input stream.
type Decoder struct {
	reader               io.Reader
	referenceReaders     []io.Reader
	anchorNodeMap        map[string]ast.Node
	anchorValueMap       map[string]reflect.Value
	customUnmarshalerMap map[reflect.Type]func(context.Context, interface{}, []byte) error
	commentMaps          []CommentMap
	toCommentMap         CommentMap
	opts                 []DecodeOption
	referenceFiles       []string
	referenceDirs        []string
	isRecursiveDir       bool
	isResolvedReference  bool
	validator            StructValidator
	disallowUnknownField bool
	allowedFieldPrefixes []string
	allowDuplicateMapKey bool
	useOrderedMap        bool
	useJSONUnmarshaler   bool
	parsedFile           *ast.File
	streamIndex          int
	decodeDepth          int
	steps                int
	aliasDepth           int
	aliasNodesLeft       int
	aliasBytesLeft       int
	bounded              bool
	stopped              error
	origins              format.Origins
	resolveAlias         func(*ast.AliasNode) (ast.Node, bool)
	collectErrors        bool
	// unknownField, from ReportUnknownField, takes the error of each
	// unknown field, and the decode does not return that error.
	unknownField func(error)
	// anchorUnknown holds the unknown fields that the decoder reported
	// while it read the value that anchorValueMap keeps under the same
	// key, with their paths below the anchor.
	anchorUnknown map[string][]unknownReport
	// path holds the steps from the node of a decode under CollectErrors
	// to the value that the decoder reads.
	path []Step
	// openNulls counts the aliases that read as null because the decoder
	// was reading their anchor.
	openNulls int
}

// keeps reports whether the decoder keeps the value of an anchor that it
// read since openNulls held mark. With a resolver from [ResolveAlias], it
// keeps the value only when no alias in the read read as null because its
// anchor was open. Such a null depends on the anchors open around the read,
// so a read from another place can give another value. Without a resolver,
// the decoder keeps the value of each anchor that it reads, as go-yaml
// does.
func (d *Decoder) keeps(mark int) bool {
	return d.resolveAlias == nil || d.openNulls == mark
}

// anchorKey returns the key under which the decoder holds the anchor n: its
// name, or with a resolver from [ResolveAlias] the identity of its value.
func (d *Decoder) anchorKey(n *ast.AnchorNode) string {
	if d.resolveAlias == nil {
		return n.Name.GetToken().Value
	}
	return fmt.Sprintf("%p", n.Value)
}

// aliasKey returns the key of the anchor that alias reads. Without a
// resolver, that is the name of the alias, the value of its name token. The
// text of the name node also holds a comment that follows the name. With a
// resolver, it is the key of the node that the resolver binds the alias to,
// and aliasKey registers that node. A decode of a node below the document
// thus does not need an earlier pass over the anchors outside the node.
func (d *Decoder) aliasKey(alias *ast.AliasNode) string {
	if d.resolveAlias == nil {
		return alias.Value.GetToken().Value
	}
	node, ok := d.resolveAlias(alias)
	if !ok || node == nil {
		return "\x00unbound"
	}
	key := fmt.Sprintf("%p", node)
	if _, exists := d.anchorNodeMap[key]; !exists {
		d.anchorNodeMap[key] = node
	}
	return key
}

// enterMerge returns ctx for the reads of the entries that node, the value
// of a merge key or an element of a sequence under a merge key, brings in.
// For an alias, the decoder reads those entries inside the anchor of the
// alias. A merge of an alias inside the anchor that it reads closes a loop.
// Such a merge fails with a MissingAnchorError, as it does where the decoder
// reads the anchor itself.
func (d *Decoder) enterMerge(ctx context.Context, node ast.Node) (context.Context, error) {
	for {
		switch n := node.(type) {
		case *ast.TagNode:
			node = n.Value
			continue
		case *ast.AnchorNode:
			node = n.Value
			continue
		}
		break
	}
	open := getAnchorMap(ctx)
	if alias, ok := node.(*ast.AliasNode); ok {
		key := d.aliasKey(alias)
		if _, ok := open[key]; ok {
			return ctx, &MissingAnchorError{Alias: alias}
		}
		return withAnchor(ctx, key), nil
	}
	return ctx, nil
}

// mapEntries returns an iterator over the entries that a read of node goes
// through, in order, as getMapNode returns them. Under a merge key, a
// sequence brings in the entries of each of its elements, and the decoder
// reads the entries of an element inside the anchor of that element alone.
// An alias under a merge key that reads a sequence brings in the elements
// of that sequence the same way. So an alias inside the anchor that it
// reads reads as null in a sequence too, and a merge of it fails. The
// decoder reads the entries of any other node with ctx.
func (d *Decoder) mapEntries(ctx context.Context, node ast.Node, isMerge bool) (*entryIter, error) {
	if isMerge {
		d.stepIn()
		defer d.stepOut()
		if d.isExceededMaxDepth() {
			return nil, ErrExceededMaxDepth
		}
		switch n := node.(type) {
		case *ast.TagNode:
			return d.mapEntries(ctx, n.Value, isMerge)
		case *ast.AnchorNode:
			d.anchorNodeMap[d.anchorKey(n)] = n.Value
			return d.mapEntries(ctx, n.Value, isMerge)
		case *ast.AliasNode:
			// The caller opened the anchor of the alias. A sequence that
			// the alias reads brings in its elements as a sequence under
			// the merge key does.
			anchor := d.anchorNodeMap[d.aliasKey(n)]
			if anchor == nil {
				return nil, &MissingAnchorError{Alias: n}
			}
			content := anchor
		unwrap:
			for {
				switch c := content.(type) {
				case *ast.TagNode:
					content = c.Value
				case *ast.AnchorNode:
					content = c.Value
				default:
					break unwrap
				}
			}
			if _, ok := content.(*ast.SequenceNode); ok {
				return d.mapEntries(ctx, anchor, isMerge)
			}
		case *ast.SequenceNode:
			it := &entryIter{parts: make([]entryPart, len(n.Values))}
			for i, value := range n.Values {
				valueCtx, err := d.enterMerge(ctx, value)
				if err != nil {
					return nil, err
				}
				it.parts[i].ctx = valueCtx
			}
			for i, value := range n.Values {
				mapNode, err := d.getMapNode(value, false)
				if err != nil {
					return nil, err
				}
				it.parts[i].node = mapNode
			}
			return it, nil
		}
	}
	mapNode, err := d.getMapNode(node, isMerge)
	if err != nil {
		return nil, err
	}
	return &entryIter{parts: []entryPart{{ctx: ctx, node: mapNode}}}, nil
}

type ctxEntryKey struct{}

// withEntryContexts returns ctx with ctxs, the context for the read of each
// value node of a mapping that decodeStruct builds for an inline field.
func withEntryContexts(ctx context.Context, ctxs map[ast.Node]context.Context) context.Context {
	return context.WithValue(ctx, ctxEntryKey{}, ctxs)
}

// entryContext returns the context for the read of node, the value of an
// entry: the one that withEntryContexts holds for node, or ctx.
func entryContext(ctx context.Context, node ast.Node) context.Context {
	ctxs, _ := ctx.Value(ctxEntryKey{}).(map[ast.Node]context.Context)
	if c, ok := ctxs[node]; ok {
		return c
	}
	return ctx
}

// entryPart is a mapping whose entries a read goes through, with the
// context for the reads of those entries.
type entryPart struct {
	ctx  context.Context
	node ast.MapNode
}

// entryIter iterates over the entries of its parts in order. Its ctx is the
// context for the reads of the current entry.
type entryIter struct {
	*ast.MapNodeIter
	ctx   context.Context
	parts []entryPart
}

// Next advances the iterator to the next entry and reports whether there
// is one.
func (it *entryIter) Next() bool {
	for it.MapNodeIter == nil || !it.MapNodeIter.Next() {
		if len(it.parts) == 0 {
			return false
		}
		it.ctx, it.MapNodeIter = it.parts[0].ctx, it.parts[0].node.MapRange()
		it.parts = it.parts[1:]
	}
	return true
}

// MissingAnchorError is the error of an alias under a merge key, or at a
// mapping or a sequence of a typed target, that the decoder holds no anchor
// for. Its message is upstream's.
type MissingAnchorError struct {
	Alias *ast.AliasNode
}

func (e *MissingAnchorError) Error() string {
	return fmt.Sprintf("cannot find anchor by alias name %s", e.Alias.Value.GetToken().Value)
}

// ErrAliasBudget is the error of a decode that read more through aliases
// than [AliasBudget] allows.
var ErrAliasBudget = errors.New("aliases repeat more of the document than the alias budget allows")

// tick counts one node that the decoder reads. It returns the error of ctx
// once ctx has ended, and [ErrAliasBudget] once the decoder has read more
// nodes through aliases than its budget allows.
//
// The decoder goes on after the error of an element or a field, so the
// error of tick stays: every later tick returns it, and the decode returns
// it alone.
func (d *Decoder) tick(ctx context.Context) error {
	if d.stopped != nil {
		return d.stopped
	}
	d.steps++
	if d.steps&0xff == 0 {
		d.stopped = ctx.Err()
	}
	if d.bounded && d.aliasDepth > 0 {
		d.aliasNodesLeft--
	}
	if d.bounded && (d.aliasNodesLeft < 0 || d.aliasBytesLeft < 0) {
		d.stopped = ErrAliasBudget
	}
	return d.stopped
}

// finish returns err, the error of a decode, with the errors of an
// [Errors] in the order of the document. A decode that tick stopped
// returns the error of tick alone.
func (d *Decoder) finish(err error) error {
	if d.stopped != nil {
		err, d.stopped = d.stopped, nil
		return err
	}
	err = d.located(err)
	if list, ok := err.(Errors); ok {
		sort.SliceStable(list, func(i, j int) bool { return offsetOf(list[i]) < offsetOf(list[j]) })
	}
	return err
}

// Step is one step on the way from the node of a decode to a value below
// it: the entry of a key, the element at an index, or the key of an entry.
type Step struct {
	// The name of the key, for an entry or a key.
	Key string
	// The index of the element, when IsIndex is set.
	Index   int
	IsIndex bool
	// Whether the step names the key of the entry and not its value.
	IsKey bool
}

// PathError is an error of a decode under [CollectErrors] with the path of
// the value that it is about. The path names the way the decode read the
// value, through each alias and each `<<` merge key, so a value that
// several aliases read gives one error for each of them, each with its own
// path. An inline field does not add a step.
type PathError struct {
	Err  error
	Path []Step
}

func (e *PathError) Error() string { return e.Err.Error() }

func (e *PathError) Unwrap() error { return e.Err }

// located returns err with the path of the value that the decoder is in,
// and then steps. An error that holds a path already keeps it. Without
// [CollectErrors], err comes back as it is.
func (d *Decoder) located(err error, steps ...Step) error {
	if !d.collectErrors || err == nil {
		return err
	}
	switch err := err.(type) {
	case *PathError:
		return err
	case Errors:
		for i := range err {
			err[i] = d.located(err[i], steps...)
		}
		return err
	}
	path := make([]Step, 0, len(d.path)+len(steps))
	return &PathError{Err: err, Path: append(append(path, d.path...), steps...)}
}

// keyName returns the name of key for a [Step]. The key of an entry that
// the decoder made for an inline field has no token.
func keyName(key ast.Node) string {
	if tk := key.GetToken(); tk != nil {
		return tk.Value
	}
	if made, ok := key.(*ast.StringNode); ok {
		return made.Value
	}
	return ""
}

// below is createDecodedNewValue for a value at step below the value that
// the decoder is in.
func (d *Decoder) below(
	ctx context.Context, step Step, typ reflect.Type, defaultVal reflect.Value, node ast.Node,
) (reflect.Value, error) {
	// The defer takes the step off the path when a value below panics too,
	// because an unmarshaler can recover a panic of its callback.
	depth := len(d.path)
	defer func() { d.path = d.path[:depth] }()
	d.path = append(d.path, step)
	v, err := d.createDecodedNewValue(ctx, typ, defaultVal, node)
	return v, d.located(err)
}

// captureUnknown runs read and returns the errors of the unknown fields
// that read reports through unknownField. When forward is set, it also
// hands each one on as it comes. Otherwise it holds them, and the caller
// decides whether to hand them on.
func (d *Decoder) captureUnknown(forward bool, read func() error) ([]error, error) {
	if d.unknownField == nil {
		return nil, read()
	}
	outer := d.unknownField
	defer func() { d.unknownField = outer }()
	var reports []error
	d.unknownField = func(err error) {
		reports = append(reports, err)
		if forward {
			outer(err)
		}
	}
	err := read()
	return reports, err
}

// reportUnknown hands each error of reports to unknownField.
func (d *Decoder) reportUnknown(reports []error) {
	for _, err := range reports {
		d.unknownField(err)
	}
}

// unknownReport is an unknown field that the decoder reported while it
// read the value of an anchor, with its path below the anchor.
type unknownReport struct {
	err  error
	path []Step
}

// keepUnknown records reports, the unknown fields that the decoder
// reported while it read the anchor with the key key at depth steps on
// the path. An alias that takes the value of the anchor in place of a read
// of its own reports them again, below its own path.
func (d *Decoder) keepUnknown(key string, depth int, reports []error) {
	if len(reports) == 0 {
		delete(d.anchorUnknown, key)
		return
	}
	if d.anchorUnknown == nil {
		d.anchorUnknown = map[string][]unknownReport{}
	}
	kept := make([]unknownReport, 0, len(reports))
	for _, err := range reports {
		r := unknownReport{err: err}
		if located, ok := err.(*PathError); ok && len(located.Path) >= depth {
			r.err, r.path = located.Err, slices.Clone(located.Path[depth:])
		}
		kept = append(kept, r)
	}
	d.anchorUnknown[key] = kept
}

// replayUnknown reports the unknown fields that keepUnknown recorded for
// the anchor with the key key, below the path of the alias that takes its
// value.
func (d *Decoder) replayUnknown(key string) {
	if d.unknownField == nil {
		return
	}
	for _, r := range d.anchorUnknown[key] {
		d.unknownField(d.located(r.err, r.path...))
	}
}

// Errors holds every error that a decode under [CollectErrors] found, in
// the order of the document. An error without a place in the document goes
// first.
type Errors []error

// Error returns the message of each error, one per line, as the join of
// the errors package of the standard library writes them.
func (e Errors) Error() string {
	msgs := make([]string, len(e))
	for i, err := range e {
		msgs[i] = err.Error()
	}
	return strings.Join(msgs, "\n")
}

func (e Errors) Unwrap() []error { return e }

// note adds err to found, with each error of an [Errors] on its own.
// Without [CollectErrors], found keeps the first error alone, as go-yaml
// does.
func (d *Decoder) note(found *[]error, err error) {
	if !d.collectErrors && len(*found) != 0 {
		return
	}
	if list, ok := err.(Errors); ok {
		*found = append(*found, list...)
		return
	}
	*found = append(*found, err)
}

// noted returns the error for found, with last after it when last is not
// nil. Without [CollectErrors], last comes back alone.
func (d *Decoder) noted(found []error, last error) error {
	if last != nil {
		if !d.collectErrors {
			return last
		}
		d.note(&found, last)
	}
	switch len(found) {
	case 0:
		return nil
	case 1:
		return found[0]
	}
	return Errors(found)
}

// noteField adds err, the error of the field called field of the struct
// type called typ, to found, as note does. It names the field in each
// TypeError of the decoder's own, as go-yaml does for the one error it
// keeps. For an inline field, a TypeError that names a field of the inline
// struct keeps that name under typ.
func (d *Decoder) noteField(found *[]error, err error, typ, field string, inline bool) {
	if !d.collectErrors && len(*found) != 0 {
		return
	}
	list, ok := err.(Errors)
	if !ok {
		list = Errors{err}
	}
	for _, err := range list {
		// The decoder names the field only in a TypeError of its own, so
		// an error that wraps a TypeError and the error of an unmarshaler
		// stay as they are. A TypeError of its own can carry a path.
		own := err
		if located, ok := own.(*PathError); ok {
			own = located.Err
		}
		if te, ok := own.(*errors.TypeError); ok {
			fieldName := fmt.Sprintf("%s.%s", typ, field)
			if inline && te.StructFieldName != nil {
				fieldName = fmt.Sprintf("%s.%s", typ, *te.StructFieldName)
			}
			te.StructFieldName = &fieldName
		}
		*found = append(*found, err)
	}
}

// offsetOf returns where err lies in the document: at the node of an
// [UnmarshalerError], or at the token of an error of the decoder. It
// returns -1 for an error without a place.
func offsetOf(err error) int {
	var tk *token.Token
	for at := err; at != nil && tk == nil; at = errors.Unwrap(at) {
		switch e := at.(type) {
		case *UnmarshalerError:
			if e.Node != nil {
				tk = e.Node.GetToken()
			}
			if tk == nil {
				return -1
			}
		case errors.Error:
			tk = e.GetToken()
			if tk == nil {
				return -1
			}
		}
	}
	if tk == nil || tk.Position == nil {
		return -1
	}
	return tk.Position.Offset
}

// UnmarshalerError is the error of a value that decodes itself: one with an
// unmarshaler method, one that a function from [CustomUnmarshaler] decodes,
// a [time.Time], or a [time.Duration]. It holds the node that the decoder
// handed the value and unwraps to the error of the unmarshaler. The decoder
// writes the name of a field only into a TypeError of its own, so a
// TypeError that an unmarshaler returns keeps the field of the decode that
// made it.
type UnmarshalerError struct {
	// The error that the unmarshaler returned.
	Err error
	// The node that the value decoded from. At an alias, it is the content
	// of the anchor.
	Node ast.Node

	dec *Decoder
}

func (e *UnmarshalerError) Error() string { return e.Err.Error() }

func (e *UnmarshalerError) Unwrap() error { return e.Err }

// unmarshalerError returns err, the error that the unmarshaler of a value
// that decodes from src returned, as an [UnmarshalerError] at src. The
// unmarshaler can return an error that its callback returned, unchanged.
// Such an error, one of passed, is the decoder's own, so it stays as it is
// with the place where the decoder found it.
func (d *Decoder) unmarshalerError(err error, src ast.Node, passed ...error) error {
	for _, p := range passed {
		if sameError(err, p) {
			return err
		}
	}
	return &UnmarshalerError{Err: err, Node: src, dec: d}
}

// ownError returns err, the error of a value that decodes itself from src,
// as an [UnmarshalerError] at src. An error that the decoder placed stays
// as it is: an error at a token, and an error of the decoder for a value
// inside this one.
func (d *Decoder) ownError(err error, src ast.Node) error {
	switch e := err.(type) {
	case errors.Error:
		if e.GetToken() != nil {
			return err
		}
	case *UnmarshalerError:
		if e.dec == d {
			return err
		}
	case Errors:
		return err
	case *PathError:
		return err
	}
	return &UnmarshalerError{Err: err, Node: src, dec: d}
}

// sameError reports whether a and b are the same error value.
func sameError(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	if ta != tb {
		return false
	}
	if !ta.Comparable() {
		va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
		if ta.Kind() != reflect.Slice {
			return false
		}
		return va.Len() == vb.Len() && (va.Len() == 0 || va.Pointer() == vb.Pointer())
	}
	return a == b
}

// NewDecoder returns a new decoder that reads from r.
func NewDecoder(r io.Reader, opts ...DecodeOption) *Decoder {
	return &Decoder{
		reader:               r,
		anchorNodeMap:        map[string]ast.Node{},
		anchorValueMap:       map[string]reflect.Value{},
		customUnmarshalerMap: map[reflect.Type]func(context.Context, interface{}, []byte) error{},
		opts:                 opts,
		referenceReaders:     []io.Reader{},
		referenceFiles:       []string{},
		referenceDirs:        []string{},
		isRecursiveDir:       false,
		isResolvedReference:  false,
		disallowUnknownField: false,
		allowDuplicateMapKey: false,
		useOrderedMap:        false,
	}
}

const maxDecodeDepth = 10000

// maxPointerDepth is the most pointers the decoder follows from a type to
// the type of the value it decodes.
const maxPointerDepth = 64

func (d *Decoder) stepIn() {
	d.decodeDepth++
}

func (d *Decoder) stepOut() {
	d.decodeDepth--
}

func (d *Decoder) isExceededMaxDepth() bool {
	return d.decodeDepth > maxDecodeDepth
}

func (d *Decoder) castToFloat(v interface{}) interface{} {
	switch vv := v.(type) {
	case int:
		return float64(vv)
	case int8:
		return float64(vv)
	case int16:
		return float64(vv)
	case int32:
		return float64(vv)
	case int64:
		return float64(vv)
	case uint:
		return float64(vv)
	case uint8:
		return float64(vv)
	case uint16:
		return float64(vv)
	case uint32:
		return float64(vv)
	case uint64:
		return float64(vv)
	case float32:
		return float64(vv)
	case float64:
		return vv
	case string:
		// if error occurred, return zero value
		f, _ := strconv.ParseFloat(vv, 64)
		return f
	}
	return 0
}

func (d *Decoder) mapKeyNodeToString(ctx context.Context, node ast.MapKeyNode) (string, error) {
	key, err := d.nodeToValue(ctx, node)
	if err != nil {
		return "", err
	}
	if key == nil {
		return "null", nil
	}
	if k, ok := key.(string); ok {
		return k, nil
	}
	return fmt.Sprint(key), nil
}

func (d *Decoder) setToMapValue(ctx context.Context, node ast.Node, m map[string]interface{}) error {
	d.stepIn()
	defer d.stepOut()
	if d.isExceededMaxDepth() {
		return ErrExceededMaxDepth
	}

	d.setPathToCommentMap(node)
	switch n := node.(type) {
	case *ast.MappingValueNode:
		if n.Key.IsMergeKey() {
			ctx, err := d.enterMerge(ctx, n.Value)
			if err != nil {
				return err
			}
			iter, err := d.mapEntries(ctx, n.Value, true)
			if err != nil {
				return err
			}
			for iter.Next() {
				if err := d.setToMapValue(iter.ctx, iter.KeyValue(), m); err != nil {
					return err
				}
			}
		} else {
			key, err := d.mapKeyNodeToString(ctx, n.Key)
			if err != nil {
				return err
			}
			v, err := d.nodeToValue(ctx, n.Value)
			if err != nil {
				return err
			}
			m[key] = v
		}
	case *ast.MappingNode:
		for _, value := range n.Values {
			if err := d.setToMapValue(ctx, value, m); err != nil {
				return err
			}
		}
	case *ast.AnchorNode:
		anchorName := d.anchorKey(n)
		d.anchorNodeMap[anchorName] = n.Value
	}
	return nil
}

func (d *Decoder) setToOrderedMapValue(ctx context.Context, node ast.Node, m *MapSlice) error {
	d.stepIn()
	defer d.stepOut()
	if d.isExceededMaxDepth() {
		return ErrExceededMaxDepth
	}

	d.setPathToCommentMap(node)
	switch n := node.(type) {
	case *ast.MappingValueNode:
		if n.Key.IsMergeKey() {
			ctx, err := d.enterMerge(ctx, n.Value)
			if err != nil {
				return err
			}
			iter, err := d.mapEntries(ctx, n.Value, true)
			if err != nil {
				return err
			}
			for iter.Next() {
				if err := d.setToOrderedMapValue(iter.ctx, iter.KeyValue(), m); err != nil {
					return err
				}
			}
		} else {
			key, err := d.mapKeyNodeToString(ctx, n.Key)
			if err != nil {
				return err
			}
			value, err := d.nodeToValue(ctx, n.Value)
			if err != nil {
				return err
			}
			*m = append(*m, MapItem{Key: key, Value: value})
		}
	case *ast.MappingNode:
		for _, value := range n.Values {
			if err := d.setToOrderedMapValue(ctx, value, m); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Decoder) setPathToCommentMap(node ast.Node) {
	if node == nil {
		return
	}
	if d.toCommentMap == nil {
		return
	}
	d.addHeadOrLineCommentToMap(node)
	d.addFootCommentToMap(node)
}

func (d *Decoder) addHeadOrLineCommentToMap(node ast.Node) {
	sequence, ok := node.(*ast.SequenceNode)
	if ok {
		d.addSequenceNodeCommentToMap(sequence)
		return
	}
	commentGroup := node.GetComment()
	if commentGroup == nil {
		return
	}
	texts := []string{}
	targetLine := node.GetToken().Position.Line
	minCommentLine := math.MaxInt
	for _, comment := range commentGroup.Comments {
		if minCommentLine > comment.Token.Position.Line {
			minCommentLine = comment.Token.Position.Line
		}
		texts = append(texts, comment.Token.Value)
	}
	if len(texts) == 0 {
		return
	}
	commentPath := node.GetPath()
	if minCommentLine < targetLine {
		switch n := node.(type) {
		case *ast.MappingNode:
			if len(n.Values) != 0 {
				commentPath = n.Values[0].Key.GetPath()
			}
		case *ast.MappingValueNode:
			commentPath = n.Key.GetPath()
		}
		d.addCommentToMap(commentPath, HeadComment(texts...))
	} else {
		d.addCommentToMap(commentPath, LineComment(texts[0]))
	}
}

func (d *Decoder) addSequenceNodeCommentToMap(node *ast.SequenceNode) {
	if len(node.ValueHeadComments) != 0 {
		for idx, headComment := range node.ValueHeadComments {
			if headComment == nil {
				continue
			}
			texts := make([]string, 0, len(headComment.Comments))
			for _, comment := range headComment.Comments {
				texts = append(texts, comment.Token.Value)
			}
			if len(texts) != 0 {
				d.addCommentToMap(node.Values[idx].GetPath(), HeadComment(texts...))
			}
		}
	}
	firstElemHeadComment := node.GetComment()
	if firstElemHeadComment != nil {
		texts := make([]string, 0, len(firstElemHeadComment.Comments))
		for _, comment := range firstElemHeadComment.Comments {
			texts = append(texts, comment.Token.Value)
		}
		if len(texts) != 0 {
			if len(node.Values) != 0 {
				d.addCommentToMap(node.Values[0].GetPath(), HeadComment(texts...))
			}
		}
	}
}

func (d *Decoder) addFootCommentToMap(node ast.Node) {
	var (
		footComment     *ast.CommentGroupNode
		footCommentPath = node.GetPath()
	)
	switch n := node.(type) {
	case *ast.SequenceNode:
		footComment = n.FootComment
		if n.FootComment != nil {
			footCommentPath = n.FootComment.GetPath()
		}
	case *ast.MappingNode:
		footComment = n.FootComment
		if n.FootComment != nil {
			footCommentPath = n.FootComment.GetPath()
		}
	case *ast.MappingValueNode:
		footComment = n.FootComment
		if n.FootComment != nil {
			footCommentPath = n.FootComment.GetPath()
		}
	}
	if footComment == nil {
		return
	}
	var texts []string
	for _, comment := range footComment.Comments {
		texts = append(texts, comment.Token.Value)
	}
	if len(texts) != 0 {
		d.addCommentToMap(footCommentPath, FootComment(texts...))
	}
}

func (d *Decoder) addCommentToMap(path string, comment *Comment) {
	for _, c := range d.toCommentMap[path] {
		if c.Position == comment.Position {
			// already added same comment
			return
		}
	}
	d.toCommentMap[path] = append(d.toCommentMap[path], comment)
	sort.Slice(d.toCommentMap[path], func(i, j int) bool {
		return d.toCommentMap[path][i].Position < d.toCommentMap[path][j].Position
	})
}

func (d *Decoder) nodeToValue(ctx context.Context, node ast.Node) (any, error) {
	d.stepIn()
	defer d.stepOut()
	if d.isExceededMaxDepth() {
		return nil, ErrExceededMaxDepth
	}
	if err := d.tick(ctx); err != nil {
		return nil, err
	}

	d.setPathToCommentMap(node)
	switch n := node.(type) {
	case *ast.NullNode:
		return nil, nil
	case *ast.StringNode:
		return n.GetValue(), nil
	case *ast.IntegerNode:
		return n.GetValue(), nil
	case *ast.FloatNode:
		return n.GetValue(), nil
	case *ast.BoolNode:
		return n.GetValue(), nil
	case *ast.InfinityNode:
		return n.GetValue(), nil
	case *ast.NanNode:
		return n.GetValue(), nil
	case *ast.TagNode:
		if n.Directive != nil {
			v, err := d.nodeToValue(ctx, n.Value)
			if err != nil {
				return nil, err
			}
			if v == nil {
				return "", nil
			}
			return fmt.Sprint(v), nil
		}
		switch token.ReservedTagKeyword(n.Start.Value) {
		case token.TimestampTag:
			t, _ := d.castToTime(ctx, n.Value)
			return t, nil
		case token.IntegerTag:
			v, err := d.nodeToValue(ctx, n.Value)
			if err != nil {
				return nil, err
			}
			// An integer under a !!int tag, or a string that spells one,
			// reads as a Go int where it fits. Any other value fails.
			if s, ok := v.(string); ok {
				if num := token.ToNumber(s); num != nil && num.Type != token.NumberTypeFloat {
					v = num.Value
				}
			}
			switch vv := v.(type) {
			case int64:
				if int64(int(vv)) == vv {
					return int(vv), nil
				}
				return vv, nil
			case uint64:
				if vv <= math.MaxInt {
					return int(vv), nil
				}
				return vv, nil
			}
			return nil, errors.ErrTypeMismatch(reflect.TypeOf(0), reflect.TypeOf(v), n.Value.GetToken())
		case token.FloatTag:
			v, err := d.nodeToValue(ctx, n.Value)
			if err != nil {
				return nil, err
			}
			return d.castToFloat(v), nil
		case token.NullTag:
			return nil, nil
		case token.BinaryTag:
			v, err := d.nodeToValue(ctx, n.Value)
			if err != nil {
				return nil, err
			}
			str, ok := v.(string)
			if !ok {
				return nil, errors.ErrSyntax(
					fmt.Sprintf("cannot convert %q to string", fmt.Sprint(v)),
					n.Value.GetToken(),
				)
			}
			b, _ := base64.StdEncoding.DecodeString(str)
			return b, nil
		case token.BooleanTag:
			v, err := d.nodeToValue(ctx, n.Value)
			if err != nil {
				return nil, err
			}
			str := strings.ToLower(fmt.Sprint(v))
			b, err := strconv.ParseBool(str)
			if err == nil {
				return b, nil
			}
			switch str {
			case "yes":
				return true, nil
			case "no":
				return false, nil
			}
			return nil, errors.ErrSyntax(fmt.Sprintf("cannot convert %q to boolean", fmt.Sprint(v)), n.Value.GetToken())
		case token.StringTag:
			v, err := d.nodeToValue(ctx, n.Value)
			if err != nil {
				return nil, err
			}
			if v == nil {
				return "", nil
			}
			return fmt.Sprint(v), nil
		case token.MappingTag:
			return d.nodeToValue(ctx, n.Value)
		default:
			return d.nodeToValue(ctx, n.Value)
		}
	case *ast.AnchorNode:
		anchorName := d.anchorKey(n)

		// To handle the case where alias is processed recursively, the result of alias can be set to nil in advance.
		d.anchorNodeMap[anchorName] = nil
		mark := d.openNulls
		anchorValue, err := d.nodeToValue(withAnchor(ctx, anchorName), n.Value)
		if err != nil {
			delete(d.anchorNodeMap, anchorName)
			return nil, err
		}
		d.anchorNodeMap[anchorName] = n.Value
		if d.keeps(mark) {
			d.anchorValueMap[anchorName] = reflect.ValueOf(anchorValue)
			delete(d.anchorUnknown, anchorName)
		}
		return anchorValue, nil
	case *ast.AliasNode:
		text := d.aliasKey(n)
		if _, exists := getAnchorMap(ctx)[text]; exists {
			// self recursion.
			d.openNulls++
			return nil, nil
		}
		if v, exists := d.anchorValueMap[text]; exists {
			if !v.IsValid() {
				return nil, nil
			}
			return v.Interface(), nil
		}
		if node, exists := d.anchorNodeMap[text]; exists {
			// A resolver can bind an alias to an anchor that the decoder has
			// not read.
			return d.readAliasedAnchor(ctx, text, node)
		}
		return nil, errors.ErrSyntax(fmt.Sprintf("could not find alias %q", n.Value.GetToken().Value), n.Value.GetToken())
	case *ast.LiteralNode:
		return n.Value.GetValue(), nil
	case *ast.MappingKeyNode:
		return d.nodeToValue(ctx, n.Value)
	case *ast.MappingValueNode:
		if n.Key.IsMergeKey() {
			ctx, err := d.enterMerge(ctx, n.Value)
			if err != nil {
				return nil, err
			}
			iter, err := d.mapEntries(ctx, n.Value, true)
			if err != nil {
				return nil, err
			}
			if d.useOrderedMap {
				m := MapSlice{}
				for iter.Next() {
					if err := d.setToOrderedMapValue(iter.ctx, iter.KeyValue(), &m); err != nil {
						return nil, err
					}
				}
				return m, nil
			}
			m := make(map[string]any)
			for iter.Next() {
				if err := d.setToMapValue(iter.ctx, iter.KeyValue(), m); err != nil {
					return nil, err
				}
			}
			return m, nil
		}
		key, err := d.mapKeyNodeToString(ctx, n.Key)
		if err != nil {
			return nil, err
		}
		if d.useOrderedMap {
			v, err := d.nodeToValue(ctx, n.Value)
			if err != nil {
				return nil, err
			}
			return MapSlice{{Key: key, Value: v}}, nil
		}
		v, err := d.nodeToValue(ctx, n.Value)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{key: v}, nil
	case *ast.MappingNode:
		if d.useOrderedMap {
			m := make(MapSlice, 0, len(n.Values))
			for _, value := range n.Values {
				if err := d.setToOrderedMapValue(ctx, value, &m); err != nil {
					return nil, err
				}
			}
			return m, nil
		}
		m := make(map[string]interface{}, len(n.Values))
		for _, value := range n.Values {
			if err := d.setToMapValue(ctx, value, m); err != nil {
				return nil, err
			}
		}
		return m, nil
	case *ast.SequenceNode:
		v := make([]interface{}, 0, len(n.Values))
		for _, value := range n.Values {
			vv, err := d.nodeToValue(ctx, value)
			if err != nil {
				return nil, err
			}
			v = append(v, vv)
		}
		return v, nil
	}
	return nil, nil
}

func (d *Decoder) getMapNode(node ast.Node, isMerge bool) (ast.MapNode, error) {
	d.stepIn()
	defer d.stepOut()
	if d.isExceededMaxDepth() {
		return nil, ErrExceededMaxDepth
	}

	switch n := node.(type) {
	case ast.MapNode:
		return n, nil
	case *ast.AnchorNode:
		anchorName := d.anchorKey(n)
		d.anchorNodeMap[anchorName] = n.Value
		return d.getMapNode(n.Value, isMerge)
	case *ast.AliasNode:
		node := d.anchorNodeMap[d.aliasKey(n)]
		if node == nil {
			return nil, &MissingAnchorError{Alias: n}
		}
		if m, ok := node.(*ast.MappingNode); ok && d.bounded {
			// The caller reads each entry of the anchor again.
			d.aliasNodesLeft -= len(m.Values)
		}
		return d.getMapNode(node, isMerge)
	case *ast.TagNode:
		return d.getMapNode(n.Value, isMerge)
	case *ast.SequenceNode:
		if !isMerge {
			return nil, errors.ErrUnexpectedNodeType(node.Type(), ast.MappingType, node.GetToken())
		}
		var mapNodes []ast.MapNode
		for _, value := range n.Values {
			mapNode, err := d.getMapNode(value, false)
			if err != nil {
				return nil, err
			}
			mapNodes = append(mapNodes, mapNode)
		}
		return ast.SequenceMergeValue(mapNodes...), nil
	}
	return nil, errors.ErrUnexpectedNodeType(node.Type(), ast.MappingType, node.GetToken())
}

func (d *Decoder) getArrayNode(node ast.Node) (ast.ArrayNode, error) {
	d.stepIn()
	defer d.stepOut()
	if d.isExceededMaxDepth() {
		return nil, ErrExceededMaxDepth
	}

	if _, ok := node.(*ast.NullNode); ok {
		return nil, nil
	}
	if anchor, ok := node.(*ast.AnchorNode); ok {
		return d.getArrayNode(anchor.Value)
	}
	if alias, ok := node.(*ast.AliasNode); ok {
		node := d.anchorNodeMap[d.aliasKey(alias)]
		if node == nil {
			return nil, &MissingAnchorError{Alias: alias}
		}
		return d.getArrayNode(node)
	}
	if tag, ok := node.(*ast.TagNode); ok {
		return d.getArrayNode(tag.Value)
	}
	arrayNode, ok := node.(ast.ArrayNode)
	if !ok {
		return nil, errors.ErrUnexpectedNodeType(node.Type(), ast.SequenceType, node.GetToken())
	}
	return arrayNode, nil
}

func (d *Decoder) convertValue(v reflect.Value, typ reflect.Type, src ast.Node) (reflect.Value, error) {
	if typ.Kind() != reflect.String {
		if !v.Type().ConvertibleTo(typ) {

			// Special case for "strings -> floats" aka scientific notation
			// If the destination type is a float and the source type is a string, check if we can
			// use strconv.ParseFloat to convert the string to a float.
			if (typ.Kind() == reflect.Float32 || typ.Kind() == reflect.Float64) &&
				v.Type().Kind() == reflect.String {
				if f, err := strconv.ParseFloat(v.String(), 64); err == nil {
					if typ.Kind() == reflect.Float32 {
						return reflect.ValueOf(float32(f)), nil
					} else if typ.Kind() == reflect.Float64 {
						return reflect.ValueOf(f), nil
					}
					// else, fall through to the error below
				}
			}
			return reflect.Zero(typ), errors.ErrTypeMismatch(typ, v.Type(), src.GetToken())
		}
		return v.Convert(typ), nil
	}
	// cast value to string
	var strVal string
	switch v.Type().Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		strVal = strconv.FormatInt(v.Int(), 10)
	case reflect.Float32, reflect.Float64:
		strVal = fmt.Sprint(v.Float())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		strVal = strconv.FormatUint(v.Uint(), 10)
	case reflect.Bool:
		strVal = strconv.FormatBool(v.Bool())
	default:
		if !v.Type().ConvertibleTo(typ) {
			return reflect.Zero(typ), errors.ErrTypeMismatch(typ, v.Type(), src.GetToken())
		}
		return v.Convert(typ), nil
	}

	val := reflect.ValueOf(strVal)
	if val.Type() != typ {
		// Handle named types, e.g., `type MyString string`
		val = val.Convert(typ)
	}
	return val, nil
}

func (d *Decoder) deleteStructKeys(structType reflect.Type, unknownFields map[string]ast.Node) error {
	if structType.Kind() == reflect.Ptr {
		structType = structType.Elem()
	}
	if structType.Kind() == reflect.Map || structType.Kind() == reflect.Interface {
		// An inline map or interface takes every key, so no
		// unknown field remains.
		clear(unknownFields)
		return nil
	}
	if structType.Kind() != reflect.Struct {
		return nil
	}
	structFieldMap, err := structFieldMap(structType)
	if err != nil {
		return err
	}

	for j := 0; j < structType.NumField(); j++ {
		field := structType.Field(j)
		if isIgnoredStructField(field) {
			continue
		}

		structField, exists := structFieldMap[field.Name]
		if !exists {
			continue
		}

		if structField.IsInline && structField.IsAutoAlias {
			// An inline field with the alias option reads the anchor of
			// a merge key, not the keys of the mapping.
			continue
		}
		if structField.IsInline {
			_ = d.deleteStructKeys(field.Type, unknownFields)
		} else {
			delete(unknownFields, structField.RenderName)
		}
	}
	return nil
}

// textBudget returns the count of the bytes that the formatter may still
// write for aliases, or nil for a decode without [AliasBudget].
func (d *Decoder) textBudget() *int {
	if !d.bounded {
		return nil
	}
	return &d.aliasBytesLeft
}

func (d *Decoder) unmarshalableDocument(ctx context.Context, node ast.Node) ([]byte, error) {
	doc := format.FormatNodeWithResolvedAlias(node, d.anchorNodeMap, getAnchorMap(ctx), d.aliasKey, d.textBudget(), &d.origins)
	if d.bounded && d.aliasBytesLeft < 0 {
		return nil, ErrAliasBudget
	}
	return []byte(doc), nil
}

func (d *Decoder) unmarshalableText(ctx context.Context, node ast.Node) ([]byte, bool) {
	doc := format.FormatNodeWithResolvedAlias(node, d.anchorNodeMap, getAnchorMap(ctx), d.aliasKey, d.textBudget(), &d.origins)
	var v string
	if err := Unmarshal([]byte(doc), &v); err != nil {
		return nil, false
	}
	return []byte(v), true
}

type jsonUnmarshaler interface {
	UnmarshalJSON([]byte) error
}

func (d *Decoder) existsTypeInCustomUnmarshalerMap(t reflect.Type) bool {
	if _, exists := d.customUnmarshalerMap[t]; exists {
		return true
	}

	globalCustomUnmarshalerMu.Lock()
	defer globalCustomUnmarshalerMu.Unlock()
	if _, exists := globalCustomUnmarshalerMap[t]; exists {
		return true
	}
	return false
}

func (d *Decoder) unmarshalerFromCustomUnmarshalerMap(t reflect.Type) (func(context.Context, interface{}, []byte) error, bool) {
	if unmarshaler, exists := d.customUnmarshalerMap[t]; exists {
		return unmarshaler, exists
	}

	globalCustomUnmarshalerMu.Lock()
	defer globalCustomUnmarshalerMu.Unlock()
	if unmarshaler, exists := globalCustomUnmarshalerMap[t]; exists {
		return unmarshaler, exists
	}
	return nil, false
}

func (d *Decoder) canDecodeByUnmarshaler(dst reflect.Value) bool {
	ptrValue := dst.Addr()
	if d.existsTypeInCustomUnmarshalerMap(ptrValue.Type()) {
		return true
	}
	iface := ptrValue.Interface()
	switch iface.(type) {
	case BytesUnmarshalerContext,
		BytesUnmarshaler,
		InterfaceUnmarshalerContext,
		InterfaceUnmarshaler,
		NodeUnmarshaler,
		NodeUnmarshalerContext,
		*time.Time,
		*time.Duration,
		encoding.TextUnmarshaler:
		return true
	case jsonUnmarshaler:
		return d.useJSONUnmarshaler
	}
	return false
}

func (d *Decoder) decodeByUnmarshaler(ctx context.Context, dst reflect.Value, src ast.Node) error {
	ptrValue := dst.Addr()
	if unmarshaler, exists := d.unmarshalerFromCustomUnmarshalerMap(ptrValue.Type()); exists {
		b, err := d.unmarshalableDocument(ctx, src)
		if err != nil {
			return err
		}
		if err := unmarshaler(ctx, ptrValue.Interface(), b); err != nil {
			return d.unmarshalerError(err, src)
		}
		return nil
	}
	iface := ptrValue.Interface()

	if unmarshaler, ok := iface.(BytesUnmarshalerContext); ok {
		b, err := d.unmarshalableDocument(ctx, src)
		if err != nil {
			return err
		}
		if err := unmarshaler.UnmarshalYAML(ctx, b); err != nil {
			return d.unmarshalerError(err, src)
		}
		return nil
	}

	if unmarshaler, ok := iface.(BytesUnmarshaler); ok {
		b, err := d.unmarshalableDocument(ctx, src)
		if err != nil {
			return err
		}
		if err := unmarshaler.UnmarshalYAML(b); err != nil {
			return d.unmarshalerError(err, src)
		}
		return nil
	}

	if unmarshaler, ok := iface.(InterfaceUnmarshalerContext); ok {
		calls := &callbackCalls{d: d, ctx: ctx, src: src}
		if err := unmarshaler.UnmarshalYAML(ctx, calls.decode); err != nil {
			return calls.failed(err)
		}
		return nil
	}

	if unmarshaler, ok := iface.(InterfaceUnmarshaler); ok {
		calls := &callbackCalls{d: d, ctx: ctx, src: src}
		if err := unmarshaler.UnmarshalYAML(calls.decode); err != nil {
			return calls.failed(err)
		}
		return nil
	}

	if unmarshaler, ok := iface.(NodeUnmarshaler); ok {
		if err := unmarshaler.UnmarshalYAML(src); err != nil {
			return d.unmarshalerError(err, src)
		}

		return nil
	}

	if unmarshaler, ok := iface.(NodeUnmarshalerContext); ok {
		if err := unmarshaler.UnmarshalYAML(ctx, src); err != nil {
			return d.unmarshalerError(err, src)
		}

		return nil
	}

	if _, ok := iface.(*time.Time); ok {
		return d.decodeTime(ctx, dst, src)
	}

	if _, ok := iface.(*time.Duration); ok {
		return d.decodeDuration(ctx, dst, src)
	}

	if unmarshaler, isText := iface.(encoding.TextUnmarshaler); isText {
		b, ok := d.unmarshalableText(ctx, src)
		if d.bounded && d.aliasBytesLeft < 0 {
			return ErrAliasBudget
		}
		if ok {
			if err := unmarshaler.UnmarshalText(b); err != nil {
				return d.unmarshalerError(err, src)
			}
			return nil
		}
	}

	if d.useJSONUnmarshaler {
		if unmarshaler, ok := iface.(jsonUnmarshaler); ok {
			b, err := d.unmarshalableDocument(ctx, src)
			if err != nil {
				return err
			}
			jsonBytes, err := YAMLToJSON(b)
			if err != nil {
				return err
			}
			jsonBytes = bytes.TrimRight(jsonBytes, "\n")
			if err := unmarshaler.UnmarshalJSON(jsonBytes); err != nil {
				return d.unmarshalerError(err, src)
			}
			return nil
		}
	}

	return ErrTextOnly
}

// callbackCalls holds the calls of the callback that the decoder hands an
// InterfaceUnmarshaler for a value that decodes from src.
type callbackCalls struct {
	d   *Decoder
	ctx context.Context
	src ast.Node
	// The errors that the calls returned, and the unknown fields that
	// each of those calls reported. A call that fails holds its unknown
	// fields, because the unmarshaler can drop its result and read the
	// value another way.
	passed []error
	held   [][]error
}

// decode is the callback. It decodes src into v, which must be a pointer.
func (c *callbackCalls) decode(v interface{}) error {
	rv := reflect.ValueOf(v)
	if rv.Type().Kind() != reflect.Ptr {
		return ErrDecodeRequiredPointerType
	}
	reports, err := c.d.captureUnknown(false, func() error {
		return c.d.decodeValue(c.ctx, rv.Elem(), c.src)
	})
	if err != nil {
		c.passed = append(c.passed, err)
		c.held = append(c.held, reports)
		return err
	}
	c.d.reportUnknown(reports)
	return nil
}

// failed returns err, the error of the unmarshaler, as unmarshalerError
// does. When err is the error of a call, unchanged, it reports the unknown
// fields of that call.
func (c *callbackCalls) failed(err error) error {
	for i, p := range c.passed {
		if sameError(err, p) {
			c.d.reportUnknown(c.held[i])
			break
		}
	}
	return c.d.unmarshalerError(err, c.src, c.passed...)
}

// ErrTextOnly is the error of a mapping or a sequence for a type that
// decodes itself from text alone, through an UnmarshalText method. The
// decoder does not call the method for such a node. Its message is
// go-yaml's.
var ErrTextOnly = errors.New("does not implemented Unmarshaler")

var (
	astNodeType = reflect.TypeOf((*ast.Node)(nil)).Elem()
)

func (d *Decoder) decodeValue(ctx context.Context, dst reflect.Value, src ast.Node) error {
	d.stepIn()
	defer d.stepOut()
	if d.isExceededMaxDepth() {
		return ErrExceededMaxDepth
	}
	if err := d.tick(ctx); err != nil {
		return err
	}
	if !dst.IsValid() {
		return nil
	}

	if src.Type() == ast.AnchorType {
		anchor, _ := src.(*ast.AnchorNode)
		anchorName := d.anchorKey(anchor)
		mark := d.openNulls
		reports, err := d.captureUnknown(true, func() error {
			return d.decodeValue(withAnchor(ctx, anchorName), dst, anchor.Value)
		})
		if err != nil {
			return err
		}
		if d.keeps(mark) {
			d.anchorValueMap[anchorName] = dst
			d.keepUnknown(anchorName, len(d.path), reports)
		}
		return nil
	}
	if d.canDecodeByUnmarshaler(dst) {
		if err := d.decodeByUnmarshaler(ctx, dst, src); err != nil {
			return d.ownError(err, src)
		}
		return nil
	}
	valueType := dst.Type()
	switch valueType.Kind() {
	case reflect.Ptr:
		// An alias to a null is a null too.
		content := src
		if alias, ok := src.(*ast.AliasNode); ok {
			if node := d.anchorNodeMap[d.aliasKey(alias)]; node != nil {
				content = node
			}
		}
		if content.Type() == ast.NullType {
			// set nil value to pointer
			dst.Set(reflect.Zero(valueType))
			return nil
		}
		if n, ok := pointedNode(valueType, content); ok {
			dst.Set(n)
			return nil
		}
		v := d.createDecodableValue(dst.Type())
		if v.Kind() == reflect.Ptr {
			srcValue, err := d.nodeToValue(ctx, src)
			if err != nil {
				return err
			}
			return errors.ErrTypeMismatch(valueType, reflect.TypeOf(srcValue), src.GetToken())
		}
		// The decode starts from the value that the pointer reaches.
		held := dst
		for held.Kind() == reflect.Ptr && !held.IsNil() {
			held = held.Elem()
		}
		if held.Type() == v.Type() {
			v.Set(held)
		}
		if err := d.decodeValue(ctx, v, src); err != nil {
			return err
		}
		castedValue, err := d.castToAssignableValue(v, dst.Type(), src)
		if err != nil {
			return err
		}
		dst.Set(castedValue)
		return nil
	case reflect.Interface:
		if dst.Type() == astNodeType {
			dst.Set(reflect.ValueOf(src))
			return nil
		}
		srcVal, err := d.nodeToValue(ctx, src)
		if err != nil {
			return err
		}
		v := reflect.ValueOf(srcVal)
		if v.IsValid() {
			dst.Set(v)
		} else {
			dst.Set(reflect.Zero(valueType))
		}
	case reflect.Map:
		return d.decodeMap(ctx, dst, src)
	case reflect.Array:
		return d.decodeArray(ctx, dst, src)
	case reflect.Slice:
		if mapSlice, ok := dst.Addr().Interface().(*MapSlice); ok {
			return d.decodeMapSlice(ctx, mapSlice, src)
		}
		return d.decodeSlice(ctx, dst, src)
	case reflect.Struct:
		if mapItem, ok := dst.Addr().Interface().(*MapItem); ok {
			return d.decodeMapItem(ctx, mapItem, src)
		}
		return d.decodeStruct(ctx, dst, src)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := d.nodeToValue(ctx, src)
		if err != nil {
			return err
		}
		if i, ok := v.(int); ok {
			// nodeToValue reads an integer under a !!int tag as a Go int.
			v = int64(i)
		}
		switch vv := v.(type) {
		case int64:
			if !dst.OverflowInt(vv) {
				dst.SetInt(vv)
				return nil
			}
		case uint64:
			if vv <= math.MaxInt64 && !dst.OverflowInt(int64(vv)) {
				dst.SetInt(int64(vv))
				return nil
			}
		case float64:
			if vv <= math.MaxInt64 && !dst.OverflowInt(int64(vv)) {
				dst.SetInt(int64(vv))
				return nil
			}
		case string: // handle scientific notation
			if i, err := strconv.ParseFloat(vv, 64); err == nil {
				if 0 <= i && i <= math.MaxUint64 && !dst.OverflowInt(int64(i)) {
					dst.SetInt(int64(i))
					return nil
				}
			} else { // couldn't be parsed as float
				return errors.ErrTypeMismatch(valueType, reflect.TypeOf(v), src.GetToken())
			}
		default:
			return errors.ErrTypeMismatch(valueType, reflect.TypeOf(v), src.GetToken())
		}
		return errors.ErrOverflow(valueType, fmt.Sprint(v), src.GetToken())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := d.nodeToValue(ctx, src)
		if err != nil {
			return err
		}
		if i, ok := v.(int); ok {
			// nodeToValue reads an integer under a !!int tag as a Go int.
			v = int64(i)
		}
		switch vv := v.(type) {
		case int64:
			if 0 <= vv && !dst.OverflowUint(uint64(vv)) {
				dst.SetUint(uint64(vv))
				return nil
			}
		case uint64:
			if !dst.OverflowUint(vv) {
				dst.SetUint(vv)
				return nil
			}
		case float64:
			if 0 <= vv && vv <= math.MaxUint64 && !dst.OverflowUint(uint64(vv)) {
				dst.SetUint(uint64(vv))
				return nil
			}
		case string: // handle scientific notation
			if i, err := strconv.ParseFloat(vv, 64); err == nil {
				if 0 <= i && i <= math.MaxUint64 && !dst.OverflowUint(uint64(i)) {
					dst.SetUint(uint64(i))
					return nil
				}
			} else { // couldn't be parsed as float
				return errors.ErrTypeMismatch(valueType, reflect.TypeOf(v), src.GetToken())
			}

		default:
			return errors.ErrTypeMismatch(valueType, reflect.TypeOf(v), src.GetToken())
		}
		return errors.ErrOverflow(valueType, fmt.Sprint(v), src.GetToken())
	}
	srcVal, err := d.nodeToValue(ctx, src)
	if err != nil {
		return err
	}
	v := reflect.ValueOf(srcVal)
	if v.IsValid() {
		convertedValue, err := d.convertValue(v, dst.Type(), src)
		if err != nil {
			return err
		}
		dst.Set(convertedValue)
	}
	return nil
}

func (d *Decoder) createDecodableValue(typ reflect.Type) reflect.Value {
	// A type such as "type P *P" never ends in a type that is not a
	// pointer, so the walk gives up, and the caller rejects the pointer.
	for i := 0; typ.Kind() == reflect.Ptr && i < maxPointerDepth; i++ {
		typ = typ.Elem()
	}
	return reflect.New(typ).Elem()
}

func (d *Decoder) castToAssignableValue(value reflect.Value, target reflect.Type, src ast.Node) (reflect.Value, error) {
	if target.Kind() != reflect.Ptr {
		if !value.Type().AssignableTo(target) {
			return reflect.Value{}, errors.ErrTypeMismatch(target, value.Type(), src.GetToken())
		}
		return value, nil
	}

	const maxAddrCount = 5

	for i := 0; i < maxAddrCount; i++ {
		if value.Type().AssignableTo(target) {
			break
		}
		if !value.CanAddr() {
			// A pointer to a pointer: the inner pointer needs a place.
			held := reflect.New(value.Type())
			held.Elem().Set(value)
			value = held
			continue
		}
		value = value.Addr()
	}
	if !value.Type().AssignableTo(target) {
		return reflect.Value{}, errors.ErrTypeMismatch(target, value.Type(), src.GetToken())
	}
	return value, nil
}

func (d *Decoder) createDecodedNewValue(
	ctx context.Context, typ reflect.Type, defaultVal reflect.Value, node ast.Node,
) (reflect.Value, error) {
	if node.Type() == ast.AliasType {
		aliasName := d.aliasKey(node.(*ast.AliasNode))
		if _, open := getAnchorMap(ctx)[aliasName]; open {
			// An alias inside the anchor it names reads as null, as
			// nodeToValue reads it.
			d.openNulls++
			return d.createDecodedNewValue(ctx, typ, defaultVal, ast.Null(token.New("null", "null", node.GetToken().Position)))
		}
		if anchor := d.anchorNodeMap[aliasName]; anchor != nil {
			// A pointer to the type of the anchor's node points to the
			// node itself, not to the value that the decoder keeps.
			if n, ok := pointedNode(typ, anchor); ok {
				return n, nil
			}
		}
		value := d.anchorValueMap[aliasName]
		if value.IsValid() {
			v, err := d.castToAssignableValue(value, typ, node)
			if err == nil {
				// The alias takes the value and does not read the
				// anchor, so it reports the unknown fields of that read.
				d.replayUnknown(aliasName)
				return v, nil
			}
		}
		anchor, exists := d.anchorNodeMap[aliasName]
		if exists {
			// The alias reads its anchor, so an alias inside reads as null.
			node = anchor
			ctx = withAnchor(ctx, aliasName)
			d.aliasDepth++
			defer func() { d.aliasDepth-- }()
		}
	}
	var newValue reflect.Value
	if node.Type() == ast.NullType {
		newValue = reflect.New(typ).Elem()
	} else {
		newValue = d.createDecodableValue(typ)
	}
	for defaultVal.Kind() == reflect.Ptr {
		defaultVal = defaultVal.Elem()
	}
	if defaultVal.IsValid() && defaultVal.Type().AssignableTo(newValue.Type()) {
		newValue.Set(defaultVal)
	}
	if node.Type() != ast.NullType {
		if err := d.decodeValue(ctx, newValue, node); err != nil {
			return reflect.Value{}, err
		}
	}
	if n, ok := pointedNode(typ, node); ok {
		return n, nil
	}
	return d.castToAssignableValue(newValue, typ, node)
}

// pointedNode returns node, or the value of the anchors and tags around
// node, when typ is a pointer to the type of that node. A pointer to the
// type of the node points to the node itself.
func pointedNode(typ reflect.Type, node ast.Node) (reflect.Value, bool) {
	for {
		if reflect.TypeOf(node) == typ {
			return reflect.ValueOf(node), true
		}
		switch n := node.(type) {
		case *ast.AnchorNode:
			node = n.Value
		case *ast.TagNode:
			node = n.Value
		default:
			return reflect.Value{}, false
		}
	}
}

// readAliasedAnchor reads node, the content of the anchor with the key key
// that an alias reads, as nodeToValue reads the alias. The alias reads its
// anchor, so an alias inside reads as null.
//
// With a resolver from [ResolveAlias], the key names one node. The decoder
// keeps the value under the rule of [Decoder.keeps], as it keeps the value
// of an anchor that it reads. Each later alias of the anchor takes the
// value and does not read the anchor again. A decode of a node below the
// document thus reads each anchor outside the node once, as it reads each
// anchor inside the node. Without a resolver, the key is a name that a later
// anchor can take, so the decoder keeps nothing.
func (d *Decoder) readAliasedAnchor(ctx context.Context, key string, node ast.Node) (any, error) {
	d.aliasDepth++
	defer func() { d.aliasDepth-- }()
	mark := d.openNulls
	v, err := d.nodeToValue(withAnchor(ctx, key), node)
	if err != nil {
		return nil, err
	}
	if d.resolveAlias != nil && d.keeps(mark) {
		d.anchorValueMap[key] = reflect.ValueOf(v)
		delete(d.anchorUnknown, key)
	}
	return v, nil
}

// keyToNodeMap returns the key or the value node of each entry of node by
// its key. When ctxs is not nil, it records there the context for the read
// of each node that it returns. The decoder reads the entries that a merge
// key brings in inside the anchor of the merge, and the entries of a
// mapping built for an inline field as entryContext gives them.
func (d *Decoder) keyToNodeMap(ctx context.Context, node ast.Node, ignoreMergeKey bool, getKeyOrValueNode func(*ast.MapNodeIter) ast.Node, ctxs map[ast.Node]context.Context) (map[string]ast.Node, error) {
	d.stepIn()
	defer d.stepOut()
	if d.isExceededMaxDepth() {
		return nil, ErrExceededMaxDepth
	}

	mapNode, err := d.getMapNode(node, false)
	if err != nil {
		return nil, err
	}
	keyMap := map[string]struct{}{}
	keyToNodeMap := map[string]ast.Node{}
	mapIter := mapNode.MapRange()
	for mapIter.Next() {
		keyNode := mapIter.Key()
		if keyNode.IsMergeKey() {
			if ignoreMergeKey {
				continue
			}
			mergeCtx, err := d.enterMerge(ctx, mapIter.Value())
			if err != nil {
				return nil, err
			}
			mergeMap, err := d.keyToNodeMap(mergeCtx, mapIter.Value(), ignoreMergeKey, getKeyOrValueNode, ctxs)
			if err != nil {
				return nil, err
			}
			for k, v := range mergeMap {
				if err := d.validateDuplicateKey(keyMap, k, v); err != nil {
					return nil, err
				}
				keyToNodeMap[k] = v
			}
		} else {
			keyVal, err := d.nodeToValue(ctx, keyNode)
			if err != nil {
				return nil, err
			}
			key, ok := keyVal.(string)
			if !ok {
				return nil, err
			}
			if err := d.validateDuplicateKey(keyMap, key, keyNode); err != nil {
				return nil, err
			}
			keyToNodeMap[key] = getKeyOrValueNode(mapIter)
			if ctxs != nil {
				ctxs[keyToNodeMap[key]] = entryContext(ctx, keyToNodeMap[key])
			}
		}
	}
	return keyToNodeMap, nil
}

func (d *Decoder) keyToKeyNodeMap(ctx context.Context, node ast.Node, ignoreMergeKey bool) (map[string]ast.Node, error) {
	m, err := d.keyToNodeMap(ctx, node, ignoreMergeKey, func(nodeMap *ast.MapNodeIter) ast.Node { return nodeMap.Key() }, nil)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// keyToValueNodeMap also returns the context for the read of each value
// node, as keyToNodeMap records it.
func (d *Decoder) keyToValueNodeMap(ctx context.Context, node ast.Node, ignoreMergeKey bool) (map[string]ast.Node, map[ast.Node]context.Context, error) {
	ctxs := map[ast.Node]context.Context{}
	m, err := d.keyToNodeMap(ctx, node, ignoreMergeKey, func(nodeMap *ast.MapNodeIter) ast.Node { return nodeMap.Value() }, ctxs)
	if err != nil {
		return nil, nil, err
	}
	return m, ctxs, nil
}

func (d *Decoder) setDefaultValueIfConflicted(v reflect.Value, fieldMap StructFieldMap) error {
	for v.Type().Kind() == reflect.Ptr {
		v = v.Elem()
	}
	typ := v.Type()
	if typ.Kind() != reflect.Struct {
		return nil
	}
	embeddedStructFieldMap, err := structFieldMap(typ)
	if err != nil {
		return err
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if isIgnoredStructField(field) {
			continue
		}
		structField := embeddedStructFieldMap[field.Name]
		if !fieldMap.isIncludedRenderName(structField.RenderName) {
			continue
		}
		// if declared same key name, set default value
		fieldValue := v.Field(i)
		if fieldValue.CanSet() {
			fieldValue.Set(reflect.Zero(fieldValue.Type()))
		}
	}
	return nil
}

// This is a subset of the formats allowed by the regular expression
// defined at http://yaml.org/type/timestamp.html.
var allowedTimestampFormats = []string{
	"2006-1-2T15:4:5.999999999Z07:00", // RCF3339Nano with short date fields.
	"2006-1-2t15:4:5.999999999Z07:00", // RFC3339Nano with short date fields and lower-case "t".
	"2006-1-2 15:4:5.999999999",       // space separated with no time zone
	"2006-1-2",                        // date only
}

func (d *Decoder) castToTime(ctx context.Context, src ast.Node) (time.Time, error) {
	if src == nil {
		return time.Time{}, nil
	}
	v, err := d.nodeToValue(ctx, src)
	if err != nil {
		return time.Time{}, err
	}
	if t, ok := v.(time.Time); ok {
		return t, nil
	}
	s, ok := v.(string)
	if !ok {
		return time.Time{}, errors.ErrTypeMismatch(reflect.TypeOf(time.Time{}), reflect.TypeOf(v), src.GetToken())
	}
	for _, format := range allowedTimestampFormats {
		t, err := time.Parse(format, s)
		if err != nil {
			// invalid format
			continue
		}
		return t, nil
	}
	return time.Time{}, nil
}

func (d *Decoder) decodeTime(ctx context.Context, dst reflect.Value, src ast.Node) error {
	t, err := d.castToTime(ctx, src)
	if err != nil {
		return err
	}
	dst.Set(reflect.ValueOf(t))
	return nil
}

func (d *Decoder) castToDuration(ctx context.Context, src ast.Node) (time.Duration, error) {
	if src == nil {
		return 0, nil
	}
	v, err := d.nodeToValue(ctx, src)
	if err != nil {
		return 0, err
	}
	if t, ok := v.(time.Duration); ok {
		return t, nil
	}
	s, ok := v.(string)
	if !ok {
		return 0, errors.ErrTypeMismatch(reflect.TypeOf(time.Duration(0)), reflect.TypeOf(v), src.GetToken())
	}
	t, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	return t, nil
}

func (d *Decoder) decodeDuration(ctx context.Context, dst reflect.Value, src ast.Node) error {
	t, err := d.castToDuration(ctx, src)
	if err != nil {
		return err
	}
	dst.Set(reflect.ValueOf(t))
	return nil
}

// getMergeAliasName support single alias only
func (d *Decoder) getMergeAliasName(src ast.Node) string {
	mapNode, err := d.getMapNode(src, true)
	if err != nil {
		return ""
	}
	mapIter := mapNode.MapRange()
	for mapIter.Next() {
		key := mapIter.Key()
		value := mapIter.Value()
		if key.IsMergeKey() && value.Type() == ast.AliasType {
			return d.aliasKey(value.(*ast.AliasNode))
		}
	}
	return ""
}

func (d *Decoder) decodeStruct(ctx context.Context, dst reflect.Value, src ast.Node) error {
	if src == nil {
		return nil
	}
	d.stepIn()
	defer d.stepOut()
	if d.isExceededMaxDepth() {
		return ErrExceededMaxDepth
	}

	structType := dst.Type()
	srcValue := reflect.ValueOf(src)
	srcType := srcValue.Type()
	if srcType.Kind() == reflect.Ptr {
		srcType = srcType.Elem()
		srcValue = srcValue.Elem()
	}
	if structType == srcType {
		// dst value implements ast.Node
		dst.Set(srcValue)
		return nil
	}
	structFieldMap, err := structFieldMap(structType)
	if err != nil {
		return err
	}
	ignoreMergeKey := structFieldMap.hasMergeProperty()
	keyToNodeMap, valueCtxs, err := d.keyToValueNodeMap(ctx, src, ignoreMergeKey)
	if err != nil {
		return err
	}
	var unknownFields map[string]ast.Node
	if d.disallowUnknownField {
		unknownFields, err = d.keyToKeyNodeMap(ctx, src, ignoreMergeKey)
		if err != nil {
			return err
		}
	}

	aliasName := d.getMergeAliasName(src)
	var found []error

	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		if isIgnoredStructField(field) {
			continue
		}
		structField := structFieldMap[field.Name]
		if structField.IsInline {
			fieldValue := dst.FieldByName(field.Name)
			if structField.IsAutoAlias {
				if aliasName != "" {
					newFieldValue := d.anchorValueMap[aliasName]
					if node := d.anchorNodeMap[aliasName]; !newFieldValue.IsValid() && node != nil && d.resolveAlias != nil {
						// The resolver can bind the alias to an anchor that
						// the decoder has not read, such as one outside the
						// node of the decode. The field takes the anchor as
						// nodeToValue reads it, as it does after the decoder
						// reads the anchor itself.
						v, err := d.readAliasedAnchor(ctx, aliasName, node)
						if err != nil {
							return err
						}
						newFieldValue = reflect.ValueOf(v)
					}
					if newFieldValue.IsValid() {
						value, err := d.castToAssignableValue(newFieldValue, fieldValue.Type(), d.anchorNodeMap[aliasName])
						if err != nil {
							return err
						}
						fieldValue.Set(value)
					}
				}
				continue
			}
			if !fieldValue.CanSet() {
				return fmt.Errorf("cannot set embedded type as unexported field %s.%s", field.PkgPath, field.Name)
			}
			if fieldValue.Type().Kind() == reflect.Ptr && src.Type() == ast.NullType {
				// set nil value to pointer
				fieldValue.Set(reflect.Zero(fieldValue.Type()))
				continue
			}
			// The mapping holds the entries in the order of the document,
			// so each decode reads them, and finds their errors, in the
			// same order.
			names := slices.Collect(maps.Keys(keyToNodeMap))
			sort.Slice(names, func(i, j int) bool {
				a, b := nodeOffset(keyToNodeMap[names[i]]), nodeOffset(keyToNodeMap[names[j]])
				if a == b {
					return names[i] < names[j]
				}
				return a < b
			})
			mapNode := ast.Mapping(nil, false)
			for _, k := range names {
				key := &ast.StringNode{BaseNode: &ast.BaseNode{}, Value: k}
				mapNode.Values = append(mapNode.Values, ast.MappingValue(nil, key, keyToNodeMap[k]))
			}
			newFieldValue, err := d.createDecodedNewValue(withEntryContexts(ctx, valueCtxs), fieldValue.Type(), fieldValue, mapNode)
			if d.disallowUnknownField {
				if err := d.deleteStructKeys(fieldValue.Type(), unknownFields); err != nil {
					return err
				}
			}

			if err != nil {
				d.noteField(&found, err, structType.Name(), field.Name, true)
				continue
			}
			_ = d.setDefaultValueIfConflicted(newFieldValue, structFieldMap)
			fieldValue.Set(newFieldValue)
			continue
		}
		v, exists := keyToNodeMap[structField.RenderName]
		if !exists {
			continue
		}
		delete(unknownFields, structField.RenderName)
		fieldValue := dst.FieldByName(field.Name)
		if fieldValue.Type().Kind() == reflect.Ptr && src.Type() == ast.NullType {
			// set nil value to pointer
			fieldValue.Set(reflect.Zero(fieldValue.Type()))
			continue
		}
		fieldCtx := ctx
		if c, ok := valueCtxs[v]; ok {
			fieldCtx = c
		}
		newFieldValue, err := d.below(fieldCtx, Step{Key: structField.RenderName}, fieldValue.Type(), fieldValue, v)
		if err != nil {
			d.noteField(&found, err, structType.Name(), field.Name, false)
			continue
		}
		fieldValue.Set(newFieldValue)
	}
	if len(found) != 0 && !d.collectErrors {
		return found[0]
	}

	// Ignore unknown fields when parsing an inline struct (recognized by a nil token).
	// Unknown fields are expected (they could be fields from the parent struct).
	if len(unknownFields) != 0 && d.disallowUnknownField && src.GetToken() != nil {
		// The keys go in the order of the document, so the decoder names
		// the same field at every decode.
		keys := make([]string, 0, len(unknownFields))
		for key := range unknownFields {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := unknownFields[keys[i]].GetToken(), unknownFields[keys[j]].GetToken()
			if a == nil || b == nil || a.Position.Offset == b.Position.Offset {
				return keys[i] < keys[j]
			}
			return a.Position.Offset < b.Position.Offset
		})
		for _, key := range keys {
			var ok bool
			for _, prefix := range d.allowedFieldPrefixes {
				if strings.HasPrefix(key, prefix) {
					ok = true
					break
				}
			}
			if !ok {
				err := errors.ErrUnknownField(fmt.Sprintf(`unknown field "%s"`, key), unknownFields[key].GetToken())
				if d.unknownField != nil {
					d.unknownField(d.located(err, Step{Key: key, IsKey: true}))
					continue
				}
				if !d.collectErrors {
					return err
				}
				found = append(found, d.located(err, Step{Key: key, IsKey: true}))
			}
		}
	}
	if len(found) != 0 {
		return d.noted(found, nil)
	}

	if d.validator != nil {
		if err := d.validator.Struct(dst.Interface()); err != nil {
			ev := reflect.ValueOf(err)
			if ev.Type().Kind() == reflect.Slice {
				for i := 0; i < ev.Len(); i++ {
					fieldErr, ok := ev.Index(i).Interface().(FieldError)
					if !ok {
						continue
					}
					fieldName := fieldErr.StructField()
					structField, exists := structFieldMap[fieldName]
					if !exists {
						continue
					}
					node, exists := keyToNodeMap[structField.RenderName]
					if exists {
						// TODO: to make FieldError message cutomizable
						return errors.ErrSyntax(
							fmt.Sprintf("%s", err),
							d.getParentMapTokenIfExistsForValidationError(node.Type(), node.GetToken()),
						)
					} else if t := src.GetToken(); t != nil && t.Prev != nil && t.Prev.Prev != nil {
						// A missing required field will not be in the keyToNodeMap
						// the error needs to be associated with the parent of the source node
						return errors.ErrSyntax(fmt.Sprintf("%s", err), t.Prev.Prev)
					}
				}
			}
			return err
		}
	}
	return nil
}

// nodeOffset returns the offset of the token of node in the document, or
// -1 for a node without a place.
func nodeOffset(node ast.Node) int {
	if tk := node.GetToken(); tk != nil && tk.Position != nil {
		return tk.Position.Offset
	}
	return -1
}

// getParentMapTokenIfExists if the NodeType is a container type such as MappingType or SequenceType,
// it is necessary to return the parent MapNode's colon token to represent the entire container.
func (d *Decoder) getParentMapTokenIfExistsForValidationError(typ ast.NodeType, tk *token.Token) *token.Token {
	if tk == nil {
		return nil
	}
	if typ == ast.MappingType {
		// map:
		//   key: value
		//      ^ current token ( colon )
		if tk.Prev == nil {
			return tk
		}
		key := tk.Prev
		if key.Prev == nil {
			return tk
		}
		return key.Prev
	}
	if typ == ast.SequenceType {
		// map:
		//   - value
		//   ^ current token ( sequence entry )
		if tk.Prev == nil {
			return tk
		}
		return tk.Prev
	}
	return tk
}

func (d *Decoder) decodeArray(ctx context.Context, dst reflect.Value, src ast.Node) error {
	d.stepIn()
	defer d.stepOut()
	if d.isExceededMaxDepth() {
		return ErrExceededMaxDepth
	}

	arrayNode, err := d.getArrayNode(src)
	if err != nil {
		return err
	}
	if arrayNode == nil {
		return nil
	}
	iter := arrayNode.ArrayRange()
	arrayValue := reflect.New(dst.Type()).Elem()
	arrayType := dst.Type()
	elemType := arrayType.Elem()
	idx := 0

	var found []error
	// pos counts the elements of the sequence. idx counts the slots of the
	// array, and it skips an element that does not decode.
	for pos := 0; iter.Next(); pos++ {
		v := iter.Value()
		if elemType.Kind() == reflect.Ptr && v.Type() == ast.NullType {
			// set nil value to pointer
			arrayValue.Index(idx).Set(reflect.Zero(elemType))
		} else {
			dstValue, err := d.below(ctx, Step{Index: pos, IsIndex: true}, elemType, reflect.Value{}, v)
			if err != nil {
				d.note(&found, err)
				continue
			}
			arrayValue.Index(idx).Set(dstValue)
		}
		idx++
	}
	dst.Set(arrayValue)
	return d.noted(found, nil)
}

func (d *Decoder) decodeSlice(ctx context.Context, dst reflect.Value, src ast.Node) error {
	d.stepIn()
	defer d.stepOut()
	if d.isExceededMaxDepth() {
		return ErrExceededMaxDepth
	}

	arrayNode, err := d.getArrayNode(src)
	if err != nil {
		return err
	}
	if arrayNode == nil {
		return nil
	}
	iter := arrayNode.ArrayRange()
	sliceType := dst.Type()
	sliceValue := reflect.MakeSlice(sliceType, 0, iter.Len())
	elemType := sliceType.Elem()

	var found []error
	for idx := 0; iter.Next(); idx++ {
		v := iter.Value()
		if elemType.Kind() == reflect.Ptr && v.Type() == ast.NullType {
			// set nil value to pointer
			sliceValue = reflect.Append(sliceValue, reflect.Zero(elemType))
			continue
		}
		dstValue, err := d.below(ctx, Step{Index: idx, IsIndex: true}, elemType, reflect.Value{}, v)
		if err != nil {
			d.note(&found, err)
			continue
		}
		sliceValue = reflect.Append(sliceValue, dstValue)
	}
	dst.Set(sliceValue)
	return d.noted(found, nil)
}

func (d *Decoder) decodeMapItem(ctx context.Context, dst *MapItem, src ast.Node) error {
	d.stepIn()
	defer d.stepOut()
	if d.isExceededMaxDepth() {
		return ErrExceededMaxDepth
	}

	mapIter, err := d.mapEntries(ctx, src, isMerge(ctx))
	if err != nil {
		return err
	}
	if !mapIter.Next() {
		return nil
	}
	ctx = mapIter.ctx
	key := mapIter.Key()
	value := mapIter.Value()
	if key.IsMergeKey() {
		mergeCtx, err := d.enterMerge(withMerge(ctx), value)
		if err != nil {
			return err
		}
		if err := d.decodeMapItem(mergeCtx, dst, value); err != nil {
			return err
		}
		return nil
	}
	k, err := d.nodeToValue(ctx, key)
	if err != nil {
		return err
	}
	v, err := d.nodeToValue(ctx, value)
	if err != nil {
		return err
	}
	*dst = MapItem{Key: k, Value: v}
	return nil
}

func (d *Decoder) validateDuplicateKey(keyMap map[string]struct{}, key interface{}, keyNode ast.Node) error {
	k, ok := key.(string)
	if !ok {
		return nil
	}
	if !d.allowDuplicateMapKey {
		if _, exists := keyMap[k]; exists {
			return errors.ErrDuplicateKey(fmt.Sprintf(`duplicate key "%s"`, k), keyNode.GetToken())
		}
	}
	keyMap[k] = struct{}{}
	return nil
}

func (d *Decoder) decodeMapSlice(ctx context.Context, dst *MapSlice, src ast.Node) error {
	d.stepIn()
	defer d.stepOut()
	if d.isExceededMaxDepth() {
		return ErrExceededMaxDepth
	}

	mapIter, err := d.mapEntries(ctx, src, isMerge(ctx))
	if err != nil {
		return err
	}
	mapSlice := MapSlice{}
	keyMap := map[string]struct{}{}
	for mapIter.Next() {
		ctx := mapIter.ctx
		key := mapIter.Key()
		value := mapIter.Value()
		if key.IsMergeKey() {
			mergeCtx, err := d.enterMerge(withMerge(ctx), value)
			if err != nil {
				return err
			}
			var m MapSlice
			if err := d.decodeMapSlice(mergeCtx, &m, value); err != nil {
				return err
			}
			for _, v := range m {
				if err := d.validateDuplicateKey(keyMap, v.Key, value); err != nil {
					return err
				}
				mapSlice = append(mapSlice, v)
			}
			continue
		}
		k, err := d.nodeToValue(ctx, key)
		if err != nil {
			return err
		}
		if err := d.validateDuplicateKey(keyMap, k, key); err != nil {
			return err
		}
		v, err := d.nodeToValue(ctx, value)
		if err != nil {
			return err
		}
		mapSlice = append(mapSlice, MapItem{Key: k, Value: v})
	}
	*dst = mapSlice
	return nil
}

func (d *Decoder) decodeMap(ctx context.Context, dst reflect.Value, src ast.Node) error {
	d.stepIn()
	defer d.stepOut()
	if d.isExceededMaxDepth() {
		return ErrExceededMaxDepth
	}

	mapIter, err := d.mapEntries(ctx, src, isMerge(ctx))
	if err != nil {
		return err
	}
	mapType := dst.Type()
	mapValue := reflect.MakeMap(mapType)
	keyType := mapValue.Type().Key()
	valueType := mapValue.Type().Elem()
	keyMap := map[string]struct{}{}
	var found []error
	for mapIter.Next() {
		key := mapIter.Key()
		value := mapIter.Value()
		ctx := entryContext(mapIter.ctx, value)
		if key.IsMergeKey() {
			mergeCtx, err := d.enterMerge(withMerge(ctx), value)
			if err != nil {
				return d.noted(found, err)
			}
			if err := d.decodeMap(mergeCtx, dst, value); err != nil {
				return d.noted(found, err)
			}
			iter := dst.MapRange()
			for iter.Next() {
				if err := d.validateDuplicateKey(keyMap, iter.Key(), value); err != nil {
					return d.noted(found, err)
				}
				mapValue.SetMapIndex(iter.Key(), iter.Value())
			}
			continue
		}

		k := d.createDecodableValue(keyType)
		if d.canDecodeByUnmarshaler(k) {
			if err := d.decodeByUnmarshaler(ctx, k, key); err != nil {
				return d.noted(found, d.located(d.ownError(err, key), Step{Key: keyName(key), IsKey: true}))
			}
		} else {
			keyVal, err := d.below(ctx, Step{Key: keyName(key), IsKey: true}, keyType, reflect.Value{}, key)
			if err != nil {
				return d.noted(found, err)
			}
			k = keyVal
		}

		if k.IsValid() {
			if err := d.validateDuplicateKey(keyMap, k.Interface(), key); err != nil {
				return d.noted(found, err)
			}
		}
		if valueType.Kind() == reflect.Ptr && value.Type() == ast.NullType {
			// set nil value to pointer
			mapValue.SetMapIndex(k, reflect.Zero(valueType))
			continue
		}
		dstValue, err := d.below(ctx, Step{Key: keyName(key)}, valueType, reflect.Value{}, value)
		if err != nil {
			d.note(&found, err)
		}
		if !k.IsValid() {
			// expect nil key
			mapValue.SetMapIndex(d.createDecodableValue(keyType), dstValue)
			continue
		}
		if keyType.Kind() != k.Kind() {
			return d.noted(found, errors.ErrSyntax(
				fmt.Sprintf("cannot convert %q type to %q type", k.Kind(), keyType.Kind()),
				key.GetToken(),
			))
		}
		mapValue.SetMapIndex(k, dstValue)
	}
	dst.Set(mapValue)
	return d.noted(found, nil)
}

func (d *Decoder) fileToReader(file string) (io.Reader, error) {
	reader, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	return reader, nil
}

func (d *Decoder) isYAMLFile(file string) bool {
	ext := filepath.Ext(file)
	if ext == ".yml" {
		return true
	}
	if ext == ".yaml" {
		return true
	}
	return false
}

func (d *Decoder) readersUnderDir(dir string) ([]io.Reader, error) {
	pattern := fmt.Sprintf("%s/*", dir)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	readers := []io.Reader{}
	for _, match := range matches {
		if !d.isYAMLFile(match) {
			continue
		}
		reader, err := d.fileToReader(match)
		if err != nil {
			return nil, err
		}
		readers = append(readers, reader)
	}
	return readers, nil
}

func (d *Decoder) readersUnderDirRecursive(dir string) ([]io.Reader, error) {
	readers := []io.Reader{}
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, _ error) error {
		if !d.isYAMLFile(path) {
			return nil
		}
		reader, readerErr := d.fileToReader(path)
		if readerErr != nil {
			return readerErr
		}
		readers = append(readers, reader)
		return nil
	}); err != nil {
		return nil, err
	}
	return readers, nil
}

func (d *Decoder) resolveReference(ctx context.Context) error {
	for _, opt := range d.opts {
		if err := opt(d); err != nil {
			return err
		}
	}
	for _, file := range d.referenceFiles {
		reader, err := d.fileToReader(file)
		if err != nil {
			return err
		}
		d.referenceReaders = append(d.referenceReaders, reader)
	}
	for _, dir := range d.referenceDirs {
		if !d.isRecursiveDir {
			readers, err := d.readersUnderDir(dir)
			if err != nil {
				return err
			}
			d.referenceReaders = append(d.referenceReaders, readers...)
		} else {
			readers, err := d.readersUnderDirRecursive(dir)
			if err != nil {
				return err
			}
			d.referenceReaders = append(d.referenceReaders, readers...)
		}
	}
	for _, reader := range d.referenceReaders {
		bytes, err := io.ReadAll(reader)
		if err != nil {
			return err
		}

		// assign new anchor definition to anchorMap
		if _, err := d.parse(ctx, bytes); err != nil {
			return err
		}
	}
	d.isResolvedReference = true
	return nil
}

func (d *Decoder) parse(ctx context.Context, bytes []byte) (*ast.File, error) {
	var parseMode parser.Mode
	if d.toCommentMap != nil {
		parseMode = parser.ParseComments
	}
	var opts []parser.Option
	if d.allowDuplicateMapKey {
		opts = append(opts, parser.AllowDuplicateMapKey())
	}
	f, err := parser.ParseBytes(bytes, parseMode, opts...)
	if err != nil {
		return nil, err
	}
	normalizedFile := &ast.File{}
	for _, doc := range f.Docs {
		// try to decode ast.Node to value and map anchor value to anchorMap
		v, err := d.nodeToValue(ctx, doc.Body)
		if err != nil {
			return nil, err
		}
		if v != nil || (doc.Body != nil && doc.Body.Type() == ast.NullType) {
			normalizedFile.Docs = append(normalizedFile.Docs, doc)
			cm := CommentMap{}
			maps.Copy(cm, d.toCommentMap)
			d.commentMaps = append(d.commentMaps, cm)
		}
		for k := range d.toCommentMap {
			delete(d.toCommentMap, k)
		}
	}
	return normalizedFile, nil
}

func (d *Decoder) isInitialized() bool {
	return d.parsedFile != nil
}

func (d *Decoder) decodeInit(ctx context.Context) error {
	if !d.isResolvedReference {
		if err := d.resolveReference(ctx); err != nil {
			return err
		}
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, d.reader); err != nil {
		return err
	}
	file, err := d.parse(ctx, buf.Bytes())
	if err != nil {
		return err
	}
	d.parsedFile = file
	return nil
}

func (d *Decoder) decode(ctx context.Context, v reflect.Value) error {
	d.decodeDepth = 0
	d.anchorValueMap = make(map[string]reflect.Value)
	d.anchorUnknown = nil
	if len(d.parsedFile.Docs) == 0 {
		// empty document.
		dst := v.Elem()
		if dst.IsValid() {
			dst.Set(reflect.Zero(dst.Type()))
		}
	}
	if len(d.parsedFile.Docs) <= d.streamIndex {
		return io.EOF
	}
	body := d.parsedFile.Docs[d.streamIndex].Body
	if body == nil {
		return nil
	}
	if len(d.commentMaps) > d.streamIndex {
		maps.Copy(d.toCommentMap, d.commentMaps[d.streamIndex])
	}
	if err := d.decodeValue(ctx, v.Elem(), body); err != nil {
		return d.finish(err)
	}
	d.streamIndex++
	return nil
}

// Decode reads the next YAML-encoded value from its input
// and stores it in the value pointed to by v.
//
// See the documentation for Unmarshal for details about the
// conversion of YAML into a Go value.
func (d *Decoder) Decode(v interface{}) error {
	return d.DecodeContext(context.Background(), v)
}

// DecodeContext reads the next YAML-encoded value from its input
// and stores it in the value pointed to by v with context.Context.
func (d *Decoder) DecodeContext(ctx context.Context, v interface{}) error {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Type().Kind() != reflect.Ptr {
		return ErrDecodeRequiredPointerType
	}
	d.stopped, d.path = nil, d.path[:0]
	if d.isInitialized() {
		if err := d.decode(ctx, rv); err != nil {
			return err
		}
		return nil
	}
	if err := d.decodeInit(ctx); err != nil {
		return err
	}
	if err := d.decode(ctx, rv); err != nil {
		return err
	}
	return nil
}

// DecodeFromNode decodes node into the value pointed to by v.
func (d *Decoder) DecodeFromNode(node ast.Node, v interface{}) error {
	return d.DecodeFromNodeContext(context.Background(), node, v)
}

// DecodeFromNodeContext decodes node into the value pointed to by v with context.Context.
func (d *Decoder) DecodeFromNodeContext(ctx context.Context, node ast.Node, v interface{}) error {
	rv := reflect.ValueOf(v)
	if rv.Type().Kind() != reflect.Ptr {
		return ErrDecodeRequiredPointerType
	}
	d.stopped, d.path = nil, d.path[:0]
	if !d.isInitialized() {
		if err := d.decodeInit(ctx); err != nil {
			return err
		}
	}
	// resolve references to the anchor on the same file
	if _, err := d.nodeToValue(ctx, node); err != nil {
		return d.finish(err)
	}
	if err := d.decodeValue(ctx, rv.Elem(), node); err != nil {
		return d.finish(err)
	}
	return nil
}
