package edit

import (
	"context"
	"encoding"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/mapkey"
)

// rendered is the text of a value that an edit writes.
type rendered struct {
	// The value as the model reads it, from the encode that gives the
	// text, and the tag of a collection there, as [graph.placeTag] reads
	// it.
	value *value
	tag   string
	// The text in flow style, on one line, which a flow collection holds.
	flow string
	// The header of each block scalar of the block lines, with its string.
	headers []renderedHeader
	// The lines in block style, without a line break at the end, for a
	// collection or a block scalar. A plain or quoted scalar does not have
	// block lines.
	block []string
	// The instances of the constructs that a Set does not write, which
	// the finder finds in the YAML of the value, first those of the YAML of
	// its marshalers and then those of the text of the encode, as
	// [Doc.valueInstances] gives them.
	refused []instance
	// The block scalar that the block lines end with, as [Doc.scalarTail]
	// reads it.
	tail tailScalar
	// The column where the collection of the block lines starts, when head
	// is true.
	indent int
	// Whether the value is a mapping or a sequence.
	collection bool
	// Whether the value is a mapping.
	mapping bool
	// Whether the first of the block lines holds only the properties of a
	// block collection that starts on a later line, as in "!t\na: 1".
	head bool
	// Whether the value is an empty mapping or sequence.
	empty bool
	// Whether the block lines end with a block scalar, and whether it has
	// keep chomping.
	scalar, keeps bool
}

// renderedHeader is the header of a block scalar in the block lines of a
// [rendered] value: its line, its column there, and its size, with the
// string of the scalar.
type renderedHeader struct {
	str             string
	line, col, size int
}

// render returns the text of v, from one encode of v. A block collection
// indents by step, and a block sequence under a key indents its "-" by step
// when seqIndent is true. The flow text and the value of the model come
// from the same encode. Each Go string of the flow text takes the text that
// go-yaml's flow encoder writes for that string alone, as [Doc.flowRun]
// describes. Each other scalar keeps the text of the encode, such as the
// plain timestamp of a [time.Time]. The model holds the value that
// go-yaml's encoder meant, as the node of the encode holds it, and the text
// holds what go-yaml's parse of the encoded text reads. When the two
// differ, as for a float that the encoder writes as 1e+06 and the decoder
// reads as a string, render returns an error that matches errEncoder. So
// does a panic of go-yaml's encoder, and a refusal of go-yaml's parser of
// the encoded text. So does a byte order mark there, as [Doc.valueMisread]
// finds it. So do line breaks of both kinds in the encoded text where the
// YAML of no marshaler of v holds both. The encoder wrote them then, as it
// writes the header of a block scalar of "\r\n" lines with a "\n".
// After those checks, the finder reads the text, and the
// rendered value holds the instances that it finds. A failed check leaves
// the instances that the finder found in the YAML of the marshalers of v,
// because a construct there can make go-yaml fail on the text, as an alias
// whose anchor the YAML lacks does.
//
//nolint:contextcheck // The flow text encodes the strings of the encode, which reads no state that a context carries.
func render(ctx context.Context, v any, step int, seqIndent bool) (rendered, error) {
	data, meant, refused, err := encode(ctx, v, encoder.WithIndent(step), encoder.WithIndentSequence(seqIndent))
	if err != nil {
		return rendered{refused: refused}, err
	}

	// The string of a block scalar holds the line break that ends its
	// text, so the parse reads the encode with that line break.
	text := strings.TrimSuffix(string(data), "\n")

	enc, err := encodedDoc(string(data))
	if err != nil {
		return rendered{refused: refused}, goyamlProblem("go-yaml's parser refuses the text that go-yaml's "+
			"encoder writes for the value: %s", refusal(err, "that text"))
	}

	if f, ok := enc.valueMisread(); ok {
		return rendered{refused: refused}, misreadProblem(f, "the text that go-yaml's encoder writes for the value",
			"that text")
	}

	body := enc.node.AST()

	err = readsBack(meant, body)
	if err != nil {
		return rendered{refused: refused}, err
	}

	found := enc.valueInstances("")
	if lineBreaks(found) && !lineBreaks(refused) {
		return rendered{refused: refused}, fmt.Errorf(
			"%w writes the value with both \"\\n\" and \"\\r\\n\" line breaks, though the YAML of each marshaler "+
				"of the value writes one kind", errEncoder,
		)
	}

	// The flow text writes each Go string as go-yaml's flow encoder writes
	// it, as a move into a flow collection writes a string without quotes
	// of its own. A flow collection cannot hold a block scalar, so the
	// encoder writes a string with a line break in double quotes.
	g := newGraph(body, enc.node.Resolver(), enc.keyReader(), enc.enclosedAliases(), 0)
	r := rendered{
		value: g.root, tag: g.tag, flow: enc.flowRunOf(body, 0, &flowStyle{encoded: true}).text,
		refused: append(refused, found...),
	}

	// The text of a marshaler can write the properties of a block
	// collection alone on its first line, and the encoder keeps the
	// collection at the column of that text.
	content := astnode.Content(body)
	s, spanned := enc.nodeSpan(content)

	switch {
	case spanned && isBlockCollection(content) && enc.lineOf(s.start) > 0:
		r.collection, r.mapping, r.head, r.indent = true, isMapping(content), true, enc.column(s.start)
	case strings.HasPrefix(r.flow, "{"):
		r.collection, r.mapping = true, true
	case strings.HasPrefix(r.flow, "["):
		r.collection = true
	}

	r.empty = r.flow == "{}" || r.flow == "[]"

	if (r.collection || strings.Contains(text, "\n")) && !r.empty {
		r.block = strings.Split(text, "\n")

		if lit := endScalar(body); lit != nil {
			r.scalar = true
			r.keeps = endsKeeping(body)
			r.tail = enc.scalarTail(body, len(r.block)-1)
		}

		r.headers = enc.scalarHeaders()
	}

	return r, nil
}

