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
// that several branches of an anyOf or oneOf reject.
const noFormMessage = "value matches none of the allowed forms"

// Violation is one constraint of a JSON schema that a value breaks.
// [Schema.Check] and [Schema.ValidateValue] report each one as a
// [*niceyaml.Error] that wraps a Violation and carries the YAML path to
// the failing location. A binding reports that path from `$`. For Check,
// `$` is the root of the document. For ValidateValue, it is the value,
// which did not come from a document.
//
// A caller reads the constraint from the Violation instead of matching on
// the text of the message. Some reports suppress a rule, reword a
// message, or write a format such as the Static Analysis Results
// Interchange Format (SARIF). Such a report walks the problems of the
// error and reads the Violation of each:
//
//	for problem := range niceyaml.Problems(err) {
//		v, ok := errors.AsType[*schema.Violation](problem.Err)
//		if !ok {
//			continue
//		}
//
//		path, _ := problem.Path()
//		emit(v.Keyword, v.SchemaPath, path, v.Message)
//	}
//
// Several violations come back under a count summary from
// [niceyaml.NewSummary], which does not wrap a Violation.
// [niceyaml.Problems] passes over the summary and yields one node per
// violation. [errors.As] on the error of a node searches that violation
// and passes over its details. So the loop over [niceyaml.Problems]
// finds the Violation of each problem once. [errors.As] on the summary
// itself also searches the violations that it heads, and it finds the
// Violation of the first.
//
// # Branches
//
// A value that does not match any branch of an anyOf or oneOf fails
// every branch at once. The failures of a branch say what is wrong with
// the value only when the author of the document meant the value for
// that branch. The report leaves out each branch that fails only because
// the value itself has another type, when at least one branch fails
// another way. So a malformed string under a schema that allows a string
// with a pattern or null reports the pattern alone. When every branch
// fails by the type of the value, the report keeps every branch.
//
// When one branch remains, its failures are violations of their own, as
// though the schema held that branch alone. When several remain, the
// value has one violation. That violation carries the path to the value
// and wraps a Violation with the keyword anyOf or oneOf. It holds one
// detail per branch, from [niceyaml.WithDetails]. The position of the
// branch in the schema names each detail, as "form 2" names the second.
// Forms do not carry a location or wrap a Violation. The details of each
// form are the violations of its branch, each with a path and a
// Violation of its own. For a string that must hold three characters or
// start with x, the error reads:
//
//	1:4: $.v: value matches none of the allowed forms
//	    form 1
//	        1:4: $.v: string length 2 is less than 3
//	    form 2
//	        1:4: $.v: string does not match pattern "^x"
//
// The loop over [niceyaml.Problems] reaches every binding, so it emits
// the violation of the value and each violation under its forms. A report
// that wants one row per problem walks [niceyaml.ErrorTree.Problems]
// instead. It yields the violation of the value once, with the forms as
// the Children of its node. So the rows of a validation match the count
// that its summary states.
//
// # Missing Members
//
// When a mapping leaves out a member that the schema requires, the
// violation does not have a value to point at. The violation of
// required, of dependentRequired, or of the list form of dependencies
// carries the path of the missing member. So a report reads the missing
// field from the path and not from the message:
//
//	2:1: $.server.name: missing required property "name"
//
// The path names the member as the schema does. It does not select a
// node, so the error binds at the key of the mapping, here server.
// [niceyaml.SourceError.Nearest] returns the path of that mapping.
//
//nolint:errname // Named for the constraint the value breaks, which a [*niceyaml.Error] reports.
type Violation struct {
	// Keyword is the JSON Schema keyword that the value fails, such as
	// "type", "required", or "pattern". [jsonschema.KeywordType] and the
	// constants beside it name each keyword.
	Keyword string

	// SchemaPath is the JSON Pointer to Keyword in the schema, along the
	// path that the validator took to reach it. The path steps through each
	// $ref on the way and does not name the definition that the $ref points
	// at. So a pattern in the definition behind the $ref of spec reads
	// /properties/spec/$ref/properties/sla/pattern. A keyword in a
	// definition that several references reach therefore has one SchemaPath
	// per reference, and the paths agree after the last $ref.
	SchemaPath string

	// Message describes the failure, such as `expected "array", got
	// "string"`. It is the text that [Violation.Error] returns, and the
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

// Is reports whether target is the mark that [niceyaml.IsInvalid] reads.
// A value that breaks a constraint is the fault of the document, so a
// Violation matches that mark. The module keeps the mark internal, so
// the method does not match any target that a caller can name. The
// [*niceyaml.Error] that a schema builds around each Violation declares
// the fault already. So Is matters for a Violation that reaches a caller
// on its own, such as one that a [niceyaml.Validator] returns without a
// wrapper. A nil Violation does not match any target.
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

// converter turns the failures of one validation into violations. Its
// root is the root of the tree that the validated value came from, or
// nil for a value without a node. Its idx finds the members of the
// mappings in that document.
type converter struct {
	root ast.Node
	idx  *datapath.Index
}

// validationError converts a [*jsonschema.ValidationError] into a
// [*niceyaml.Error].
//
// The conversion reduces the error tree to its violations, as
// [converter.violations] finds them. A single violation becomes the main
// error and carries its own path, so the printer highlights that
// location and [niceyaml.Error.Path] reports it. Several violations
// become a count summary from [niceyaml.NewSummary], which does not have
// a path of its own. Each violation that it heads carries the path to
// one failing location.
func (c converter) validationError(ve *jsonschema.ValidationError) error {
	found := c.violations(ve)
	if len(found) == 0 {
		found = []error{niceyaml.Invalid(newViolation(ve))}
	}

	return niceyaml.NewSummary(fmt.Sprintf("%d schema violations", len(found)), found...)
}

// violations returns the violations in the error tree of e, each a
// [*niceyaml.Error] that wraps a [*Violation].
//
// A concrete failure is one violation. A failed anyOf or oneOf becomes
// the violations that [converter.union] reduces it to. Any other keyword
// that wraps the failures of its subschemas, such as allOf, $ref, or
// then, does not add anything to them. So those failures stand in its
// place.
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

// isConcrete reports whether e is a failure to report itself, rather
// than a wrapper around the failures of its subschemas.
// [jsonschema.ValidationError.Leaves] distinguishes the two.
func isConcrete(e *jsonschema.ValidationError) bool {
	leaves := e.Leaves()

	return len(leaves) == 1 && leaves[0] == e
}

// union returns the violations of e, a failed anyOf or oneOf.
//
// The value failed every branch. The failures of a branch say what is
// wrong with the value only when the author meant the value for that
// branch. The author did not mean the value for a branch that fails only
// by the type of the value itself, as [branch.typeOnly] reports. So
// union drops such a branch when another branch fails some other way.
// When every branch fails by type, union keeps every branch.
//
// When a single branch remains, its failures stand in place of e, as
// though the schema held that branch alone. When several branches
// remain, they become one violation at the value, which wraps a
// [*Violation] with the keyword of e. That violation nests one error per
// branch. The position of the branch in the schema names each error, as
// "form 2" names the second. Form errors do not carry a location, and
// each nests the violations of its branch.
//
// When [branches] cannot group the failures by branch, the failures of e
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
// that each one failed in. It lists the branches in the order that their
// first causes appear in e. The validator reports the failures of every
// branch in one list, so the group of a cause comes from its schema path.
// The schema path of e ends at its keyword, and the path of each cause
// continues from there with the index of its branch. When e does not
// have any causes, branches reports false. It also reports false when the
// path of a cause does not name a branch at that point, as the path of a
// hand-built error might not.
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

// typeOnly reports whether the branch fails only by the type of the
// value that e is about. The branch fails that way when every concrete
// failure in it is a type failure at the instance location of e. A type
// failure below that location, such as at a member of the value, belongs
// to a value whose type the branch accepts. So it does not count.
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
// its [*Violation] and carries the location that e fails at, as
// [converter.at] gives it.
func (c converter) leaf(e *jsonschema.ValidationError) error {
	return niceyaml.Invalid(newViolation(e), c.at(e)...)
}

// at returns the options that give an error the location that e fails
// at. The first is the YAML path to that location. A failure that
// constrains the key of a member, as [constrainsKey] reports, points at
// the key through [paths.Path.Key]. Any other failure points at the
// value. A failure about a member that the mapping leaves out, as
// [missingMember] finds one, takes the location that
// [converter.atMissing] gives it instead.
//
// A failure about the size or the elements of a mapping or a sequence,
// such as one of maxItems, carries the path of that value. The error
// binds at the key of the entry that holds the value, or at the "-" of
// the value when it is a sequence element, as [niceyaml.AtPath]
// describes.
//
// The path spells each key as the source does. So a key that the decoder
// respells, such as 0x10 for the member name 16, still names its member.
// Without a root, the path spells each key as the decoder does. A caller
// of [Schema.ValidateValue] does not pass a root.
//
// When the walk cannot write a spelling that selects the member, as
// [sourcePath] describes, the path keeps the decoded name. That path then
// selects another entry or does not select a node. A second option gives
// the position of the node that the walk reached, as
// [datapath.Target.Token] finds it. The error binds at that position, and
// the path names the member only in the message.
func (c converter) at(e *jsonschema.ValidationError) []niceyaml.ErrorOption {
	target := sourcePath(c.root, c.idx, e.InstanceSegments())

	if name, ok := missingMember(e); ok {
		return c.atMissing(target, name)
	}

	return c.atTarget(target, constrainsKey(e))
}

// atTarget returns the options that give an error the location target,
// at the key of its member when key is set, as [converter.at] describes.
func (c converter) atTarget(target datapath.Target, key bool) []niceyaml.ErrorOption {
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
// the value. The keywords are additionalProperties, which does not allow
// a member of that name, and propertyNames, which the name of the member
// breaks.
func constrainsKey(e *jsonschema.ValidationError) bool {
	switch e.Keyword {
	case jsonschema.KeywordAdditionalProperties, jsonschema.KeywordPropertyNames:
		return true
	default:
		return false
	}
}

// atMissing returns the options that locate a failure about the member
// name, which the mapping at target leaves out. The first is the path of
// the missing member, so the path of the violation names that member.
// That path does not select a node, and the error binds at the key of
// the mapping, as [niceyaml.SourceError.Nearest] describes. The source
// does not spell a key for the member, so the path names it as the
// schema does.
//
// When the path binds somewhere other than the mapping, a second option
// gives the position of the mapping, and the error binds at that
// position. The path binds elsewhere when the path to the mapping
// selects another entry, as [datapath.Target.Token] describes. It also
// binds elsewhere when a selector with name selects a key of the
// mapping, as [datapath.Index.SelectsEntry] reports. The member that
// such a key sets has another name. For example, the key 0x10 sets the
// member 16 under a schema that requires 0x10.
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

// The text that the validator puts in front of the name of a missing
// member, in the message of each keyword that requires one. The name
// follows, quoted as [strconv.Quote] writes it.
const (
	requiredPrefix  = "missing required property "
	dependentPrefix = "property "
	dependentInfix  = " requires property "
)

// missingMember returns the name of the member that e reports missing
// from the mapping at its instance location, and true. Such a failure
// comes from required, from dependentRequired, and from the list form of
// the legacy dependencies. It reports false for any other failure.
//
// The validator carries the name only in the message, so missingMember
// reads it from there. The message reads `missing required property
// "name"` for required, and `property "port" requires property "name"`
// for dependentRequired and dependencies. For a message of another form,
// missingMember reports false.
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
