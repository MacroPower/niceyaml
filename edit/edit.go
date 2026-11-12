package edit

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/bom"
	"go.jacobcolvin.com/niceyaml/internal/fault"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/mapkey"
	"go.jacobcolvin.com/niceyaml/internal/quantity"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

var (
	// ErrEngine indicates an edit that the engine cannot write so that the
	// text reads as the calls ask. It marks a bug in the engine, and it
	// also marks go-yaml behavior that the engine cannot write around.
	// Go-yaml's parser can refuse or misread the edited text, with or
	// without its comments, or panic on it. Go-yaml's decoder can refuse
	// it. Go-yaml's encoder can panic on the value of a Set, or write text
	// that go-yaml's decoder reads as another value. It can also write a
	// character that YAML does not allow in a text, or both "\n" and
	// "\r\n" line breaks for YAML of one kind. A call can also write a text
	// that go-yaml's decoder reads otherwise than YAML does, in one of the
	// ways that the Supported YAML section of the package documentation
	// lists. The engine wrote that text, so the call fails with ErrEngine.
	// The message names go-yaml in each of those cases. ErrEngine also
	// indicates new text that differs from the model of the calls of an
	// Apply, as the Transactions section of the package documentation
	// describes. It also indicates a call that the model cannot take. The
	// error binds where the user's text holds the value that differs, or at
	// the place of the call, as the Errors section of the package
	// documentation describes. An error that matches ErrEngine never
	// matches [ErrUnsupported] or [ErrMisread].
	ErrEngine = errors.New("the engine cannot write this edit")

	// ErrMisread indicates a document of valid YAML that go-yaml's decoder
	// reads otherwise than YAML does, in one of the ways that the Supported
	// YAML section of the package documentation lists. Only [New] fails with
	// an error that matches ErrMisread. The problem names go-yaml and what
	// it reads, and its detail says what the user can write so that go-yaml
	// reads the text as YAML does. A text that go-yaml's parser refuses
	// fails New with the error of the parse, as the Source gives it. That
	// error does not match ErrMisread.
	//
	// The error of New holds a problem for each instance that New refuses,
	// so one error can match both ErrMisread and [ErrUnsupported]. A program
	// reads the kind of each problem from the Err field of each row that
	// [go.jacobcolvin.com/niceyaml.Problems] yields.
	//
	// The user can fix the text, so [go.jacobcolvin.com/niceyaml.IsInvalid]
	// reports true for the error. It reports true for a document that holds
	// YAML outside the subset that the engine edits too. It reports false
	// for a refusal that the program decides, such as one that matches
	// [ErrUsage], [ErrValue], or [ErrProtected]. A program can write the
	// paths and the values of its calls itself. Its server then classifies
	// the errors of the package with IsInvalid alone:
	//
	//	switch {
	//	case err == nil:
	//	case niceyaml.IsInvalid(err):
	//		status = 422 // the user rewrites the text as the detail says
	//	default:
	//		status = 500 // a bug in the program or the engine, among other faults
	//	}
	//
	// A server that passes a path or a value of the request to a call lets
	// the request decide more refusals. Such a server tests for ErrUsage,
	// ErrValue, and ErrProtected before the default case. It also tests for
	// an [*UnsupportedError] whose InValue field is true, which a value of
	// the request with an anchor or a tag fails with. A typed key of the
	// request that a [Node.SetKey] writes fails that way too when a decode
	// reads it as another value, as 1.0 reads as 1, or as a string at its
	// place.
	//
	// A call that writes such a text fails with an error that matches
	// [ErrEngine] and not ErrMisread, because the engine wrote that text. So
	// does go-yaml's encoder, and go-yaml's parser or decoder when it refuses
	// the text that the engine wrote.
	ErrMisread = errors.New("go-yaml misreads the document")

	// ErrUsage indicates a call with an argument that the package does not
	// take, whatever the document holds. [New] fails so with a nil Node, a
	// Node below the root of its document, or a path of [WithProtected] that
	// is not a `$` path of names and indexes. [Doc.Apply] fails so with a
	// path that does not start at `$` or that holds a `~`. [FileText] fails
	// so with a Doc of another Source, or with two Docs of one document.
	// Those errors bind nowhere, and the message says what the call takes,
	// as in "Apply takes a $ path without ~, not @.spec".
	//
	// A method of [Node] fails so too, and the error binds at the place of
	// the call, as the Errors section of the package documentation
	// describes. The methods fail so with:
	//
	//   - a path of [Node.At] that selects more than one value or holds a
	//     `~`, at each method that edits or decodes and at a [Node.Nodes] of
	//     an `@` path
	//   - a path of [Node.Nodes] that holds a `~`
	//   - a Set of the root of a document that holds a value
	//   - a SetKey, a Move, or a Remove of the root
	//   - a SetKey of an item of a sequence
	//   - an Insert at a path that does not end in an index
	//   - a Move to a target that is no `$` path of names and indexes, to
	//     `$`, or to a path inside the value that moves
	//   - a [Before] or an [After] of a Move to a target in the mapping of
	//     the entry that moves, when it names the key of that entry
	//
	// The program is at fault for each, so
	// [go.jacobcolvin.com/niceyaml.IsInvalid] reports false for the error.
	// Each fails at a place that does not hold a value too, where the call
	// does nothing otherwise, except a Set of the root. A Before or an After
	// that names the entry that moves through an alias of the document fails
	// without ErrUsage, because the document decides it.
	ErrUsage = errors.New("the program passes an argument that the package does not take")

	// ErrValue indicates a Go value of [Node.Set] or [Node.Insert] that the
	// engine cannot write, whatever the document holds. A Set fails so with
	// a value of a type that go-yaml's encoder does not encode, such as a
	// channel or a function, and with a value that holds one. It fails so
	// with a value that holds itself or that holds two keys that a decode
	// reads as one. So does YAML of a marshaler that holds more than one
	// document or that repeats a key, and a chain of more than 1,000
	// marshalers. The error binds at the place of the call. The program
	// wrote the value, so [go.jacobcolvin.com/niceyaml.IsInvalid] reports
	// false for the error. A value that go-yaml's encoder cannot write so
	// that go-yaml reads it back fails with [ErrEngine] instead, as
	// [Node.Set] describes.
	//
	// A Set also fails so with a marshaler that returns an error, and
	// [errors.Is] and [errors.As] find the error of the marshaler through
	// the error of the Set. But [errors.Is] does not find a sentinel of the
	// package, such as ErrEngine, through it. The error of the Set binds at
	// the place of the call, and IsInvalid reports false for it, whatever
	// the error of the marshaler declares. So a marshaler that returns a
	// problem of another document does not blame the user's document. The
	// error of the Set keeps its own binding and fault, so [errors.As] does
	// not find a [*niceyaml.Error], a [*niceyaml.SourceError], or a
	// [*niceyaml.PanicError] of the marshaler. A marshaler that returns the
	// error of a context binds nowhere, because a binding of niceyaml gives
	// the error of a context no place in the text.
	//
	// A marshaler that returns a problem of the user's document, such as the
	// error of a [Node] of the same Doc whose place holds no value, fails the
	// Set without ErrValue. The values of the document decide that problem,
	// so the error of the Set keeps the fault and the sentinels of the
	// problem. The error of the Set still binds at the place of the call.
	ErrValue = errors.New("the value cannot be written")

	// ErrProtected indicates an edit of a protected value of [WithProtected],
	// of a value inside one, or of a value that holds one. It also indicates a
	// call that changes or creates a protected value through another place,
	// such as an alias. The error binds at the place of the call. The program
	// declares the protected values and writes the edits, so
	// [go.jacobcolvin.com/niceyaml.IsInvalid] reports false for the error.
	ErrProtected = errors.New("the edit changes a protected value")

	// ErrNested indicates an Apply that a [Func] of the same Doc calls. The
	// nested Apply fails with the error and calls its Func zero times. The
	// outer Apply fails with the error only when its Func returns it, and
	// the error then binds at the node of that call. A Func selects the
	// places of another path with [Node.Nodes] instead.
	ErrNested = errors.New("Apply runs inside another Apply of the Doc")

	// ErrEnded indicates an edit or a decode through a [Node] after the
	// Apply that gave the node returned. [Node.Exists] and [Node.Nodes]
	// return the error then, and so does each method of the node that edits
	// or decodes. The error binds nowhere, and
	// [go.jacobcolvin.com/niceyaml.IsInvalid] reports false for it.
	ErrEnded = errors.New("the Apply that gave the node has returned")

	// ErrDisplaced indicates a call through a [Node] that lost its place to
	// an earlier edit of the Apply, as [Node] describes. The edit removed
	// the place, or it made the path of the node read another value.
	// [Node.Exists] returns the error then, and so does a [Node.Nodes] of an
	// `@` path. So does each method of the node that edits or decodes.
	// [Doc.Apply] does not call its Func for a place of its path that an
	// earlier call took this way. A [Func] that loops over the nodes it kept
	// can thus skip each node whose Exists returns the error. The error
	// binds as [Node] describes, and [go.jacobcolvin.com/niceyaml.IsInvalid]
	// reports true for it.
	ErrDisplaced = errors.New("the node lost its place to an earlier edit")

	// The problem of a call of New with a nil Node or a Node below the root.
	errDocument = usage("New takes the root of a document")
	// The problem of a document that holds a byte order mark that the
	// Source does not read as YAML does.
	errMarkPlace = errors.New("a byte order mark")
	// The problem of a call of New with a protected path that is not a `$`
	// path of names and indexes.
	errProtectedPath = usage("WithProtected takes a $ path of names and indexes")
	// The problem of an Apply on a path that does not start at `$`, or that
	// holds a `~`.
	errPath = usage("Apply takes a $ path without ~")
	// The problems of a call of FileText with a Doc of another Source than
	// the one it takes, and with two Docs of one document.
	errFileTextSources = usage("FileText takes Docs of its Source")
	errFileTextTwice   = usage("FileText takes each document once")
	// The problem of a parse of the text that finds no document in it.
	errNoDocument = errors.New("the text holds no document")
)

