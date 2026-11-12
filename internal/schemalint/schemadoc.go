package schemalint

import (
	"maps"
	"net/url"
	"slices"
	"strings"

	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml/paths"
)

// Doc is one compiled schema document, which resolves the $ref and
// $dynamicRef keywords that point inside it.
//
// Create instances with [NewDoc].
type Doc struct {
	byPointer map[string]*jsonschema.Schema
	byAnchor  map[string]*jsonschema.Schema
	root      *jsonschema.Schema
	// Whether the document ignores each sibling of a $ref, as Draft-07
	// does.
	refOnly bool
}

// Property is what a schema document says about one property.
type Property struct {
	// Path names the property.
	Path paths.Path
	// Required reports whether a required keyword in the chain of the
	// parent schema names the property.
	Required bool
	// Deprecated reports whether a schema in the chain of a property schema
	// holds the deprecated keyword, as [Doc.Marked] reads it.
	Deprecated bool
}

// NewDoc creates a new [Doc] of the schema that v checks with.
func NewDoc(v *jsonschema.Validator) *Doc {
	d := &Doc{
		byPointer: make(map[string]*jsonschema.Schema),
		byAnchor:  make(map[string]*jsonschema.Schema),
		root:      v.Schema(),
		refOnly:   v.Draft() == jsonschema.Draft7,
	}

	for loc, s := range jsonschema.Schemas(d.root) {
		d.byPointer[string(loc.Pointer)] = s

		for _, anchor := range []string{s.Anchor, s.DynamicAnchor} {
			if _, ok := d.byAnchor[anchor]; anchor != "" && !ok {
				d.byAnchor[anchor] = s
			}
		}
	}

	return d
}

// Root returns the root schema of the document.
func (d *Doc) Root() *jsonschema.Schema {
	return d.root
}

// resolve returns the schema that ref names inside the document, or nil
// for a ref to another document or to a place that does not exist.
func (d *Doc) resolve(ref string) *jsonschema.Schema {
	frag, ok := strings.CutPrefix(ref, "#")
	if !ok {
		return nil
	}

	frag, err := url.PathUnescape(frag)
	if err != nil {
		return nil
	}

	if frag == "" || strings.HasPrefix(frag, "/") {
		return d.byPointer[frag]
	}

	return d.byAnchor[frag]
}

// chain returns the schema s and each schema whose keywords apply to a
// value along with s. Each schema that a chain of $ref, $dynamicRef, and
// allOf reaches from s is such a schema. So is the branch of an anyOf that
// [nullable] reads through. Under Draft-07, a schema with a $ref adds only
// the schema that the $ref reaches.
func (d *Doc) chain(s *jsonschema.Schema) []*jsonschema.Schema {
	out, _ := d.chainOf(s)

	return out
}

// chainOf returns the chain of s, as [Doc.chain] does. It also reports
// whether the chain holds a $ref or $dynamicRef that the document does not
// resolve, such as a $ref to another document.
func (d *Doc) chainOf(s *jsonschema.Schema) ([]*jsonschema.Schema, bool) {
	var out []*jsonschema.Schema

	lost := false
	seen := make(map[*jsonschema.Schema]bool)
	queue := []*jsonschema.Schema{s}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if cur == nil || seen[cur] {
			continue
		}

		seen[cur] = true

		if cur.Ref != "" {
			target := d.resolve(cur.Ref)
			lost = lost || target == nil
			queue = append(queue, target)

			if d.refOnly {
				continue
			}
		}

		out = append(out, cur)

		if cur.DynamicRef != "" {
			target := d.resolve(cur.DynamicRef)
			lost = lost || target == nil
			queue = append(queue, target)
		}

		queue = append(queue, cur.AllOf...)

		if other := nullable(cur); other != nil {
			queue = append(queue, other)
		}
	}

	return out, lost
}

// nullable returns the branch of the anyOf of s that admits more than
// null, when the other branch admits only null. The anyOf that gen writes
// around the schema of a pointer holds two such branches. It returns nil
// for any other schema.
func nullable(s *jsonschema.Schema) *jsonschema.Schema {
	if len(s.AnyOf) != 2 {
		return nil
	}

	for i, branch := range s.AnyOf {
		if onlyNull(branch) {
			return s.AnyOf[1-i]
		}
	}

	return nil
}

