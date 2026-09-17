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
//	fmt.Println(p.Print(source.Lines()))
//
// Errors come back bound to the source, so they show users exactly where
// the problem is:
//
//	file, err := source.File()
//	if err != nil {
//		// The %+v verb displays the YAML with the problematic location
//		// highlighted; plain %v prints the message and position only.
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
// the errors it and its Documents produce to itself. [Source.WrapError]
// binds errors built elsewhere.
//
// [line.Lines] is the view. It organizes tokens into lines, and each
// [line.Line] carries optional metadata for rendering. Annotations hold
// error messages and diff headers, flags mark inserted and deleted lines, and
// overlays apply style spans for highlighting. A Source hands out an
// independent view from [Source.Lines], so the metadata added to one view
// reaches neither the Source nor another view.
//
// A view need not be a YAML document. Diffs, for example, interleave lines
// from two revisions and are plain [line.Lines] values.
//
// [printer.Printer] renders any [line.View], which [line.Lines] satisfies,
// with syntax highlighting via lipgloss. It supports customizable gutters
// (line numbers, diff markers), word wrapping, and annotation rendering.
// [diff.Differ] compares two views, and [finder.Finder] searches one.
//
// Themes from [go.jacobcolvin.com/niceyaml/style/theme] provide color
// palettes. Without one, [printer.Printer] renders with [style.Default].
//
// [Error] points at a location in a YAML document: a path, a token, or a
// range. [Error.Error] returns the message with that location, so a
// validator can build one without holding the source. A token or range
// position is known when the Error is built and reads "[line:col]"; a path
// reads "$.path" until a source resolves it.
//
// [SourceError] binds an Error to its [Source]. Every error a Source or one
// of its Documents produces is one, and [Source.WrapError] binds an Error
// built elsewhere. [SourceError.Error] puts the resolved position of a path
// in front of the message, and [SourceError.Detail] renders the surrounding
// lines with the location highlighted. The %+v verb prints both. Nested
// errors appear as annotations below their respective lines, with distant
// errors displayed in separate hunks. A SourceError never rewrites the
// message it binds, so an error built by hand goes through WrapError before
// [fmt.Errorf] adds context, which keeps the position beside the message.
//
// # Lines
//
// YAML tokens can span multiple lines, as block scalars and multiline
// strings do, while diffing, printing, and searching are much simpler with
// line-by-line access. [line.NewLines] splits multiline tokens at line
// boundaries into one part per line and keeps a reference to the original
// token every part was cut from, and [line.Lines.Tokens] reverses the split.
// A [Source] does this on creation and hands out a private copy of the
// result from [Source.Lines]:
//
//	view := source.Lines()
//	view.AddOverlay(style.GenericError, errorRange)
//	view.BlendOverlay(style.GenericHighlight, matches...)
//	fmt.Println(p.Print(view))
//
// Every token the module hands out, from [Source.Tokens], [line.Lines.TokenAt],
// [line.Line.Tokens], or [line.Line.Token], is shared with the lines. Treat
// them as read-only and call [token.Token.Clone] before modifying one. The
// [line] package documents the view and its metadata in full.
//
// # Error Presentation
//
// The %+v verb renders a [SourceError] with a default [printer.Printer] and
// two lines of context. The code that prints the error chooses anything else,
// through [DetailOption] values passed to [SourceError.Render] or
// [SourceError.Detail]:
//
//	var bound *niceyaml.SourceError
//	if errors.As(err, &bound) {
//		fmt.Println(bound.Render(
//			niceyaml.WithPrinter(p),
//			niceyaml.WithContextLines(3),
//		))
//	}
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
// [Document.Decode] supports two validation hooks: a [DocumentValidator]
// passed with [WithValidator] checks the whole document before decoding, and
// a type implementing [Validator] validates itself after decoding. A
// [go.jacobcolvin.com/niceyaml/schema.Validator] is a DocumentValidator that
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
// Diff output is a [line.Lines] view rather than a [Source], since the
// interleaved lines do not form a YAML document. It uses [line.Flag] to mark
// inserted/deleted lines and [line.Annotation] for unified diff hunk headers.
//
// # Text Search
//
// [finder.Finder] locates strings within tokens, returning [position.Range]
// values suitable for [line.Lines.AddOverlay] and [line.Lines.BlendOverlay].
//
// Use [normalizer.New] with [finder.WithNormalizer] for case-insensitive,
// diacritic-insensitive matching:
//
//	f := finder.New(finder.WithNormalizer(normalizer.New()))
//	view := source.Lines()
//	idx := f.Load(view)
//	view.AddOverlay(style.GenericHighlight, idx.Find("search term")...)
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
// Rendering builds on lipgloss, and the [style] package exposes its Style
// type directly since a theme is a set of lipgloss styles. The
// [go.jacobcolvin.com/niceyaml/fangs] and
// [go.jacobcolvin.com/niceyaml/bubbles/yamlviewport] packages are adapters
// for the charm libraries they build on and expose those libraries' types by
// design. Each is a module of its own, so bubbletea, fang, and cobra stay
// out of this module's dependencies.
package niceyaml
