package manifest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/internal/nilness"
	"go.jacobcolvin.com/niceyaml/matcher"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
)

// Registry holds the kinds of an app: their versions, schemas, and steps,
// and the fields that name them. [Registry.Load] reads manifests into a
// [Set], and [Registry.Verify] checks the schemas in a test.
// [Registry.Check] returns the manifests that pass beside the problems of
// a load. [Registry.Kinds] lists the kinds as data.
//
// A nil error from [Registry.Load] or [Registry.Check] means that the
// documents pass the checks of a load. A nil error from
// [Registry.PlanMigration] means that the documents pass the checks of a
// plan. A plan skips the checks across the set, such as [Singleton]. A
// program that gates on the whole set thus calls Load or Check.
//
// [Registry.Check] and [Registry.PlanMigration] return a non-nil report
// beside a non-nil error when the documents hold a problem. The report
// then holds what passed, and its Err method returns the same error. A nil
// report means that the call did not run to the end.
//
// The methods of a Registry are safe for concurrent use.
//
// Create instances with [NewRegistry] or [MustNewRegistry].
type Registry struct {
	// The schema registry that loads and compiles the schema of each
	// version the first time that a document reaches it. It comes from
	// [WithSchemaRegistry], or the registry builds one of its own.
	schemas *schema.Registry
	// The kind of each name that a document can write in its kind field.
	// The names are the registered names and the old names from KindName.
	names map[string]*kindEntry
	// The kinds of each Go type, in the order of registration.
	types map[reflect.Type][]*kindEntry
	// What the schema of each version says about the type fields, keyed by
	// the version, as [Registry.schemaFields] returns it. Each entry is a
	// [schemaFields] value that names the validator it reads.
	fields sync.Map
	kinds  []*kindEntry
	// The kinds that can take a document without type fields. Each one
	// does not carry a [Claim], and it reads the unversioned layout or
	// [WithRemoved] names that layout. A load picks the kind of such a
	// document only when the list holds one kind. Otherwise the document
	// fails, as [docState.adopt] describes.
	plain   []*kindEntry
	removed []*removal
	cfg     registryConfig
	// Whether the messages of a load name the version field. They do
	// under [WithVersionOnly], when a kind lists a version that [Version]
	// names, and in a registry without kinds.
	versioned bool
}

// kindEntry is a [Kind] as a registry reads it.
type kindEntry struct {
	kind *Kind
	// The removal of the unversioned layout of the kind, or nil.
	removedUnversioned *removal
	// The version that a rewrite writes.
	written  *versionEntry
	byName   map[string]*versionEntry
	versions []*versionEntry
}

// versionEntry is one version of a [Kind] as a registry reads it.
type versionEntry struct {
	cfg  *versionConfig
	kind *kindEntry
	// The removal that names the version, which the kind still lists, or
	// nil.
	removal *removal
	// The name that the version gives its kind.
	kindName string
	index    int
}

// removal is a version that [WithRemoved] names.
type removal struct {
	version string
	kind    string
	details []string
}

// RegistryOption configures a [Registry].
//
// Available options:
//   - [WithKinds]
//   - [WithAppName]
//   - [WithMigrateHint]
//   - [WithDecodeOptions]
//   - [WithTypeRules]
//   - [WithMarshalers]
//   - [WithVersionField]
//   - [WithKindField]
//   - [WithVersionOnly]
//   - [WithIgnore]
//   - [WithRemoved]
//   - [WithCheck]
//   - [WithSchemaRegistry]
type RegistryOption func(*registryConfig)

// registryConfig holds the settings that each [RegistryOption] configures.
type registryConfig struct {
	appName         string
	migrateHint     string
	decodeOptions   []niceyaml.DecodeOption
	typeRules       []niceyaml.TypeRule
	marshalers      []encoder.Marshaler
	ignores         []matcher.Matcher
	kinds           []*Kind
	checks          []func(ctx context.Context, set *Set) error
	removed         []*removal
	schemas         *schema.Registry
	versionField    paths.Path
	kindField       paths.Path
	versionFieldSet bool
	kindFieldSet    bool
	versionOnly     bool
}

// WithKinds is a [RegistryOption] that adds kinds to the registry, after
// the kinds before them. A load asks the claims of the kinds in this
// order.
func WithKinds(kinds ...*Kind) RegistryOption {
	return func(c *registryConfig) {
		c.kinds = append(c.kinds, kinds...)
	}
}