// lineBreaks reports whether found, the instances of the finder in the
// YAML of a value, holds an instance of [ConstructLineBreaks].
func lineBreaks(found []instance) bool {
	return slices.ContainsFunc(found, func(in instance) bool { return in.construct == ConstructLineBreaks })
}

// scalarHeaders returns the header of each block scalar of the current
// text, with its string.
func (d *Doc) scalarHeaders() []renderedHeader {
	var headers []renderedHeader

	d.inspect(func(n ast.Node) bool {
		lit, ok := n.(*ast.LiteralNode)
		if !ok || lit.Start == nil || lit.Value == nil {
			return true
		}

		if hs, ok := d.tokenSpan(lit.Start); ok {
			line := d.lineOf(hs.start)
			headers = append(headers, renderedHeader{
				str: lit.Value.Value, line: line, col: hs.start - d.lineStart(line), size: hs.end - hs.start,
			})
		}

		return true
	})

	return headers
}

var (
	// The reason of a value that go-yaml's encoder cannot write so that
	// go-yaml reads it back. Such a value fails its Set with [ErrEngine].
	errEncoder = errors.New("go-yaml's encoder")
	// The problem of a value that holds itself, which go-yaml's encoder
	// writes without end.
	errSelfReference = errors.New("it refers to itself")
	// The reason of a value with a scalar whose text go-yaml's decoder
	// reads as a value of another kind, as the float 1e+06 reads as a
	// string. It matches errEncoder.
	errScalarKind = fmt.Errorf("%w writes", errEncoder)
)

// maxMarshalers is the most marshalers that a chain of a Set can hold, in
// which each marshaler returns the value that the next marshals. A longer
// chain fails the Set. The check of the value calls each marshaler of the
// chain on the way, so a chain without end does not end the check either.
// The check compares the value of each marshaler with the values before
// it. Its work thus grows as the square of the length of the chain.
const maxMarshalers = 1000

// maxMarshalersText is maxMarshalers as a message writes it.
const maxMarshalersText = "1,000"

