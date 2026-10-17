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

// deprecated holds the deprecations of one node of the text that a check
// reads. Together they give one warning.
type deprecated struct {
	// The record that the check evaluated first, which gives the headline.
	first jsonschema.Deprecation
	// The token at each step of the path of target. [compareRoutes]
	// orders the paths to one node by these tokens.
	route []*token.Token
	// The messages of the schemas, each once, in the order that the check
	// evaluated them.
	details []string
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

		if msg := deprecationMessage(rec.Schema); msg != "" && !slices.Contains(d.details, msg) {
			d.details = append(d.details, msg)
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

// deprecationWarning returns the warning of d. A record that marks a key
// reads `deprecated field "name"`, and one that marks a value reads
// `deprecated value "text"` for a scalar and `deprecated value` for a
// mapping or a sequence. Each message of a schema becomes a detail.
func (c converter) deprecationWarning(d *deprecated, instance any) error {
	key := d.first.TargetsKey()

	headline := fmt.Errorf("%w value", ErrDeprecated)

	if key {
		headline = fmt.Errorf("%w field %q", ErrDeprecated, keyName(d))
	} else if text, ok := c.valueText(d, instance); ok {
		headline = fmt.Errorf("%w value %q", ErrDeprecated, text)
	}

	opts := c.atTarget(d.target, key)

	if len(d.details) > 0 {
		details := make([]error, 0, len(d.details))
		for _, msg := range d.details {
			details = append(details, errors.New(msg))
		}

		opts = append(opts, niceyaml.WithDetails(details...))
	}

	//nolint:wrapcheck // The mark adds no text.
	return hook.MarkWarning(niceyaml.Invalid(headline, opts...))
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

// valueText returns the text of the scalar that d marks, and reports
// whether d marks a scalar with text. It reads the text of the node, and
// the value in instance where d does not reach a node.
func (c converter) valueText(d *deprecated, instance any) (string, bool) {
	switch node := c.idx.Deref(d.target.Node).(type) {
	case nil:
	case *ast.MappingNode, *ast.MappingValueNode, *ast.SequenceNode:
		return "", false
	default:
		if text, ok := stringText(node); ok {
			return text, true
		}

		tk := node.GetToken()
		if tk == nil || tk.Value == "" {
			return "", false
		}

		return tk.Value, true
	}

	return instanceText(instance, d.first.InstanceSegments())
}

// instanceText returns the JSON text of the scalar at segments in
// instance, and reports whether instance holds a scalar there.
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
