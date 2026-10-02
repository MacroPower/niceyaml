package schema

import (
	"fmt"
	"slices"

	"github.com/goccy/go-yaml/ast"
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// noFormMessage is the message of the violation that stands for a value
// several branches of an anyOf or oneOf reject.
const noFormMessage = "value matches none of the allowed forms"

// Violation is one constraint of a JSON schema that a value breaks.
// [Schema.Validate] and [Schema.ValidateValue] report each one as a
// [*niceyaml.Error] that wraps a Violation and carries the YAML path to
// the failing location. A caller reads the constraint from the Violation
// instead of matching on the text of the message. A report that
// suppresses a rule, rewords a message, or writes a format such as SARIF
// walks the bound errors and reads the Violation of each:
//
//	for bound := range niceyaml.AllBindings(err) {
//		v, ok := errors.AsType[*schema.Violation](bound.Cause())
//		if !ok {
//			continue
//		}
//
//		path, _ := bound.Path()
//		emit(v.Keyword, v.SchemaPath, path, v.Message)
//	}
//
// [niceyaml.SourceError.Cause] returns the error of one binding alone, so
// the search above finds the Violation of that binding or none. The count
// summary of several violations wraps no Violation. [errors.As] on its
// binding searches the errors nested in it too and finds the Violation of
// the first. An unbound error from [Schema.ValidateValue] holds its
// Violation as its [niceyaml.Error.Cause].
//
// A value that matches no branch of an anyOf or oneOf fails every branch
// at once, and the failures of a branch say what is wrong with the value
// only when the value was meant for that branch. The report leaves out
// each branch that fails only because the value itself has another type,
// as long as some branch fails another way. A malformed string under a
// schema that allows a string with a pattern or null thus reports the
// pattern alone. When every branch fails by the type of the value, each
// one stays.
//
// When one branch remains, its failures are violations of their own, as
// though the schema held that branch alone. When several remain, the
// value has one violation, which carries the path to the value and wraps
// a Violation with the keyword anyOf or oneOf. That error nests one error
// per branch, named by the position of the branch in the schema, as
// "form 2" names the second. A form carries no location and wraps no
// Violation. It nests the violations of its branch, each with a path and
// a Violation of its own, as for a string that must hold three characters
// or start with x:
//
//	1:4: $.v: value matches none of the allowed forms
//	    form 1
//	        1:4: $.v: string length 2 is less than 3
//	    form 2
//	        1:4: $.v: string does not match pattern "^x"
//
// The walk above therefore emits the violation of the value and each
// violation under its forms. A report that wants one row per value emits
// the first and passes over the errors [niceyaml.SourceError.Errors]
// nests under it.
//
//nolint:errname // Named for the finding it holds, which a [*niceyaml.Error] reports.
type Violation struct {
	// Keyword is the JSON Schema keyword the value fails, such as "type",
	// "required", or "pattern". [jsonschema.KeywordType] and the constants
	// beside it name each keyword.
	Keyword string

	// SchemaPath is the JSON Pointer to Keyword in the schema, along the
	// path the validator took to reach it. The path steps through each
	// $ref on the way and does not name the definition the $ref points
	// at, so a pattern in the definition behind the $ref of spec reads
	// /properties/spec/$ref/properties/sla/pattern. A keyword in a
	// definition that several references reach therefore has one
	// SchemaPath per reference, and they agree after the last $ref.
	SchemaPath string

	// Message describes the failure, such as `expected "array", got
	// "string"`. It is the text [Violation.Error] returns, and the
	// message of the [*niceyaml.Error] that wraps the Violation.
	Message string
}

// Error returns [Violation.Message]. A nil Violation has an empty message.
func (v *Violation) Error() string {
	if v == nil {
		return ""
	}

	return v.Message
}

// newViolation returns the [*Violation] that the failure e reports.
func newViolation(e *jsonschema.ValidationError) *Violation {
	return &Violation{
		Keyword:    e.Keyword,
		SchemaPath: string(e.SchemaPath),
		Message:    e.Message,
	}
}

// newValidationError converts a [*jsonschema.ValidationError] into a
// [*niceyaml.Error].
//
// The conversion reduces the error tree to its violations, as
// [converter.violations] finds them. A single violation becomes the main
// error and carries its own path, so the printer highlights that location
// and [niceyaml.Error.Path] reports it. Several violations become a count
// summary with no path of its own, and each nested error carries the path
// to one failing location. The index idx finds the members of the
// mappings in the document of n.
func newValidationError(ve *jsonschema.ValidationError, n *niceyaml.Node, idx *memberIndex) *niceyaml.Error {
	c := converter{root: rootOf(n), idx: idx}

	found := c.violations(ve)

	switch len(found) {
	case 0:
		return niceyaml.WrapError(newViolation(ve))
	case 1:
		return found[0]
	}

	return niceyaml.NewError(
		fmt.Sprintf("%d schema violations", len(found)),
		niceyaml.WithErrors(nested(found)...),
	)
}

// nested returns errs as the errors [niceyaml.WithErrors] nests.
func nested(errs []*niceyaml.Error) []error {
	out := make([]error, 0, len(errs))
	for _, err := range errs {
		out = append(out, err)
	}

	return out
}

// converter turns the failures of one validation into violations. The
// tree root is the one the validated value came from, or nil for a value
// with no node, and idx finds the members of the mappings in its
// document.
type converter struct {
	root ast.Node
	idx  *memberIndex
}

// violations returns the violations in the error tree of e, each a
// [*niceyaml.Error] that wraps a [*Violation].
//
// A concrete failure is one violation. A failed anyOf or oneOf is the
// violations [converter.union] reduces it to. Any other keyword that
// wraps the failures of its subschemas, such as allOf, $ref, or then,
// adds nothing to them, so those failures stand in its place.
func (c converter) violations(e *jsonschema.ValidationError) []*niceyaml.Error {
	switch {
	case e == nil:
		return nil

	case isConcrete(e):
		return []*niceyaml.Error{c.leaf(e)}

	case e.Keyword == jsonschema.KeywordAnyOf || e.Keyword == jsonschema.KeywordOneOf:
		return c.union(e)
	}

	return c.all(e.Causes)
}

// all returns the violations in the error trees of causes, in order.
func (c converter) all(causes []*jsonschema.ValidationError) []*niceyaml.Error {
	var out []*niceyaml.Error

	for _, cause := range causes {
		out = append(out, c.violations(cause)...)
	}

	return out
}

// isConcrete reports whether e is a failure to report itself, and no
// wrapper around the failures of its subschemas, as
// [jsonschema.ValidationError.Leaves] tells the two apart.
func isConcrete(e *jsonschema.ValidationError) bool {
	leaves := e.Leaves()

	return len(leaves) == 1 && leaves[0] == e
}

// union returns the violations of e, a failed anyOf or oneOf.
//
// The value failed every branch, and the failures of a branch say what
// is wrong with the value only when the value was meant for that branch.
// A branch that fails only by the type of the value itself, as
// [branch.typeOnly] reports, was not, so union drops it when another
// branch fails some other way. When every branch fails by type, each
// one stays.
//
// The failures of a single branch that remains stand in place of e, as
// though the schema held that branch alone. Several branches that remain
// become one violation at the value, which wraps a [*Violation] with the
// keyword of e. It nests one error per branch, named by the position of
// the branch in the schema, as "form 2" names the second. That error
// carries no location and nests the violations of its branch.
//
// Where [branches] cannot tell the branches apart, the failures of e
// stand in its place, as those of any other wrapper do.
func (c converter) union(e *jsonschema.ValidationError) []*niceyaml.Error {
	all, ok := branches(e)
	if !ok {
		return c.all(e.Causes)
	}

	kept := make([]branch, 0, len(all))

	for _, b := range all {
		if !b.typeOnly(e) {
			kept = append(kept, b)
		}
	}

	if len(kept) == 0 {
		kept = all
	}

	if len(kept) == 1 {
		return c.all(kept[0].causes)
	}

	forms := make([]error, 0, len(kept))
	for _, b := range kept {
		forms = append(forms, niceyaml.NewError(
			fmt.Sprintf("form %d", b.index+1),
			niceyaml.WithErrors(nested(c.all(b.causes))...),
		))
	}

	v := newViolation(e)
	v.Message = noFormMessage

	return []*niceyaml.Error{
		niceyaml.WrapError(v, niceyaml.AtExactPath(c.path(e)), niceyaml.WithErrors(forms...)),
	}
}

// branch holds the failures of one branch of an anyOf or oneOf.
type branch struct {
	causes []*jsonschema.ValidationError
	// The position of the branch in the list of its keyword.
	index int
}

// branches groups the causes of e, a failed anyOf or oneOf, by the branch
// each one failed in, with the branches in the order their first causes
// come. The validator reports the failures of every branch in one list,
// so the group of a cause comes from its schema path. The schema path of
// e ends at its keyword, and the path of each cause goes on from there
// with the index of its branch. Reports false when e has no causes, and
// when the path of a cause names no branch there, as that of an error
// built by hand may not.
func branches(e *jsonschema.ValidationError) ([]branch, bool) {
	at := len(e.SchemaSegments())

	var out []branch

	for _, cause := range e.Causes {
		if cause == nil {
			continue
		}

		segments := cause.SchemaSegments()
		if at >= len(segments) || !segments[at].IsIndex {
			return nil, false
		}

		index := segments[at].Index

		i := slices.IndexFunc(out, func(b branch) bool { return b.index == index })
		if i < 0 {
			i = len(out)
			out = append(out, branch{index: index})
		}

		out[i].causes = append(out[i].causes, cause)
	}

	return out, len(out) > 0
}

// typeOnly reports whether the branch fails only by the type of the value
// e is about. That holds when every concrete failure in the branch is a
// type failure at the instance location of e. A type failure below that
// location, such as at a member of the value, belongs to a value whose
// type the branch accepts, so it does not count.
func (b branch) typeOnly(e *jsonschema.ValidationError) bool {
	for _, cause := range b.causes {
		for _, leaf := range cause.Leaves() {
			if leaf.Keyword != jsonschema.KeywordType {
				return false
			}

			if !slices.Equal(leaf.InstanceSegments(), e.InstanceSegments()) {
				return false
			}
		}
	}

	return true
}

// leaf converts one concrete failure into a [*niceyaml.Error] that wraps
// its [*Violation] and carries the YAML path to the failing location, as
// [converter.path] writes it.
//
// The path is exact, as [niceyaml.AtExactPath] sets one. A path that
// [sourcePath] could not spell selects nothing or another entry, so the
// error binds with no position, where a mapping nearby would be a wrong
// one.
func (c converter) leaf(e *jsonschema.ValidationError) *niceyaml.Error {
	return niceyaml.WrapError(newViolation(e), niceyaml.AtExactPath(c.path(e)))
}

// path returns the YAML path to the location e fails at. A failure that
// constrains the key of a member, such as an additional property, points
// at the key through [paths.Path.Key], and any other at the value.
//
// The path spells each key as the source does, so a key the decoder
// respells, such as 0x10 for the member name 16, still names its member.
// Without a root, which a [Schema.ValidateValue] caller does not hand
// over, the path spells each key as the decoder does.
func (c converter) path(e *jsonschema.ValidationError) paths.Path {
	path := sourcePath(c.root, c.idx, e.InstanceSegments())

	if e.TargetsKey() {
		path = path.Key()
	}

	return path
}
