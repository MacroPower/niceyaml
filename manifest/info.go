package manifest

import (
	"reflect"
	"slices"

	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
)

// KindInfo describes a kind that a [Registry] reads, as
// [Registry.Kinds] lists it. A test, a command that lists the kinds of
// an app, or a generator of docs reads the declaration of the registry
// through it.
type KindInfo struct {
	// Type is the Go type of the current version, as [NewKind] gives it.
	// Several kinds can share a type.
	Type reflect.Type
	// Name is the name of the kind, as [NewKind] gives it.
	Name string
	// Versions lists the versions of the kind oldest first, so the last
	// is the current version.
	Versions []VersionInfo
	// NameField is the path that [NameField] gives the kind. It holds
	// the zero [paths.Path] when HasNameField is false.
	NameField paths.Path
	// HasNameField reports whether the kind carries [NameField].
	HasNameField bool
	// Required reports whether the kind carries [Required].
	Required bool
	// Singleton reports whether the kind carries [Singleton].
	Singleton bool
	// Deprecated reports whether the kind carries [DeprecatedKind].
	Deprecated bool
}

// VersionInfo describes a version of a kind, as [KindInfo.Versions]
// lists it.
type VersionInfo struct {
	// Name is the name of the version, as [Version] gives it. It is empty
	// for the layout of [Unversioned], as [Object.Version] is.
	Name string
	// KindName is the name of the kind that documents of the version
	// write in the kind field. It is the name that [KindName] gives the
	// version, or else the name of the kind. Under [WithVersionOnly],
	// [Registry.KindField] reports false and documents do not write the
	// name, but KindName still holds it.
	KindName string
	// Schema is the schema that [Schema] gives the version. It holds the
	// zero [schema.Ref] when HasSchema is false. A caller compiles it with
	// the [schema.Registry] of [WithSchemaRegistry], or with one of its
	// own.
	Schema schema.Ref
	// Steps counts the steps that [Migrate] adds to the version. The
	// [StepID.Step] of each step runs from 1 to Steps, inclusive.
	Steps int
	// HasSchema reports whether the version carries [Schema].
	HasSchema bool
	// Deprecated reports whether the version carries [Deprecated].
	Deprecated bool
	// Written reports whether a rewrite writes the version. A rewrite
	// writes one version of each kind. It writes the version that carries
	// [Written], or else the current version.
	Written bool
	// Removed reports whether [WithRemoved] names the version, which the
	// kind still lists.
	Removed bool
}

// Kinds returns the kinds that the registry reads, in the order of
// [WithKinds]. Each call returns a new slice that the caller may change.
//
// The list leaves out the claims and the decode options of each kind,
// and the functions of its steps. It also leaves out each version or kind
// that [WithRemoved] names but the registry does not list.
func (r *Registry) Kinds() []KindInfo {
	kinds := make([]KindInfo, len(r.kinds))

	for i, entry := range r.kinds {
		k := entry.kind

		info := KindInfo{
			Type:         k.typ,
			Name:         k.name,
			NameField:    k.cfg.nameField,
			HasNameField: k.cfg.hasNameField,
			Required:     k.cfg.required,
			Singleton:    k.cfg.singleton,
			Deprecated:   k.cfg.deprecated,
			Versions:     make([]VersionInfo, len(entry.versions)),
		}

		for j, v := range entry.versions {
			info.Versions[j] = VersionInfo{
				Schema:     v.cfg.schema,
				Name:       v.cfg.name,
				KindName:   v.kindName,
				Steps:      len(v.cfg.steps),
				HasSchema:  v.cfg.hasSchema,
				Deprecated: v.cfg.deprecated,
				Written:    v == entry.written,
				Removed:    v.removal != nil,
			}
		}

		kinds[i] = info
	}

	return slices.Clip(kinds)
}

// VersionField returns the path of the version field of each document,
// as [WithVersionField] or [WithVersionOnly] names it. It returns
// `$.apiVersion` without either option.
func (r *Registry) VersionField() paths.Path {
	return r.cfg.versionField
}

// KindField returns the path of the kind field of each document, as
// [WithKindField] names it, and true. It returns `$.kind` without the
// option. Under [WithVersionOnly], documents do not have a kind field, so
// it returns false.
func (r *Registry) KindField() (paths.Path, bool) {
	if r.cfg.versionOnly {
		return paths.Path{}, false
	}

	return r.cfg.kindField, true
}
