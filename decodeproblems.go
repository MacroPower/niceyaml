package niceyaml

import (
	"cmp"
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/decodewalk"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/position"
)

// decodeProblems returns err, the rejection a decode of node into v
// with cfg returned, with the other problems of that decode beside it,
// for the Node to bind.
//
// The go-yaml decoder decides whether a decode fails, and it returns one
// rejection for a document that holds several problems. That is the
// first value it rejects, in the order the target declares its fields,
// or an unknown field when it rejects no value. DecodeProblems runs
// only after the decoder rejected, and its result always holds that
// rejection as [Node.decodeRejection] returns it, so no decode passes or
// fails because of it. A [problemCollector] finds the other problems.
//
// With no other problem, the result is the rejection as
// [Node.decodeRejection] returns it. With more, it is a summary from
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
func (n *Node) decodeProblems(
	ctx context.Context,
	err error,
	node ast.Node,
	v any,
	cfg decodeConfig,
) error {
	if err == nil {
		return nil
	}

	rejection := n.decodeRejection(err)

	at, ok := n.rejectedAt(err, rejection)
	if !ok || ctx.Err() != nil {
		return rejection
	}

	c := newProblemCollector(ctx, n, cfg)
	c.collect(reflect.TypeOf(v).Elem(), node)

	delete(c.found, at)

	if len(c.found) == 0 || ctx.Err() != nil {
		return rejection
	}

	unknown, ok := err.(*yaml.UnknownFieldError) //nolint:errorlint // A wrapped error is the unmarshaler's own.

	problems := make([]decodeProblem, 0, len(c.found)+1)
	problems = append(problems, decodeProblem{
		err:          rejection,
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

	return NewSummary(msg, errs...)
}

// rejectedAt returns the position that marks err, a rejection of the
// decoder, among the problems a [problemCollector] finds, where
// rejection is err as [Node.decodeRejection] returns it. The decoder
// names a value it rejects by a token of the source, and the collector
// marks a problem it decodes by the same token. Any other rejection lies
// where the Node binds it, which is where the collector marks a problem
// that takes a path.
//
// The binding of a rejection in the document [NewSourceFromLayers] builds resolves in
// the file of a layer, and the collector reads the merged text. Such a
// rejection lies where [Node.mergedPosition] finds it in that text.
//
// The bool result is false for a rejection that binds at no position,
// such as one the decoder reports with no token for a value that
// [Node.locateDecodeError] did not find. It is false too for a binding
// that n did not make, such as one an unmarshaler returns for a source
// of its own.
func (n *Node) rejectedAt(err, rejection error) (position.Position, bool) {
	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if ok && n.holdsToken(yamlErr.GetToken()) {
		return position.NewFromToken(yamlErr.GetToken()), true
	}

	if n.merges() {
		return n.mergedPosition(rejection)
	}

	srcErr, ok := n.bindOwn(n.decodeRejection(err)).(*SourceError) //nolint:errorlint // A binding below a wrapper is not one n made.
	if !ok || srcErr.node != n {
		return position.Position{}, false
	}

	return srcErr.Position()
}

// mergedPosition returns the position of rejection in the text of the
// document of n, which [NewSourceFromLayers] built, where rejection is a rejection as
// [Node.decodeRejection] returns it. That is the position the binding of
// the rejection reports in a document no merge built. A position stands
// as it is, and so does the start of a range. A path that starts at `@`
// reads from the scope of n. Every path resolves as
// [Node.resolveLocation] resolves it.
//
// The bool result is false for a rejection with no location. It is false
// for one that takes the location of a binding, as the error an
// unmarshaler returns for a source of its own does. It is false too for
// a location that resolves nowhere in the text.
func (n *Node) mergedPosition(rejection error) (position.Position, bool) {
	found := anchorOf(placeable(rejection))
	if _, own := found.err.(*Error); !own { //nolint:errorlint // The anchor itself, found by the walk.
		return position.Position{}, false
	}

	var loc location

	switch at := found.loc.(type) {
	case position.Range:
		loc.pos = at.Start

	case position.Position:
		loc.pos = at

	default:
		if found.ambiguous {
			return position.Position{}, false
		}

		var err error

		loc, err = n.resolveLocation(n.base.Join(found.path))
		if err != nil {
			return position.Position{}, false
		}
	}

	if checkInRange(loc, n.source.lines) != nil {
		return position.Position{}, false
	}

	return loc.pos, true
}

// decodeRejection returns err, an error from the decoder, for the Node to
// bind. A [yaml.Error] at a token of the source comes back as
// [Node.tokenRejection] returns it, at the path of the node the decoder
// names by the token. That path starts at `$`, so the Node binds it with
// no scope in front. Any other error comes back as [asDecodeError]
// returns it, with a message that names each anchor as the document
// does, and keeps the location it carries, such as one a value's own
// UnmarshalYAML returns, or the path [Node.locateDecodeError] put it
// under.
//
// Only a [yaml.Error] the decoder returns itself converts. One that an
// UnmarshalYAML wraps stays the error of that unmarshaler, with the text
// and sentinels of its wrapper. One from a parse an UnmarshalYAML runs on
// its bytes has a token of that parse, so it stays the error of the
// value too. The decoder builds the mapping an inline field decodes
// from, with no token for the mapping or its keys, so a rejection of a
// key of an inline map, or of an inline field whose type takes no
// mapping, has no token of the source either.
func (n *Node) decodeRejection(err error) error {
	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if !ok || !n.holdsToken(yamlErr.GetToken()) {
		return asDecodeError(n.doc.restoreError(err))
	}

	return n.tokenRejection(yamlErr)
}

// tokenRejection returns err, a rejection the decoder reported at a token
// of the source, as an [*Error] that matches [ErrDecode]. The Error reads
// as [document.rejectionText] writes err, with each anchor named as the
// document names it, matches the label rejectionText gives err, and
// carries the location [Node.rejectionLocation] gives the token. The path
// in that location starts at `$`.
func (n *Node) tokenRejection(err yaml.Error) error {
	msg, label := n.doc.rejectionText(err)
	rejected := decodeError{err: yamlMessageError{err: err, msg: msg}, label: label}

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
// decode read, as [decodewalk.Walk] pairs them. Where the walk reaches a
// leaf, it decodes the node of the leaf alone into a new value of the
// type of the leaf. A leaf is a value the walk reads nothing below. That
// is a scalar, or a node of a kind its type does not decode from, such
// as a sequence where a struct takes a mapping. The walk never decodes a
// mapping of the document into the struct or the map that reads it, or a
// sequence into its slice. A value thus costs the same however deep it
// lies, and the pass decodes no value below such a mapping or sequence a
// second time.
//
// A leaf adds a problem only for these rejections. The decoder builds
// the first three itself, with no code of the caller:
//
//   - A [yaml.TypeError], a [yaml.OverflowError], or a
//     [yaml.UnexpectedNodeTypeError] at a token of the source. Those are
//     a value of the wrong kind and a number out of range.
//   - The error of [time.ParseDuration] for a scalar in a
//     [time.Duration], which the decoder returns with no token.
//   - The refusal of a mapping or a sequence in a type that takes a
//     scalar alone, as [unmarshaler.Options.RefusesForText] reports,
//     which the decoder returns with no token too. The problem reads as
//     [textRejection] writes it.
//   - The error of an UnmarshalText method for a scalar in a type that
//     [unmarshaler.Options.FromText] reports, which the decoder returns
//     with no token as well.
//
// The walk reads nothing at or below any other value that decodes
// itself, as [unmarshaler.Options.ReportsOwnError] lists those types, a
// type an option of the decode gives an unmarshaler among them. Only the
// unmarshaler of such a value says what it accepts, and a second call
// can answer otherwise than the first, as one that counts the names it
// has seen does. The decoder parses a [time.Duration] and a [time.Time]
// itself, so the walk reads both. It hands an UnmarshalText method the
// text of a scalar, without the context or the node of the decode. The
// walk therefore takes that method to answer the same for the same text,
// and reads the scalar. The decoder calls no method for a mapping or a
// sequence it refuses for such a type, so the walk reads that node as a
// leaf too. The walk reads nothing below an interface or a
// [yaml.MapSlice] either, which take any value.
//
// The walk cannot see every rule the decode applies, such as the types
// [yaml.RegisterCustomUnmarshaler] gave a function, or the value the
// decoder holds for an anchor. So the value that reads a leaf confirms
// each problem, as [problemCollector.confirmed] describes. The collector
// keeps a problem only when the decoder rejects the leaf there as it
// rejected the leaf alone. A struct that a registered function decodes
// thus judges its own fields. It judges nothing below them, so the
// collector can add a problem there that the function accepts.
//
// The pass decodes one leaf at a time, so the one unmarshaler it calls
// for a value the walk reads is that UnmarshalText method. It calls the
// method once for each field, element, or map value that reads the
// scalar, and once more to confirm a scalar the method rejects. A decode
// of the struct that reads a leaf can call code the walk does not pair
// with the leaf. That is a registered function and the unmarshaler of
// another value that reads the entry of the leaf, as a field of the same
// name in an inline struct does. What those return adds no problem.
//
// When the decoder rejects unknown fields, [decodewalk.UnknownFields]
// adds the rejection of each one the document holds.
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
	ctx     context.Context
	node    *Node
	decoder *yaml.Decoder
	// The problems found, by the position that marks each.
	found map[position.Position]decodeProblem
	// What the walk knows of the decode, which decodes with decoder.
	rules decodewalk.Rules
	// The settings of the decode.
	cfg decodeConfig
}

// newProblemCollector creates a new [*problemCollector] for a decode of
// n with cfg.
func newProblemCollector(ctx context.Context, n *Node, cfg decodeConfig) *problemCollector {
	dec := n.newDecoder(cfg)

	return &problemCollector{
		ctx:     ctx,
		node:    n,
		decoder: dec,
		found:   map[position.Position]decodeProblem{},
		rules:   n.walkRules(ctx, dec, cfg.unmarshalers),
		cfg:     cfg,
	}
}

// walkRules returns the [decodewalk.Rules] of a decode of n whose options
// name the unmarshalers u, for a walk that asks dec about the values it
// reaches. The walk reads each node as the [decodetree.Tree] of the
// document reads it.
func (n *Node) walkRules(ctx context.Context, dec *yaml.Decoder, u unmarshaler.Options) decodewalk.Rules {
	return decodewalk.Rules{
		Resolver: n.doc.pathResolver(),
		View:     n.doc.tree().View,
		Decode: func(node ast.Node, v any) error {
			return decodeWithRecover(ctx, dec, node, v)
		},
		Unmarshalers: u,
	}
}

// collect finds the problems of a decode of node into a value of type
// t: the leaves the decoder rejects, and the unknown fields, when the
// decoder rejects those.
func (c *problemCollector) collect(t reflect.Type, node ast.Node) {
	c.register(node)

	from := datapath.ParentAt(c.rules.Resolver, c.node.base)
	decodewalk.Walk(c.ctx, c.rules, t, node, from, c.visit)

	if !c.cfg.disallowUnknownFields {
		return
	}

	for _, field := range decodewalk.UnknownFields(c.ctx, c.rules, t, node) {
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
// that node refers to, as [Node.prime] reads them. Every later
// decode of the pass then reads an alias as the decode did.
//
// The decoder stops that read at a node it cannot read as a plain value,
// such as a tag that does not convert its value, and the decode returned
// that rejection. The anchors from that node on stay unknown to the
// decoder of the pass, as they did to the decoder of the decode. A value
// that reads one of them through an alias adds no problem, since its
// decode fails for the alias.
func (c *problemCollector) register(node ast.Node) {
	view, err := c.node.prime(c.ctx, c.decoder, node)
	if err != nil {
		return
	}

	var sink any

	_ = c.rules.Decode(view, &sink) //nolint:errcheck // The decode returned this rejection.
}

// add keeps problem, unless the collector holds one at the same position
// already.
func (c *problemCollector) add(problem decodeProblem) {
	if _, held := c.found[problem.at]; !held {
		c.found[problem.at] = problem
	}
}

// visit reads v, a value the walk reaches, and tells the walk whether to
// go below it. A leaf adds its problem, as [problemCollector.leaf]
// describes. The walk goes below no key, no member whose key has no
// name, since no path spells its place, and no value that decodes
// itself. Of those, a duration, a time, and a scalar in a type that
// [unmarshaler.Options.FromText] reports are leaves. So is a mapping or a
// sequence the decoder refuses for such a type, as
// [unmarshaler.Options.RefusesForText] reports. An inline field that
// decodes itself reads the mapping of its struct and is no leaf.
func (c *problemCollector) visit(v *decodewalk.Value) decodewalk.Action {
	u := c.cfg.unmarshalers

	switch {
	case v.Kind == decodewalk.Key, !v.Named, v.Kind == decodewalk.Inline && u.ReportsOwnError(v.Type):
		return decodewalk.Skip

	case v.Type == unmarshaler.DurationType || v.Type == unmarshaler.TimeType:
		c.leaf(v)

		return decodewalk.Skip

	case u.ReportsOwnError(v.Type):
		content := datapath.Deref(c.rules.Resolver, v.Node)

		// The decoder calls an UnmarshalText method for a scalar alone. It
		// refuses any other node itself, unless another unmarshaler of the
		// type takes that node.
		if u.RefusesForText(v.Type, content) || astnode.IsScalar(content) && u.FromText(v.Type) {
			c.leaf(v)
		}

		return decodewalk.Skip

	case v.Reads == decodewalk.Whole:
		c.leaf(v)

		return decodewalk.Skip

	case v.Reads == decodewalk.Nothing:
		return decodewalk.Skip

	default:
		return decodewalk.Descend
	}
}

// leaf decodes the node of v into a new value of its type and adds the
// rejection the decoder builds for it as a problem. The walk reads
// nothing below v.
//
// A rejection at a token of the source takes the location of that token,
// as [Node.tokenRejection] gives it. The error of a parse has no token.
// That is the error of [time.ParseDuration] for a scalar in a duration,
// or of the UnmarshalText method of a type that
// [unmarshaler.Options.FromText] reports. It takes the path of v, as
// [Node.locateDecodeError] puts one under the path of its value. It adds
// a problem only when that path selects the node the document holds for
// v, which the path of an entry that a later one hides does not. The
// path selects that node when it points at the token
// [astnode.PathToken] gives it. The refusal of a mapping or a sequence
// that [unmarshaler.Options.RefusesForText] reports has no token either.
// It takes that path the same way, and reads as [textRejection] writes
// it.
//
// Any other error adds nothing. That is another rejection of the
// decoder, such as one for a tag that does not convert its value, the
// error of a type that only a [yaml.RegisterCustomUnmarshaler] function
// decodes, or a panic that [decodeWithRecover] placed. It is also an
// error of an UnmarshalText method that names a place of its own, as
// [Node.lacksLocation] reports, since the position that marks a problem
// is the one its path resolves to.
func (c *problemCollector) leaf(v *decodewalk.Value) {
	err := c.rules.Decode(c.rules.View(v.Node), reflect.New(v.Type).Interface())
	if err == nil {
		return
	}

	if rejected, ok := builtRejection(err); ok {
		tk := rejected.GetToken()
		if c.node.holdsToken(tk) && c.confirmed(err, v) {
			c.add(decodeProblem{err: c.node.tokenRejection(rejected), at: position.NewFromToken(tk)})
		}

		return
	}

	_, isYAML := err.(yaml.Error) //nolint:errorlint // The decoder returns it unwrapped.
	if isYAML || !c.node.lacksLocation(err) {
		return
	}

	content := datapath.Deref(c.rules.Resolver, v.Node)

	var cause error

	switch {
	case c.cfg.unmarshalers.RefusesForText(v.Type, content):
		cause = textRejection(content)

	// The decoder parses the text of a scalar as a duration, or hands it
	// to an UnmarshalText method, and returns the error of that parse as
	// it is.
	case astnode.IsScalar(content) && (v.Type == unmarshaler.DurationType || c.cfg.unmarshalers.FromText(v.Type)):
		cause = asDecodeError(c.node.doc.restoreError(err))

	default:
		return
	}

	path := v.Path()

	loc, lerr := c.node.pathLocation(path)
	if lerr != nil || loc.tk != astnode.PathToken(v.Held, v.From) || !c.confirmed(err, v) {
		return
	}

	c.add(decodeProblem{err: Rebase(cause, path), at: loc.pos})
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

// confirmed reports whether the value that reads v, a leaf, rejects it as
// err, the rejection a decode of v alone returned.
//
// A struct reads a field from a mapping that holds the entry of the
// field alone, into a new value of the type of that struct, as the
// [decodetree.Tree] of the document reads the entry. So does a struct
// that holds a map inline, for a member of that map. The decoder thus
// applies what the walk cannot see, such as a
// [yaml.RegisterCustomUnmarshaler] function for the struct. A struct that
// holds an inline field that decodes itself, as
// [unmarshaler.Options.InlinesUnmarshaler] reports, confirms nothing,
// since any decode of it calls that unmarshaler.
//
// A sequence or a map reads v as the one element of a sequence, which
// decodes into a slice of the type of v. The decoder reads a field, an
// element, and a map value alike, and it reads an alias otherwise than
// the content of its anchor. Where the value it holds for the anchor fits
// the target, it assigns that value and decodes nothing. A leaf that
// fails alone can thus pass through its alias.
//
// The value the walk starts at decodes alone in the decode itself, so
// nothing more confirms it.
func (c *problemCollector) confirmed(err error, v *decodewalk.Value) bool {
	var (
		node   ast.Node
		target reflect.Value
	)

	switch {
	case v.Struct != nil:
		if c.cfg.unmarshalers.InlinesUnmarshaler(v.Struct) {
			return false
		}

		view, ok := c.rules.View(v.Entry).(*ast.MappingValueNode)
		if !ok || view.Key == nil {
			return false
		}

		node, target = ast.Mapping(view.Start, false, view), reflect.New(v.Struct)

	case v.Kind == decodewalk.Element || v.Kind == decodewalk.Member:
		elements := ast.Sequence(v.Held.GetToken(), false)
		elements.Values = append(elements.Values, c.rules.View(v.Held))

		node, target = elements, reflect.New(reflect.SliceOf(v.Type))

	default:
		return true
	}

	return sameRejection(err, c.rules.Decode(node, target.Interface()))
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