// WithAppName is a [RegistryOption] that names the app in the messages of
// a load, as in "cafe reads Cafe and MenuItem". Without it, the messages
// say "this program".
func WithAppName(name string) RegistryOption {
	return func(c *registryConfig) {
		c.appName = name
	}
}

// WithMigrateHint is a [RegistryOption] that sets the detail that comes
// last on each problem that a rewrite clears, such as
// `run "cafe config migrate" to update this file`. Only the problems of the
// files of an [input.Path] input carry it, because [WriteAtomic], the
// default writer of [Plan.Apply], rewrites only those. A file that a link leads
// to outside the input directory does not carry it either.
func WithMigrateHint(hint string) RegistryOption {
	return func(c *registryConfig) {
		c.migrateHint = hint
	}
}

// WithDecodeOptions is a [RegistryOption] that adds options to each decode
// of a manifest. They apply after the option that rejects unknown fields
// and before the options of [DecodeOptions]. A [niceyaml.TypeDefaults] in a
// [niceyaml.OverrideTypeRules] among the DecodeOptions of a kind thus
// replaces one in WithDecodeOptions for the same type. Both apply over the
// rules of [WithTypeRules]. With
// niceyaml.WithUnknownFields(niceyaml.UnknownFieldsWarn) among them, the
// unknown fields of each manifest become warnings of the load. Neither
// UnknownFieldsWarn nor UnknownFieldsReject reports the type fields that
// the registry reads.
func WithDecodeOptions(opts ...niceyaml.DecodeOption) RegistryOption {
	return func(c *registryConfig) {
		c.decodeOptions = append(c.decodeOptions, opts...)
	}
}

// WithTypeRules is a [RegistryOption] that sets how the Go types of the
// kinds read YAML, such as a [net.IPNet] that a document writes as CIDR
// text:
//
//	manifest.WithTypeRules(niceyaml.CustomUnmarshaler(decodeCIDR))
//
// A load passes the rules to each [niceyaml.Source] that it builds,
// through [niceyaml.WithTypeRules]. Every decode that the load runs on a
// document applies them ahead of the rules of its Source, even a Source
// that [input.Sources] reads. The decode of a manifest, the decode inside
// a validator, and [go.jacobcolvin.com/niceyaml/edit.Node.Decode] in a
// step thus apply them. So does a decode of [Object.Current].
// [Object.Original] from a Source that Sources reads keeps the rules of
// that Source alone. The load thus parses each document of such a Source
// again, into a copy that holds both lists, and [Object.Current] holds
// that copy. A Source that the load builds itself holds the rules
// already, so its documents do not get that copy.
//
// The defaults of a [niceyaml.TypeDefaults] among the rules reach the
// decode of each migration step too. A step that decodes a value and sets
// it back then writes the defaults into the file. Give the defaults of a
// kind to [niceyaml.OverrideTypeRules] in [WithDecodeOptions] or
// [DecodeOptions] instead, so they reach the decode of a manifest alone.
//
// The rules of the Source of a document apply over them. Those come from
// [input.WithSourceOptions], or from the program that built a Source that
// [input.Sources] reads. A [niceyaml.OverrideTypeRules] in
// [WithDecodeOptions] or [DecodeOptions] applies over both in the decode
// of a manifest. Each WithTypeRules adds to the ones before it, so a
// later function for a type replaces an earlier one.
func WithTypeRules(rules ...niceyaml.TypeRule) RegistryOption {
	return func(c *registryConfig) {
		c.typeRules = append(c.typeRules, rules...)
	}
}

// WithMarshalers is a [RegistryOption] that sets how the Go types of the
// kinds write YAML, such as a [net.IPNet] that a document writes as CIDR
// text:
//
//	manifest.WithMarshalers(encoder.CustomMarshaler(func(ctx context.Context, n net.IPNet) (any, error) {
//		return n.String(), nil
//	}))
//
// A migration step passes the marshalers to the
// [go.jacobcolvin.com/niceyaml/edit.Doc] of the document through
// [go.jacobcolvin.com/niceyaml/edit.WithMarshalers]. Each
// [go.jacobcolvin.com/niceyaml/edit.Node.Set] and
// [go.jacobcolvin.com/niceyaml/edit.Node.Insert] in a step thus writes a
// value that it read through the rules of [WithTypeRules] the way the
// document holds it. Each WithMarshalers adds to the ones before it, so a
// later function for a type replaces an earlier one.
func WithMarshalers(ms ...encoder.Marshaler) RegistryOption {
	return func(c *registryConfig) {
		c.marshalers = append(c.marshalers, ms...)
	}
}

