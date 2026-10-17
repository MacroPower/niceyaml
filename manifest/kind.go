package manifest

import (
	"context"
	"reflect"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// Kind is one type of manifest, such as a MenuItem. It has one Go type,
// for its current version, and lists its versions oldest first, so the
// last version is the current one. [WithKinds] adds kinds to a registry.
//
// A Kind is a value of its own and never changes, so one Kind can join
// several registries.
//
// Create instances with [NewKind].
type Kind struct {
	// The Go type of the current version.
	typ reflect.Type
	// The function that decodes a document into a value of typ, as
	// [niceyaml.Node.Decode] does.
	decode func(ctx context.Context, n *niceyaml.Node, opts []niceyaml.DecodeOption) (any, error)
	name   string
	cfg    kindConfig
}

// NewKind creates a new [Kind] named name, whose current version decodes
// into a value of type T. The name is the name of the current version, the
// one that messages give the kind, as in "missing Cafe".
//
// [NewRegistry] checks the kind, so a kind that contradicts itself, such as
// one that lists a version twice, fails there.
func NewKind[T any](name string, opts ...KindOption) *Kind {
	k := &Kind{
		typ:  reflect.TypeFor[T](),
		name: name,
		decode: func(ctx context.Context, n *niceyaml.Node, opts []niceyaml.DecodeOption) (any, error) {
			return n.Decode[T](ctx, opts...)
		},
	}

	for _, opt := range opts {
		opt(&k.cfg)
	}

	return k
}

// KindOption declares a part of a [Kind], such as a version.
//
// Available options:
//   - [Version]
//   - [Unversioned]
//   - [Claim]
//   - [Required]
//   - [Singleton]
//   - [DecodeOptions]
type KindOption func(*kindConfig)

// kindConfig holds the parts that each [KindOption] declares.
type kindConfig struct {
	claims        []matcher.Matcher
	versions      []*versionConfig
	decodeOptions []niceyaml.DecodeOption
	required      bool
	singleton     bool
}

// Version is a [KindOption] that adds the version named name to the kind,
// after the versions before it. The name is the text that the version field
// of a document holds, such as "cafe.jacobcolvin.com/v1", or "2" under
// [WithVersionOnly]. Versions compare by their place in the list, never by
// their text.
func Version(name string, opts ...VersionOption) KindOption {
	return func(c *kindConfig) {
		v := &versionConfig{name: name}

		for _, opt := range opts {
			opt(v)
		}

		c.versions = append(c.versions, v)
	}
}

// Unversioned is a [KindOption] that adds the unversioned layout of the
// kind. The layout reads the documents without type fields, as the Routing
// section of the package documentation describes. It takes the place of
// the oldest version, so it must come first among the versions. It takes
// the options of [Version] except [KindName].
func Unversioned(opts ...VersionOption) KindOption {
	return func(c *kindConfig) {
		v := &versionConfig{unversioned: true}

		for _, opt := range opts {
			opt(v)
		}

		c.versions = append(c.versions, v)
	}
}

// Claim is a [KindOption] that loads each document as the kind when the
// document does not have a kind field and m accepts it. A load asks the
// claims of the kinds in the order of [WithKinds], and the first that
// accepts the document wins. A matcher that returns an error fails the
// document with that error.
//
// Each Claim adds a matcher, and the kind claims each document that one of
// its matchers accepts. A load asks the matchers of a kind in the order
// given. [NewRegistry] rejects a nil matcher.
func Claim(m matcher.Matcher) KindOption {
	return func(c *kindConfig) {
		c.claims = append(c.claims, m)
	}
}

// Required is a [KindOption] that fails a load whose set does not hold a
// manifest of the kind.
func Required() KindOption {
	return func(c *kindConfig) {
		c.required = true
	}
}

// Singleton is a [KindOption] that fails a load whose set holds more than
// one manifest of the kind. [Set.One] reads the manifest of such a kind.
func Singleton() KindOption {
	return func(c *kindConfig) {
		c.singleton = true
	}
}

// DecodeOptions is a [KindOption] that adds options to each decode of a
// manifest of the kind. They apply after those of [WithDecodeOptions].
func DecodeOptions(opts ...niceyaml.DecodeOption) KindOption {
	return func(c *kindConfig) {
		c.decodeOptions = append(c.decodeOptions, opts...)
	}
}

// VersionOption declares a part of a version, such as its schema.
//
// Available options:
//   - [Schema]
//   - [Migrate]
//   - [Deprecated]
//   - [KindName]
//   - [Written]
//   - [Loosened]
type VersionOption func(*versionConfig)

// versionConfig holds the parts that each [VersionOption] declares.
type versionConfig struct {
	schema      schema.Ref
	name        string
	kindName    string
	details     []string
	steps       []step
	unversioned bool
	hasSchema   bool
	deprecated  bool
	hasKindName bool
	written     bool
	loosened    bool
}

// Schema is a [VersionOption] that gives the version the schema that ref
// names. A load checks each document that declares the version, or that
// passes through it on the way to the current version, against the
// schema. A version without a schema skips the check.
func Schema(ref schema.Ref) VersionOption {
	return func(c *versionConfig) {
		c.schema, c.hasSchema = ref, true
	}
}

// step is one step of a version, which [Migrate] adds.
type step struct {
	fn   edit.Func
	path paths.Path
}

// Migrate is a [VersionOption] that adds a step to the version, after the
// steps before it. The steps of a version bring a document from the version
// before it, so the oldest version must not take a step. The step calls fn
// once for each place that p selects, in document order, and zero times
// when p selects zero places. The path must start at `$` and must not hold
// a `~`. The Versions section of the package documentation describes the
// calls.
//
// The step runs as one [edit.Doc.Apply], whose messages name the version.
// [edit.Node.Decode] in fn applies the unmarshalers of [WithUnmarshalers].
// It leaves out the decode options of the registry and the kind, which
// describe the current version. Fn should pass the ctx that it got to
// Decode, so the warnings of the decode join the warnings of the load.
func Migrate(p paths.Path, fn edit.Func) VersionOption {
	return func(c *versionConfig) {
		c.steps = append(c.steps, step{path: p, fn: fn})
	}
}

// Deprecated is a [VersionOption] that marks the version deprecated. A load
// warns about each document that declares it, with details after the
// headline that niceyaml writes. On the current version, it marks the
// whole kind deprecated, and every document of the kind warns.
func Deprecated(details ...string) VersionOption {
	return func(c *versionConfig) {
		c.deprecated, c.details = true, details
	}
}

// KindName is a [VersionOption] that gives the name that the version used
// for its kind, where it differs from the name of the kind. A document of
// the version writes that name in its kind field.
func KindName(name string) VersionOption {
	return func(c *versionConfig) {
		c.kindName, c.hasKindName = name, true
	}
}

// Written is a [VersionOption] that marks an older version as the version
// that a rewrite writes. Without the mark, a rewrite writes the current
// version, the last version of the kind. An author marks an older version
// while users might go back to a release that does not read the current
// one. A rewrite then writes files that the older release reads. The mark
// stays on its version when the author adds a newer one. [NewRegistry]
// rejects the mark on the current version, which a rewrite writes without
// it.
func Written() VersionOption {
	return func(c *versionConfig) {
		c.written = true
	}
}

// Loosened is a [VersionOption] that marks a version whose schema admits
// more than its release did, because a step stopped removing a value.
// [NewRegistry] rejects [Written] on such a version. It also rejects
// Loosened on the current version of a kind whose versions do not carry
// Written, when the kind lists an older version. A rewrite of such a kind
// moves each document of the older version to the current one.
func Loosened() VersionOption {
	return func(c *versionConfig) {
		c.loosened = true
	}
}
