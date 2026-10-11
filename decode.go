package niceyaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/decodetree"
	"go.jacobcolvin.com/niceyaml/internal/docparse"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/internal/multierr"
	"go.jacobcolvin.com/niceyaml/internal/nilness"
	"go.jacobcolvin.com/niceyaml/internal/pathindex"
	"go.jacobcolvin.com/niceyaml/internal/rejection"
	"go.jacobcolvin.com/niceyaml/internal/segment"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// SelfValidator is a type that validates itself after a decode. A decode
// calls Validate on every value in the result that implements it, and on
// the values below a value before the value itself. Validate reports a
// problem as an [*Error] with an `@` path, which reads from the value,
// and the decode puts that path under the path of the value in the
// document. A type thus checks its invariants once and reports the right
// lines in any document. The document is at fault for every error a
// Validate returns, so [IsInvalid] reports it.
//
// # Checked Values
//
// [Node.Decode] and [Node.DecodeInto] call Validate after decoding on
// every value in the decoded value that implements it: the value
// itself, and each field, element, or map entry below it, however deep,
// unless [WithSelfValidation] switches that off.
//
// A check that belongs to the caller rather than the type, such as one
// that needs a registry of known names, runs on the decoded value after
// Decode returns. [Node.DataLocator] finds each value the check names in
// the document, and [Node.Bind] binds the result to the document the
// value came from:
//
//	cfg, err := doc.Decode[Config](ctx, settings)
//	if err != nil {
//		return err
//	}
//
//	loc := doc.DataLocator()
//
//	var errs []error
//
//	for port, server := range cfg.Ports {
//		if !known[server.Name] {
//			errs = append(errs, niceyaml.NewError("unknown server", loc.At("ports", strconv.Itoa(port), "name")))
//		}
//	}
//
//	return doc.Bind(errors.Join(errs...))
//
// The check costs no second decode and reads the value the options of
// the caller decoded. The locator spells a key as the document does. The
// error "c.yaml:3:11: $.ports.0x10.name: unknown server" thus names an
// entry the document writes under 0x10.
//
// A check that must run inside the decode, so a [DecodeOption] carries it
// to every decode and its failures report beside the others, is a
// [Validator]. Such a Validator decodes the node itself with
// [Node.Decode] and checks the value it gets, as the Decoded Data
// section of Validator describes.
//
// Any value with a Validate method takes part, including one from a
// package that names its own check that way, such as a generated
// message type. A decode runs those checks too and reports their errors
// at the value that owns the method. A Validate that the value's own
// UnmarshalYAML already called runs a second time inside the decode, so
// a Validate that rewrites its value should be idempotent. An error from
// that first call reports as a decode error, which matches [ErrDecode],
// rather than as a self-validation error.
//
// # Paths
//
// An [*Error] the value returns writes an `@` path, which reads from the
// value itself. The decode puts it under the path of the value in the
// document, and leaves a `$` path, which reads from the root of the
// document, as it is. The path of the value names a field by the name
// go-yaml decoded it under, from its yaml tag, its json tag, or its
// lowercased name. It names an element by its index and a map entry by
// its key. A map key validates too, under the path of its entry with a
// `~` after it, so its errors point at the key rather than the value. The
// decode reads the roles of the errors as [Rebase] does. A problem with
// no location points at the value, whether it stands alone or under a
// summary from [NewSummary], and a detail from [WithDetails] gains no
// location. A type thus checks its invariants once and reports the right
// lines in any document:
//
//	type Config struct {
//		Hours Hours  `yaml:"hours"`
//		Items []Item `yaml:"items"`
//	}
//
//	func (h Hours) Validate() error {
//		if h.Close.Before(h.Open) {
//			return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Current().Child("close")))
//		}
//
//		return nil
//	}
//
// A decode of Config reports $.hours.close from Hours and $.items[2].price
// from an Item, with Config declaring no Validate of its own.
//
// # Nested Values
//
// The values below a value validate first, and the value validates only
// when every one of them passed. A parent that checks a relation between
// its fields thus sees fields that hold together, and a decode reports
// every value that failed. A field an inline tag flattens keeps the path
// of the struct that holds it. A flattened field whose name a field of
// that struct also uses does not validate, since go-yaml sets it to its
// zero value and decodes the document into the other field. A node type
// of the go-yaml ast package validates nothing below it, since go-yaml
// sets it to the node it decodes. A struct that embeds a node decodes
// field by field, so its fields validate. A parent need not call the
// Validate of its fields, and [Rebase] is for a check run on a value
// after Decode returns.
//
// # Self-Decoding Values
//
// A value whose type decodes itself validates as any other value does,
// and so does every value below it. A type decodes itself through an
// UnmarshalYAML or UnmarshalText method, through a function from
// [WithCustomUnmarshaler], or through an UnmarshalJSON method under
// [WithJSONUnmarshalers]. A method that sets defaults and then decodes
// the fields thus keeps every check below its type.
//
//	func (s *Server) UnmarshalYAML(unmarshal func(any) error) error {
//		type plain Server
//
//		*s = Server{Port: 8080}
//
//		return unmarshal((*plain)(s))
//	}
//
// Such a method may fill its value from anything, so the fields need not
// mirror the document. The decode names each field of the struct by its
// tag all the same, as it names the fields of any struct. A field the
// method leaves as it was, or fills from anything but the document,
// validates too. The document may thus hold nothing at the path of an
// error at or below such a value. A path that names a key a mapping
// leaves out binds at the key of that mapping, as it does below any
// struct. Any other path the document does not hold binds at the nearest
// value that decodes itself, which is the value that returned the error
// or one above it. The error keeps its path, and [SourceError.Nearest]
// returns the path of the value it bound at. An Addr that decodes from
// the text "db:0" thus reports its port on that text:
//
//	app.yaml:2:7: $.addr.port: port must be at least 1
//
// A slice, an array, or a map that decodes itself has no tags, and its
// method may sort its elements, drop some, or key them anew. No path
// below such a value says where an element sits in the document, so
// every error below it binds at the value.
//
// The tags of a struct that decodes itself are how its author says where
// each field sits. A field tagged `yaml:",inline"` reads the mapping of
// the struct, so the errors below it report their own lines. That suits
// a struct that holds one of several types, which a key of its mapping
// names.
//
//	type Box struct {
//		Store Store `yaml:",inline"`
//	}
//
// Without the tag, the decode looks for the Store under a key named
// store, and each error below it binds at the Box. A field tagged
// `yaml:"-"` does not validate, so the Validate of the struct checks it,
// and walks the values below it with [SelfValidateValue]. The decode
// reads every other field itself and runs that Validate only when they
// all pass, so no field reports twice. A struct whose method reorders a
// slice field keeps the tag of that field, so an error under one of the
// elements reports at the element the document holds at that index.
//
// The decode cannot see a type that a function from
// [yaml.RegisterCustomUnmarshaler] decodes in every decode of the
// program. The values below such a type validate the same way, but the
// decode reads it as a type that decodes field by field. An error below
// it whose path resolves to nothing thus has no value to bind at, and an
// error below a slice or a map of such a type reports at the element its
// path names. A program gives that function to WithCustomUnmarshaler
// instead.
//
// # Map Keys
//
// A Validate holds no [Node], so it has no [DataLocator] to find how the
// document spells a key. A parent that checks the entries of a map and
// builds each path from the Go key names the key as the decoder read it,
// such as 16 for the key 0x10, and that path misses the entry. The check
// of an entry belongs in a Validate of the entry's own type instead. The
// decode puts the errors of that Validate under the key as the document
// spells it:
//
//	type Config struct {
//		Ports map[int]Server `yaml:"ports"`
//	}
//
//	func (s Server) Validate() error {
//		if s.Name == "" {
//			return niceyaml.NewError("name is required", niceyaml.AtPath(paths.Current().Child("name")))
//		}
//
//		return nil
//	}
//
// A decode of Config then reports $.ports.0x10.name for the entry under
// the key 0x10.
//
// # Error Text
//
// [Error.Error] carries no location, so a Validate may add context
// around an Error with [fmt.Errorf] at any depth. The text the wrapper
// writes holds no path, and the decode puts the position and the joined
// path in front of the whole text:
//
//	cafe.yaml:4:10: $.hours.close: hours check: closes before it opens
//
// A caller that calls Validate itself holds an error that no binding
// holds yet, and its message names no path. Such a caller reads it with
// [FormatError], which puts the path in front of the text the same way.
// That path is the one the value wrote, so it reads from the value:
//
//	@.close: hours check: closes before it opens
//
// A caller that returns or logs the error binds it with [BindValue]
// instead. The text of that error names each path wherever it prints,
// and a document can still place it. [SelfValidateValue] runs the
// Validate of every value below the value too, as a decode does, and
// binds the result the same way.
//
// # Embedded Fields
//
// A struct does not own a Validate it gets from an embedded field, so the
// method runs once, on that field at the field's own path. The method
// does not run when the field is nil or ignored. A struct that decodes
// itself through an UnmarshalYAML or UnmarshalText method it gets from an
// embedded field decodes the document into that field, so the field
// validates at the path of the struct. Go-yaml decodes no other field of
// such a struct, so none of them validates.
//
// # Deferred Validation
//
// A check that reads state the caller fills in after the decode, such as
// a field a library sets from the environment or a flag, runs through
// [Node.SelfValidate]. The caller decodes with [WithSelfValidation] off,
// fills in that state, and then calls SelfValidate on the value, which
// walks it as the decode would have. SelfValidate describes where an
// error binds when the value no longer mirrors the document, such as
// under a field the document lacks.
//
// # Fault
//
// A Validate checks the value, so the document is at fault for every
// error it returns, with a location or without, and [IsInvalid] reports
// it. That holds for an error of I/O too, such as one from a check that a
// named file exists, and for an error inside [Place]. A Validate whose
// I/O can fail for reasons outside the document thus leaves that check to
// the caller, or to a [Validator], which declares each error itself.
//
// # Cancellation
//
// A Validate that returns the error of a context that ended, one that
// matches [context.Canceled] or [context.DeadlineExceeded], stops the
// walk. The decode returns that error alone, with no location, in place
// of every error the walk found before it, as [MultiValidator] does.
// The walk stops the same way once the context of the decode ends, and
// the decode then returns the error of that context.
type SelfValidator interface {
	Validate() error
}

// Validator validates a [*Node] before it decodes, as a JSON schema does,
// or a schema registry that picks the schema from the document's content
// or file path. Pass one to [Node.Decode] with [WithValidator], or run
// one on its own with [Node.Validate]. Check checks the Node it gets,
// which is the root of a document or a Node from [Node.At], and returns
// its errors as it wrote them from that Node: an [*Error] whose `@` path
// reads from the Node, a plain error, or an error a Node bound already.
// Check is the raw step, and the validators this module ships return
// their errors that way too. The Check of
// [go.jacobcolvin.com/niceyaml/schema.Registry] is the exception and
// binds every error it returns. Node.Validate and a decode bind what Check
// returns, as [Node.Bind] binds an error, so the error names the source
// the validator read and its `@` paths resolve from the Node. An Error
// from [NewError] or [Invalid] says what is wrong with the document, and
// any other error says the check could not run. A document that did not
// parse never reaches a validator that a decode or Node.Validate runs.
//
// See [ValidatorFunc], [MultiValidator], [ChainValidator], [SkipEmpty],
// [go.jacobcolvin.com/niceyaml/schema.Schema], and
// [go.jacobcolvin.com/niceyaml/schema.Registry] for implementations.
//
// # Scope
//
// The Node is the scope that runs the validator: the root of a whole
// document, or the node a Node from [Node.At] selects, so a validator
// given to a scoped decode checks that node and its `@` paths resolve
// from it. A validator that needs the whole document reaches it through
// [Node.Document]. One that can only check
// a whole document, as a [go.jacobcolvin.com/niceyaml/schema.Registry]
// can, refuses a scoped Node with an error instead of checking the
// document around it, so a caller validates once at the root and decodes
// the nodes below it without that validator.
//
// # Decoded Data
//
// A validator that checks the decoded data reads the node with
// [Node.Decode], which runs the validators the caller passes and no
// other, so a validator never runs itself again. That decode reads the
// node with the settings of its [Source], such as the reference
// documents of [WithReferences], so a validator reads an alias as every
// decode of the Source reads it. A [DecodeOption] reaches the decode that
// gets it and no other, so a validator that decodes a type a
// [WithCustomUnmarshaler] function decodes gives that option to its own
// decode. The context carries cancellation and deadlines to validators
// doing cancellable work, such as remote schema reference resolution:
//
//	func (s *Schema) Check(ctx context.Context, n *niceyaml.Node) error {
//		data, err := n.Decode[any](ctx)
//		if err != nil {
//			return err
//		}
//
//		return s.check(ctx, data)
//	}
//
// The names of that data are not the keys of the document, since the
// decoder respells a key such as 0x10, which sets the member 16. A
// validator that reports where a finding lies in the data takes each
// location from [Node.DataLocator], which reads the names as the decoder
// does.
//
// A validator that decodes into a type of the program follows three
// rules. It takes the options of the decode that runs it and gives them
// to its own decode. It passes [WithSelfValidation] off to that decode,
// since the decode that runs it calls each Validate method on its own
// result. And one validator decodes for every check on the type, since
// each validator that decodes reads the node again, and two of them
// under [MultiValidator] report one rejection of the decoder twice:
//
//	func typed(settings niceyaml.DecodeOption, check func(*niceyaml.DataLocator, *Config) error) niceyaml.Validator {
//		return niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
//			cfg, err := n.Decode[Config](ctx, settings, niceyaml.WithSelfValidation(false))
//			if err != nil {
//				return err
//			}
//
//			return check(n.DataLocator(), &cfg)
//		})
//	}
//
//	settings := niceyaml.DecodeOptions(niceyaml.WithDisallowUnknownFields(true))
//	cfg, err := doc.Decode[Config](ctx, settings, niceyaml.WithValidator(typed(settings, check)))
//
// # Errors
//
// A validator writes the path of an error from the Node it got, as a
// [SelfValidator] writes it from the value it checks. A key the
// document lacks has no Node, so its error carries the path of the key,
// and the Node that runs the validator points the error at the key of
// the mapping that lacks the value, as Node.At describes:
//
//	return niceyaml.NewError("kind is required", niceyaml.AtPath(kindPath))
//
// A validator that read a value through a Node from [Node.At] names the
// value the same way, with the path it scoped the Node by. A path that a
// Node hands out, such as [Node.Path] of a Node from [Node.Nodes], starts
// at `$`, so it names the same value through the Node the validator got,
// whatever the scope of that Node:
//
//	for _, item := range items {
//		errs = append(errs, niceyaml.NewError("bad item", niceyaml.AtPath(item.Path())))
//	}
//
// An error with no location binds at the value the Node selects, as
// [Node.Bind] describes, so a validator that runs on a Node from Node.At
// or Node.Nodes reports a plain error at that value.
//
// A validator binds an error itself, through [Node.Bind], when it wants
// the error somewhere other than the Node it got. One that scopes a Node
// with Node.At and binds through it reports the value it read, and one
// that binds through [Node.Document] gives an error that is no fault of
// the value, such as a schema that does not load, no location. A bound
// error comes back from the Node that runs the validator as it is, since
// Node.Bind leaves a bound error as it is.
//
// A bound error keeps its text, so context a validator wraps around a
// bound error stands in front of the position, as in
// "config schema: svc.yaml:2:7: $.port: 0 is less than 1". The binding
// puts the position in front of context wrapped around an unbound
// error instead, as in "svc.yaml:2:7: $.port: config schema: 0 is less
// than 1". A validator that wants its context in front thus binds first.
//
// # Nested Validators
//
// A validator that runs another on a Node of its own choosing, such as
// each element of a list, runs it through [Node.Validate] and returns
// the error as it is:
//
//	for _, item := range items {
//		if err := item.Validate(ctx, inner); err != nil {
//			return err // bound at $.items[i]
//		}
//	}
//
// A call to inner.Check(ctx, item) returns the error as inner wrote it,
// with its `@` paths reading from item. The Node that runs the outer
// validator then binds it and resolves those paths from itself. In the
// c.yaml below, a rule that reserves the name admin rejects the second
// item:
//
//	name: lunch
//	items:
//	  - name: soup
//	  - name: admin
//
// The rule writes its error at `@.name`. The outer validator reports one
// of these lines, by the call it makes:
//
//	item.Validate(ctx, inner)   c.yaml:4:11: $.items[1].name: reserved name
//	inner.Check(ctx, item)      c.yaml:1:7: $.name: reserved name
//
// When the item is a Node of another source, the error names the wrong
// file too, since it takes the source of the Node that binds it.
//
// # Fault
//
// A validator declares which of its errors are the fault of the document.
// It returns an [*Error] from [NewError] or [Invalid] to report what is
// wrong with the document, with a location or without, and [IsInvalid]
// reports that Error. An error of the validator's own type declares the
// fault inside Invalid, where [errors.As] still finds it. The validator
// returns any other error when the check itself could not run, such as an
// I/O error, and IsInvalid does not report that error. A location changes
// neither. [Place] shows an error of the second kind at the value the
// check read, with the options NewError and Invalid take, and declares
// no fault:
//
//	_, err := os.Stat(spec.License)
//	switch {
//	case errors.Is(err, fs.ErrNotExist):
//		return niceyaml.NewError("license file does not exist", niceyaml.AtPath(licensePath))
//	case err != nil:
//		return niceyaml.Place(fmt.Errorf("stat license: %w", err), niceyaml.AtPath(licensePath))
//	}
//
// The first error is the fault of the document, which names a file that
// does not exist. The second binds at the same value, as in
// "c.yaml:3:12: $.spec.license: stat license: permission denied", and it
// stays a check that could not run, which IsInvalid does not report.
//
// # Origin
//
// A validator that resolves such a file beside the file that names it
// reads the directory from [Node.Origin] of the Node of the value, and
// not from [Node.FilePath] of the Node it got. In the Source
// [NewSourceFromLayers] builds, that Node belongs to the merged
// document, which has the file path of the lowest layer, whichever layer
// holds the value. Origin returns the Node of the value in the file of
// its layer, and the receiver where the validator runs on one file.
//
// # Unparsed Documents
//
// A document with a YAML syntax error has no tree to check. Node.Validate
// and a decode return the syntax error [Node.Err] returns before any
// validator runs, so a validator they run always gets a document that
// parsed. A caller that calls Check itself can hand it a Node whose
// document did not parse. [Node.Decode], [Node.At], and [Node.Nodes]
// return the syntax error for such a Node, and a validator passes that
// error on. [ValidatorFunc], [MultiValidator], [ChainValidator], and the
// validators of [go.jacobcolvin.com/niceyaml/schema] return it before
// they read the Node.
type Validator interface {
	Check(ctx context.Context, n *Node) error
}