// WithVersionField is a [RegistryOption] that reads the version of each
// document from the `$` path p in place of `$.apiVersion`.
func WithVersionField(p paths.Path) RegistryOption {
	return func(c *registryConfig) {
		c.versionField, c.versionFieldSet = p, true
	}
}

// WithKindField is a [RegistryOption] that reads the kind of each document
// from the `$` path p in place of `$.kind`.
func WithKindField(p paths.Path) RegistryOption {
	return func(c *registryConfig) {
		c.kindField, c.kindFieldSet = p, true
	}
}

// WithVersionOnly is a [RegistryOption] for a registry of one kind, whose
// documents name their version at the `$` path p and do not have a kind
// field. A plain config file thus gains versions.
func WithVersionOnly(p paths.Path) RegistryOption {
	return func(c *registryConfig) {
		c.versionField, c.versionOnly = p, true
	}
}

// WithIgnore is a [RegistryOption] that skips each document that m
// accepts, such as the Kubernetes manifests in a directory beside those of
// the app. A skipped document does not produce an object or a message.
//
// Each WithIgnore adds a matcher, and a load skips each document that one
// of the matchers accepts. The load asks the matchers in the order given,
// and stops at the first that accepts the document or returns an error. A
// matcher that returns an error fails the document with that error.
// The load adds the warnings of each match to the warnings of a document
// that it keeps. It drops them for a document that it skips.
// [NewRegistry] rejects a nil matcher.
func WithIgnore(m matcher.Matcher) RegistryOption {
	return func(c *registryConfig) {
		c.ignores = append(c.ignores, m)
	}
}

// WithRemoved is a [RegistryOption] that keeps the name of a version that
// the app has removed. A document that declares the version thus fails
// with details rather than with "unknown version". The kind is the name
// that the documents of the version write. An empty version names the
// unversioned layout of the kind, which a document without a version field
// declares. The error about that layout sits at the kind field of a
// document that writes the field.
//
// The registry does not need to hold the kind. A document that names a
// kind that the app has removed, with a version that WithRemoved names for
// that kind, fails with the details rather than with "unknown kind". When
// the registry holds kinds and none of them lists a [Version], an empty
// version retires a kind of a format without versions. The error then
// reads `removed kind "legacy"`.
func WithRemoved(version, kind string, details ...string) RegistryOption {
	return func(c *registryConfig) {
		c.removed = append(c.removed, &removal{version: version, kind: kind, details: details})
	}
}

// WithCheck is a [RegistryOption] that adds a check across the manifests
// of a set, such as a reference from one manifest to another. A load calls
// check with the set of its manifests when it does not find an error in
// the files, the documents, or the checks of names and counts.
// [Registry.Load] returns that set when the load does not find a later
// error. Each error and warning that check returns joins the report of the
// load. An error that check binds in a document, such as through
// [Object.Bind] or [Object.Current], leaves that manifest out of
// [Checked.Set]. Several checks
// run in the order given, and each one runs even when an earlier one
// returns an error. [NewRegistry] rejects a nil function.
func WithCheck(check func(ctx context.Context, set *Set) error) RegistryOption {
	return func(c *registryConfig) {
		c.checks = append(c.checks, check)
	}
}

// WithSchemaRegistry is a [RegistryOption] that loads and compiles the
// schema of each version through r, in place of a registry that
// [NewRegistry] builds with the defaults of [schema.NewRegistry]. The
// schemas thus load with the HTTP client, the access rules, the limits,
// and the compile options of r, and they share its cache with every other
// user of r. A program that watches a schema file for changes calls
// [schema.Registry.Invalidate] on r, and the next load reads the file
// again. A nil r keeps the registry that NewRegistry builds.
//
// A load asks r only for the schema that the [Schema] option of a
// version names, with [schema.Registry.Schema]. The resolvers of r do
// not take part in a load.
func WithSchemaRegistry(r *schema.Registry) RegistryOption {
	return func(c *registryConfig) {
		c.schemas = r
	}
}

