// Package niceyaml provides utilities for working with YAML documents.
// It is built using [yaml] and [charm.land/lipgloss/v2].
//
// By directly styling YAML tokens a single time, niceyaml is much more
// consistent, flexible, and performant, when compared to using multiple
// distinct styling systems.
//
// It also provides an alternative implementation of go-yaml's source
// annotations for errors, as well as adapters for use with JSON schema
// validation.
//
// # Usage
//
// Parse YAML into a [Source], then use a [printer.Printer] to render it with
// syntax highlighting:
//
//	source := niceyaml.NewSourceFromString(yamlContent)
//	p := printer.New()
//	fmt.Println(p.Print(source.View()))
//
// Errors come back bound to the source, so they show users exactly where
// the problem is:
//
//	file, err := source.File()
//	if err != nil {
//		// The %+v verb prints the message and a plain-text excerpt of
//		// the YAML with the problematic location marked; plain %v prints
//		// the message and position only.
//		fmt.Printf("%+v\n", err)
//	}
//
// # Architecture
//
// The module separates a YAML document from its rendering, and each part
// lives in a package of its own.
//
// [Source], in this package, is the file. It owns the tokens from go-yaml,
// lazily parses them into an AST with [Source.File], returns each YAML
// document in the file as a [Document] from [Source.Documents], and binds
// the errors it and its Documents produce to itself. [Document.Bind]
// binds errors built elsewhere.
//
// [line.Lines] is the content, the tokens organized into lines, and it
// never changes. [line.View] is one rendering of that content: it shares
// the lines and carries the decoration of the rendering. Annotations hold
// error messages and diff headers, flags mark inserted and deleted lines,
// and overlays apply style spans for highlighting. A Source hands out its
// lines from [Source.Lines] and a fresh view over them from [Source.View],
// so the decoration added to one view reaches neither the Source nor
// another view, and taking a view costs nothing.
//
// A view need not be a YAML document. Diffs, for example, interleave lines
// from two revisions and are plain [line.View] values.
//
// [printer.Printer] renders a [line.View] with syntax highlighting via
// lipgloss. It supports customizable gutters (line numbers, diff markers),
// word wrapping, and annotation rendering. [diff.Differ] compares two
// [line.Lines] values, and [finder.Finder] searches one.
//
// Themes from [go.jacobcolvin.com/niceyaml/style/theme] provide color
// palettes. Without one, [printer.Printer] renders with [style.Default].
//
// [Error] points at a location in a YAML document: a path, a position, or
// a range. [Error.Error] returns the message, with a path in front as
// "$.path", so a validator can build one without holding the source.
//
// [SourceError] binds an error to its [Source] and to the document its
// path resolves in. Every error a Source or one of its Documents produces
// is one, and [Document.Bind] binds an error built elsewhere to that
// document. [SourceError.Error] is one line: the resolved position in
// front of the message, or the name of the source alone when the error
// carries no location. [SourceError.Excerpt] returns the surrounding
// lines with the location highlighted.
// Nested errors from [WithErrors] are structure on the Error, and binding
// binds each of them too: [SourceError.Errors] returns one SourceError per
// nested error, with its own resolved location and its own children, so a
// validator's report of several violations is a tree of bound errors. The
// %+v verb prints the message, one line per nested error behind its
// position, and the excerpt, so a log that prints the error that way still
// names every violation and where it is. The excerpt marks every location
// in the tree, with each nested error as an annotation below its own line
// and distant errors in separate hunks. An error that unwraps to several,
// such as one from [errors.Join], binds as one SourceError with a child
// per branch, and [SourceErrors] finds every binding in an error joined
// from bound errors, such as one per document of a file, for a caller
// that renders them all. A SourceError never rewrites the
// message it binds, so an error built by hand goes through Bind before
// [fmt.Errorf] adds context, which keeps the position beside the message.
//
// # Lines
//
// YAML tokens can span multiple lines, as block scalars and multiline
// strings do, while diffing, printing, and searching are much simpler with
// line-by-line access. [line.NewLines] splits multiline tokens at line
// boundaries into one part per line and keeps a reference to the original
// token every part was cut from, and [line.Lines.Tokens] reverses the split.
// A [Source] does this on creation, hands out the result from
// [Source.Lines], and hands out a view to decorate from [Source.View]:
//
//	view := source.View()
//	view.AddOverlay(kind.GenericError, errorRange)
//	view.BlendOverlay(kind.GenericHighlight, matches...)
//	fmt.Println(p.Print(view))
//
// Every token the module hands out, from [Source.Tokens], [line.Lines.TokenAt],
// [line.Line.Tokens], or [line.Line.Token], is shared with the lines. Treat
// them as read-only and call [token.Token.Clone] before modifying one. A
// copy of a token, such as one from the [*ast.File] that [Source.File]
// parses, matches the original by its type, value, origin, and position,
// so a token taken from a node finds its lines the same way. The [line]
// package documents the view and its metadata in full.
//
// # Error Presentation
//
// The %+v verb prints a [SourceError] as plain text: the message, one line
// per nested error, then the excerpt around the locations with two lines
// of context and carets under the offending columns. The output holds no
// escape sequences, so it goes into a log as it is. A terminal gets color
// from [printer.Printer.PrintError],
// which prints the same parts with the printer's styles, width, and
// context lines, and accepts any error, so a caller need not look for the
// [SourceError] in the chain. It draws the message as a tree, with a
// connector in front of each nested error, and renders the excerpt of
// every SourceError in the error's tree:
//
//	p := printer.New(printer.WithWidth(width), printer.WithContextLines(3))
//	fmt.Println(p.PrintError(err))
//
// This package knows nothing of the printer. The marks of an error are
// decoration on a [line.View], so a caller renders them with any renderer
// and composes them with anything else it renders. [SourceError.Excerpt]
// returns the hunks around the locations as a view, as [diff.Result.Hunks]
// does for a diff, and [SourceError.Annotate] marks a view that holds
// lines of the source, so a viewer shows a document with every error in
// place:
//
//	view := source.View()
//	for _, bound := range validationErrors {
//		_ = bound.Annotate(view)
//	}
//	fmt.Println(p.Print(view))
//
// Annotate finds each line by identity, since every view over a source
// shares its lines, so the view may be a slice of the source, such as one
// document of a file from [Document.Span], or a diff against another
// revision, where the marks land on the lines of this source alone.
//
// [Document.Ranges] returns the ranges a path covers, the same ones an
// error at that path highlights, for a caller that marks a value on a view
// without an error to bind.
//
// This separates error production (validators, decoders) from error
// presentation (source context, formatting), allowing each layer to provide
// what it knows: a validator the path, a source the document, and the
// caller that prints the terminal width and theme.
//
// # Validation Pipeline
//
// For structured validation, [Source.Decode] decodes a file that holds one
// document, and [Source.Documents] returns a [Document] for each document
// of a file that holds several:
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
// [Document.Decode] supports two validation hooks: a [Validator]
// passed with [WithValidator] checks the whole document before decoding, and
// a type implementing [SelfValidator] validates itself after decoding. A
// [go.jacobcolvin.com/niceyaml/schema.Schema] is a Validator that
// checks the document against one JSON schema, and a
// [go.jacobcolvin.com/niceyaml/schema.Registry] is one that picks
// the schema for the document:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// [Document.Validate] runs the same validators without decoding.
//
// [Document.DecodeInto] runs the same pipeline on a value you already hold,
// such as one pre-populated with defaults.
//
// Both return errors bound to the source, so a decoding failure or a
// validator's [Error] renders its location with the %+v verb as it is.
//
// # Diffs
//
// [diff.Differ] computes line differences using an [lcs.Algorithm]. The
// default, [lcs.Hirschberg], is space-efficient for large files:
//
//	result := diff.Diff(original.Lines(), modified.Lines())
//	p := printer.New()
//	fmt.Println(p.Print(result.Unified()))
//	fmt.Println(p.Print(result.Hunks(3)))
//
// Custom algorithms implement [lcs.Algorithm]. For a reusable [diff.Differ]:
//
//	d := diff.New(diff.WithAlgorithm(myAlgo))
//	result := d.Diff(before.Lines(), after.Lines())
//
// Diff output is a [line.View] rather than a [Source], since the
// interleaved lines do not form a YAML document. It uses [line.Flag] to mark
// inserted/deleted lines and [line.Annotation] for unified diff hunk headers.
//
// # Text Search
//
// [finder.Finder] locates strings within tokens, returning [position.Range]
// values suitable for [line.View.AddOverlay] and [line.View.BlendOverlay].
//
// Use [normalizer.New] with [finder.WithNormalizer] for case-insensitive,
// diacritic-insensitive matching:
//
//	f := finder.New(finder.WithNormalizer(normalizer.New()))
//	idx := f.Load(source.Lines())
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
// path type when a caller needs it.
//
// Every go-yaml setting has a named option, such as [WithAllowDuplicateKeys]
// or [go.jacobcolvin.com/niceyaml/encoder.WithIndent]. The options that pass
// go-yaml values through carry a YAML prefix, as in [WithYAMLDecodeOptions]
// and [go.jacobcolvin.com/niceyaml/encoder.WithYAMLOptions], so a
// caller can tell at the call site when the go-yaml dependency shows. A test
// in this package enforces both rules on every exported declaration.
//
// The [go.jacobcolvin.com/niceyaml/schema] package follows the same rule
// for the JSON Schema library it builds on:
// [go.jacobcolvin.com/niceyaml/schema.Compile] takes a schema document as
// bytes, and the options that pass that library's values through carry a
// JSONSchema prefix, as in
// [go.jacobcolvin.com/niceyaml/schema.WithJSONSchemaOptions]. The same
// test enforces it.
//
// Rendering builds on lipgloss, and the [style] package exposes its Style
// type directly since a theme is a set of lipgloss styles. The kinds of text
// a rendering names, such as [kind.GenericError], live in
// [go.jacobcolvin.com/niceyaml/style/kind], which imports nothing, so the
// [line], [diff], and [finder] packages mark content without depending on
// lipgloss. The
// [go.jacobcolvin.com/niceyaml/fangs] and
// [go.jacobcolvin.com/niceyaml/bubbles/yamlviewport] packages are adapters
// for the charm libraries they build on and expose those libraries' types by
// design. Each is a module of its own, so bubbletea, fang, and cobra stay
// out of this module's dependencies.
package niceyaml
