package manifest

import (
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"

	"go.jacobcolvin.com/niceyaml"
)

// Set holds the manifests that one load returns, in load order. Its typed
// accessors return each manifest as an [Object] of the Go type of its
// kind.
//
// The methods of a Set are safe for concurrent use.
//
// Receive instances from [Registry.Load] or [Checked.Set].
type Set struct {
	reg      *Registry
	warnings error
	objects  []setObject
	sources  []*niceyaml.Source
}

// setObject is one manifest of a [Set], with its kind.
type setObject struct {
	entry *kindEntry
	obj   Object[any]
}

// Object is one manifest of a [Set], where T is the Go type of its kind.
//
// A load decodes each document in the layout of the current version.
// Original holds the document of the file, and Current holds the
// document that Value decoded from. For a file that declares an older
// version, the steps of the later versions edit a copy of the document,
// and the file stays as it is. Current then holds the copy. For a file
// that declares the current version, Current is Original, except for a
// Source that [input.Sources] reads under [WithUnmarshalers]. Value, Name,
// Current, and Bind follow the layout of the current version, whatever
// version the file declares. A `$` path of that version thus resolves in
// Current and in Bind. Only Original follows the layout of Version.
//
// Current, Original, and Bind keep the [niceyaml.Source] of the file in
// memory. Current and Bind keep a second copy of the document only for a
// migrated document, and for a Source that [input.Sources] reads under
// [WithUnmarshalers]. For a migrated document, they keep the edits of the
// migration too. They read the edits to bind each error at the line that
// the user wrote. Copy Value to keep the value alone.
//
// Receive instances from [Set.One], [Set.All], [Set.Get], or
// [Set.Objects].
type Object[T any] struct {
	// Value holds the decoded value of the current version.
	Value T
	// Current holds the document in the layout of the current version,
	// which is the document that Value decoded from. A function of
	// [WithCheck] reads it to decode another Go type, to run a
	// [niceyaml.Validator], or to resolve a path of the current version.
	// Its decodes apply the unmarshalers of [WithUnmarshalers].
	//
	// Current is a copy of the document in a Source of its own, as
	// [niceyaml.NewSourceFromDerived] builds one, in two cases. The first
	// is a migrated document. The second is a document of a Source that
	// [input.Sources] reads under [WithUnmarshalers], because only the
	// copy holds the unmarshalers of the registry. Current is Original for
	// every other document. The copy holds the lines of this document
	// alone. Each error that binds through Current lands on the line that
	// the user wrote in the file. Such errors include those of
	// [niceyaml.Node.Bind], [niceyaml.Node.Validate],
	// [niceyaml.Node.DecodeAt], and [niceyaml.Node.At].
	//
	// Each position that a method of a copy takes or returns is a position
	// in the copy, not in the file. Such methods include
	// [niceyaml.Node.Range], [niceyaml.Node.Span], [niceyaml.Node.View],
	// and [niceyaml.Node.DocumentIndex]. The text of the Source of a copy
	// is the text of the copy. For a position in the file, bind an error
	// through Current and read [niceyaml.ErrorTree.Range] of the binding,
	// or read Original.
	Current *niceyaml.Node
	// Original holds the document as the file writes it, in the layout of
	// Version. Its Source is the Source of the file. Original thus gives
	// the name and the text of the file, and the index of the document in
	// the file. A path of the current version might not resolve in
	// Original. From a Source that [input.Sources] reads, Original keeps
	// the unmarshalers of that Source alone.
	Original *niceyaml.Node
	// The function that binds an error with the paths of the current
	// version.
	bind func(error) error
	// Name holds the name, from the field of the [NameField] of the kind.
	// It is empty for a kind without NameField.
	Name string
	// Kind holds the name of the kind, as [NewKind] gives it, even for a
	// document that declares an old name from [KindName].
	Kind string
	// Version holds the version that the file declares, and is empty for
	// an unversioned document.
	Version string
	// UpToDate is true when the file declares the written version of its
	// kind or a newer one. A rewrite then leaves the document as it is.
	UpToDate bool
}

// Bind binds err in the document of o, with each path in the terms of the
// current version, whatever version the file declares. The error lands on
// the line that the user wrote, even when the document migrated. An error
// without a location names the file, and the document in a file of
// several, as [niceyaml.Node.Bind] does.
//
// Bind binds as o.Current.Bind does. It also adds a detail to each problem
// at a field that the file leaves out, which says that the field holds its
// default. An Object that a test builds itself binds without that detail,
// and without Current it returns err as it is.
func (o Object[T]) Bind(err error) error {
	switch {
	case o.bind != nil:
		return o.bind(err)
	case o.Current != nil:
		return o.Current.Bind(err) //nolint:wrapcheck // The binding is the error of the caller.
	default:
		return err
	}
}