// encode returns the text of v that encoder.Marshal writes under opts, and
// the node that go-yaml's encoder made for v. Before the encoder runs, the
// check of [checkValue] reads v. Its problem comes back as it is, and the
// instances that it finds come back with the result of the encode. A panic
// of a marshaler of v that the check calls goes on as it is. A panic of
// go-yaml's encoder, as on a map key that is nil, returns an error that
// matches errEncoder. Any other error of the encode comes back as it is.
func encode(ctx context.Context, v any, opts ...encoder.Option) ([]byte, ast.Node, []instance, error) {
	refused, err := checkValue(ctx, v)
	if err != nil {
		return nil, nil, nil, err
	}

	var (
		data     []byte
		node     ast.Node
		panicked any
	)

	func() {
		defer func() { panicked = recover() }()

		data, node, err = hook.Marshal(ctx, v, opts)
	}()

	switch {
	case panicked != nil:
		return nil, nil, refused, fmt.Errorf("%w panicked on the value: %v", errEncoder, panicked)
	case err != nil:
		return nil, nil, refused, fmt.Errorf("encode the value: %w", err)
	}

	return data, node, refused, nil
}

// valueCheck is the walk of [checkValue].
type valueCheck struct {
	ctx context.Context
	// The references that hold the value that the walk reads, each as its
	// type, its address, and its length.
	open map[[3]any]bool
	// The values on the way whose marshalers return the value that the walk
	// reads, in order.
	marshaling []reflect.Value
	// The instances of the constructs that a Set does not write, which the
	// finder finds in the YAML of each marshaler, in the order of the walk.
	refused []instance
}

// checkValue returns the problem of v, a value of a Set, before go-yaml's
// encoder writes it. The check reads the values of v that the encoder
// reads. It reads each field of a struct that the encoder writes: an
// exported or an embedded field whose tag does not name "-". At a value
// that a MarshalYAML method of its own marshals, the check calls that
// method, as the encoder calls it, and reads what the method returns
// instead. The encoder calls the method again, so each such method runs
// twice. The encoder alone calls a MarshalText method.
//
// A value that holds itself through a map, a slice, or a pointer returns
// errSelfReference. Go-yaml's encoder writes the value of each reference
// again at each place. Such a value thus has no end, and the encoder
// overflows the stack. Each reference counts as its type, its address,
// and its length, so a slice is the same reference as a slice of the same
// elements. Two places can hold one reference without a cycle. A value
// whose marshaler returns a value that holds it returns errSelfReference
// too. So does a marshaler that returns a value equal to one that a
// marshaler on the way marshals, as [reflect.DeepEqual] compares them. A
// chain of marshalers longer than maxMarshalers, in which each marshaler
// returns the value that the next marshals, returns a problem that names
// the type of the first and the limit.
//
// The YAML that a marshaler writes, as MarshalYAML of
// [yaml.BytesMarshaler] does, must be a value of YAML that go-yaml reads,
// as [marshaledYAML] checks. The check returns the instances that the
// finder finds in that YAML, as marshaledYAML finds them.
func checkValue(ctx context.Context, v any) ([]instance, error) {
	c := &valueCheck{ctx: ctx, open: make(map[[3]any]bool)}

	err := c.walk(reflect.ValueOf(v))

	return c.refused, err
}

// walk returns the problem of v, as [checkValue] describes. Go-yaml's
// encoder writes a nil pointer or interface as null, without a call of its
// marshaler.
func (c *valueCheck) walk(v reflect.Value) error {
	if !v.IsValid() || (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return nil
	}

	if marshals, err := c.marshal(v); marshals {
		return err
	}

	var ref [3]any

	switch v.Kind() {
	case reflect.Interface:
		return c.walk(v.Elem())

	case reflect.Pointer, reflect.Map, reflect.Slice:
		if v.Kind() != reflect.Pointer && (v.IsNil() || v.Len() == 0) {
			return nil
		}

		ref = [3]any{v.Type(), v.Pointer(), 0}
		if v.Kind() == reflect.Slice {
			ref[2] = v.Len()
		}

		if c.open[ref] {
			return errSelfReference
		}

		c.open[ref] = true
		defer delete(c.open, ref)

	case reflect.Array:
	case reflect.Struct:
		for i := range v.NumField() {
			if !encodedField(v.Type().Field(i)) {
				continue
			}

			err := c.walk(v.Field(i))
			if err != nil {
				return err
			}
		}

		return nil

	default:
		return nil
	}

	switch v.Kind() {
	case reflect.Pointer:
		return c.walk(v.Elem())

	case reflect.Map:
		for it := v.MapRange(); it.Next(); {
			err := c.key(it.Key())
			if err == nil {
				err = c.walk(it.Key())
			}

			if err == nil {
				err = c.walk(it.Value())
			}

			if err != nil {
				return err
			}
		}

		return nil

	default:
		for i := range v.Len() {
			err := c.walk(v.Index(i))
			if err != nil {
				return err
			}
		}

		return nil
	}
}

