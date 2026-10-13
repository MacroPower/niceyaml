package manifest

import (
	"errors"
	"fmt"
	"iter"
	"reflect"

	"go.jacobcolvin.com/niceyaml"
)

// Set holds the manifests that one load returns, in load order. Its typed
// accessors return each manifest as an [Object] of the Go type of its
// kind.
//
// The methods of a Set are safe for concurrent use.
//
// Receive instances from [Registry.Load].
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
// Receive instances from [Set.One], [Set.All], [Set.Get], or
// [Set.Objects].
type Object[T any] struct {
	// Value holds the decoded value of the current version.
	Value T
	// Node holds the document as the user wrote it.
	Node *niceyaml.Node
	// Binds an error with the paths of the current version.
	bind func(error) error
	// Name holds the name, from the field of [WithNameField].
	Name string
	// Kind holds the name of the kind, as [NewKind] gives it, even for a
	// document that declares an old name from [KindName].
	Kind string
	// Version holds the version that the file declares, and is empty for
	// an unversioned document.
	Version string
	// UpToDate is true when a rewrite leaves the document as it is: the
	// file declares the written version of its kind, or a newer one.
	UpToDate bool
}

// Bind binds err in the document of o, with each path in the terms of the
// current version, whatever version the file declares. The error lands on
// the line that the user wrote, even when the document migrated. An error
// without a location names the file, and the document in a file of
// several, as [niceyaml.Node.Bind] does. On an Object that a test builds
// itself, Bind binds as o.Node.Bind does.
func (o Object[T]) Bind(err error) error {
	switch {
	case o.bind != nil:
		return o.bind(err)
	case o.Node != nil:
		return o.Node.Bind(err) //nolint:wrapcheck // The binding is the error of the caller.
	default:
		return err
	}
}

// newSet returns the set of the documents that passed every check.
func (l *loader) newSet() *Set {
	s := &Set{reg: l.reg}

	for _, f := range l.files {
		if f.src != nil {
			s.sources = append(s.sources, f.src)
		}
	}

	for _, d := range l.docs {
		if !d.passed {
			continue
		}

		v := d.route.version
		entry := d.route.kind

		obj := Object[any]{
			Value:    d.value,
			Node:     d.doc,
			bind:     d.work.Bind,
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
// the set holds none. The kind carries [Singleton], so the set holds one at
// most.
//
// One panics when T is not the Go type of a kind of the registry, or when
// the kind does not carry [Singleton], so the first test that runs the
// call fails.
func (s *Set) One[T any]() (Object[T], bool) {
	entry := s.reg.kindOf(reflect.TypeFor[T]())
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

// All yields the manifests of the kind whose Go type is T, in load order.
//
// All panics when T is not the Go type of a kind of the registry. It
// panics at the call, before a loop ranges over the sequence.
func (s *Set) All[T any]() iter.Seq[Object[T]] {
	entry := s.reg.kindOf(reflect.TypeFor[T]())

	return func(yield func(Object[T]) bool) {
		for _, o := range s.objects {
			if o.entry == entry && !yield(typed[T](o.obj)) {
				return
			}
		}
	}
}

// Get returns the manifest of the kind whose Go type is T with the given
// name, and false when the set holds none.
//
// Get panics when T is not the Go type of a kind of the registry, or when
// the registry reads no names, as [WithNameField] gives them.
func (s *Set) Get[T any](name string) (Object[T], bool) {
	entry := s.reg.kindOf(reflect.TypeFor[T]())
	if !s.reg.cfg.hasNameField {
		panic("manifest: Get needs a registry with manifest.WithNameField")
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
// the load found none.
func (s *Set) Warnings() error {
	return s.warnings
}

// Sources returns the Source of each file that the load read, in load
// order, a file without manifests included.
func (s *Set) Sources() []*niceyaml.Source {
	return s.sources
}

// typed returns o with its value as a T.
func typed[T any](o Object[any]) Object[T] {
	v, _ := o.Value.(T) //nolint:errcheck // The kind of the object decodes into T.

	return Object[T]{
		Value:    v,
		Node:     o.Node,
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
// failed.
func (l *loader) checkNames() {
	reg := l.reg
	if !reg.cfg.hasNameField {
		return
	}

	seen := make(map[*kindEntry]map[string]*docState)

	for _, d := range l.docs {
		if !d.hasName || d.route.kind == nil {
			continue
		}

		entry := d.route.kind

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
			niceyaml.AtPath(reg.cfg.nameField)))}
		details = append(details, reg.oldName(entry, d.route.wroteKind)...)

		err := d.work.Bind(newProblem(ErrDuplicateName, fmt.Sprintf("duplicate %s %q", entry.kind.name, d.name),
			niceyaml.AtPath(reg.cfg.nameField), niceyaml.WithDetails(details...)))

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
// That is its kind field, its version field when it has no kind field, or
// its start when it has no type fields.
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
// set lacks: what the user adds, and why each directory input that
// selected no file did so.
func (l *loader) missingDetails(entry *kindEntry) []error {
	reg := l.reg
	written := entry.written

	var add error

	switch {
	case written.cfg.unversioned:
		add = fmt.Errorf("add %s document without %s", article(entry.kind.name), reg.typeFieldsText())
	case reg.cfg.versionOnly:
		add = fmt.Errorf("add a document with %s: %s", fieldName(reg.cfg.versionField), written.cfg.name)
	default:
		add = fmt.Errorf("add a document with %s: %s and %s: %s", fieldName(reg.cfg.versionField),
			written.cfg.name, fieldName(reg.cfg.kindField), written.kindName)
	}

	details := []error{add}
	for _, e := range l.empty {
		details = append(details, errors.New(e))
	}

	return details
}