// sentinelError is a problem that matches sentinel, an error of the
// package such as [ErrEngine], or [paths.ErrNotFound]. It has the message
// of err, the error behind it. A report that lists the problems of the
// error thus reads the problem once, rather than once for the sentinel and
// once for err.
//
// The error of a call can also have a cause, such as the error of a
// marshaler. [errors.Is] and [errors.As] find the cause, but Unwrap does
// not return it. The Is method does not match the mark of a fault through
// the cause, and the As method does not find a binding, a located error,
// or a recovered panic in it. A binding thus reads the place and the fault
// of the error, not those of the cause. The sentinel is nil for a problem
// that only keeps a cause.
type sentinelError struct {
	sentinel error
	err      error
	cause    error
}

// usage returns the problem of a call that msg describes, which matches
// [ErrUsage].
func usage(msg string) error {
	return &sentinelError{sentinel: ErrUsage, err: errors.New(msg)}
}

// Error returns the message of the error behind e.
func (e *sentinelError) Error() string {
	return e.err.Error()
}

// Is reports whether target is the sentinel of e or matches the cause of
// e. It reports false for [fault.ErrInvalid], so a fault that the cause
// declares does not reach e. When the sentinel of e is [ErrValue], it also
// reports false for the other sentinels of the package. The cause of such
// a problem comes from the program, so a sentinel in it says nothing of
// the call.
func (e *sentinelError) Is(target error) bool {
	if e.sentinel != nil && target == e.sentinel {
		return true
	}

	if e.cause == nil || target == fault.ErrInvalid {
		return false
	}

	if e.sentinel == ErrValue {
		switch target {
		case ErrEngine, ErrMisread, ErrUnsupported, ErrUsage, ErrProtected, ErrNested, ErrEnded, ErrDisplaced:
			return false
		}
	}

	return errors.Is(e.cause, target)
}

// As reports whether the cause of e holds an error that target points to,
// and sets target to the first such error, as [errors.As] does. It reports
// false for a target of a [*niceyaml.SourceError], a [*niceyaml.Error], or
// a [*niceyaml.PanicError], so the binding, the location, and a panic of
// the cause do not reach e.
func (e *sentinelError) As(target any) bool {
	if e.cause == nil {
		return false
	}

	switch target.(type) {
	case **niceyaml.SourceError, **niceyaml.Error, **niceyaml.PanicError:
		return false
	}

	return errors.As(e.cause, target)
}