// onlyNull reports whether s is a schema whose type admits only null.
func onlyNull(s *jsonschema.Schema) bool {
	return s != nil && (s.Type == "null" || len(s.Types) == 1 && s.Types[0] == "null")
}

// Marked reports whether a schema in the chain of s holds the deprecated
// keyword. The chain of s holds s and each schema that a chain of $ref,
// $dynamicRef, and allOf reaches from it without a branch on the way. It
// also holds the branch of the anyOf that gen writes around the schema of
// a pointer that admits more than null.
func (d *Doc) Marked(s *jsonschema.Schema) bool {
	return slices.ContainsFunc(d.chain(s), func(t *jsonschema.Schema) bool { return t.Deprecated })
}

// Field is what a schema document says about one value, as [Doc.Field]
// finds it.
type Field struct {
	// Declared reports whether the schema declares the value, through
	// properties for a name or items for an index. The schema that such a
	// keyword gives the value does not change the answer.
	Declared bool
	// Required reports whether a required keyword names the value.
	Required bool
	// Unsure reports whether a schema that the walk does not read might
	// read the value. Such a schema is a branch that declares or requires
	// the value, or one behind a $ref or $dynamicRef on the way that the
	// document does not resolve.
	Unsure bool
}

// Field returns what the document says about the value at p. The walk
// follows the properties of each name of p and the items of each index,
// through the chain of each schema on the way, as [Doc.Marked] describes
// a chain. A value that the walk does not reach is neither declared nor
// required. Beside the chains, the walk follows the branches of each
// schema on the way, to tell whether one of them reads the value. A branch
// is a schema under anyOf, oneOf, not, if, then, else, or
// dependentSchemas.
//
// A schema on the way declares the value when it gives the value a schema,
// whatever that schema holds. A property schema whose chain is empty thus
// declares its property. Under Draft-07, the chain of a schema that holds
// a $ref to another document is empty.
func (d *Doc) Field(p paths.Path) Field {
	var f Field

	group, lost := d.chainOf(d.root)

	var branches []*jsonschema.Schema

	mentioned := false

	for sel := range p.Selectors() {
		more, moreLost := d.branches(append(slices.Clone(group), branches...))
		branches = append(branches, more...)
		lost = lost || moreLost

		var next, nextBranches []*jsonschema.Schema

		f.Declared, f.Required = false, false
		mentioned = false

		for _, s := range group {
			f.Required = f.Required || sel.Kind == paths.SelectorChild && slices.Contains(s.Required, sel.Name)

			sub, subLost, ok := d.below(s, sel)
			next = append(next, sub...)
			lost = lost || subLost
			f.Declared = f.Declared || ok
		}

		for _, s := range branches {
			mentioned = mentioned || sel.Kind == paths.SelectorChild && slices.Contains(s.Required, sel.Name)

			sub, subLost, ok := d.below(s, sel)
			nextBranches = append(nextBranches, sub...)
			lost = lost || subLost
			mentioned = mentioned || ok
		}

		group, branches = next, nextBranches
	}

	f.Unsure = lost || mentioned

	return f
}

// below returns the chain of the schema that s gives the value at sel, a
// name or an index. It also reports whether that chain holds a ref that
// the document does not resolve, as [Doc.chainOf] does. The last result
// reports whether s gives the value a schema. The chain of such a schema
// is empty under Draft-07 when the schema holds a $ref that the document
// does not resolve.
func (d *Doc) below(s *jsonschema.Schema, sel paths.Selector) ([]*jsonschema.Schema, bool, bool) {
	var sub *jsonschema.Schema

	switch sel.Kind {
	case paths.SelectorChild:
		sub = s.Properties[sel.Name]
	case paths.SelectorIndex:
		sub = item(s, sel.Index)
	default:
		return nil, false, false
	}

	if sub == nil {
		return nil, false, false
	}

	chain, lost := d.chainOf(sub)

	return chain, lost, true
}