// key returns the problem of k, a key of a map of the value, when
// go-yaml's encoder does not write k as a key of YAML. It then writes the
// string that fmt prints for k, as for an array or a struct, so the text
// does not read back as k. The check asks the encoder for the node of k
// alone, as the encoder asks for it before it writes the string, and the
// problem matches errScalarKind. The encoder writes that string when the
// encode of k fails too, as with an error of the MarshalYAML method of k.
// The problem is then that error, as the error of the marshaler of a value
// is the problem of its encode. A panic of the encode of k returns no
// problem, because the encode of the value then panics.
func (c *valueCheck) key(k reflect.Value) (err error) {
	if !k.CanInterface() {
		return nil
	}

	defer func() {
		if recover() != nil {
			err = nil
		}
	}()

	key := k.Interface()

	node, err := yaml.NewEncoder(io.Discard).EncodeToNodeContext(c.ctx, key)
	if err != nil {
		return fmt.Errorf("encode a key of type %s: %w", reflect.TypeOf(key), err)
	}

	if _, ok := node.(ast.MapKeyNode); ok {
		return nil
	}

	return fmt.Errorf("%w a key of type %s as the string %s",
		errScalarKind, reflect.TypeOf(key), shownCut(fmt.Sprint(key)))
}

// marshal calls the MarshalYAML method of v that go-yaml's encoder calls
// to marshal it, and returns the problem of what the method returns. It
// reports whether v has a method that the encoder calls to marshal it. A
// method that fails returns no problem, because the encode then fails with
// its error. The encoder writes the text of MarshalText as a string, so the
// check does not call that method.
func (c *valueCheck) marshal(v reflect.Value) (bool, error) {
	if !v.CanInterface() {
		return false, nil
	}

	switch m := v.Interface().(type) {
	case yaml.BytesMarshalerContext:
		doc, err := m.MarshalYAML(c.ctx)

		return true, c.marshaledYAML(v, doc, err)

	case yaml.BytesMarshaler:
		doc, err := m.MarshalYAML()

		return true, c.marshaledYAML(v, doc, err)

	case yaml.InterfaceMarshalerContext:
		return true, c.marshaled(v, func() (any, error) { return m.MarshalYAML(c.ctx) })

	case yaml.InterfaceMarshaler:
		return true, c.marshaled(v, m.MarshalYAML)

	case encoding.TextMarshaler:
		return true, nil

	default:
		return false, nil
	}
}

// marshaledYAML returns the problem of doc, the YAML that the marshaler of
// v writes, as [marshaledYAML] gives it, with the error of the marshaler.
// The walk keeps the instances that the finder finds in doc.
func (c *valueCheck) marshaledYAML(v reflect.Value, doc []byte, err error) error {
	refused, err := marshaledYAML(v, doc, err)
	c.refused = append(c.refused, refused...)

	return err
}

// marshaled returns the problem of the value that marshal, the marshaler
// of v, returns, as [checkValue] describes.
func (c *valueCheck) marshaled(v reflect.Value, marshal func() (any, error)) error {
	if len(c.marshaling) >= maxMarshalers {
		return fmt.Errorf("the MarshalYAML methods of %s return values with marshalers more than %s levels deep",
			c.marshaling[0].Type(), maxMarshalersText)
	}

	for _, m := range c.marshaling {
		if m.Type() == v.Type() && reflect.DeepEqual(m.Interface(), v.Interface()) {
			return errSelfReference
		}
	}

	out, err := marshal()
	if err != nil {
		return nil //nolint:nilerr // The encode fails with the error of the marshaler.
	}

	c.marshaling = append(c.marshaling, v)
	defer func() { c.marshaling = c.marshaling[:len(c.marshaling)-1] }()

	return c.walk(reflect.ValueOf(out))
}