// NewRegistry creates a new [Registry] from opts. It returns an error for
// options that contradict each other, such as a kind that lists a version
// twice, two kinds of one name, or [Written] on two versions of one kind.
// The error names the kind and the option, for the author of the registry.
func NewRegistry(opts ...RegistryOption) (*Registry, error) {
	cfg := registryConfig{
		versionField: paths.Doc().Child("apiVersion"),
		kindField:    paths.Doc().Child("kind"),
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	schemas := cfg.schemas
	if schemas == nil {
		schemas = schema.NewRegistry()
	}

	r := &Registry{
		schemas: schemas,
		names:   make(map[string]*kindEntry),
		types:   make(map[reflect.Type][]*kindEntry),
		removed: cfg.removed,
		cfg:     cfg,
	}

	b := &registryBuilder{r: r}
	b.checkFields()

	b.checkFuncs()

	for _, k := range cfg.kinds {
		b.addKind(k)
	}

	b.addRemovals()
	b.checkKinds()

	err := errors.Join(b.errs...)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}

	return r, nil
}

// MustNewRegistry is like [NewRegistry] but panics on options that
// contradict each other. Use it for a registry that a package declares, and
// call [NewRegistry] in a test to catch the mistake.
func MustNewRegistry(opts ...RegistryOption) *Registry {
	r, err := NewRegistry(opts...)
	if err != nil {
		panic(err)
	}

	return r
}

// registryBuilder checks the options of a [Registry] as [NewRegistry]
// builds it.
type registryBuilder struct {
	r    *Registry
	errs []error
}

// failf adds the problem that format describes.
func (b *registryBuilder) failf(format string, args ...any) {
	b.errs = append(b.errs, fmt.Errorf(format, args...))
}

// checkFields checks the paths of the type fields.
func (b *registryBuilder) checkFields() {
	cfg := &b.r.cfg

	if cfg.versionOnly {
		if cfg.versionFieldSet {
			b.failf("manifest.WithVersionOnly names the only type field, so it takes no manifest.WithVersionField")
		}

		if cfg.kindFieldSet {
			b.failf("manifest.WithVersionOnly names the only type field, so it takes no manifest.WithKindField")
		}

		if len(cfg.kinds) > 1 {
			b.failf("manifest.WithVersionOnly reads documents without a kind field, "+
				"so the registry holds one kind, not %d", len(cfg.kinds))
		}
	}

	fields := cfg.typeFields()

	for i, f := range fields {
		if !plainPath(f.path) {
			b.failf("%s takes a $ path of names and indexes, not %s", f.option, f.path)
		}

		for _, g := range fields[:i] {
			if f.path.Equal(g.path) {
				b.failf("%s and %s name the same path %s", g.option, f.option, f.path)
			}
		}
	}
}

// optionField is the path of a field that an option names, as a message of
// [NewRegistry] gives it.
type optionField struct {
	option string
	path   paths.Path
}

// typeFields returns the paths of the type fields, each with the option
// that names it.
func (c *registryConfig) typeFields() []optionField {
	fields := []optionField{{"manifest.WithVersionField", c.versionField}}

	if c.versionOnly {
		fields[0].option = "manifest.WithVersionOnly"
	} else {
		fields = append(fields, optionField{"manifest.WithKindField", c.kindField})
	}

	return fields
}

// checkNameField checks the path of the [NameField] of k.
func (b *registryBuilder) checkNameField(k *Kind) {
	if !k.cfg.hasNameField {
		return
	}

	p := k.cfg.nameField
	if !plainPath(p) {
		b.failf("kind %s: manifest.NameField takes a $ path of names and indexes, not %s", k.name, p)
	}

	for _, f := range b.r.cfg.typeFields() {
		if p.Equal(f.path) {
			b.failf("kind %s: %s and manifest.NameField name the same path %s", k.name, f.option, p)
		}
	}
}

// checkFuncs checks the matchers of [WithIgnore] and the functions of
// [WithCheck].
func (b *registryBuilder) checkFuncs() {
	for _, m := range b.r.cfg.ignores {
		if nilness.IsNil(m) {
			b.failf("manifest.WithIgnore got a nil matcher")
		}
	}

	for _, check := range b.r.cfg.checks {
		if check == nil {
			b.failf("manifest.WithCheck got a nil function")
		}
	}
}