// branches returns the chain of each branch of a schema in group, and of
// each branch of a schema that such a chain holds. A branch is a schema
// under anyOf, oneOf, not, if, then, else, or dependentSchemas. Its
// keywords apply to a value in some documents and not in others. The
// chain already holds the other branch of the anyOf that [nullable] reads
// through, so that anyOf does not add a branch. The result also reports
// whether a chain holds a ref that the document does not resolve.
func (d *Doc) branches(group []*jsonschema.Schema) ([]*jsonschema.Schema, bool) {
	var out []*jsonschema.Schema

	lost := false
	seen := make(map[*jsonschema.Schema]bool)
	queue := group

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if cur == nil || seen[cur] {
			continue
		}

		seen[cur] = true

		for _, branch := range branchesOf(cur) {
			chain, chainLost := d.chainOf(branch)
			lost = lost || chainLost

			out = append(out, chain...)
			queue = append(queue, chain...)
		}
	}

	return out, lost
}

// branchesOf returns the branches of s, as [Doc.branches] names them. The
// order is not fixed. The result holds nil for each branch keyword that s
// leaves out.
func branchesOf(s *jsonschema.Schema) []*jsonschema.Schema {
	var out []*jsonschema.Schema

	if nullable(s) == nil {
		out = append(out, s.AnyOf...)
	}

	out = append(out, s.OneOf...)
	out = append(out, s.Not, s.If, s.Then, s.Else)
	out = slices.AppendSeq(out, maps.Values(s.DependentSchemas))

	return slices.AppendSeq(out, maps.Values(s.DependencySchemas))
}

// item returns the schema that prefixItems, items, or additionalItems
// gives the element at index i of an array that s checks. It returns nil
// when s does not give the element a schema through one of these keywords.
func item(s *jsonschema.Schema, i int) *jsonschema.Schema {
	switch {
	case i < len(s.PrefixItems):
		return s.PrefixItems[i]
	case i < len(s.ItemsArray):
		return s.ItemsArray[i]
	case len(s.ItemsArray) > 0:
		return s.AdditionalItems
	default:
		return s.Items
	}
}

// Properties returns what the document says about each property that the
// root schema reaches through properties, in the order of a walk that
// visits the names of each mapping in sorted order. A property schema
// marks its property deprecated or required through its own keywords and
// those of each schema in its chain, as [Doc.Marked] describes a chain.
func (d *Doc) Properties() []*Property {
	var out []*Property

	byPath := make(map[string]*Property)
	d.collect(d.root, paths.Doc(), map[*jsonschema.Schema]bool{}, func(p paths.Path) *Property {
		key := p.String()

		prop, ok := byPath[key]
		if !ok {
			prop = &Property{Path: p}
			byPath[key] = prop
			out = append(out, prop)
		}

		return prop
	})

	return out
}

// collect records what the chain of s says about each property under
// path, and walks each property schema in turn. The walk stops at a
// schema on its own path, so a recursive type adds its properties once.
func (d *Doc) collect(
	s *jsonschema.Schema,
	path paths.Path,
	onPath map[*jsonschema.Schema]bool,
	at func(paths.Path) *Property,
) {
	group := d.chain(s)
	for _, t := range group {
		if onPath[t] {
			return
		}
	}

	for _, t := range group {
		onPath[t] = true
	}

	defer func() {
		for _, t := range group {
			delete(onPath, t)
		}
	}()

	// A name that the group requires and lists under no properties is a
	// property too.
	names := make(map[string]bool)
	required := make(map[string]bool)
	subs := make(map[string][]*jsonschema.Schema)

	for _, t := range group {
		for _, name := range t.Required {
			names[name], required[name] = true, true
		}

		for name, sub := range t.Properties {
			names[name] = true
			subs[name] = append(subs[name], sub)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(names)) {
		p := at(path.Child(name))
		p.Required = p.Required || required[name]

		for _, sub := range subs[name] {
			p.Deprecated = p.Deprecated || d.Marked(sub)
			d.collect(sub, path.Child(name), onPath, at)
		}
	}
}
