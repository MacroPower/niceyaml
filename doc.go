// Package niceyaml provides utilities for working with YAML documents. It
// builds on [yaml] and [charm.land/lipgloss/v2].
//
// It styles the YAML tokens directly and in one pass, rather than through
// several distinct styling systems.
//
// It also provides an alternative implementation of go-yaml's source
// annotations for errors, and adapters for JSON schema validation.
//
// # Usage
//
// Parse YAML into a [Source], then use a
// [go.jacobcolvin.com/niceyaml/printer.Printer] to render it with syntax
// highlighting:
//
//	source := niceyaml.NewSourceFromString(yamlContent)
//	p := printer.New()
//	lipgloss.Println(p.Print(source.View()))
//
// [NewSourceFromFile] reads a file from disk, and [NewSourceFromFS] reads one
// from an [fs.FS], such as an [embed.FS]. [NewSourceFromReader] reads from
// any [io.Reader], such as standard input.
//
// A Source binds the errors it returns to itself, so they show users exactly
// where the problem is:
//
//	file, err := source.File()
//	if err != nil {
//		// FormatError prints the message and a plain-text excerpt of
//		// the YAML. The excerpt marks the problematic location and
//		// shows two lines of context. err.Error() is the message and
//		// position alone.
//		log.Print(niceyaml.FormatError(err))
//	}
//
// # Architecture
//
// The module separates a YAML document from its rendering, and each part
// lives in a package of its own.
//
// [Source], in this package, is the file. It owns the tokens from go-yaml and
// parses them lazily into an abstract syntax tree (AST), which [Source.File]
// returns. [Source.Documents] returns the root [Node] of each YAML document
// in the file. The Source binds the errors that it and its Nodes produce to
// itself. [Node.Bind] binds an error from any other code to the document of
// the Node, and [Source.Bind] binds one to the document that holds its
// location.
//
// The Source keeps the text it read. [Source.Text] returns that text byte for
// byte, [Source.Offset] converts a position in its lines to a byte offset in
// the text, and [Source.PositionAt] converts a byte offset back. A program
// that replaces a scalar on one line can splice the text at those offsets.
// The [go.jacobcolvin.com/niceyaml/edit] package
// renames keys and sets, moves, and removes values, and it keeps every byte
// that an edit does not change.
//
// [line.Lines] is the content. It holds the tokens line by line, and it never
// changes. [line.View] is one rendering of that content. It shares the lines
// and carries the decoration of the rendering. Annotations hold error
// messages and diff headers, flags mark inserted and deleted lines, and
// overlays apply style spans for highlighting. A Source returns its lines
// from [Source.Lines] and a fresh view over them from [Source.View].
//
// The decoration that a caller adds to one view does not reach the Source or
// any other view, and taking a view does not copy the content. [Node.View] is
// the same view, sliced to the lines of one document, with the line numbers
// they have in the file. Every index and range that a view takes is in the
// coordinates of its lines, and a slice keeps those coordinates. The ranges
// that a [go.jacobcolvin.com/niceyaml/finder.Finder] or [Node.Ranges] returns
// therefore apply alike to a view of the whole source and to a slice of it.
//
// A view does not have to hold a YAML document. Diffs, for example,
// interleave lines from two revisions and are plain [line.View] values.
//
// [go.jacobcolvin.com/niceyaml/printer.Printer] renders a [line.View] with
// syntax highlighting through lipgloss. It supports word wrapping, annotation
// rendering, and customizable gutters, such as line numbers and diff markers.
// [go.jacobcolvin.com/niceyaml/diff.Differ] compares two
// [line.Sequence] values, each a [line.Lines] or a [line.View], and
// [go.jacobcolvin.com/niceyaml/finder.Finder] searches one.
//
// Themes from [go.jacobcolvin.com/niceyaml/style/theme] provide color
// palettes. Without one, [go.jacobcolvin.com/niceyaml/printer.Printer]
// renders with the dark palette of
// [go.jacobcolvin.com/niceyaml/style.Default], which also returns a
// palette for a light terminal.
//
// # Options
//
// Every constructor of the module takes functional options. Each option
// that sets a value in place of a default keeps that default on a nil
// argument. An interface that holds a nil pointer or a nil func counts as
// nil too. A program that builds a value only on some condition thus
// passes it as it is:
//
//	var gutter printer.Gutter
//	if numbered {
//		gutter = printer.LineNumberGutter()
//	}
//
//	p := printer.New(printer.WithGutter(gutter)) // the default gutter otherwise
//
// Turning a feature off takes a named function or option, such as
// [go.jacobcolvin.com/niceyaml/printer.NoGutter] or
// [go.jacobcolvin.com/niceyaml/finder.Exact], so the off state is
// visible at the call site. The exception is an option whose value is a
// security boundary, such as
// [go.jacobcolvin.com/niceyaml/schema.WithAccess]. A nil there is a
// bug, such as the result of an ignored error, and the option panics
// rather than fail open.
//
// A check is a value that helps decide whether a document passes, such as
// a [Validator], a matcher, a schema resolver, or the function of
// [WithValueCheck]. A skipped check lets a document pass without the
// check that the program meant to run, so the module rejects a nil check
// everywhere. The function that gets a nil check panics and names itself
// in the message, as [WithValidator], [Node.Validate], and
// [go.jacobcolvin.com/niceyaml/schema.When] do. The options of
// [go.jacobcolvin.com/niceyaml/manifest.NewRegistry] report a nil check
// through the error of NewRegistry instead, beside the other mistakes of
// its options.
//
// A program that checks only on some condition passes a check that
// passes every document, such as the result of [NoValidator] or
// [go.jacobcolvin.com/niceyaml/schema.AcceptAll]. A function that only
// drops warnings, such as the function of
// [go.jacobcolvin.com/niceyaml/manifest.WithWarningFilter], is not a
// check, and the option adds nothing for a nil function.
//
// Every integer limit option, such as [WithByteLimit] or
// [go.jacobcolvin.com/niceyaml/schema.WithLoadLimit], follows one rule. A
// positive limit sets the limit, and a limit of zero or less keeps the
// limit as it is. A program thus passes a field of a configuration as it
// is, and an unset field keeps the default. [NoLimit] turns the check
// off, so the off state is visible at the call site. Given more than
// once, the last option with a positive limit wins.
//
// # Lines
//
// A YAML token can span several lines, as block scalars and multiline strings
// do, but diffing, printing, and searching are much simpler line by line.
// [line.NewLines] splits each multiline token at its line boundaries into one
// part per line. Each part keeps a reference to the original token it came
// from, and [line.Lines.Tokens] reverses the split.
//
// A [Source] splits its tokens this way in its constructor. [Source.Lines]
// returns the result, and [Source.View] returns a view to decorate:
//
//	view := source.View()
//	view.AddOverlay(kind.GenericError, errorRange)
//	view.BlendOverlay(kind.GenericHighlight, matches...)
//	lipgloss.Println(p.Print(view))
//
// The lines hold every token that the module returns, from [Source.Tokens],
// [line.Lines.TokenAt], [line.Line.Tokens], or [line.Line.Token]. Treat those
// tokens as read-only, and call [token.Token.Clone] before you modify one. A
// copy of a token, such as one from the [*ast.File] that [Source.File]
// parses, matches the original by its type, value, origin, and position. A
// token from a node thus finds its lines as the original does. The [line]
// package documents the view and its metadata in full.
//
// # Documents
//
// A [Source] holds one YAML file, which is a stream of text with one or more
// documents in it. Its constructor lexes the text. The Source parses the
// tokens the first time a caller asks for the tree or a document, through
// [Source.File], [Source.Documents], or any method that reads them. Every
// later call returns the same tree, the same Nodes, and the same errors.
//
// The comments and the %YAML or %TAG directives above a "---" header belong
// to the document below them. The comments after a "..." marker belong to the
// next document. When a document does not follow the marker, they belong to
// the last document. [Node.Preamble] returns the comments and directives
// above the content of a document. A file that opens with a license header
// thus holds one document.
//
// Each document parses on its own, so a YAML syntax error fails only the
// document that holds it. The [Node] of a document that did not parse keeps
// its place among the documents of the file. [Node.Err] returns the error of
// its parse, and so do the methods that Node.Err lists, such as [Node.Decode]
// and [Node.Validate]. [Node.AST] returns nil for it, and [Node.View] renders
// its lines as it renders those of any document.
//
// A "---" header that directly follows an anchor without a value parses
// together with the document above it. A syntax error in either document thus
// fails both, and both return that one error. The two documents also share
// one count of the document byte limit of [WithDocumentByteLimit].
// [Source.File] returns the errors of the documents that did not parse in one
// [*SourceError] bound to the Source, and [Bindings] yields the error of each
// document from it. Each syntax error matches [ErrSyntax], and each error of
// a document past a limit of the Source matches [ErrLimit]. A text that is
// not UTF-8 does not parse at all. The whole text is one document whose error
// matches [ErrEncoding].
//
// A document without content is empty, as [Node.IsEmpty] reports. A trailing
// "---" leaves one, as in `name: x\n---\n`. So does a Helm template that
// renders to a comment alone, or to a "---" header with only comments below
// it. In a file that holds a document with content, [Source.Documents] skips
// every empty document, and so do [Source.Document], [Source.Decode],
// [Source.ValidateDocuments], and the path resolution of [Source.Bind].
// Kubernetes manifests and the output of a Helm chart thus decode and
// validate as they are.
//
// [Source.AllDocuments] returns every document, and [Node.DocumentIndex]
// counts every document. A document below an empty one thus has an index
// larger than its place among the documents that Source.Documents returns.
//
// For a file without content, Source.Documents returns one empty document.
// Files without content include an empty file, a file of only whitespace or
// comments, and a stream of only "..." markers. A file of only empty
// documents is one too, and Source.Documents returns the first of those
// documents. The YAML spec does not find a document in a stream of markers
// alone, and niceyaml departs from the spec so that such a file reads as an
// empty file does. The empty document decodes to the zero value, and a schema
// that requires a mapping rejects it. [SkipEmpty] wraps a validator so that
// it passes an empty document, for a caller whose file might be empty as a
// whole.
//
// # Errors
//
// An [*Error] is what a check knows about a problem: a message, a location,
// and whether the document is at fault, but not the source. A validator
// builds one without holding the document. Binding the Error through a [Node]
// or a [Source] resolves the location against the text and produces a
// [*SourceError]. The message of a SourceError puts the position and the path
// in front of the text, as in "cfg.yaml:4:10: $.license: no such file".
// [Error.Error] returns the message alone. A wrapper such as [fmt.Errorf] can
// thus add context anywhere between the check and the binding, and the bound
// line keeps that context after the path.
//
// An error from a method of a [Node], such as [Node.DecodeAt], is bound
// already. A wrapper around it reads as the same wrapper inside the binding
// does. A [Validator] that adds "decode price:" to such an error with
// [fmt.Errorf] thus gets the row "cfg.yaml:4:3: $.price: decode price: not
// found". The row reads the same when the call joins that problem with
// others.
//
// [Problems] yields that problem with "not found" as its
// [ErrorTree.Message] and "decode price" as its [ErrorTree.Context] in both
// cases. A wrapper around several problems is a heading above them, and
// each of those problems reports the text of the wrapper as its context
// too. A report thus carries the same message and the same context for a
// problem, however many other problems the wrapper holds.
//
// The problem is an [*Error] or a binding. An error that holds neither,
// such as the error of [os.Open], is the exception. It keeps the text of
// each wrapper that shares its row in its message. Its context holds only
// the text of the headings above it, as [ErrorTree.Context] describes.
//
// In a join, the [SourceError.Message] of the binding that niceyaml builds
// around the wrapper holds "decode price: not found", and so does the mark
// in the excerpt. [errors.As] on a lone wrapper finds the binding inside
// it, whose message is "not found", so a report reads its fields from
// Problems. The message of the wrapper itself keeps the position behind
// the context, as [fmt.Errorf] wrote it.
//
// An [Invalid], [Unchecked], or [Locate] with a location around such an
// error does not move it, because the binding resolved its location in
// the document. The excerpt then marks the value that the method read,
// and the details of the options still show. A binding whose location did
// not resolve takes the location of the options instead. So does an error
// of another source, such as an included file, and its row then names
// both files.
//
// Each bound problem of a join keeps its own place, whether a source bound
// the whole join or that problem alone. The location of the options does
// not show on it, as [Locate] describes. An included file whose check
// returns several problems thus reads "inc.yaml:1:6: $.key: bad key" and
// "inc.yaml:2:7: $.port: bad port".
//
// Each error plays one of three roles. A problem is one thing wrong, such as
// an Error from [NewError]. A detail from [WithDetails] explains a problem,
// as its reasons or a related location do, and is never a problem of its own.
// A heading stands over several problems, as a join from [errors.Join], a
// summary from [NewSummary], or a wrapper around either does.
//
// Every reader follows these roles. [SourceError.Error] prints one line per
// problem, [Problems] yields one node per problem, and [errors.Is] and
// [errors.As] match problems and skip details. [Rebase] and a scoped
// [Node.Bind] give a location to each problem that does not carry one, and
// never to a heading or a detail:
//
//	conflict := niceyaml.NewError("port 80 conflicts", niceyaml.AtPath(portB),
//		niceyaml.WithDetails(niceyaml.NewError("first declared here", niceyaml.AtPath(portA))))
//
//	return niceyaml.NewSummary(fmt.Sprintf("%d violations", len(errs)), errs...)
//
// A location is a path from [AtPath], a position from [AtPosition], a range
// from [AtRange], or a path with a position or a range. A path names the
// value the error is about, and a position or a range names the characters at
// fault. An Error with both binds at the characters and names the path in its
// message without resolving it.
//
// A path starts at one of two points, as in RFC 9535 JSONPath. A `$` path
// reads from the root of the document, and an `@` path reads from the [Node]
// that binds the error. A check for a type thus writes `@` paths from the
// value it checked. The binding puts the path of that Node in front, so every
// path that a [SourceError] reports starts at `$`. Each such path names the
// same value through any Node of the document.
//
// A path names a map key as the document spells it. [AtData] sets a path by
// the names of decoded data instead, which read from the binding Node as an
// `@` path does. The binding writes the path as the document spells it, so
// a check of decoded values names an entry by its decoded key.
//
// A heading can carry a location, such as a join that [Invalid] locates or a
// summary that [Error.With] locates. Such a heading gives its location to
// each problem below it that does not carry one. A problem with a location of
// its own keeps it, and a detail does not gain one. A lower heading that does
// not have a location takes the location and gives it to the problems below
// it. The nearest heading with a location wins. A [Rebase] between the
// heading and a problem points the problem at its base instead, so whichever
// of the two lies nearer the problem gives it its location.
//
// A join does not have a line of its own, so the location of a join shows
// only on the lines of the problems that take it:
//
//	return niceyaml.Invalid(errors.Join(sizeErr, keysErr), niceyaml.AtPath(licensePath))
//	// cfg.yaml:4:10: $.license: file is too large
//	// cfg.yaml:4:10: $.license: too many keys
//
// When every problem of the join carries a location of its own, the location
// of the join does not show.
//
// The code that builds an error declares whether the document is at fault for
// it or the check could not run. A location never decides that question.
// [NewError] and [Invalid] declare a fault. [Unchecked] takes the same
// options, shows an I/O error at the value a check read, and declares that
// the check could not run. [Locate] takes the same options and adds the
// location to an error. It does not declare anything, so the fault stays with
// that error.
//
// The document is at fault for each problem that the parse reports with
// [ErrSyntax], and for each problem that a decode reports with [ErrDecode].
// Any other error does not declare anything, and a wrapper is a fault only
// when the error it wraps is one. The error of a context that ended and a
// [PanicError] are never faults:
//
//	niceyaml.NewError("license file does not exist", niceyaml.AtPath(p)) // fault
//	niceyaml.Invalid(checkErr, niceyaml.AtPath(p))                       // fault
//	niceyaml.Unchecked(statErr, niceyaml.AtPath(p))                      // check could not run
//	niceyaml.Locate(nameErr, niceyaml.AtPath(p))                         // what nameErr declares
//
// A check is code of the caller that niceyaml runs against a document. Each
// [Validator], [SelfValidator], [SelfValidatorContext], UnmarshalYAML or
// UnmarshalText method, [CustomUnmarshaler] function, and [WithValueCheck]
// function is a check. When a check returns an error that does not declare
// anything, such as a plain I/O error, niceyaml presumes the document at fault
// for it. Such an error from an unmarshaler matches ErrDecode too. A check
// thus keeps its faults when it moves from one kind to another.
// [Node.Validate], [Source.ValidateDocuments], and the WithValidator step of a
// decode presume the fault for the result of a Validator. The decode presumes
// it for the result of each other check.
//
// The presumption covers only the result of a check that niceyaml runs.
// [Node.Bind] binds an error without it, because niceyaml did not run the
// code that returned the error. A direct call to [Validator.Check] does not
// add a presumption of its own. A Check that runs Node.Validate gets the
// presumption from that call. [Each] runs its validator that way, so a
// direct call to its Check presumes the fault for that validator.
//
// An Error from Unchecked on the cause chain of the error that a check
// returns turns the presumption off. An I/O error that a check wraps in
// Unchecked is thus not a fault, and it does not match ErrDecode. An Error
// from Locate leaves the presumption on. An Unchecked around a join turns
// the presumption off for each branch that it heads. An Unchecked in one
// branch turns it off for that branch alone, in a join or in a wrapper that
// has several %w verbs and heads several problems. A declared fault still
// holds anywhere on the chain, so NewError inside Unchecked and Unchecked
// inside Invalid are faults.
//
// When any branch of the result of a check matches [context.Canceled] or
// [context.DeadlineExceeded], niceyaml does not presume a fault for any of
// its branches, with Unchecked or without it. Only a branch that declares a
// fault, as one from NewError or Invalid does, stays a fault. The
// Cancellation section describes that rule. The same holds for the result of
// a check when any branch of it holds a [PanicError].
//
// A wrapper that [fmt.Errorf] builds with several %w verbs heads the branches
// that are problems, as a join does. Each other branch reads as a sentinel.
// The wrapper shows the text of a sentinel, but a sentinel is not a problem
// of its own.
//
// A branch is a problem when it carries a location or errors below it. So is
// a branch that holds an Error that declares a fault, declares that its check
// could not run, or marks a severity. These Errors come from calls such as
// NewError, [NewWarning], Invalid, Unchecked, and [AsErrors]. A wrapper with
// one such branch is that problem, so a sentinel in front of a warning leaves
// one warning:
//
//	return fmt.Errorf("%w: %w", ErrLegacy, niceyaml.NewWarning("legacy layout"))
//
// Readers cannot distinguish a plain error, such as one from [errors.New],
// from a sentinel, so a plain error beside a problem reads as a sentinel too.
// Return separate problems with [errors.Join], or build each with NewError,
// Invalid, or Unchecked. Invalid does not declare anything for the error of a
// context that ended. An Error from Invalid without a location around such an
// error thus reads as a sentinel as well. An Error from Locate without a
// location or details reads as the error it wraps does, so one around a plain
// error reads as a sentinel too.
//
// [IsInvalid] reports whether the document is at fault for every problem of
// an error. A caller reads the answer to pick a status code or an exit code.
// [ErrorTree.IsInvalid] asks the same of one row of a report. One problem
// that is not a fault makes IsInvalid report false for the whole error, and a
// heading or a detail does not decide anything. A [PanicError] is a bug in
// code, but a document can still trigger one in go-yaml. A public endpoint
// that answers such a document with a 4xx status thus tests for the
// PanicError before it asks IsInvalid.
//
// A plain I/O error beside violations in one wrapper with several %w verbs
// reads as a sentinel, so IsInvalid does not count it. IsInvalid counts it in
// a join of the two, and in the wrapper when Unchecked wraps the I/O error. A
// wrapper that is one problem does not declare a fault when a branch holds
// the error of a context that ended. Invalid does not declare a fault for
// that error either.
//
// A check that runs on a value inside a document reports `@` paths that read
// from that value. [Rebase] puts the path of the value in front of each `@`
// path in the tree of an error, details included. It also points each problem
// that does not carry a location at the value itself. A heading with a
// location inside the rebased error gives that location to the problems below
// it instead. A `$` path, a position, and a range stay as they are, and
// rebases compose. A decode rebases what every nested [SelfValidator],
// every nested [SelfValidatorContext], and every function from
// WithValueCheck returns.
//
// A [Node] from [Node.At] or [Node.Nodes] binds an error as a Rebase under
// its own path does. An error that a check binds through the Node of its
// value thus does not need a Rebase. A path names a map key as the document
// spells it, so a base that a program builds from a decoded key can miss. For
// example, the key 0x10 of a map[int]T decodes to 16, and a base with the key
// 16 misses it. [AtData] locates an error by the names of decoded data
// instead. The Node that binds the error writes the path as the document
// spells it, and a Rebase puts its base in front of the names.
//
// Binding resolves each location once. A check for a required field names a
// path to a value that the document does not hold. Such a path binds at the
// nearest node that the document holds along it, whatever selector misses.
// A mapping that lacks a key, a sequence too short for an index, and a null
// bind at the key of their entry. A scalar that a path looks into binds at
// its own text. A path that enters an alias the document cannot follow binds
// at the alias. One such alias refers to an anchor in a reference document.
// A [SelfValidator], a [SelfValidatorContext], or a function from
// WithValueCheck can return an error below a value that decodes itself.
// Such an error binds at that value when its path does not bind at any node
// of the document, such as a wildcard path.
// [SourceError.Nearest] reports the node in each case.
//
// Any other location that does not resolve costs the binding its position.
// A wildcard path, a path in a document without content, and a line past
// the end of the source are such locations.
// [SourceError.Unresolved] then returns the reason, such as [ErrOutOfRange]
// for a line that the source does not hold, or [ErrPathNeedsDocument] for a
// path without a single document to resolve in. Such a binding names its
// source in front of the message, and [FormatError] prints the reason in
// place of its excerpt.
//
// An error that is or wraps a [*SourceError] is already bound, unless an
// [*Error] above it carries a location, heads errors, or holds details.
// [Rebase] and every Bind method return a bound error as it is.
//
// The error that [BindValue] returns is the exception. BindValue binds an
// error about a value that did not come from a document, such as the body of
// a request. Its text names each path from that value, as in "$.port: 0 is
// less than 1". It does not stand in a document, so a document can still
// place it.
//
// Rebase and every Bind method read that error as the error that BindValue
// built it from. A decode places it at the value whose Validate method,
// ValidateContext method, [Validator], or function from WithValueCheck
// returned it. A wrapper around it keeps its own text, after the position and
// the path. Placing it builds a new error and leaves the error from BindValue
// as it is:
//
//	err := engine.ValidateValue(cfg.Request)
//
//	return doc.Bind(niceyaml.Rebase(err, paths.Doc().Child("request")))
//
// # Error Presentation
//
// [FormatError] prints an error as plain text for a log. The message comes
// first, as a tree with a connector in front of each error below another. An
// excerpt of each source follows, with context lines around each location and
// carets under the offending columns. FormatError looks inside the wrappers
// and joins around every [SourceError], and its output does not hold escape
// sequences:
//
//	log.Print(niceyaml.FormatError(err))
//
// A logger formats an error itself. The %+v verb of an [*Error] or a
// [*SourceError] prints what FormatError prints, and [Error.LogValue] and
// [SourceError.LogValue] give a [log/slog] handler the tree without the
// excerpt. The %+v verb and the LogValue methods show at most
// [ErrorListLimit] problems and count the rest. An error with thousands of
// problems thus reaches a log as a short text, but FormatError renders every
// problem in its tree. [Source.File] and [Source.ValidateDocuments] return
// one SourceError for a file, however many of its documents fail, so the
// cap holds for the errors of a whole file. A program that wants a short
// report from FormatError, [NewErrorReport], or
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] passes
// [WithProblemLimit]. The report then keeps the first problems of the tree
// and the headings above them, and it counts the rest in
// [ErrorReport.Omitted]. Its excerpts mark only the problems it keeps:
//
//	log.Print(niceyaml.FormatError(err, niceyaml.WithProblemLimit(50)))
//
// The LogValue methods give every error one string, so a log store reads the
// attribute of an error as text. A program that wants the fields of each
// problem in a structured log logs the nodes that [Problems] yields under a
// key of its own. A JSON handler writes each node through
// [ErrorTree.MarshalJSON]. Unlike the string from LogValue, the slice
// holds every problem that Problems yields, each with all of its details.
// A document with thousands of problems thus logs one large record. A
// program that logs errors from untrusted input caps the slice itself, for
// example at [ErrorListLimit] problems:
//
//	problems := slices.Collect(niceyaml.Problems(err))
//	problems = problems[:min(len(problems), niceyaml.ErrorListLimit)]
//	logger.Error("decode", slog.Any("err", err), slog.Any("problems", problems))
//
// An excerpt shows lines of the source to whoever reads the log. A program
// that loads a text with secrets in it, such as the values of its
// environment, creates that [Source] with [WithExcerpts] set to false. Every
// renderer then prints the position, the path, and the message of an error
// that binds there, but not a line of the text.
//
// An excerpt bounds what one line adds to the output. A line longer than
// [DefaultExcerptWidth] columns shows a window of that many columns around
// each location that the excerpt marks on it. An excerpt marks at most
// [DefaultExcerptMarks] problems on one line, and a row below their carets
// counts the rest, as "and 15990 more on this line". A document minified
// onto one line thus adds at most that many windows and caret rows,
// however many problems the line holds. The tree still names every problem
// unless [WithProblemLimit] cuts it. For each renderer, [WithContextLines]
// sets the number of context lines, [WithExcerptWidth] sets that width, and
// [WithExcerptMarks] sets that number of marks.
//
// To build a report for another program to read, such as JSON lines, CI
// annotations, or editor diagnostics, a program walks [Problems]. Problems
// yields one node of the [ErrorTree] per problem, with its details below it.
// A row takes its fields from the node through [ErrorTree.Message],
// [ErrorTree.Context], [ErrorTree.Path], [ErrorTree.Name], and
// [ErrorTree.Position], rather than from its label. The position counts from
// 0, and [position.Position.OneBased] prints it counted from 1, as the row
// shows it. The message is that of the problem alone. The context is the
// text that each wrapper above the problem added, such as "decode price",
// whether the wrapper holds that problem alone or heads several. A tool that
// opens the file, such as an editor, reads [ErrorTree.FilePath] in place of
// the name. [errors.As] on the Err field of the node finds the typed error
// of that problem.
//
// A column counts runes. A language server converts each column with
// [Source.UTF16Column] before it publishes a diagnostic, because a client
// of the Language Server Protocol counts columns in UTF-16 code units.
// [Source.PositionAtUTF16] converts a cursor from the client back to a
// position.
//
// A report in JSON encodes each node. [ErrorTree.MarshalJSON] writes the
// message, the context, the name, the file path, and the path as its text.
// It writes [ErrorTree.Range] counted from 0, as every position in this
// module counts, and does not write the position. The row of a node
// counts from 1, so a problem that the row shows at 2:7 writes a range
// that starts at {"line":1,"col":6}. For a position from [AtPosition], the
// range starts at the content of the token, so a report that needs the
// exact position reads [ErrorTree.Position]:
//
//	enc := json.NewEncoder(w)
//	for problem := range niceyaml.Problems(err) {
//		if err := enc.Encode(problem); err != nil {
//			return err
//		}
//	}
//
// A terminal gets color from
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError], which takes the
// same options:
//
//	p := printer.New(printer.WithWrapWidth(width))
//	lipgloss.Fprintln(os.Stderr, p.PrintError(err, niceyaml.WithContextLines(3)))
//
// FormatError and PrintError both draw the [ErrorReport] that
// [NewErrorReport] builds. The report holds the tree, the excerpt of each
// source, and the bindings that get a reason in place of an excerpt. A custom
// renderer, such as one that writes HTML, draws the same report. It reads
// the row of each node in parts from [ErrorTree.Row], so it can style the
// location, the "warning:" label, and the path apart from the text. A
// renderer that draws plain text reads [ErrorTree.Label] instead.
//
// A bound error keeps its [Source] alive, with every line, token, and parse
// of the file, and so does its report. A program that holds errors after it
// drops their files passes each error through [Detach]. A check that prints
// the errors of every file at the end is one such program. The detached error
// keeps its messages, its locations, and a copy of the lines that its
// excerpts show, and it renders as the error did:
//
//	warnings := niceyaml.NewWarnings()
//	err := source.ValidateDocuments(warnings.Context(ctx), reg)
//	if report := warnings.Report(err); report != nil {
//		errs = append(errs, niceyaml.Detach(report))
//	}
//
// Report returns the warnings of a file that passes too, so the program
// calls it whether or not err is nil. [HasErrors] then tells the report of
// a file that failed from the report of one that only warns. Each file gets
// a collector of its own, because Report returns every warning that the
// collector holds.
//
// Of the lines of its file, a detached error keeps only the lines that its
// excerpts show. Those include the excerpt that go-yaml renders for each
// syntax error and decode error, a fixed number of lines around the token of
// the error. The memory that those lines take thus grows with the number of
// problems and with the context lines around each. It does not grow with the
// length of the file, with two exceptions. One line of a scalar that spans
// several lines carries the value of the whole scalar, and the copy of that
// line keeps that value. A syntax error in a quoted scalar with no closing
// quote keeps the rest of the source.
//
// The program detaches the whole error of a file once, after
// [Warnings.Report] adds the warnings of the collector. When the bindings of
// one source go through separate calls of Detach, they render as bindings of
// separate sources, each under an excerpt of its own. Such a binding in the
// error of a [Locate] result with a location reads as a live binding when its
// source binds the result, as Locate describes. A collector keeps the Source
// of each warning it holds alive until the program drops the collector.
//
// Detach rebuilds an error type of the program around detached bindings
// when the type has a DetachErrors method, as Detach describes. An error
// type without the method comes back as it is, and keeps the Source of each
// binding that it wraps.
//
// The marks of an error decorate a [line.View], so this package does not
// depend on the printer. [Annotate] marks every binding of an error on a view
// that holds lines of its source, such as the whole source, one document of
// it, or a diff against another revision. [line.View.Hunks] then keeps the
// marked lines with context around each:
//
//	view := source.View()
//	niceyaml.Annotate(err, view)
//	lipgloss.Println(p.Print(view.Hunks(2)))
//
// [SourceError.Excerpt] returns those hunks for one binding. [Node.Ranges]
// returns the ranges that an error at a path highlights. [Node.PathAt]
// returns the path of the node at a position, for a viewer that names the
// value under its cursor.
//
// A unified diff shows each unchanged line as the line of the after revision.
// An error of the before revision thus marks only the lines that the diff
// deletes from it. [go.jacobcolvin.com/niceyaml/diff.Result.UnifiedRow] maps
// each line of the before revision to its row.
//
// # Warnings
//
// A problem is either an error or a warning, and a warning does not make a
// check fail. [NewWarning] takes the options of [NewError], so a warning
// carries a location and details and binds to a [Source] as an error does.
// The nearest severity mark on the cause chain of a problem decides its
// severity. NewWarning, [Node.Warn], [WarnValue], and [AsWarnings] mark a
// warning, [AsErrors] marks an error above each warning, and a problem
// without a mark is an error. AsWarnings keeps the type of the error it
// marks, so a check that warns with an error type of its own gives
// [errors.As] that type.
// [Invalid], [Unchecked], [Locate], [Rebase], [Node.Bind], and [fmt.Errorf]
// do not add a mark, so a warning stays a warning inside them. A warning
// among the details of an error leaves that error an error.
//
// Every reader distinguishes errors from warnings. [HasErrors] reports
// whether an error holds an error, and [IsInvalid] decides from the errors
// and skips the warnings. Each node of an [ErrorTree] reports its severity in
// its Warning field. [errors.Is] and [errors.As] match a warning as they
// match an error. Each renderer writes "warning:" after the position of a
// warning, where compilers put it. A heading that niceyaml writes counts each
// severity, as in "cafe.yaml: 2 errors, 1 warning":
//
//	cafe.yaml: 2 errors, 1 warning
//	|-- 2:1: $.curency~: unknown field "curency"
//	|-- 3:1: warning: $.legacyPricing~: deprecated field "legacyPricing"
//	`-- 4:1: $.wifi~: unknown field "wifi"
//
// A [Validator] reports each warning with Node.Warn and returns only its
// failures. A check that gets a context and not a Node reports each warning
// with [WarnValue] in the same way. Such checks include a function of
// [WithValueCheck], a ValidateContext method of a [SelfValidatorContext],
// and an engine that checks a decoded value, as
// [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue] does. A nested
// call that gets that context, such as [SelfValidateValue], hands its
// warnings to that context too. A Validate method of a [SelfValidator] does
// not get a context, so it returns its warnings beside its errors. [Node.Validate],
// [Source.ValidateDocuments], the Decode methods of Node and Source,
// [Node.SelfValidate], [Source.SelfValidate], and [SelfValidateValue] collect
// the warnings of the checks they run. Each such call gives every warning to
// the [Warnings] collector on its context, whether the call passes or fails.
// Without a collector on the context, the call drops its warnings. A decode
// under [UnknownFieldsWarn] fails instead with an error that wraps
// [ErrNoWarnings], because nothing receives the warnings that the caller asked
// for. The call returns only its errors, so a call that returns nil passed,
// and [errors.Is] on its error does not match a warning.
//
// A decode stops at the first step that fails, as the Decoding section
// describes, so its collector gets the warnings of the steps that ran. When
// a [Validator] fails, the decoder and the Validate and ValidateContext
// methods do not run. When the decoder rejects the document, the Validate
// and ValidateContext methods do not run. Rules that apply to one call and
// must report beside the warnings of those methods go in functions of
// [WithValueCheck]. Those functions run in the step of the Validate and
// ValidateContext methods.
//
// The call returns a failure that does not hold a warning as the check wrote
// it. In a result that holds both, the call removes the warnings from the
// joins that [errors.Join] builds and from the wrappers of one %w verb that
// [fmt.Errorf] builds. It keeps those joins and wrappers. An error of another
// type that holds both keeps its type only through a WithoutWarnings method.
// The method returns the error without its warnings, as [WithoutWarnings]
// describes. The call looks for the method on the error that the check
// returns, on the branches of those joins, and on the errors that those
// wrappers wrap. It uses the result of the method only when that result
// holds an error and does not hold a warning. Any other error that holds
// both becomes the join of its errors. Such errors include a summary from
// [NewSummary], an error whose method returns nil or its receiver, and an
// error that [Node.Bind] has bound. That join drops the heading and the type
// of the error, so [errors.As] does not find that type.
//
// A [Validator] keeps the type of its failure without such a method. It
// reports its warnings with Node.Warn and returns a failure that holds only
// errors, which the call returns as the validator wrote it. A failure that
// the validator binds with Node.Bind and that still holds a warning loses
// its type, even with the method. A check without a [*Node] that reports
// its warnings with [WarnValue] keeps the type of its failure in the same
// way. A Validate method does not get a context. It returns its warnings
// beside its errors, so a type of its own that holds both needs the method.
//
// Each problem keeps its [ErrorTree.Context] through that split. A warning
// that the call removes from a wrapper still reports the text of that
// wrapper, and so does each error of such a join.
//
// A program that prints only the error of a call does not show its warnings.
// A renderer that lists the warnings between the errors, as a compiler does,
// asks [Warnings.Report] for one error that holds both:
//
//	warnings := niceyaml.NewWarnings()
//
//	config, err := doc.Decode[Config](warnings.Context(ctx), niceyaml.WithValidator(reg))
//	if err != nil {
//		return warnings.Report(err)
//	}
//
// When the call passes, its warnings stay in the collector, and
// [Warnings.Err] returns them.
//
// Wherever niceyaml reads a result to decide whether a check passed, a result
// in which every problem is a warning counts as a pass. [ChainValidator] thus
// continues to the next validator after a validator that gave only warnings.
// The decoder step reports each problem as an error, whatever it wraps, except
// the unknown fields that [UnknownFieldsWarn] reports as warnings. A value
// that decodes itself thus warns from its Validate or ValidateContext method
// rather than from its unmarshaler.
//
// A wrapper that [fmt.Errorf] builds with several %w verbs heads a warning
// and an Error from [NewError] beside it as two problems, as the Errors
// section describes. A plain error beside a warning in such a wrapper reads
// as a sentinel, so the wrapper is one warning, and a check that returns it
// passes. Join the plain error and the warning with [errors.Join], or build
// the error with NewError, to keep it an error.
//
// A program in strict mode turns each warning into an error by applying
// AsErrors to the report of the call:
//
//	warnings := niceyaml.NewWarnings()
//
//	config, err := doc.Decode[Config](warnings.Context(ctx), niceyaml.WithValidator(reg))
//	if strict {
//		err = niceyaml.AsErrors(warnings.Report(err))
//	}
//
// # Decoding
//
// [Source.Decode] decodes a file that holds one document, and
// [Source.Document] returns the root [Node] of that document.
// [Source.Documents] returns the root of each document of a file that holds
// several:
//
//	for _, doc := range source.Documents() {
//		config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//		if err != nil {
//			return err
//		}
//	}
//
// The Node of a document that did not parse returns the error of its parse
// from [Node.Decode]. A loop over the documents thus gets that error beside
// the errors of the documents that parsed. [Source.ValidateDocuments]
// validates every document and returns their errors in one [*SourceError].
// The Documents section describes syntax errors and empty documents.
//
// [Node.Decode] returns a new value, and [Node.DecodeInto] fills one that the
// caller holds. A decode runs these steps in order, and the first step that
// fails ends it:
//
//  1. The target must be a non-nil pointer, or the decode returns an error
//     that wraps [ErrDecodeTarget]. A decode under [UnknownFieldsWarn] must
//     carry a [Warnings] collector on its context, or it returns an error
//     that wraps [ErrNoWarnings].
//  2. For a document that did not parse, the decode returns the error of its
//     parse.
//  3. Each [Validator] from [WithValidator] checks the node, in the order the
//     caller gives them.
//  4. A decode of a value that holds an alias fails with an error that
//     matches [ErrExcessiveAliasing] when nested aliases make a decode of the
//     document read far more than the document holds. The Alias Limit
//     section names the value that a decode of a Node at an alias reads.
//     The type of the target reads text when any type that a decode into it
//     reaches reads text. Those types include the types of its fields and
//     elements, and the key and value types of its maps. For such a target,
//     the decode also fails when the aliases of the whole document repeat
//     more text than the document byte limit of the [Source] allows. It
//     fails as well when they nest the document past its depth limit, as
//     the Limits section describes.
//  5. The go-yaml decoder fills the target.
//  6. Every value in the result that implements [SelfValidator] or
//     [SelfValidatorContext] validates itself, and each function from
//     [WithValueCheck] or [WithNodeCheck] checks the values of its type.
//     [WithSelfValidation] set to false turns this step off, and
//     [WithSelfValidationOf] set to false skips the methods of one type.
//
// A field that the document leaves out keeps its value. A mapping merges into
// a struct field by field at every depth, and the document replaces a slice,
// an array, a map, or an interface value whole. A null leaves the target as
// it is, unless the target is a pointer or an interface. A null without a tag
// sets a pointer or an interface to nil. A null under a key leaves a struct
// field as it is too, unless the field is a pointer. The Defaults section
// describes the starting value of each value that a decode creates.
//
// A field, an element, or a map entry of type [ast.Node], or of a pointer to
// a node type of the go-yaml ast package, gets a node of the document. The
// node is the one that [Node.AST] returns for its place, and at an alias it
// is the content of the anchor. It prints the text of the document, and
// [Node.DocumentResolver] resolves paths from it. The go-yaml decoder reads a
// tree of its own, which gives some anchors new names. The decode then puts
// the node of the document in place of each node of that tree. A value of a
// node type that is not a pointer, such as [ast.MappingNode], gets a copy of
// the node of the document. The copy prints the text of the document, but it is not
// the node that [Node.AST] returns.
//
// The decode does not reach a node that the decoder hands an UnmarshalYAML
// method, or a node inside a type that decodes itself. A node inside an
// anchor of a reference document from [WithReferences] stays the decoder's
// node too. Such a node spells an alias to a renamed anchor with a count in
// brackets, such as `*x [2]`, which is not valid YAML. It spells a !!int tag
// on an integer in the verbatim form `!<tag:yaml.org,2002:int>`. The
// resolver of the document does not know the node. A caller can decode a
// value from such a node and read the positions of its tokens, but should
// not write its text out as YAML.
//
// A [DecodeOption] sets how one decode runs, and it applies only to the call
// that gets it. A setting that describes the documents, such as
// [WithReferences] or [WithAliasLimit], belongs to the [Source], which
// applies it to every decode and validation of them.
//
// A decode binds each alias to an anchor by the rule of
// [go.jacobcolvin.com/niceyaml/paths.Resolver], which the paths package
// describes. An alias reads the last anchor of its name before it, inside
// the decoded node or outside it. An anchor counts again where its value
// ends, so an alias after it reads it rather than an anchor of its name
// inside its value. An anchor on the value of a `<<` merge key counts for
// an alias elsewhere only when the document does not define another anchor
// of its name before the alias. At each merge key, the anchors inside each
// mapping that the merge key brings in count again. Each alias keeps the
// anchor that the rule binds it to, wherever a decode reads it. When the
// document does not define an anchor of that name before the alias, the
// alias reads one of a reference document.
//
// The decoder of goccy/go-yaml binds some aliases of reused anchor names
// otherwise, so yaml.Unmarshal reads some documents apart from a decode of
// niceyaml:
//
//   - An alias after an anchor on a merge value reads the last other anchor
//     of its name in go-yaml, even one after the alias. In
//     "m: {<<: &x {p: 1}}", "b: *x", and "c: &x 2", go-yaml reads b as 2,
//     and niceyaml reads b as {p: 1}.
//   - At a merge key, go-yaml looks up again each alias inside the mapping
//     that the merge key brings in. In "k1: &k 1", "b: &b {v: *k}",
//     "k2: &k 2", and "m: {<<: *b}", go-yaml reads m.v as 2, and niceyaml
//     reads m.v as 1.
//   - An alias inside the value of an anchor of its name reads null in
//     go-yaml, even after an inner anchor of that name. In
//     "r1: &x0 {z1: [&x0 d, *x0]}", go-yaml reads z1 as [d, null], and
//     niceyaml reads z1 as [d, d].
//   - An alias key inside the value of its anchor reads that anchor in
//     go-yaml where a merge key brings it in. In "b: &b {*b : 1}" and
//     "m: {<<: *b}", go-yaml reads m as {"map[null:1]": 1}, and niceyaml
//     reads m as {null: 1}.
//
// The [Unmarshaler] values from [CustomUnmarshaler] and [JSONUnmarshalers]
// set how the types of the program read YAML, so a program gives them to the
// Source with [WithUnmarshalers]. Every decode of the documents then applies
// them, wherever it runs. That rule covers a decode inside a [Validator], the
// decode function that a CustomUnmarshaler function gets, and the map keys
// that [Node.SelfValidate] spells. It also covers a decode in code that did
// not build the Source, such as a library that the program passes the Source
// to. The unmarshalers of [WithUnmarshaler] in a call take precedence over
// them, so a call can replace or remove the function of a type and turn the
// JSON methods off. A program gives the unmarshalers of a Source from
// [NewSourceFromLayers] to [MergeUnmarshalers], as the Layers section
// describes. The [go.jacobcolvin.com/niceyaml/encoder.Marshaler] values set
// how the same types write YAML, for an encode and for the edits of the
// edit package.
//
// When the decoder rejects a value, the decode returns an error that matches
// [ErrDecode] and carries the `$` path of the value. Its message describes
// the document rather than the Go target:
//
//	config.yaml:3:11: $.servers[0].port: expected integer, got string
//
// The kinds that a message names are mapping, sequence, string, integer,
// float, boolean, null, timestamp, binary for a value under a !!binary tag,
// and duration for a [time.Duration]. The message for a number outside the
// range of its type reads "expected integer from 0 to 65535, got 70000". The
// rejection of a key, such as an unknown field, carries a path that ends in
// `~`. The error for a value of the wrong kind matches [ErrWrongKind], the
// error for a number out of range matches [ErrOverflow], and the error for an
// unknown field matches [ErrUnknownField]. Each of them also matches
// ErrDecode.
//
// The path names the way that the decode read the value, and the position
// names where the document writes it, as for a schema error. A value inside
// an anchor that an alias or a `<<` merge key brings in thus reports the path
// through that alias or merge key, at the position of the anchor:
//
//	config.yaml:1:21: $.hours.open: expected integer, got string
//
// The decode of the anchor itself reports the path of the anchor. Where the
// path of a use selects another value, such as a later entry of the same
// key, the error reports the path where the document writes the value. So
// do the rejection of a map key and every rejection that the decode cannot
// tie to a use. A value of a reference document reports its path below the
// alias that reads it, as the References section describes. A value below
// one that a [CustomUnmarshaler] function decodes reports its path below
// that value, even through an alias. The function reads the value with
// each alias and each `<<` merge key written out.
//
// A `<<` merge key can write its source in place, as a mapping or a sequence
// of mappings. The path of a value there names the entry in the mapping that
// holds the merge key, as a path through the merge reads it.
// `server: {<<: {port: x}}` thus gives `$.server.port`. The path does not name
// the merge key or an index of a sequence there.
//
// A later entry of the key can take that path. Such an entry lies in the
// mapping that holds the merge key, or in a later source of the same merge
// key. The path then goes through the merge key instead, and through the index
// of the source in a sequence of sources. In a decode into
// map[string]map[string]int, `server: {<<: {port: x}, port: 1}` thus gives
// `$.server.<<.port`, and `server: {<<: [{port: x}, {port: 1}]}` gives
// `$.server.<<[0].port`.
//
// The decoder stops at the first value it rejects. The decode then searches
// for the other problems of the document and reports them together, in the
// order of the source:
//
//	app.yaml: 3 problems
//	app.yaml:1:1: $.replcas~: unknown field "replcas"
//	app.yaml:2:10: $.timeout: expected integer, got string
//	app.yaml:4:11: $.servers[0].port: expected integer, got string
//
// The error is a summary from [NewSummary] that heads one error per problem,
// and [ErrorTree.Problems] yields those errors. For a document with one
// problem, the decode returns that problem alone. A value that several
// aliases or `<<` merge keys read gives one problem for each use that the
// decoder rejects, each with the path of that use. The search adds values of
// the wrong kind, numbers out of range, durations that do not parse, scalars
// that an UnmarshalText method rejects, and unknown fields under
// [UnknownFieldsReject]. The decoder confirms each one.
//
// The search does not read a value of a reference document, such as the
// value at an alias of one. Such a value reports a problem only when the
// decoder rejects it first, so each decode reports at most one problem of
// the reference documents.
//
// Apart from those scalars and a [time.Time], the search does not read
// anything at or below a value that decodes itself. A type with an
// UnmarshalYAML method thus reports one bad value per decode. A value
// validates itself only after the decoder accepts the document. The search
// calls an UnmarshalText method again on a new value, so the method must
// answer from its text alone.
//
// The decoder returns the error of a value that decodes itself without a
// token of the source. Such a value has an unmarshaler method that the
// decoder calls, a function from [CustomUnmarshaler], or the type
// [time.Duration]. The decode then decodes each such value again, in the
// order the decoder reads them. It binds the error at the first value that
// fails the same way, or at the deepest value inside that one that also fails
// that way:
//
//	config.yaml:7:14: $.servers[1].timeout: time: invalid duration "soon"
//
// For a value inside an anchor of a reference document, the error binds at
// the alias that reads the anchor, as the References section describes.
//
// An `@` path in an [*Error] from an unmarshaler reads from the value of that
// unmarshaler. The error stays in the chain beside ErrDecode, unless it holds
// an Error from [Unchecked], as the Errors section describes. An unmarshaler
// that answers differently the second time can move the error to another
// value. When an error does not recur at any value, the decode returns it
// without a location.
//
// The search, the decodes that locate an error, and the fill of the
// defaults decode values again. So does the self-validation walk, which
// decodes map keys to spell them. Each unmarshaler that such a decode runs
// gets a context without the [Warnings] collector of the call. The
// collector thus gets only the warnings that the unmarshalers hand to the
// context of the decoder step.
//
// The decode recovers a panic in the go-yaml decoder and in the unmarshalers
// it calls. It returns the panic as an error that holds a [PanicError] and
// binds that error at the first token of the node. That error does not match
// ErrDecode, and [IsInvalid] does not report it. The package does not
// recover a panic in a Validator, in a Validate or ValidateContext method, or
// in a function from [WithValueCheck].
//
// A Source from [NewSourceFromLayers] runs these steps once on the merged
// document, as the Layers section describes.
//
// # Defaults
//
// [Node.Decode] starts from the value that [Defaults] returns, and
// [Node.DecodeInto] starts from the value that the caller passes. A caller
// fills that value with Defaults, or with [DefaultsInto] for a type that it
// holds as a [reflect.Type]. A decode
// also creates values: each element of a slice or an array, each value of a
// map, and the target of each pointer that the document fills. Each starts
// from the defaults of its type, which [WithDefaults] sets:
//
//	var decoding = niceyaml.DecodeOptions(
//		niceyaml.WithDefaults(func(s *CafeSpec) {
//			s.Seats = 20
//			s.Kitchen.Port = 9101
//		}),
//		niceyaml.WithDefaults(func(p *Printer) {
//			p.Port = 9100
//		}),
//	)
//
// The defaults of a type start from its zero value, and each struct field
// takes the defaults of its own type. Then the function of the type runs, so
// a parent wins where its function writes. With the `decoding` options, each
// Printer defaults to port 9100, and the kitchen printer of a CafeSpec
// defaults to port 9101.
//
// A value that the decode creates fills from its defaults, as the Decoding
// section describes. A null element or map value holds the defaults of its
// type, and a null sets a pointer to nil. The target of a pointer that
// already points to a value, such as one that a function of WithDefaults
// sets, starts from that value. The value it pointed to stays as it was.
//
// A value that decodes itself, through an unmarshaler method or a
// [CustomUnmarshaler] function, starts from its defaults too. Its unmarshaler
// decides what to keep.
//
// The decoder starts from zero each element, each map value, and the target of
// each nil pointer in a field, element, or map value, and so every value
// inside them. When such a value decodes itself and the defaults give it a
// value other than zero, the decode calls its unmarshaler again on a value
// that starts from the defaults. The decode keeps the value of the second
// call, and the [Warnings] collector gets only the warnings of the first call.
// The decode calls the unmarshaler of every other value except a map key
// once, on the value that it starts from. It calls the unmarshaler again
// when it decodes again a value that holds it, as it does for some values
// that a reference document writes. The target of [Node.Decode] thus decodes
// itself once from its defaults, and so does the value that
// [Node.DecodeInto] allocates for a nil pointer that its target points to.
// Under [UnknownFieldsWarn], a decode whose context carries a collector also
// calls again the unmarshalers of the structs that decode themselves from a
// mapping, as [WithUnknownFields] describes.
//
// The value at an alias starts from the defaults of its own place. The places
// that aliases of one anchor fill share one value when they start from equal
// values. Two pointer fields with equal defaults thus share one target, and
// two maps or slices share their contents. A program that changes one of
// them in place changes the others too. A decode without WithDefaults gives
// each place a value of its own. A place that starts from another value
// builds a value of its own. An interface value at an alias holds the value
// of its anchor.
//
// The fill of the defaults does not map the values inside an anchor of a
// reference document to the document. The value at an alias to one still
// starts from the defaults of its place. When the decoder started that value
// from zero, the decode decodes it again from its defaults, so the
// unmarshalers inside it run a second time. Each value that the decoder
// creates inside the anchor keeps the value of the decoder.
//
// A struct field whose entry a `<<` merge key brings in from a reference
// document keeps the value that the decoder gives it. The decoder starts such
// a field from the value that its struct holds. When the decoder started the
// struct from zero, the decode decodes the struct again from its defaults
// first, so the unmarshalers inside it run a second time.
//
// Each key of a map keeps the value that the decoder gives it. A panic in a
// function of WithDefaults makes the decode return an error that holds a
// [PanicError].
//
// The decode reads each key of a map again to find its entry in these cases:
//
//   - The map starts from a map that is not nil.
//   - The map type, the key type, the value type, or a type inside them has a
//     function of WithDefaults.
//   - A struct holds the map inline, directly or through inline structs, and
//     that struct starts from a value other than zero.
//   - A struct holds the map inline, directly or through inline structs, and
//     its type or a type inside it has a function of WithDefaults.
//
// Each such read calls again the unmarshaler of each key that decodes itself.
// The decode reads the keys once for a map below a value that decodes itself.
// It also reads them once for a map behind an alias to a reference document,
// unless that map starts from a map that is not nil. The map then decodes
// again from that start. The [Warnings] collector gets only the warnings of
// the decoder's call.
//
// # Validation
//
// A [Validator] checks a node before the decode, as a JSON schema does. A
// [SelfValidator] checks a decoded value afterward, and so do a
// [SelfValidatorContext] and a function from [WithValueCheck] or
// [WithNodeCheck]. [Node.Validate] runs a Validator without decoding. A
// [go.jacobcolvin.com/niceyaml/schema.Schema] checks a node against one JSON
// schema, and a [go.jacobcolvin.com/niceyaml/schema.Registry] picks the schema
// for each document.
//
// [MultiValidator] runs several validators and reports every failure, so it
// suits independent rules. [ChainValidator] stops at the first validator that
// fails, so it suits a check that needs an earlier check to pass. A decode
// runs the validators of repeated WithValidator options as ChainValidator
// does:
//
//	err := source.ValidateDocuments(ctx, niceyaml.MultiValidator(schema, names))
//
// Every function that takes a Validator panics for a nil one, as the
// Options section describes. The Validator from [NoValidator] passes
// every node, so a program that validates only on some condition passes
// it where it does not have a check:
//
//	v := niceyaml.NoValidator()
//	if strict {
//		v = schema
//	}
//
//	err := source.ValidateDocuments(ctx, v)
//
// A validator writes its errors from the Node it gets, so an `@` path reads
// from that Node. The decode or the Node.Validate call that runs the
// validator binds the result as [Node.Bind] binds an error. An error that
// the validator bound itself comes back as it is only when the document is
// at fault for each of its problems already and the error holds no warning.
// The call presumes the fault on any other bound error, as the Errors
// section describes.
//
// The validators of niceyaml bind their errors in [Validator.Check] the same
// way, at the Node that Check got. A validator can thus call Check of one of
// them on an element and return the result, and the error names the element.
// A type that the program writes can return its errors from Check unbound. A
// validator that returns such a result for an element reports it at the wrong
// value. A validator that runs a validator it did not write on a Node that it
// picks thus calls Validate on that Node, so the error binds there.
//
// [Each] runs a validator on every Node that a path selects, such as each
// element of a list. It binds each failure at the Node that its validator
// checked:
//
//	items := paths.Current().Child("items").IndexAll()
//	err := source.ValidateDocuments(ctx, niceyaml.MultiValidator(schema, niceyaml.Each(items, reserved)))
//
// A program passes a check of the decoded values of one type to the decode
// with [WithValueCheck]. WithValueCheck suits a rule that applies to one
// call. It also suits a check of a type from another package, because the
// program cannot add a Validate method to that type. A type that the program
// owns and whose check needs the context of the call implements
// SelfValidatorContext instead. The decode runs the function on every value
// of the type in the result, in the step where the values validate
// themselves:
//
//	allowed := niceyaml.WithValueCheck(func(ctx context.Context, p *netip.Prefix) error {
//		ok, err := tenant.Allows(ctx, *p)
//		if err != nil {
//			return niceyaml.Unchecked(err)
//		}
//
//		if !ok {
//			return niceyaml.NewError("prefix not allowed")
//		}
//
//		return nil
//	})
//
//	cfg, err := doc.Decode[Config](ctx, niceyaml.WithValidator(schema), allowed)
//
// A validator that reads decoded data decodes the node itself with
// [Node.Decode], and a decode does not pass its options to the validators it
// runs. The decode of the validator still applies the unmarshalers of
// [WithUnmarshalers], which belong to the Source. A check that runs after
// Decode returns, such as one that runs its I/O in parallel, locates each
// finding with [AtData] and binds the findings with Node.Bind. The decoder
// respells some keys, so the key 0x10 sets the member 16. The binding reads
// the names of decoded data as the decoder does. A check of the data thus
// reports a finding where a schema reports a violation of the same value. A
// check that needs the path of a finding and not an error reads it from
// [Node.DataLocator].
//
// Every check follows one rule for the fault of the document, as the Errors
// section describes. When a validator, a Validate or ValidateContext method,
// or a function from WithValueCheck returns an error that does not declare
// anything, niceyaml presumes the document at fault for it. A check thus
// keeps its faults when it moves from one kind to another. A check that
// cannot run, such as one whose I/O fails, wraps its error in [Unchecked]
// so that [IsInvalid] does not report it:
//
//	allowed := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
//		ok, err := tenant.Allows(ctx, n.FilePath())
//		if err != nil {
//			return niceyaml.Unchecked(err)
//		}
//
//		if !ok {
//			return errors.New("tenant may not deploy this file")
//		}
//
//		return nil
//	})
//
// A schema reports each violation as a fault. When the schema itself fails
// to load, its error comes inside Unchecked, so IsInvalid distinguishes the
// two. The presumption skips a result that matches [context.Canceled] or
// [context.DeadlineExceeded], as the Cancellation section describes. It
// skips a result that holds a [PanicError] too.
//
// A decode calls Validate on every value in the result that implements
// SelfValidator, and ValidateContext with the context of the call on every
// value that implements [SelfValidatorContext]. On a type that declares both
// methods, it calls ValidateContext alone. It then calls the functions from
// WithValueCheck for the type of the value, in the order the caller gives
// them. The values below a value validate first. Every check runs on every
// value of its type, whatever any other check returns, and the decode reports
// the failure of each check. A document with a problem at several depths thus
// reports all of them in one decode, the checks of a parent among them.
//
// A check therefore guards each precondition that it reads, because a check
// below it or before it might have failed. A Validate method returns nil
// when a field that it depends on is empty, and lets the check of that field
// report the problem:
//
//	func (d Database) Validate() error {
//		if len(d.Replicas) == 0 {
//			return nil
//		}
//
//		...
//	}
//
// A function from WithValueCheck that must not run on a value that fails its
// own Validate calls Validate first and returns nil when [HasErrors] reports
// an error in the result:
//
//	func(ctx context.Context, d *Database) error {
//		if niceyaml.HasErrors(d.Validate()) {
//			return nil
//		}
//
//		...
//	}
//
// The result of a Validate that only warns is not nil. A guard that tests it
// against nil thus skips the function on every value that only warns. A
// check that repeats a check below it reports a second error for the same
// problem, so each check reports only what it checks itself.
//
// A type that declares ValidateContext keeps a Validate for code outside
// niceyaml that calls that method, and that Validate calls ValidateContext
// with [context.Background]. A nested call that gets the context of
// ValidateContext, such as [SelfValidateValue], hands its warnings to that
// context. A ValidateContext hands each warning that it raises itself to
// that context with [WarnValue]. Such a ValidateContext returns nil for a
// value that passes, and so does that Validate. A guard of such a type
// calls ValidateContext with its own context and tests the result against
// nil. A ValidateContext that returns a warning beside its errors needs a
// guard that asks HasErrors instead.
//
// A Validate or ValidateContext that an embedded field promotes runs once,
// on that field. The walk counts only the methods that a type declares
// itself, not the ones it promotes. A struct that declares Validate and
// embeds a field that declares ValidateContext thus gets both, its own
// Validate and the ValidateContext of the field. A Validate or
// ValidateContext that an UnmarshalYAML method already called runs again
// in the decode, so it must be idempotent.
//
// The walk calls Validate and ValidateContext on every type that declares
// one of them, including a type of another module. The author of such a
// type might not know these rules. The method might also serve another
// purpose, such as a check that must run after the program provisions the
// value. [WithSelfValidationOf] set to false skips the methods of one type
// and keeps the rest of the walk, the functions of WithValueCheck for that
// type among them. A program that wants the check at its own time calls the
// method from such a function and wraps a failure that is not a fault of
// the document in [Unchecked]:
//
//	off := niceyaml.WithSelfValidationOf[vendor.Client](false)
//	client := niceyaml.WithValueCheck(func(ctx context.Context, c *vendor.Client) error {
//		c.Provision()
//
//		err := c.Validate()
//		if err != nil {
//			return niceyaml.Unchecked(err)
//		}
//
//		return nil
//	})
//
//	cfg, err := doc.Decode[Config](ctx, off, client)
//
// Validate and each function write `@` paths, which read from the value, and
// the decode puts them under the path of the value in the document. An Hours
// under spec that reports @.close thus reports $.spec.hours.close. The path
// names a field by its yaml tag, its json tag, or its lowercased name, as
// [FieldKey] returns it. It names an element by its index and a map entry
// by its key as the document spells it. A map key validates at the path of
// its entry with a `~` after it, and a field that an inline tag flattens
// keeps the path of its struct.
//
// A struct field that the document leaves out validates too, with the value
// that the decode left in it, such as its zero value. A field that go-yaml
// never decodes, such as one tagged `yaml:"-"`, does not validate.
//
// A value that decodes itself validates as any other value does, and so does
// every value below it. Its fields do not have to mirror the document. An
// error below it whose path the document does not hold thus binds at the
// nearest node that the document holds along that path. That node can be
// the value itself or any node below it, such as the sequence that lacks
// an element or the scalar that the value parses. [SourceError.Nearest]
// names it.
// Every error below a slice, an array, or a map that decodes itself binds at
// that value.
//
// A Validate method gets only its value. A ValidateContext method and a
// function from WithValueCheck get the value and the context of the call. A
// check that needs the Node of its value, such as one that resolves a path
// beside the file or the layer that sets the value, uses [WithNodeCheck].
// Its function gets the Node of each value of its type beside the value, or
// nil where the document does not hold the value.
//
// A path names a map key as the document spells it, and a check of decoded
// values knows only the decoded key. A check of the type of a map entry
// does not name the key, and the decode puts its paths under the key as the
// document spells it. Some checks name an entry of a map that they range
// over, such as a check of the parent of the map or of a map of builtin
// values. Such a check locates the entry with [AtData] by its decoded key.
// The decode reads those names from the value that the check saw and writes
// each key as the document spells it:
//
//	for k, p := range c.Ports {
//		if p.Name == "" {
//			return niceyaml.NewError("name is empty", niceyaml.AtData("ports", strconv.Itoa(k), "name"))
//		}
//	}
//
// [Node.SelfValidate] runs the self-validation step on its own, for a program
// whose library writes its environment or its flags into the value. The program
// decodes with [WithSelfValidation] set to false, lets the library fill the
// value, and then calls SelfValidate. SelfValidate runs only the functions of
// WithValueCheck and WithNodeCheck that it gets, so the program passes them to
// SelfValidate as well. A Validator does not see a value that the library sets
// this way. A program that keeps its constraints in a schema checks the final
// value with [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue] and
// places the result with Node.Bind.
//
// The walk follows the value, and each error binds where its path resolves in
// the document. An error under a field or an element that the document lacks
// binds at the nearest node the document holds. That node can be the
// mapping that lacks the field or the sequence that lacks the element.
// [Source.SelfValidate] runs the step through the one document of a Source.
// An empty Source represents an optional file that does not exist.
//
// A check on a value that did not come from a document, such as the body of a
// request, does not have a line to report. [SelfValidateValue] runs the
// Validate or ValidateContext method of every value below such a value, and the
// functions of WithValueCheck and WithNodeCheck that it gets. It binds the
// result with [BindValue], so the text names each path, as in "$.close: closes
// before it opens". A caller that later learns where the value stands places
// the error there with [Rebase] and Node.Bind.
//
// # Scoped Nodes
//
// [Node.At] returns a Node scoped to the node that a path selects, and
// [Node.Nodes] returns one for each node that a path with wildcards selects.
// An `@` path resolves from the receiver, and a `$` path resolves from the
// root of the document. A path from [Node.Path] thus scopes the same node
// through any Node of the document. A path that selects the root gives the
// root itself, so a document has one Node at `$`. A function that takes the
// root, such as [go.jacobcolvin.com/niceyaml/edit.New], thus takes it. A
// function that needs the root refuses any other Node with an error that
// matches [ErrScopedNode].
//
// A scoped Node reads from its scope. [Node.PathAt] and [Node.NodeAt] answer
// for the positions inside the scope alone, and [Node.Document] returns the
// root, which answers for the whole document. [Node.DocumentResolver]
// belongs to the whole document and resolves a `$` path from its root. It
// does not have a current node, so it rejects an `@` path, where [Node.At]
// reads one from the Node.
//
// A path that ends at an alias scopes the alias, as the document writes it,
// whether the alias carries a tag and wherever its anchor lies.
// [Node.AST], [Node.Tokens], [Node.Span], [Node.View], and [Node.Range]
// describe the alias, and an error at the root of the scope binds there. A
// decode, [Node.Kind], and an `@` path below the Node read the content of
// the anchor. [Node.Target] returns the Node of that content, where the
// anchor defines it, and returns the receiver for a Node that is not at an
// alias:
//
//	c, err := doc.At(paths.Doc().Child("c")) // c: *x
//	if err != nil {
//		return err
//	}
//
//	base, err := c.Target() // the Node at $.base, where &x defines it
//
// The decode steps run on a scoped Node as they run on a document. Decode
// reads the one value, and a validator that the caller passes checks that
// node. An `@` path in an error from the validator, the decoder, a
// SelfValidator, or a SelfValidatorContext resolves from the node. The bound
// error carries it as a `$` path from the root. A check for a type thus
// reports the same lines whether the type is the whole document or a value
// inside one:
//
//	hours, err := doc.At(paths.Doc().Child("spec", "hours"))
//	if err != nil {
//		return err
//	}
//
//	h, err := hours.Decode[Hours](ctx, niceyaml.WithValidator(hoursSchema))
//
// A value at an alias reports other lines in one case. A decode of the Node
// at the alias binds a rejection of the whole value at the root of the Node,
// which is the alias. A decode of an enclosing Node binds the same rejection
// at the anchor, as the Decoding section describes. Both errors carry the
// path of the alias.
//
// A Node encodes as the value that it decodes to, so
// [go.jacobcolvin.com/niceyaml/encoder.Marshal] prints the nodes that a path
// selects. [Node.MarshalYAML] describes what the encode keeps of the node.
//
// [Node.Bind] binds the result of a check that the caller runs on the value,
// and it reads the `@` paths of that result from the node. A scoped Node
// binds a problem without a location at its value, as it binds an error with
// [AtPath] of [paths.Current]. A summary or a join is only a heading, and
// each problem below it binds on its own. A problem below a heading with a
// location binds at that location instead, and that location resolves from
// the node. A detail does not gain a location.
//
// The root of a document leaves a problem without a location as it is,
// because such a problem can be about the whole document. A location does not
// declare anything, so [IsInvalid] reports a bound error only when it reports
// the error that the Node bound.
//
// [Node.Document] returns the root of the document. A validator that can
// check only a whole document, such as a
// [go.jacobcolvin.com/niceyaml/schema.Registry], refuses a scoped Node. A
// caller thus validates at the root and decodes the nodes below it without
// that validator. A decode of a scoped Node reports the path of each use
// inside the scope, as the Decoding section describes. A value that an alias
// or a `<<` merge key of the document brings in from an anchor outside the
// scope thus reports a path inside the scope, at the position of the anchor.
// The Decoding section lists the rejections that keep the path where the
// document writes the value, such as the rejection of a map key. Such a
// rejection can report a path outside the scope. A value of a reference
// document reports its path below the alias that reads it, as the References
// section describes. That path lies inside the scope
// when the alias does. An alias inside an anchor of the document puts the
// path at the place where that anchor defines it, which can lie outside the
// scope. A value below one that a [CustomUnmarshaler] function decodes
// reports its path below that value, as the Decoding section describes.
//
// [Node.DecodeAt] scopes and decodes a value that the document must hold.
// [Node.DecodeIfPresent] scopes and decodes an optional value into a variable
// that holds the default. Both drop the Node, so an `@` path in a check that
// the caller runs on the value afterward resolves from the receiver. A caller
// that binds such errors scopes the Node with At, or moves them under the
// path with [Rebase] first.
//
// [Node.At] and [Node.DecodeAt] return an error that wraps [paths.ErrNotFound]
// for a path that does not select anything, and IsInvalid reports that error.
// The error carries the path and binds at the nearest node the document
// holds, as in "cfg.yaml:3:1: $.server.name: not found" for a key that the
// mapping at line 3 lacks. In a document without content, the error does not
// have a position, and it wraps [paths.ErrNoDocument] as well. A decode can
// return an error that matches paths.ErrNotFound for a node that is present,
// so the error of At alone tells whether a value is absent.
//
// # Cancellation
//
// The context of a call reaches every [Validator] that the call runs, every
// function from [CustomUnmarshaler], [WithValueCheck], and [WithNodeCheck],
// and every ValidateContext method of a [SelfValidatorContext]. A check that
// does remote work, such as resolving a schema reference, thus stops when the
// caller cancels the context. A Validate method of a [SelfValidator] does
// not get the context, so a type whose check does I/O implements
// SelfValidatorContext instead.
//
// The go-yaml decoder never checks the context. A decode checks it before the
// decoder starts and between the anchors that the decode registers, but a
// deadline does not stop the decoder after it starts. To decode a type that
// reads its own text, such as one with an UnmarshalText method, the decoder
// reads every token of the file for each value. A long list of such values
// can thus take a second or more.
//
// An ended context stops each step that loops:
//
//   - The search for the other problems of a rejected decode stops, and the
//     decode returns the rejection alone.
//   - The self-validation walk stops. It returns the error of the context in
//     place of the errors it found, and it gives the warnings it found to the
//     collector of the context.
//   - [MultiValidator] does not run another validator. It returns the error
//     of the context, or the error of the validator that saw the context end.
//   - [Each] does not run its validator on another Node. It returns the error
//     of the context, or the error that its validator returned on the Node
//     where the context ended.
//   - [NewSourceFromLayers] returns the error of the context.
//   - [go.jacobcolvin.com/niceyaml/diff.Diff] and
//     [go.jacobcolvin.com/niceyaml/diff.Differ.Diff] stop the search for
//     the edits between two revisions, and return the error of the context
//     in place of a result.
//
// Only the context of the call stops these steps. A check can hold a deadline
// of its own, such as one that a function from WithValueCheck derives from
// its context, or the Timeout of a [net/http.Client]. The error of that
// deadline matches [context.DeadlineExceeded] when the context of the call is
// still live.
//
// The self-validation walk reports such an error as it reports any other
// error of a check, under the path of the value. It then validates the values
// that remain. MultiValidator and Each keep such an error beside the other
// failures of their run. A caller that wants the first timeout to stop the
// call cancels the context that it passed.
//
// Niceyaml does not presume the document at fault for the result of a check
// that matches [context.Canceled] or [context.DeadlineExceeded], whether the
// walk or [Node.Validate] runs the check. That rule reads the whole result,
// as [Invalid] does. One such branch in a join thus turns the presumption
// off for each problem of the join. An [Unchecked] around that branch does
// not keep the presumption for the other branches. A check that returns
// other problems beside a timeout should declare each of them with
// [NewError] or Invalid, and each declared problem stays a fault.
//
// The error of a context that ended matches [context.Canceled] or
// [context.DeadlineExceeded], but not [ErrDecode]. Invalid does not declare
// a fault for it, and [IsInvalid] does not report it. A binding does not give
// it a location, even through a scoped Node. The error of the context of a
// call thus names at most the source, as in "cfg.yaml: context canceled".
//
// # References
//
// The aliases of a document can name the anchors of another file, such as a
// file of shared defaults. The document then reads that file as a reference
// document through [WithReferences] on its Source. Every decode and every
// validation of the document reads the anchors of its reference documents, so
// a schema reads `server: *base` as the decode does. A program reads each
// reference file into a Source of its own, with [NewSourceFromFile],
// [NewSourceFromFS], or [NewSourceFromReader]. A program that keeps them in a
// directory picks the files and their order. The order decides which anchor
// wins when two of the files define the same name:
//
//	names, err := fs.Glob(fsys, "refs/*.yaml")
//	if err != nil {
//		return err
//	}
//
//	refs := make([]*niceyaml.Source, 0, len(names))
//
//	for _, name := range names {
//		ref, err := niceyaml.NewSourceFromFS(fsys, name)
//		if err != nil {
//			return err
//		}
//
//		refs = append(refs, ref)
//	}
//
//	source, err := niceyaml.NewSourceFromFile("app.yaml", niceyaml.WithReferences(refs...))
//
// These calls replace the ReferenceFiles, ReferenceDirs, RecursiveDir, and
// ReferenceReaders options of go-yaml, which a decode does not take.
//
// An alias reads an anchor of the reference documents only when its own
// document does not define an anchor of its name before it. When the
// reference documents define one anchor name more than once, the last of them
// in the text wins, even an anchor inside another anchor of that name. The
// reference documents go in the order the caller gives them, so an anchor of
// a later one overrides an anchor of the same name in an earlier one. A Source
// among them puts its own reference documents ahead of its own text, so its
// own anchors override theirs.
//
// An alias inside a reference document reads only the anchors of the
// reference documents, whatever anchors the document that reads it defines.
// It reads the last anchor of its name by the same rule, even one after it or
// in a later reference document.
//
// An anchor inside another anchor breaks the rule when a later anchor shares
// its name. A decode that reads the outer anchor registers the inner one
// again. An alias that the decode reads after the outer anchor might then
// read the inner anchor, but [NewSourceFromLayers] reads the last. A typed
// decode and a decode into an any value, which is the decode that a schema
// runs, might also read different anchors. Each anchor of the reference
// documents should thus have a name of its own.
//
// A decode reads the anchors of the reference documents that its aliases
// reach, and every anchor that an alias inside those anchors reaches. Its
// cost thus follows those anchors rather than the size of the reference
// documents, however many documents read them.
//
// A path resolves in the document itself. It selects an alias that reads a
// reference document as the alias, and it does not enter that alias.
// [Node.At] and [Node.Nodes] thus scope such an alias, and a decode or a
// validator of the scoped Node reads the anchor through the reference
// documents. [Node.Target] returns the Node of the anchor in its reference
// document. A path that enters the alias returns an error that wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrReferenceAlias], and [IsInvalid]
// does not report that error. The error also wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrAlias], and it binds at the alias
// under the path of the alias, as in "app.yaml:1:9: $.server: resolve
// $.server.port: alias reads a reference document: *base". Every read of a
// path that enters the alias returns it, such as At, Node.Nodes,
// [Node.DecodeAt], [Node.DecodeIfPresent], and [Node.Ranges].
//
// A `<<` merge key that reads such an alias brings in the names of the
// mapping of its anchor. A path through a name whose entry the merge key
// brings in returns the same error, because the value lies in the reference
// document. A path through any other name selects the entry that the
// document writes, before or after the merge key, or nothing, as the
// go.jacobcolvin.com/niceyaml/paths package describes. A path also returns
// the error when the reference document merges an anchor that no reference
// document defines. A decode of the Node of the mapping reads the entries
// that the merge key brings in.
//
// A validator that reads a value below such an alias decodes the Node it
// gets, as [Each] hands it, rather than call Node.DecodeAt with a path
// through the alias:
//
//	price := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
//		item, err := n.Decode[Item](ctx)
//		if err != nil {
//			return err
//		}
//
//		if item.Price > 190 {
//			return niceyaml.NewError("too expensive", niceyaml.AtPath(paths.Current().Child("price")))
//		}
//
//		return nil
//	})
//
//	items := paths.Current().Child("items").IndexAll()
//	err := doc.Validate(ctx, niceyaml.Each(items, price))
//
// An Each path that goes below such an alias, such as
// `@.items[*].price`, returns the error before the validator runs on any
// Node. A rule for a value inside each element thus selects the element, as
// the example does, and reads below it in the validator.
//
// A decode of the scoped Node starts from the defaults of its type, as
// Node.Decode does. A decode of the document starts the value at such an
// alias from the defaults of its place. The defaults of a place can differ
// from the defaults of its type, such as for a field that the function of
// [WithDefaults] for its parent sets. A validator that decodes the scoped
// Node thus might see a default that the decode of the document does not
// hold. Inside the anchor, each value that the decoder creates keeps the
// value of the decoder in both decodes, as the Defaults section describes.
//
// A [go.jacobcolvin.com/niceyaml/matcher.Content] or
// [go.jacobcolvin.com/niceyaml/matcher.Text] matcher whose path ends
// at such an alias reads the value through the decode, with the spelling
// and the quotes that the reference document gives it. A
// [go.jacobcolvin.com/niceyaml/matcher.Exists] matcher matches,
// because the path selects the alias. A matcher whose path enters the alias
// returns the error, so a registry does not route that document.
//
// An error whose path enters such an alias binds at the alias, because the
// document does not hold a line for the value. The price validator returns
// such an error for an item that the document writes as an alias:
//
//	app.yaml:2:5: $.items[0].price: too expensive
//
// [SourceError.Nearest] reports the path of that alias, and
// [SourceError.Unresolved] returns the error that wraps ErrReferenceAlias. A
// caller can thus distinguish this position from one at the value itself.
//
// A `<<` merge key that reads a reference document brings in the entries of a
// mapping there. A path through a key of the mapping that holds the merge key
// reads those entries as the decoder reads them. The decoder reads the last
// entry of a key, so an entry after the merge key replaces the entry of its
// key that the merge key brings in. An error at a key that the merge key
// brings in binds at the alias of the merge key. SourceError.Nearest then
// reports the path of the mapping that holds the merge key, and
// SourceError.Unresolved returns the error that wraps ErrReferenceAlias, as it
// does for an alias. An error at any other key binds as it does in a mapping
// without the merge key.
//
// The decoder rejects a value of the anchor with a token of the reference
// document. The decode binds that error at the alias that led the decoder to
// the token, and its path names the value below the alias. It binds as the
// error of the price validator binds, so SourceError.Nearest reports the path
// of the alias:
//
//	app.yaml:2:5: $.items[0].price: expected integer, got string
//
// A `<<` merge key brings in entries from a reference document, or from a
// mapping that it writes in place. For such an entry, the path names the entry
// in the mapping that holds the merge key, as a path through the merge reads
// it. The path does not name the merge key or an index of a sequence there, so
// `services: {<<: {q: *img}}` gives `$.services.q.port`. A merge key that reads
// an anchor of the document brings in the entries where the anchor defines
// them. The error then binds at the alias of the reference document inside the
// anchor, so `base: &b {q: *img}` with `services: {<<: *b}` gives
// `$.base.q.port`. Its path names the place of that alias in the anchor, as
// the Scoped Nodes section describes for an alias inside an anchor.
//
// A later entry of the key can take the path through the merge. Such an entry
// lies in the mapping that holds the merge key, or in a later source of the
// same merge key. The path then goes through the merge key instead, and
// through the index of the source in a sequence of sources. That path binds at
// the alias. `services: {<<: {q: *img}, q: {port: 1}}` thus gives
// `$.services.<<.q.port`, and `services: {<<: [{q: *img}, {q: {port: 1}}]}`
// gives `$.services.<<[0].q.port`.
//
// Where the path through the merge key does not bind at the alias either, the
// error keeps the path through the merge and binds at the position of the
// alias. The error binds at the position of the alias without a path when the
// anchor reaches the value on two or more paths, such as through two aliases
// of one inner anchor.
//
// The decode finds that alias among the aliases of reference documents that
// it reads. When it reads one, the error binds there. When it reads several,
// the error binds at the alias whose anchor reaches the token. When no alias
// reaches the token, the error binds without a position.
//
// Two or more aliases can reach the token, such as two aliases of one anchor.
// The decode then decodes each value that reaches them again, in the order
// that the decoder reads the values. It binds the error at the first value
// that fails the same way. That order takes the fields of a struct in the
// order its type declares them, and the elements of a sequence and the
// entries of a map in document order. When no value fails the same way, the
// error binds without a position.
//
// The decoder of a map returns the error of a `<<` merge key before the error
// of any other entry. So the decode tries the merge keys of a map first. It
// reads the source of the first merge key whose merge fails the same way as
// it reads the map. The decoder reads the mappings of a sequence there as one
// mapping, and the decode reads them the same way. The document does not hold
// the values of a source in a reference document, so the error binds at the
// alias that reads such a source.
//
// The decode departs from the order of the decoder inside a value that
// decodes itself. It follows the fields of the type of that value, which
// might differ from the order its unmarshaler reads them in.
//
// The decoder returns the error of a value that decodes itself, such as a
// [time.Duration], without a token. The decode decodes the value at each
// alias of a reference document again, and binds the error at the first
// alias whose value fails the same way:
//
//	app.yaml:2:4: $.w: time: invalid duration "soon"
//
// When a value inside the anchor reported the error, the decode walks the
// anchor as it walks the document, and the path goes on to that value, as in
// `$.w.timeout`. An `@` path in the error reads from that value. A `<<` merge
// key that reads a reference document stands for each field whose entry it
// brings in. The decode tries such a field where the decoder reads it among
// the fields of its struct, and the error binds as an error at the path of
// the field binds.
//
// A reference document brings its anchors and its own reference documents.
// Its Source parses it once, with its [WithAllowDuplicateKeys],
// [WithByteLimit], [WithDocumentByteLimit], and [WithDepthLimit] options,
// and every Source that reads it shares the parse. A reference document that
// does not parse fails every decode of the Source that reads it, with the
// error of the parse. The
// unmarshalers and the [WithAliasLimit] option of its Source do not reach any
// decode of a Source that reads it.
//
// # Layers
//
// [NewSourceFromLayers] merges the documents of several files, such as a base
// file with the file of one environment over it, into one document. It
// returns a Source of the merged document. The layers go lowest first. Each
// layer resolves its own aliases and `<<` merge keys first, with the
// reference documents and the settings of its own Source. An alias thus reads
// the anchor of its own file, and the merged document does not hold an alias.
// A decode of the merged Source validates the merged document once, so a
// schema that requires a key passes when any layer sets it.
//
// A mapping merges into the mapping below it key by key, at every depth. The
// file of one environment thus changes one field of one service and keeps the
// rest of the base file. A sequence or a scalar replaces what lies below it
// whole, and so does a mapping over a sequence or a scalar. A sequence never
// merges element by element, and an empty sequence, as in `tags: []`, clears
// the one below it. An empty mapping keeps the mapping below it.
//
// A sequence replaces because the file of one environment then states its
// whole list. It can thus shorten or clear the list of the base file. A format
// whose lists grow from layer to layer instead names them with [MergeAppend],
// such as the plugins of a tool or the ports of each service:
//
//	merged, err := niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{base, prod},
//		niceyaml.MergeAppend(
//			paths.Doc().Child("plugins"),
//			paths.Doc().Child("services").ChildAll().Child("ports"),
//		),
//	)
//
// The sequence at each of those paths adds its elements to the sequence below
// it. The merged sequence thus holds the elements of the lowest layer first.
// Each path holds `.name` and `.*` selectors only, because the merge matches
// it against the keys of the mappings that merge.
//
// A layer cannot replace a sequence that appends. An empty sequence there
// keeps the elements below it. A written null still removes the key with
// every element below it, and a sequence of a higher layer then starts the
// sequence again.
//
// An untagged null that a layer writes as `null` or `~` removes its key and
// the value below it, at any depth:
//
//	# base.yaml                # prod.yaml
//	proxy:                     proxy: ~
//	  url: http://proxy:3128   env: {B: null}
//	env: {A: 1, B: 2}
//
// The merged document holds env.A alone. A null of a higher layer keeps the
// key removed, and a higher layer that sets a value there restores the key
// with only that value.
//
// A written null never adds its key. Where the layers below do not hold the
// key, the merged document lacks it too, at any depth of a mapping that the
// layer adds. A null of a higher layer keeps that key out as well, and a
// value of a higher layer sets it. The file of a user thus turns a default
// off with `proxy: ~`, and that line reads the same after the defaults gain
// or lose the key.
//
// The lowest layer that holds a value is the document that the layers above
// it change, so it keeps each null it writes. A merge of one layer thus holds
// what its file holds. A written null of a higher layer removes a null that a
// layer below holds under its key, written or not, as it removes any other
// value. In every layer, a written null stays inside a sequence, because an
// element never merges with an element of a layer below.
//
// A layer without content, such as a file of comments, does not hold a value.
// A layer with content holds one, even where its document is an empty mapping
// or a null, as in a file that holds only `{}` or `~`. A file above such a
// layer thus does not keep its written nulls.
//
// A null that a layer leaves unwritten keeps the value below it. A key that
// does not have anything after it holds such a null, and so does a tag
// without a value. A key whose entries are all commented out thus does not
// change anything. A null at the root of a layer keeps the document below it.
//
// An unwritten null also keeps a key out where a written null of a layer
// below removes the key or does not add it. It stays a null where no layer
// below holds a value or writes a null under its key. A layer above the
// lowest one thus sets a null with `proxy:`, and only there.
//
// Two keys are one key when a path selects them by the same name, as
// [go.jacobcolvin.com/niceyaml/paths.Resolver.KeyName] gives it. The keys 80
// and "80" thus merge, but 80 and 0x50 stay apart. The merged document spells
// a key as the highest layer that holds a value under it does. A key that is
// a sequence or a mapping does not have a name, and the merge fails with
// [ErrUnnamedKey].
//
// The merged Source stands for one layer, which is the layer that [MergeAs]
// names or else the lowest layer. It holds the merged value as block-style
// YAML below the preamble of that layer, so a schema directive in the
// comments of that file names the schema of the merged document. It does not
// keep any other comment. Each scalar keeps the spelling of its layer, such
// as 0x10 or 1.50, except that a block scalar or a string of several lines
// becomes one double-quoted line.
//
// The Source takes its name, its file path, and its file system from the
// Source of that layer. It also takes the unmarshalers of that Source, unless
// a [MergeUnmarshalers] option sets them. Its text holds the values of every
// layer. The Source thus does not show an excerpt when the Source of a layer,
// or of a reference document that a layer reads, has [WithExcerpts] set to
// false.
//
// That text is not a file of the program. [Node.View], [Node.Span],
// [Node.Ranges], and [Node.PathAt] read the merged lines, so a position from
// the file of a layer names whatever lies there in the merged text. Every
// Node of the document has the [Node.FilePath] and [Node.FS] of the layer
// that the Source stands for. [Node.Origin] returns the Node that holds a
// value in the file of its layer.
//
// A program that embeds its defaults below the file of the user passes
// MergeAs with the Source of that file. It gives its unmarshalers, such as
// the cidr [Unmarshaler] that [CustomUnmarshaler] shows, to
// [MergeUnmarshalers]:
//
//	merged, err := niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{defaults, user},
//		niceyaml.MergeAs(user),
//		niceyaml.MergeUnmarshalers(cidr),
//	)
//
// The directive of the file then picks the schema, and a
// [go.jacobcolvin.com/niceyaml/matcher.FilePath] matcher routes on the
// path of the file. The heading of an error names the file rather than the
// defaults. Every decode of the merged Source applies cidr, including the
// decode inside a [Validator].
//
// A merge without MergeUnmarshalers takes the unmarshalers that
// [WithUnmarshalers] gives the Source of the layer that the merged Source
// stands for. When the file that MergeAs names is missing, the merged Source
// stands for the lowest layer instead. Unmarshalers that a program gives to
// that file alone thus apply only when it exists. A program gives its
// unmarshalers to MergeUnmarshalers, or the same unmarshalers to the Source
// of every layer. Every decode of the merged Source then reads the types of
// the program the same way whichever files exist.
//
// A Node that [Node.Origin] returns decodes with the unmarshalers of its
// layer. A program that decodes origins thus gives the same unmarshalers to
// the Source of every layer.
//
// Each error of the merged Source binds in the file of a layer and never in
// the merged text, so it names the line that a user edits:
//
//	base.yaml:3:9: $.server.port: port must be at least 1
//
// An error with a path binds in the highest layer whose document holds a
// value at that path, through the aliases and merge keys of that layer. A
// layer that leaves a null unwritten there does not hold a value. An error at
// a mapping that several layers hold binds in the highest of them. An error
// under a sequence binds in the layer that holds the sequence. An error with
// a position or a range in the merged text binds at the value that lies
// there.
//
// Several layers hold a sequence that [MergeAppend] names, and each element
// keeps the layer that wrote it. An error at an element or under one binds in
// that layer. An error at the sequence itself binds in the highest layer that
// holds the sequence, as an error at a mapping does. An error under an element
// stays in the layer of that element when it binds at a value above the
// element, such as a sequence that decodes itself. It stays there too when it
// binds at the key of such a value.
//
// An error for a path that every layer misses binds at the nearest node that
// a layer holds along the path, such as the key of the mapping that lacks a
// key. It binds in the layer whose node lies deepest along the path, and in
// the highest of several such layers. An error at a
// key that a written null removes or does not add binds at that null, in the
// highest layer that writes one there. An error without a location binds
// without a position in the layer that the Source stands for, as in
// "base.yaml: quota service: connection refused".
//
// The bound error reports the [SourceError.Source], the [SourceError.Node],
// and the [SourceError.Path] that it has in the layer it binds in. A layer
// from [Node.At] holds its value under a path of its own file. An error at
// `$.server.port` thus reads `$.defaults.server.port` in a layer that holds
// the value under defaults. [Annotate] marks the error on a view of that
// layer but not on a view of the merged Source, and [SourceError.Excerpt]
// shows the lines of the layer.
//
// A layer holds each element of a sequence that MergeAppend names at the
// index the element has in its own file. An error at `$.plugins[2]` thus
// reads `$.plugins[0]` in the file whose first plugin it is. [Node.Origin]
// returns the Node at that path of the file.
//
// A merged Source is a layer like any other, and so is a Node of its
// document. A merge that holds one reads it as a file of the same text. That
// text lacks each key that a written null of its own layers removed or did
// not add, but the layer keeps the null. The null thus removes the key that a
// layer below the merged Source holds. The merge binds each error in the file
// that holds the value, so every error and every origin belongs to a document
// that a merge did not build.
//
// The decode fills the Go value from the merged document as the Decoding
// section describes. A default that the value holds thus survives where the
// layers leave a field out or remove it. Defaults that should merge key by
// key go in a layer of their own, such as an embedded file below the others.
//
// A nil layer does not add anything. [NewSourceFromFile] returns a nil Source
// for a file it could not read, so a program passes an optional file as it is
// after it checks the error. MergeAs with a nil Source keeps the lowest
// layer, so an optional file goes to MergeAs as it is too. A merge that skips
// every layer returns an empty Source without a name. A decode of it leaves
// the value as it was, and each error binds without a position, as in
// "$.servers[1].port: port is required".
//
// A layer that a program builds from its environment or its flags merges and
// binds as a file does. It cannot set one element of a sequence. By default,
// the decode ignores a key that the target does not read, and
// [WithUnknownFields] reports such a key. After the layer sets one
// key of a mapping, an error at that mapping binds in that layer, whichever
// layer the fix belongs in. So does an error for a key that the mapping
// lacks.
//
// [go.jacobcolvin.com/niceyaml/encoder.Marshal] writes a nil pointer field of
// a struct as `null`. A layer that a program encodes from a struct thus
// removes the value under each nil pointer whose field lacks the omitempty
// option. It does not set a null where the layers below lack the key.
// [WithExcerpts] set to false keeps the lines of a layer that holds secrets
// out of every excerpt.
//
// # Alias Limit
//
// A document of a few hundred bytes can nest aliases so that a reader that
// follows them reads far more than the document holds. The go-yaml decoder
// can take minutes on such a document and never checks the context, and a
// schema reads every use of every alias. A decode, a
// [go.jacobcolvin.com/niceyaml/schema.Schema], and a
// [go.jacobcolvin.com/niceyaml/matcher.Content] or
// [go.jacobcolvin.com/niceyaml/matcher.Text] matcher therefore refuse
// to read a value that holds an alias when the aliases of its document
// exceed the limit. The error matches [ErrExcessiveAliasing], and
// [IsInvalid] reports it, because the aliases of the document are the
// cause wherever the count runs. The error binds at the alias of the
// document that repeats the most nodes, as in "app.yaml:14:30: aliases
// expand the document past the alias limit". That alias can lie outside
// the node that the call reads.
//
// The value of a Node at a local alias without a tag that lies outside
// the content of its anchor is the content of the anchor. So the check
// weighs that content, not the alias. A decode of such a Node in a
// document past the limit thus passes when that content does not hold an
// alias. The value of a Node at a tagged alias is the tag, which holds the
// alias. The value of a Node at an alias that reads
// a reference document, or at an alias inside the content of its own
// anchor, is the alias itself. A decode of either Node fails in a
// document past the limit.
//
// A path through a mapping whose `<<` merge key reads a reference document
// finds its entry by reading the merge sources of that mapping, as the
// References section describes. A path reads those sources as it reads the
// merge sources of the document, so the count does not stop the read. The
// limit behind [go.jacobcolvin.com/niceyaml/paths.ErrExcessiveMerging]
// bounds it instead. An error at such a path thus binds in a document past
// the limit as it binds with the limit off.
//
// [WithAliasLimit] set to false turns the limit off for the documents of one
// Source, for input that the program trusts, such as a file it ships. It also
// turns off the bounds that the Limits section describes for the text that
// aliases write out. Two checks stay on whatever the option says.
// [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue] takes a Go value
// and does not take a Source. It refuses a value that shares its maps and
// slices past the limit, even one that a Source with the limit off decoded.
// [Node.Nodes] refuses a path whose selectors reach far more nodes through
// aliases than the document holds.
//
// The count reads through each alias that a reference document of
// [WithReferences] resolves. Such an alias counts what the anchor of its
// name holds there, through the aliases inside that anchor. A decode also
// reads each reference anchor that its aliases reach once. That read counts
// as the definition of an anchor counts in its own document, and it holds
// each anchor inside that anchor. A document thus gets the verdict that it
// gets with those anchors defined inside it. Defaults that a few aliases or
// `<<` merge keys share pass as they do in one file. An upload that repeats
// a large reference anchor many times fails, and the error binds at an
// alias of the upload:
//
//	upload.yaml:1:5: aliases expand the document past the alias limit
//
// The reference documents can define one anchor name more than once. The
// count then takes the largest anchor that a decode can read for the name,
// as the References section describes those anchors. A document whose
// decode reads a smaller anchor of the name can thus fail, even with one
// alias. One file that defines the same anchors counts the anchor that its
// decode reads, so that file can pass. Give each reference anchor a name
// of its own to avoid that refusal.
//
// Reference anchors that reach each other by name in a loop count as past
// the limit, so a document whose aliases reach the loop fails. An anchor
// that holds an alias of its own name, as in "m: &m {k: *m}", is a loop of
// one name. A decode into an any value takes that alias as null, but a
// decode into a Go type follows it and might not end. The count thus
// refuses a document whose aliases reach such an anchor, whatever reads the
// document. One file that defines the anchor reads the alias as null in
// every decode, so that file can pass.
//
// A loop counts when it closes through any anchor that a decode can read
// for a name. An anchor inside another anchor is such an anchor even when a
// later anchor shares its name, as the References section describes. A
// decode reads that inner anchor only after it reads the outer anchor, but
// the count refuses every document whose aliases reach the loop.
//
// [WithAliasLimit] on the Source that reads the reference documents decides
// whether the count runs. The option on a reference Source does not change
// anything.
//
// [NewSourceFromLayers] expands each alias of a layer in full, so a layer
// must pass the limit as a decode into a type that reads text does. The
// document byte limit and the depth limit of the Source of each layer bound
// that text too,
// as the Limits section describes.
//
// # Limits
//
// The go-yaml parser takes time and memory that grow faster than the text of
// one document, so every [Source] bounds what it parses. By default, a Source
// reads at most 256 KiB from a file or a reader and parses at most 256 KiB of
// text. Each of its documents holds at most 256 KiB of text and nests at most
// 100 levels. [WithByteLimit], [WithDocumentByteLimit], and [WithDepthLimit]
// set other limits, and [NoLimit] turns the check off. [WithoutLimits] turns
// each check off, and a later option of one limit sets that limit again.
//
// The byte limit bounds the whole text, and the document byte limit bounds
// each document. The cost of a parse grows faster than the text of one
// document but in proportion to the number of documents. A program that
// reads a stream of many small documents, such as the output of a Helm
// render, thus raises the byte limit and keeps the document byte limit. A
// program that reads one large document raises both.
//
// A file or a reader past the byte limit fails as a file that does not read.
// A Source of a text that the program holds, as [NewSourceFromString] takes
// one, keeps a text past the byte limit with its tokens and its lines. A
// program thus renders, diffs, and searches such a text as it does any
// other. The parser does not read the text. The whole text is one document
// that fails as a document that does not parse, however many documents it
// holds.
//
// A document past the document byte limit fails as a document that does not
// parse, at its first token that is not a comment. A document past the depth
// limit fails the same way, at the token that opens its first level past the
// limit. The other documents of the Source still parse. Each error of a limit
// matches [ErrLimit] and not [ErrSyntax], and [IsInvalid] reports it.
//
// The go-yaml decoder writes the node of each value that reads text out as
// text, with each alias in full. Types that read text include those with an
// UnmarshalText method or an UnmarshalYAML method that takes bytes, and those
// that a function of [CustomUnmarshaler] decodes. A target reads text when
// any type that a decode into it reaches reads text, such as one field of
// type [netip.Addr]. [NewSourceFromLayers] writes each layer out the same
// way. The aliases of a small document can make that text far longer and
// deeper than the document, so the limits of the Source bound what the
// aliases add:
//
//   - The text that the aliases of the document repeat stays within the
//     document byte limit. The count weighs the text of each alias in two
//     ways and takes the longer. One way writes the content of the alias in
//     block style, with each entry and each element on a line indented to
//     its level in the document. The other way copies the content as the
//     document spells it, comments and spaces included, and indents each
//     line to the line of the alias.
//   - Every alias keeps the nesting of the document within the depth limit.
//     An alias nests the levels around it and the levels that its content
//     nests, through the aliases there.
//
// An alias that a reference document of [WithReferences] resolves repeats
// the text of its anchor there and nests the levels of that anchor. Both
// counts follow the aliases inside the anchor. Those aliases are not
// aliases of the document, so their text counts only where an alias of the
// document repeats the anchor. A document can thus fit the document byte
// limit where one file that also defines the anchors does not.
//
// Such a decode or merge of a value that holds an alias checks the whole
// document before it writes any text. The check does not follow which values
// read text. It counts every alias of the document, including an alias that a
// plain string field reads. It fails on a document past either bound with an
// error that matches both [ErrLimit] and [ErrExcessiveAliasing], as in
// "app.yaml:30:5: aliases repeat more than 256 KiB of text". The error binds
// at the first alias that takes the text past the bound. That alias might not
// reach any value that reads text.
//
// A decode into a value that does not read text does not write any text, so
// these bounds do not apply to it. The alias limit still applies.
//
// A [go.jacobcolvin.com/niceyaml/matcher.Content] matcher that decodes
// into a type that reads text applies the same bounds. [WithAliasLimit] set
// to false turns both bounds off with the alias limit.
// [WithDocumentByteLimit] or [WithDepthLimit] set to [NoLimit] turns off the
// bound that its limit sets.
//
// The merged text of NewSourceFromLayers skips both byte limits and the depth
// limit. It holds the content of its layers in block style, plus at most what
// the bounds let their aliases add. The text that a function of
// CustomUnmarshaler decodes skips the limits too, because it holds at most
// its node and what the bounds let the aliases there add.
//
// # Diffs
//
// [go.jacobcolvin.com/niceyaml/diff.Differ] computes line differences with an
// [go.jacobcolvin.com/niceyaml/diff/lcs.Algorithm]. The default,
// [go.jacobcolvin.com/niceyaml/diff/lcs.Hirschberg], is space-efficient for
// large files:
//
//	result, err := diff.Diff(ctx, original.Lines(), modified.Lines())
//	if err != nil {
//		return err
//	}
//
//	p := printer.New()
//	lipgloss.Println(p.Print(result.Unified()))
//	lipgloss.Println(p.Print(result.Hunks(3)))
//
// [go.jacobcolvin.com/niceyaml/diff.New] creates a reusable
// [go.jacobcolvin.com/niceyaml/diff.Differ] from options.
// [go.jacobcolvin.com/niceyaml/diff.WithLineKey] sets the key that the diff
// compares in place of the content of each line. A key that leaves out
// padding, comments, or quotes makes the diff ignore them. The diff shows
// each line as its revision wrote it:
//
//	d := diff.New(diff.WithLineKey(func(l *line.Line) string {
//		return strings.TrimRight(l.Content(), " \t")
//	}))
//	result, err := d.Diff(ctx, before.Lines(), after.Lines())
//
// Custom algorithms implement
// [go.jacobcolvin.com/niceyaml/diff/lcs.Algorithm], and
// [go.jacobcolvin.com/niceyaml/diff.WithAlgorithm] sets one. The algorithm
// aligns the lines by their keys.
//
// Inputs that share many lines in a different order take time close to the
// square of their length, even inside the byte limit of a Source. The
// context that a diff takes bounds that time, as the Cancellation section
// describes.
//
// [Source.Lines] is every line of the file. [Node.View] holds the lines that
// one document or node covers. A diff reads a view as it reads lines, so a
// diff of one document of a file that holds several compares only that
// document:
//
//	result, err := diff.Diff(ctx, before[1].View(), after[1].View())
//
// The gutter and the hunk headers of the diff show the line numbers of the
// file.
//
// Diff output is a [line.View] rather than a [Source], because the
// interleaved lines do not form a YAML document. It uses [line.Flag] to mark
// inserted and deleted lines and [line.Annotation] for the hunk headers of a
// unified diff.
//
// # Text Search
//
// [go.jacobcolvin.com/niceyaml/finder.Finder] locates strings within tokens
// and returns [position.Range] values for [line.View.AddOverlay] and
// [line.View.BlendOverlay].
//
// A search folds case and ignores diacritics by default. Options such as
// [go.jacobcolvin.com/niceyaml/finder.WithDiacriticFold] turn each built-in
// fold on or off, [go.jacobcolvin.com/niceyaml/finder.Exact] turns them all
// off, and [go.jacobcolvin.com/niceyaml/finder.WithFold] adds a fold of the
// caller's own:
//
//	idx := finder.New().Load(source.Lines())
//	view := source.View()
//	view.AddOverlay(kind.GenericHighlight, idx.Find("search term")...)
//	lipgloss.Println(p.Print(view))
//
// A Finder loads a [line.View] too and searches the lines it holds. The
// ranges are in the coordinates of the file, so a search of one document
// marks the view of that document:
//
//	view := doc.View()
//	view.AddOverlay(kind.GenericHighlight, finder.New().Load(view).Find("search term")...)
//
// # Dependencies
//
// niceyaml is a facade over go-yaml. The exported API names go-yaml types
// only where niceyaml wraps the document model rather than hiding it. Those
// types are the [*ast.File] and [*ast.DocumentNode] that a [Source] parses
// into, the [token.Tokens] it lexes, and the [ast.Node] and [*token.Token]
// that resolving a [paths.Path] returns. They also include the mapping,
// entry, sequence, anchor, and alias nodes that the lookups of a
// [paths.Resolver] return or name in a [paths.AliasError] or a
// [paths.Match]. Positions, ranges, lines, errors, and styles are
// niceyaml's own types, and [paths.Path.YAMLPath] converts to go-yaml's
// path type when a caller needs it.
//
// The error for text that the go-yaml parser rejects matches [ErrSyntax], and
// the error for YAML that parsed but does not decode matches [ErrDecode]. The
// errors for a value of the wrong kind, a number out of range, and an unknown
// field each match a sentinel of their own beside ErrDecode, as
// [ErrUnknownField] describes. A caller thus distinguishes these cases
// without naming go-yaml's error types. The package returns a panic in
// go-yaml, or in an unmarshaler that go-yaml calls, as a [PanicError] that
// does not match any of those sentinels.
//
// The go-yaml settings that niceyaml supports have named options, such as
// [WithAllowDuplicateKeys], [WithUnmarshaler], or
// [go.jacobcolvin.com/niceyaml/encoder.WithIndent]. A parse and a decode take
// only named options, so niceyaml knows what each one changes and applies it
// to every decoder that a call runs. An option or a method that takes or
// yields a go-yaml value carries a YAML prefix, so a caller can see at the
// call site where the go-yaml dependency shows. [Node.YAMLComments] returns
// go-yaml's comment map,
// [go.jacobcolvin.com/niceyaml/encoder.WithYAMLComments] takes it, and a
// decode under [WithYAMLOrderedMaps] yields its ordered map. The encoder
// takes only named options too, because it rewrites the output of go-yaml
// and a go-yaml option that it cannot inspect can undo that work.
//
// A test in this package checks every exported declaration. It fails when a
// declaration names a go-yaml type outside the allowlist of the test. It also
// fails when a declaration names the comment map and its identifier lacks
// the YAML prefix.
//
// The [go.jacobcolvin.com/niceyaml/schema] package follows the same rule for
// the JSON Schema library it builds on.
// [go.jacobcolvin.com/niceyaml/schema.Compile] takes a schema document as
// bytes, and the options that pass the values of that library through carry a
// JSONSchema prefix, as in
// [go.jacobcolvin.com/niceyaml/schema.WithJSONSchemaOptions]. The same test
// enforces that rule.
//
// Rendering builds on lipgloss, and the [go.jacobcolvin.com/niceyaml/style]
// package exposes the lipgloss Style type directly because a theme is a set
// of lipgloss styles. The kinds of text that a rendering names, such as
// [kind.GenericError], live in [go.jacobcolvin.com/niceyaml/style/kind]. That
// package holds only names, so the [line],
// [go.jacobcolvin.com/niceyaml/diff], and
// [go.jacobcolvin.com/niceyaml/finder] packages mark content without
// depending on lipgloss. The [go.jacobcolvin.com/niceyaml/fangs] and
// [go.jacobcolvin.com/niceyaml/bubbles/yamlviewport] packages are adapters
// for the charm libraries they build on, and they expose the types of those
// libraries. Each is a module of its own, so bubbletea, fang, and cobra stay
// out of the dependencies of this module.
//
// Of the public packages of this module, only
// [go.jacobcolvin.com/niceyaml/printer],
// [go.jacobcolvin.com/niceyaml/style], and
// [go.jacobcolvin.com/niceyaml/style/theme] link lipgloss and the other
// charm libraries. A program that loads, decodes, or edits YAML without
// rendering it does not link them. A test of this module fails when
// another public package starts to link one.
package niceyaml
