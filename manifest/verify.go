package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/schemahook"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Verify loads and compiles the schema of each version, and returns an
// error that names each version whose schema fails, with the error of the
// schema. A load fails each document that reaches such a version, so a
// test calls Verify to find the mistake before a user does:
//
//	func TestRegistry(t *testing.T) {
//		t.Parallel()
//
//		require.NoError(t, config.Registry.Verify(t.Context()))
//	}
//
// Verify shares the schemas with the loads of the registry, so a schema
// that compiles here serves each load without a second compile.
//
// # Type Fields
//
// Verify also reports each type field that the schema of a version
// requires and does not declare under properties. The check of a schema
// sees only the type fields that the schema declares, so such a schema
// fails every document:
//
//	MenuItem v1: $.kind: required, but no properties keyword declares it
//
// The check follows the chains of schemas that the Deprecations section
// describes. It does not look inside a branch or behind a $ref to another
// document. A branch is a schema under anyOf, oneOf, not, if, then, else,
// or dependentSchemas. Verify reports each type field that a branch
// declares or requires, and each on a path through such a $ref, when the
// schema does not also declare the field outside them:
//
//	MenuItem v1: $.kind: a branch or a $ref to another document might read it
//
// Verify skips each version that [Unversioned] adds, whose documents
// hold no type fields.
//
// # Deprecations
//
// Verify also reports each use of the deprecated keyword in the schema of
// the current version that leaves a user no way to follow the warning:
//
//   - A property that the schema requires and marks deprecated. The user
//     cannot remove it without failing the load:
//
//     MenuItem v1: $.spec.legacyPricing: required and deprecated
//
//   - A property that the schema deprecates and the schema of the written
//     version requires. A file at the written version warns about it, and
//     the user cannot remove it until the mark moves:
//
//     Hours v2: $.spec.notes: deprecated, but v1 requires it
//
//   - A deprecated keyword on the root schema, which warns about every
//     document of the kind. [Deprecated] on the current version marks a
//     kind deprecated instead:
//
//     Hours v1: $: the root schema is deprecated
//
// A chain of $ref, $dynamicRef, and allOf with no branch on the way joins
// schemas. So does the anyOf that gen writes around the schema of a
// pointer, whose other branch admits only null. A property schema marks
// its property deprecated or required
// through its own keywords and those of each schema that such a chain
// reaches from it. The root schema is the schema of the version and each
// schema that such a chain reaches from it. Verify matches the properties
// of two versions by path, so it misses a property that a step renames
// between them. It follows each $ref inside the schema document, and
// passes over a $ref to another document.
//
// # Defaults
//
// Verify also reports each property that the schema of the current
// version requires, that the document replaces whole, and whose default
// is not zero. Every file must then write the property, so the default
// never applies:
//
//	Cafe v1: $.spec.seats: required, so its default 20 never applies
//
// The defaults come from the [niceyaml.WithDefaults] options among the
// decode options of the registry and the kind. A document replaces whole
// every value but a struct, which a mapping merges into field by field,
// and a pointer to one. The walk follows properties, items, and
// additionalProperties through the chains of schemas. Under an item, a
// map value, or a nil pointer, it reads the defaults of the type. Under a
// pointer that a parent function sets, it reads the value that the
// function set.
//
// A function of a parent type might set a field of a child type, as one
// of CafeSpec sets the port of its kitchen Printer. A detail then names
// the default that the child type gives itself, when the default at the
// place differs. The omitzero option on the field makes it optional at
// each place of the child type. A panic in a function of WithDefaults reaches the
// caller.
func (r *Registry) Verify(ctx context.Context) error {
	var errs []error

	for _, entry := range r.kinds {
		compiled := make(map[*versionEntry]*jsonschema.Validator)

		for _, v := range entry.versions {
			if !v.cfg.hasSchema {
				continue
			}

			sch, err := r.schemas.Schema(ctx, v.cfg.schema)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s %s: %w", entry.kind.name, v.label(), err))

				continue
			}

			compiled[v] = schemahook.Validator(sch)
		}

		errs = append(errs, r.verifyTypeFields(entry, compiled)...)
		errs = append(errs, verifyDeprecations(entry, compiled)...)
		errs = append(errs, r.verifyDefaults(entry, compiled)...)
	}

	return errors.Join(errs...)
}