// encodedField reports whether go-yaml's encoder writes f, a field of a
// struct: an exported or an embedded field whose tag does not name "-".
// The tag is the "yaml" tag, or the "json" tag of a field without one, as
// go-yaml reads them.
func encodedField(f reflect.StructField) bool {
	tag := f.Tag.Get("yaml")
	if tag == "" {
		tag = f.Tag.Get("json")
	}

	return (f.IsExported() || f.Anonymous) && tag != "-"
}

// readsAs reports whether go-yaml's decoder reads node into a string as
// text, as a load of a type field reads the field.
func readsAs(node ast.Node, text string) bool {
	s, ok, err := mapkey.StringOf(node)

	return err == nil && ok && s == text
}

// readsBack returns an error that matches errEncoder when go-yaml's
// decoder refuses body, go-yaml's parse of the text of an encode, or when
// body reads a value of another kind than meant holds at its place. Meant
// is the node that go-yaml's encoder made. The decoder refuses an alias
// inside its own anchor, as in "&a !!str *a", whose value has no end.
// The error names the first scalar of meant that the parse reads so, by
// the type of its node and its text. A float that the encoder writes as
// 1e+06 is such a scalar, because the decoder reads that text as a string.
// For a scalar of the YAML of a marshaler, a block scalar among them, the
// error is a [*marshalerError]. It names the scalar at its place in that
// YAML and the text that the encoder writes for it, with its tags.
// The scalars of meant do not hold the values that a decode reads, because
// go-yaml's encoder keeps the quotes of a string in its value and gives the
// float 1e+06 the value 0. The decoder still reads each of them as a value
// of the kind that the encoder meant, so the check compares the kinds of
// the two decodes.
func readsBack(meant, body ast.Node) error {
	var got any

	err := astnode.NodeToValue(body, &got)
	if err != nil {
		return fmt.Errorf("%w writes text that go-yaml's decoder refuses: %s", errEncoder, refusal(err, "that text"))
	}

	m, b := misread(meant, body)
	if m == nil {
		return nil
	}

	err = astnode.NodeToValue(b, &got)
	if err != nil {
		return fmt.Errorf("%w writes text that go-yaml's decoder refuses: %s", errEncoder, refusal(err, "that text"))
	}

	if keyMisread(m, b) {
		form := "a key of another form"

		switch _, explicit := b.(*ast.MappingKeyNode); {
		case astnode.IsMergeKey(b):
			form = "a merge key"
		case explicit:
			form = "an explicit key"
		}

		return fmt.Errorf("%w writes the key %s as text that go-yaml's decoder reads as %s",
			errEncoder, cut(shown(astnode.Token(m).Value)), form)
	}

	tk := m.GetToken()
	lit, isLiteral := m.(*ast.LiteralNode)

	var what string

	switch {
	case isLiteral:
		what = "the block scalar of " + cut(shown(lit.Value.Value))
	case tk != nil && astnode.IsScalar(m):
		what = fmt.Sprintf("the %s %s", strings.ToLower(m.Type().String()), tk.Value)
	default:
		return fmt.Errorf("%w writes a %s as text that go-yaml's decoder reads as %s",
			errEncoder, strings.ToLower(m.Type().String()), cut(shown(got)))
	}

	// Go-yaml's encoder makes each token of a Go value alone, and its parser
	// links the tokens of the YAML of a marshaler. The message then names the
	// scalar at its place in that YAML, and the text that the encoder writes
	// for it.
	if tk != nil && (tk.Prev != nil || tk.Next != nil) {
		written := cut(strconv.Quote(withProperties(body, b)))
		at := "of"

		if tk.Position != nil {
			at = fmt.Sprintf("at %d:%d of", tk.Position.Line, tk.Position.Column)
		}

		fix := fmt.Sprintf("write the scalar as %s in the YAML of the marshaler, so that go-yaml reads it as "+
			"it reads the text that the encoder writes", written)

		// The encoder writes a double-quoted string as go-yaml reads it, so
		// a block scalar can take that form and keep its string.
		if isLiteral {
			fix = fmt.Sprintf("write the string as %s in the YAML of the marshaler, a double-quoted string that "+
				"go-yaml's encoder writes as go-yaml reads it", cut(strconv.Quote(lit.Value.Value)))
		}

		return &marshalerError{
			msg: fmt.Sprintf("go-yaml reads %s %s the YAML that a marshaler of the value returns, but go-yaml's "+
				"encoder writes that scalar as %s, which go-yaml's decoder reads as %s", what, at, written,
				cut(shown(got))),
			fix: fix,
		}
	}

	return fmt.Errorf("%w %s as text that go-yaml's decoder reads as %s", errScalarKind, what, cut(shown(got)))
}