// Unwrap returns the error behind e.
func (e *sentinelError) Unwrap() error {
	return e.err
}

// DetachErrors returns a copy of e with the error behind e and the cause
// of e detached by detach. A cause that binds in the user's document, such
// as the error of a decode that a marshaler returns, thus does not keep
// the Source of that document. [niceyaml.Detach] calls it.
func (e *sentinelError) DetachErrors(detach func(error) error) error {
	c := *e
	c.err = detach(e.err)
	c.cause = detach(e.cause)

	return &c
}

// Func edits a document through n, a node on one place that the path of
// [Doc.Apply] selects. An error fails the Apply, and an error without a
// location binds at the value of n.
//
// Each edit through a [Node] parses and checks the whole text, as
// [Doc.Apply] describes. Apply checks ctx between calls, not inside one. A
// Func that makes many edits in one call should check ctx between them and
// return its error.
type Func func(ctx context.Context, n *Node) error

// Option configures a [Doc].
//
// Options:
//   - [WithApp]
//   - [WithMarshalers]
//   - [WithProtected]
//   - [WithUnmarshalers]
type Option func(*config)

// config holds the settings that each [Option] gives a [Doc].
type config struct {
	// The name of the app in the messages of the engine.
	app string
	// The paths of the values that [WithProtected] protects.
	protected []paths.Path
	// The unmarshalers that each decode applies ahead of those of the
	// Source of the document.
	unmarshalers []niceyaml.Unmarshaler
	// The marshalers that each Set and Insert encodes its value with.
	marshalers []encoder.Marshaler
}

// WithApp is an [Option] that names the app in the messages of the
// engine. A refusal of YAML that the engine does not edit names it, as in
// "cafe does not edit a document that defines the anchor &x twice". So
// does the message of a bug in the engine, which asks the user to report
// the bug to the authors of the app. Without WithApp, a message names
// "this program".
func WithApp(name string) Option {
	return func(c *config) {
		c.app = name
	}
}

// WithProtected is an [Option] that protects the value at each of ps, a
// `$` path of names and indexes, and every value inside it. Each Apply
// protects the node of the text that each path reaches when it starts,
// through an alias or a `<<` merge key too, whichever path names the node.
// A path of [Doc.Apply] that selects more than one value, such as `.*` or
// `..name`, passes over each protected value and each value inside one. A
// path of names and indexes selects such a value as it selects any other.
// A [Func] cannot set, rename, move, or remove a protected value or a
// value inside one, and it cannot set, move, or remove a value that holds
// one. A path that holds no value when the Apply starts stays protected as
// the Apply found it, so a call that creates a value there fails. A path
// that a reference document decides protects the alias or the mapping
// whose lookup reads that document, as a value that holds the protected
// value. Each WithProtected adds to the paths before it. An Apply with
// [WithoutProtection] runs without the protection. The Protected Values
// section of the package documentation gives the rules.
func WithProtected(ps ...paths.Path) Option {
	return func(c *config) {
		c.protected = append(c.protected, ps...)
	}
}

// WithUnmarshalers is an [Option] that gives each decode of the document
// the unmarshalers us, ahead of those of its [niceyaml.Source], as though
// [niceyaml.WithUnmarshalers] gave them first. [Node.Decode] applies
// them, and so does each decode of the Node that [Doc.Node] returns. Each
// WithUnmarshalers adds to the ones before it.
func WithUnmarshalers(us ...niceyaml.Unmarshaler) Option {
	return func(c *config) {
		c.unmarshalers = append(c.unmarshalers, us...)
	}
}

// WithMarshalers is an [Option] that sets how the Go types of the program
// write YAML. [Node.Set] and [Node.Insert] encode each value under the
// marshalers ms, as [encoder.Marshal] does under
// [encoder.WithMarshalers]. [Node.SetKey] runs no marshaler on its key.
// A [Func] that reads a value with
// [Node.Decode] through the unmarshalers of the program thus writes it
// back the way the document holds it:
//
//	cidr := encoder.CustomMarshaler(func(ctx context.Context, n net.IPNet) (any, error) {
//		return n.String(), nil
//	})
//
//	d, err := edit.New(doc, edit.WithMarshalers(cidr, encoder.JSONMarshalers(true)))
//
// Each WithMarshalers adds to the ones before it, so a later function for
// a type replaces an earlier one.
func WithMarshalers(ms ...encoder.Marshaler) Option {
	return func(c *config) {
		c.marshalers = append(c.marshalers, ms...)
	}
}

// ApplyOption configures one call of [Doc.Apply].
//
// Options:
//   - [WithLabel]
//   - [WithoutProtection]
type ApplyOption func(*applyConfig)

// applyConfig holds the settings that each [ApplyOption] gives an Apply.
type applyConfig struct {
	label string
	// Whether the Apply runs without the guards of [WithProtected].
	unprotected bool
}

// WithLabel is an [ApplyOption] that names the edits of the Apply in its
// messages, as "v2" does in `v2 renames "cost" to "price", which already
// exists`. Without it, a message names "the edit". Each [Removal] of the
// Apply holds the label too.
func WithLabel(label string) ApplyOption {
	return func(c *applyConfig) {
		c.label = label
	}
}

// WithoutProtection is an [ApplyOption] that runs the Apply as if the Doc
// had no paths of [WithProtected]. The Func can then edit a protected value
// as it edits any other value, and a path that selects more than one value
// selects the protected values too. Every other check of the Apply still
// runs. The option covers one Apply, so the next Apply without it guards
// the values again. A Func gets only a [Node], and an Apply that a Func
// calls fails with [ErrNested], so only the owner of the Doc lifts the
// protection, such as a framework that runs the Funcs of others and then
// writes the type fields that it protects from them.
func WithoutProtection() ApplyOption {
	return func(c *applyConfig) {
		c.unprotected = true
	}
}