// ValidatorFunc adapts a function to the [Validator] interface.
//
//	kindPath := paths.Current().Child("kind")
//	known := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
//		node, err := n.At(kindPath)
//		if err != nil {
//			return err
//		}
//
//		kind, err := node.Decode[string](ctx)
//		if err != nil {
//			return err
//		}
//
//		if kind != "Deployment" {
//			return niceyaml.NewError("unknown kind", niceyaml.AtPath(kindPath))
//		}
//
//		return nil
//	})
//
// The function writes its errors from the Node it got, as [Validator]
// asks, and Check returns what the function returns. The body of a
// ValidatorFunc is thus also right as the Check method of a type, and
// the two return the same error for the same Node.
//
// A check that knows nothing of YAML returns a plain error, which
// declares no fault. The function declares the document at fault for
// that error by returning the check inside [Invalid], which returns nil
// for a value that passes:
//
//	return niceyaml.Invalid(check(v))
type ValidatorFunc func(ctx context.Context, n *Node) error

// Check implements [Validator]. It returns what f returns, as f wrote
// it, so a nil [*Error] or [*SourceError] pointer from f comes back as
// that pointer, which [Node.Validate] reads as no failure. For a
// document that did not parse, it returns the syntax error [Node.Err]
// returns without calling f.
func (f ValidatorFunc) Check(ctx context.Context, n *Node) error {
	if n.doc.err != nil {
		return n.doc.err
	}

	return f(ctx, n)
}

// MultiValidator returns a [Validator] that runs every validator in
// order and reports every failure, where [ChainValidator] stops at the
// first that fails. It suits rules that check a document independently,
// as the rules of a linter do, such as a schema and a check on the names
// it uses, so one run reports the violations of both:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(niceyaml.MultiValidator(schema, names)))
//
// Each validator sees the node whatever the ones before it reported, so
// a validator that decodes the node reports its own failure beside a
// schema violation of the same value. A validator that needs an earlier
// one to have passed goes in a ChainValidator instead. The run skips a
// nil validator, and one that holds a nil pointer or func.
//
// Two or more failures come back joined in the order given, each as the
// validator wrote it, so [errors.Is] matches any one of them. The Node
// that runs the MultiValidator binds the join as one tree, as [Node.Bind]
// describes, so each failure binds at its own location and each line of
// the message carries the source and the position of its own failure:
//
//	svc.yaml:1:7: $.name: reserved name
//	svc.yaml:2:7: $.port: 0 is less than 1
//
// The message of that binding lists the failures in the order of their
// positions, as the tree shows them.
//
// A lone failure comes back as it would if that validator ran alone. No
// failure is no error. A context that ends stops the run, and the error
// is then the one the context reports, or the one the validator that saw
// it end returned.
//
// MultiValidator copies the validators it gets, so a caller that edits
// the slice it passed changes nothing in the Validator.
func MultiValidator(validators ...Validator) Validator {
	validators = slices.Clone(validators)

	return ValidatorFunc(func(ctx context.Context, n *Node) error {
		var errs []error

		for _, dv := range validators {
			if nilness.IsNil(dv) {
				continue
			}

			err := ctx.Err()
			if err != nil {
				return err //nolint:wrapcheck // The context names the reason, and the Node that runs the validator binds it.
			}

			// A typed nil pointer is no failure, as [Node.Bind] reads it.
			err = dv.Check(ctx, n)
			if isNothing(err) {
				continue
			}

			if ctx.Err() != nil {
				return err //nolint:wrapcheck // The validator's own error, which the Node that runs the validator binds.
			}

			errs = append(errs, err)
		}

		return multierr.Join(errs)
	})
}

// ChainValidator returns a [Validator] that runs the validators in order
// and stops at the first that fails, where [MultiValidator] runs every
// one. It suits a check that needs an earlier one to pass, such as a
// check that follows the references of a document once a schema has
// accepted its shape:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(niceyaml.ChainValidator(schema, refs)))
//
// A validator runs only after every validator before it has passed, so
// it can trust the shape the schema checked, and one that decodes the
// node reports no value the schema already rejected. Rules that check a
// document independently go in a MultiValidator instead, which reports
// the failures of all of them. The run skips a nil validator, and one
// that holds a nil pointer or func, and a ChainValidator of no
// validators passes every Node.
//
// A decode runs the validators of repeated [WithValidator] options the
// same way, so these two decodes return the same error:
//
//	doc.Decode[Config](ctx, niceyaml.WithValidator(niceyaml.ChainValidator(schema, refs)))
//	doc.Decode[Config](ctx, niceyaml.WithValidator(schema), niceyaml.WithValidator(refs))
//
// The failure comes back as the validator wrote it, so the Node that
// runs the ChainValidator binds it as it binds the failure of that
// validator alone.
//
// ChainValidator copies the validators it gets, so a caller that edits
// the slice it passed changes nothing in the Validator.
func ChainValidator(validators ...Validator) Validator {
	validators = slices.Clone(validators)

	return ValidatorFunc(func(ctx context.Context, n *Node) error {
		return n.firstFailure(ctx, validators)
	})
}

// SkipEmpty returns a [Validator] that passes a document with no content
// and runs v on every other Node. [Node.IsEmpty] reports such a
// document: an empty file, a file of comments alone, or a "---" header
// with nothing but comments below it.
//
// An empty document validates as any other, so a schema that wants a
// mapping rejects it, and a registry that requires a schema finds none
// for it. [Source.Documents] and [Source.ValidateDocuments] leave out the
// empty documents of a file that holds a document with content, and keep
// the one document of an empty file. A caller whose file may be empty as
// a whole wraps its validator:
//
//	err := source.ValidateDocuments(ctx, niceyaml.SkipEmpty(reg))
//
// So does a caller that validates each document [Source.AllDocuments]
// returns with [Node.Validate], where every empty document runs the
// validator.
//
// A decode takes it the same way, so a configuration file that may be
// empty decodes to the zero value:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(niceyaml.SkipEmpty(schema)))
//
// SkipEmpty passes only the documents IsEmpty reports. A document that
// holds a value runs v, whatever the value, so v still checks an
// explicit null such as `--- null` and an alias with no anchor before
// it. A document that did not parse returns its syntax error. A Node
// that [Node.At] or [Node.Nodes] scopes below the root is not a
// document, so v runs on it.
//
// SkipEmpty returns the error of v as v wrote it, and a nil v passes
// every Node. SkipEmpty takes one validator, and it wraps the one that
// [MultiValidator] or [ChainValidator] makes of several.
func SkipEmpty(v Validator) Validator {
	return ValidatorFunc(func(ctx context.Context, n *Node) error {
		if n.IsEmpty() || nilness.IsNil(v) {
			return nil
		}

		//nolint:wrapcheck // The validator's own error, which the Node that runs the validator binds.
		return v.Check(ctx, n)
	})
}

// newDocuments creates the root [*Node] of each of docs, the YAML
// documents of the file src parsed, in file order. A document that did not
// parse gets a Node too, which carries the error in errs of its run, and
// [bindSyntaxErrors] binds that error to its document.
func newDocuments(src *Source, docs []docparse.Document, errs []error) []*Node {
	nodes := make([]*Node, len(docs))

	for i, parsed := range docs {
		doc := &document{
			root:       parsed.Root,
			tokens:     parsed.Tokens,
			positioned: parsed.Positioned,
			index:      i,
			fileIndex:  parsed.FileIndex,
			preamble:   parsed.Preamble,
		}

		if parsed.Failure >= 0 {
			doc.err = errs[parsed.Failure]
		}

		doc.node = &Node{source: src, doc: doc, content: doc.tokens, base: paths.Doc(), span: parsed.Span}
		doc.tree = sync.OnceValue(doc.newDecodeTree)
		doc.pathIndex = sync.OnceValue(func() *pathindex.Index { return pathindex.New(doc.root.Body) })
		doc.state = aliasing.NewState(!src.skipAliasLimit, func() (ast.Node, *paths.Resolver) {
			return doc.root.Body, doc.pathResolver()
		})
		nodes[i] = doc.node
	}

	bindSyntaxErrors(nodes)

	return nodes
}

// bindSyntaxErrors binds the syntax error of each of docs that did not
// parse to the root of the document it belongs to. The parser reports an
// error before any document exists, so the error arrives bound to the
// source alone. The documents of one run follow one another and share its
// error, so the error belongs to the one of them whose span holds its
// line. An error that resolved no location belongs to the first of them.
// The error stays one binding with one document, whichever of the
// documents returns it.
func bindSyntaxErrors(docs []*Node) {
	for i, doc := range docs {
		if doc.doc.err == nil {
			continue
		}

		for bound := range allBindings(doc.doc.err) {
			// An earlier document of the run bound the error already.
			if bound.node != nil {
				continue
			}

			bound.node = doc

			if bound.locErr != nil {
				continue
			}

			// The parse hands every document of a run the one binding of
			// its error, so the documents that follow with that error are
			// the rest of the run.
			for _, other := range docs[i:] {
				if !errors.Is(other.doc.err, doc.doc.err) {
					break
				}

				if other.span.Contains(bound.loc.pos.Line) {
					bound.node = other

					break
				}
			}
		}
	}
}

func init() {
	aliasing.StateOf = documentState
}

// documentState returns the [*aliasing.State] of the document of n, which
// must be a [*Node], or nil for a nil Node. It is [aliasing.StateOf],
// through which the packages of the module reach the state a document
// keeps.
func documentState(n any) *aliasing.State {
	node, ok := n.(*Node)
	if !ok {
		panic(fmt.Sprintf("aliasing.StateOf: %T is not a *niceyaml.Node", n))
	}

	if node == nil {
		return nil
	}

	return node.doc.state
}

// document is what describes one YAML document of a [Source] as a whole:
// its root, the tokens of the whole document, its index in the file, the
// length of its preamble, and the resolver its paths resolve through.
// Every [Node] of the document shares it.
type document struct {
	// The root Node of the document.
	node *Node
	// The node the document parsed into. For a document that did not
	// parse, it is the stand-in [docparse.Parse] made, which has no body.
	root *ast.DocumentNode
	// The syntax error of a document that did not parse, bound to the
	// Source and to the document bindSyntaxErrors picks for it, which is
	// nil for one that parsed.
	err error
	// Resolves every path in root, which pathResolver creates when the
	// first path needs it. No Node edits the tree, so the resolver binds
	// the aliases once however many paths the Nodes of the document
	// resolve.
	resolver *paths.Resolver
	// Returns the tree the go-yaml decoder reads for the document, and
	// builds it on the first call.
	tree func() *decodetree.Tree
	// Returns the index that finds the node of root at a position, and
	// builds it on the first call of PathAt.
	pathIndex func() *pathindex.Index
	// The state that the packages of the module reach through
	// aliasing.StateOf.
	state *aliasing.State
	// The tokens of the whole document.
	tokens token.Tokens
	// The tokens that carry a position, in the order of their offsets,
	// which the extent of a Node searches.
	positioned token.Tokens
	index      int
	// The index of root in the Docs of the file the Source parsed, which
	// can differ from index, since preamble nodes fold into documents.
	fileIndex int
	// The number of tokens at the start of tokens before the content.
	preamble int
	// Creates resolver once, for the first path any Node resolves.
	resolverOnce sync.Once
}

// pathResolver returns the [paths.Resolver] for the document, and creates
// it on the first call. A document that did not parse gets a resolver
// with no document, so every path returns an error there. A resolver for
// its stand-in root would list no nodes for a path and return no error,
// as it does for an empty document.
func (d *document) pathResolver() *paths.Resolver {
	d.resolverOnce.Do(func() {
		if d.err != nil {
			d.resolver = paths.NewResolver(nil)

			return
		}

		d.resolver = paths.NewResolver(d.root)
	})

	return d.resolver
}

// Node is a scope in a YAML document: the root of the document, which
// [Source.Documents] and [Source.Document] return, or a node a path
// selects. [Node.At] returns the one node a path selects, and
// [Node.Nodes] returns one Node per match of a path that can select
// several, such as one with a `[*]` or `.*` selector. Every method reads
// from the node and resolves an `@` path from it, so [Node.Decode]
// decodes it alone, [Node.Validate] runs a [Validator] on it, and
// [Node.Bind] resolves the `@` paths of an error from it, so a check
// written for the type of a value reports the same lines whether the
// value is the whole document or one inside it. A `$` path resolves from
// the root of the document through any Node.
//
// Receive instances from [Source.Documents], [Source.AllDocuments],
// [Source.Document], [Node.At], [Node.Nodes], [Node.Document],
// [SourceError.Node], or [SourceError.Document].
//
// # Decoding
//
// [Node.Decode] returns a new value and [Node.DecodeInto] fills one the
// caller already holds, such as one pre-populated with defaults. Both run
// the same pipeline. Each [Validator] given with [WithValidator] checks
// the node before decoding, and a value that implements [SelfValidator]
// validates itself after, unless [WithSelfValidation] switches that off.
// [Node.Validate] runs the first step on its own, and [Node.SelfValidate]
// runs the last, on a value the caller may have changed since the decode.
//
//	for _, doc := range docs {
//		config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(validator))
//		if err != nil {
//			return err
//		}
//	}
//
// [Source.Documents] returns the documents of such a loop. It leaves out
// the empty documents beside one with content, so the loop decodes the
// documents with content. A file with no content holds an empty document,
// which [Node.IsEmpty] reports. A validator that wants a mapping rejects
// it, and a decode with no such validator returns the zero value. A
// source that holds one document hands its root out from
// [Source.Document].
//
// A Node holds the Source it came from, and every decoding method binds
// the [Error] values it produces to that source, so the errors it returns
// carry a [SourceError] that renders the offending lines.
//
// # Scoped Nodes
//
// A Node from [Node.At] or [Node.Nodes] is scoped to the node a path
// selects. Decode decodes that node alone, which reads one value without
// decoding the whole document, such as a discriminator field that routes
// the document. [Node.DecodeAt] scopes and decodes in one call, for a
// caller that needs the value and not the Node, and
// [Node.DecodeIfPresent] does so for a value the document may leave out.
// Bind resolves the `@` paths in an error from the node, so a check
// written for the type of that value reports the right lines.
// The bound error carries each path from the root of the document, as a
// `$` path, so it names the value as a decode of the whole document does.
// Any Node reaches the root of its document through [Node.Document], and
// [Node.Path] is [paths.Doc] for the root and the `$` path to the node
// for a scoped Node.
//
// # Whole Document
//
// [Node.DocumentAST], [Node.DocumentIndex], [Node.Preamble], and
// [Node.FilePath] describe the document as a whole, whatever Node of it a
// caller holds. [Node.PathAt] reads the whole document too. It returns
// the `$` path of the node at a position, so a viewer names the value
// under its cursor.
//
// # Unparsed Documents
//
// [Source.AllDocuments] returns a Node for a document with a YAML syntax
// error too. Such a document has no tree, and [Node.Err] returns the
// syntax error. The methods that read the tree return that error:
// [Node.Decode], [Node.DecodeInto], [Node.DecodeAt],
// [Node.DecodeIfPresent], [Node.Validate], [Node.At], [Node.Nodes], and
// [Node.Ranges]. [Node.AST] and
// [Node.DocumentAST] return nil, and [Node.PathAt] finds no node. The
// methods that read the tokens and the lines work as they do for any
// document: [Node.Tokens], [Node.Preamble], [Node.Span], and [Node.View].
// A caller thus renders or diffs a document that does not parse yet, and
// the other documents of the file decode and validate as if it did.
//
// # Merged Documents
//
// A Node of the Source [NewSourceFromLayers] builds belongs to the
// document its layers merge into. Its Source holds the merged text,
// which is no file, so each error it returns binds in the file of a
// layer instead, as NewSourceFromLayers describes.
type Node struct {
	// The node the scope selects, which At or Nodes resolves once when it
	// scopes the Node. The root of a document leaves it unset, since its
	// node is the body of the document.
	node   ast.Node
	source *Source
	// The enclosing document.
	doc *document
	// The tokens of the node: the tokens of the whole document for its
	// root, and a sub-slice of them for a Node from At.
	content token.Tokens
	// The scope: the `$` path from the document root to the node, which is
	// paths.Doc for a whole document.
	base paths.Path
	// The lines of the source that the node covers.
	span position.Span
}

// DocumentAST returns the [*ast.DocumentNode] of the whole document the
// Node belongs to, the go-yaml node the document parsed into.
// [Node.Document] returns the root [*Node] of the same document,
// [Node.AST] returns the go-yaml node the Node selects, and [Node.Path]
// is the path from the document root to it. The node is part of the
// tree [Source.File] returns, which every Node of the Source shares and
// resolves against, so a caller must not modify it. A document that did
// not parse has no node, so DocumentAST returns nil for it, as [Node.Err]
// describes.
func (n *Node) DocumentAST() *ast.DocumentNode {
	if n.doc.err != nil {
		return nil
	}

	return n.doc.root
}