// marshalerError is the problem of a scalar of the YAML of a marshaler
// that go-yaml reads as another value from the text that go-yaml's encoder
// writes for it, as [readsBack] finds it. It matches errScalarKind. The fix
// says what the author of the marshaler can write.
type marshalerError struct {
	msg, fix string
}

// Error returns the message of e.
func (e *marshalerError) Error() string {
	return e.msg
}

// Unwrap returns errScalarKind.
func (e *marshalerError) Unwrap() error {
	return errScalarKind
}

// withProperties returns the text of node, a node of body, with the tags
// and the anchors that body writes on it, as in "!t 1".
func withProperties(body, node ast.Node) string {
	holders := make(map[ast.Node]ast.Node)

	astnode.Inspect(body, func(n ast.Node) bool {
		switch p := n.(type) {
		case *ast.TagNode:
			holders[p.Value] = p
		case *ast.AnchorNode:
			holders[p.Value] = p
		}

		return true
	})

	for holder, ok := holders[node]; ok; holder, ok = holders[node] {
		node = holder
	}

	return node.String()
}

// misread returns the first node of meant whose decode has another type
// than the decode of its place in body, with that node of body. Meant is
// a node that go-yaml's encoder made, and body is the parse of its text. A
// collection whose node of body is not a collection of its kind and size
// differs whole. An alias differs when body does not hold an alias of the
// same name there. A tag differs when body does not hold the same tag
// there. A block scalar differs when its place decodes to another string.
// Another node that does not decode differs too. Misread returns nil nodes
// when each place decodes to a value of the type of the decode of meant.
func misread(meant, body ast.Node) (ast.Node, ast.Node) {
	var pairs [][2]ast.Node

	switch m := meant.(type) {
	case *ast.MappingNode:
		b, ok := body.(*ast.MappingNode)
		if !ok || len(m.Values) != len(b.Values) {
			return meant, body
		}

		for i, e := range m.Values {
			if keyMisread(e.Key, b.Values[i].Key) {
				return e.Key, b.Values[i].Key
			}

			pairs = append(pairs, [2]ast.Node{e.Key, b.Values[i].Key}, [2]ast.Node{e.Value, b.Values[i].Value})
		}

	case *ast.SequenceNode:
		b, ok := body.(*ast.SequenceNode)
		if !ok || len(m.Values) != len(b.Values) {
			return meant, body
		}

		for i, v := range m.Values {
			pairs = append(pairs, [2]ast.Node{v, b.Values[i]})
		}

	case *ast.AnchorNode:
		b, ok := body.(*ast.AnchorNode)
		if !ok {
			return meant, body
		}

		pairs = [][2]ast.Node{{m.Value, b.Value}}

	case *ast.TagNode:
		// One tag reads the values below it the same way, so they compare as
		// values without the tag do. A decode of the tagged node alone does
		// not find the anchors of its aliases outside it.
		b, ok := body.(*ast.TagNode)
		if !ok || b.Start.Value != m.Start.Value {
			return meant, body
		}

		pairs = [][2]ast.Node{{m.Value, b.Value}}

	case *ast.MappingValueNode:
		b, ok := body.(*ast.MappingValueNode)
		if !ok {
			return meant, body
		}

		if keyMisread(m.Key, b.Key) {
			return m.Key, b.Key
		}

		pairs = [][2]ast.Node{{m.Key, b.Key}, {m.Value, b.Value}}

	case *ast.AliasNode:
		// An alias reads the value of its anchor, which the walk compares at
		// the anchor. A decode of the alias alone does not find the anchor.
		if b, ok := body.(*ast.AliasNode); !ok || b.Value.GetToken().Value != m.Value.GetToken().Value {
			return meant, body
		}

	case *ast.LiteralNode:
		// A block scalar of the encoder holds its string, so the decodes
		// compare by value. Go-yaml's encoder drops the blank lines at the
		// end of the text of a marshaler, which a block scalar with keep
		// chomping holds.
		var want, got any

		if astnode.NodeToValue(meant, &want) != nil || astnode.NodeToValue(body, &got) != nil || want != got {
			return meant, body
		}

	default:
		var want, got any

		if astnode.NodeToValue(meant, &want) != nil || astnode.NodeToValue(body, &got) != nil ||
			reflect.TypeOf(want) != reflect.TypeOf(got) {
			return meant, body
		}
	}

	for _, p := range pairs {
		if astnode.IsNil(p[0]) || astnode.IsNil(p[1]) {
			if astnode.IsNil(p[0]) != astnode.IsNil(p[1]) {
				return meant, body
			}

			continue
		}

		if m, b := misread(p[0], p[1]); m != nil {
			return m, b
		}
	}

	return nil, nil
}

