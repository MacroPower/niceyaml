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
//		log.Print(niceyaml.FormatError(err, 2))
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
// the document its location falls in.
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
// [Error] points at a location in a YAML document: a path, a position, or a
// range, so a validator can build one without holding the source.
// [Error.Error] returns the message alone, with no location in it, and a
// binding puts the location in front. [FormatError] reads an Error that no
// binding holds yet, with its path in front as "@.path: msg".
//
// A path starts at one of two points, as in RFC 9535 JSONPath. A `$`
// path, from [paths.Doc], reads from the root of the document. An `@`
// path, from [paths.Current], reads from the current node: the [Node]
// that resolves the path, binds an error that carries it, or rebases it.
// A check written for a type reports `@` paths, which read from the value
// it checked, and the Node of that value puts its own path in front when
// it binds them. Every path the package hands out, such as [Node.Path],
// [Node.PathAt], and [SourceError.Path], starts at `$`, so it names the
// same value through any Node of the document.
//
// An Error is one problem. [WithDetails] adds the errors that explain it,
// such as its reasons, the forms a value failed to match, or a related
// location, and a detail is never a problem of its own. Several problems
// are a join from [errors.Join], or a summary from [NewSummary] whose
// message heads them, such as "2 schema violations":
//
//	conflict := niceyaml.NewError("port 80 conflicts", niceyaml.AtPath(portB),
//		niceyaml.WithDetails(niceyaml.NewError("first declared here", niceyaml.AtPath(portA))))
//
//	return niceyaml.NewSummary(fmt.Sprintf("%d violations", len(errs)), errs...)
//
// Every reader keeps to these roles. The lines of a message, the rows of
// a report, and the locations a scoped [Node] gives thus agree on what
// the problems of an error are. So do [errors.Is] and [errors.As], which
// match the problems of an error and pass over the details that explain
// them.
//
// [SourceError] binds an error to its [Source] and to the document its
// path resolves in. Every error a Source or one of its Nodes produces is
// one. [Node.Bind] binds an error built elsewhere to that document, and
// [Source.Bind] binds one to the document its location falls in. A
// position or a range falls in the document whose span holds it, and a
// path in the one document of a source that holds one.
// [SourceError.Error] puts the resolved position in front of the
// message, or the name of the source alone when the error carries no
// location, and runs over several lines when the message does. An error
// with no position in a file of several documents names its document
// behind the name, as "cafe.yaml: document 3: no matching schema". Under a
// summary or a join, it lists the problems below, one per line behind its
// own position, and it leaves details out. The message of a validator's
// report thus names each violation wherever the error goes, as a wrapper
// from [fmt.Errorf] carries it:
//
//	load config: cafe.yaml: 2 schema violations
//	cafe.yaml:6:8: $.spec.sla: string does not match pattern
//	cafe.yaml:22:11: $.spec.hours.days: expected "array", got "string"
//
// The list stops after [ErrorListLimit] errors and counts the rest.
// [SourceError.Excerpt] returns the surrounding lines with the location
// highlighted.
// The errors a summary heads and the details of an Error are part of the
// Error, and binding binds each of them too. [SourceError.Members] returns
// one SourceError per problem a summary heads and [SourceError.Details]
// one per detail, each with its own children, if any, and its own
// location when its error carries one. A validator's report of several
// violations is therefore a tree of bound errors. [FormatError] prints
// the message as a tree with a branch per error below another behind its
// position, details included, then the excerpt, so a log names every
// violation and where it is. The excerpt marks every location in the
// tree, with the message of each error below the root as an annotation
// below its own line and distant errors in separate hunks. An error that
// unwraps to several, such as one from [errors.Join], binds as one
// SourceError with a child per branch. A wrapper from [fmt.Errorf] with
// several %w verbs keeps only the branches that carry a location or
// errors below them, so a sentinel it wraps beside a cause shows only in
// its message.
// [Bindings] finds every binding in an error joined from bound errors,
// such as one per document of a file. FormatError prints the errors of
// such a join on one excerpt per source, each with its message beside
// its caret. A SourceError keeps the text it binds and puts the position
// and the path in front of it, so context that [fmt.Errorf] adds around
// an Error before the binding stays behind the path, and context it adds
// around the binding stands in front of the position.
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
// # Error Presentation
//
// [FormatError] prints an error as plain text. The message comes first,
// as a tree with a connector in front of each error below another. The
// excerpt around the locations follows, with the context lines the
// caller asks for and carets under the offending columns. FormatError
// looks through the wrappers and joins around a [SourceError], so it
// renders an error however a program wrapped it, and it renders the
// excerpt of every SourceError in the error's tree. The excerpt is
// [line.View.String], so a view a caller decorates, such as one with
// search matches, renders the same way. The output holds no escape
// sequences, so it goes into a log as it is:
//
//	log.Print(niceyaml.FormatError(err, 2))
//
// An [*Error] or a [*SourceError] logged as a [log/slog] attribute
// logs the tree without the excerpt, through [Error.LogValue] and
// [SourceError.LogValue], so a structured log names every error in the
// tree in one attribute whichever handler writes it.
//
// An excerpt shows lines of the source to whoever reads the log or the
// terminal. A program that loads a text with secrets in it, such as the
// values of its environment, creates that [Source] with [WithExcerpts]
// set to false. Every renderer then prints the position, the path, and
// the message of an error bound there, and no line of the text.
//
// An excerpt shows part of a line longer than [DefaultExcerptWidth]
// columns: a window of that many columns around each location on it,
// with "..." in place of the rest. A document minified onto one line
// thus adds a row of bounded length to a log. [WithExcerptWidth] sets
// another width for a [Source], or 0 for whole lines.
//
// A report a program reads, such as JSON lines, CI annotations, or editor
// diagnostics, lists the problems of an error as rows.
// [ErrorTree.Problems] yields one node per problem, by the roles the
// errors declare. It passes over the summary a validator puts above its
// violations, it keeps the details of each problem below its node, and
// it yields an error bound to no source, such as a file that failed to
// read. A row takes its fields from the node rather than from the text,
// whose form depends on where the node sits in the tree.
// [ErrorTree.Message] and [ErrorTree.Path] give the message and the path
// of any node. The binding the node holds gives the file and the
// position, through [SourceError.Source] and [SourceError.Position]. In
// a file that holds several documents, [SourceError.DocumentIndex] gives
// the document. The node holds the error of its problem in Err, so
// [errors.As] there finds the typed error of that row, such as the rule
// a validator wrapped, and never one of a detail.
//
// A terminal gets color from
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError], which prints
// the same tree and excerpts with the printer's styles, width, and
// context lines. Both build the tree with [NewErrorTree], which a
// renderer of its own reads too:
//
//	p := printer.New(printer.WithWrap(width), printer.WithContextLines(3))
//	lipgloss.Fprintln(os.Stderr, p.PrintError(err))
//
// This package knows nothing of the printer. The marks of an error decorate a
// [line.View], so a caller renders them with any renderer and composes them with
// anything else it renders. [SourceError.Excerpt] returns the hunks around the
// locations as a view, as [go.jacobcolvin.com/niceyaml/diff.Result.Hunks] does for a
// diff, and [Annotate] marks a view that holds lines of the source with
// every binding in an error, so a viewer shows a document with every
// error in place:
//
//	view := source.View()
//	niceyaml.Annotate(err, view)
//	lipgloss.Println(p.Print(view))
//
// Annotate finds each line by identity, since every view over a source
// shares its lines. The view may therefore be a slice of the source, such
// as one document of a file from [Node.Span], or a diff against another
// revision, where the marks of each error land on the lines of its own
// source that the diff holds. A line keeps each mark once, so a caller
// marks one view with several errors, or with the same error again, and
// no message doubles. [SourceError.Annotate] makes the same marks for one
// binding and the bindings below it.
// [line.View.Hunks] then keeps the marked lines with context around
// each, so a viewer shows the excerpt of every error at once, with
// search matches or any other decoration in it:
//
//	lipgloss.Println(p.Print(view.Hunks(2)))
//
// [Node.Ranges] returns the ranges an error at a path highlights, those of
// the token the path points at, for a caller that marks a value on a
// view without an error to bind. [Node.PathAt] goes the other way, from a
// position to the path of the node there. A viewer names the value under
// its cursor with it, and a check that reads the lines of a file reports
// the path of the value it found:
//
//	if path, ok := doc.PathAt(cursor); ok {
//		status = path.String() // $.spec.replicas
//	}
//
// This separates error production (validators, decoders) from error
// presentation (source context, formatting). Each layer provides what it
// knows. A validator provides the path, a source the document, and the
// caller that prints the terminal width and theme.
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
//	docs, err := source.Documents()
//	if err != nil {
//		return err
//	}
//
//	for _, doc := range docs {
//		config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(validator))
//		if err != nil {
//			return err
//		}
//	}
//
// Each document parses on its own, so a YAML syntax error fails the
// document that holds it and no other. [Source.AllDocuments] returns
// every document of such a file, and the [Node] of a document that did
// not parse returns its syntax error from [Node.Err], [Node.Decode], and
// [Node.Validate]. A caller that reports on a whole file, as a linter
// does, calls [Source.ValidateDocuments], which validates each document
// and joins what they return. One pass thus names every syntax error,
// beside what the validator reports for each document that parsed.
// [Source.File], [Source.Documents], [Source.Document], and
// [Source.Decode] need the whole file to parse. Every error of the parse
// matches [ErrSyntax], whichever of these returns it.
//
// A file of several documents often holds empty ones, such as the one a
// trailing "---" leaves at the end of a file, and [Node.IsEmpty] reports
// them. Source.Documents leaves out each empty document of a file that
// holds a document with content, and Source.Document, Source.Decode, and
// Source.ValidateDocuments leave the same documents out. Kubernetes
// manifests and the output of a Helm chart thus decode and validate as
// they are, and a configuration file that ends in "---" holds one
// document. Source.AllDocuments returns the empty documents too. A file
// with no content holds empty documents alone, so the loop above meets
// one. A schema that wants a mapping rejects it, and a decode with no
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
// itself again. It returns its errors bound through that node, as
// [Validator] describes, so a call to Validate returns the error
// Node.Validate does. A validator that runs another on each element of a
// list thus reports each failure on the lines of its element. A validator
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
// that no field of the target reads, each at the path of the key.
//
// [Node.DecodeInto] runs the same pipeline on a value you already hold,
// such as one pre-populated with defaults.
//
// A program that reads its environment or its flags passes what they set
// as one more layer of [Layers], as described below, and the whole
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
// [Layers], which holds the Node of each file in the order they apply.
// The files merge into one document. A mapping merges into the mapping
// below it key by key, a sequence or a scalar replaces what lies below
// it, and a null keeps it. Each file resolves its own aliases and merge
// keys first. The pipeline runs once on the merged document, so a schema
// that requires a key passes when any file sets it, and each error binds
// in the file that holds its value:
//
//	cfg, err := niceyaml.NewLayers(base, prod).Decode[Config](ctx, niceyaml.WithValidator(schema))
//	if err != nil {
//		return err
//	}
//
// [Layers.Document] returns the root Node of the merged document, and a
// [Validator] gets that Node. A caller reads one value of the files
// through it, as [Node.DecodeAt] reads one of a document, or prints
// what the files merge into. The document takes its name and its
// preamble from the lowest file, and its text is no file, so each error
// still binds in the file that holds its value. The decode fills the Go
// value from that document by the rule of [Node.DecodeInto], so defaults
// the value holds survive where the files leave a field out.
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
//	env, err := niceyaml.NewSourceFromBytes(data, niceyaml.WithName("environment")).Document()
//	if err != nil {
//		return err
//	}
//
//	cfg, err := niceyaml.NewLayers(base, prod, env).Decode[Config](ctx, niceyaml.WithValidator(schema))
//
// The schema then checks the port the environment set. An error under
// the port binds in that layer, as in
// "environment:2:9: $.server.port: port must be at least 1". [Layers]
// describes what such a layer cannot do, such as unset a value.
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
// [IsInvalid] tells a document at fault apart from a check that could
// not run. The document is at fault for a syntax error, a decode
// rejection, a schema violation, and an error a [SelfValidator] returns,
// and it is not for a schema that does not load or a context that ended.
// A [Validator] declares which of its errors is which by the constructor
// it calls. [NewError] and [Invalid] declare the document at fault,
// with a location or without. [Place] takes the same options and
// declares nothing, as any other error does, so it shows a check that
// could not run at the value the check read:
//
//	niceyaml.NewError("license file does not exist", niceyaml.AtPath(p)) // the document is at fault
//	niceyaml.Invalid(checkErr, niceyaml.AtPath(p))                       // the document is at fault
//	niceyaml.Place(statErr, niceyaml.AtPath(p))                          // the check could not run
//
// IsInvalid reports whether the document is at fault for every problem
// of an error, which is the check that picks a status code or an exit
// code. A report that lists the problems one by one asks
// [ErrorTree.IsInvalid] of each.
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
// matches [ErrSyntax], and a decode that fails on YAML that parsed
// matches [ErrDecode]. A caller thus tells both cases apart without
// naming go-yaml's error types.
//
// The go-yaml settings niceyaml supports have named options, such as
// [WithAllowDuplicateKeys], [WithCustomUnmarshaler], or
// [go.jacobcolvin.com/niceyaml/encoder.WithIndent]. A parse and a decode
// take named options alone, so niceyaml knows what each one changes and
// applies it to every decoder a call runs. An option that takes or
// yields a go-yaml value carries a YAML prefix, so a caller can tell at
// the call site when the go-yaml dependency shows. [WithYAMLComments] and
// [go.jacobcolvin.com/niceyaml/encoder.WithYAMLComments] take go-yaml's
// comment map, and a decode under [WithYAMLOrderedMaps] yields its
// ordered map. The encoder settings with no option of their own pass
// through [go.jacobcolvin.com/niceyaml/encoder.WithYAMLOptions], which
// takes go-yaml's own options. A test in this package checks every
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