// Err returns the YAML syntax error of the document the Node belongs to,
// or nil for a document that parsed. [Source.AllDocuments] returns a Node
// for every document of a file, and a syntax error fails the one document
// that holds it. The error is the [*SourceError] the parser reported for
// that document, bound to the Source. It matches [ErrSyntax], and it is
// among the errors [Source.File] returns:
//
//	for _, doc := range source.AllDocuments() {
//		if err := doc.Err(); err != nil {
//			log.Print(niceyaml.FormatError(err))
//
//			continue
//		}
//
//		lipgloss.Println(p.Print(doc.View()))
//	}
//
// The error is bound to the document it belongs to, so
// [SourceError.Document] returns the root of that document for it,
// whether [Node.Err], [Source.File], or a method of the Node returned the
// error.
//
// A panic in the parser fails the documents it was reading as a syntax
// error does. Err then returns the error of that panic, which holds a
// [PanicError] and does not match ErrSyntax.
//
// A document that did not parse has no tree. [Node.Decode],
// [Node.DecodeInto], [Node.DecodeAt], [Node.DecodeIfPresent],
// [Node.Validate], [Node.At], [Node.Nodes], and [Node.Ranges] return the
// error Err returns, and no [Validator] runs.
// [Node.AST] and [Node.DocumentAST] return nil, and [Node.PathAt] reports
// false for every position. A path in an error that [Node.Bind] binds
// resolves nowhere, as Bind describes.
//
// A "---" header that directly follows an anchor with no value parses
// together with the document above it, as [Source.AllDocuments]
// describes. Both documents then return the same error, which is bound to
// the one of the two that holds its location. A nil Node has none.
func (n *Node) Err() error {
	if n == nil {
		return nil
	}

	return n.doc.err
}

// Resolver returns the [*paths.Resolver] of the whole document the Node
// belongs to, which every Node of the document shares. It resolves every
// path from the document root, whether it starts at `$` or `@`, as
// [paths.Resolver] describes. A path below the Node therefore starts with
// [Node.Path], which [paths.Path.Join] puts in front of an `@` path. The
// resolver follows the aliases and merge keys of the go-yaml
// nodes [Node.AST] and [Node.DocumentAST] return. The document creates
// the resolver once, when the first of its Nodes needs it, and the
// resolver binds the aliases of the document then. A [Validator] that
// runs for each item of a list therefore binds them once, where a
// resolver from [paths.NewResolver] binds them again for every item.
//
// A document that did not parse has no tree, as [Node.Err] describes, so
// its resolver holds no document, as [paths.NewResolver] of a nil
// document does. Every path it resolves returns an error wrapping
// [paths.ErrNoDocument], where [paths.Resolver.Matches] returns no
// matches and no error in an empty document that parsed.
func (n *Node) Resolver() *paths.Resolver {
	return n.doc.pathResolver()
}

// Document returns the root [*Node] of the document the Node belongs to,
// so a Node from [Node.At] reaches the whole document, as a validator
// that picks a schema from the file path or the content of the document
// does. Every Node of a document returns the root [Source.AllDocuments]
// returns, and that root returns itself. A nil Node belongs to none.
func (n *Node) Document() *Node {
	if n == nil {
		return nil
	}

	return n.doc.node
}

// AST returns the [ast.Node] the Node selects, the one [Node.Decode]
// decodes. For the root Node that is the body of the whole document, and
// for one from [Node.At] or [Node.Nodes] it is the node that method
// resolved when it scoped the Node. The text of any node, including a
// mapping or a sequence, is its String method:
//
//	scoped, err := doc.At(path)
//	if err != nil {
//		return err
//	}
//
//	fmt.Println(scoped.AST().String())
//
// The body is what the parser built: nil for an empty document, and a
// comment group for one holding only comments. When the whole file holds
// whitespace alone, or text the lexer emits nothing for such as a lone
// "!", the body of its one document is a scalar holding the placeholder
// token [tokens.Tokenize] makes, and [tokens.IsPlaceholder] tells it
// apart from a scalar the file holds. A document of whitespace below a
// "---" header has a nil body. AST returns each of these bodies as it
// is, as such a document decodes to nothing. [Node.IsEmpty] reports
// each of these documents, so a caller need not tell the bodies apart.
// A document that did not parse has no body either, and [Node.Err]
// tells it apart from an empty document. The node is part of the tree
// [Source.File] returns, which every Node of the Source shares and
// resolves against, so a caller must not modify it.
//
// The node keeps the anchor or the tag written on it, so a type switch on
// it can see an [*ast.AnchorNode] or an [*ast.TagNode] where the source
// holds a mapping. [Node.Kind] looks through those and through an alias,
// and reports whether the Node holds a mapping, a sequence, or a scalar.
func (n *Node) AST() ast.Node {
	if n.base.IsRoot() {
		return n.doc.root.Body
	}

	return n.node
}

// At returns a [*Node] scoped to the node path selects. An `@` path
// resolves from the receiver, and a `$` path from the root of the
// document, so a path from [Node.Path] or [Node.PathAt] of any Node of
// the document scopes the node it names. The Node shares the source and
// the document with the receiver, and reaches the document through
// [Node.Document]. A path that selects nothing returns an error bound to
// the source, which wraps [paths.ErrNotFound] when nothing exists at the
// path. A validator given to a scoped decode checks the node, and an `@`
// path in an error it or the decoded value reports resolves from the
// node. A check written for a type thus reports the same lines whether
// the type is the whole document or a value inside one:
//
//	hours, err := doc.At(paths.Doc().Child("spec", "hours"))
//	if err != nil {
//		return err
//	}
//
//	h, err := hours.Decode[Hours](ctx, niceyaml.WithValidator(hoursSchema))
//	if err != nil {
//		return err
//	}
//
//	return hours.Bind(check(h))
//
// A node whose tokens carry no position covers no lines and holds no
// tokens.
//
// # Errors
//
// At resolves the node to find the lines and tokens it covers, and a path
// that selects nothing returns the error [paths.Resolver.Node] describes,
// bound to the source: an error wrapping [paths.ErrNotFound] when nothing
// exists at the path, which also wraps [paths.ErrNoDocument] when the
// document has no content at all, such as an empty document or one
// holding only directives; [paths.ErrAlias] when an alias on the path
// does not resolve; [paths.ErrExcessiveMerging] when the key lookups of
// the path read far more nodes under `<<` merge keys than the document
// holds; and [paths.ErrWildcard] for a path that could match several
// nodes, which [Node.Nodes] scopes one by one.
//
// A document that did not parse has no node to select, so At returns the
// syntax error [Node.Err] returns.
//
// # Error Locations
//
// A path that names a key a mapping leaves out says where the value
// belongs, so its error binds as an [Error] with [AtPath] of the path
// binds. The error points at the key of the mapping that lacks the
// value, as [SourceError.Nearest] describes. Its message carries the
// path in front:
//
//	cfg.yaml:3:1: $.server.name: not found
//
// Any other path that selects nothing names no mapping, so its error
// carries no location. That holds for an index past the end of a
// sequence, for a key looked up in a scalar, and for any path in a
// document with no content:
//
//	cfg.yaml: resolve $.items[7]: not found
//
// The document lacks the value in each case, so [IsInvalid] reports
// every error that wraps [paths.ErrNotFound], with a location or without.
//
// # Absent Values
//
// A caller that falls back to a default when a value is absent reads it
// with [Node.DecodeIfPresent]:
//
//	version := 1
//
//	_, err := doc.DecodeIfPresent(ctx, versionPath, &version)
//	if err != nil {
//		return err
//	}
//
// A caller that needs the Node of such a value tests the error of At for
// [paths.ErrNotFound] before it decodes. A decode can return an error
// that matches paths.ErrNotFound for a node that is present, as
// [Node.DecodeAt] describes, so the same test after the decode takes
// that failure for an absent value.
func (n *Node) At(path paths.Path) (*Node, error) {
	if n.doc.err != nil {
		return nil, n.doc.err
	}

	c := *n
	c.base = n.base.Join(path)

	node, err := n.doc.pathResolver().Node(c.base)
	if err != nil {
		// Bind to the receiver, a Node that exists, rather than to the
		// copy, whose scope moved to a path that resolves to no node.
		return nil, n.bindUnresolved(path, err)
	}

	c.node = node
	c.span, c.content = n.scope(c.base, node)

	return &c, nil
}

// Nodes returns a [*Node] scoped to each node path selects, in document
// order, so a path with a `[*]`, `.*`, `..name`, or `..*` selector, which
// [Node.At] rejects, scopes every element of a sequence, every entry of a
// mapping, every entry with a name at any depth, or every node at any
// depth. An `@` path resolves from the receiver and a `$` path from the
// root of the document, as for Node.At. A path that selects nothing
// returns no Nodes and no error. Each Node is scoped as one from Node.At
// is, and [Node.Path] is the `$` path that selects its node alone, as
// [paths.Resolver.Matches] resolves it, so a validator run on each element,
// or an error bound to it, reports the element it came from:
//
//	items, err := doc.Nodes(paths.Current().Child("items").IndexAll())
//	if err != nil {
//		return err
//	}
//
//	for _, item := range items {
//		if err := item.Validate(ctx, itemSchema); err != nil {
//			errs = append(errs, err) // bound at $.items[i]
//		}
//	}
//
// # Mapping Entries
//
// A mapping of named things, such as the jobs of a workflow, takes a `.*`
// selector from [paths.Path.ChildAll]. Each Node is then scoped at the
// path of one entry, and the last selector of that path, which
// [paths.Path.Last] gives, names the entry:
//
//	jobs, err := doc.Nodes(paths.Current().Child("jobs").ChildAll())
//	if err != nil {
//		return err
//	}
//
//	for _, job := range jobs {
//		sel, _ := job.Path().Last() // .build
//		fmt.Println(sel.Name)       // build
//		// ...
//	}
//
// The name is the text the selector matches the key by, as
// [paths.Resolver.KeyName] gives it, so a key the source spells `3.10`
// has the name 3.10 and the path `$.jobs.'3.10'`. A `~` selector from
// [paths.Path.Key] reaches the key node itself, which decodes as the
// decoder reads it, so that key decodes to the string 3.1.
//
// # Recursive Selectors
//
// A `..*` selector from [paths.Path.RecursiveAll] scopes every node below
// the receiver, each once, where the source writes it. A check that
// applies to every key of a document thus visits each key once:
//
//	nodes, err := doc.Nodes(paths.Current().RecursiveAll()) // @..*
//	if err != nil {
//		return err
//	}
//
//	for _, n := range nodes {
//		sel, ok := n.Path().Last()
//		if ok && sel.Kind == paths.SelectorChild && !snake.MatchString(sel.Name) {
//			errs = append(errs, niceyaml.NewError(
//				fmt.Sprintf("key %q is not snake_case", sel.Name),
//				niceyaml.AtPath(n.Path().Key())))
//		}
//	}
//
// # Empty Results
//
// A path that selects nothing returns no Nodes and no error, as
// [paths.Resolver.Nodes] does. A document with no content, which
// [Node.IsEmpty] reports, holds nothing for a selector to reach. A path
// with selectors returns no Nodes there too, where Node.At returns an
// error wrapping [paths.ErrNoDocument]. A loop over the items of each
// document of a file thus passes over an empty document. The root path
// selects the null at the "---" header of such a document, and nothing
// in one without a header, such as an empty file.
//
// # Errors
//
// The errors [paths.Resolver.Nodes] returns come back bound to the source:
// one wrapping [paths.ErrAlias] when an alias on the path does not
// resolve, [ErrExcessiveAliasing] when aliases lead a selector of the
// path to far more nodes than the document holds, and
// [paths.ErrExcessiveMerging] when the key lookups of a selector read far
// more nodes under `<<` merge keys than that. A document that did not
// parse returns the syntax error [Node.Err] returns.
func (n *Node) Nodes(path paths.Path) ([]*Node, error) {
	if n.doc.err != nil {
		return nil, n.doc.err
	}

	found, err := n.doc.pathResolver().Matches(n.base.Join(path))
	if err != nil {
		return nil, n.bindOwn(err)
	}

	nodes := make([]*Node, 0, len(found))

	for _, m := range found {
		c := *n
		c.base = m.Path
		c.node = m.Node
		c.span, c.content = n.scope(m.Path, m.Node)
		nodes = append(nodes, &c)
	}

	return nodes, nil
}

// scope returns the lines and the tokens of the Node that [Node.At] or
// [Node.Nodes] scopes at path, a `$` path that selects node. The tokens
// are those of node, as [Node.extent] finds them, and so are the lines,
// with one addition. The key of an entry introduces a mapping or a
// sequence that is its value, and the "-" of an element of a block
// sequence introduces one that is the element. [astnode.PathToken] gives
// that token. Where it lies on a line above the lines of node, the lines
// start there, so they hold the token [paths.Resolver.Token] resolves for
// path.
//
// A scalar is its own token, and nothing holds the root of a document or
// the key of an entry, so each keeps the lines of its node. So does the
// Node of a path that ends at an alias. Its node is the content of the
// anchor, and the alias the path points at introduces none of it.
func (n *Node) scope(path paths.Path, node ast.Node) (position.Span, token.Tokens) {
	span, tks := n.extent(node)

	// A scalar is its own token, so no token above it introduces it.
	if astnode.IsScalar(astnode.Content(node)) {
		return span, tks
	}

	in := datapath.ParentAt(n.doc.pathResolver(), path)

	var value ast.Node

	switch {
	case in.Entry != nil:
		value = in.Entry.Value
	case in.Sequence != nil && in.Index < len(in.Sequence.Values):
		value = in.Sequence.Values[in.Index]
	}

	// The content behind an alias lies at its anchor, so the key or the
	// "-" in front of the alias introduces none of the lines of node.
	if _, ok := astnode.Content(value).(*ast.AliasNode); ok {
		return span, tks
	}

	tk := astnode.PathToken(value, in)
	if tk == nil || tk.Position == nil {
		return span, tks
	}

	if at := tk.Position.Line - 1; at >= 0 && at < span.Start {
		span = position.NewSpan(at, span.End)
	}

	return span, tks
}

// extent returns the lines and the tokens of node in the document. The
// tokens run from the first token under the node through the last, in
// source order. The lines run from the one the first token starts on
// through the last one that holds content of a token among them. A node
// whose tokens carry no position covers no lines and holds no tokens. A
// node with no token in the document, such as an implicit null the
// parser adds, holds no tokens and covers the line its token names.
func (n *Node) extent(node ast.Node) (position.Span, token.Tokens) {
	first, last := astnode.Bounds(node)
	if len(first) == 0 {
		return position.Span{}, nil
	}

	total := n.source.lines.Len()
	lo, hi := first[0].Position.Offset, last[0].Position.Offset

	all := n.doc.positioned
	from := sort.Search(len(all), func(i int) bool { return all[i].Position.Offset >= lo })
	to := sort.Search(len(all), func(i int) bool { return all[i].Position.Offset > hi })
	tks := all[from:to:to]

	// A token that holds no text can share its offset with a token of
	// another node. The empty content of a block scalar sits where the
	// next key starts, and an implicit null takes the offset of the ":"
	// or "-" before it. A token at either end of the window therefore
	// belongs to the node only when the node holds it.
	outside := func(tk *token.Token) bool {
		switch tk.Position.Offset {
		case lo:
			return !slices.ContainsFunc(first, func(b *token.Token) bool { return segment.SameToken(tk, b) })
		case hi:
			return !slices.ContainsFunc(last, func(b *token.Token) bool { return segment.SameToken(tk, b) })
		default:
			return false
		}
	}

	if slices.ContainsFunc(tks, outside) {
		tks = slices.DeleteFunc(slices.Clone(tks), outside)
	}

	if len(tks) == 0 {
		at := min(max(first[0].Position.Line-1, 0), total)

		return position.NewSpan(at, min(at+1, total)), nil
	}

	start := tks[0].Position.Line - 1
	end := start

	// The last token with content ends the span. A token after it, such as
	// the empty content of a block scalar, holds no text to put a line
	// into the span.
	for _, tk := range slices.Backward(tks) {
		if lastLine, ok := n.lastContentLine(tk); ok {
			end = max(end, lastLine)

			break
		}
	}

	return position.NewSpan(min(max(start, 0), total), min(end+1, total)), tks
}

// lastContentLine returns the last line of the source that holds content
// of tk other than spaces. The lexer folds the line breaks and the
// indentation of the next line into a scalar, and a line where tk holds
// only those does not count, so a sibling's line stays out of the span.
// The boolean is false when tk holds no content on any line.
func (n *Node) lastContentLine(tk *token.Token) (int, bool) {
	lines := n.source.lines

	last, found := 0, false

	for i := max(tk.Position.Line-1, 0); i < lines.Len(); i++ {
		sp, ok := lines.Line(i).ContentSpan(tk)
		if !ok {
			// The lines of a token follow one another from the line its
			// position names, so the first line without it ends the
			// search. A token that holds no text, such as the empty
			// content of a block scalar, is on no line.
			break
		}

		if sp.Len() > 0 {
			last, found = i, true
		}
	}

	return last, found
}

// Path returns the scope of the [Node]: the `$` path from the document
// root to the node, which is [paths.Doc] for the root Node of a document
// and the joined paths for one from [Node.At]. A Node from [Node.Nodes]
// reports the path that selects its node alone, so the Node for the first
// match of `$.items[*]` reports `$.items[0]`. The path starts at `$`, so
// it goes back to [Node.At], [Node.Ranges], or [AtPath] through any Node
// of the document and names the same node.
func (n *Node) Path() paths.Path {
	return n.base
}

// Source returns the [*Source] the node came from.
func (n *Node) Source() *Source {
	return n.source
}

// DocumentIndex returns the 0-indexed position within the file of the
// document the Node belongs to. It counts every document of the file, an
// empty one included, so it is the index of the root in the slice
// [Source.AllDocuments] returns. [Source.Documents] leaves out the empty
// documents beside one with content, so a root below one of them has a
// smaller index in that slice. A Node from [Node.At] reports the index
// of the document that holds it, and the index of the node itself within
// a sequence is the Index of the selector [paths.Path.Last] gives for
// [Node.Path]. The message of a bound error counts documents from 1, as
// it counts lines, so "document 3" in [SourceError.Error] is the document
// at index 2. [SourceError.DocumentIndex] reports the index of the
// document an error is bound to, for a caller that holds the error and
// no Node.
func (n *Node) DocumentIndex() int {
	return n.doc.index
}

// Tokens returns the tokens of the node, with the positions they have in
// the source. For the root of a document they are its preamble, its
// content, and, when no document follows, the comments after a "..."
// marker that ends it, which otherwise become the preamble of the next
// document. They are nil when no token anchors the document, such as one
// with neither a header nor a body. For a Node from [Node.At] they run
// from the first token under the node through the last, comments between
// them included, and are nil when the scope selects nothing. The slice
// is a copy, so reordering it reaches nothing. Every Node shares the
// tokens themselves, so a caller must not modify them.
func (n *Node) Tokens() token.Tokens {
	return slices.Clone(n.content)
}

