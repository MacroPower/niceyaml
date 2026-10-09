package schema

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/fault"
	"go.jacobcolvin.com/niceyaml/position"
)

// noFormMessage is the message of the violation that stands for a value
// several branches of an anyOf or oneOf reject.
const noFormMessage = "value matches none of the allowed forms"

// Violation is one constraint of a JSON schema that a value breaks.
// [Schema.Validate] and [Schema.ValidateValue] report each one as a
// [*niceyaml.Error] that wraps a Violation and carries the YAML path to
// the failing location. A binding reports that path from `$`. For
// Validate, `$` is the root of the document. For ValidateValue, it is
// the value, which came from no document. A
// caller reads the constraint from the Violation instead of matching on
// the text of the message. A report that suppresses a rule, rewords a
// message, or writes a format such as SARIF walks the bound errors and
// reads the Violation of each:
//
//	for bound := range niceyaml.AllBindings(err) {
//		v, ok := errors.AsType[*schema.Violation](bound.Reported())
//		if !ok {
//			continue
//		}
//
//		path, _ := bound.Path()
//		emit(v.Keyword, v.SchemaPath, path, v.Message)
//	}
//
// [niceyaml.SourceError.Reported] returns the error of one binding
// alone, so the search above finds the Violation of that binding or
// none. Several violations come back under a count summary from
// [niceyaml.NewSummary], which wraps no Violation. [errors.As] on its
// binding searches the violations it heads too and finds the Violation
// of the first.
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
// a Violation with the keyword anyOf or oneOf. That error holds one
// detail per branch, from [niceyaml.WithDetails], named by the position
// of the branch in the schema, as "form 2" names the second. A form
// carries no location and wraps no Violation. Its details are the
// violations of its branch, each with a path and a Violation of its own,
// as for a string that must hold three characters or start with x:
//
//	1:4: $.v: value matches none of the allowed forms
//	    form 1
//	        1:4: $.v: string length 2 is less than 3
//	    form 2
//	        1:4: $.v: string does not match pattern "^x"
//
// The walk above reaches every binding, so it emits the violation of the
// value and each violation under its forms. A report that wants one row
// per problem walks [niceyaml.ErrorTree.Problems] instead. It yields the
// violation of the value once, with the forms as the Children of its
// node, so the rows of a validation match the count its summary states.
//
// A mapping that leaves out a member the schema requires has no value to
// point at. The violation of required, of dependentRequired, or of the
// list form of dependencies carries the path the member would have, so a
// report reads the missing field from the path and not from the message:
//
//	2:1: $.server.name: missing required property "name"
//
// The path names the member as the schema does. It selects nothing, so
// the error binds at the key of the mapping, here server, and
// [niceyaml.SourceError.Nearest] returns the path of that mapping.
//
//nolint:errname // Named for the constraint the value breaks, which a [*niceyaml.Error] reports.
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

