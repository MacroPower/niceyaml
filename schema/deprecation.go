package schema

import (
	"cmp"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Deprecation is the warning about one node that deprecated schemas apply
// to. [Schema.Check] and [Schema.ValidateValue] report each one as a
// [*niceyaml.Error] that wraps a Deprecation and carries the YAML path to
// the node, as they report a [*Violation]. So a report reads the schemas
// from the Deprecation instead of matching on the text of the warning:
//
//	for problem := range niceyaml.Problems(warnings.Err()) {
//		d, ok := errors.AsType[*schema.Deprecation](problem.Err)
//		if !ok {
//			continue
//		}
//
//		path, _ := problem.Path()
//
//		for _, s := range d.Schemas {
//			emit(s.SchemaPath, path, s.Message)
//		}
//	}
//
// A check gives each node at most one warning, as the Deprecations
// section of the package describes. So one Deprecation lists every
// deprecated schema that reaches its node.
//
// Every Deprecation matches [ErrDeprecated], and so do warnings that a
// schema did not produce. A load of [go.jacobcolvin.com/niceyaml/manifest]
// warns about a deprecated version or kind, and that warning does not wrap
// a Deprecation.
//
//nolint:errname // Named for what it reports, as [Violation] is.
type Deprecation struct {
	// Message is the headline of the warning, such as `deprecated field
	// "legacyPricing"`. It is the text that [Deprecation.Error] returns,
	// and the message of the [*niceyaml.Error] that wraps the Deprecation.
	Message string

	// Schemas holds each deprecated schema that reaches the node, in the
	// order that the check evaluated them. A value has two when the
	// definition behind a $ref and an allOf branch beside the $ref both
	// deprecate it. The check reaches a node behind an alias or a merge
	// key once for each use, and Schemas lists each SchemaPath once.
	Schemas []DeprecatedSchema

	// Key reports whether the schemas mark the key of a member. When it
	// is false, they mark the value.
	Key bool
}

// DeprecatedSchema is one schema with "deprecated": true that applies to
// the node of a [Deprecation].
type DeprecatedSchema struct {
	// SchemaPath is the JSON Pointer to the schema, along the path that
	// the validator took to reach it. It names the schema and not its
	// deprecated keyword, so the root schema has the empty pointer. The
	// path steps through each $ref on the way, as
	// [Violation.SchemaPath] does. A schema in a definition that several
	// references reach therefore has one SchemaPath per reference.
	SchemaPath string

	// Message is the deprecationMessage keyword of the schema. It is
	// empty for a schema without that keyword.
	Message string
}

// Error returns [Deprecation.Message]. A nil Deprecation has an empty
// message.
func (d *Deprecation) Error() string {
	if d == nil {
		return ""
	}

	return d.Message
}

// Is reports whether target is [ErrDeprecated]. A nil Deprecation does
// not match any target.
func (d *Deprecation) Is(target error) bool {
	return d != nil && target == ErrDeprecated
}

// deprecated holds the deprecations of one node of the text that a check
// reads. Together they give one warning.
type deprecated struct {
	// The record that the check evaluated first, which gives the headline.
	first jsonschema.Deprecation
	// The token at each step of the path of target. [compareRoutes]
	// orders the paths to one node by these tokens.
	route []*token.Token
	// The schemas of the records, each once per schema path, in the
	// order that the check evaluated them.
	schemas []DeprecatedSchema
	// The target of the path that comes first in document order.
	target datapath.Target
}

// nodeID names the node of the text that a record reaches. For a record
// that marks the key of a member, the node is the entry of the member.
// For any other record, the node is the content of the value. A record
// that does not reach a node, such as a record of a value that no
// document holds, takes its path in place of the node.
//
//nolint:unused // The fields tell the keys of a map apart.
type nodeID struct {
	node ast.Node
	path string
	key  bool
}

// deprecations returns one warning for each node of the text that records
// reach, as [Schema.Check] describes. The instance is the value that the
// check read. It gives the text of a value that no node holds.
func (c converter) deprecations(records []jsonschema.Deprecation, instance any) []error {
	var found []*deprecated

	byNode := make(map[nodeID]*deprecated)

	for _, rec := range records {
		target, route := sourceRoute(c.root, c.idx, rec.InstanceSegments(), rec.TargetsKey())
		id := c.nodeID(rec, target)

		d, ok := byNode[id]
		switch {
		case !ok:
			d = &deprecated{target: target, route: route, first: rec}
			byNode[id] = d
			found = append(found, d)

		case compareRoutes(route, d.route) < 0:
			d.target, d.route = target, route
		}

		schemaPath := string(rec.SchemaPath)
		if !slices.ContainsFunc(d.schemas, func(s DeprecatedSchema) bool { return s.SchemaPath == schemaPath }) {
			d.schemas = append(d.schemas, DeprecatedSchema{
				SchemaPath: schemaPath,
				Message:    deprecationMessage(rec.Schema),
			})
		}
	}

	warnings := make([]error, 0, len(found))
	for _, d := range found {
		warnings = append(warnings, c.deprecationWarning(d, instance))
	}

	return warnings
}

// nodeID returns the [nodeID] of the node that rec reaches at t.
func (c converter) nodeID(rec jsonschema.Deprecation, t datapath.Target) nodeID {
	key := rec.TargetsKey()

	var node ast.Node

	switch {
	case key && t.Parent.Entry != nil:
		node = t.Parent.Entry
	case !key:
		node = c.idx.Deref(t.Node)
	}

	if astnode.IsNil(node) {
		return nodeID{path: string(rec.InstancePath), key: key}
	}

	return nodeID{node: node, key: key}
}

// deprecationWarning returns the warning of d, which wraps a
// [*Deprecation]. A record that marks a key reads `deprecated field
// "name"`, and one that marks a value reads `deprecated value "text"` for
// a scalar and `deprecated value` for a mapping or a sequence. The text is
// that of the value in instance, which the schema validated. Each distinct
// message of a schema becomes a detail.
func (c converter) deprecationWarning(d *deprecated, instance any) error {
	key := d.first.TargetsKey()

	message := ErrDeprecated.Error() + " value"

	if key {
		message = fmt.Sprintf("%s field %q", ErrDeprecated, keyName(d))
	} else if text, ok := instanceText(instance, d.first.InstanceSegments()); ok {
		message = fmt.Sprintf("%s value %q", ErrDeprecated, text)
	}

	opts := c.atTarget(d.target, key)

	var (
		messages []string
		details  []error
	)

	for _, s := range d.schemas {
		if s.Message == "" || slices.Contains(messages, s.Message) {
			continue
		}

		messages = append(messages, s.Message)
		details = append(details, errors.New(s.Message))
	}

	if len(details) > 0 {
		opts = append(opts, niceyaml.WithDetails(details...))
	}

	//nolint:wrapcheck // The mark adds no text.
	return hook.MarkWarning(niceyaml.Invalid(&Deprecation{Message: message, Schemas: d.schemas, Key: key}, opts...))
}

// keyName returns the name of the member that d marks, as its path spells
// it.
func keyName(d *deprecated) string {
	sel, ok := d.target.Path.Last()
	if ok && sel.Kind == paths.SelectorChild {
		return sel.Name
	}

	segs := d.first.InstanceSegments()

	return segs[len(segs)-1].Key
}

// instanceText returns the JSON text of the scalar at segments in
// instance, and reports whether instance holds a scalar there. The
// instance is the value that go-yaml decoded and the schema validated, so
// "!!str ~" gives "" and a block scalar gives its string.
func instanceText(instance any, segments []jsonschema.Segment) (string, bool) {
	v := instance

	for _, seg := range segments {
		switch x := v.(type) {
		case map[string]any:
			v = x[seg.Key]
		case []any:
			if seg.Index >= len(x) {
				return "", false
			}

			v = x[seg.Index]

		default:
			return "", false
		}
	}

	switch x := v.(type) {
	case nil:
		return "null", true
	case string:
		return x, true
	}

	switch reflect.ValueOf(v).Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		return "", false
	default:
		return fmt.Sprint(v), true
	}
}

// deprecationMessage returns the deprecationMessage keyword of s, or "" for
// a schema without one.
func deprecationMessage(s *jsonschema.Schema) string {
	if s == nil {
		return ""
	}

	msg, _ := s.Extra[jsonschema.KeywordDeprecationMessage].(string) //nolint:errcheck // Any other value holds no message.

	return msg
}

// compareRoutes orders two paths that reach one node by document order.
// It compares the position of the token at each step in turn, and puts a
// step without a token last.
func compareRoutes(a, b []*token.Token) int {
	for i := range min(len(a), len(b)) {
		if c := compareTokens(a[i], b[i]); c != 0 {
			return c
		}
	}

	return cmp.Compare(len(a), len(b))
}

// compareTokens orders two tokens by line, then by column, and puts a
// token without a position last.
func compareTokens(a, b *token.Token) int {
	switch hasA, hasB := a != nil && a.Position != nil, b != nil && b.Position != nil; {
	case !hasA && !hasB:
		return 0
	case !hasA:
		return 1
	case !hasB:
		return -1
	}

	return cmp.Or(cmp.Compare(a.Position.Line, b.Position.Line), cmp.Compare(a.Position.Column, b.Position.Column))
}