// newSet returns the set of the documents that passed their own checks
// and that reject did not mark.
func (l *loader) newSet() *Set {
	s := &Set{reg: l.reg}

	for _, f := range l.files {
		if f.src != nil {
			s.sources = append(s.sources, f.src)
		}
	}

	for _, d := range l.docs {
		if !d.passed || d.rejected {
			continue
		}

		v := d.route.version
		entry := d.route.kind

		obj := Object[any]{
			Value:    d.value,
			Current:  d.work,
			Original: d.doc,
			bind:     d.binder().bind,
			Name:     d.name,
			Kind:     entry.kind.name,
			UpToDate: v.index >= entry.written.index,
		}

		if !v.cfg.unversioned {
			obj.Version = v.cfg.name
		}

		s.objects = append(s.objects, setObject{entry: entry, obj: obj})
	}

	return s
}

// One returns the manifest of the kind whose Go type is T, and false when
// the set does not hold one. The kind carries [Singleton], so the set
// holds one at most.
//
// One panics when T is not the Go type of exactly one kind of the
// registry, or when the kind does not carry [Singleton]. The first test
// that runs the call thus fails.
func (s *Set) One[T any]() (Object[T], bool) {
	entry := s.reg.kindOf("One", reflect.TypeFor[T]())
	if !entry.kind.cfg.singleton {
		panic(fmt.Sprintf("manifest: One needs a kind with manifest.Singleton, and %s has none", entry.kind.name))
	}

	for _, o := range s.objects {
		if o.entry == entry {
			return typed[T](o.obj), true
		}
	}

	return Object[T]{}, false
}

// All yields the manifests of every kind whose Go type is T, in load
// order. When several kinds share T, the sequence mixes their manifests,
// and [Object.Kind] tells them apart.
//
// All panics when T is not the Go type of a kind of the registry. It
// panics at the call, before a loop ranges over the sequence.
func (s *Set) All[T any]() iter.Seq[Object[T]] {
	entries := s.reg.kindsOf(reflect.TypeFor[T]())

	return func(yield func(Object[T]) bool) {
		for _, o := range s.objects {
			if slices.Contains(entries, o.entry) && !yield(typed[T](o.obj)) {
				return
			}
		}
	}
}

// Get returns the manifest of the kind whose Go type is T with the given
// name, and false when the set does not hold one.
//
// Get panics when T is not the Go type of exactly one kind of the
// registry, or when the kind has no [NameField].
func (s *Set) Get[T any](name string) (Object[T], bool) {
	entry := s.reg.kindOf("Get", reflect.TypeFor[T]())
	if !entry.kind.cfg.hasNameField {
		panic(fmt.Sprintf("manifest: Get needs a kind with manifest.NameField, and %s has none", entry.kind.name))
	}

	for _, o := range s.objects {
		if o.entry == entry && o.obj.Name == name {
			return typed[T](o.obj), true
		}
	}

	return Object[T]{}, false
}

// Objects yields every manifest of every kind in load order, for a tool
// that does not know the Go types. The Value of each holds a value of the
// Go type of its kind.
func (s *Set) Objects() iter.Seq[Object[any]] {
	return func(yield func(Object[any]) bool) {
		for _, o := range s.objects {
			if !yield(o.obj) {
				return
			}
		}
	}
}

// Warnings returns the warnings of the load as a [*LoadError], or nil when
// the load did not find a warning. Warnings returns nil for the set of a
// [Checked] whose [Checked.Err] returns an error, because that error holds
// the warnings.
func (s *Set) Warnings() error {
	return s.warnings
}

// Sources returns the Source of each file that the load read, in load
// order, including a file without manifests.
func (s *Set) Sources() []*niceyaml.Source {
	return s.sources
}

// typed returns o with its value as a T.
func typed[T any](o Object[any]) Object[T] {
	v, _ := o.Value.(T) //nolint:errcheck // The kind of the object decodes into T.

	return Object[T]{
		Value:    v,
		Current:  o.Current,
		Original: o.Original,
		bind:     o.bind,
		Name:     o.Name,
		Kind:     o.Kind,
		Version:  o.Version,
		UpToDate: o.UpToDate,
	}
}