// keyMisread reports whether body, a key of go-yaml's parse of the text of
// an encode, takes another form than meant, the key that go-yaml's encoder
// made there. Go-yaml can read the text of a string key as a << merge key
// or as an explicit key. The kinds of the two decodes compare as [misread]
// compares them. The keys of the encoder do not hold the strings that a
// decode reads, as [readsBack] describes, so their strings do not compare.
func keyMisread(meant, body ast.Node) bool {
	if astnode.IsMergeKey(meant) != astnode.IsMergeKey(body) {
		return true
	}

	_, explicit := meant.(*ast.MappingKeyNode)
	_, ok := body.(*ast.MappingKeyNode)

	return ok != explicit
}

// cut returns s cut to the bytes that a message shows, as [shownValue]
// cuts a value.
func cut(s string) string {
	if len(s) <= maxShown {
		return s
	}

	return strings.ToValidUTF8(s[:maxShown], "") + "..."
}

// encodedDoc returns a [Doc] that holds text, the encode of a value or the
// YAML of a marshaler, so the writers of the edits read it as they read
// the current text. The Doc reads text as the user's text too, so the
// words of its problems name the places of text. The parse takes a
// key that the text repeats. The parse of the document then refuses such
// a key, unless its Source allows it. The document of the Doc is the
// first document of text with content, as go-yaml's encoder reads the
// YAML of a marshaler.
func encodedDoc(text string) (*Doc, error) {
	src := niceyaml.NewSourceFromString(text, niceyaml.WithByteLimit(0), niceyaml.WithDepthLimit(0),
		niceyaml.WithAliasLimit(false), niceyaml.WithAllowDuplicateKeys(true))

	var node *niceyaml.Node

	for _, n := range src.AllDocuments() {
		err := n.Err()
		if err != nil {
			return nil, err //nolint:wrapcheck // The caller names the text.
		}

		if node == nil || node.IsEmpty() {
			node = n
		}

		if !n.IsEmpty() {
			break
		}
	}

	if node == nil {
		return nil, errNoDocument
	}

	lines := lineStarts(text)
	origin := make([]int, len(text))

	for i := range origin {
		origin[i] = i
	}

	return &Doc{
		src: src, user: node, node: node, cur: text, curLines: lines, orig: text, origLines: lines, origin: origin,
	}, nil
}

// scalarStyle returns the text of s, a Go string that replaces a scalar of
// the style of old, in that style when the text allows it. A single-quoted
// scalar has no escapes, so a string with a line break or a character that
// YAML does not allow in a text takes double quotes. It reports false when
// the style of old does not apply.
func scalarStyle(s string, old *token.Token) (string, bool) {
	if old == nil {
		return "", false
	}

	switch old.Type {
	case token.DoubleQuoteType:
		return doubleQuoted(s), true

	case token.SingleQuoteType:
		if strings.ContainsAny(s, "\r\n") || strings.ContainsFunc(s, func(r rune) bool { return !printable(r) }) {
			return doubleQuoted(s), true
		}

		return "'" + strings.ReplaceAll(s, "'", "''") + "'", true

	default:
		return "", false
	}
}