// Preamble returns the tokens of the document the Node belongs to before
// its content: the comments and %YAML or %TAG directives above its "---"
// header, the header itself, and the comments between the header and the
// first token of the content. The parser cuts the tokens above the header
// off as a node of their own, and [Source.Documents] folds them back into
// the document the YAML spec attaches them to. A schema directive written
// above the header is thus in the preamble of the document it describes.
// A document without content, such as one holding comments alone, is all
// preamble. The slice is a copy of shared tokens that a caller must not
// modify, as for [Node.Tokens].
func (n *Node) Preamble() token.Tokens {
	return slices.Clone(n.doc.tokens[:n.doc.preamble])
}

// FilePath returns the path of the file the document came from, which is
// [Source.FilePath]. It is absolute for a document [NewSourceFromFile]
// read, so a schema routes on the file and not on how the caller
// spelled its path. Returns an empty string when the source has none.
//
// The document [NewSourceFromLayers] builds came from no file, and its
// Source has the path of the lowest layer. Every Node of it returns that path, whichever
// layer holds its value. [Node.Origin] returns the Node of the value in
// the file of its layer, and FilePath of that Node names the file.
func (n *Node) FilePath() string {
	return n.source.FilePath()
}

// FS returns the file system the path of the file the document came from
// names a file in, which is [Source.FS]. It returns nil when the source
// has none, where a path names a file on disk. A Node of the document
// [NewSourceFromLayers] builds returns the file system of the lowest
// layer, as
// [Node.FilePath] describes.
func (n *Node) FS() fs.FS {
	return n.source.FS()
}

// Span returns the lines of [Source.Lines] that the node covers. A whole
// document covers the lines from the one its first token starts on to the
// one before the next document starts, or to the end of the source for
// the last document. The first document also covers the lines above its
// first token, so the spans of a source cover every one of its lines. A
// document after the first with no tokens covers no lines.
//
// A Node from [Node.At] or [Node.Nodes] covers the lines of its node,
// from the one the first token under the node starts on through the one
// the last token ends on. A scope that selects nothing covers no lines.
//
// A mapping or a sequence also covers the line of the token that
// introduces it, where that token lies above its content. The token is
// the key of the entry the mapping or the sequence is the value of, or
// the "-" of the element it is in a block sequence:
//
//	hours:      # the span of $.hours starts here
//	  open: 9
//	  close: 5
//
// The span then runs from the line of that token through the last line
// of the node. A flow collection on the line below its key thus starts at
// the key, and so does a mapping or a sequence under an explicit key. A
// line between the key and the content lies in the span too, such as one
// that holds an anchor, a tag, or a comment. [Node.Tokens] returns the
// tokens of the value alone, so the token that introduces it is not
// among them.
//
// A scalar is its own token, so it covers its own lines alone. The line
// of its key is in the span only when the scalar starts on it:
//
//	plain:
//	  one       # the span of $.plain is this line
//
// A scope from [paths.Path.Key] covers the key.
//
// [Node.Ranges] of the scope and an error bound at its root resolve to
// the token the path of the scope points at, as [paths.Resolver.Token]
// describes. That token is the key or the "-" that introduces the node,
// or else a token of the node itself, so the span holds its line.
// [SourceError.Annotate] thus marks such an error on [Node.View].
//
// A path that ends at an alias is the exception. The node of such a
// path, such as `$.c` in `c: *x`, is the content of the anchor. The span
// covers the lines of that content alone, which [Node.AST],
// [Node.Decode], and [Node.Tokens] read. Node.Ranges of the scope and an
// error bound at its root resolve to the alias, where the path points.
// The alias lies outside the span unless it shares a line with that
// content, as it can in a flow collection. Annotate then marks nothing on
// Node.View and reports false. A caller that shows the scope with that
// error adds the line of the error to the view:
//
//	view := hours.View()
//	if !bound.Annotate(view) { // only a path that ends at an alias
//		if pos, ok := bound.Position(); ok {
//			view = hours.Source().View().Slice(hours.Span(), position.NewSpan(pos.Line, pos.Line+1))
//			bound.Annotate(view)
//		}
//	}
//
// A tag on the alias keeps the node at the tag, as [paths.Resolver.Node]
// describes, so the node of `$.c` in `c: !t *x` is the tag with the alias
// under it, and the span covers the line of the alias instead.
//
// An error at a Node of the document [NewSourceFromLayers] builds binds
// in the file of a layer, so it marks nothing on a view of the merged
// text. A caller takes [Node.Origin] of that Node first and marks the
// view of the origin, whose span follows the same rules in the file of
// its layer.
//
// [Node.View] returns a view of the source sliced to the span, so a
// caller that renders the node need not slice one itself. The span
// slices any other view over the source, such as one that carries
// decoration already:
//
//	lipgloss.Println(p.Print(view.Slice(doc.Span())))
func (n *Node) Span() position.Span {
	return n.span
}

// View returns a new [*line.View] over the lines of [Source.Lines] that
// the node covers, [Node.Span], with the line numbers they have in the
// file. A document of a file that holds several, or the node a Node from
// [Node.At] selects, renders on its own:
//
//	lipgloss.Println(p.Print(doc.View()))
//
// The view keeps the index each line has in the source, so a diff or a
// search of the node takes the view and reports lines of the file. The
// views of one document in two revisions of a file that holds several
// diff that document alone:
//
//	result := diff.Diff(before[1].View(), after[1].View())
//
// [line.View.Held] returns the lines of the view on their own, such as
// for the text of the node:
//
//	text := doc.View().Held().Content()
//
// Each call returns a view of its own with no decoration, as [Source.View]
// does, so overlays and annotations added to one reach neither the Source
// nor another view. The view shares its lines with every view over the
// source, so [SourceError.Annotate] marks a bound error on it as on a
// view of the whole source when the location of the error lies in
// [Node.Span]. An error outside the span marks nothing on the view, and
// Annotate reports false for it. An error bound at the root of a scope
// lies in the span, unless the path of the scope ends at an alias on
// another line than the content of the anchor. Node.Span shows how to
// slice a view that holds the line of such an error.
func (n *Node) View() *line.View {
	return line.NewView(n.source.lines, n.span)
}

// Ranges returns the ranges of the token path points at, the token
// [paths.Resolver.Token] resolves, one per line the token spans, without the
// spaces around its content. They are the ranges [SourceError.Excerpt]
// highlights for an [Error] built with [AtPath] at that path, and the path
// resolves as it does in such an Error: an `@` path from the scope of the
// Node, and a `$` path, such as one from [SourceError.Path], from the root
// of the document. A scalar covers every line of its text, and a block
// scalar its indicator. A mapping or a sequence covers the token that
// introduces it: the key of the entry that holds it, the "-" of the
// element it is in a block sequence, or else its own "{" or "[". A block
// mapping or a block sequence at the root covers its first key or its
// first element. A path from [paths.Path.Key] covers the key of the
// entry whatever its value holds. The ranges mark the value on a view of
// the source:
//
//	ranges, err := doc.Ranges(paths.Doc().Child("spec", "replicas"))
//	if err != nil {
//		return err
//	}
//
//	view := doc.View()
//	view.AddOverlay(kind.GenericHighlight, ranges...)
//
// To mark every line of a mapping or sequence, pass the path to [Node.At]
// and use [Node.Span] or [Node.View] of the Node it returns.
//
// A path that does not resolve returns the error [paths.Resolver.Token]
// describes, bound to the source. The error of a path that names a key a
// mapping leaves out binds at that mapping, as it does for [Node.At], and
// [IsInvalid] reports every error that wraps [paths.ErrNotFound]. A
// path whose token carries no
// position returns an error wrapping [ErrNoLocation]. A document that
// did not parse returns the syntax error [Node.Err] returns. Returns nil
// when the value holds no content on any line.
func (n *Node) Ranges(path paths.Path) (position.Ranges, error) {
	if n.doc.err != nil {
		return nil, n.doc.err
	}

	loc, err := n.pathLocation(path)
	if err != nil {
		return nil, n.bindUnresolved(path, err)
	}

	return loc.highlight(n.source.lines), nil
}

// pathLocation returns the location of the token that path resolves to in
// the document, through [paths.Resolver.Token], with an `@` path resolving
// from the scope. The location holds the token and its position. An error
// from [paths.Resolver.Token] names the path already and comes back as it is,
// and a token without a position is [ErrNoLocation].
func (n *Node) pathLocation(path paths.Path) (location, error) {
	tk, err := n.doc.pathResolver().Token(n.base.Join(path))
	if err != nil {
		//nolint:wrapcheck // The paths error already names the path.
		return location{}, err
	}

	if tk == nil || tk.Position == nil {
		return location{}, fmt.Errorf("%w: token at path has no position", ErrNoLocation)
	}

	return location{pos: position.NewFromToken(tk), tk: tk}, nil
}

// nearestLocation returns the location an error binds at when its path
// names a key the document leaves out: the key of the mapping that lacks
// it, as [paths.Resolver.Nearest] finds that mapping, with an `@` path
// resolving from the scope. The reason is the error the path failed to
// resolve with. It reports false when that reason is not
// [paths.ErrNotFound], when no mapping lacks the key, and when the key of
// that mapping carries no position.
func (n *Node) nearestLocation(path paths.Path, reason error) (location, bool) {
	if !errors.Is(reason, paths.ErrNotFound) {
		return location{}, false
	}

	resolver := n.doc.pathResolver()

	near, ok := resolver.Nearest(n.base.Join(path))
	if !ok {
		return location{}, false
	}

	tk, err := resolver.Token(near.Key())
	if err != nil || tk == nil || tk.Position == nil {
		return location{}, false
	}

	return location{pos: position.NewFromToken(tk), tk: tk, near: &near}, true
}

// aliasLocation returns the location an error binds at when its path
// enters an alias the document cannot follow: the token of that alias,
// with an `@` path resolving from the scope. Such an alias names no
// anchor before it, as one to an anchor of a reference document does, or
// lies inside the anchor it names. The reason is the error the path
// failed to resolve with, which the location keeps. It reports false when
// that reason is not [paths.ErrAlias] and when the token of the alias
// carries no position. It also reports false when the alias sits under a
// `<<` merge key. A path enters no alias to read a key of such a mapping,
// and the alias may set any key or none, so it says nothing of where the
// value is.
func (n *Node) aliasLocation(path paths.Path, reason error) (location, bool) {
	if !errors.Is(reason, paths.ErrAlias) {
		return location{}, false
	}

	resolver := n.doc.pathResolver()

	// The path fails at the first selector that reads through the alias,
	// so the longest path above it that resolves ends at the node that
	// selector reads.
	for at, ok := n.base.Join(path).Parent(); ok; at, ok = at.Parent() {
		tk, err := resolver.Token(at)
		if err != nil {
			continue
		}

		// The resolver reads through every node but an alias it cannot
		// follow. Where it reads through this one, the selector failed at
		// an alias under a `<<` merge key of the mapping here.
		_, err = resolver.Node(at)
		if !errors.Is(err, paths.ErrAlias) || tk == nil || tk.Position == nil {
			return location{}, false
		}

		return location{pos: position.NewFromToken(tk), tk: tk, near: &at, unfollowed: reason}, true
	}

	return location{}, false
}

// bindUnresolved binds reason, the error path failed to resolve with from
// the scope of n, as [Node.At] and [Node.Ranges] return it. A reason that
// wraps [paths.ErrNotFound] says the document lacks the value, so it
// binds inside an [Error] from [Invalid], which matches [errInvalid]. A
// path that names a key a mapping leaves out binds as an Error with
// [AtPath] of that path binds, at the key of the mapping that lacks it,
// as [Node.nearestLocation] finds it. The binding writes the path in
// front, so the message is the one of [notFoundError], which leaves the
// path out. Any other path that selects nothing names no mapping, so its
// error binds with no location, as [Node.bindOwn] binds it. Any other
// reason is about the call, and it binds as it is.
func (n *Node) bindUnresolved(path paths.Path, reason error) error {
	if !errors.Is(reason, paths.ErrNotFound) {
		return n.bindOwn(reason)
	}

	if _, ok := n.nearestLocation(path, reason); !ok {
		return n.bindOwn(Invalid(reason))
	}

	return n.bindOwn(Invalid(notFoundError{err: reason}, AtPath(path)))
}

// notFoundError is the error of a path that names a key a mapping leaves
// out. Its message is the message of [paths.ErrNotFound] alone, where the
// error it unwraps to, the one the path failed to resolve with, names the
// path too.
type notFoundError struct {
	err error
}

func (e notFoundError) Error() string {
	return paths.ErrNotFound.Error()
}

func (e notFoundError) Unwrap() error {
	return e.err
}

// Validate runs v on the node. It is the validation step of [Node.Decode]
// on its own, for a caller that checks a document without decoding it:
//
//	for _, doc := range docs {
//		if err := doc.Validate(ctx, reg); err != nil {
//			return err
//		}
//	}
//
// Validate takes one validator, and that validator decides how many
// violations the node reports. A caller with several validators names how
// they run together. [MultiValidator] runs every one and reports every
// failure, which suits rules that check the node independently, as the
// rules of a linter do. [ChainValidator] stops at the first that fails,
// which suits a check that needs an earlier one to pass, such as one
// that reads the values a schema requires:
//
//	err := doc.Validate(ctx, niceyaml.MultiValidator(schema, names))
//	err := doc.Validate(ctx, niceyaml.ChainValidator(schema, refs))
//
// [SkipEmpty] wraps either one, so that a document with no content
// passes.
//
// A nil v runs nothing, and neither does one that holds a nil pointer or
// func, so Validate then returns what [Node.Err] returns. A validator
// that returns a nil [*Error] or [*SourceError] pointer passes.
//
// A validator writes its errors from the Node it got, as [Validator]
// describes, and Validate binds them to the source as a [SourceError]
// through [Node.Bind], so an [*Error] renders its location and any other
// error names the source. A Node from [Node.At] or [Node.Nodes] also
// points an error with no location at its own value, as Node.Bind
// describes. An error the validator bound itself comes back as it is.
// A direct call to the Check method of a validator is the raw step. It
// returns the error as the validator wrote it, whichever validator it
// is: an unbound error names no source and its `@` paths read from the
// Node the validator got, and an error the validator bound itself is
// bound already. Validate is thus the way to run a validator, the
// caller's own or one it did not write, and the way a validator runs
// another on a Node of its choosing, as item.Validate(ctx, inner) runs
// inner on an element.
//
// A document that did not parse fails before v runs, with the syntax
// error [Node.Err] returns, even when v is nil. A caller that validates
// each document of a file thus collects the syntax errors of the file in
// the same loop, as [Source.ValidateDocuments] does.
func (n *Node) Validate(ctx context.Context, v Validator) error {
	return n.validate(ctx, []Validator{v})
}

// validate runs validators in order on n, and binds the first error with
// n when the validator left it unbound. A document that did not parse
// returns its syntax error, and no validator runs.
func (n *Node) validate(ctx context.Context, validators []Validator) error {
	if n.doc.err != nil {
		return n.doc.err
	}

	return n.Bind(n.firstFailure(ctx, validators))
}

// firstFailure runs validators in order on n and returns the first
// failure as the validator wrote it, or nil when each of them passes. It
// skips a nil validator, and one that holds a nil pointer or func.
func (n *Node) firstFailure(ctx context.Context, validators []Validator) error {
	for _, dv := range validators {
		if nilness.IsNil(dv) {
			continue
		}

		// A typed nil pointer is no failure, as [Node.Bind] reads it.
		err := dv.Check(ctx, n)
		if isNothing(err) {
			continue
		}

		return err //nolint:wrapcheck // The validator's own error, which the Node that runs the validator binds.
	}

	return nil
}

