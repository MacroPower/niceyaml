package edit

import (
	"errors"
	"fmt"
	"strconv"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// ErrUnsupported indicates a document or a call that reaches YAML that the
// engine does not edit, as the Supported YAML section of the package
// documentation lists it. A detail says what the user can write instead.
// An error that matches ErrUnsupported is an [*UnsupportedError], and it
// never matches [ErrEngine].
var ErrUnsupported = errors.New("the engine does not edit this YAML")

// Construct names a YAML construct that the engine does not edit, as the
// Supported YAML section of the package documentation lists them. The zero
// value names no construct.
type Construct int

// [Construct] constants.
const (
	// ConstructAliasProperties is a tag or an anchor on an alias, as in
	// "!t *x" or "!!str *x".
	ConstructAliasProperties Construct = iota + 1
	// ConstructConvertingTag is a tag that converts a collection, as in
	// "!!str &x {a: 1}".
	ConstructConvertingTag
	// ConstructTagKind is the tag "!", or one of the tags !!set, !!omap,
	// and !!pairs, in the shorthand form or in the verbatim form, as in
	// "!<tag:yaml.org,2002:set>".
	ConstructTagKind
	// ConstructReusedAnchor is an anchor name that the document defines
	// twice, or that it defines and aliases while a reference document
	// defines it too.
	ConstructReusedAnchor
	// ConstructRecursiveAlias is an alias inside the value of the anchor
	// that it reads, as in "a: &x [*x]".
	ConstructRecursiveAlias
	// ConstructKeyProperties is an alias as a mapping key, or an anchor or
	// a tag on a key, as in "*k : v", "&k a: v", and "&q <<: *s".
	ConstructKeyProperties
	// ConstructAnchoredEmpty is an anchor on an empty value or an empty
	// item, as in "a: &x".
	ConstructAnchoredEmpty
	// ConstructLineBreaks is a text with both "\n" and "\r\n" line breaks,
	// or with a "\r" alone.
	ConstructLineBreaks
	// ConstructKeyText is a key that a decode reads as another value than
	// its text, as 0x10 reads as 16, or that reads as the null value, as
	// null and ~ do.
	ConstructKeyText
	// ConstructMergeForm is a << merge key in another form than one alias
	// of an anchored mapping of the document without a merge key of its
	// own. Such forms are a sequence of aliases, a mapping written in
	// place, a scalar, an empty value, and a tag on the value or on the
	// key. An alias of a mapping that holds a merge key is one too, and so
	// is a mapping with two merge keys. So is a merge key that a call leaves
	// reading something other than a mapping. A merge key in the YAML of a
	// value that a Set writes is one as well, and its problem has InValue
	// set. A merge key whose value is one alias of a reference document is
	// not such a form.
	ConstructMergeForm
	// ConstructMergeKeys is a change to the keys of a merging mapping that
	// hides or reveals an entry that its merge key brings in.
	ConstructMergeKeys
	// ConstructRepeatedKey is an entry whose key its mapping repeats. A
	// mapping with two << merge keys whose values are aliases of reference
	// documents repeats the key << too.
	ConstructRepeatedKey
	// ConstructExplicitKey is an entry with an explicit key, which the
	// indicator "?" opens.
	ConstructExplicitKey
	// ConstructBlockScalar is a change to the string of a block scalar of
	// the user's text.
	ConstructBlockScalar
	// ConstructFlowLayout is a comment inside a flow collection, a pair
	// without braces in a flow sequence, or a Move of a block value into a
	// flow collection.
	ConstructFlowLayout
	// ConstructAliasCopy is an anchor that a call takes away from an alias
	// that reads it, or a Move after which an alias comes before the anchor
	// that it reads.
	ConstructAliasCopy
	// ConstructValueAlias is an anchor or an alias in the YAML of a value
	// that a call sets.
	ConstructValueAlias
)

// String returns the words that name c, such as "alias properties" for
// [ConstructAliasProperties], or "Construct(n)" for a value n that names
// no construct.
func (c Construct) String() string {
	switch c {
	case ConstructAliasProperties:
		return "alias properties"
	case ConstructConvertingTag:
		return "converting tag"
	case ConstructTagKind:
		return "tag kind"
	case ConstructReusedAnchor:
		return "reused anchor"
	case ConstructRecursiveAlias:
		return "recursive alias"
	case ConstructKeyProperties:
		return "key properties"
	case ConstructAnchoredEmpty:
		return "anchored empty"
	case ConstructLineBreaks:
		return "line breaks"
	case ConstructKeyText:
		return "key text"
	case ConstructMergeForm:
		return "merge form"
	case ConstructMergeKeys:
		return "merge keys"
	case ConstructRepeatedKey:
		return "repeated key"
	case ConstructExplicitKey:
		return "explicit key"
	case ConstructBlockScalar:
		return "block scalar"
	case ConstructFlowLayout:
		return "flow layout"
	case ConstructAliasCopy:
		return "alias copy"
	case ConstructValueAlias:
		return "value alias"
	default:
		return "Construct(" + strconv.Itoa(int(c)) + ")"
	}
}

// UnsupportedError is the problem of a document or a call that reaches a
// construct that the engine does not edit. It matches [ErrUnsupported]. The
// problem binds at the first token of the construct in the user's text. The
// user changes the text there so that the call passes. Each other site of
// the construct comes as a detail. The document is at fault for a construct
// of its own text, so [niceyaml.IsInvalid] reports such a problem. A
// construct in the YAML of a value that a Set writes comes from the
// program, and so does a key that a SetKey repeats in its mapping. Its
// problem binds at the place of the call, InValue is true, and IsInvalid
// does not report it.
type UnsupportedError struct {
	// The message of the problem.
	msg string
	// Related holds the paths of the sites of the construct in text order,
	// such as the first anchor of a name that the document defines twice.
	// Each path names its site in the user's text, as the Errors section of
	// the package documentation describes.
	// For an anchor that a call takes away, it holds each alias that reads
	// the anchor and the mapping of each merge key that reads it. The first
	// of them is the reader where the problem binds. For a merge key in a
	// refused form, it holds each anchored mapping that an alias of the
	// merge key reads.
	Related []paths.Path
	// Construct is the construct that the document or the call reaches.
	Construct Construct
	// InValue reports whether the construct lies in a value or a key that
	// the program passed to a call, rather than in the text of the
	// document.
	InValue bool
}

// Error returns the message of e.
func (e *UnsupportedError) Error() string {
	return e.msg
}

// Unwrap returns [ErrUnsupported].
func (e *UnsupportedError) Unwrap() error {
	return ErrUnsupported
}

// site is a place of the user's text: the range that a problem marks there,
// and the path of the value that holds it.
type site struct {
	// The note of the call that wrote the value at the site, or nil where
	// the user wrote it. A detail names that call, as [Doc.callDetails]
	// gives it.
	writer *note
	path   paths.Path
	at     position.Range
	// The origin of the first byte of the site in the current text, for a
	// site that [Doc.site] or [Doc.offsetSite] gives, which tells who wrote
	// the site.
	origin int
	// Whether the token of the site lies in the key of the entry that path
	// names, so that the path of that key ends in `~`.
	inKey bool
}

// relatedSite is a site of a construct other than the one where its
// problem binds, with the words of the detail that marks it.
type relatedSite struct {
	note string

	site
}

// refusal returns the problem of in, an instance of a construct of the
// user's text, bound in the user's text at its site. A call that named
// names, which reaches in, fails with the problem, which then names the
// call. The problem names the program as [WithApp] names it, and each
// related site comes as a detail before the fix. The details that name
// calls come last, as [Doc.callDetails] gives them.
func (d *Doc) refusal(in *instance, named string) error {
	app := d.appName()
	msg := app + " does not edit " + in.what

	switch {
	case in.call != "":
		msg = d.subject() + " " + fmt.Sprintf(in.call, named)
	case in.but != "":
		msg = fmt.Sprintf("%s %s%s", d.subject(), named, in.but)
	case named != "":
		msg = fmt.Sprintf("%s %s, which %s, and %s does not %s", d.subject(), named, in.reach, app, in.object)
	}

	related := make([]paths.Path, 0, len(in.readers)+len(in.related))
	related = append(related, in.readers...)
	details := make([]error, 0, len(in.related)+2)

	for _, r := range in.related {
		related = append(related, r.path)
		details = append(details, d.user.Bind(niceyaml.NewError(r.note,
			niceyaml.AtPath(r.path), niceyaml.AtRange(r.at), niceyaml.WithDetails(d.writerDetails(r.site)...))))
	}

	details = append(details, errors.New(in.fix))
	details = append(details, d.callDetails(in.at)...)

	err := &UnsupportedError{
		msg:       msg,
		Construct: in.construct,
		Related:   related,
		InValue:   in.inValue,
	}

	opts := []niceyaml.ErrorOption{
		niceyaml.AtPath(in.at.path), niceyaml.AtRange(in.at.at), niceyaml.WithDetails(details...),
	}

	// The program wrote the construct, so the document is not at fault.
	if in.inValue {
		//nolint:wrapcheck // The binding is the problem of the call.
		return d.user.Bind(niceyaml.Unchecked(err, opts...))
	}

	return d.user.Bind(niceyaml.Invalid(err, opts...)) //nolint:wrapcheck // The binding is the problem of the document.
}

// recordRefusal returns the problem of a call that named names, which
// reaches in, as [Doc.refusal] gives it, and the Apply records it.
func (d *Doc) recordRefusal(in *instance, named string) error {
	problem := d.refusal(in, named)
	d.record(problem)

	return problem
}