// Doc is the text of one YAML document as the edits leave it. A Doc is not
// safe for concurrent use.
//
// Create instances with [New].
type Doc struct {
	// The first problem of the call that runs that matches [ErrEngine],
	// which fails the Apply whatever the Func returns.
	callErr error
	// The problem of the first call of the Apply running after which the
	// text differed from the model of the calls, which each later call of
	// the Apply returns, or nil.
	stuck error
	// The document as the user wrote it.
	user *niceyaml.Node
	// The parse of the current text, and its document.
	src  *niceyaml.Source
	node *niceyaml.Node
	// The index of the current parse, which [Doc.parseIndex] makes again
	// after each parse, and one other index that it keeps. The end of an
	// Apply keeps only the reader of the keys of the current parse. The
	// readers of the parses of an Apply share the decodes of the keys
	// without an alias in plain.
	index, other *parseIndex
	plain        *mapkey.Plain
	// What go-yaml's decoder reads from each scalar key that the
	// canonical-key test read, by its token, as [Doc.readKey] reads it.
	keyReads map[keyRead]keyReading
	// The last edit of the Apply running.
	lastEdit *editRecord
	// The string of each block scalar of the user's document, by the offset
	// in orig of its indicator, which [Doc.readUserScalars] reads.
	scalars map[int]string
	// The text of the lines of the user's document.
	orig string
	// The offset in orig of each byte that a byte order mark stood in front
	// of in the user's text. The tokenizer drops such a mark, so orig does
	// not hold it. [New] refuses such a mark inside a document, and a mark
	// right after one that the tokenizer drops. [Doc.Text] writes each other
	// mark again.
	marks []int
	// The line break that a line an edit adds ends with when the current
	// text does not have one: the first line break of the user's document,
	// or else of its file, or else "\n".
	brk string
	// The current text.
	cur string
	// The text after the document through the "---" that opens the next
	// document, or "" when no such header follows. It holds the comment
	// lines above the header, which belong to the next document, as
	// [go.jacobcolvin.com/niceyaml.Node.Span] gives them to it. Each parse
	// reads the current text with it, as the parser reads the document in
	// its file, and no edit writes it.
	tail string
	// The label of the Apply running, or "" for one without a label.
	label string
	// What the call running does, as a message names it, such as
	// "renames $.a to "b"", or "" when no call runs.
	callNamed string
	// What the call running does, as the detail that names it names it,
	// such as "reads $.a" for a read, or "" when no call runs. The calls of
	// [Node] and the write of a type field run, as [Doc.callDetails] names
	// them.
	running string
	cfg     config
	notes   []note
	// The line starts of orig and of cur.
	origLines []int
	curLines  []int
	// The offsets of the runes of the lines of cur that [Doc.offset] read.
	runes runeOffsets
	// The nodes of the Apply running, whose paths each edit keeps up to
	// date, and the guards of the paths of [WithProtected], as
	// [Doc.guardValues] gives them.
	nodes  []*Node
	guards []*guard
	// Each node that the edit of the call running changes, as the call found
	// it, which a call that fails restores.
	found    map[*Node]Node
	removals []Removal
	// The model of the calls of the Apply running, or nil before its first
	// call.
	model *model
	// The zones of the Apply running, in the order of their sites, as
	// [Doc.beginZones] finds them, and the key of each, as [Doc.heldZone]
	// reads it.
	zones    []*zone
	zoneKeys map[zoneKey]bool
	// The storage of the groups of keys that [Doc.constructZones] reads.
	groups *keyGroups
	// The reach of the Set running, which [Doc.renderPayload] checks, or
	// nil.
	pending *reachCall
	// The edits of values that [Node.Update] writes without a parse of
	// each, while deferring is true, as [Doc.writeValue] adds them. The
	// next [Doc.apply] makes them with its own edits.
	deferred []edit
	// The capture of the site of an error that the edit package binds again,
	// as [Doc.sitePlace] fills it, or nil.
	capture *capture
	// The merges of the text that the call running found, which
	// [Doc.mergeKept] checks after the call.
	mergesAt mergeState
	// The place in orig that each byte of cur came from, or for a byte an
	// edit wrote, -1 minus the index of its note.
	origin []int
	// The first offset in origin of each origin that [Doc.partSpan] looked
	// up, as [Doc.firstOffset] reads it.
	firsts originFirsts
	// The text that the call running found, which holds the place of the
	// call, while calling reports that a call runs.
	callStart state
	// The site where the value that the Set running replaces binds, while
	// replacing reports that the Set replaces a value, as [Doc.startCall]
	// finds it.
	replaced site
	// The line of the user's text where orig starts.
	base int
	// Whether a block sequence that an edit creates indents its "-", as
	// [Doc.sequenceIndent] tells: 1 when it does, -1 when it does not, or 0
	// before sequenceIndent reads it.
	seqIndent int
	// The bytes [start, end) of the text of the user's Source that orig
	// holds, with its byte order mark.
	start, end int
	// Whether an Apply is running, and whether a call of an edit runs.
	applying, calling bool
	// Whether the Set running replaces a value, as replaced holds its site.
	replacing bool
	// Whether the check after the last call runs, whose problems name no
	// call, as [Doc.callDetails] tells.
	checking bool
	// Whether [Doc.writeValue] adds its edits to deferred instead of
	// making them.
	deferring bool
	// Whether the error that binds in the current text is a problem that the
	// edit package found, as [Doc.bindOwn] binds it, and not an error that a
	// caller binds by a path.
	own bool
}

// state is the text of a [Doc] as an Apply found it, which a failed Apply
// restores.
type state struct {
	src      *niceyaml.Source
	node     *niceyaml.Node
	cur      string
	origin   []int
	curLines []int
	notes    int
	removals int
}

// save returns the text of d as it stands.
func (d *Doc) save() state {
	return state{
		src:      d.src,
		node:     d.node,
		cur:      d.cur,
		origin:   d.origin,
		curLines: d.curLines,
		notes:    len(d.notes),
		removals: len(d.removals),
	}
}

// restore replaces the text of d with the text that s saved, and drops
// the notes and the removals after s.
func (d *Doc) restore(s state) {
	d.show(s)

	d.notes = d.notes[:s.notes]
	d.removals = d.removals[:s.removals]
}

// show replaces the text of d with the text that s saved, and keeps the
// notes and the removals. The notes of that text stay valid, because the
// edits only add notes.
func (d *Doc) show(s state) {
	d.src, d.node, d.cur, d.origin, d.curLines = s.src, s.node, s.cur, s.origin, s.curLines
}