// Bind binds err to the document's source, with the paths in err
// resolving in this document: an `@` path from the scope of the Node, and
// a `$` path from the root of the document. It resolves every location
// in err as it binds, so the position [SourceError.Error] reports and the
// range [SourceError.Range] returns are fixed from then on, and
// [SourceError.Excerpt] returns the excerpt as a view for the caller to
// render. A Node from [Node.At] or [Node.Nodes] binds a problem with no
// location at its own value, and the root of a document gives such a
// problem no location. Bind returns nil for a nil err, and an error that
// is bound already comes back as it is.
//
// # Caller Checks
//
// The Node methods bind the errors they return already. Bind is for an
// error built elsewhere, such as a validator's [*Error] with a path, or
// one from a check the caller runs on a value it took from the document.
// Such an error writes an `@` path from the value, so the Node scoped to
// that value with [Node.At] binds it, and a check written for a type
// takes a pointer to it and goes with any decode of that type:
//
//	item, err := doc.At(path)
//	if err != nil {
//		return err
//	}
//
//	value, err := item.Decode[map[string]any](ctx)
//	if err != nil {
//		return err
//	}
//
//	return item.Bind(check(value))
//
// # Paths
//
// The message of a bound [*Error] carries its path from the root of the
// document, as a `$` path, behind the position the path resolved to. The
// Node puts its own [Node.Path] in front of each `@` path in err, as
// [Rebase] does, and leaves a `$` path as it is. A check that wrote
// `@.price` thus reports `$.items[1].price` when the Node at `$.items[1]`
// binds it, in the message and in [SourceError.Path], as a decode of the
// whole document reports it. A path that names the value by its place in
// the document, such as the [Node.Path] of another Node, starts at `$`
// and binds through any Node of the document. The paths of a join, of the
// errors a summary from [NewSummary] heads, and of the details from
// [WithDetails] change the same way. An Error writes no path into its own
// message, so text that a wrapper such as [fmt.Errorf] added around a
// located Error holds none, and the bound message names the joined path
// once, in front of that text.
//
// # Problems Without Locations
//
// A Node from Node.At or Node.Nodes stands for one value, so a problem
// with no location that it binds is about that value. The Node binds
// such a problem at itself, as it binds an Error with [AtPath] of
// [paths.Current]. A check that returns a plain error thus reports
// `$.items[1]` and the position of that item. The Node reads the roles
// the errors declare, problem by problem, as [Rebase] does:
//
//   - A problem with no location binds at the Node whatever its details
//     carry. An error "ports conflict" above the two ports it names thus
//     keeps its message on the line of the value.
//   - A summary, a join, and a wrapper around either are headings. A
//     heading gains no location, and each problem it heads binds on its
//     own, at the Node when it carries no location, beside located ones.
//   - A detail explains the error above it, so it gains no location.
//
// The Node thus binds an error as the root of the document binds a
// Rebase of it under [Node.Path], so a check bound through a scoped Node
// reports the lines and the paths that the same check run by a
// [SelfValidator] reports. The location places the error and declares
// nothing about it, so [IsInvalid] reports the bound error only when it
// reports err, where it reports every error a SelfValidator returns. An
// error that matches [context.Canceled] or [context.DeadlineExceeded] is
// about the call, so it gains no location either. The root of a document
// gives no error a location, since an error bound there can be about the
// document as a whole, such as a schema that does not load. A caller
// that holds a scoped Node binds such an error through the root
// [Node.Document] returns.
//
// An error that gains no location, such as one from
// [go.jacobcolvin.com/niceyaml/paths] bound through the root of a
// document, binds all the same, and the bound error names the source in
// front of the message, as "name: msg". An error from one file of many
// thus still says which file. In a source that holds more than one
// document, the document stands behind the name, counted from 1, as in
// "name: document 3: msg", so an error from one document of many says
// which document. The message of err stays as it is, and the position
// goes in front of it.
//
// # Positions and Ranges
//
// An Error that carries a position or a range beside its path,
// from [AtPosition] or [AtRange], binds at that position or range instead,
// with the position or the start of the range in front of the message.
// Bind does not resolve the path of such an Error, so a path the document
// does not hold gives no [SourceError.Unresolved] reason.
//
// # Source Binding
//
// [Source.Bind] binds an error to the document its location falls in,
// so a caller that holds the source rather than a document binds there.
// A position or a range finds the document whose span holds it, and a
// path resolves in the one document of a source that holds one. A path
// in a source that holds several resolves nowhere there, with
// [ErrPathNeedsDocument] as the reason, and binds here instead.
//
// # Error Trees
//
// Binding binds the whole tree of err. The [Error] that anchors it gives
// the [SourceError] its location. Every error a summary along the way
// heads becomes a child of the binding in the tree [NewErrorTree] builds,
// and every detail of an Error along the way becomes a child with Detail
// set. An error that unwraps to several, such as one from
// [errors.Join], binds the same way whatever wraps it. The SourceError
// carries no location of its own, and each branch is a child. A wrapper
// that [fmt.Errorf] builds with several %w verbs keeps only its branches
// that carry a location or errors below them, and when one remains, it
// binds where that branch does. Each child binds at the location its own
// error carries, if any, so a validator that joins its violations
// reports each one with its position. To keep several errors as separate
// bindings, bind each one before joining them.
//
// # Nil and Bound Errors
//
// If err is nil, Bind returns nil. A nil [*Error] or [*SourceError]
// pointer as err carries nothing to bind, so Bind returns a nil error for
// it too. A validator that accumulates into a typed pointer and returns
// it on success thus reports no error. Such a pointer inside the chain
// binds nothing, so Bind looks past it. An error that is or wraps a
// [*SourceError] along its cause chain, with no [*Error] above it that
// carries a location, heads errors, or holds details, is bound already,
// to this source or another, and comes back as it is. Binding is thus
// idempotent. A located Error above a binding binds anew at its own
// location, and its message keeps the position the inner binding
// resolved. An Error with details above a binding binds anew around it,
// with those details as children. Bind never modifies err.
//
// One binding stands in no document, which is the error [BindValue]
// returns for a value that came from none. Bind binds the error it was
// made from, as it binds an error that no source bound yet, so a
// validator that returns such a result reports it in the document. Each
// binding below that one binds the same way on its own.
//
// # Unparsed Documents
//
// A document that did not parse has no tree to resolve a path in, so a
// path bound through its Node resolves nowhere. The bound error keeps
// its message and the name of the source, and [SourceError.Unresolved]
// returns [ErrPathNeedsDocument] wrapping the syntax error [Node.Err]
// returns. A position or a range binds there as it does in any document.
func (n *Node) Bind(err error) error {
	return bindTree(err, binder{src: n.source, node: n, locate: true})
}

// bindOwn binds an error of an operation of n, as [Node.Bind] binds it,
// with one difference. The error is about the call, such as a path that
// resolves to no node or a decode target that is no pointer, so an error
// that holds no location gains none, even when n is a Node from [Node.At]
// or [Node.Nodes].
func (n *Node) bindOwn(err error) error {
	return bindTree(err, binder{src: n.source, node: n})
}

