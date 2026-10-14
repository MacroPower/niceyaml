package manifest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// Registry holds the kinds of an app: their versions, schemas, and steps,
// and the fields that name them. [Registry.Load] reads manifests into a
// [Set], and [Registry.Verify] checks the schemas in a test.
//
// The methods of a Registry are safe for concurrent use.
//
// Create instances with [NewRegistry] or [MustNewRegistry].
type Registry struct {
	// Loads and compiles the schema of each version the first time a
	// document reaches it.
	schemas *schema.Registry
	// Each name a document can write in its kind field, the registered
	// names and the old names from KindName.
	names map[string]*kindEntry
	// The kind of each Go type.
	types   map[reflect.Type]*kindEntry
	kinds   []*kindEntry
	removed []*removal
	cfg     registryConfig
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
	// The name the version gives its kind.
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
//   - [WithNameField]
//   - [WithAppName]
//   - [WithMigrateHint]
//   - [WithDecodeOptions]
//   - [WithVersionField]
//   - [WithKindField]
//   - [WithVersionOnly]
//   - [WithIgnore]
//   - [WithRemoved]
//   - [WithCheck]
type RegistryOption func(*registryConfig)

// registryConfig holds the settings each [RegistryOption] configures.
type registryConfig struct {
	ignore          matcher.Matcher
	appName         string
	migrateHint     string
	decodeOptions   []niceyaml.DecodeOption
	kinds           []*Kind
	checks          []func(ctx context.Context, set *Set) error
	removed         []*removal
	versionField    paths.Path
	kindField       paths.Path
	nameField       paths.Path
	hasNameField    bool
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

// WithNameField is a [RegistryOption] that gives each manifest a name, read
// from the `$` path p, such as `$.metadata.name`. The path names the field
// as the current version spells it. Two manifests of one kind must not
// share a name, and [Set.Get] finds a manifest by its name.
func WithNameField(p paths.Path) RegistryOption {
	return func(c *registryConfig) {
		c.nameField, c.hasNameField = p, true
	}
}

// WithAppName is a [RegistryOption] that names the app in the messages of
// a load, as in "a newer release of cafe might read this version". Without
// it, the messages say "this program".
func WithAppName(name string) RegistryOption {
	return func(c *registryConfig) {
		c.appName = name
	}
}

// WithMigrateHint is a [RegistryOption] that sets the detail that comes
// last on each problem that a rewrite clears, such as
// `run "cafe config migrate" to update this file`.
func WithMigrateHint(hint string) RegistryOption {
	return func(c *registryConfig) {
		c.migrateHint = hint
	}
}

// WithDecodeOptions is a [RegistryOption] that adds options to each decode
// of a manifest. They apply after the rejection of unknown fields and
// before the options of [DecodeOptions], so a [niceyaml.WithDefaults] of a
// kind replaces one of the registry for the same type.
func WithDecodeOptions(opts ...niceyaml.DecodeOption) RegistryOption {
	return func(c *registryConfig) {
		c.decodeOptions = append(c.decodeOptions, opts...)
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
// documents name their version at the `$` path p and have no kind field.
// A plain config file thus gains versions.
func WithVersionOnly(p paths.Path) RegistryOption {
	return func(c *registryConfig) {
		c.versionField, c.versionOnly = p, true
	}
}

// WithIgnore is a [RegistryOption] that skips each document that m
// accepts, such as the Kubernetes manifests in a directory beside those of
// the app. A skipped document produces no object and no message.
func WithIgnore(m matcher.Matcher) RegistryOption {
	return func(c *registryConfig) {
		c.ignore = m
	}
}

// WithRemoved is a [RegistryOption] that keeps the name of a version that
// the app no longer reads, so a document that declares it fails with
// details rather than with "unknown version". The kind is the name that
// the documents of the version write. An empty version names the
// unversioned layout of the kind.
func WithRemoved(version, kind string, details ...string) RegistryOption {
	return func(c *registryConfig) {
		c.removed = append(c.removed, &removal{version: version, kind: kind, details: details})
	}
}

// WithCheck is a [RegistryOption] that adds a check across the manifests
// of a set, such as a reference from one manifest to another. A load calls
// check with the set it returns once the documents and the checks across
// the set found no error. Each error and warning that check returns joins
// the report of the load. Several checks run in the order given.
func WithCheck(check func(ctx context.Context, set *Set) error) RegistryOption {
	return func(c *registryConfig) {
		c.checks = append(c.checks, check)
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

	r := &Registry{
		schemas: schema.NewRegistry(),
		names:   make(map[string]*kindEntry),
		types:   make(map[reflect.Type]*kindEntry),
		removed: cfg.removed,
		cfg:     cfg,
	}

	b := &registryBuilder{r: r}
	b.checkFields()

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

// checkFields checks the paths of the type fields and the name field.
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

	type field struct {
		option string
		path   paths.Path
	}

	fields := []field{{"manifest.WithVersionField", cfg.versionField}}

	if cfg.versionOnly {
		fields[0].option = "manifest.WithVersionOnly"
	} else {
		fields = append(fields, field{"manifest.WithKindField", cfg.kindField})
	}

	if cfg.hasNameField {
		fields = append(fields, field{"manifest.WithNameField", cfg.nameField})
	}

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

	if other, ok := r.types[k.typ]; ok {
		b.failf("kinds %s and %s share the Go type %s", other.kind.name, k.name, k.typ)
	} else {
		r.types[k.typ] = entry
	}

	if r.cfg.versionOnly && k.cfg.claim != nil {
		b.failf("kind %s: manifest.WithVersionOnly names the only type field, so it takes no manifest.Claim", k.name)
	}

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
				"and manifest.Unversioned reads documents without type fields", k.name)
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

		if vc.written && vc.loosened {
			b.failf("kind %s: %s carries manifest.Loosened, so an earlier release rejects what a rewrite "+
				"writes there; move manifest.Written below the version whose step stopped removing the value",
				k.name, label)
		}

		entry.versions = append(entry.versions, v)
	}

	if entry.written == nil {
		entry.written = entry.versions[0]
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

// addRemovals checks each removal and ties it to the version it names.
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

// checkKinds checks the rules that span the versions of a kind, and the
// kinds that read documents without type fields.
func (b *registryBuilder) checkKinds() {
	var plain []string

	for _, entry := range b.r.kinds {
		b.checkDeprecations(entry)
		b.checkInline(entry)

		reads := entry.removedUnversioned != nil ||
			len(entry.versions) > 0 && entry.versions[0].cfg.unversioned

		if reads && entry.kind.cfg.claim == nil {
			plain = append(plain, entry.kind.name)
		}
	}

	if len(plain) > 1 {
		b.failf("kinds %s read documents without type fields, which cannot pick between them; give each but one "+
			"manifest.Claim", joinAnd(plain))
	}
}

// checkDeprecations checks where [Deprecated] marks the versions of entry.
func (b *registryBuilder) checkDeprecations(entry *kindEntry) {
	k := entry.kind
	current := len(entry.versions) - 1

	for _, v := range entry.versions {
		if !v.cfg.deprecated {
			continue
		}

		switch {
		case v.removal != nil:
			b.failf("kind %s: %s carries manifest.Deprecated, but manifest.WithRemoved names it", k.name, v.label())

		case v.index == current && k.cfg.required:
			b.failf("kind %s: manifest.Deprecated on the current version of a Required kind leaves the files "+
				"no change that clears its warning", k.name)

		case v.index != current && v.index >= entry.written.index:
			b.failf("kind %s: %s carries manifest.Deprecated, but a rewrite does not move a document off the "+
				"written version or a newer one; move manifest.Written to a newer version first", k.name, v.label())
		}
	}
}

// label returns the name of v as an author reads it in a message of
// [NewRegistry] or [Registry.Verify].
func (v *versionEntry) label() string {
	if v.cfg.unversioned {
		return "the unversioned layout"
	}

	return v.cfg.name
}

// removedLabel returns the version a removal names as an author reads it.
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

// kindOf returns the kind of Go type t, or panics with a message for the
// author of the call, as the typed accessors of a [Set] do.
func (r *Registry) kindOf(t reflect.Type) *kindEntry {
	entry, ok := r.types[t]
	if !ok {
		panic(fmt.Sprintf("manifest: %s is not the Go type of a kind", t))
	}

	return entry
}