// keepNode keeps n as the call running found it, unless the call kept it
// already, so a call that fails restores it.
func (d *Doc) keepNode(n *Node) {
	if !d.calling {
		return
	}

	if d.found == nil {
		d.found = make(map[*Node]Node)
	}

	if _, ok := d.found[n]; !ok {
		d.found[n] = *n
	}
}

// undoCall leaves the Doc as the call running found it, after the call
// failed. The text, the removals, and the nodes that the call changed go
// back to what the call found. The notes of the call stay, because each is
// the origin of text that the call wrote, and nothing refers to them once
// that text goes. When the model took the change of the call, the model
// starts again from the text, as at the first call of an Apply. That text
// holds the values of the model before the call. The calls after it thus
// read the text, the nodes, and the model as they stood before the call.
func (d *Doc) undoCall(changed bool) {
	d.show(d.callStart)

	d.removals = d.removals[:d.callStart.removals]

	for n, was := range d.found {
		*n = was
	}

	d.found = nil

	if changed {
		d.model = nil
	}
}

// Removal is one value of the user's text that an edit removed. A rename
// or a move can take the value to another place first, and a Set can
// replace it. Edits inside the value can take some of its text or move
// other text into it. Key, Path, Range, and Item still describe the value
// as the user's text holds it.
type Removal struct {
	// Reason is the reason that [WithReason] gave, or "".
	Reason string
	// Label is the label that [WithLabel] gave the Apply that removed the
	// value, or "".
	Label string
	// Key is the key that the user wrote for the value, or "" when the
	// user's text holds the value as an item.
	Key string
	// Path is the path of the value as the user's text spells it. It ends
	// in `~` when the user wrote the value under a key. [paths.Path.Value]
	// gives the path of the removed value.
	Path paths.Path
	// Range is the range in the user's text of the key that the user wrote
	// for the value. For an item of a block sequence, it runs from the "-"
	// through the value. For an item of a flow sequence, it is the range of
	// the value.
	Range position.Range
	// Item is true when the user's text holds the value as an item of a
	// sequence.
	Item bool
}

// New creates a new [Doc] that holds the text of doc, the root of a
// document of a [niceyaml.Source], such as one that
// [niceyaml.Source.Documents] returns. It returns the error of a document
// that does not parse, or of a reference document of
// [niceyaml.WithReferences] that does not parse, because a decode of the
// document then fails. It also returns an error for a nil doc and for a
// Node below the root. Such an error matches [ErrUsage]. So does the error
// of a path of [WithProtected] that is not a `$` path of names and indexes,
// which fails New too. The error
// of a Node below the root also matches [niceyaml.ErrScopedNode]. A path
// that selects the root, as [paths.Doc] does in [niceyaml.Node.At], gives
// the root itself, so New takes that Node. So does a
// [niceyaml.Node.NodeAt] that finds the root. New reads the document as
// go-yaml reads it. It refuses some documents that go-yaml's decoder
// reads otherwise than YAML does, with a problem that matches
// [ErrMisread]. The Supported YAML section of the package
// documentation lists them. It also refuses a document that holds YAML
// outside the subset that the engine edits, as that section lists it, with
// a problem that matches [ErrUnsupported]. The error of New holds a
// problem for each instance that it refuses. The problems stop at the
// first anchor on an empty value under which go-yaml's parser nests the
// lines after it, as that section describes.
//
// New takes an empty document, which [niceyaml.Node.IsEmpty] reports, such
// as an empty text or a text of comments alone. Its root holds no value, so
// a [Func] writes the first value with [Node.Set], as the Empty Documents
// section of the package documentation describes. Go-yaml's parser reads no
// value from a tag at the end of the text, as in "!t" or "!t\t" without a
// line break. Go-yaml's lexer reads a tab after a tag as a part of the tag.
// So such a document is empty too. The parser reads an
// empty value under a tag that a line break or a space follows. So New
// takes "!t\n" with that value as its root.
//
// New takes a document of [niceyaml.NewSourceFromLayers], whose
// [niceyaml.Source.Layers] are not empty. Its Doc edits the merged text,
// which holds the values of every layer, as a program that shows an
// effective configuration wants. That text is not the text of the file
// that [niceyaml.Source.FilePath] names. A program that writes a file
// edits the document of the layer that holds the value instead. That
// document is the [niceyaml.Node.Document] of the Node that
// [niceyaml.Node.Origin] returns.
func New(doc *niceyaml.Node, opts ...Option) (*Doc, error) {
	switch {
	case doc == nil:
		return nil, fmt.Errorf("%w, not nil", errDocument)
	case doc != doc.Document():
		return nil, &sentinelError{
			sentinel: niceyaml.ErrScopedNode,
			err:      fmt.Errorf("%w, not the node at %s", errDocument, doc.Path()),
		}
	}

	var cfg config

	for _, opt := range opts {
		opt(&cfg)
	}

	err := doc.Err()
	if err != nil {
		return nil, err //nolint:wrapcheck // The error of the document binds in it.
	}

	// Every decode of the document fails on a reference document that does
	// not parse, so New fails with the error of that parse.
	for _, ref := range hook.ReferenceDocuments(doc) {
		if r, ok := ref.(*niceyaml.Node); ok && r.Err() != nil {
			return nil, r.Err() //nolint:wrapcheck // The error of the document binds in it.
		}
	}

	for _, p := range cfg.protected {
		if !p.IsAbsolute() || !plain(p) {
			return nil, fmt.Errorf("%w, not %s", errProtectedPath, p)
		}
	}

	d, err := readDoc(doc, cfg)
	if err != nil {
		return nil, err
	}

	err = d.newRefusal()
	if err != nil {
		return nil, err
	}

	d.readUserScalars()

	return d, nil
}