// contextEnded reports whether err is, or wraps, the error of a context
// that ended: [context.Canceled] or [context.DeadlineExceeded].
func contextEnded(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// DecodeOption configures [Node.Decode], [Node.DecodeInto],
// [Node.DecodeAt], and [Node.DecodeIfPresent]. [Source.Decode] and
// [Source.DecodeInto] apply them to the one decode of the document of a
// Source, such as the merged document of [NewSourceFromLayers].
// [Node.SelfValidate] and [Source.SelfValidate] take them too, and read
// only the options that say how a value decodes, as Node.SelfValidate
// lists them.
//
// Available options:
//   - [WithValidator]
//   - [WithSelfValidation]
//   - [WithDisallowUnknownFields]
//   - [WithAllowedFieldPrefixes]
//   - [WithCustomUnmarshaler]
//   - [WithJSONUnmarshalers]
//   - [WithYAMLOrderedMaps]
//   - [DecodeOptions]
//
// A DecodeOption sets how one decode runs. A setting that describes the
// documents belongs to the [Source], which applies it to every decode
// and every validation. [WithReferences] names the reference documents
// whose anchors an alias reads that way, and [WithAliasLimit] says
// whether the alias limit applies.
//
// An option reaches the call that gets it and no other. A [Validator]
// that decodes the Node it gets makes a call of its own, with the
// options the validator gives that call.
type DecodeOption func(*decodeConfig)

// decodeConfig holds the settings a [DecodeOption] configures. Its zero
// value holds the defaults.
type decodeConfig struct {
	validators           []Validator
	allowedFieldPrefixes []string
	// The types the options hand to an unmarshaler that no method of the
	// type declares.
	unmarshalers          unmarshaler.Options
	skipSelfValidation    bool
	disallowUnknownFields bool
	orderedMaps           bool
}

// newDecodeConfig returns the settings of a decode: the defaults, with
// opts applied over them in order. The result shares nothing with any
// other decode.
func newDecodeConfig(opts []DecodeOption) decodeConfig {
	var cfg decodeConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

// yamlOptions returns the go-yaml options the settings stand for, which
// every go-yaml decoder of the call applies. The function of each
// [WithCustomUnmarshaler] reads its value through decode.
func (c decodeConfig) yamlOptions(decode unmarshaler.ValueDecoder) []yaml.DecodeOption {
	opts := c.unmarshalers.YAMLOptions(decode)

	if c.disallowUnknownFields {
		opts = append(opts, yaml.DisallowUnknownField())
	}

	if len(c.allowedFieldPrefixes) > 0 {
		opts = append(opts, yaml.AllowFieldPrefixes(c.allowedFieldPrefixes...))
	}

	if c.orderedMaps {
		opts = append(opts, yaml.UseOrderedMap())
	}

	return opts
}

// WithValidator is a [DecodeOption] that validates the document with dv
// before decoding it, and a validation error ends the decode before any
// typed decoding. Each WithValidator option adds a validator. The decode
// runs them as [ChainValidator] runs them, so it runs them in the order
// given and stops at the first that fails. A caller that wants every
// failure of several validators gives one option a [MultiValidator].
// Validation skips a nil dv, and one that holds a nil pointer or func,
// such as a schema a program loads only on some paths. A
// [go.jacobcolvin.com/niceyaml/schema.Schema] checks the document against
// one JSON schema, and a [go.jacobcolvin.com/niceyaml/schema.Registry]
// against the schema it picks for the document:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// An empty document validates as any other. [SkipEmpty] wraps a
// validator that should pass one, so the decode returns the zero value.
func WithValidator(dv Validator) DecodeOption {
	return func(c *decodeConfig) {
		c.validators = append(c.validators, dv)
	}
}

// WithSelfValidation is a [DecodeOption] that sets whether the values
// in a decoded value that implement [SelfValidator] validate themselves
// after decoding. The default is true. Validators given with
// [WithValidator] run either way. [Node.SelfValidate] and
// [Source.SelfValidate] run the walk whatever the option says, so a
// caller that turns it off for the decode validates the value later.
func WithSelfValidation(enabled bool) DecodeOption {
	return func(c *decodeConfig) {
		c.skipSelfValidation = !enabled
	}
}

// WithDisallowUnknownFields is a [DecodeOption] that sets whether a mapping
// key with no field in the target struct is an error. The default is false,
// and the decode then skips unknown keys. With the option on, a decode
// that fails reports every unknown field the document holds, and each of
// those errors matches [ErrUnknownField]. The report holds only keys the
// go-yaml decoder rejects, so a key under a prefix that
// [WithAllowedFieldPrefixes] allows stays out of it. It lists the fields
// in the order of the source, each at the path of its key:
//
//	cfg.yaml: 2 unknown fields
//	cfg.yaml:3:1: $.replicsa~: unknown field "replicsa"
//	cfg.yaml:7:5: $.servers[0].prot~: unknown field "prot"
//
// # Report
//
// The error is a summary from [NewSummary] that counts the fields on the
// first line of its message and heads one error for each. The message
// lists them, and [ErrorTree.Problems] yields them. Each of those errors
// matches [ErrUnknownField], and [ErrDecode] as every problem of a
// decode does. A caller that needs go-yaml's type finds the
// [yaml.UnknownFieldError] the decoder returns for that field with
// [errors.As]. A
// document with one unknown field reports that field as the error itself.
// A document that holds another problem too, such as a value of the
// wrong kind, lists its unknown fields among the problems
// [Node.DecodeInto] reports for one decode, and the first line then
// counts problems. ErrUnknownField tells the unknown fields from the
// other problems of that report.
//
// # Search
//
// The go-yaml decoder decides whether the decode fails. It reports a
// value it rejects before any unknown field. It stops at the first
// unknown field it finds, and it finds the fields of one mapping in no
// fixed order. So the decode looks for the unknown fields itself once
// the decoder has rejected the document. It reads each mapping that a
// struct of the target decodes from, and asks the decoder about each key
// no field of the struct names, with the options of the decode. The
// report therefore holds only keys the decoder rejects, and it is the
// same on every run. A key under a prefix that [WithAllowedFieldPrefixes]
// allows stays out of it.
//
// # Unchecked Mappings
//
// The report follows the decoder where the decoder checks nothing:
//
//   - The decoder decodes no field from a mapping that holds a key it
//     does not read as a string, such as `1` or `true`, and rejects no
//     key of that mapping. It takes no key either from such a mapping
//     that a `<<` merge key brings in.
//   - The decoder decodes no field from a mapping whose `<<` merge key
//     it refuses, such as one that names a sequence of mappings, and
//     rejects no key of that mapping.
//   - The decoder fills a field of an interface type with maps and
//     slices, so no key below such a field is unknown.
//   - A struct that decodes itself decides what the decoder checks. An
//     UnmarshalYAML that decodes into a second type with the same fields
//     has the decoder check them, and one that parses the text itself
//     does not. A struct that a [WithCustomUnmarshaler] function decodes,
//     or an UnmarshalJSON method under [WithJSONUnmarshalers], decides
//     the same way.
//
// # Limits
//
// Three limits remain. The decode adds no unknown field to a rejection
// that binds at no position in the source, as [Node.DecodeInto]
// describes, so such a rejection comes back alone. The search reads the
// values below a struct that decodes itself as if its fields mirrored
// the document, so it misses an unknown field below one whose fields do
// not, unless the decoder names that field. And nothing shows which
// types [yaml.RegisterCustomUnmarshaler] gave a function for the whole
// program, so the search reads the values below such a type as the
// fields of its struct, and can report a key there that the function
// accepts. A function from WithCustomUnmarshaler has no such limit.
func WithDisallowUnknownFields(disallow bool) DecodeOption {
	return func(c *decodeConfig) {
		c.disallowUnknownFields = disallow
	}
}

// WithAllowedFieldPrefixes is a [DecodeOption] that lets a mapping hold
// a key that starts with one of prefixes where
// [WithDisallowUnknownFields] rejects a key with no field, such as the
// `x-` keys a format reserves for extensions:
//
//	spec, err := doc.Decode[Spec](ctx,
//		niceyaml.WithDisallowUnknownFields(true),
//		niceyaml.WithAllowedFieldPrefixes("x-"),
//	)
//
// Each WithAllowedFieldPrefixes adds to the prefixes given before it. An
// empty prefix allows every key. The option changes nothing in a decode
// that accepts unknown fields.
func WithAllowedFieldPrefixes(prefixes ...string) DecodeOption {
	return func(c *decodeConfig) {
		c.allowedFieldPrefixes = append(c.allowedFieldPrefixes, prefixes...)
	}
}

// WithCustomUnmarshaler is a [DecodeOption] that decodes every value of
// type T with fn, for a type the program cannot give an UnmarshalYAML
// method, such as one of another package. The decoder calls fn in place
// of decoding the fields, elements, or entries of T, and ahead of any
// unmarshaler method T has. The function reads the value through the
// decode function it gets, which decodes with the options of the decode
// that called fn. The decode binds an error fn returns at the value. The
// function serves the decode that gets the option, and a later option for
// the same T replaces an earlier one. The option below decodes a
// [net.IPNet] from its CIDR text:
//
//	cidr := niceyaml.WithCustomUnmarshaler(func(ctx context.Context, n *net.IPNet, decode func(any) error) error {
//		var s string
//
//		err := decode(&s)
//		if err != nil {
//			return err
//		}
//
//		_, parsed, err := net.ParseCIDR(s)
//		if err != nil {
//			return err
//		}
//
//		*n = *parsed
//
//		return nil
//	})
//
//	cfg, err := doc.Decode[Config](ctx, cidr)
//
// # Calls
//
// The decoder calls fn in place of decoding the fields, elements, or
// entries of T, and ahead of any unmarshaler method T has. It hands fn
// the context of the decode and a decode function, as it hands one to an
// UnmarshalYAML method that takes one. The decoder reads a pointer as
// the value it points to, so a field of type *T decodes through fn too,
// and a null leaves it nil without a call.
//
// # Decode Function
//
// The decode function decodes the value into the pointer fn gives it,
// with the options of the decode that called fn, so another
// WithCustomUnmarshaler decodes its type below the value and
// [WithDisallowUnknownFields] rejects an unknown field there. A scalar
// reads the same however the document writes it, in quotes, in a block,
// or with a comment on its line, and an alias reads as the content of
// its anchor. No [Validator] runs on the value.
//
// A decode into T would call fn for the same value again, so the decode
// function returns an error for a pointer to that type. A function that
// decodes T by its fields decodes into a second type with those fields,
// such as `type fields T`, as an UnmarshalYAML method does.
//
// A function that needs the YAML text of the value decodes into a
// [yaml.RawMessage]. The text is a document of its own, with each alias
// in the value written out in full, so a scalar keeps its quotes and the
// comment on its line.
//
// # Errors
//
// The decode function rejects a value as a decode of the document does,
// with every problem of the value in one error. The decode that called
// fn binds an error fn returns in the document, so fn returns the
// rejection as it is:
//
//	config.yaml:3:3: $.nets[1]: expected string, got mapping
//
// An error of fn's own binds at the value. An [*Error] that fn returns
// writes an `@` path that reads from the value, as the error of a
// [SelfValidator] does, so a check names the field it read:
//
//	if r.To < r.From {
//		return niceyaml.NewError("to is below from", niceyaml.AtPath(paths.Current().Child("to")))
//	}
//
// The path of a field resolves through an alias and a `<<` merge key to
// the line that holds the field. A [yaml.Error] that [errors.As] finds
// in a rejection names a token of the text of the value, not of the
// document.
//
// # Validation and Reports
//
// The decode reads T as a type that decodes itself, as it reads one with
// an UnmarshalYAML method. The values below a value of T thus validate
// as [SelfValidator] describes. The search for unknown fields and for
// the other problems of a failed decode leaves the value to fn, as
// [Node.DecodeInto] describes, so one decode reports the first value
// that fn rejects.
//
// # Types
//
// Each WithCustomUnmarshaler names one type, and a later one for the
// same T replaces the earlier. A nil fn names no type, and neither does
// a T that is a pointer type. The decoder decodes the value a pointer
// points to, so no value reaches an fn for the pointer, and T names the
// type of that value instead. The function serves the decode that gets
// the option, and a [Validator] that decodes T gives the option to its
// own decode. A [DecodeOptions] value holds the option for every decode
// of a program.
//
// # Registered Functions
//
// A function that [yaml.RegisterCustomUnmarshaler] registers decodes T
// the same way in every decode of the program, with no option. Nothing
// shows which types have one, so a decode reads such a type by its
// fields, with the limits Node.DecodeInto lists. Give the function to
// WithCustomUnmarshaler instead.
func WithCustomUnmarshaler[T any](fn func(ctx context.Context, v *T, decode func(any) error) error) DecodeOption {
	return func(c *decodeConfig) {
		t := reflect.TypeFor[T]()
		if fn == nil || t.Kind() == reflect.Pointer {
			return
		}

		c.unmarshalers = c.unmarshalers.With(t, func(decode unmarshaler.ValueDecoder) yaml.DecodeOption {
			return yaml.CustomUnmarshalerContext(func(ctx context.Context, v *T, text []byte) error {
				err := fn(ctx, v, func(dst any) error {
					// A decode into T would call fn for the same value again,
					// and so on without end.
					if dt := reflect.TypeOf(dst); dt != nil && unmarshaler.Base(dt) == t {
						return fmt.Errorf("decode target is the type the function decodes: got %T", dst)
					}

					return decode(ctx, text, dst)
				})
				if err != nil {
					return shieldedError{err: err}
				}

				return nil
			})
		})
	}
}

// shieldedError holds the error a [WithCustomUnmarshaler] function
// returned while the go-yaml decoder carries it. The decoder searches
// the error of a struct field for a [yaml.TypeError] with [errors.As],
// and returns that TypeError alone in place of the error it found it in.
// An error that wraps one, as the rejection of a decode callback does,
// would thus lose its message and its path. A shieldedError unwraps to
// nothing, so that search ends at it, and [decodeWithRecover] takes the
// error out again.
//
// An unmarshaler above the value can still wrap a shieldedError in an
// error of its own. The shieldedError then stays in the chain, where it
// matches what its error matches, apart from a TypeError. A decode reads
// no location below it, so the wrapped error points at the value of the
// unmarshaler that wrapped it.
type shieldedError struct {
	err error
}

func (e shieldedError) Error() string {
	return e.err.Error()
}

// Is reports whether the error e holds matches target.
func (e shieldedError) Is(target error) bool {
	return errors.Is(e.err, target)
}

// As sets target to the first error in the chain of the error e holds
// that matches it, as [errors.As] does, and finds no [yaml.TypeError].
func (e shieldedError) As(target any) bool {
	if _, ok := target.(**yaml.TypeError); ok {
		return false
	}

	return errors.As(e.err, target)
}

// WithJSONUnmarshalers is a [DecodeOption] that sets whether a type with
// an UnmarshalJSON method decodes through that method. The default is
// false, and the decode then reads such a type by its fields, elements,
// or entries, as it reads a type with no method.
//
// When enabled, the decoder converts the node to JSON, with each alias
// in the node written out in full, and hands the JSON to the method. It
// calls an UnmarshalYAML method of the type in place of UnmarshalJSON,
// and an UnmarshalText method for a node that reads as a string.
//
// The decode then reads such a type as one that decodes itself. The
// values below a value of the type thus validate as [SelfValidator]
// describes, and the error its method returns binds at the value, as
// [Node.DecodeInto] describes. A struct that gets the method from an
// embedded field decodes the document into that field, so the field
// validates at the path of the struct.
func WithJSONUnmarshalers(enabled bool) DecodeOption {
	return func(c *decodeConfig) {
		c.unmarshalers.JSON = enabled
	}
}

// WithYAMLOrderedMaps is a [DecodeOption] that sets whether a mapping
// that decodes into an any value becomes a [yaml.MapSlice], which holds
// the entries in the order of the document. The default is false, and
// such a mapping then becomes a map[string]any. The option carries the
// YAML prefix because the decoded value holds a go-yaml type.
//
//	data, err := doc.Decode[any](ctx, niceyaml.WithYAMLOrderedMaps(true))
//
// A target of a typed map or a struct decodes as it does without the
// option. A [go.jacobcolvin.com/niceyaml/schema.Schema] reads a MapSlice
// as the mapping it holds, so
// [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue] checks the
// decoded value as it is.
func WithYAMLOrderedMaps(enabled bool) DecodeOption {
	return func(c *decodeConfig) {
		c.orderedMaps = enabled
	}
}

// DecodeOptions is a [DecodeOption] that applies opts in order. A
// program that decodes many documents names its decoder settings in one
// place, and passes the one value wherever a DecodeOption goes. A second
// value adds the schema of the documents to those settings, for each
// decode of a whole document:
//
//	settings := niceyaml.DecodeOptions(niceyaml.WithDisallowUnknownFields(true))
//	strict := niceyaml.DecodeOptions(niceyaml.WithValidator(reg), settings)
//
//	for _, doc := range source.Documents() {
//		manifest, err := doc.Decode[Manifest](ctx, strict)
//		...
//	}
//
// A validator among opts checks the node that each call decodes. For
// [Node.DecodeAt], [Node.DecodeIfPresent], and a Node from [Node.At] or
// [Node.Nodes], that node is one value inside the document, which the
// schema of the whole document does not describe, so those calls take
// the settings without that schema. A call applies the result as it
// applies opts written in its place, so other options go before it and
// after it:
//
//	kind, err := doc.DecodeAt[string](ctx, kindPath, settings, niceyaml.WithSelfValidation(false))
//
// [WithValidator] and [WithAllowedFieldPrefixes] add to what the options
// before them gave, and [WithCustomUnmarshaler] adds a type. Every other
// option replaces what an earlier one of its kind set, so the last of
// them in a call sets the value. A call that gets the same DecodeOptions
// twice therefore runs each of its validators twice. A program that also
// validates a document without decoding it keeps its [Validator] in a
// variable, and gives it to both WithValidator and [Node.Validate].
//
// DecodeOptions copies opts, so the result keeps its options when the
// caller changes the slice it passed. A nil DecodeOption among opts
// panics in the call that applies the result, as a nil DecodeOption
// given to that call does.
func DecodeOptions(opts ...DecodeOption) DecodeOption {
	opts = slices.Clone(opts)

	return func(c *decodeConfig) {
		for _, opt := range opts {
			opt(c)
		}
	}
}

// DecodeInto validates the node, decodes it into v, and validates the
// result. The node is the whole document for the root Node of a
// document, and v must be a non-nil pointer. Each [Validator] from
// [WithValidator] runs on the node first. The go-yaml decoder then fills
// v, and every value in v that implements [SelfValidator] validates
// itself. Fields absent from the document keep their existing values, so
// a caller may fill v with defaults first. Each error comes back bound to
// the source as a [SourceError], and an error of the decode itself
// matches [ErrDecode], unless it is a panic, which comes back as a
// [PanicError]. The error for a value the decoder rejects carries the
// position and the `$` path of that value, and a decode that fails
// reports the problems it finds together.
//
// A program that decodes many nodes with the same options states them
// once with [DecodeOptions].
//
// # Steps
//
// Any v that is not a non-nil pointer returns an error wrapping
// [ErrDecodeTarget], bound to the source, before anything runs. A
// document that did not parse then returns the syntax error [Node.Err]
// returns, and v stays as it is.
//
// Each [Validator] from [WithValidator] runs on the node before
// decoding, in the order given, and no validator runs when opts name
// none. After decoding succeeds, every value in v that implements
// [SelfValidator] validates itself, with the paths it reports put under
// the path of the value, unless [WithSelfValidation] switches that off.
// [Node.SelfValidate] runs that step on its own, once the caller has
// changed v.
//
// # Defaults
//
// Fields absent from the document keep their existing values, so a
// caller may fill v with defaults first. A mapping in the document merges
// into a struct v holds, field by field at every depth, and it merges the
// same way through a pointer to a struct and through an inline field. The
// document replaces a slice, an array, a map, or a value of an interface
// type whole, so no element or entry of the old one remains. A program
// that layers one file over another merges both through
// [NewSourceFromLayers] instead of decoding each in turn. The files then
// merge as documents, so a map keeps the entries of both, and each error
// binds in the file that holds its value.
//
// # Nulls and Tags
//
// A null with no tag, anchored or not, leaves v as it is, unless v points
// to a pointer or an interface. A null with neither a tag nor an anchor
// leaves a struct field as it is too, unless the field is a pointer,
// which the null sets to nil. The go-yaml decoder rejects a tagged or
// anchored null in a field of some kinds, such as an int or a struct.
// When v points to a pointer, a nil pointer gets a new value, and the
// node decodes into the value the pointer points to. An untagged null
// sets the pointer to nil. So does a document whose body is an alias to a
// null in a reference document, with a tag or without. A node without
// content, or a tagged null such as "!!null", leaves the pointer as it
// is.
//
// An integer with a !!int tag, such as `!!int 0x10`, decodes into any
// integer type, as the integer alone does.
//
// # Errors
//
// YAML decoding errors, and [Error] values from the validators, come back
// bound to the source as [SourceError] values, with an `@` path in them
// resolving from the scope. Every error of the decode itself matches
// [ErrDecode], which lists them, apart from a panic. A value the go-yaml
// decoder rejects, such as one that does not read as the target type,
// carries the `$` path of that value, so [SourceError.Path] reports it
// and the message names the value, as an error from a [Validator] or a
// [SelfValidator] at that value does:
//
//	config.yaml:3:11: $.servers[0].port: expected integer, got string
//
// The message describes the document and names no Go type. A value of the
// wrong kind reads "expected integer, got string". The kinds are mapping,
// sequence, string, integer, float, boolean, null, timestamp, binary for
// a value under a !!binary tag, and duration for a [time.Duration]
// target. A target that takes no YAML value but a null, such as a
// channel, reads "expected no value, got integer". A value the decoder
// rejects for its Go type alone reads "expected mapping, got mapping of
// another type", as the value of an anchor does when an inline field
// with an alias option cannot hold it. A number outside the range of an
// integer type reads "expected integer from 0 to 65535, got 70000". A
// rejection of a key, such as an unknown field or a key that does not
// read as the key type of a map, carries the path of the key, which ends
// in `~`.
//
// Three sentinels label these rejections, and each problem with a label
// matches ErrDecode too. A value of the wrong kind or of another type
// matches [ErrWrongKind], a number outside the range of an integer type
// matches [ErrOverflow], and an unknown field matches [ErrUnknownField].
// Any other problem matches ErrDecode alone, as ErrUnknownField
// describes. A caller that needs the Go types reaches the go-yaml error
// of a rejection with [errors.As], as a [yaml.TypeError], a
// [yaml.OverflowError], a [yaml.UnexpectedNodeTypeError], or a
// [yaml.UnknownFieldError]. A mapping or a sequence for a value with an
// UnmarshalText method matches ErrWrongKind and holds no go-yaml error.
// The text of a go-yaml error that names a
// token holds an excerpt of the document, which go-yaml builds itself.
// The chain of a rejection unwraps to no such error, so a reporter that
// prints each error of a chain prints the message and not that excerpt.
//
// The path names the place where the document writes the value. A value
// that an alias or a `<<` merge key brings in reports the path where its
// anchor defines it, which can lie outside the scope of a Node from
// [Node.At]. The error binds where that path resolves, so it marks the
// token an error from a validator at the same path marks. For a mapping
// or a sequence that is the token that introduces it, as [AtPath]
// describes, and for a scalar under a tag it is the scalar.
//
// A path cannot select every value. The earlier of two entries with one
// key is such a value, which a decode into a map reads when
// [WithAllowDuplicateKeys] lets the mapping hold both. An error at that
// value binds at the token the decoder reported, and the path names the
// value in the message. A value under a key with no name has no path, so
// its error carries that position alone.
//
// # Multiple Problems
//
// The decoder stops at the first value it rejects, in the order the
// target declares its fields, and it rejects an unknown field under
// [WithDisallowUnknownFields] only when it rejects no value. DecodeInto
// then looks for the other problems of the document, so one decode
// reports them together, in the order of the source:
//
//	app.yaml: 3 problems
//	app.yaml:1:1: $.replcas~: unknown field "replcas"
//	app.yaml:2:10: $.timeout: expected integer, got string
//	app.yaml:4:11: $.servers[0].port: expected integer, got string
//
// The error is a summary from [NewSummary] whose first line counts the
// problems, and reads "3 unknown fields" when each is an unknown field.
// It heads one error for each problem, which [ErrorTree.Problems]
// yields, and the rejection of the decoder is
// always one of them. The summary points at no value, so its
// [SourceError.Position] and [SourceError.Path] report false, and each
// error below it reports its own. The message lists [ErrorListLimit]
// problems at most and counts the rest, and ErrorTree.Problems yields
// every one. A document with one problem returns that problem as the
// error itself.
//
// The decoder decides whether the decode fails. DecodeInto adds a
// problem only where the decoder, given that one value alone, returns
// one of these rejections:
//
//   - A value of the wrong kind, or a number out of range. The value is
//     a scalar, or a mapping or a sequence where the target takes
//     another kind.
//   - A [time.Duration] that does not parse.
//   - A scalar that the UnmarshalText method of the target rejects, when
//     the target has neither an UnmarshalYAML method nor a function from
//     [WithCustomUnmarshaler]. An enum with an UnmarshalText method thus
//     reports every bad value in one decode.
//   - A mapping or a sequence where such a target takes a scalar, as the
//     paragraphs below describe.
//   - An unknown field, as [WithDisallowUnknownFields] describes.
//
// The search leaves every other problem to the next decode:
//
//   - It reads nothing at or below any other value that decodes itself,
//     apart from a [time.Time], since a second call of an unmarshaler
//     can answer otherwise than the first. A type with an UnmarshalYAML
//     method thus reports one bad value for each decode. A value that a
//     [WithCustomUnmarshaler] function decodes is such a value, and so
//     is one that decodes through an UnmarshalJSON method under
//     [WithJSONUnmarshalers].
//   - It reads nothing below an interface, and nothing in a mapping the
//     decoder decodes no field from, as WithDisallowUnknownFields lists
//     them.
//   - It reads nothing behind an alias to an anchor of a reference
//     document from [WithReferences], since the document holds no line
//     for that value.
//   - It adds nothing to a rejection that binds at no position in the
//     source, as the next paragraphs describe one, and nothing once ctx
//     has ended.
//   - No value validates itself until the decoder rejects nothing, so
//     the report holds no error of a [SelfValidator].
//
// One limit remains. Nothing shows which types
// [yaml.RegisterCustomUnmarshaler] gave a function for the whole program,
// so the search reads the values below such a type as the fields or
// elements of the type. The decoder confirms a problem with the struct
// that reads the value as a field, so such a function judges the fields
// of its own struct. The search can still report a value further below
// that the function accepts. A function from [WithCustomUnmarshaler] has
// no such limit.
//
// The search runs only after a decode fails, and it decodes one value at
// a time. The one unmarshaler it calls for a value it reads is the
// UnmarshalText method the list above names. It hands the method the
// scalar again, on a new value of the target type, so the method must
// answer from its text alone. One that reads the value v held before the
// decode, or state that an earlier call changed, can reject in the
// search what it took in the decode, and the report then holds a problem
// the document does not have. A decode that fails can call the method
// four times for one value. The decode calls it once. The second decode
// that finds the value behind an error calls it once more, as the
// paragraphs below describe. The search calls it once, and again to
// confirm a scalar the method rejects.
//
// To confirm a problem, the search decodes the struct that reads the
// value from that one entry. That decode can call a registered function
// again, and the unmarshaler of a field of the same name in an inline
// struct. What they return adds no problem.
//
// # Errors Without Tokens
//
// An error the decoder reports without a token of the source comes back
// with no location and keeps the text go-yaml gave it. The decoder
// reports one for a target type whose definition it refuses, such as a
// struct with two fields of one name or an inline embedded struct that
// is not exported. It reports another for a key of a map tagged inline,
// such as the key `name` beside a map[int]int, and for a field tagged
// inline whose type cannot hold a mapping, such as an int. A value of an
// inline map keeps its token, so an error in that value binds as it
// would in any other field. The error of a value that decodes itself has
// no token either, and the next paragraphs describe where it binds.
//
// A few errors without a token bind where the document causes them. A
// value nested deeper than the decoder allows binds at the first token
// of the node that is not a comment. A `<<` merge key whose alias names
// no anchor before it, or an anchor that holds the merge key, binds at
// the alias. A rejection of a value that an alias reads from a reference
// document from [WithReferences] binds at that alias when the node holds
// one alias to a reference document, directly or inside an anchor its
// aliases reach. When the node holds several, the error carries no
// location, even if the target type reads only one of them. The decoder
// reports two other errors as it reports that rejection, so they bind
// the same way in such a node. One is an unwrapped go-yaml error that an
// UnmarshalYAML returns from a parse of its own. The other is a go-yaml
// error the decoder reports without a token, such as the one for a key
// of an inline map[int]int.
//
// # Panics
//
// DecodeInto recovers a panic in the go-yaml decoder and in the code the
// decoder calls, which is an UnmarshalYAML or UnmarshalText method and a
// function from [WithCustomUnmarshaler]. The panic comes back as an error
// that holds a [PanicError], with the value of the panic and the stack.
// The error binds at the first token of the node that is not a comment.
// A panic below a value that a WithCustomUnmarshaler function decodes
// through its decode callback binds at that value instead. The error
// does not match [ErrDecode], and [IsInvalid] does not report it. It can
// come back beside the other problems of the document, in a summary that
// matches ErrDecode through them.
//
// The search for those problems and the second decode that the next
// section describes call an unmarshaler again, and a panic in such a
// call adds no problem. Nothing recovers a panic in a [Validator] or in
// the Validate method of a [SelfValidator], as PanicError describes.
//
// # Self-Decoding Values
//
// The decoder returns the error of a value that decodes itself with no
// token of the source. Such a value has an UnmarshalYAML or UnmarshalText
// method, an UnmarshalJSON method under [WithJSONUnmarshalers], or a
// function from [WithCustomUnmarshaler], or is a [time.Duration], which
// the decoder parses with [time.ParseDuration]. DecodeInto finds the
// value and binds the error at its path, so [SourceError.Path] reports
// the `$` path of the value and the message names the value:
//
//	config.yaml:7:14: $.servers[1].timeout: time: invalid duration "soon"
//
// The decoder hands an UnmarshalText method the text of a scalar. For a
// mapping or a sequence it calls no method and returns an error of its
// own, which names neither the value nor its kind. DecodeInto reports
// that value as it reports one a string field rejects:
//
//	config.yaml:4:3: $.nets[1]: expected string, got sequence
//
// Under [WithJSONUnmarshalers], a type that has an UnmarshalJSON method
// too takes the mapping or the sequence through that method instead.
//
// To find the value, DecodeInto decodes each such value of v a second
// time, from its own node into a new value, in the order the decoder
// reads them. The first one whose decode fails with the same message
// takes the error. An error that names a place has to name the same
// place both times. DecodeInto then looks below that value the same way,
// so a value that decodes itself through a second type with the same
// fields, such as `type plain T`, hands the error to the field that
// failed. The error of an unmarshaler stays in the chain, so it matches
// what the unmarshaler returned beside [ErrDecode].
//
// An unmarshaler holds no [Node], so an [*Error] it returns writes an
// `@` path that reads from its own value, as the error of a
// [SelfValidator] does. DecodeInto puts that path under the path of the
// value. An UnmarshalYAML of the value at `$.ranges[1]` that returns an
// Error at `@.to` thus reports the field it checked:
//
//	config.yaml:4:9: $.ranges[1].to: to is below from
//
// A `$` path stays as it is, and so does a position or a range, such as
// a position that an UnmarshalYAML built from its node. A value that
// returns the error of a value below it, with text of its own in front
// or without, leaves the path to the value that wrote it. An error that
// a source bound already keeps that binding, such as the error of a
// decode an unmarshaler runs on a source of its own.
//
// The second decode runs only after a decode fails, and it calls the
// unmarshaler of each value it reaches once more. The location is right
// for an unmarshaler that returns the same error for the same node. An
// unmarshaler that reads state an earlier call changed, such as a set
// of the names it has seen, may fail at another value the second time,
// and the error then binds there. An error that no value reproduces
// comes back as it is. It gains no location, and an `@` path it writes
// reads from the node the decode read. That holds for an unmarshaler
// that reads the value v held before the decode, for a value an alias
// reads from a reference document, and for a type that only a function
// from [yaml.RegisterCustomUnmarshaler] decodes, since the decoder never
// shows which types have one. It also holds for a mapping or sequence
// that is the node itself, where the error would point at the whole of
// what the caller decoded. A scalar that is the node itself takes the
// error. So does a mapping or a sequence the decoder refuses for an
// UnmarshalText method, since the node is the value it refused.
//
// # Aliases
//
// An alias inside the node resolves against the anchors of the whole
// document, to the anchor of its name defined last before the alias,
// inside the node or outside it, as a path through the alias resolves.
// That holds whatever order the decoder reads the anchors in, including
// the order of the fields of a struct. An alias with no anchor of its
// name before it reads an anchor of a reference document from
// [WithReferences], even when the document defines the name after the
// alias. A failure in an anchor outside the node that the node reads,
// directly or through another anchor, fails the decode, as it fails a
// decode of the whole document. An alias inside the anchor it names, such
// as `*x` in `b: &x {s: *x}`, reads as null into every target, in a
// decode of the node and of the whole document alike. The text the
// decoder hands an UnmarshalText or UnmarshalYAML method spells it as
// null too. A `<<` key that merges the anchor it sits in fails the decode
// instead. The decoder reads a copy of the document that gives a name of
// its own to an anchor whose name another anchor shares, and to an anchor
// that follows an alias of its name with no anchor before it. The copy of
// a document whose [Source] has reference documents from [WithReferences]
// gives every anchor a name of its own, so an alias inside a reference
// document never reads an anchor of the document. Each alias to a renamed
// anchor carries the new name too.
//
// # AST Nodes
//
// An [ast.Node] the decode fills, or one an UnmarshalYAML method takes,
// spells a renamed alias with the new name of its anchor. It spells a
// !!int tag on an integer in the verbatim form of the same tag,
// `!<tag:yaml.org,2002:int>`, which the go-yaml decoder reads as it
// reads the integer alone. The text an UnmarshalYAML method takes spells
// the tag as the document does. The node is part of the tree
// [Node.DocumentAST] returns, or of the copy of it that every decode of
// the document reads, so a caller must not modify it.
//
// # Alias Limit
//
// A few hundred bytes of nested aliases can take the go-yaml decoder
// minutes to decode, and the decoder never checks ctx. When the node
// holds an alias, DecodeInto counts what a decode of the whole document
// reads, with each alias reading its content in full, after the
// validators run. When aliases make up too much of that count, under the
// rule gopkg.in/yaml.v3 applies, it returns an error matching
// [ErrExcessiveAliasing] without decoding. Every node of such a document
// that holds an alias gets the same error. To decode a type with an
// UnmarshalText method or an UnmarshalYAML method that takes bytes, the
// decoder writes the node out as text, with a copy of the content of
// each alias. When the type of v, or a type the decoder reaches from it,
// is such a type, DecodeInto counts the document as text too, where each
// copy of a scalar weighs its length in bytes. An UnmarshalYAML method
// that takes a decode function can decode the node into any type it
// picks, so DecodeInto counts the document as text for a type with such
// a method too. An UnmarshalYAML method that takes the node reads it as
// it is, and the decoder calls it ahead of an UnmarshalText method. A
// type with such a method adds no such count, and neither do the types
// of its fields, which the decoder never reaches. The decoder reads a
// [time.Time] from its value, so it adds no such count either. A function
// from [WithCustomUnmarshaler] reads its node as text too, and so does an
// UnmarshalJSON method under [WithJSONUnmarshalers], so DecodeInto counts
// the document as text for a type either one decodes. The count sees only
// the types in v and the options of the decode, so it misses text that
// go-yaml hands a function from [yaml.RegisterCustomUnmarshaler].
// [WithAliasLimit] on the [Source] turns both counts off for every decode
// of its documents.
//
// # Decode Time
//
// The decoder reads every token of the file each time it writes a value
// out as text, whether or not the value holds an alias. The time of a
// decode therefore grows with the number of such values times the length
// of the file, where a decode that writes no text grows with the length
// alone. A list of 2,000 entries that each hold one value with an
// UnmarshalText method takes about a second to decode, and the same list
// of strings takes a few milliseconds. No deadline of ctx stops that
// decode. A [time.Time] or a [time.Duration] costs what a string does, and
// so does a type whose UnmarshalYAML method takes the node or a decode
// function.
func (n *Node) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	return n.decodeInto(ctx, v, newDecodeConfig(opts), nil)
}

// decodeInto is [Node.DecodeInto] with its settings resolved. The decode
// also collects the comments of the node in comments, as
// [Node.decodeNode] describes, unless comments is nil.
func (n *Node) decodeInto(ctx context.Context, v any, cfg decodeConfig, comments yaml.CommentMap) error {
	err := checkDecodeTarget(v)
	if err != nil {
		return n.bindOwn(err)
	}

	err = n.validate(ctx, cfg.validators)
	if err != nil {
		return err
	}

	err = aliasing.CheckDecode(n)
	if err == nil && aliasing.DecodesTextWith(reflect.TypeOf(v).Elem(), cfg.unmarshalers) {
		err = aliasing.CheckDecodeText(n)
	}

	if err != nil {
		return n.Bind(Invalid(err, atToken(astnode.StartToken(n.AST()))))
	}

	err = n.decodeNode(ctx, n.AST(), v, cfg, comments)
	if err != nil {
		return err
	}

	if cfg.skipSelfValidation {
		return nil
	}

	return n.selfValidate(ctx, v, cfg)
}

// checkDecodeTarget returns [ErrDecodeTarget] unless v is a non-nil
// pointer. The go-yaml decoder panics on a nil interface and decodes
// nothing into a nil pointer, so the check runs before v reaches it.
// When v points to a pointer, that pointer may be nil, since
// [decodeTarget] allocates it.
func checkDecodeTarget(v any) error {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return fmt.Errorf("%w: got nil", ErrDecodeTarget)
	}

	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("%w: got %T", ErrDecodeTarget, v)
	}

	return nil
}