// checkNames reports each manifest whose name an earlier manifest of its
// kind took, in load order. Every document that reached the layout of the
// current version with a name takes part, even one that a later check
// failed. Only the documents of a kind with a [NameField] hold a name.
func (l *loader) checkNames() {
	reg := l.reg
	seen := make(map[*kindEntry]map[string]*docState)

	for _, d := range l.docs {
		if !d.hasName || d.route.kind == nil {
			continue
		}

		entry := d.route.kind
		namePath := entry.kind.cfg.nameField

		names := seen[entry]
		if names == nil {
			names = make(map[string]*docState)
			seen[entry] = names
		}

		first, ok := names[d.name]
		if !ok {
			names[d.name] = d

			continue
		}

		details := []error{first.work.Bind(niceyaml.NewError("first defined here",
			niceyaml.AtPath(namePath)))}
		details = append(details, reg.oldName(entry, d.route.wroteKind)...)

		err := d.work.Bind(newProblem(ErrDuplicateName, fmt.Sprintf("duplicate %s %q", entry.kind.name, d.name),
			niceyaml.AtPath(namePath), niceyaml.WithDetails(details...)))

		l.add(&row{err: err, doc: d, file: d.file, index: d.index})
	}
}

// checkCounts reports each kind that [Required] marks and the set lacks,
// and each manifest of a kind that [Singleton] marks after the first.
func (l *loader) checkCounts() {
	reg := l.reg
	counted := make(map[*kindEntry][]*docState)

	for _, d := range l.docs {
		if d.route.counted != nil {
			counted[d.route.counted] = append(counted[d.route.counted], d)
		}
	}

	for _, entry := range reg.kinds {
		docs := counted[entry]

		if entry.kind.cfg.required && len(docs) == 0 {
			err := l.named.Bind(newProblem(ErrMissingManifest, "missing "+entry.kind.name,
				niceyaml.WithDetails(l.missingDetails(entry)...)))

			l.add(&row{err: err, file: len(l.files), index: -1, unplaced: true})
		}

		if !entry.kind.cfg.singleton || len(docs) < 2 {
			continue
		}

		first := docs[0]

		for _, d := range docs[1:] {
			details := []error{first.doc.Bind(niceyaml.NewError("first defined here", first.countAt()))}
			details = append(details, reg.oldName(entry, d.route.wroteKind)...)

			err := d.doc.Bind(newProblem(ErrExtraManifest, "more than one "+entry.kind.name,
				d.countAt(), niceyaml.WithDetails(details...)))

			l.add(&row{err: err, doc: d, file: d.file, index: d.index})
		}
	}
}

// countAt returns where an error about the count of the kind of d binds.
// The place is its kind field, its version field when it does not have a
// kind field, or its start when it does not have a type field.
func (d *docState) countAt() niceyaml.ErrorOption {
	reg := d.l.reg

	switch {
	case d.route.kindTxt.found:
		return niceyaml.AtPath(reg.cfg.kindField)
	case d.route.versionTxt.found:
		return niceyaml.AtPath(reg.cfg.versionField)
	default:
		return startOf(d.doc)
	}
}

// missingDetails returns the details of the problem of a kind that the
// set lacks. The first detail names the document that the user should add.
// Each other detail tells why a directory input did not select a file.
func (l *loader) missingDetails(entry *kindEntry) []error {
	reg := l.reg
	written := entry.written

	vField, kField := fieldName(reg.cfg.versionField), fieldName(reg.cfg.kindField)

	var add error

	// A document of the unversioned layout leaves out the version field.
	// The hint asks for the kind field too, unless a load picks the kind
	// without it. In a registry whose messages do not name the version
	// field, the hint asks for the kind field alone.
	switch {
	case written.cfg.unversioned && reg.versioned && reg.adopts(entry):
		add = fmt.Errorf("add %s document without %s", article(entry.kind.name), reg.typeFieldsText())
	case written.cfg.unversioned && reg.versioned:
		add = fmt.Errorf("add a document with %s: %s and without %s", kField, written.kindName, vField)
	case written.cfg.unversioned:
		add = fmt.Errorf("add a document with %s: %s", kField, written.kindName)
	case reg.cfg.versionOnly:
		add = fmt.Errorf("add a document with %s: %s", vField, written.cfg.name)
	default:
		add = fmt.Errorf("add a document with %s: %s and %s: %s", vField, written.cfg.name, kField, written.kindName)
	}

	details := []error{add}
	for _, e := range l.empty {
		details = append(details, errors.New(e))
	}

	return details
}