// readDoc returns the [Doc] of doc, the root of a document that parses, as
// [New] reads it with cfg before it checks the text.
func readDoc(doc *niceyaml.Node, cfg config) (*Doc, error) {
	src := doc.Source()
	text := src.Text()
	span := doc.Span()

	// A byte order mark that the tokenizer drops lies before column 0 of
	// its line, so the document starts past the mark of its first line, and
	// that mark stays out of its text.
	start, ok := src.Offset(position.New(span.Start, 0))
	if !ok {
		start = 0
	}

	end := len(text)
	if span.End < src.Lines().Len() {
		if off, ok := src.Offset(position.New(span.End, 0)); ok {
			end = lineStart(text, off)
		}
	}

	d := &Doc{user: doc, base: span.Start, cfg: cfg, start: start}

	d.end = end
	orig := text[start:end]

	// The comment lines between the document and a "---" after it belong to
	// the next document, as its span holds them. The parse reads them with
	// the header, and no edit writes them.
	var tail string

	if line, ok := headerAfter(src, span.End); ok {
		tail = text[end:headerEnd(src, line)]
	}

	// The parse counts positions without the marks that the tokenizer
	// drops, such as one after the "---" of a later document, so the text
	// and its tail leave them out too. The tokenizer reads the tail after
	// the text, so the marks of the tail drop as they drop there.
	dropped, _ := bom.Drop(orig + tail)
	orig, d.marks = bom.Drop(orig)
	d.tail = dropped[len(orig):]

	d.orig, d.cur = orig, orig
	d.origLines = lineStarts(orig)
	d.brk = cmp.Or(firstBreak(orig), firstBreak(text), "\n")

	d.origin = make([]int, len(orig))
	for i := range d.origin {
		d.origin[i] = i
	}

	err := d.parse()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEngine, err)
	}

	return d, nil
}

// tailContext returns the lines of the tail before the line of its "---",
// the comment lines above the header of the next document and the blank
// lines between them, with their line breaks.
func (d *Doc) tailContext() string {
	starts := lineStarts(d.tail)

	return d.tail[:starts[len(starts)-1]]
}

// headerMarker is the marker of a document header, which starts a
// document.
const headerMarker = "---"

// lineStart returns off, the offset in text of column 0 of a line as
// [niceyaml.Source.Offset] gives it, or the offset of the byte order mark
// before it. The tokenizer drops such a mark, which lies before column 0 of
// its line.
func lineStart(text string, off int) int {
	if strings.HasSuffix(text[:off], bom.Mark) {
		return off - len(bom.Mark)
	}

	return off
}

// headerAfter returns the line of the "---" that opens the next document
// after line, the line where a document of src ends, past the comments
// between them. It reports false when another token, or no token, follows
// those comments. The parser reads a document before such a header with the
// header after it, which gives an anchor without a value at the end of the
// document a null.
func headerAfter(src *niceyaml.Source, line int) (int, bool) {
	lines := src.Lines()

	// The lines from line on hold the tokens after the document in the
	// order of the text, and a token that starts before line is part of
	// the document.
	for l := line; l < lines.Len(); l++ {
		for _, tk := range lines.Line(l).SourceTokens() {
			switch {
			case tk.Position.Line <= line, tk.Type == token.CommentType:
			case tk.Type == token.DocumentHeaderType:
				return tk.Position.Line - 1, true
			default:
				return 0, false
			}
		}
	}

	return 0, false
}

// headerEnd returns the offset in the text of src past the "---" that
// starts the next document on line, as [headerAfter] gives it.
func headerEnd(src *niceyaml.Source, line int) int {
	text := src.Text()

	off, ok := src.Offset(position.New(line, 0))
	if !ok {
		return len(text)
	}

	at := strings.Index(text[off:], headerMarker)
	if at < 0 {
		return len(text)
	}

	return off + at + len(headerMarker)
}