// maxPointerDepth caps the pointers [decodeTarget] allocates through. A
// type that refers back to itself, such as "type P *P", never reaches a
// value that is not a pointer, and the go-yaml decoder loops forever on
// a non-nil one.
const maxPointerDepth = 8

// decodeTarget returns the pointer the go-yaml decoder decodes node into
// for v, a non-nil pointer. The decoder decodes nothing into a nil
// pointer and replaces the value behind a non-nil one. When v points to
// a pointer, decodeTarget therefore allocates each nil pointer on the way
// down and returns the last one, and the decoder fills its value in
// place. For a node that reads as null, it returns v, so the decoder sets
// *v to nil. It also returns v when the pointers go deeper than
// [maxPointerDepth].
func decodeTarget(v any, node ast.Node) any {
	if anchor, ok := node.(*ast.AnchorNode); ok {
		node = anchor.Value
	}

	if astnode.IsNil(node) || node.Type() == ast.NullType {
		return v
	}

	rv := reflect.ValueOf(v)

	depth := 0
	for t := rv.Type().Elem(); t.Kind() == reflect.Pointer; t = t.Elem() {
		depth++
		if depth > maxPointerDepth {
			return v
		}
	}

	for range depth {
		elem := rv.Elem()
		if elem.IsNil() {
			elem.Set(reflect.New(elem.Type().Elem()))
		}

		rv = elem
	}

	return rv.Interface()
}

// yamlOptions returns the go-yaml options for a decoder of a call with
// cfg: the source's decode options, then the ones the settings of cfg
// stand for. The function of each [WithCustomUnmarshaler] in cfg reads
// its value as [Node.decodeValue] decodes it with cfg.
func (n *Node) yamlOptions(cfg decodeConfig) []yaml.DecodeOption {
	decode := func(ctx context.Context, text []byte, dst any) error {
		return n.decodeValue(ctx, text, dst, cfg)
	}

	return slices.Concat(n.source.decodeOptions(), cfg.yamlOptions(decode))
}

// decodeValue decodes text into dst with cfg, for the decode callback of
// a [WithCustomUnmarshaler] function. The text is what the go-yaml
// decoder hands that function for one value of the document of n, and
// dst is the pointer the function gave the callback. Any other dst
// returns an error wrapping [ErrDecodeTarget].
//
// The text is a document of its own, so decodeValue reads it as a
// [Source] that takes the settings of the source of n, and decodes the
// document of that Source as [Node.decodeNode] does. The settings of cfg
// that say how a value decodes thus apply below the value too, and a
// failed decode reports every problem of the value. A [Validator] checks
// a document, so decodeValue runs none.
//
// No Node binds the error of that decode. It names places in the text,
// and decodeValue returns it as [valueError] writes it for the decode
// that called the function to place. An error of ctx comes back as it
// is.
func (n *Node) decodeValue(ctx context.Context, text []byte, dst any, cfg decodeConfig) error {
	err := checkDecodeTarget(dst)
	if err != nil {
		return err
	}

	source := NewSourceFromBytes(text, func(c *sourceConfig) {
		c.allowDuplicateKeys = n.source.allowDuplicateKeys
	})

	doc, err := source.Document()
	if err != nil {
		// A panic of the parser stays a panic, so the decode that called
		// the function passes it on.
		if p, ok := errors.AsType[*PanicError](err); ok {
			return fmt.Errorf("parse the text of the value: %w", recoveredError{err: p, in: "parser"})
		}

		// The decoder wrote the text from a node that parsed, and the
		// positions of the error count the lines of that text.
		//nolint:errorlint // The binding of the text is no binding of the document.
		return fmt.Errorf("parse the text of the value: %v", err)
	}

	return valueError(doc.decodeUnbound(ctx, doc.AST(), dst, cfg, nil))
}

// decodeNode decodes node to v with cfg, and binds the error to the
// source: a rejection of the decoder as an [*Error] at the path of the
// offending value, as [Node.bindDecodeError] describes, and any other,
// such as a canceled context, as it is. A node without content,
// the body of an empty document, leaves v as it is, so defaults already
// in v survive, as [Node.DecodeInto] promises. [yaml.Unmarshal] instead
// zeroes its target for input that holds no value. A tagged null, such
// as a "!!seq" tag over no value, also leaves v as it is, since the
// go-yaml decoder reads it as no value. A null with no tag, anchored or
// not, leaves v as it is too, unless v points to a pointer or an
// interface, as [keepsNullTarget] describes. When v points to a pointer,
// a null sets that pointer to nil, and so does an alias that reads as
// null. A panic in the decoder comes back as an error that holds a
// [*PanicError], bound at the first token of node that is not a
// comment. The decoder reads node in the [decodetree.Tree] of the document,
// and for a node below the body, a failure in an anchor outside node
// that node reads comes back as its error. The error of a value that
// decodes itself binds at the path [Node.locateDecodeError] finds for
// that value. A rejection comes back with the other problems of the
// document that [Node.decodeProblems] finds, bound as one error.
//
// The decoder that fills v also collects the comments of node in
// comments, as [yaml.CommentToMap] collects them, unless comments is
// nil. [Node.YAMLComments] passes an empty map of its own. A node that
// starts no decoder adds nothing to the map. DecodeNode empties the map
// once the decoder has registered the anchors a node below the body
// reads, since their comments lie outside node.
//
// The go-yaml decoder never checks the context, so a context that has
// ended before the decode starts, or while it registers the anchors node
// needs, stops the decode, and its error comes back as it is, whatever
// node holds.
func (n *Node) decodeNode(
	ctx context.Context,
	node ast.Node,
	v any,
	cfg decodeConfig,
	comments yaml.CommentMap,
) error {
	return n.bindOwn(n.decodeUnbound(ctx, node, v, cfg, comments))
}

// decodeUnbound decodes node to v with cfg, as [Node.decodeNode]
// describes, and returns the error before the Node binds it.
// [Node.decodeValue] writes that error for another document to bind.
func (n *Node) decodeUnbound(
	ctx context.Context,
	node ast.Node,
	v any,
	cfg decodeConfig,
	comments yaml.CommentMap,
) error {
	err := ctx.Err()
	if err != nil {
		return err //nolint:wrapcheck // The caller binds the error.
	}

	if !astnode.HasContent(node) || astnode.IsTaggedNull(node) || keepsNullTarget(node, v) {
		return nil
	}

	yamlOpts := n.yamlOptions(cfg)
	if comments != nil {
		yamlOpts = append(yamlOpts, yaml.CommentToMap(comments))
	}

	dec := yaml.NewDecoder(bytes.NewReader(nil), yamlOpts...)
	view := n.doc.tree().View(node)

	// The decoder registers the anchors of the node it decodes, so an alias
	// in a node below the body finds an anchor defined elsewhere in the
	// document only after the decoder has seen that anchor.
	if node != n.doc.root.Body {
		err = n.primeAnchors(ctx, dec, view)
		if err != nil {
			return n.decodeRejection(err)
		}

		// The pass decoded anchors outside node with dec, which wrote
		// their comments to the map.
		clear(comments)
	}

	// The decoder tests an alias for null, rather than the anchor it
	// names, so it gives a pointer target a value for an alias to a null.
	target := reflect.ValueOf(v).Elem()
	if target.Kind() == reflect.Pointer && isNullAlias(ctx, dec, node, view) {
		target.SetZero()

		return nil
	}

	// The recover turns a panic in the go-yaml decoder, or in a value's
	// own UnmarshalYAML, into an error that holds a PanicError.
	err = decodeWithRecover(ctx, dec, view, decodeTarget(v, node))

	// The decoder returns the error of a value that decodes itself with
	// no token, so the Node finds the value that reported it.
	err = n.locateDecodeError(ctx, n.rejection(err, view), node, v, cfg)

	// The decoder stops at its first rejection, so the Node finds the
	// other problems of the document and reports them together.
	return n.decodeProblems(ctx, err, node, v, cfg)
}

// keepsNullTarget reports whether node, or the value an anchor on node
// names, is a null with no tag, and v points to a value that is neither
// a pointer nor an interface. The go-yaml decoder keeps such a value for
// a null with no tag or anchor in a struct field. For a null at the top
// of a decode, it keeps some kinds, such as a string, and rejects
// others, such as an int, a map, or a struct. [Node.decodeNode] skips
// the decoder when this holds, so a null keeps the value of every such
// kind, as it keeps the field. [Node.At] and [Node.Nodes] hand a decode
// the value an anchor names, so keepsNullTarget looks through the anchor
// to read an anchored null at the root as a scoped decode reads it. The
// decoder still rejects an anchored null in a field, as it rejects a
// tagged one.
func keepsNullTarget(node ast.Node, v any) bool {
	if anchor, ok := node.(*ast.AnchorNode); ok {
		node = anchor.Value
	}

	if astnode.IsNil(node) || node.Type() != ast.NullType {
		return false
	}

	switch reflect.TypeOf(v).Elem().Kind() {
	case reflect.Pointer, reflect.Interface:
		return false
	default:
		return true
	}
}

// isNullAlias reports whether node is an alias, or an anchor on one,
// that dec reads as null. [Node.At] and [Node.Nodes] resolve every alias
// on a path to its anchor, so only the body of a document reaches a
// decode as an alias. Such an alias names an anchor of a reference
// document, which only dec can read. The check reads view, the
// [decodeView] of node, with dec itself, which holds the anchors of the
// reference documents for the decode that follows.
func isNullAlias(ctx context.Context, dec *yaml.Decoder, node, view ast.Node) bool {
	if anchor, ok := node.(*ast.AnchorNode); ok {
		node = anchor.Value
	}

	if _, ok := node.(*ast.AliasNode); !ok {
		return false
	}

	var value any

	err := decodeWithRecover(ctx, dec, view, &value)

	return err == nil && value == nil
}

// rejection returns err, which the go-yaml decoder returned for scope, a
// node of the [decodetree.Tree], as an [*Error] that matches [ErrDecode] and
// [errPlaced] when the decoder reported the rejection without a token of
// the source. The depth limit of the decoder binds at the first
// token of scope that is not a comment. A `<<` merge key whose alias
// names no mapping the decoder can find binds at the alias, when err is
// the decoder's failure for that alias, as [decodetree.Tree.UnresolvedMerge]
// describes, with the message a decode of the whole document gives that
// alias. Any other [yaml.Error] without a token of the source comes back
// that way when scope holds an alias to a reference document,
// as [decodetree.Tree.ReferenceAliases] finds them, with go-yaml's position
// and excerpt of that document left out and the message and the label
// [document.rejectionText] gives it. It binds at the alias when
// scope holds one such alias, and carries no location when it holds
// several, since the token does not say which of them led to it. The
// count ignores the target type, so a type that reads only one of
// several such aliases gets no location either. The decoder gives no
// way to tell such an error from an unwrapped [yaml.Error] of the parse
// an UnmarshalYAML runs on its bytes, so that error comes back the same
// way. Any other error comes back as it is, such as one from a value's own
// UnmarshalYAML, an ended context, or a panic [decodeWithRecover]
// already placed. So does any error for a nil scope.
func (n *Node) rejection(err error, scope ast.Node) error {
	if err == nil || astnode.IsNil(scope) || errors.Is(err, errPlaced) || contextEnded(err) {
		return err
	}

	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if ok && n.holdsToken(yamlErr.GetToken()) {
		return err
	}

	tree := n.doc.tree()

	var (
		at    *token.Token
		cause error
	)

	if errors.Is(err, yaml.ErrExceededMaxDepth) {
		at, cause = astnode.StartToken(scope), n.doc.restoreError(err)
	} else if tk, msg, ok := tree.UnresolvedMerge(scope, err); ok {
		// The decoder's failure for an alias inside its own anchor names
		// the null it merges in its place, which the source does not hold,
		// so the message is the one a decode of the whole document gives.
		at, cause = tk, restoredError{err: err, msg: msg}
	}

	if at == nil && ok {
		refs := tree.ReferenceAliases(scope)
		if len(refs) == 0 {
			return err
		}

		msg, label := n.doc.rejectionText(yamlErr)
		rejected := decodeError{err: yamlMessageError{err: yamlErr, msg: msg}, label: label, placed: true}

		// The token does not say which anchor of a reference document
		// holds it, so only a decode that reads one alias from a
		// reference knows which alias led to it.
		if len(refs) > 1 {
			return Invalid(rejected)
		}

		return Invalid(rejected, atToken(refs[0]))
	}

	if at == nil {
		return err
	}

	rejected := decodeError{err: fmt.Errorf("decoder rejected the value: %w", cause), placed: true}

	return Invalid(rejected, atToken(at))
}

