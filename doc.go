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
// The code that builds an error declares whether the document is at fault
// for it or the check could not run, and a location never decides it.
// [NewError] and [Invalid] declare a fault. [Place] takes the same
// options and declares nothing, so it shows an I/O error at the value a
// check read. The document is at fault for each problem the parse reports
// with [ErrSyntax] and a decode reports with [ErrDecode], and for every
// error a [SelfValidator] returns. Any other error declares nothing, and
// a wrapper is a fault only when the error it wraps is one. The error of
// a context that ended and a [PanicError] are never faults:
//
//	niceyaml.NewError("license file does not exist", niceyaml.AtPath(p)) // fault
//	niceyaml.Invalid(checkErr, niceyaml.AtPath(p))                       // fault
//	niceyaml.Place(statErr, niceyaml.AtPath(p))                          // check could not run
//
// [IsInvalid] reports whether the document is at fault for every problem
// of an error, which picks a status code or an exit code, and
// [ErrorTree.IsInvalid] asks the same of one row of a report. One problem
// that is no fault makes the whole error not invalid, and a heading or a
// detail decides nothing. A wrapper that [fmt.Errorf] builds with several
// %w verbs is one problem, so an I/O error wrapped beside violations goes
// uncounted where a join of the two counts it. A decode cannot tell the
// I/O of an UnmarshalYAML or UnmarshalText method from its check of the
// value, so an error such a method returns is a fault. A caller that must
// tell them apart checks for its own errors first. A [PanicError] is
// a bug in code. A document can still trip one in go-yaml, so a public
// endpoint that answers such a document with a 4xx status tests for the
// PanicError before it asks IsInvalid.
//
// A check run on a value inside a document reports `@` paths that read
// from that value. [Rebase] puts the path of the value in front of each
// `@` path in the tree of an error, details included, and points each
// problem that carries no location at the value itself. A `$` path, a
// position, and a range stay as they are, and rebases compose. A decode
// rebases what every nested [SelfValidator] returns, and a [Node] from
// [Node.At] or [Node.Nodes] binds an error as a Rebase under its own path
// does, so a check bound through the Node of its value needs no Rebase.
// A path names a map key as the document spells it, so a base built from
// a decoded key can miss, as the key 0x10 of a map[int]T that decodes to
// 16 does. A [DataLocator] finds an entry by its decoded key.
//
// Binding resolves each location once. A path the document leaves out,
// as a check for a required field names, binds at the key of the mapping
// that lacks the value. A path that enters an alias the document cannot
// follow, such as one to an anchor in a reference document, binds at the
// alias. An error a [SelfValidator] returns below a value that decodes
// itself binds at that value when its path selects nothing.
// [SourceError.Nearest] reports the node in each case. Any other location
// that does not resolve costs the binding its position, and
// [SourceError.Unresolved] returns the reason, such as [ErrOutOfRange]
// for a line the source does not hold or [ErrPathNeedsDocument] for a
// path with no single document to resolve in. Such a binding names its
// source in front of the message, and [FormatError] prints the reason in
// place of its excerpt.
//
// An error that is or wraps a [*SourceError], with no [*Error] above it
// that carries a location, heads errors, or holds details, is bound
// already, and every Bind and [Rebase] return it as it is. The exception
// is the error [BindValue] returns for a value that came from no
// document, such as the body of a request. Its text names each path from
// that value, as "$.port: 0 is less than 1", and it stands in no
// document, so a document can still place it. Rebase and every Bind read
// it as the error it was made from, and a decode places it at the value
// whose Validate method or [Validator] returned it. A wrapper around it
// keeps its own text, behind the position and the path. Placing builds a
// new error and leaves the result as it is:
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
// node through [ErrorTree.Message], [ErrorTree.Path], [ErrorTree.Source],
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
// # Validation Pipeline
//
// For structured validation, [Source.Decode] decodes a file that holds
// one document, [Source.Document] returns the root [Node] of that
// document for a caller that works in steps, and [Source.Documents]
// returns the root of each document of a file that holds several:
//
//	source := niceyaml.NewSourceFromString(yamlContent)
//	config, err := source.Decode[Config](ctx, niceyaml.WithValidator(validator))
//
//	for _, doc := range source.Documents() {
//		config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(validator))
//		if err != nil {
//			return err
//		}
//	}
//
// Each document parses on its own, so a YAML syntax error fails the
// document that holds it and no other. The [Node] of a document that did
// not parse returns its syntax error from [Node.Err], [Node.Decode], and
// [Node.Validate], so the loop above meets it where it meets the errors
// of the documents that parsed. A caller that reports on a whole file,
// as a linter does, calls [Source.ValidateDocuments], which validates
// each document and joins what they return. One pass thus names every
// syntax error, beside what the validator reports for each document that
// parsed. [Source.File] returns the syntax errors of the file as one
// error, for a caller that needs the whole file to parse before it reads
// any document, and [Source.Document] and [Source.Decode] return that
// error too. Every syntax error matches [ErrSyntax], whichever of these
// returns it.
//
// A file of several documents often holds empty ones, such as the one a
// trailing "---" leaves at the end of a file, and [Node.IsEmpty] reports
// them. Source.Documents leaves out each empty document of a file that
// holds a document with content, and Source.Document, Source.Decode, and
// Source.ValidateDocuments leave the same documents out. Kubernetes
// manifests and the output of a Helm chart thus decode and validate as
// they are, and a configuration file that ends in "---" holds one
// document. Source.AllDocuments returns the empty documents too. A file
// with no content holds one empty document, so the loop above meets
// it. A schema that wants a mapping rejects it, and a decode with no
// such schema returns the zero value. [SkipEmpty] wraps a validator so
// that it passes an empty document, for a caller whose file may be empty
// as a whole:
//
//	err := source.ValidateDocuments(ctx, niceyaml.SkipEmpty(reg))
//
// The root Node decodes, validates, and binds the whole document, and
// every function that takes a Node takes it as it is. [Node.Decode] runs
// validation on both sides of the decode. A [Validator] passed with
// [WithValidator] checks the node before decoding, and after decoding a
// type implementing [SelfValidator] validates itself. A
// [go.jacobcolvin.com/niceyaml/schema.Schema] is a Validator that checks
// the document against one JSON schema, and a
// [go.jacobcolvin.com/niceyaml/schema.Registry] is one that picks the
// schema for the document:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// [Node.Validate] runs a validator without decoding, and
// [DecodeOptions] holds several options as one, so a file of many
// documents states its schema and its decoder settings once:
//
//	strict := niceyaml.DecodeOptions(
//		niceyaml.WithValidator(reg),
//		niceyaml.WithDisallowUnknownFields(true),
//	)
//	for _, doc := range docs {
//		config, err := doc.Decode[Config](ctx, strict)
//		if err != nil {
//			return err
//		}
//	}
//
// Node.Validate and Source.ValidateDocuments take one validator, so a
// caller with several names how they run together. [MultiValidator] runs
// every one and reports every failure, which suits rules that check a
// document independently, as the rules of a linter do. [ChainValidator]
// stops at the first that fails, which suits a check that needs an
// earlier one to pass, such as one that reads the values a schema
// requires:
//
//	err := source.ValidateDocuments(ctx, niceyaml.MultiValidator(schema, names))
//	err := source.ValidateDocuments(ctx, niceyaml.ChainValidator(schema, refs))
//
// A decode runs the validators of repeated WithValidator options as
// ChainValidator runs them.
//
// A DecodeOption sets how one decode runs, and the Source holds what its
// documents mean. A document whose aliases name the anchors of another
// file, such as a file of shared defaults, gets that file through
// [WithReferences] on its Source. Every decode and every validation of
// the document then reads those anchors, so [Source.ValidateDocuments]
// reports what a decode of each document reports. An error about a value
// that file holds points at the alias in the document that reads it.
//
// The Source also says whether the alias limit applies to its documents.
// A decode, a schema, and a
// [go.jacobcolvin.com/niceyaml/schema/matcher.Content] or
// [go.jacobcolvin.com/niceyaml/schema/matcher.Text] matcher refuse a
// document whose nested aliases would make them read far more than the
// document holds. The error matches [ErrExcessiveAliasing], and
// [IsInvalid] reports it.
// [WithAliasLimit] on the Source turns the limit off for input the
// program trusts.
//
// A validator reads the node it checks with [Node.Decode], which runs
// the validators the caller passes and no other, so a validator never runs
// itself again. It writes its errors from that node, as [Validator]
// describes, and [Node.Validate] binds them. A validator that runs
// another on each element of a list through Node.Validate thus reports
// each failure on the lines of its element. A validator
// that follows the aliases of the document itself takes the resolver the
// document holds from [Node.Resolver], so checking each item of a list
// binds those aliases once.
//
// A validator that checks the decoded data, as an adapter for another
// schema language does, names what it finds by the member names and
// indices of that data. A path names a key as the source spells it, and
// the decoder respells some keys, so the key 0x10 sets the member 16.
// [Node.DataLocator] finds where such names lie in the document, so the
// adapter reports a finding where a
// [go.jacobcolvin.com/niceyaml/schema.Schema] reports a violation of the
// same value:
//
//	loc := n.DataLocator()
//
//	for _, f := range findings {
//		errs = append(errs, niceyaml.NewError(f.Msg, loc.At(f.Path...))) // $.ports.0x10.name
//	}
//
// A [SelfValidator] writes `@` paths, which read from the value itself.
// The decode calls Validate on every value in the result that implements
// it, and puts the paths each one reports under the path of that value in
// the document.
// A type therefore checks its own invariants once, and a document
// reports the lines of the field, element, or entry that holds it:
//
//	func (h Hours) Validate() error {
//		if h.Close.Before(h.Open) {
//			return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Current().Child("close")))
//		}
//
//		return nil
//	}
//
// A decode of a Config that holds Hours under spec reports
// $.spec.hours.close. A path may name a key the document leaves out, as
// a check for a required field does, and the error then binds at the key
// of the mapping that lacks it. [Rebase] puts the result of a check run
// on a value after Decode returns under the path of that value the same
// way.
//
// A check on a value that came from no document, such as the body of a
// request, has no line to report. [BindValue] binds its error to no
// document, so the text names each path, as in "$.close: closes before
// it opens". A caller that later learns where the value stands in a
// document places the same error there with Rebase and [Node.Bind].
// [SelfValidateValue] runs the Validate of every value below such a
// value, as a decode does, and binds the result the same way.
//
// A value the go-yaml decoder rejects reports the same way. The error
// matches [ErrDecode] and carries the path of the value, and its
// message describes the document rather than the Go target:
//
//	config.yaml:3:11: $.servers[0].port: expected integer, got string
//
// The decoder stops at the first value it rejects. The decode then looks
// for the other values the decoder rejects for their kind or range, so
// one decode reports them together, as [Node.DecodeInto] describes.
// Under [WithDisallowUnknownFields], the report also holds every key
// that no field of the target reads, each at the path of the key. A
// caller that walks the report with [Problems] tells a value of the
// wrong kind, a number outside the range of its type, and an unknown
// field apart with [ErrWrongKind], [ErrOverflow], and [ErrUnknownField].
//
// [Node.DecodeInto] runs the same pipeline on a value you already hold,
// such as one pre-populated with defaults.
//
// A program that reads its environment or its flags passes what they set
// as one more layer of [NewSourceFromLayers], as described below, and
// the whole
// pipeline then checks those values. [Node.SelfValidate] runs the last
// step on its own, for a program whose library writes them into the
// value instead. The program decodes the file with [WithSelfValidation]
// off, lets the library fill the value, and then validates the result,
// so a required field that only the environment sets passes. No
// [Validator] sees a value set this way, and each error still binds to
// the file, where its path resolves, as Node.SelfValidate describes for
// a value that no longer mirrors the document:
//
//	var cfg Config
//	if err := doc.DecodeInto(ctx, &cfg, niceyaml.WithSelfValidation(false)); err != nil {
//		return err
//	}
//
//	applyEnv(&cfg)
//
//	if err := doc.SelfValidate(ctx, &cfg); err != nil {
//		return err
//	}
//
// [Source.SelfValidate] runs the same step through the one document of a
// Source, as [Source.DecodeInto] decodes it. An empty Source stands in
// for a file that does not exist, so a program whose file is optional
// validates its defaults and the environment through the same calls. An
// error then reads as its path and its message, as in
// "$.servers[1].port: port is required".
//
// A program that layers one file over another merges them through
// [NewSourceFromLayers], which takes the Source of each file in the
// order they apply and returns a Source of the one document the files
// merge into. A mapping merges into the mapping below it key by key, a
// sequence or a scalar replaces what lies below it, and a null written
// as `null` or `~` removes its key. Each file resolves its own aliases
// and merge keys first. The pipeline runs once on the merged document,
// so a schema that requires a key passes when any file sets it, and each
// error binds in the file that holds its value:
//
//	merged, err := niceyaml.NewSourceFromLayers(ctx, base, prod)
//	if err != nil {
//		return err
//	}
//
//	cfg, err := merged.Decode[Config](ctx, niceyaml.WithValidator(schema))
//	if err != nil {
//		return err
//	}
//
// The merge returns the error of a file that did not parse, bound in
// that file, and the error of ctx once ctx ends.
//
// [Source.Document] of the merged Source returns the root Node of the
// merged document, and a [Validator] gets that Node. A caller reads one
// value of the files through it, as [Node.DecodeAt] reads one of a
// document, or prints what the files merge into. The document takes its name and its
// preamble from the lowest file, and its text is no file, so each error
// still binds in the file that holds its value. [Node.Origin] returns
// the Node that holds a value of the document in its file, for a
// validator that resolves a path beside that file. The decode fills the
// Go value from that document by the rule of [Node.DecodeInto], so
// defaults the value holds survive where the files leave a field out.
//
// The environment and the flags of a program go in as one more layer,
// above the files. The program builds a map of the keys they set, under
// the names the YAML uses. Each value has the Go type of its field. The
// program encodes the map into a document:
//
//	overrides := map[string]any{
//		"server": map[string]any{"port": port}, // APP_SERVER_PORT
//	}
//
//	data, err := encoder.Marshal(ctx, overrides)
//	if err != nil {
//		return err
//	}
//
//	env := niceyaml.NewSourceFromBytes(data,
//		niceyaml.WithName("environment"), niceyaml.WithExcerpts(false))
//
//	merged, err := niceyaml.NewSourceFromLayers(ctx, base, prod, env)
//	if err != nil {
//		return err
//	}
//
//	cfg, err := merged.Decode[Config](ctx, niceyaml.WithValidator(schema))
//
// The schema then checks the port the environment set. An error under
// the port binds in that layer, as in
// "environment:2:9: $.server.port: port must be at least 1".
// [WithExcerpts] set to false keeps the lines of that layer, and the
// secrets they hold, out of the excerpt of such an error.
// [NewSourceFromLayers] describes what such a layer cannot do, such as
// set one element of a sequence.
//
// [Node.At] returns a Node scoped to the node a path selects, and the
// same pipeline then runs on that node. Decode reads one value without
// decoding the whole document, and a validator given to it checks the
// node. The `@` paths in every error it returns or binds resolve from the
// node, and the bound error carries them as `$` paths from the root of
// the document.
// A check written for a type thus reports the same lines and the same
// paths whether the type is the whole document or a value inside one. The
// Node reaches the document it belongs to through [Node.Document]:
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
//	if err := hours.Bind(checkHours(&h)); err != nil {
//		return err
//	}
//
// A check the caller runs on the value, such as one that needs a registry
// of known names, binds its result through [Node.Bind], so an [Error]
// with an `@` path resolves from the node the value came from, and an
// error with no location points at that node.
//
// All three return errors bound to the source, so a path that selects
// nothing, a decoding failure, or a validator's [Error] renders its
// location through [FormatError] as it is.
//
// A caller that needs the value and not the Node reads it in one call.
// [Node.DecodeAt] reads a value the document must hold, and returns the
// error of At when the document lacks it. [Node.DecodeIfPresent] reads a
// value the document may leave out into a variable that holds the
// default, and reports whether the document holds the value:
//
//	kind, err := doc.DecodeAt[string](ctx, paths.Doc().Child("kind"))
//	if err != nil {
//		return err
//	}
//
//	version := 1
//
//	found, err := doc.DecodeIfPresent(ctx, paths.Doc().Child("version"), &version)
//	if err != nil {
//		return err
//	}
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
// [WithAllowDuplicateKeys], [WithCustomUnmarshaler], or
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