// Apply runs fn once on each place that path selects, in document order,
// and then checks the result. The path starts at `$` and does not hold a
// `~`. When path does not select any place, Apply calls fn zero times.
// Each entry and each item of the text is a place of its own, even when
// its value is an alias, so each takes a call. A path that reaches one
// entry or one item through an alias or a `<<` merge key reaches the same
// text, so all such paths take one call.
//
// An Apply at a path that the document lacks thus calls fn zero times, and
// a Set inside fn creates nothing. To set a value whether or not the
// document holds it, apply at `$` and call [Node.Set] on the node that
// [Node.At] returns. That Set creates the value and the mappings above it:
//
//	err := d.Apply(ctx, paths.Doc(), func(ctx context.Context, root *edit.Node) error {
//		return root.At(paths.MustParse("$.image.tag")).Set(ctx, "v2")
//	})
//
// A Func edits the places of other paths through [Node.Nodes], which
// selects the places that Apply selects. All the edits of one Apply then
// pass or fail together.
//
// The one call for each place covers the places that path selects. It does
// not cover the nodes that [Node.At] returns. Those nodes can reach the
// same text in two calls. The nodes at `@.retries` do so when path selects
// each mapping that merges one anchored mapping. An fn that sets that
// value from the value that it reads then edits it one time for each such
// mapping. [Node.Own] returns the same path for those nodes, so fn can
// keep the paths that it edited and skip the later calls.
//
// A Source that [niceyaml.WithAllowDuplicateKeys] gives can hold two
// entries of one key in a mapping. A path of the key selects the last of
// them, as a decode reads it. A selector that selects more than one value,
// such as `.*`, `..*`, or `..name`, selects each entry of the key that it
// selects, so each takes a call. The selectors after it read below each
// such entry. The node of an entry that a later entry hides has the path
// of its key too, and an `@` path of [Node.At] reads below that entry. So
// the node reads the values of its own entry. A call that edits either
// entry fails with [ErrUnsupported], as [ConstructRepeatedKey] names it.
//
// [Node.Own] returns the path of the key for the node of a hidden entry.
// It can return that path for the node of the last entry too, so fn cannot
// tell those entries apart by Own.
//
// Apply calls fn only on places that hold a value. A recursive selector
// thus selects nothing below a tag that converts a collection, such as
// !!str, because the decode reads that collection as a scalar. The root of
// an empty document is the one place without a value that a path selects.
// The path `$` selects it, so fn writes the first value there, and every
// other path selects nothing. A `$` path of [WithProtected] protects the
// root, so `$` then selects nothing too, unless the Apply runs with
// [WithoutProtection].
//
// In an Apply without [WithoutProtection], a path that selects more than
// one value, such as `.*`, `[*]`, `..name`, or `..*`, passes over each
// place that is a protected value of [WithProtected] or lies inside one. A
// path of names and indexes alone selects such a place when it holds a
// value, so fn runs there and can read the value. Each edit that fn makes
// there fails with an error that matches [ErrProtected], as an edit
// through [Node.At] fails. So when WithProtected protects `$.apiVersion`,
// an Apply at `$.apiVersion` whose fn sets the value fails. The same Apply
// at `$.*` calls fn on each other value of the root and passes.
//
// Each call sees the edits of the calls before it. A read after the write of
// an earlier call thus reads the written value, through an alias too. Apply
// skips each place that an earlier call removed, either directly or by
// replacing a value above it. When a method of [Node] cannot make its edit,
// it returns its problem to fn and leaves the Doc as it found it. The
// Apply fails with that problem only when fn returns it or when the
// problem matches [ErrEngine]. So fn can skip the edit or try another. The
// calls act in order, so a later call can edit a value that an earlier
// call edited, through any path.
//
// After each call, the text must hold the values of a model of the calls,
// and each alias must read the value that the model holds at its place.
// Otherwise the call fails with [ErrEngine] at the first difference, and
// each later call of the Apply returns that error. A call also fails with
// ErrEngine when it leaves a text that [New] refuses with [ErrMisread] for
// its `<<` merge keys, or when it moves a byte order mark to a place where
// the tokenizer of the Source drops it, as the package documentation
// describes.
//
// A call fails with [ErrUnsupported] when it reaches YAML that the engine
// does not edit, as the Supported YAML section of the package
// documentation lists it. A rename that makes a mapping hide an entry that
// its `<<` merge key brings in is one such call. A read never fails so.
//
// After the last call, Apply checks the text. The text must decode, and it
// must hold the values of the model, as the package documentation
// describes. A difference from the model shows a bug in the engine or a
// misread of go-yaml. Its error matches [ErrEngine] and binds where the
// user's text holds the value that differs.
//
// Each call parses and checks the whole text that it leaves. So the cost
// of a path that selects several places grows with the number of places
// times the size of the text. A rename of each key of a text of 400 keys,
// 3.7 KiB, took 0.5 s and allocated more than 900 MiB. A program that edits
// untrusted input should keep the byte limits and the alias limit of its
// Source on, which [niceyaml.WithByteLimit],
// [niceyaml.WithDocumentByteLimit], and [niceyaml.WithAliasLimit] set.
// Those limits bound the text that the Apply starts from, so they bound
// the cost of each call that does not grow the text. The calls read
// the text without the limits, and Apply checks the limits again only
// after the last call. A deadline on ctx bounds the number of calls.
//
// Apply checks ctx before each call of fn. When ctx has ended, Apply
// returns its error, bound to the user's Source, and the Doc drops the
// edits of the calls so far. So fn runs zero times on a context that ended
// before the Apply. To let an Apply finish after its context ends, pass
// it the context that [context.WithoutCancel] returns.
//
// Apply returns the first problem, bound in the user's text, and stops
// there. A failed Apply leaves the Doc as it found it, so the Doc drops
// every edit of the calls before the problem. Apply returns an error that
// matches [ErrUsage], and calls fn zero times, for a path that does not
// start at `$` or that holds a `~`. It returns an error that matches
// [ErrNested] for a call from a Func of the same Doc, and the outer Apply
// fails with that error only when its Func returns it. A text whose aliases
// expand past the alias limit also fails before fn runs, with the error that
// a decode of the text gives. A path whose matches read through an alias
// that the text does not resolve, such as an alias of a reference document
// at a `<<` merge key, fails the Apply too. That problem reads as the
// problem of such a path of [Node.At] does, and it binds at the alias.
func (d *Doc) Apply(ctx context.Context, path paths.Path, fn Func, opts ...ApplyOption) error {
	if d.applying {
		return ErrNested
	}

	if !path.IsAbsolute() || holdsKey(path) {
		return fmt.Errorf("%w, not %s", errPath, path)
	}

	var cfg applyConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	d.applying = true
	saved := d.save()
	passed := false

	// A panic in fn leaves the Doc as it found it too.
	defer func() {
		if !passed {
			d.restore(saved)
		}

		d.endApply()
	}()

	err := d.run(ctx, path, fn, cfg)
	passed = err == nil

	return err
}

// run runs fn on each place that path selects, as the Apply that cfg
// describes, and checks the result.
func (d *Doc) run(ctx context.Context, path paths.Path, fn Func, cfg applyConfig) error {
	start := d.save()

	// A text whose aliases take a decode past the alias limit fails with the
	// error of that decode, before the calls read its aliases.
	if aliasing.PastLimit(d.node) {
		err := d.userDecode(ctx)
		if err != nil {
			return err
		}
	}

	d.begin(cfg.label)

	if !cfg.unprotected {
		d.guardValues()
	}

	nodes, err := d.matches(path)
	if err != nil {
		return err
	}

	for _, n := range nodes {
		if n.gone || d.displaced(n) {
			continue
		}

		// Each call parses and checks the whole text, so the context bounds
		// the number of calls.
		err := ctx.Err()
		if err != nil {
			return d.user.Bind(err) //nolint:wrapcheck // The error binds in the user's text.
		}

		err = d.endCall(n, fn(ctx, n))
		if err != nil {
			return err
		}
	}

	return d.check(ctx, start)
}

// holdsKey reports whether path holds a `~` selector.
func holdsKey(path paths.Path) bool {
	for sel := range path.Selectors() {
		if sel.Kind == paths.SelectorKey {
			return true
		}
	}

	return false
}

// endApply ends the Apply running. Its nodes stop taking edits, and the
// Doc drops what its calls kept, as [Doc.endCalls] drops it.
func (d *Doc) endApply() {
	for _, n := range d.nodes {
		n.ended = true
	}

	d.nodes, d.guards = nil, nil
	d.applying = false
	d.endCalls()
}

// Text returns the current text of the document. During an Apply, Text
// returns the text after the last call that passed. A byte order mark that
// the tokenizer drops after the first line of the document, as after a
// comment line in the prefix of the document, stays in front of the
// line where the user wrote it. When an edit took that line away, the mark
// stands in front of the next line of the user's text that the edits kept,
// or goes with the text after it. When an edit wrote content in front of
// the line of a mark in the prefix of the document, the mark moves in front
// of that content. The tokenizer still drops it there. A mark at the start of
// the document stands before the text of the document, so Text leaves it
// out, and [FileText] keeps it. Before the first edit, Text thus holds the
// bytes that [niceyaml.Node.Text] returns for the document, without a mark
// at its start.
func (d *Doc) Text() string {
	return d.withMarks(d.textMarks())
}

