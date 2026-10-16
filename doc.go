// Package niceyaml provides utilities for working with YAML documents.
// It builds on [yaml] and [charm.land/lipgloss/v2].
//
// Rather than using multiple distinct styling systems, niceyaml styles the
// YAML tokens directly, a single time.
//
// It also provides an alternative implementation of go-yaml's source
// annotations for errors, as well as adapters for use with JSON schema
// validation.
//
// # Usage
//
// Parse YAML into a [Source], then use a [go.jacobcolvin.com/niceyaml/printer.Printer]
// to render it with syntax highlighting:
//
//	source := niceyaml.NewSourceFromString(yamlContent)
//	p := printer.New()
//	lipgloss.Println(p.Print(source.View()))
//
// [NewSourceFromFile] reads a file from disk, [NewSourceFromFS] one from
// an [fs.FS] such as an [embed.FS], and [NewSourceFromReader] any
// [io.Reader], such as standard input.
//
// Errors come back bound to the source, so they show users exactly where
// the problem is:
//
//	file, err := source.File()
//	if err != nil {
//		// FormatError prints the message and a plain-text excerpt of
//		// the YAML with the problematic location marked and two lines
//		// of context. err.Error() is the message and position alone.
//		log.Print(niceyaml.FormatError(err))
//	}
//
// # Architecture
//
// The module separates a YAML document from its rendering, and each part
// lives in a package of its own.
//
// [Source], in this package, is the file. It owns the tokens from go-yaml,
// lazily parses them into an AST with [Source.File], returns the root
// [Node] of each YAML document in the file from [Source.Documents], and
// binds the errors it and its Nodes produce to itself. [Node.Bind] binds
// errors built elsewhere to that document, and [Source.Bind] binds one to
// the document its location falls in. The Source keeps the text it read.
// [Source.Text] returns that text byte for byte, and [Source.Offset]
// converts a position in its lines to a byte offset in the text, for a
// program that edits the file in place.
//
// [line.Lines] is the content, the tokens organized into lines, and it never changes.
// [line.View] is one rendering of that content. It shares the lines and carries the
// decoration of the rendering. Annotations hold error messages and diff headers, flags
// mark inserted and deleted lines, and overlays apply style spans for highlighting. A
// Source hands out its lines from [Source.Lines] and a fresh view over them from
// [Source.View]. The decoration a caller adds to one view reaches neither the Source
// nor another view, and taking a view copies no content. [Node.View] is the same view
// sliced to the lines of one document, with the line numbers they have in the file.
// Every index and range a view takes is in the coordinates of its lines, and a slice
// keeps them. The ranges a [go.jacobcolvin.com/niceyaml/finder.Finder] or
// [Node.Ranges] returns therefore apply to a view of the whole source and to a slice
// of it alike.
//
// A view need not be a YAML document. Diffs, for example, interleave lines
// from two revisions and are plain [line.View] values.
//
// [go.jacobcolvin.com/niceyaml/printer.Printer] renders a [line.View] with syntax
// highlighting via lipgloss. It supports customizable gutters (line numbers, diff
// markers), word wrapping, and annotation rendering.
// [go.jacobcolvin.com/niceyaml/diff.Differ] compares two [line.Sequence] values, each
// a [line.Lines] or a [line.View], and [go.jacobcolvin.com/niceyaml/finder.Finder]
// searches one.
//
// Themes from [go.jacobcolvin.com/niceyaml/style/theme] provide color palettes. Without
// one, [go.jacobcolvin.com/niceyaml/printer.Printer] renders with
// [go.jacobcolvin.com/niceyaml/style.Default].
//
// # Lines
//
// YAML tokens can span multiple lines, as block scalars and multiline
// strings do, while diffing, printing, and searching are much simpler with
// line-by-line access. [line.NewLines] splits multiline tokens at line
// boundaries into one part per line and keeps a reference to the original
// token each part came from, and [line.Lines.Tokens] reverses the split.
// A [Source] does this on creation, hands out the result from
// [Source.Lines], and hands out a view to decorate from [Source.View]:
//
//	view := source.View()
//	view.AddOverlay(kind.GenericError, errorRange)
//	view.BlendOverlay(kind.GenericHighlight, matches...)
//	lipgloss.Println(p.Print(view))
//
// The lines hold every token the module hands out, from [Source.Tokens],
// [line.Lines.TokenAt], [line.Line.Tokens], or [line.Line.Token]. Treat
// them as read-only and call [token.Token.Clone] before modifying one. A
// copy of a token, such as one from the [*ast.File] that [Source.File]
// parses, matches the original by its type, value, origin, and position,
// so a token taken from a node finds its lines the same way. The [line]
// package documents the view and its metadata in full.
//
// # Documents
//
// A [Source] holds one YAML file, a stream of text with one or more
// documents in it. Its constructor lexes the text, and the Source parses
// the tokens the first time a caller asks for the tree or a document,
// through [Source.File], [Source.Documents], or any method that reads
// them. Every later call returns the same tree, the same Nodes, and the
// same errors.
//
// The comments and the %YAML or %TAG directives above a "---" header,
// and the comments after a "..." marker, belong to the document below
// them, or to the last document when none follows. [Node.Preamble]
// returns the ones above the content of a document. A file that opens
// with a license header thus holds one document.
//
// Each document parses on its own, so a YAML syntax error fails the
// document that holds it and no other. The [Node] of a document that did
// not parse keeps its place among the documents of the file. [Node.Err]
// returns its syntax error, and so do the methods Node.Err lists, such
// as [Node.Decode] and [Node.Validate]. [Node.AST] returns nil for
// it, and [Node.View] renders its lines as it renders those of any
// document. A "---" header that directly follows an anchor with no value
// parses together with the document above it, so a syntax error in
// either fails both, and both return that one error. [Source.File]
// returns the syntax errors of the file as one error, and every one of
// them matches [ErrSyntax].
//
// A document with no content is empty, as [Node.IsEmpty] reports. A
// trailing "---" leaves one, as in `name: x\n---\n`, and so does a Helm
// template that renders to a comment alone or a "---" header with
// comments alone below it. In a file that holds a document with content,
// [Source.Documents] leaves out every empty document, and so do
// [Source.Document], [Source.Decode], [Source.ValidateDocuments], and the
// path resolution of [Source.Bind]. Kubernetes manifests and the output
// of a Helm chart thus decode and validate as they are.
// [Source.AllDocuments] returns every document, and [Node.DocumentIndex]
// counts every document, so a document below an empty one has an index
// larger than its place among the documents Source.Documents returns.
//
// A file with no content holds one empty document as far as
// Source.Documents goes. That is the case for an empty file, a file of
// whitespace or comments alone, a stream of "..." markers alone, and a
// file of empty documents alone, where it is the first of them. The YAML
// spec finds no document in a stream of markers alone, and niceyaml
// departs from it so that such a file reads as an empty file does. The
// empty document decodes to the zero value, and a schema that wants a
// mapping rejects it. [SkipEmpty] wraps a validator so that it passes an
// empty document, for a caller whose file may be empty as a whole.
//
// # Errors
//
// An [*Error] is what a check knows about a problem: a message, a
// location, and whether the document is at fault, with no source. A
// validator builds one without holding the document. Binding it through a
// [Node] or a [Source] resolves the location against the text and
// produces a [*SourceError], whose message puts the position and the path
// in front of the text, as "cfg.yaml:4:10: $.license: no such file".
// [Error.Error] returns the message alone, so a wrapper such as
// [fmt.Errorf] may add context anywhere between the check and the
// binding, and the bound line keeps that context behind the path.
//
// Each error plays one of three roles. A problem is one thing wrong, such
// as an Error from [NewError]. A detail from [WithDetails] explains a
// problem, as its reasons or a related location do, and is never a
// problem of its own. A heading stands over several problems, as a join
// from [errors.Join], a summary from [NewSummary], or a wrapper around
// either does. Every reader keeps to these roles. [SourceError.Error]
// prints one line per problem, [Problems] yields one node per problem,
// and [errors.Is] and [errors.As] match problems and pass over details.
// [Rebase] and a scoped [Node.Bind] give a location to each problem that
// carries none and to no heading or detail:
//
//	conflict := niceyaml.NewError("port 80 conflicts", niceyaml.AtPath(portB),
//		niceyaml.WithDetails(niceyaml.NewError("first declared here", niceyaml.AtPath(portA))))
//
//	return niceyaml.NewSummary(fmt.Sprintf("%d violations", len(errs)), errs...)
//
// A location is a path from [AtPath], a position from [AtPosition], a
// range from [AtRange], or a path with a position or a range. A path
// names the value the error is about, and a position or a range names the
// characters at fault. An Error with both binds at the characters and
// names the path in its message without resolving it. A path starts at
// one of two points, as in RFC 9535 JSONPath. A `$` path reads from the
// root of the document, and an `@` path reads from the [Node] that binds
// the error, so a check written for a type writes `@` paths from the
// value it checked. The binding puts the path of that Node in front, so
// every path a [SourceError] reports starts at `$` and names the same
// value through any Node of the document.
//
// A heading with a location, such as a join that [Invalid] locates or a
// summary that [Error.With] locates, gives that location to each problem
// below it that carries none. A problem with a location of its own keeps
// it, and a detail gains none. A heading below it with no location takes
// the location and gives it to the problems below it, and the nearest
// heading with a location wins. A [Rebase] between the heading and a
// problem points the problem at its base instead, so whichever of the two
// lies nearer the problem gives it its location.
//
// A join has no line of its own, so the location of a join shows only on
// the lines of the problems that take it:
//
//	return niceyaml.Invalid(errors.Join(sizeErr, keysErr), niceyaml.AtPath(licensePath))
//	// cfg.yaml:4:10: $.license: file is too large
//	// cfg.yaml:4:10: $.license: too many keys
//
// When every problem of the join carries a location of its own, the
// location of the join shows nowhere.
//
// The code that builds an error declares whether the document is at fault
// for it or the check could not run, and a location never decides it.
// [NewError] and [Invalid] declare a fault. [Unchecked] takes the same
// options, shows an I/O error at the value a check read, and declares that
// the check could not run. [Locate] takes the same options, adds the
// location to an error, and declares nothing, so the fault stays with that
// error. The document is at fault for each problem the parse
// reports with [ErrSyntax] and a decode reports with [ErrDecode]. Any
// other error declares nothing, and a wrapper is a fault only when the
// error it wraps is one. The error of a context that ended and a
// [PanicError] are never faults:
//
//	niceyaml.NewError("license file does not exist", niceyaml.AtPath(p)) // fault
//	niceyaml.Invalid(checkErr, niceyaml.AtPath(p))                       // fault
//	niceyaml.Unchecked(statErr, niceyaml.AtPath(p))                      // check could not run
//	niceyaml.Locate(nameErr, niceyaml.AtPath(p))                         // what nameErr declares
//
// A hook is code of the caller that a decode runs on one value. Each
// [SelfValidator], UnmarshalYAML or UnmarshalText method,
// [CustomUnmarshaler] function, and [WithValueCheck] function is a hook.
// The decode presumes the document at fault for an error a hook returns
// that declares nothing, a plain I/O error included. Such an error
// from an unmarshaler matches ErrDecode too. A check thus keeps its
// faults when it moves from one hook to another. A [Validator] checks a
// node before the decode and is not a hook. It declares its own faults, so
// a plain error it returns is not a fault.
//
// An Error from Unchecked on the cause chain of the error that a hook
// returns turns the presumption off. An I/O error that a hook wraps in
// Unchecked is thus no fault and does not match ErrDecode. An Error from
// Locate leaves the presumption on. An Unchecked around a join turns the
// presumption off for each branch it heads. An Unchecked in one branch of
// a join, or of a wrapper with several %w verbs that heads several
// problems, turns it off for that branch alone. A declared fault still
// holds anywhere on the chain, so NewError inside Unchecked and Unchecked
// inside Invalid are faults.
//
// A wrapper that [fmt.Errorf] builds with several %w verbs heads the
// branches that are problems, as a join does. It reads each other branch
// as a sentinel, whose text the wrapper shows and which is no problem of
// its own. A branch is a problem when it carries a location or errors
// below it. So is a branch that holds an Error that declares a fault,
// declares that its check could not run, or marks a severity. Such Errors
// come from calls such as NewError, [NewWarning], Invalid, Unchecked, and
// [AsErrors]. A wrapper with one such branch is that problem, so a
// sentinel in front of a warning leaves one warning:
//
//	return fmt.Errorf("%w: %w", ErrLegacy, niceyaml.NewWarning("legacy layout"))
//
// No reader can tell a plain error, such as one from [errors.New], from a
// sentinel, so a plain error beside a problem reads as a sentinel too.
// Return separate problems with [errors.Join], or build each with
// NewError, Invalid, or Unchecked. Invalid declares nothing for the error
// of a context that ended, so an Error from Invalid with no location
// around such an error reads as a sentinel as well. An Error from Locate
// with no location and no details reads as the error it wraps does, so
// one around a plain error reads as a sentinel too.
//
// [IsInvalid] reports whether the document is at fault for every problem
// of an error, the question a caller asks to pick a status code or an
// exit code. [ErrorTree.IsInvalid] asks the same of one row of a report.
// One problem that is no fault makes IsInvalid report false for the whole
// error, and a heading or a detail decides nothing. A [PanicError] is a
// bug in code. A document can still trip one in go-yaml, so a public
// endpoint that answers such a document with a 4xx status tests for the
// PanicError before it asks IsInvalid.
//
// A plain I/O error beside violations in one wrapper with several %w
// verbs reads as a sentinel, so IsInvalid does not count it. IsInvalid
// counts it in a join of the two, and in the wrapper when Unchecked wraps
// the I/O error. A wrapper that is one problem declares no fault when a
// branch holds the error of a context that ended. Invalid declares no
// fault for that error either.
//
// A check run on a value inside a document reports `@` paths that read
// from that value. [Rebase] puts the path of the value in front of each
// `@` path in the tree of an error, details included, and points each
// problem that carries no location at the value itself. A heading with a
// location inside the rebased error gives that location to the problems
// below it instead. A `$` path, a position, and a range stay as they are,
// and rebases compose. A decode rebases what every nested [SelfValidator]
// and every function from WithValueCheck returns. A [Node] from [Node.At]
// or [Node.Nodes] binds an error as a Rebase under its own path does, so a
// check bound through the Node of its value does not need a Rebase.
// A path names a map key as the document spells it, so a base built from
// a decoded key can miss, as the key 0x10 of a map[int]T that decodes to
// 16 does. A [DataLocator] finds an entry by its decoded key.
//
// Binding resolves each location once. A path the document leaves out,
// as a check for a required field names, binds at the key of the mapping
// that lacks the value. A path that enters an alias the document cannot
// follow, such as one to an anchor in a reference document, binds at the
// alias. An error that a [SelfValidator] or a function from WithValueCheck
// returns below a value that decodes itself binds at that value when its
// path selects nothing. [SourceError.Nearest] reports the node in each
// case. Any other location that does not resolve costs the binding its
// position, and [SourceError.Unresolved] returns the reason, such as
// [ErrOutOfRange] for a line the source does not hold or
// [ErrPathNeedsDocument] for a path with no single document to resolve in.
// Such a binding names its source in front of the message, and
// [FormatError] prints the reason in place of its excerpt.
//
// An error that is or wraps a [*SourceError], with no [*Error] above it
// that carries a location, heads errors, or holds details, is bound
// already, and every Bind and [Rebase] return it as it is. The exception
// is the error [BindValue] returns for a value that came from no
// document, such as the body of a request. Its text names each path from
// that value, as "$.port: 0 is less than 1", and it stands in no
// document, so a document can still place it. Rebase and every Bind read
// it as the error it was made from, and a decode places it at the value
// whose Validate method, [Validator], or function from WithValueCheck
// returned it. A wrapper around it keeps its own text, behind the position
// and the path. Placing builds a new error and leaves the result as it is:
//
//	err := engine.ValidateValue(cfg.Request)
//
//	return doc.Bind(niceyaml.Rebase(err, paths.Doc().Child("request")))
//
// # Error Presentation
//
// [FormatError] prints an error as plain text for a log. The message
// comes first, as a tree with a connector in front of each error below
// another, and an excerpt of each source follows, with context lines
// around each location and carets under the offending columns. It looks
// through the wrappers and joins around every [SourceError], and its
// output holds no escape sequences:
//
//	log.Print(niceyaml.FormatError(err))
//
// A logger formats an error itself. The %+v verb of an [*Error] or a
// [*SourceError] prints what FormatError prints, and [Error.LogValue] and
// [SourceError.LogValue] give a [log/slog] handler the tree without the
// excerpt. Both show [ErrorListLimit] problems and count the rest, so an
// error with thousands of problems reaches a log as a short text, where
// FormatError renders every one.
//
// An excerpt shows lines of the source to whoever reads the log. A
// program that loads a text with secrets in it, such as the values of its
// environment, creates that [Source] with [WithExcerpts] set to false.
// Every renderer then prints the position, the path, and the message of
// an error bound there, and no line of the text. A line longer than
// [DefaultExcerptWidth] columns shows a window around each location, so a
// document minified onto one line adds a row of bounded length.
// [WithContextLines] and [WithExcerptWidth] set both for each renderer.
//
// A report a program reads, such as JSON lines, CI annotations, or editor
// diagnostics, walks [Problems], which yields one node of the [ErrorTree]
// per problem with its details below it. A row takes its fields from the
// node through [ErrorTree.Message], [ErrorTree.Path], [ErrorTree.Name],
// and [ErrorTree.Position], rather than from its label, and [errors.As]
// on the Err of the node finds the typed error of that problem.
//
// A terminal gets color from
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError], which takes
// the same options:
//
//	p := printer.New(printer.WithWrapWidth(width))
//	lipgloss.Fprintln(os.Stderr, p.PrintError(err, niceyaml.WithContextLines(3)))
//
// Both renderers draw the [ErrorReport] that [NewErrorReport] builds,
// which holds the tree, the excerpt of each source, and the bindings that
// get a reason in place of an excerpt. A renderer of its own, such as one
// that writes HTML, draws the same report.
//
// A bound error keeps its [Source] alive, with every line, token, and
// parse of the file, and so does its report. A program that keeps errors
// past the files they came from passes each error through [Detach], as a
// check that prints the errors of every file at the end does. The
// detached error keeps its messages, its locations, and a copy of the
// lines its excerpts show, and it renders as the error did:
//
//	warnings := niceyaml.NewWarnings()
//	if err := source.ValidateDocuments(warnings.Context(ctx), reg); err != nil {
//		errs = append(errs, niceyaml.Detach(warnings.Report(err)))
//	}
//
// The program detaches the whole error of a file once, after
// [Warnings.Report] adds the warnings of the collector. The bindings of
// one source that separate calls of Detach detached render as bindings of
// separate sources, each under an excerpt of its own. A collector keeps
// the Source of each warning it holds alive until the program drops the
// collector.
//
// The marks of an error decorate a [line.View], so this package knows
// nothing of the printer. [Annotate] marks every binding of an error on a
// view that holds lines of its source, such as the whole source, one
// document of it, or a diff against another revision. [line.View.Hunks]
// then keeps the marked lines with context around each:
//
//	view := source.View()
//	niceyaml.Annotate(err, view)
//	lipgloss.Println(p.Print(view.Hunks(2)))
//
// [SourceError.Excerpt] returns those hunks for one binding. [Node.Ranges]
// returns the ranges an error at a path would highlight, and
// [Node.PathAt] goes from a position to the path of the node there, for a
// viewer that names the value under its cursor.
//
// # Warnings
//
// A problem is either an error or a warning, and a warning does not make a
// check fail. [NewWarning] takes the options of [NewError], so a warning
// carries a location and details and binds to a [Source] as an error
// does. The nearest severity mark on the cause chain of a problem decides
// its severity. NewWarning and [Node.Warn] mark a warning, [AsErrors] marks
// an error above each warning, and a problem with no mark is an error.
// [Invalid], [Unchecked], [Locate], [Rebase], [Node.Bind], and
// [fmt.Errorf] add no mark, so a warning stays a warning inside them. A
// warning among the details of an error leaves that error an error.
//
// Every reader keeps the two apart. [HasErrors] reports whether an error
// holds an error, [IsInvalid] decides from the errors and passes over the
// warnings, and each node of an [ErrorTree] reports its severity in
// Warning. [errors.Is] and [errors.As] match a warning as they match an
// error. Each renderer writes "warning:" after the position of a warning,
// where compilers put it, and a heading that niceyaml writes counts each
// severity, as in "cafe.yaml: 2 errors, 1 warning":
//
//	cafe.yaml: 2 errors, 1 warning
//	|-- 2:1: $.curency~: unknown field "curency"
//	|-- 3:1: warning: $.legacyPricing~: deprecated field "legacyPricing"
//	`-- 4:1: $.wifi~: unknown field "wifi"
//
// A [Validator] reports each warning with Node.Warn and returns its
// failures alone, and a Validate method of a [SelfValidator] returns its
// warnings beside its errors. [Node.Validate], [Source.ValidateDocuments],
// the Decode methods of Node and Source, both SelfValidate methods, and
// [SelfValidateValue] collect the warnings of the checks they run. Each
// such call hands every warning to the [Warnings] collector on its
// context, whether the call passes or fails. Without a collector on the
// context, the call drops its warnings. The call returns its errors
// alone, so a call that returns nil passed, and [errors.Is] on its error
// matches no warning.
//
// A failure that holds no warning comes back as the check wrote it. In a
// result that holds both, the call removes the warnings from the joins
// and from the wrappers of one %w verb that [fmt.Errorf] builds, and keeps
// those joins and wrappers. Any other error in the result that holds
// both, such as a summary from [NewSummary], becomes the join of its
// errors. That join drops the heading and the type of the error, so
// [errors.As] does not find that type.
//
// A program that prints only the error of a call shows none of its
// warnings. A renderer that lists the warnings between the errors, as a
// compiler does, asks [Warnings.Report] for one error that holds both:
//
//	warnings := niceyaml.NewWarnings()
//
//	config, err := doc.Decode[Config](warnings.Context(ctx), niceyaml.WithValidator(reg))
//	if err != nil {
//		return warnings.Report(err)
//	}
//
// Wherever niceyaml reads a result to decide whether a check passed, a
// result whose every problem is a warning counts as a pass. The
// self-validation walk thus runs the Validate method of a value when the
// values below it gave only warnings, and [ChainValidator] goes on to the
// next validator. The decoder step reports each problem as an error,
// whatever it wraps, so a value that decodes itself warns from its
// Validate method rather than from its unmarshaler.
//
// A wrapper that [fmt.Errorf] builds with several %w verbs heads a
// warning and an Error from [NewError] beside it as two problems, as the
// Errors section describes. A plain error beside a warning in such a
// wrapper reads as a sentinel, so the wrapper is one warning, and a check
// that returns it passes. Join the plain error and the warning with
// [errors.Join], or build the error with NewError, to keep it an error.
//
// A program in strict mode turns each warning into an error with
// AsErrors, applied to the report of the call:
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
// [Source.Decode] decodes a file that holds one document, [Source.Document]
// returns the root [Node] of that document, and [Source.Documents] returns
// the root of each document of a file that holds several:
//
//	for _, doc := range source.Documents() {
//		config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//		if err != nil {
//			return err
//		}
//	}
//
// The Node of a document that did not parse returns its syntax error from
// [Node.Decode], so the loop above meets it beside the errors of the
// documents that parsed. [Source.ValidateDocuments] validates every document
// and joins what they return. The Documents section describes syntax errors
// and empty documents.
//
// [Node.Decode] returns a new value, and [Node.DecodeInto] fills one the
// caller holds. A decode runs these steps in order, and the first that fails
// ends it:
//
//  1. The target must be a non-nil pointer, or the decode returns an error
//     wrapping [ErrDecodeTarget].
//  2. A document that did not parse returns its syntax error.
//  3. Each [Validator] from [WithValidator] checks the node, in the order
//     given.
//  4. A node that holds an alias fails with an error matching
//     [ErrExcessiveAliasing] when nested aliases would make a decode of
//     the document read far more than the document holds. A decode into a
//     type that reads text also fails when the aliases of the document
//     repeat more text than the byte limit of the [Source] allows. It
//     fails as well when they nest the document past its depth limit, as
//     the Limits section describes.
//  5. The go-yaml decoder fills the target.
//  6. Every value in the result that implements [SelfValidator] validates
//     itself, and each function from [WithValueCheck] checks the values of
//     its type, unless [WithSelfValidation] turns that off.
//
// A field the document leaves out keeps its value. A mapping merges into a
// struct field by field at every depth, and the document replaces a slice,
// an array, a map, or an interface value whole. A null leaves the target as
// it is, unless the target is a pointer or an interface, which a null with
// no tag sets to nil. A null under a key leaves a struct field as it is
// too, unless the field is a pointer. The Defaults section describes the
// value that each value a decode creates starts from.
//
// A [DecodeOption] sets how one decode runs, and it reaches the call that
// gets it and no other. A setting that describes the documents, such as
// [WithReferences] or [WithAliasLimit], belongs to the [Source], which
// applies it to every decode and validation of them. An alias reads the
// anchor of its name defined last before it, inside the decoded node or
// outside it, and an alias with no such anchor reads one of a reference
// document.
//
// The [Unmarshaler] values from [CustomUnmarshaler] and [JSONUnmarshalers]
// set how the types of the program read YAML, so a program gives them to
// the Source with [WithUnmarshalers]. Every decode of the documents then
// applies them, wherever it runs. That covers a decode inside a
// [Validator], the decode function a CustomUnmarshaler function gets, and
// the map keys [Node.SelfValidate] spells. It also covers a decode in
// code that did not build the Source, such as a library the program hands
// it to. The unmarshalers of [WithUnmarshaler] in a call apply over them,
// so a call can replace or remove the function of a type and turn the
// JSON methods off. A program gives the unmarshalers of a Source from
// [NewSourceFromLayers] to [MergeUnmarshalers], as the Layers section
// describes.
//
// A value the decoder rejects returns an error that matches [ErrDecode] and
// carries the `$` path of the value, and the message describes the document
// rather than the Go target:
//
//	config.yaml:3:11: $.servers[0].port: expected integer, got string
//
// The kinds a message names are mapping, sequence, string, integer, float,
// boolean, null, timestamp, binary for a value under a !!binary tag, and
// duration for a [time.Duration]. A number outside the range of its type
// reads "expected integer from 0 to 65535, got 70000". The rejection of a
// key, such as an unknown field, carries a path that ends in `~`. A value of
// the wrong kind matches [ErrWrongKind], a number out of range
// [ErrOverflow], and an unknown field [ErrUnknownField], each beside
// ErrDecode. The path names where the document writes the value, so a value
// that an alias brings in reports the path of its anchor.
//
// The decoder stops at the first value it rejects. The decode then looks for
// the other problems of the document and reports them together, in the order
// of the source:
//
//	app.yaml: 3 problems
//	app.yaml:1:1: $.replcas~: unknown field "replcas"
//	app.yaml:2:10: $.timeout: expected integer, got string
//	app.yaml:4:11: $.servers[0].port: expected integer, got string
//
// The error is a summary from [NewSummary] that heads one error per problem,
// which [ErrorTree.Problems] yields. A document with one problem returns
// that problem alone. The search adds values of the wrong kind, numbers out
// of range, durations that do not parse, scalars that an UnmarshalText
// method rejects, and unknown fields under [WithDisallowUnknownFields], and
// the decoder confirms each one. Apart from those scalars and a [time.Time],
// the search reads nothing at or below a value that decodes itself, so a
// type with an UnmarshalYAML method reports one bad value per decode. No
// value validates itself until the decoder accepts the document. The search
// calls an UnmarshalText method again on a new value, so the method must
// answer from its text alone.
//
// The decoder returns the error of a value that decodes itself with no token
// of the source. Such a value has an unmarshaler method the decoder calls, a
// function from [CustomUnmarshaler], or the type [time.Duration]. The
// decode decodes each such value again, in the order the decoder reads them,
// and binds the error at the first that fails the same way, or at the
// deepest value inside it that also fails that way:
//
//	config.yaml:7:14: $.servers[1].timeout: time: invalid duration "soon"
//
// An `@` path in an [*Error] from an unmarshaler reads from its own value.
// The error stays in the chain beside ErrDecode, unless it holds an Error
// from [Unchecked], as the Errors section describes. An unmarshaler that
// answers otherwise the second time can move the error to another value, and
// an error that no value reproduces comes back with no location.
//
// The decode recovers a panic in the go-yaml decoder and in the unmarshalers
// it calls. The panic comes back as an error that holds a [PanicError],
// bound at the first token of the node, which does not match ErrDecode and
// which [IsInvalid] does not report. Nothing recovers a panic in a Validator,
// in a Validate method, or in a function from [WithValueCheck].
//
// A Source from [NewSourceFromLayers] runs these steps once on the merged
// document, as the Layers section describes.
//
// # Defaults
//
// [Node.Decode] starts from the value that [Defaults] returns, and
// [Node.DecodeInto] starts from the value the caller passes. A decode also
// creates values: each element of a slice or an array, each value of a
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
// The defaults of a type start from its zero value, with each struct field
// built from the defaults of its own type, and then the function of the
// type runs. A parent thus wins where its function writes. With the
// options above, each Printer defaults to port 9100, and the kitchen
// printer of a CafeSpec to port 9101.
//
// A value the decode creates fills as the Decoding section describes, from
// its defaults. A null element or map value holds the defaults of its type,
// and a null sets a pointer to nil. The target of a pointer that already
// points to a value, such as one that a function of WithDefaults sets,
// starts from that value. The value it pointed to stays as it was. A value
// that decodes itself, through an unmarshaler method or a
// [CustomUnmarshaler] function, starts from its defaults too. Its
// method decides what to keep.
//
// The value at an alias starts from the defaults of its own place. The
// places that aliases of one anchor fill share one value when they start
// from equal values, as the go-yaml decoder shares it. Two pointer fields
// with equal defaults thus share one target. A place that starts from
// another value builds a value of its own. An interface value at an alias
// holds the value of its anchor.
//
// Each key of a map keeps the value the decoder gives it, and so does a
// value behind an alias to a reference document. A panic in a function of
// WithDefaults makes the decode return an error that holds a [PanicError].
//
// # Validation
//
// A [Validator] checks the node before it decodes, as a JSON schema does. A
// [SelfValidator] checks a decoded value after, and so does a function from
// [WithValueCheck]. [Node.Validate] runs a Validator without decoding. A
// [go.jacobcolvin.com/niceyaml/schema.Schema] checks one JSON schema, and a
// [go.jacobcolvin.com/niceyaml/schema.Registry] picks the schema for each
// document. [MultiValidator] runs several validators and reports every
// failure, which suits independent rules. [ChainValidator] stops at the
// first that fails, which suits a check that needs an earlier one to pass,
// and a decode runs repeated WithValidator options that way:
//
//	err := source.ValidateDocuments(ctx, niceyaml.MultiValidator(schema, names))
//
// A validator writes its errors from the Node it gets, so an `@` path reads
// from that Node. The decode or Node.Validate that runs it binds the result
// as [Node.Bind] binds an error, and leaves an error the validator bound
// itself as it is. A direct call of [Validator.Check] returns the error as
// the validator wrote it. A validator that calls Check on an element and
// returns the result thus reports it at the wrong value. A validator that
// runs another on a Node of its choosing calls Validate on that Node, so
// the error binds there. [Each] runs a validator on every Node a path
// selects, such as each element of a list, and binds each failure at the
// Node that its validator checked:
//
//	items := paths.Current().Child("items").IndexAll()
//	err := source.ValidateDocuments(ctx, niceyaml.MultiValidator(schema, niceyaml.Each(items, reserved)))
//
// A check of the decoded values of one type goes to the decode with
// [WithValueCheck]. It suits a check that needs the context of the call,
// and a check of a type of another package, which the program cannot give
// a Validate method. The decode runs the function on every value of the
// type in the result, in the step where the values validate themselves:
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
// [Node.Decode], and a decode passes none of its options to the validators
// it runs. The decode of the validator still applies the unmarshalers of
// [WithUnmarshalers], which belong to the Source. A check that runs after
// Decode returns, such as one that runs its I/O in parallel, locates each
// finding with [Node.DataLocator]. The decoder respells some keys, so the
// key 0x10 sets the member 16. A DataLocator reads the names of decoded
// data as the decoder does, so a check of the data reports a finding where
// a schema reports a violation of the same value.
//
// A validator declares which of its errors are the fault of the document
// by the constructor it calls, as the Errors section describes. [NewError]
// and [Invalid] declare a fault. A plain error declares none, and neither
// does an error from [Unchecked]. A schema reports a violation as a fault
// and a schema that does not load as none, so [IsInvalid] tells the two
// apart. A decode presumes the document at fault for an error from a
// Validate method or a function from WithValueCheck that declares nothing.
// A check thus keeps its faults when it moves from one to the other. Such
// a check wraps an I/O error in Unchecked, so IsInvalid does not report
// it.
//
// A decode calls Validate on every value in the result that implements
// SelfValidator, and then the functions from WithValueCheck for the type of
// the value, in the order given. The values below a value validate first,
// and the value validates only when no value below it failed. A Validate
// method or a function whose result holds warnings alone passes, as the
// Warnings section describes. The functions for a value run only once its
// Validate passes, and the first that fails stops the rest. A Validate that
// an embedded field promotes runs once, on that field. A Validate that an
// UnmarshalYAML already called runs again in the decode, so it must be
// idempotent.
//
// Validate and each function write `@` paths, which read from the value,
// and the decode puts them under the path of the value in the document. An
// Hours under spec that reports @.close thus reports $.spec.hours.close. The
// path names a field by its yaml tag, its json tag, or its lowercased name,
// an element by its index, and a map entry by its key as the document
// spells it. A map key validates at the path of its entry with a `~` after
// it, and a field an inline tag flattens keeps the path of its struct. A
// struct field the document leaves out validates too, with the value the
// decode left in it, such as its zero value. A field that go-yaml never
// decodes, such as one tagged `yaml:"-"`, does not validate.
//
// A value that decodes itself validates as any other, and so does every
// value below it. Its fields need not mirror the document, so an error below
// it whose path the document does not hold binds at the nearest such value,
// which [SourceError.Nearest] names. Every error below a slice, an array, or
// a map that decodes itself binds at that value. Neither a Validate nor a
// function from WithValueCheck holds a Node, so a check that needs one, such
// as a check against a registry, runs after Decode returns and binds its
// result with Node.Bind. A check of map entries belongs in a Validate or a
// WithValueCheck function of the type of the entry, which the decode puts
// under the key as the document spells it.
//
// [Node.SelfValidate] runs the last step on its own, for a program whose
// library writes its environment or its flags into the value. The program
// decodes with [WithSelfValidation] off, lets the library fill the value,
// and then calls SelfValidate. SelfValidate runs only the functions of
// WithValueCheck it gets, so the program passes them to SelfValidate as
// well. No Validator sees a value set this way. The walk follows the value,
// and each error binds where its path resolves in the document. An error
// under a field the document lacks binds at the key of the mapping that
// lacks it, and an error under an element the document lacks binds with no
// position. [Source.SelfValidate] runs the step through the one document of
// a Source, and an empty Source stands in for an optional file that does
// not exist.
//
// A check on a value that came from no document, such as the body of a
// request, has no line to report. [SelfValidateValue] runs the Validate of
// every value below such a value, and the functions of WithValueCheck it
// gets, and binds the result with [BindValue], so the text names each path,
// as in "$.close: closes before it opens". A caller that later learns where
// the value stands places the error there with [Rebase] and Node.Bind.
//
// # Scoped Nodes
//
// [Node.At] returns a Node scoped to the node a path selects, and
// [Node.Nodes] returns one for each node a path with wildcards selects. An
// `@` path resolves from the receiver and a `$` path from the root of the
// document, so a path from [Node.Path] scopes the same node through any Node
// of the document.
//
// The decode steps run on a scoped Node as they run on a document. Decode
// reads the one value, and a validator given to it checks that node. An `@`
// path in an error from the validator, the decoder, or a SelfValidator
// resolves from the node, and the bound error carries it as a `$` path from
// the root. A check written for a type thus reports the same lines whether
// the type is the whole document or a value inside one:
//
//	hours, err := doc.At(paths.Doc().Child("spec", "hours"))
//	if err != nil {
//		return err
//	}
//
//	h, err := hours.Decode[Hours](ctx, niceyaml.WithValidator(hoursSchema))
//
// [Node.Bind] binds the result of a check the caller runs on the value, with
// its `@` paths read from the node. A scoped Node binds a problem with no
// location at its value, as an error with [AtPath] of [paths.Current] binds.
// A summary or a join only heads its problems, and each binds on its own.
// A problem below a heading with a location binds at that location instead,
// read from the node, and a detail gains no location. The root of a
// document leaves a problem with no location without one, because such an
// error can be about the whole document. A location declares nothing, so
// [IsInvalid] reports a bound error only when it reports the error the Node
// bound.
//
// [Node.Document] returns the root of the document. A validator that can
// check only a whole document, as a
// [go.jacobcolvin.com/niceyaml/schema.Registry] can, refuses a scoped Node,
// so a caller validates at the root and decodes the nodes below it without
// that validator. A value that an alias or a `<<` merge key brings in
// reports the path where its anchor defines it, which can lie outside the
// scope.
//
// [Node.DecodeAt] scopes and decodes a value the document must hold, and
// [Node.DecodeIfPresent] one it may leave out, into a variable that holds
// the default. Both drop the Node, so an `@` path in a check the caller runs
// on the value afterward resolves from the receiver. A caller that binds
// such errors scopes the Node with At, or moves them under the path with
// [Rebase] first.
//
// A path that selects nothing returns an error that wraps
// [paths.ErrNotFound], which IsInvalid reports. For a key a mapping leaves
// out, the error binds at the key of that mapping, as in "cfg.yaml:3:1:
// $.server.name: not found", and any other such error has no location. In a
// document with no content, the error also wraps [paths.ErrNoDocument]. A
// decode can return an error that matches paths.ErrNotFound for a node that
// is present, so only the error of At tells that a value is absent.
//
// # Cancellation
//
// The context of a call reaches every [Validator] the call runs and every
// function from [CustomUnmarshaler] and [WithValueCheck]. A check that
// does remote work, such as resolving a schema reference, thus stops when
// the caller cancels the context.
//
// The go-yaml decoder never checks the context. A decode checks it before
// the decoder starts and between the anchors it registers, and no deadline
// stops the decoder once it runs. To decode a type that reads its own text,
// such as one with an UnmarshalText method, the decoder reads every token of
// the file for each value, so a long list of such values can take a second
// or more.
//
// An ended context stops each step that loops:
//
//   - The search for the other problems of a rejected decode stops, and
//     the decode returns the rejection alone.
//   - The self-validation walk stops. It returns the error of the context
//     in place of the errors it found, and hands the warnings it found to
//     the collector of the context. A Validate method or a function from
//     WithValueCheck that returns the error of a context that ended stops
//     the walk the same way.
//   - [MultiValidator] does not run another validator, and returns the
//     error of the context, or the error of the validator that saw it end.
//   - [Each] does not run its validator on another Node, and returns the
//     error of the context, or the error its validator returned on the
//     Node where the context ended.
//   - [NewSourceFromLayers] returns the error of the context.
//
// The error of a context that ended matches [context.Canceled] or
// [context.DeadlineExceeded], and not [ErrDecode]. A binding gives it no
// location, even through a scoped Node, so its message names at most the
// source, as in "cfg.yaml: context canceled". [Invalid] declares no fault
// for it, and [IsInvalid] does not report it.
//
// # References
//
// A document whose aliases name the anchors of another file, such as a
// file of shared defaults, reads that file as a reference document
// through [WithReferences] on its Source. Every decode and every
// validation of the document reads the anchors of its reference
// documents, so a schema reads `server: *base` as the decode does. A
// program reads each reference file into a Source of its own, with
// [NewSourceFromFile], [NewSourceFromFS], or [NewSourceFromReader]. One
// that keeps them in a directory picks the files and their order, which
// decides the anchor that wins a name two of them define:
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
// These calls stand in for the ReferenceFiles, ReferenceDirs,
// RecursiveDir, and ReferenceReaders options of go-yaml, which a decode
// does not take.
//
// An alias reads an anchor of the reference documents only when no
// anchor of its name comes before it in its own document. Where the
// reference documents define one anchor name more than once, the last
// of them in the text wins, even an anchor inside another anchor of that
// name. The reference documents go in the order given, so an anchor of a
// later one overrides an anchor of the same name in an earlier one. A
// Source among them puts its own reference documents ahead of its own
// text, so its own anchors override theirs.
//
// An alias inside a reference document reads the anchors of the
// reference documents alone, whatever anchors the document that reads it
// defines. It reads the last anchor of its name by the same rule, even
// one after it or in a later reference document.
//
// An anchor inside another anchor breaks the rule when a later anchor
// shares its name. A decode that reads the outer anchor registers the
// inner one again. An alias that the decode reads after the outer anchor
// might then read the inner anchor, but [NewSourceFromLayers] reads the
// last. A typed decode and a decode into an any value, the decode a
// schema runs, might also read different anchors. Each anchor of the
// reference documents thus needs a name of its own.
//
// A decode reads the anchors of the reference documents that its aliases
// reach, and every anchor that an alias inside those anchors reaches.
// Its cost thus follows those anchors rather than the size of the
// reference documents, however many documents read them.
//
// A path resolves in the document itself and stops at an alias that
// reads a reference document. [Node.At] and [Node.Nodes] return an error
// wrapping [go.jacobcolvin.com/niceyaml/paths.ErrAlias] for a path that
// reaches such an alias, and [Node.Ranges] returns one for a path that
// goes through it. [IsInvalid] does not report that error. A
// [go.jacobcolvin.com/niceyaml/schema/matcher.Content],
// [go.jacobcolvin.com/niceyaml/schema/matcher.Text], or
// [go.jacobcolvin.com/niceyaml/schema/matcher.Exists] matcher whose path
// reaches the alias returns it too, so a registry routes that document
// nowhere.
//
// An error about a value of a reference document binds at the alias its
// path enters, since the document holds no line for the value:
//
//	app.yaml:2:9: $.server.port: port must be at least 1
//
// [SourceError.Nearest] reports the path of that alias, and
// [SourceError.Unresolved] returns the error wrapping ErrAlias, so a
// caller tells the position from one at the value itself. An error at a
// key of a mapping that merges a reference document under a `<<` key
// binds with no position.
//
// A reference document brings its anchors and its own reference
// documents. Its Source parses it once, with its [WithAllowDuplicateKeys],
// [WithByteLimit], and [WithDepthLimit] options, and every Source that
// reads it shares the parse. A reference document that does not parse
// fails every decode of the Source that reads it, with the error of the
// parse. The unmarshalers and the [WithAliasLimit] option of its Source
// reach no decode of a Source that reads it.
//
// # Layers
//
// [NewSourceFromLayers] merges the documents of several files into one,
// such as a base file with the file of one environment over it, and
// returns a Source of the merged document. The layers go lowest first.
// Each layer resolves its own aliases and `<<` merge keys first, with the
// reference documents and the settings of its own Source, so an alias
// reads the anchor of its own file and the merged document holds no
// alias. A decode of the merged Source validates the merged document
// once, so a schema that requires a key passes when any layer sets it.
//
// A mapping merges into the mapping below it key by key, at every depth,
// so the file of one environment changes one field of one service and
// keeps the rest of the base file. A sequence or a scalar replaces what
// lies below it whole, and so does a mapping over a sequence or a scalar.
// A sequence never merges element by element, and an empty sequence, as
// in `tags: []`, clears the one below it. An empty mapping keeps the
// mapping below it.
//
// A null written as `null` or `~` with no tag removes its key and the
// value below it, at any depth:
//
//	# base.yaml                # prod.yaml
//	proxy:                     proxy: ~
//	  url: http://proxy:3128   env: {B: null}
//	env: {A: 1, B: 2}
//
// The merged document holds env.A alone. A null of a higher layer keeps
// the key removed, and a higher layer that sets a value there brings the
// key back with that value alone. A written null over no value stays a
// null. A null that a layer leaves unwritten, as a key with nothing after
// it or a tag with no value holds, keeps the value below it, so a key
// whose entries are all commented out changes nothing. A null at the root
// of a layer keeps the document below it.
//
// Two keys are one key when a path selects them by the same name, as
// [go.jacobcolvin.com/niceyaml/paths.Resolver.KeyName] gives it. The keys
// 80 and "80" thus merge, and 80 and 0x50 stay apart. The merged document
// spells a key as the highest layer that holds a value under it does. A
// key that is a sequence or a mapping has no name, and the merge fails
// with [ErrUnnamedKey].
//
// The merged Source stands for one layer, the one [MergeAs] names or else
// the lowest. It holds the merged value as block-style YAML below the
// preamble of that layer, so a schema directive in the comments of that
// file names the schema of the merged document. It keeps no other
// comment. Each scalar keeps the spelling of its layer, such as 0x10 or
// 1.50, except that a block scalar or a string of several lines becomes
// one double-quoted line. The Source takes its name, its file path, and
// its file system from the Source of that layer. It also takes the
// unmarshalers of that Source when no [MergeUnmarshalers] sets them. Its
// text holds the values of every layer, so it shows no excerpt when the
// Source of a layer, or of a reference document a layer reads, has
// [WithExcerpts] set to false.
//
// That text is no file of the program. [Node.View], [Node.Span],
// [Node.Ranges], and [Node.PathAt] read the merged lines, so a position
// taken from the file of a layer names whatever lies there in the merged
// text. Every Node of the document has the [Node.FilePath] and [Node.FS]
// of the layer the Source stands for, and [Node.Origin] returns the Node
// that holds a value in the file of its layer.
//
// A program that embeds its defaults below the file of the user passes
// MergeAs with the Source of that file. It gives its unmarshalers, such
// as the cidr [Unmarshaler] that [CustomUnmarshaler] shows, to
// [MergeUnmarshalers]:
//
//	merged, err := niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{defaults, user},
//		niceyaml.MergeAs(user),
//		niceyaml.MergeUnmarshalers(cidr),
//	)
//
// The directive of the file then picks the schema, and a
// [go.jacobcolvin.com/niceyaml/schema/matcher.FilePath] matcher routes on
// the path of the file. The heading of an error names the file and not
// the defaults. Every decode of the merged Source applies cidr, the
// decode inside a [Validator] included.
//
// A merge with no MergeUnmarshalers takes the unmarshalers that
// [WithUnmarshalers] gives the Source of the layer the merged Source
// stands for. When the file MergeAs names is missing, the merged Source
// stands for the lowest layer instead. So unmarshalers given to that file
// alone apply only when it exists. A program gives its unmarshalers to
// MergeUnmarshalers, or the same unmarshalers to the Source of every
// layer. Every decode of the merged Source then reads the types of the
// program the same way whichever files exist.
//
// A Node that [Node.Origin] returns decodes with the unmarshalers of its
// layer. So a program that decodes origins gives the same unmarshalers to
// the Source of every layer.
//
// Each error of the merged Source binds in the file of a layer and never
// in the merged text, so it names the line a user edits:
//
//	base.yaml:3:9: $.server.port: port must be at least 1
//
// An error with a path binds in the highest layer whose document holds a
// value at that path, through the aliases and merge keys of that layer,
// and a layer that leaves a null unwritten there holds no value. An error
// at a mapping that several layers hold binds in the highest of them. An
// error under a sequence binds in the layer that holds the sequence. An
// error with a position or a range in the merged text binds at the value
// that lies there. An error for a key that no layer holds binds at the
// key of the mapping that lacks it, in the layer whose mapping lies
// deepest along the path, and in the highest of several such layers. An
// error at a key that a layer removed binds at the null that removed it,
// in the highest layer that writes one there. An error with no location
// binds with no position in the layer the Source stands for, as in
// "base.yaml: quota service: connection refused".
//
// The bound error reports the [SourceError.Source], the
// [SourceError.Node], and the [SourceError.Path] it has in the layer it
// binds in. A layer from [Node.At] holds its value under a path of its
// own file, so an error at `$.server.port` reads `$.defaults.server.port`
// in a layer that holds the value under defaults. [Annotate] marks the
// error on a view of that layer and nothing on the view of the merged
// Source, and [SourceError.Excerpt] shows the lines of the layer.
//
// A merged Source is a layer like any other, and so is a Node of its
// document. A merge that holds one reads it as a file of the same text
// and keeps each key its layers removed. It binds each error in the file
// that holds the value, so every error and every origin belongs to a
// document that no merge built.
//
// The decode fills the Go value from the merged document as the Decoding
// section describes, so a default the value holds survives where the
// layers leave a field out or remove it. Defaults that should merge key
// by key go in a layer of their own, such as an embedded file below the
// others.
//
// A nil layer adds nothing, and [NewSourceFromFile] returns a nil Source
// for a file it could not read, so a program passes an optional file as
// it is once it has checked the error. MergeAs with a nil Source keeps
// the lowest layer, so an optional file goes to MergeAs as it is too. A
// merge of no layer gives an empty Source with no name. A decode of it
// leaves the value as it was, and each error binds with no position, as
// in "$.servers[1].port: port is required".
//
// A layer that a program builds from its environment or its flags merges
// and binds as a file does. It cannot set one element of a sequence. A
// key that no field of the target reads sets nothing, and
// [WithDisallowUnknownFields] reports it. Once the layer sets one key of a
// mapping, an error at that mapping, or for a key the mapping lacks,
// binds in that layer, whichever layer the fix belongs in.
// [go.jacobcolvin.com/niceyaml/encoder.Marshal] writes a nil pointer field
// of a struct as `null`, so a layer encoded from a struct removes the
// value under each nil pointer whose field lacks the omitempty option.
// [WithExcerpts] set to false keeps the lines of a layer that holds
// secrets out of every excerpt.
//
// # Alias Limit
//
// A document of a few hundred bytes can nest aliases so that a reader
// that follows them reads far more than the document holds. The go-yaml
// decoder can take minutes on such a document and never checks the
// context, and a schema reads every use of every alias. A decode, a
// [go.jacobcolvin.com/niceyaml/schema.Schema], and a
// [go.jacobcolvin.com/niceyaml/schema/matcher.Content] or
// [go.jacobcolvin.com/niceyaml/schema/matcher.Text] matcher therefore
// refuse a node that holds an alias when the aliases of its document go
// past the limit. The error matches [ErrExcessiveAliasing], and
// [IsInvalid] reports it, because the aliases of the document are the
// cause wherever the count runs.
//
// [WithAliasLimit] set to false turns the limit off for the documents of
// one Source, for input the program trusts, such as a file it ships. It
// also turns off the bounds that the Limits section describes for the
// text that aliases write out. Two checks stay on whatever the option
// says.
// [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue] takes a Go
// value and no Source, and refuses a value that shares its maps and
// slices past the limit, even one that a Source with the limit off
// decoded. [Node.Nodes] refuses a path whose selectors reach far more
// nodes through aliases than the document holds.
//
// The count reads the documents of a Source and no reference document of
// [WithReferences], and the option on a reference Source changes nothing.
// A decode reads in full each anchor of the reference documents that its
// aliases reach. Nested `<<` merge keys there thus cost the decode what
// they expand to, with the limit on or off. A program passes trusted
// files alone as reference documents.
// [NewSourceFromLayers] writes each alias of a layer out in full, so a
// layer must pass the limit as a decode into a type that reads text does.
// The byte limit and the depth limit of the Source of each layer bound
// that text too, as the Limits section describes.
//
// # Limits
//
// The go-yaml parser takes time and memory that grow faster than the text
// of one document, so every [Source] bounds what it parses. A Source holds
// at most 256 KiB of text, and each of its documents nests at most 100
// levels, unless [WithByteLimit] or [WithDepthLimit] sets another limit,
// where 0 turns the check off. A file past the byte limit fails as a file
// that does not read, and a document past the depth limit fails as a
// document that does not parse, at the token that opens its first level
// past the limit. The other documents of the Source still parse. Each
// such error matches [ErrLimit], and [IsInvalid] reports it.
//
// A decode into a type that reads text writes the node out as text with
// each alias in full. Types that read text include those with an
// UnmarshalText method or an UnmarshalYAML method that takes bytes, and
// those that a function of [CustomUnmarshaler] decodes.
// [NewSourceFromLayers] writes each layer out the same way. The aliases of
// a small document can make that text far longer and deeper than the
// document, so the limits of the Source bound what the aliases add:
//
//   - The aliases of the document repeat no more text than the byte limit
//     allows. The count weighs the text of each alias in two ways and
//     takes the longer. One writes the content of the alias in block
//     style, with each entry and each element on a line indented to its
//     level in the document. The other copies the content as the document
//     spells it, comments and spaces included, and indents each line to
//     the line of the alias.
//   - No alias nests the document more levels than the depth limit
//     allows. An alias nests the levels around it and the levels its
//     content nests, through the aliases there.
//
// Such a decode or merge of a node that holds an alias checks the whole
// document before it writes any text. It fails on a document past either
// bound with an error that matches both [ErrLimit] and
// [ErrExcessiveAliasing], as in "app.yaml:30:5: aliases repeat more than
// 256 KiB of text". The error binds at the first alias that takes the text
// past the bound. A decode into a value that reads no text shares each
// anchor between its aliases, so neither bound applies to it.
//
// A [go.jacobcolvin.com/niceyaml/schema/matcher.Content] matcher that
// decodes into a type that reads text applies the same bounds.
// [WithAliasLimit] set to false turns both bounds off with the alias
// limit. [WithByteLimit] or [WithDepthLimit] set to 0 turns off the bound
// that its limit sets.
//
// The merged text of NewSourceFromLayers skips the byte limit and the
// depth limit. It holds the content of its layers, written in block
// style, and no more than the bounds let their aliases add. The text that
// a function of CustomUnmarshaler decodes skips the limits too, because
// it holds no more than its node and what the bounds let the aliases
// there add.
//
// # Diffs
//
// [go.jacobcolvin.com/niceyaml/diff.Differ] computes line differences using an
// [go.jacobcolvin.com/niceyaml/diff/lcs.Algorithm]. The default,
// [go.jacobcolvin.com/niceyaml/diff/lcs.Hirschberg], is space-efficient for large
// files:
//
//	result := diff.Diff(original.Lines(), modified.Lines())
//	p := printer.New()
//	lipgloss.Println(p.Print(result.Unified()))
//	lipgloss.Println(p.Print(result.Hunks(3)))
//
// Custom algorithms implement [go.jacobcolvin.com/niceyaml/diff/lcs.Algorithm]. For a
// reusable [go.jacobcolvin.com/niceyaml/diff.Differ]:
//
//	d := diff.New(diff.WithAlgorithm(myAlgo))
//	result := d.Diff(before.Lines(), after.Lines())
//
// [Source.Lines] is every line of the file. [Node.View] holds the lines one
// document or node covers, and a diff reads a view as it reads lines, so a
// diff of one document of a file that holds several compares that document
// alone:
//
//	result := diff.Diff(before[1].View(), after[1].View())
//
// Its gutter and hunk headers show the line numbers of the file.
//
// Diff output is a [line.View] rather than a [Source], since the
// interleaved lines do not form a YAML document. It uses [line.Flag] to mark
// inserted/deleted lines and [line.Annotation] for unified diff hunk headers.
//
// # Text Search
//
// [go.jacobcolvin.com/niceyaml/finder.Finder] locates strings within tokens and
// returns [position.Range] values suitable for [line.View.AddOverlay] and
// [line.View.BlendOverlay].
//
// A search folds case and ignores diacritics by default, through
// [go.jacobcolvin.com/niceyaml/normalizer.New], and
// [go.jacobcolvin.com/niceyaml/finder.WithNormalizer] sets a normalizer
// of the caller's own, or none for exact matching:
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
// only where niceyaml wraps the document model rather than hiding it: the
// [*ast.File] and [*ast.DocumentNode] a [Source] parses into, the
// [token.Tokens] it lexes, and [ast.Node] and [*token.Token] as results of
// resolving a [paths.Path]. Positions, ranges, lines, errors, and styles are
// niceyaml's own types, and [paths.Path.YAMLPath] converts to go-yaml's
// path type when a caller needs it. Text the go-yaml parser rejects
// matches [ErrSyntax], and YAML that parsed and does not decode matches
// [ErrDecode]. A value of the wrong kind, a number outside the range of
// its type, and an unknown field each match a sentinel of their own
// beside ErrDecode, as [ErrUnknownField] describes. A caller thus tells
// these cases apart without naming
// go-yaml's error types. A panic in go-yaml, or in an unmarshaler it
// calls, comes back as a [PanicError] that matches none of them.
//
// The go-yaml settings niceyaml supports have named options, such as
// [WithAllowDuplicateKeys], [WithUnmarshaler], or
// [go.jacobcolvin.com/niceyaml/encoder.WithIndent]. A parse and a decode
// take named options alone, so niceyaml knows what each one changes and
// applies it to every decoder a call runs. An option or a method that
// takes or yields a go-yaml value carries a YAML prefix, so a caller can
// tell at the call site when the go-yaml dependency shows.
// [Node.YAMLComments] returns go-yaml's comment map,
// [go.jacobcolvin.com/niceyaml/encoder.WithYAMLComments] takes it, and a
// decode under [WithYAMLOrderedMaps] yields its ordered map. The encoder
// settings with no option of their own pass through
// [go.jacobcolvin.com/niceyaml/encoder.WithYAMLOptions], which takes
// go-yaml's own options. A test in this package checks every
// exported declaration. It fails when a declaration names a go-yaml type
// outside the test's allowlist, or names the encoder option type or the
// comment map in an identifier without the YAML prefix.
//
// The [go.jacobcolvin.com/niceyaml/schema] package follows the same rule
// for the JSON Schema library it builds on:
// [go.jacobcolvin.com/niceyaml/schema.Compile] takes a schema document as
// bytes, and the options that pass that library's values through carry a
// JSONSchema prefix, as in
// [go.jacobcolvin.com/niceyaml/schema.WithJSONSchemaOptions]. The same
// test enforces it.
//
// Rendering builds on lipgloss, and the [go.jacobcolvin.com/niceyaml/style] package
// exposes its Style type directly since a theme is a set of lipgloss styles. The kinds
// of text a rendering names, such as [kind.GenericError], live in
// [go.jacobcolvin.com/niceyaml/style/kind], which holds names alone, so the [line],
// [go.jacobcolvin.com/niceyaml/diff], and [go.jacobcolvin.com/niceyaml/finder] packages
// mark content without depending on lipgloss. The [go.jacobcolvin.com/niceyaml/fangs]
// and [go.jacobcolvin.com/niceyaml/bubbles/yamlviewport] packages are adapters for the
// charm libraries they build on and expose those libraries' types. Each is a module of
// its own, so bubbletea, fang, and cobra stay out of this module's dependencies.
package niceyaml