// Is reports whether target is the mark [niceyaml.IsInvalid] reads, since
// a value that breaks a constraint is the fault of the document. The
// module keeps the mark internal, so the method matches no target a
// caller can name. The [*niceyaml.Error] a schema builds around each
// Violation declares the fault already, so this answers for a Violation
// that reaches a caller on its own, such as one a [niceyaml.Validator]
// returns as it is. A nil Violation matches nothing.
func (v *Violation) Is(target error) bool {
	return v != nil && target == fault.ErrInvalid
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
// summary from [niceyaml.NewSummary] with no path of its own, and each
// violation it heads carries the path to one failing location. The index
// idx finds the members of the mappings in the document of n.
func newValidationError(ve *jsonschema.ValidationError, n *niceyaml.Node, idx *datapath.Index) error {
	c := converter{root: rootOf(n), idx: idx}

	found := c.violations(ve)
	if len(found) == 0 {
		return niceyaml.Invalid(newViolation(ve))
	}

	return niceyaml.NewSummary(fmt.Sprintf("%d schema violations", len(found)), found...)
}

// converter turns the failures of one validation into violations. The
// tree root is the one the validated value came from, or nil for a value
// with no node, and idx finds the members of the mappings in its
// document.
type converter struct {
	root ast.Node
	idx  *datapath.Index
}

// violations returns the violations in the error tree of e, each a
// [*niceyaml.Error] that wraps a [*Violation].
//
// A concrete failure is one violation. A failed anyOf or oneOf is the
// violations [converter.union] reduces it to. Any other keyword that
// wraps the failures of its subschemas, such as allOf, $ref, or then,
// adds nothing to them, so those failures stand in its place.
func (c converter) violations(e *jsonschema.ValidationError) []error {
	switch {
	case e == nil:
		return nil

	case isConcrete(e):
		return []error{c.leaf(e)}

	case e.Keyword == jsonschema.KeywordAnyOf || e.Keyword == jsonschema.KeywordOneOf:
		return c.union(e)
	}

	return c.all(e.Causes)
}

// all returns the violations in the error trees of causes, in order.
func (c converter) all(causes []*jsonschema.ValidationError) []error {
	var out []error

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
func (c converter) union(e *jsonschema.ValidationError) []error {
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
			niceyaml.WithDetails(c.all(b.causes)...),
		))
	}

	v := newViolation(e)
	v.Message = noFormMessage

	return []error{
		niceyaml.Invalid(v, append(c.at(e), niceyaml.WithDetails(forms...))...),
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
// its [*Violation] and carries the location e fails at, as [converter.at]
// gives it.
func (c converter) leaf(e *jsonschema.ValidationError) error {
	return niceyaml.Invalid(newViolation(e), c.at(e)...)
}

// at returns the options that give an error the location e fails at. The
// first is the YAML path to that location. A failure that constrains the
// key of a member, as [constrainsKey] reports, points at the key through
// [paths.Path.Key], and any other at the value. A failure about a member
// the mapping leaves out, as [missingMember] finds one, has the location
// [converter.atMissing] gives it instead.
//
// A failure about the size or the elements of a mapping or a sequence,
// such as one of maxItems, carries the path of that value. The error
// binds at the key of the entry that holds the value, or at the "-" of
// the element it is, as [niceyaml.AtPath] describes.
//
// The path spells each key as the source does, so a key the decoder
// respells, such as 0x10 for the member name 16, still names its member.
// Without a root, which a caller of [Schema.ValidateValue] does not hand
// over, the path spells each key as the decoder does.
//
// Where no spelling selects the member, as [sourcePath] describes, the
// path keeps the decoded name and selects nothing or another entry. A
// second option then gives the position of the node the walk reached, as
// [datapath.Target.Token] finds it. The error binds at that position, and
// the path only names the member in the message.
func (c converter) at(e *jsonschema.ValidationError) []niceyaml.ErrorOption {
	target := sourcePath(c.root, c.idx, e.InstanceSegments())

	if name, ok := missingMember(e); ok {
		return c.atMissing(target, name)
	}

	key := constrainsKey(e)

	path := target.Path
	if key {
		path = path.Key()
	}

	opts := []niceyaml.ErrorOption{niceyaml.AtPath(path)}

	if tk := target.Token(key); tk != nil && tk.Position != nil {
		opts = append(opts, niceyaml.AtPosition(position.NewFromToken(tk)))
	}

	return opts
}

// constrainsKey reports whether e is a failure of a keyword that
// constrains the key of the member at its instance location, rather than
// the value. Those are additionalProperties, which allows no member of
// that name, and propertyNames, which the name of the member breaks.
func constrainsKey(e *jsonschema.ValidationError) bool {
	switch e.Keyword {
	case jsonschema.KeywordAdditionalProperties, jsonschema.KeywordPropertyNames:
		return true
	default:
		return false
	}
}

// atMissing returns the options that locate a failure about the member
// name, which the mapping at target leaves out. The first is the path the
// member would have, so the path of the violation names the missing
// member. That path selects nothing, and the error binds at the key of
// the mapping, as [niceyaml.SourceError.Nearest] describes. The source
// spells no key for the member, so the path names it as the schema does.
//
// A second option gives the position of the mapping where the path would
// bind elsewhere, and the error binds at that position. That holds where
// the path to the mapping selects another entry, as
// [datapath.Target.Token] describes. It also holds where the mapping has
// a key a selector with name selects, as [datapath.Index.SelectsEntry]
// reports, since the member that key sets has another name, as the key
// 0x10 sets the member 16 under a schema that requires 0x10.
func (c converter) atMissing(target datapath.Target, name string) []niceyaml.ErrorOption {
	opts := []niceyaml.ErrorOption{niceyaml.AtPath(target.Path.Child(name))}

	if !target.Unspelled && !c.idx.SelectsEntry(target.Node, name) {
		return opts
	}

	if tk := target.Start(true); tk != nil && tk.Position != nil {
		opts = append(opts, niceyaml.AtPosition(position.NewFromToken(tk)))
	}

	return opts
}

// The text the validator puts in front of the name of a missing member in
// the message of each keyword that requires one. The name follows, quoted
// as [strconv.Quote] writes it.
const (
	requiredPrefix  = "missing required property "
	dependentPrefix = "property "
	dependentInfix  = " requires property "
)

// missingMember returns the name of the member e reports missing from the
// mapping at its instance location, and true. Such a failure comes from
// required, from dependentRequired, and from the list form of the legacy
// dependencies. It reports false for any other failure.
//
// The validator carries the name in the message alone, so missingMember
// reads it from there: `missing required property "name"` for required,
// and `property "port" requires property "name"` for the other two. It
// reports false for a message of another form.
func missingMember(e *jsonschema.ValidationError) (string, bool) {
	var (
		quoted string
		ok     bool
	)

	switch e.Keyword {
	case jsonschema.KeywordRequired:
		quoted, ok = strings.CutPrefix(e.Message, requiredPrefix)

	case jsonschema.KeywordDependentRequired, jsonschema.KeywordDependencies:
		rest, found := strings.CutPrefix(e.Message, dependentPrefix)
		if !found {
			return "", false
		}

		// The member whose presence requires the missing one.
		trigger, err := strconv.QuotedPrefix(rest)
		if err != nil {
			return "", false
		}

		quoted, ok = strings.CutPrefix(rest[len(trigger):], dependentInfix)
	}

	if !ok {
		return "", false
	}

	name, err := strconv.Unquote(quoted)

	return name, err == nil
}