// markPlace is a byte order mark that [Doc.Text] writes. The mark stands
// before the byte at in the current text, and mark is its index in the
// marks of the Doc.
type markPlace struct {
	at, mark int
}

// textMarks returns the places of the marks that [Doc.Text] writes, in
// the order of the current text.
func (d *Doc) textMarks() []markPlace {
	if len(d.marks) == 0 {
		return nil
	}

	var (
		places []markPlace
		next   = 0
		last   = 0
	)

	// The tokenizer drops a mark at the start of each line up to the first
	// line of content of the text. Those lines make the prefix of the
	// document.
	prefix, content := bom.ContentStart(d.orig), bom.ContentStart(d.cur)

	for i := range len(d.cur) {
		if next == len(d.marks) {
			break
		}

		o := d.origin[i]
		if o < d.marks[next] {
			continue
		}

		// The mark goes at the start of the line of the first byte that the
		// edits kept from its place on, before the bytes that an edit wrote
		// there. A mark of the prefix stays in the prefix. When an edit wrote
		// content in front of its line, the mark goes before that content.
		at := strings.LastIndexByte(d.cur[:i], '\n') + 1
		if d.marks[next] <= prefix {
			at = min(at, content)
		}

		at = max(at, last)
		last = at

		for next < len(d.marks) && d.marks[next] <= o {
			next++
		}

		// Of the marks that the line takes, the one nearest the byte stands
		// for them all.
		places = append(places, markPlace{at: at, mark: next - 1})
	}

	// A mark at the end of the text stands after the last byte of the user.
	if next < len(d.marks) && d.marks[next] == len(d.orig) && d.cur != "" &&
		d.origin[len(d.cur)-1] == len(d.orig)-1 {
		places = append(places, markPlace{at: len(d.cur), mark: next})
	}

	return places
}

// withMarks returns the current text with a byte order mark at each of
// places.
func (d *Doc) withMarks(places []markPlace) string {
	if len(places) == 0 {
		return d.cur
	}

	var (
		b    strings.Builder
		last = 0
	)

	for _, p := range places {
		b.WriteString(d.cur[last:p.at])
		b.WriteString(bom.Mark)

		last = p.at
	}

	b.WriteString(d.cur[last:])

	return b.String()
}

// FileText returns the text of src, with the [Doc.Text] of each Doc in
// place of the text of its document. The other documents of src keep
// their text, and so does a byte order mark before a document. A program
// that edits several documents of one file passes the Doc of each, in any
// order. Without a Doc, FileText returns the text of src as it is. A
// program that edits only the documents that match a test can thus pass
// the Docs that it made, even when no document matched.
//
// For a src with [niceyaml.Source.Layers], FileText returns the merged
// text that niceyaml wrote from every layer. A program must not write
// that text to the file that [niceyaml.Source.FilePath] names. The file
// then holds the values of the other layers and loses its comments. A
// program that writes a file edits the document of each layer instead,
// as [New] describes.
//
// FileText returns an error that matches [ErrUsage] when a Doc comes from
// another Source than src, and when two Docs come from one document.
//
// With one Doc or more, FileText checks the text that it returns against
// the byte limit of src, which [niceyaml.Source.ByteLimit] reports.
// [Doc.Apply] checks its document against the other documents as the user
// wrote them. So the Docs of several documents can each keep the limit and
// together break it. A text past the limit fails with the message that src
// gives such a text, as in "app.yaml: file is larger than 256 KiB". The
// error matches [niceyaml.ErrLimit], and [niceyaml.IsInvalid] does not
// report it. A program that writes a larger file builds src with a larger
// limit. Without a Doc, the text is the text of src, and FileText does not
// check it.
//
// During an Apply, FileText reads the text of each Doc as Doc.Text does.
// That text stands between two calls. So FileText can report the byte
// limit for a text that a later call brings back within it.
func FileText(src *niceyaml.Source, docs ...*Doc) (string, error) {
	sorted := slices.Clone(docs)
	slices.SortStableFunc(sorted, func(a, b *Doc) int { return a.start - b.start })

	var (
		b    strings.Builder
		text = src.Text()
		last = 0
	)

	for i, d := range sorted {
		switch {
		case d.user.Source() != src:
			return "", fmt.Errorf("%w, not a Doc of another Source", errFileTextSources)
		case i > 0 && d.start < last:
			return "", fmt.Errorf("%w, not the document at line %d twice", errFileTextTwice, d.base+1)
		}

		b.WriteString(text[last:d.start])
		b.WriteString(d.Text())

		last = d.end
	}

	b.WriteString(text[last:])

	// Without a Doc, the text is the user's text, which the check does not
	// cover. A path binds in one document, not in the file. So the error
	// names src alone, as the error of a load of the file does.
	if limit := src.ByteLimit(); len(docs) > 0 && b.Len() > limit {
		return "", src.Bind(niceyaml.Unchecked(&limitError{msg: "file is larger than " + quantity.Bytes(limit)}))
	}

	return b.String(), nil
}

// Node returns the current text of the document as a document, whose
// errors bind in the user's text. An error keeps the path that it names,
// as the Errors section of the package documentation describes. During an
// Apply, Node reads the document as [Doc.Text] does.
//
// A program that parses a copy of the text passes the node as the home of
// [niceyaml.NewSourceFromDerived]. Each error of the copy then binds in
// the user's text. A nil locate fits a copy that keeps every line and
// column of the node:
//
//	n := d.Node()
//	work := niceyaml.NewSourceFromDerived(n.Source().Text(), n, nil)
func (d *Doc) Node() *niceyaml.Node {
	return d.node
}

// Edited reports whether the text of the document differs from the text
// that [New] took.
func (d *Doc) Edited() bool {
	return d.cur != d.orig
}

// Removals returns the values of the user's text that the edits removed,
// in the order of the calls. It omits each value that an earlier edit
// created.
func (d *Doc) Removals() []Removal {
	return slices.Clone(d.removals)
}