// doubleQuoted returns s as a double-quoted YAML scalar.
func doubleQuoted(s string) string {
	var b strings.Builder

	b.WriteByte('"')

	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			// A \u escape takes four hex digits, so a rune past U+FFFF
			// takes the \U escape of eight.
			if !unicode.IsPrint(r) && r != utf8.RuneError {
				if r > 0xffff {
					fmt.Fprintf(&b, `\U%08x`, r)
				} else {
					fmt.Fprintf(&b, `\u%04x`, r)
				}

				continue
			}

			b.WriteRune(r)
		}
	}

	b.WriteByte('"')

	return b.String()
}

// renderKey returns the text of key as a key of a mapping. The argument
// old is the token of the key that key replaces, or nil for a new key. A
// key that does not keep the style of old takes the text that go-yaml's
// encoder writes for it, in flow style inside a flow collection, as
// [encodedString] gives it.
func renderKey(ctx context.Context, key string, old *token.Token, flow bool) string {
	if s, ok := scalarStyle(key, old); ok {
		return s
	}

	return encodedString(ctx, key, true, flow)
}

// encodedString returns s as go-yaml's encoder writes a string, as a key of
// a mapping when key is true, and in flow style when flow is true. A string
// that is not a key is an item of a sequence. The encoder quotes a string
// that reads back as another value there, such as "x:y" in a flow
// collection. A string has no marshaler, so the encode does not fail. The
// text of a string that it cannot write is the string in double quotes.
func encodedString(ctx context.Context, s string, key, flow bool) string {
	var opts []encoder.Option

	if flow {
		opts = append(opts, encoder.WithYAMLOptions(yaml.Flow(true)))
	}

	var v any = []any{s}

	if key {
		v = map[string]any{s: nil}
	}

	data, err := encoder.Marshal(ctx, v, opts...)
	if err != nil {
		return doubleQuoted(s)
	}

	text := strings.TrimSuffix(string(data), "\n")

	if !key && flow {
		return strings.TrimSuffix(strings.TrimPrefix(text, "["), "]")
	}

	if !key {
		return strings.TrimPrefix(text, "- ")
	}

	text = strings.TrimSuffix(strings.TrimPrefix(text, "{"), "}")

	// The key itself might hold ": null", so the cut takes the suffix.
	head, ok := strings.CutSuffix(text, ": null")
	if !ok {
		return doubleQuoted(s)
	}

	return head
}

// plainText reports whether a load of a type field reads text, written as
// a plain scalar, as text: go-yaml's decoder reads the scalar alone as the
// string text, and the scalar holds text. A decode into any must read the
// plain text as YAML does. So where it reads a string, go-yaml's encoder
// must write the string plain too, which it does not for 1e3, a float of
// YAML. A type field thus keeps the plain style of the text that it
// replaces where a load reads it back.
func plainText(ctx context.Context, text string) bool {
	file, err := parser.ParseBytes([]byte(text), 0)
	if err != nil || len(file.Docs) != 1 || file.Docs[0] == nil || file.Docs[0].Body == nil {
		return false
	}

	body := file.Docs[0].Body
	own, _ := astnode.ScalarText(body)

	var read any

	_ = astnode.NodeToValue(body, &read) //nolint:errcheck // A text that does not decode fails readsAs.
	if s, ok := read.(string); ok && encodedString(ctx, s, false, false) != text {
		return false
	}

	return readsAs(body, text) && own == text
}

// fieldText returns the text of the value of a type field that holds the
// string text in place of old, or of a new one for a nil old, inside a
// flow collection when flow is true. The field keeps the quotes of old.
// Outside a flow collection, it takes the plain text where [plainText]
// reports true. Every other field takes the text that go-yaml's flow
// encoder writes for the string. The model holds the string itself, as
// [fieldScalar] gives it, so the check of the write compares the text that
// fieldText chose with the string.
func fieldText(ctx context.Context, text string, old *token.Token, flow bool) (string, error) {
	if s, quoted := scalarStyle(text, old); quoted {
		return s, nil
	}

	if !flow && plainText(ctx, text) {
		return text, nil
	}

	r, err := render(ctx, text, 2, false)
	if err != nil {
		return "", err
	}

	return r.flow, nil
}