// plainPath reports whether p starts at `$` and holds names and indexes
// alone, as the fields that a load reads before it decodes need.
func plainPath(p paths.Path) bool {
	if !p.IsAbsolute() || p.IsRoot() {
		return false
	}

	for sel := range p.Selectors() {
		if sel.Kind != paths.SelectorChild && sel.Kind != paths.SelectorIndex {
			return false
		}
	}

	return true
}

// addKind adds k to the registry and checks its versions.
func (b *registryBuilder) addKind(k *Kind) {
	r := b.r

	if k == nil {
		b.failf("manifest.WithKinds got a nil kind")

		return
	}

	if k.name == "" {
		b.failf("a kind of Go type %s has an empty name", k.typ)

		return
	}

	entry := &kindEntry{kind: k, byName: make(map[string]*versionEntry)}

	if other, ok := r.names[k.name]; ok {
		b.failf("kinds %s and %s both answer to the name %q", other.kind.name, k.name, k.name)
	} else {
		r.names[k.name] = entry
	}

	r.types[k.typ] = append(r.types[k.typ], entry)

	if r.cfg.versionOnly && len(k.cfg.claims) > 0 {
		b.failf("kind %s: manifest.WithVersionOnly names the only type field, so it takes no manifest.Claim", k.name)
	}

	for _, m := range k.cfg.claims {
		if nilness.IsNil(m) {
			b.failf("kind %s: manifest.Claim got a nil matcher", k.name)
		}
	}

	b.checkNameField(k)
	b.addVersions(entry)

	r.kinds = append(r.kinds, entry)
}

// addVersions adds the versions of entry and checks each.
func (b *registryBuilder) addVersions(entry *kindEntry) {
	k := entry.kind

	if len(k.cfg.versions) == 0 {
		b.failf("kind %s has no version", k.name)

		return
	}

	current := len(k.cfg.versions) - 1

	for i, vc := range k.cfg.versions {
		v := &versionEntry{cfg: vc, kind: entry, index: i, kindName: k.name}
		label := v.label()

		switch {
		case vc.unversioned && i > 0:
			b.failf("kind %s: manifest.Unversioned comes first among the versions", k.name)

		case !vc.unversioned && vc.name == "":
			b.failf("kind %s: manifest.Version needs a name, "+
				"and manifest.Unversioned reads documents without a version field", k.name)
		}

		if vc.hasKindName {
			switch {
			case vc.unversioned:
				b.failf("kind %s: the unversioned layout takes no manifest.KindName", k.name)
			case b.r.cfg.versionOnly:
				b.failf("kind %s: manifest.WithVersionOnly names the only type field, so %s takes no manifest.KindName",
					k.name, label)

			case i == current:
				b.failf("kind %s: the current version %s takes no manifest.KindName, "+
					"because its name is the name of the kind", k.name, label)

			case vc.kindName == "":
				b.failf("kind %s: %s has an empty manifest.KindName", k.name, label)
			default:
				v.kindName = vc.kindName
				b.claimName(entry, vc.kindName, label)
			}
		}

		b.checkSteps(k, vc, i, label)

		if vc.hasSchema && vc.schema.Key() == "" && vc.schema.Schema() == nil {
			b.failf("kind %s: %s has a schema.Ref that names no schema", k.name, label)
		}

		if vc.hasTypeFields && !vc.hasSchema {
			b.failf("kind %s: %s takes no manifest.SchemaTypeFields without manifest.Schema", k.name, label)
		}

		if _, ok := entry.byName[vc.name]; ok && !vc.unversioned {
			b.failf("kind %s lists %s twice", k.name, label)
		}

		if !vc.unversioned {
			entry.byName[vc.name] = v
		}

		if vc.written {
			if entry.written != nil {
				b.failf("%s carries manifest.Written on %s and on %s", k.name, entry.written.label(), label)
			}

			entry.written = v
		}

		entry.versions = append(entry.versions, v)
	}

	if entry.written == nil {
		entry.written = entry.versions[len(entry.versions)-1]
	}
}

