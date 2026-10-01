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
//	fmt.Println(p.Print(source.View()))
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
// [go.jacobcolvin.com/niceyaml/diff.Differ] compares two [line.Lines] values, and
// [go.jacobcolvin.com/niceyaml/finder.Finder] searches one.
//
// Themes from [go.jacobcolvin.com/niceyaml/style/theme] provide color palettes. Without
// one, [go.jacobcolvin.com/niceyaml/printer.Printer] renders with
// [go.jacobcolvin.com/niceyaml/style.Default].
//
// [Error] points at a location in a YAML document: a path, a position, or
// a range. [Error.Error] returns the message, with a path in front as
// "$.path", so a validator can build one without holding the source.
//
// [SourceError] binds an error to its [Source] and to the document its
// path resolves in. Every error a Source or one of its Nodes produces is
// one. [Node.Bind] binds an error built elsewhere to that document, and
// [Source.Bind] binds one to the document its location falls in. A
// position or a range falls in the document whose span holds it, and a
// path in the one document of a source that holds one.
// [SourceError.Error] puts the resolved position in front of the
// message, or the name of the source alone when the error carries no
// location, and runs over several lines when the message does.
// [SourceError.Excerpt] returns the surrounding lines with the location
// highlighted.
// The nested errors [WithErrors] adds are part of the Error, and binding
// binds each of them too. [SourceError.Errors] returns one SourceError
// per nested error, with its own children, if any, and its own location
// when the nested error carries one. A validator's report of several
// violations is therefore a tree of bound errors.
// [FormatError] prints the message as a tree with a branch per nested
// error behind its position, then the excerpt, so a log names every
// violation and where it is. The excerpt marks every location
// in the tree, with each nested error as an annotation below its own line
// and distant errors in separate hunks. An error that unwraps to several,
// such as one from [errors.Join], binds as one SourceError with a child
// per branch. A wrapper from [fmt.Errorf] with several %w verbs keeps
// only the branches that carry a location or errors nested below them,
// so a sentinel it wraps beside a cause shows only in its message.
// [Bindings] finds every binding in an error joined from bound errors,
// such as one per document of a file, for a caller that renders them
// all. A SourceError never rewrites the message it binds, so an error
// built by hand goes through Bind before [fmt.Errorf] adds context,
// which keeps the position beside the message.
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
//	fmt.Println(p.Print(view))
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
// as a tree with a connector in front of each nested error. The excerpt
// around the locations follows, with the context lines the caller asks
// for and carets under the offending columns. FormatError looks through
// the wrappers and joins around a [SourceError], so it renders an error
// however a program wrapped it, and it renders the excerpt of every
// SourceError in the error's tree. The excerpt is [line.View.String], so
// a view a caller decorates, such as one with search matches, renders the
// same way. The output holds no escape sequences, so it goes into a log
// as it is:
//
//	log.Print(niceyaml.FormatError(err, 2))
//
// An [*Error] or a [*SourceError] logged as a [log/slog] attribute
// logs the tree without the excerpt, through [Error.LogValue] and
// [SourceError.LogValue], so a structured log names every nested error
// in one attribute whichever handler writes it.
//
// A terminal gets color from
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError], which prints
// the same tree and excerpts with the printer's styles, width, and
// context lines. Both build the tree with [NewErrorTree], which a
// renderer of its own reads too:
//
//	p := printer.New(printer.WithWrap(width), printer.WithContextLines(3))
//	fmt.Println(p.PrintError(err))
//
// This package knows nothing of the printer. The marks of an error decorate a
// [line.View], so a caller renders them with any renderer and composes them with
// anything else it renders. [SourceError.Excerpt] returns the hunks around the
// locations as a view, as [go.jacobcolvin.com/niceyaml/diff.Result.Hunks] does for a
// diff, and [SourceError.Annotate] marks a view that holds lines of the source, so a
// viewer shows a document with every error in place:
//
//	view := source.View()
//	for bound := range niceyaml.AllBindings(err) {
//		bound.Annotate(view)
//	}
//	fmt.Println(p.Print(view))
//
// Annotate finds each line by identity, since every view over a source
// shares its lines. The view may therefore be a slice of the source, such
// as one document of a file from [Node.Span], or a diff against another
// revision, where the marks of each error land on the lines of its own
// source that the diff holds.
// [line.View.Hunks] then keeps the marked lines with context around
// each, so a viewer shows the excerpt of every error at once, with
// search matches or any other decoration in it:
//
//	fmt.Println(p.Print(view.Hunks(2)))
//
// [Node.Ranges] returns the ranges an error at a path highlights, those of
// the token that starts the value, for a caller that marks a value on a
// view without an error to bind.
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
//	docs, _ := source.Documents()
//	for _, doc := range docs {
//		config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(validator))
//		if err != nil {
//			return err
//		}
//	}
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
// [Node.Validate] runs the same validators without decoding, and a
// [Decoder] holds the options for every node it decodes, so a file of
// many documents states its schema once:
//
//	dec := niceyaml.NewDecoder(niceyaml.WithValidator(reg))
//	for _, doc := range docs {
//		config, err := dec.Decode[Config](ctx, doc)
//		if err != nil {
//			return err
//		}
//	}
//
// A validator reads the node it checks with [Node.Decode], which runs
// the validators the caller passes and no other, so a validator never runs
// itself again.
//
// A [SelfValidator] writes its paths from its own root. The decode calls
// Validate on every value in the result that implements it, and puts the
// paths each one reports under the path of that value in the document.
// A type therefore checks its own invariants once, and a document
// reports the lines of the field, element, or entry that holds it:
//
//	func (h Hours) Validate() error {
//		if h.Close.Before(h.Open) {
//			return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Root().Child("close")))
//		}
//
//		return nil
//	}
//
// A decode of a Config that holds Hours under spec reports
// $.spec.hours.close. [Rebase] puts the result of a check run on a value
// after Decode returns under the path of that value the same way.
//
// [Node.DecodeInto] runs the same pipeline on a value you already hold,
// such as one pre-populated with defaults.
//
// [Node.At] returns a Node scoped to the node a path selects, and the
// same pipeline then runs on that node. Decode reads one value without
// decoding the whole document, and a validator given to it checks the
// node. The paths in every error it returns or binds resolve from the
// node, so a check written for a type reports the same lines whether the
// type is the whole document or a value inside one. The Node reaches the
// document it belongs to through [Node.Document]:
//
//	hours, err := doc.At(paths.Root().Child("spec", "hours"))
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
// with a path resolves from the node the value came from.
//
// All three return errors bound to the source, so a path that selects
// nothing, a decoding failure, or a validator's [Error] renders its
// location through [FormatError] as it is.
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
//	fmt.Println(p.Print(result.Unified()))
//	fmt.Println(p.Print(result.Hunks(3)))
//
// Custom algorithms implement [go.jacobcolvin.com/niceyaml/diff/lcs.Algorithm]. For a
// reusable [go.jacobcolvin.com/niceyaml/diff.Differ]:
//
//	d := diff.New(diff.WithAlgorithm(myAlgo))
//	result := d.Diff(before.Lines(), after.Lines())
//
// [Source.Lines] is every line of the file. [Node.Lines] is the lines one
// document or node covers, so a diff of one document of a file that holds
// several compares that document alone:
//
//	result := diff.Diff(before[1].Lines(), after[1].Lines())
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
//	fmt.Println(p.Print(view))
//
// # Dependencies
//
// niceyaml is a facade over go-yaml. The exported API names go-yaml types
// only where niceyaml wraps the document model rather than hiding it: the
// [*ast.File] and [*ast.DocumentNode] a [Source] parses into, the
// [token.Tokens] it lexes, and [ast.Node] and [*token.Token] as results of
// resolving a [paths.Path]. Positions, ranges, lines, errors, and styles are
// niceyaml's own types, and [paths.Path.YAMLPath] converts to go-yaml's
// path type when a caller needs it. A decode the go-yaml decoder rejects
// matches [ErrDecodeRejected], with the exceptions its doc names, so a
// caller tells that case apart without naming go-yaml's error types.
//
// The go-yaml settings niceyaml supports have named options, such as
// [WithAllowDuplicateKeys] or [go.jacobcolvin.com/niceyaml/encoder.WithIndent].
// The rest pass through options that take go-yaml values, and these carry a
// YAML prefix, as in [WithYAMLDecodeOptions], [WithYAMLParserOptions], and
// [go.jacobcolvin.com/niceyaml/encoder.WithYAMLOptions], so a caller can tell
// at the call site when the go-yaml dependency shows.
// [go.jacobcolvin.com/niceyaml/encoder.WithYAMLComments] takes go-yaml's
// comment map under the same prefix. A test in this package checks every
// exported declaration. It fails when a declaration names a go-yaml type
// outside the test's allowlist, or names a go-yaml option type or the
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