// verifyTypeFields returns the problems of the type fields in the schema
// of each version of entry, as [Registry.Verify] describes. The compiled
// map holds the validator of each version whose schema compiled, or nil
// for a schema that checks nothing.
func (r *Registry) verifyTypeFields(entry *kindEntry, compiled map[*versionEntry]*jsonschema.Validator) []error {
	var errs []error

	for _, v := range entry.versions {
		cv := compiled[v]

		// An unversioned document writes no type fields, so its schema
		// never sees one.
		if cv == nil || v.cfg.unversioned {
			continue
		}

		name := entry.kind.name + " " + v.label()
		doc := newSchemaDoc(cv)

		for _, p := range r.typeFields() {
			f := doc.field(p)

			switch {
			case f.declared:
				// The check sees the field.

			case f.required:
				errs = append(errs, authorError(
					fmt.Sprintf("%s: %s: required, but no properties keyword declares it", name, p),
					fmt.Sprintf("declare %s under properties, or remove it from required", p),
				))

			case f.unsure:
				errs = append(errs, authorError(
					fmt.Sprintf("%s: %s: a branch or a $ref to another document might read it", name, p),
					fmt.Sprintf("declare %s under properties outside every branch and every such $ref", p),
				))
			}
		}
	}

	return errs
}

// verifyDeprecations returns the problems of the deprecated keywords in
// the schema of the current version of entry, as [Registry.Verify]
// describes. The compiled map holds the validator of each version whose
// schema compiled, or nil for a schema that checks nothing.
func verifyDeprecations(entry *kindEntry, compiled map[*versionEntry]*jsonschema.Validator) []error {
	current := entry.versions[len(entry.versions)-1]

	cv := compiled[current]
	if cv == nil {
		return nil
	}

	name := entry.kind.name + " " + current.label()
	doc := newSchemaDoc(cv)

	var errs []error

	if doc.marked(cv.Schema()) {
		errs = append(errs, authorError(name+": $: the root schema is deprecated",
			"use manifest.Deprecated instead"))
	}

	props := doc.properties()
	for _, p := range props {
		if p.required && p.deprecated {
			errs = append(errs, authorError(fmt.Sprintf("%s: %s: required and deprecated", name, p.path)))
		}
	}

	written := entry.written

	wv := compiled[written]
	if written == current || wv == nil {
		return errs
	}

	required := make(map[string]bool)
	for _, p := range newSchemaDoc(wv).properties() {
		required[p.path.String()] = p.required
	}

	for _, p := range props {
		if p.deprecated && required[p.path.String()] {
			errs = append(errs, authorError(
				fmt.Sprintf("%s: %s: deprecated, but %s requires it", name, p.path, written.label()),
				fmt.Sprintf("tag the field jsonschema:%q until %s carries manifest.Written",
					"deprecated=false", current.label()),
			))
		}
	}

	return errs
}

// authorError returns the problem msg of a registry, with each of details
// as a detail. The author of the registry is at fault for it, not a
// document.
func authorError(msg string, details ...string) error {
	errs := make([]error, 0, len(details))
	for _, d := range details {
		errs = append(errs, errors.New(d))
	}

	//nolint:wrapcheck // The error is the problem itself.
	return niceyaml.Unchecked(errors.New(msg), niceyaml.WithDetails(errs...))
}

// schemaDoc is one compiled schema document, which resolves the $ref and
// $dynamicRef keywords that point inside it.
type schemaDoc struct {
	byPointer map[string]*jsonschema.Schema
	byAnchor  map[string]*jsonschema.Schema
	root      *jsonschema.Schema
	// The document ignores each sibling of a $ref, as Draft-07 does.
	refOnly bool
}