// checkSteps checks the steps of vc, the version of k at index i.
func (b *registryBuilder) checkSteps(k *Kind, vc *versionConfig, i int, label string) {
	switch {
	case len(vc.steps) == 0:
		return
	case vc.unversioned:
		b.failf("kind %s: the unversioned layout takes no manifest.Migrate, because no version comes before it", k.name)
	case i == 0:
		b.failf("kind %s: %s is the oldest version, so it takes no manifest.Migrate", k.name, label)
	}

	for j, s := range vc.steps {
		switch {
		case s.fn == nil:
			b.failf("kind %s: step %d of %s has a nil function", k.name, j+1, label)
		case !s.path.IsAbsolute():
			b.failf("kind %s: step %d of %s takes a $ path, not %s", k.name, j+1, label, s.path)
		case holdsKey(s.path):
			b.failf("kind %s: step %d of %s takes a path without ~, not %s", k.name, j+1, label, s.path)
		}

		// The load protects the type fields. A step whose path of names and
		// indexes names a type field, or a place inside one, thus cannot
		// edit the place of its calls. A path that selects more than one
		// value passes over the type fields.
		if !plainPath(s.path) {
			continue
		}

		for _, f := range b.r.cfg.typeFields() {
			if _, ok := s.path.CutPrefix(f.path); ok {
				b.failf("kind %s: step %d of %s takes %s, but %s protects the type field %s from every step",
					k.name, j+1, label, s.path, f.option, f.path)
			}
		}
	}
}

// holdsKey reports whether p holds a `~` selector.
func holdsKey(p paths.Path) bool {
	for sel := range p.Selectors() {
		if sel.Kind == paths.SelectorKey {
			return true
		}
	}

	return false
}

// claimName records that the version of entry with label gives the old
// name of its kind.
func (b *registryBuilder) claimName(entry *kindEntry, name, label string) {
	other, ok := b.r.names[name]

	switch {
	case !ok:
		b.r.names[name] = entry
	case other != entry && other.kind.name == name:
		b.failf("kind %s: %s gives the name %q, which kind %s uses", entry.kind.name, label, name, other.kind.name)
	case other != entry:
		b.failf("kind %s: %s gives the name %q, which kind %s uses as an old name", entry.kind.name, label, name,
			other.kind.name)
	}
}

// addRemovals checks each removal and ties it to the version that it names.
func (b *registryBuilder) addRemovals() {
	seen := make(map[[2]string]bool)

	for _, rm := range b.r.removed {
		key := [2]string{rm.version, rm.kind}
		if seen[key] {
			b.failf("manifest.WithRemoved names %s of %s twice", removedLabel(rm.version), rm.kind)
		}

		seen[key] = true

		entry := b.r.names[rm.kind]
		if entry == nil {
			continue
		}

		if rm.version == "" {
			b.tieUnversionedRemoval(entry, rm)

			continue
		}

		v := entry.byName[rm.version]
		if v == nil {
			continue
		}

		if v.kindName != rm.kind {
			b.failf("manifest.WithRemoved names %s of %s, but that version calls the kind %q", rm.version, rm.kind,
				v.kindName)
		}

		if v.index >= entry.written.index {
			b.failf("manifest.WithRemoved names %s of %s, which is the written version of %s or newer, "+
				"so a rewrite leaves documents there", rm.version, rm.kind, entry.kind.name)
		}

		v.removal = rm
	}
}

// tieUnversionedRemoval ties rm, a removal of the unversioned layout of
// the kind of entry, to that kind.
func (b *registryBuilder) tieUnversionedRemoval(entry *kindEntry, rm *removal) {
	entry.removedUnversioned = rm

	if first := entry.versions; len(first) > 0 && first[0].cfg.unversioned {
		v := first[0]
		if v.index >= entry.written.index {
			b.failf("manifest.WithRemoved names the unversioned layout of %s, which is its written version, "+
				"so a rewrite leaves documents there", entry.kind.name)
		}

		v.removal = rm
	}
}

// checkKinds checks the rules that span the versions of a kind. It also
// records the kinds that can take a document without type fields, and
// whether the messages of a load name the version field.
func (b *registryBuilder) checkKinds() {
	r := b.r
	r.versioned = r.cfg.versionOnly || len(r.kinds) == 0

	for _, entry := range r.kinds {
		b.checkWritten(entry)
		b.checkLoosened(entry)
		b.checkDeprecations(entry)

		reads := entry.removedUnversioned != nil ||
			len(entry.versions) > 0 && entry.versions[0].cfg.unversioned

		if reads && len(entry.kind.cfg.claims) == 0 {
			r.plain = append(r.plain, entry)
		}

		if len(entry.byName) > 0 {
			r.versioned = true
		}
	}
}