// bindDecodeError binds an error from the decoder to the source. A
// [yaml.Error] at a token of the source binds as an [*Error] that matches
// [ErrDecode], with the message and the label [document.rejectionText]
// gives it. The Error carries the location [Node.rejectionLocation] gives
// the token, which is the path of the node the decoder names by it. That
// path starts at `$`, so the Node binds the Error with no scope in front
// of the path. Any other error binds with the location it carries, if
// any, such as one a value's own UnmarshalYAML returns, or one the
// decoder reports without a token of the source. It matches ErrDecode
// too, as [asDecodeError] returns it, so the error of a canceled context
// binds as it is. An error that [Node.locateDecodeError] put under a path
// binds at that path. The decoder returns the error of a value that
// decodes itself, and one for a target type whose definition it refuses,
// as plain errors. It builds the mapping a field tagged inline decodes
// from, with no token for the mapping or for its keys, around the values
// of the source. So it returns a [yaml.Error] with no token for a key of
// an inline map, such as the key `name` beside a map[int]int, and for an
// inline field whose type cannot hold a mapping, such as an int. A
// [yaml.Error] for a value of an inline map has the token of that value,
// so it binds as it does for any other value. Only a [yaml.Error] the
// decoder returns itself converts, so a [yaml.Error] that a value's
// UnmarshalYAML wraps comes back as that unmarshaler's error, with the
// text and sentinels of its wrapper. An UnmarshalYAML that parses the
// bytes it gets returns a [yaml.Error] of its own, whose token comes from
// that parse rather than the source, so it stays the value's own error,
// unless [Node.rejection] took it for a rejection of a value from a
// reference document. A message that names an anchor the [decodetree.Tree]
// renamed names it as the document does. Returns nil for a nil err.
func (n *Node) bindDecodeError(err error) error {
	if err == nil {
		return nil
	}

	// The path of a rejection starts at `$`, so the scope of n does not
	// go in front of it.
	return n.bindOwn(n.decodeRejection(err))
}

// holdsToken reports whether tk is a token of the source's parse, one of
// the tokens the parser built its tree from, or a token of the parse the
// [decodetree.Tree] of the document reads, as [decodetree.Tree.Holds] describes.
// It compares pointers, so a token from another parse, such as the one an
// UnmarshalYAML runs on its bytes, never matches, however closely it
// resembles a token of the source.
func (n *Node) holdsToken(tk *token.Token) bool {
	if tk == nil {
		return false
	}

	if _, ok := n.source.parsed().result.Index[tk]; ok {
		return true
	}

	return n.doc.tree().Holds(tk)
}

// errPlaced marks an error of a decode that [decodeWithRecover] or
// [Node.rejection] placed in the source, so [Node.locateDecodeError]
// looks for no value that reported it.
var errPlaced = errors.New("placed")

// decodeError is an error of a decode, which matches [ErrDecode] and
// [errInvalid]. It reads as the error it holds and unwraps to it. An
// error of the parse matches [ErrSyntax] instead, as a [syntaxError]
// does.
type decodeError struct {
	err error
	// The label the error matches too, which is [ErrUnknownField],
	// [ErrWrongKind], or [ErrOverflow], or nil for an error with no label.
	// The code that writes the message of the error sets it.
	label error
	// Whether the error matches [errPlaced] too.
	placed bool
}

func (e decodeError) Error() string {
	return e.err.Error()
}

func (e decodeError) Unwrap() error {
	return e.err
}

// Is reports whether target is [ErrDecode] or [errInvalid], the label
// of the error, or [errPlaced] for an error the decode placed.
func (e decodeError) Is(target error) bool {
	return target == ErrDecode || target == errInvalid ||
		e.label != nil && target == e.label || e.placed && target == errPlaced
}

// asDecodeError returns err, an error the go-yaml decoder returned, as
// one that matches [ErrDecode]. The error of a context that ended comes
// back as it is, even when an unmarshaler wraps it. So does an error that
// holds a [*PanicError], as [holdsPanic] reports, and one that matches
// already.
func asDecodeError(err error) error {
	if err == nil || errors.Is(err, ErrDecode) || contextEnded(err) || holdsPanic(err) {
		return err
	}

	return decodeError{err: err}
}

// yamlMessageError is a [yaml.Error] reduced to its message. The go-yaml
// text carries its own position and excerpt, which the [SourceError]
// binding the error renders itself, so the message alone goes in the
// chain. A yamlMessageError unwraps to nothing, so a reporter that
// prints each error of a chain never prints that excerpt, which holds
// lines of a [Source] whatever [WithExcerpts] says of it. [errors.Is]
// and [errors.As] still reach the go-yaml error. A msg that is not empty
// stands in for the message of err.
type yamlMessageError struct {
	err yaml.Error
	msg string
}

func (e yamlMessageError) Error() string {
	if e.msg != "" {
		return e.msg
	}

	return e.err.GetMessage()
}

// Is reports whether the go-yaml error e holds matches target.
func (e yamlMessageError) Is(target error) bool {
	return errors.Is(e.err, target)
}

// As sets target to the first error in the chain of the go-yaml error e
// holds that matches it, as [errors.As] does.
func (e yamlMessageError) As(target any) bool {
	return errors.As(e.err, target)
}

// decodeWithRecover decodes node into v with dec. It turns a panic in the
// decoder, or in the code of the caller that the decoder runs, into an
// [*Error] that holds a [*PanicError] and matches [errPlaced]. The Error
// points at the first token of node that is not a comment, so a comment
// above the value does not take the location. The panic is no fault of
// the document, so the Error declares nothing and matches neither
// [ErrDecode] nor [errInvalid]. The error of a [WithCustomUnmarshaler]
// function comes back as the function returned it, out of the
// [shieldedError] the decoder carried it in.
func decodeWithRecover(ctx context.Context, dec *yaml.Decoder, node ast.Node, v any) (err error) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}

		panicked := recovered("decoder", p, debug.Stack())
		panicked.placed = true

		err = Place(panicked, atToken(astnode.StartToken(node)))
	}()

	err = dec.DecodeFromNodeContext(ctx, node, v)

	// The decoder carried the error of a function from an option inside
	// a shield, so it could not rewrite it.
	if shielded, ok := err.(shieldedError); ok { //nolint:errorlint // The decoder returns it unwrapped.
		return shielded.err
	}

	return err //nolint:wrapcheck // The caller binds the error.
}

// Decode validates and decodes the node, which is the whole document for
// the root Node of a document, into a new T.
//
// Each [Validator] from [WithValidator] runs on the node before
// decoding, as [Node.DecodeInto] describes, and no validator runs when
// opts name none. After decoding succeeds, every value in the result
// that implements [SelfValidator] validates itself, unless
// [WithSelfValidation] switches that off. The values below T validate
// first, and T validates last and only when every one of them passed,
// as [SelfValidator] describes. The method set of a pointer includes
// the methods declared on the value, so both value and pointer
// receivers participate. YAML decoding errors, and [Error] values from
// the validators, come back bound to the source as [SourceError]
// values, and every error of the decode itself matches [ErrDecode],
// apart from a panic, which comes back as a [PanicError]. A node whose
// document holds too many nested aliases returns an error matching
// [ErrExcessiveAliasing], as [Node.DecodeInto] describes. An
// [ast.Node] in the result is part of a
// tree the document shares, as [Node.DecodeInto] describes, so a caller
// must not modify it. On error, the returned T is the zero value.
//
// A scoped Decode reads one typed value without decoding the whole
// document, such as a version number or a list of tags. A scalar decodes
// into a string whatever its type, so a Decode[string] reads a
// discriminator field that holds a word, such as kind. A number decodes
// into a string in its canonical spelling, so 1.10 reads as "1.1" and
// 0x10 as "16". [go.jacobcolvin.com/niceyaml/schema/matcher.Text] hands
// its function the text as the document wrote it, for a field whose
// spelling matters, such as a version. [Node.DecodeAt] scopes the Node
// and decodes it in one call:
//
//	kindPath := paths.Current().Child("kind")
//	for _, doc := range docs {
//		kind, err := doc.DecodeAt[string](ctx, kindPath)
//		if err != nil {
//			return err
//		}
//
//		switch kind {
//		case "Pod":
//			// Decode to Pod struct.
//		case "Service":
//			// Decode to Service struct.
//		}
//	}
//
// For the YAML text of any node, including a mapping or a sequence, take
// the node from [Node.AST] and call its String method. The YAML text of
// a scalar keeps its quotes and the comment after it, so "2.0" # two
// reads as that whole text.
//
// To decode into a value you already hold, use [Node.DecodeInto]. To
// decode many nodes with options stated once, hold them in
// [DecodeOptions].
func (n *Node) Decode[T any](ctx context.Context, opts ...DecodeOption) (T, error) {
	var v T

	err := n.DecodeInto(ctx, &v, opts...)
	if err != nil {
		var zero T

		return zero, err
	}

	return v, nil
}

// YAMLComments returns the comments of the node in a new map, each under
// the YAML path of the value it belongs to, as go-yaml's
// [yaml.CommentToMap] option collects them.
// [go.jacobcolvin.com/niceyaml/encoder.WithYAMLComments] writes such a
// map back, so a program that decodes a document, changes the value, and
// encodes it again keeps the comments:
//
//	cfg, err := doc.Decode[Config](ctx)
//	if err != nil {
//		return err
//	}
//
//	comments, err := doc.YAMLComments(ctx)
//	if err != nil {
//		return err
//	}
//
//	cfg.Name = "web"
//
//	out, err := encoder.Marshal(ctx, cfg, encoder.WithYAMLComments(comments))
//
// The call runs a decode of the node, as [Node.DecodeInto] decodes it
// into an any with no [DecodeOption], so a program that also decodes the
// node reads it twice. A decode that fails returns its error, as
// DecodeInto returns it, and a nil map. A document that did not parse
// fails that way, and so does a node that holds an alias with no anchor.
// A node that holds no value returns an empty map, as the body of an
// empty document or of a document of comments alone does.
//
// Each key is a path from the root of the document, as go-yaml writes
// it, whatever node the call reads. The encoder reads each key from the
// root of the value it writes, so the map of a whole document pairs with
// an encode of the value of that document. The map of a Node from
// [Node.At] does not pair with an encode of the value of that node
// alone.
func (n *Node) YAMLComments(ctx context.Context) (yaml.CommentMap, error) {
	var value any

	comments := yaml.CommentMap{}

	err := n.decodeInto(ctx, &value, decodeConfig{skipSelfValidation: true}, comments)
	if err != nil {
		return nil, err
	}

	return comments, nil
}

// newDecodeTree creates the [*decodetree.Tree] of the document.
func (d *document) newDecodeTree() *decodetree.Tree {
	src := d.node.source

	return decodetree.New(d.root.Body, d.state.Facts(), decodetree.Options{
		Reparse: func() (ast.Node, map[*token.Token]int) {
			r := src.decodeParse()
			if d.fileIndex < 0 || d.fileIndex >= len(r.File.Docs) {
				return nil, nil
			}

			return r.File.Docs[d.fileIndex].Body, r.Index
		},
		Texts: func() []string {
			return append(spellingTexts(d.tokens), src.referenceTexts()...)
		},
		References: len(src.references) > 0,
	})
}

// primeAnchors registers with dec the anchors that the aliases in node,
// a node of the [decodetree.Tree] of the document below its body, refer
// to, as [decodetree.Tree.Prime] lists them. Each anchor decodes on its own.
//
// Every anchor of the tree has a name of its own, so the pass decodes
// only anchors that node, or an anchor node reads, refers to. A failure
// in one of them would leave an alias with nothing to read, so the pass
// stops and returns it, as a decode of the whole document fails there
// too, as [Node.rejection] reads it for that anchor. A context that ends
// stops the pass, which then returns the error of the context.
func (n *Node) primeAnchors(ctx context.Context, dec *yaml.Decoder, node ast.Node) error {
	for _, anchor := range n.doc.tree().Prime(node) {
		// Without an anchor it needs, the decode of the node would fail
		// in its stead.
		err := ctx.Err()
		if err != nil {
			return err //nolint:wrapcheck // The context names the reason, and the Node binds it.
		}

		var sink any

		err = decodeWithRecover(ctx, dec, anchor, &sink)
		if err != nil {
			return n.rejection(err, anchor)
		}
	}

	return nil
}

// rejectionText returns the message [rejection.Classify] writes for err,
// with each anchor named as the document names it, and the sentinel of
// the label Classify gives err. The decoder writes text of the document
// into some messages as it is, such as the name of an unknown field, so
// each control character comes back as its picture and the message stays
// on one line.
func (d *document) rejectionText(err yaml.Error) (string, error) {
	msg, label := rejection.Classify(err)

	return escape.Control(d.tree().RestoreNames(msg)), rejectionLabel(label)
}

// restoreError returns err with a message that spells each name the
// [decodetree.Tree] of the document gave an anchor as the document does.
// It returns err itself when the message names no such anchor, and when
// err holds an [*Error] or a [*SourceError], whose message the binding
// writes from its parts.
func (d *document) restoreError(err error) error {
	var (
		located *Error
		bound   *SourceError
	)

	if errors.As(err, &located) || errors.As(err, &bound) {
		return err
	}

	msg := err.Error()

	restored := d.tree().RestoreNames(msg)
	if restored == msg {
		return err
	}

	return restoredError{err: err, msg: restored}
}

// restoredError is an error from the decoder with a message that spells
// the names of anchors as the document does. It unwraps to the error the
// decoder returned.
type restoredError struct {
	err error
	msg string
}

func (e restoredError) Error() string {
	return e.msg
}

func (e restoredError) Unwrap() error {
	return e.err
}

// spellingTexts returns the texts of tks that a new name must not spell:
// the text of tks, which holds a spelling that runs across tokens, such
// as a tag before a flow sequence, and the value of each token that holds
// " [". A value differs from the text where a quoted scalar holds an
// escape or a scalar folds a line break.
func spellingTexts(tks token.Tokens) []string {
	var (
		text   strings.Builder
		values []string
	)

	for _, tk := range tks {
		if tk == nil {
			continue
		}

		text.WriteString(tk.Origin)

		if strings.Contains(tk.Value, " [") {
			values = append(values, tk.Value)
		}
	}

	return append(values, text.String())
}

// PathAt returns the path of the innermost node of the document at pos,
// and true. The position is in the coordinates of [Source.Lines], where
// line 0 is line 1 of the text, as a cursor in a viewer, the start of a
// [go.jacobcolvin.com/niceyaml/finder.Finder] match, or
// [SourceError.Position] gives one. A viewer thus names the value under
// its cursor:
//
//	if path, ok := doc.PathAt(cursor); ok {
//		status = path.String() // $.spec.containers[0].image
//	}
//
// The path starts at `$`, the root of the document, whichever [Node] of
// the document the caller holds. A `$` path resolves from that root
// through any Node, so the path goes back to the [Node.At] or the
// [Node.Ranges] of the Node that returned it, or of any other Node of
// the document. A position in another document of the source reports
// false, so a caller with a file of several documents asks each one:
//
//	for _, doc := range docs {
//		if path, ok := doc.PathAt(pos); ok {
//			return doc, path
//		}
//	}
//
// The innermost node is an entry of a mapping or an element of a
// sequence. A position on the key of an entry gives a path that ends in
// `~`, as [paths.Path.Key] writes one, and a position on the ":" or the
// value gives the path of the entry. The "-" of a block sequence belongs
// to its element. A collection holds text of its own between its entries
// or elements, such as "[", "{", ",", and the anchor or tag in front of
// it. A position there gives the path of the collection, which is the
// root for a collection or a scalar at the root of the document. A
// position in the indentation of a line resolves to the token that
// follows it, as [line.Lines.TokenAt] describes.
//
// The path is the place where the document writes the node. A position
// on an alias gives the path of the entry or element that holds the
// alias. A position inside the content of an anchor gives the path where
// the anchor defines that content, though a path through an alias or a
// `<<` merge key reaches the same node.
//
// PathAt reports false where no node of the document lies: on a comment,
// on a blank line, on a "---" header, a "..." marker, or a directive, and
// outside the lines of the source. It reports false for a document that
// did not parse, as [Node.Err] describes, and for one with no content.
// It also reports false for a position inside an entry that no path
// selects. Such an entry is the earlier of two entries with one key, one
// that a `<<` merge key after it overrides, or one whose key has no
// name, as [paths.Resolver.KeyName] reports. A path it returns therefore
// selects the node at pos, and none of its selectors is a wildcard.
//
// The first call reads the tree of the document once, and every Node of
// the document shares what it found.
func (n *Node) PathAt(pos position.Position) (paths.Path, bool) {
	if n == nil || n.doc.err != nil {
		return paths.Path{}, false
	}

	tk := n.source.lines.TokenAt(pos)
	if tk == nil || tk.Position == nil || tk.Type == token.CommentType {
		return paths.Path{}, false
	}

	return n.doc.pathIndex().PathAt(n.doc.pathResolver(), tk.Position.Offset)
}