// property is what the schema of a version says about one property.
type property struct {
	path       paths.Path
	required   bool
	deprecated bool
}

// newSchemaDoc returns the [schemaDoc] of the schema that v checks with.
func newSchemaDoc(v *jsonschema.Validator) *schemaDoc {
	d := &schemaDoc{
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

// resolve returns the schema that ref names inside the document, or nil
// for a ref to another document or to nothing.
func (d *schemaDoc) resolve(ref string) *jsonschema.Schema {
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

// chain returns the schemas whose keywords apply to a value along with
// the schema s itself. Each schema that a chain of $ref, $dynamicRef, and
// allOf reaches from s is one of them, and so is the branch of an anyOf
// that [nullable] reads through. Under Draft-07, a schema with a $ref
// adds only the schema that the $ref reaches.
func (d *schemaDoc) chain(s *jsonschema.Schema) []*jsonschema.Schema {
	out, _ := d.chainOf(s)

	return out
}

// chainOf returns the chain of s, as [schemaDoc.chain] does. It also
// reports whether the chain holds a $ref or $dynamicRef that the document
// does not resolve, such as a $ref to another document.
func (d *schemaDoc) chainOf(s *jsonschema.Schema) ([]*jsonschema.Schema, bool) {
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

// marked reports whether a schema in the chain of s holds the deprecated
// keyword.
func (d *schemaDoc) marked(s *jsonschema.Schema) bool {
	return slices.ContainsFunc(d.chain(s), func(t *jsonschema.Schema) bool { return t.Deprecated })
}

// schemaField is what a schema document says about one value, as
// [schemaDoc.field] finds it.
type schemaField struct {
	// The schema declares the value, through properties for a name or
	// items for an index.
	declared bool
	// A required keyword names the value.
	required bool
	// A schema that the walk does not read might read the value. Such a
	// schema is a branch that declares or requires the value, or one behind
	// a $ref or $dynamicRef on the way that the document does not resolve.
	unsure bool
}

// field returns what the document says about the value at p. The walk
// follows the properties of each name of p and the items of each index,
// through the chain of each schema on the way. A value that the walk does
// not reach is neither declared nor required. Beside the chains, the walk
// follows the branches of each schema on the way, as [schemaDoc.branches]
// finds them, to tell whether one of them reads the value.
func (d *schemaDoc) field(p paths.Path) schemaField {
	var f schemaField

	group, lost := d.chainOf(d.root)

	var branches []*jsonschema.Schema

	mentioned := false

	for sel := range p.Selectors() {
		more, moreLost := d.branches(append(slices.Clone(group), branches...))
		branches = append(branches, more...)
		lost = lost || moreLost

		var next, nextBranches []*jsonschema.Schema

		f.required = false
		mentioned = false

		for _, s := range group {
			f.required = f.required || sel.Kind == paths.SelectorChild && slices.Contains(s.Required, sel.Name)

			sub, subLost := d.below(s, sel)
			next = append(next, sub...)
			lost = lost || subLost
		}

		for _, s := range branches {
			mentioned = mentioned || sel.Kind == paths.SelectorChild && slices.Contains(s.Required, sel.Name)

			sub, subLost := d.below(s, sel)
			nextBranches = append(nextBranches, sub...)
			lost = lost || subLost
		}

		f.declared = len(next) > 0
		mentioned = mentioned || len(nextBranches) > 0
		group, branches = next, nextBranches
	}

	f.unsure = lost || mentioned

	return f
}

// below returns the chain of the schema that s gives the value at sel, a
// name or an index, or nil when s gives the value none. It also reports
// whether that chain holds a ref that the document does not resolve, as
// [schemaDoc.chainOf] does.
func (d *schemaDoc) below(s *jsonschema.Schema, sel paths.Selector) ([]*jsonschema.Schema, bool) {
	var sub *jsonschema.Schema

	switch sel.Kind {
	case paths.SelectorChild:
		sub = s.Properties[sel.Name]
	case paths.SelectorIndex:
		sub = item(s, sel.Index)
	default:
		return nil, false
	}

	if sub == nil {
		return nil, false
	}

	return d.chainOf(sub)
}

// branches returns the chain of each branch of a schema in group, and of
// each branch of a schema that such a chain holds. A branch is a schema
// under anyOf, oneOf, not, if, then, else, or dependentSchemas. Its
// keywords apply to a value in some documents and not in others. The
// anyOf that [nullable] reads through adds no branch, because the chain
// holds its other branch already. The result also reports whether a
// chain holds a ref that the document does not resolve.
func (d *schemaDoc) branches(group []*jsonschema.Schema) ([]*jsonschema.Schema, bool) {
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

// branchesOf returns the branches of s, as [schemaDoc.branches] names
// them, in no fixed order. It holds nil for each branch keyword that s
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
// gives the element at index i of an array that s checks, or nil when
// none of them gives one.
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

// properties returns what the document says about each property that the
// root schema reaches through properties, in the order of a walk that
// visits the names of each mapping in sorted order.
func (d *schemaDoc) properties() []*property {
	var out []*property

	byPath := make(map[string]*property)
	d.collect(d.root, paths.Doc(), map[*jsonschema.Schema]bool{}, func(p paths.Path) *property {
		key := p.String()

		prop, ok := byPath[key]
		if !ok {
			prop = &property{path: p}
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
func (d *schemaDoc) collect(
	s *jsonschema.Schema,
	path paths.Path,
	onPath map[*jsonschema.Schema]bool,
	at func(paths.Path) *property,
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
		p.required = p.required || required[name]

		for _, sub := range subs[name] {
			p.deprecated = p.deprecated || d.marked(sub)
			d.collect(sub, path.Child(name), onPath, at)
		}
	}
}

// verifyDefaults returns a problem for each property that the schema of
// the current version of entry requires, that the document replaces
// whole, and whose default is not zero, as [Registry.Verify] describes.
// The compiled map holds the validator of each version whose schema
// compiled.
func (r *Registry) verifyDefaults(entry *kindEntry, compiled map[*versionEntry]*jsonschema.Validator) []error {
	current := entry.versions[len(entry.versions)-1]

	cv := compiled[current]
	if cv == nil {
		return nil
	}

	opts := slices.Concat(r.cfg.decodeOptions, entry.kind.cfg.decodeOptions)

	w := &defaultsWalk{
		doc:      newSchemaDoc(cv),
		name:     entry.kind.name + " " + current.label(),
		defaults: func(t reflect.Type) reflect.Value { return hook.Defaults(t, opts) },
		onPath:   map[defaultsKey]bool{},
	}

	t := entry.kind.typ
	w.walk(cv.Schema(), t, w.defaults(t), paths.Doc())

	return w.errs
}

// defaultsWalk walks a schema beside the Go type that a decode reads with
// it, and the defaults that the decode starts from at each place.
type defaultsWalk struct {
	doc      *schemaDoc
	defaults func(t reflect.Type) reflect.Value
	onPath   map[defaultsKey]bool
	name     string
	errs     []error
}

// defaultsKey names a schema that the walk reads as a type.
//
//nolint:unused // The fields tell the keys of a map apart.
type defaultsKey struct {
	schema *jsonschema.Schema
	typ    reflect.Type
}

// walk reads the value of type t at path, which starts from v, with the
// schema s. The walk follows properties, items, additionalProperties,
// and each schema in the chain of s. A pointer reads its target, which
// starts from the value the pointer holds, or from the defaults of its
// type for a nil pointer.
func (w *defaultsWalk) walk(s *jsonschema.Schema, t reflect.Type, v reflect.Value, path paths.Path) {
	for t.Kind() == reflect.Pointer {
		if v.IsNil() {
			v = w.defaults(t.Elem())
		} else {
			v = v.Elem()
		}

		t = t.Elem()
	}

	key := defaultsKey{schema: s, typ: t}
	if s == nil || w.onPath[key] {
		return
	}

	w.onPath[key] = true
	defer delete(w.onPath, key)

	group := w.doc.chain(s)

	switch {
	case t.Kind() == reflect.Struct && !unmarshaler.DecodesItself(t):
		w.fields(group, t, v, path)

	case t.Kind() == reflect.Slice || t.Kind() == reflect.Array:
		for _, g := range group {
			w.walk(g.Items, t.Elem(), w.defaults(t.Elem()), path.IndexAll())
		}

	case t.Kind() == reflect.Map:
		for _, g := range group {
			w.walk(g.AdditionalProperties, t.Elem(), w.defaults(t.Elem()), path.ChildAll())
		}
	}
}

// fields reads each field of v, a struct of type t that the decode reads
// field by field, with the properties of the schemas of group. It
// reports each field that group requires, that the document replaces
// whole, and whose default is not zero.
func (w *defaultsWalk) fields(group []*jsonschema.Schema, t reflect.Type, v reflect.Value, path paths.Path) {
	required := map[string]bool{}
	props := map[string][]*jsonschema.Schema{}

	for _, g := range group {
		for _, name := range g.Required {
			required[name] = true
		}

		for name, sub := range g.Properties {
			props[name] = append(props[name], sub)
		}
	}

	for _, f := range fieldsOf(t, v) {
		at := path.Child(f.name)

		if required[f.name] && replacedWhole(f.value.Type()) && !f.value.IsZero() {
			w.errs = append(w.errs, w.defaultProblem(at, f))
		}

		for _, sub := range props[f.name] {
			w.walk(sub, f.value.Type(), f.value, at)
		}
	}
}

// defaultProblem returns the problem of the field f at path, which the
// schema requires though its default is not zero. When the default at
// path differs from the one that the type that declares f gives itself, a
// detail says so.
func (w *defaultsWalk) defaultProblem(path paths.Path, f structField) error {
	msg := fmt.Sprintf("%s: %s: required, so its default %s never applies", w.name, path, valueText(f.value))

	own := w.defaults(f.owner).FieldByIndex(f.index)
	if reflect.DeepEqual(own.Interface(), f.value.Interface()) {
		return authorError(msg)
	}

	text := valueText(own)

	return authorError(msg, fmt.Sprintf("%s defaults %s to %s on its own, so omitzero makes %s optional with %s at "+
		"its other places", f.owner.Name(), f.name, text, f.name, text))
}

// structField is a field that a decode of a struct reads, with its
// default at one place.
type structField struct {
	owner reflect.Type
	value reflect.Value
	name  string
	index []int
}

// fieldsOf returns each field that a decode of v, a struct of type t,
// reads by name, with the fields of each struct that t holds inline. It
// leaves out the fields the decoder skips and those it cannot set.
func fieldsOf(t reflect.Type, v reflect.Value) []structField {
	var out []structField

	for i := range t.NumField() {
		field := t.Field(i)

		name, inline, skip := yamlfield.Name(field)
		if skip || !v.Field(i).CanInterface() {
			continue
		}

		if !inline {
			out = append(out, structField{owner: t, value: v.Field(i), name: name, index: []int{i}})

			continue
		}

		inner := v.Field(i)
		if inner.Kind() == reflect.Pointer {
			if inner.IsNil() {
				continue
			}

			inner = inner.Elem()
		}

		if inner.Kind() == reflect.Struct && !unmarshaler.DecodesItself(inner.Type()) {
			out = append(out, fieldsOf(inner.Type(), inner)...)
		}
	}

	return out
}

// replacedWhole reports whether a document replaces a value of type t
// whole, rather than merging a mapping into it field by field, as it does
// a struct and a pointer to one.
func replacedWhole(t reflect.Type) bool {
	base := unmarshaler.Base(t)

	return base.Kind() != reflect.Struct || unmarshaler.DecodesItself(base)
}

// valueText returns v as the JSON text of a schema, such as 20 or "SEK".
func valueText(v reflect.Value) string {
	text, err := json.Marshal(v.Interface())
	if err != nil {
		return fmt.Sprint(v.Interface())
	}

	return string(text)
}