// checkWritten checks where [Written] marks the versions of entry.
func (b *registryBuilder) checkWritten(entry *kindEntry) {
	if len(entry.versions) == 0 {
		return
	}

	k := entry.kind
	current := entry.versions[len(entry.versions)-1]

	for _, v := range entry.versions {
		switch {
		case !v.cfg.written:
			continue

		case v == current:
			b.failf("kind %s: %s is the current version, which a rewrite writes without manifest.Written; %s",
				k.name, v.label(), writtenAdvice(entry, v))

		case v.cfg.loosened:
			b.failf("kind %s: %s carries manifest.Loosened, so an earlier release rejects what a rewrite "+
				"writes there; %s", k.name, v.label(), writtenAdvice(entry, v))
		}
	}
}

// checkLoosened checks [Loosened] on the current version of entry, which
// a rewrite writes when no version of entry carries [Written].
func (b *registryBuilder) checkLoosened(entry *kindEntry) {
	w := entry.written
	if w == nil || w.cfg.written || !w.cfg.loosened || w.index == 0 {
		return
	}

	b.failf("kind %s: %s carries manifest.Loosened, so an earlier release rejects what a rewrite writes there; %s",
		entry.kind.name, w.label(), loosenedAdvice(w, entry.versions[:w.index]))
}

// loosenedAdvice returns the change that clears the problem of [Loosened]
// on w, the current version of a kind without [Written]. The versions of
// older come before w.
func loosenedAdvice(w *versionEntry, older []*versionEntry) string {
	if vs := writableVersions(older); len(vs) > 0 {
		return "mark " + labelList(vs) + " with manifest.Written"
	}

	var kept []*versionEntry

	for _, v := range older {
		if v.removal == nil {
			kept = append(kept, v)
		}
	}

	if len(kept) == 0 {
		// WithRemoved names every older version. Without them, the kind
		// lists the current version alone. Loosened fits there, but a step
		// does not, because the oldest version takes no step.
		if len(w.cfg.steps) > 0 {
			return "stop listing the versions that manifest.WithRemoved names and drop the manifest.Migrate " +
				"steps of " + w.label() + ", or drop manifest.Loosened"
		}

		return "stop listing the versions that manifest.WithRemoved names, or drop manifest.Loosened"
	}

	keptVs := writableVersions(kept)
	if len(keptVs) == 0 {
		return "drop manifest.Loosened"
	}

	// The removals alone keep Written off the older versions. Let x be
	// the newest kept version that Written can mark. The mark fits on x
	// when the kind stops listing each removed version after x, and the
	// versions before x stay listed. A load of a document older than a
	// removed version runs the steps of that version and checks its
	// schema. The advice offers the unlisting only when no removed
	// version after x carries steps or a schema.
	x := keptVs[len(keptVs)-1]

	var unlist []string

	for _, v := range older[x.index+1:] {
		switch {
		case v.removal == nil:
			continue
		case len(v.cfg.steps) > 0 || v.cfg.hasSchema:
			return "drop manifest.Loosened"
		}

		unlist = append(unlist, v.label())
	}

	return fmt.Sprintf("stop listing %s and mark %s with manifest.Written, or drop manifest.Loosened",
		joinAnd(unlist), x.label())
}

// checkDeprecations checks where [Deprecated] marks the versions of entry,
// and whether [DeprecatedKind] fits the kind.
func (b *registryBuilder) checkDeprecations(entry *kindEntry) {
	k := entry.kind
	current := len(entry.versions) - 1

	if k.cfg.deprecated && k.cfg.required {
		b.failf("kind %s: manifest.DeprecatedKind on a Required kind leaves the files no change that clears "+
			"its warning", k.name)
	}

	for _, v := range entry.versions {
		if !v.cfg.deprecated {
			continue
		}

		switch {
		case v.removal != nil:
			b.failf("kind %s: %s carries manifest.Deprecated, but manifest.WithRemoved names it", k.name, v.label())

		case v.index == current:
			b.failf("kind %s: %s carries manifest.Deprecated, but it is the current version and no rewrite "+
				"clears the warning; mark the kind with manifest.DeprecatedKind", k.name, v.label())

		case v.index >= entry.written.index:
			b.failf("kind %s: %s carries manifest.Deprecated, but a rewrite does not move a document off the "+
				"written version or a newer one; %s", k.name, v.label(), deprecatedAdvice(entry, v))
		}
	}
}

// deprecatedAdvice returns the change that clears the problem of v, a
// deprecated version of entry. The version v is older than the current
// version, and it is the version that [Written] marks or a newer one.
func deprecatedAdvice(entry *kindEntry, v *versionEntry) string {
	advice := writtenAdvice(entry, entry.written)

	if vs, drop := writtenFixes(entry); !drop && len(vs) == 0 {
		return fmt.Sprintf("drop manifest.Deprecated from %s, or %s", v.label(), advice)
	}

	return advice
}

// writtenAdvice returns the change that clears a problem of where [Written]
// sits on w, a version of entry. When neither dropping the mark nor moving
// it clears the problem, the change also drops [Loosened].
func writtenAdvice(entry *kindEntry, w *versionEntry) string {
	vs, drop := writtenFixes(entry)

	switch current := entry.versions[len(entry.versions)-1]; {
	case drop && len(vs) > 0:
		return "drop manifest.Written, or move it to " + labelList(vs)
	case drop:
		return "drop manifest.Written"
	case len(vs) > 0:
		return "move manifest.Written to " + labelList(vs)
	case w == current:
		return "drop manifest.Written and manifest.Loosened from " + w.label()
	default:
		return fmt.Sprintf("drop manifest.Written from %s and manifest.Loosened from %s", w.label(),
			current.label())
	}
}

// writtenFixes returns the versions of entry that [Written] can move to, and
// whether the kind can drop the mark. Without the mark, a rewrite writes the
// current version, which checkLoosened rejects when it carries [Loosened]
// and follows an older version.
func writtenFixes(entry *kindEntry) ([]*versionEntry, bool) {
	current := entry.versions[len(entry.versions)-1]

	return writableVersions(entry.versions[:current.index]), !current.cfg.loosened || current.index == 0
}

// writableVersions returns the versions of older that [Written] can mark,
// where older holds the versions of a kind before its current one. Each
// such version does not carry [Loosened], and it is newer than every
// version that [WithRemoved] names and every version that carries
// [Deprecated].
func writableVersions(older []*versionEntry) []*versionEntry {
	var vs []*versionEntry

	for _, v := range older {
		switch {
		case v.removal != nil || v.cfg.deprecated:
			vs = nil
		case !v.cfg.loosened:
			vs = append(vs, v)
		}
	}

	return vs
}

// labelList returns the labels of vs as a list in prose, as in "v1, v2,
// or v3".
func labelList(vs []*versionEntry) string {
	labels := make([]string, len(vs))
	for i, v := range vs {
		labels[i] = v.label()
	}

	return joinList(labels, "or")
}

// label returns the name of v as an author reads it in a message of
// [NewRegistry] or [Registry.Verify].
func (v *versionEntry) label() string {
	if v.cfg.unversioned {
		return "the unversioned layout"
	}

	return v.cfg.name
}

// removedLabel returns the version that a removal names as an author reads
// it.
func removedLabel(version string) string {
	if version == "" {
		return "the unversioned layout"
	}

	return version
}

// joinAnd returns items as a list in prose, as in "A, B, and C".
func joinAnd(items []string) string {
	return joinList(items, "and")
}

// joinList returns items as a list in prose whose last item follows conj,
// as in "A, B, or C" for "or".
func joinList(items []string, conj string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " " + conj + " " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", " + conj + " " + items[len(items)-1]
	}
}

// kindsOf returns the kinds of Go type t, or panics with a message for the
// author of the call, as the typed accessors of a [Set] do.
func (r *Registry) kindsOf(t reflect.Type) []*kindEntry {
	entries := r.types[t]
	if len(entries) == 0 {
		panic(fmt.Sprintf("manifest: %s is not the Go type of a kind", t))
	}

	return entries
}

// kindOf returns the one kind of Go type t for the accessor method, or
// panics with a message for the author of the call when t is not the Go
// type of a kind or is the Go type of several.
func (r *Registry) kindOf(method string, t reflect.Type) *kindEntry {
	entries := r.kindsOf(t)
	if len(entries) > 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.kind.name
		}

		panic(fmt.Sprintf("manifest: %s needs the Go type of one kind, and %s is the type of %s",
			method, t, joinAnd(names)))
	}

	return entries[0]
}
