// Package yamltest provides test utilities for code that works with go-yaml
// tokens and niceyaml styled output.
//
// A token fixture takes many fields to build by hand, and ANSI escape codes
// make styled output hard to read. The helpers here shorten fixtures and
// render styles as readable tags.
//
// # Token Fixtures
//
// [token.Token] has many fields that tests must populate.
// [TokenBuilder] sets those fields through chained method calls:
//
//	tok := yamltest.NewTokenBuilder().
//		Type(token.StringType).
//		Value("hello").
//		PositionLine(1).
//		PositionColumn(1).
//		Build()
//
// The builder is mutable, but [TokenBuilder.Build] returns a clone, so you can
// call it multiple times to produce independent tokens.
//
// Use [TokenBuilder.Clone] to branch from a common base configuration.
//
// # Token Comparison
//
// When tokens differ, standard equality checks produce unhelpful output.
// [RequireTokensEqual] validates both slices, compares them field by field,
// and fails the test with a readable report of what differs:
//
//	yamltest.RequireTokensEqual(t, want, got)
//
// A test that reports differences its own way runs those steps itself.
// [CompareTokens] and [CompareTokenSlices] assume non-nil tokens and
// positions, so such a test calls [ValidateTokens] first. Each diff value
// reports whether its inputs match through Equal and formats itself
// through String, so a test can hand one to t.Error. [CompareContent]
// returns the same kind of value for whole text. Before comparing, it
// converts CRLF and bare CR line endings to LF and trims leading and
// trailing newlines:
//
//	if diff := yamltest.CompareContent(want, got); !diff.Equal() {
//		t.Error(diff)
//	}
//
// Tests of code that builds a [line.Lines] collection check its integrity
// with [ValidateLines] and read the number of each line through
// [LineNumbers]. Tests of code that links tokens check the Prev and Next
// of a stream with [ValidateTokenLinks]. A table that expects one token
// names it as "value@line:column", which [TokenPlace] writes, and a table
// that expects several compares their values through [TokenValues].
//
// Most ranges that a test names sit on one line, and [LineRange] writes
// one from the line and its two columns:
//
//	want := yamltest.LineRange(0, 5, 10)
//
// # Styled Output
//
// niceyaml applies terminal styles to YAML syntax elements.
// [XMLStyles] replaces escape codes with XML-like tags:
//
//	styles := yamltest.NewXMLStyles()
//	// Input "key: value" produces:
//	// <nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue>\
//	// <text> </text><literalString>value</literalString>
//
// [NewXMLPrinter] creates a printer that renders through those styles
// without a gutter and with an empty container style. A test then compares
// the tagged text alone:
//
//	got := yamltest.NewXMLPrinter().Print(source.View())
//
// [NewPlainPrinter] creates the same printer with empty styles, for a test
// that reads the bare text.
//
// Tests of a color theme compare colors of different types through
// [ColorHex] and measure how well text reads on its background through
// [Contrast].
//
// # Test Documents
//
// [FirstDocument] parses a YAML input and returns the root [*niceyaml.Node] of
// its first document, and [FirstDocumentWithPath] also gives the source a file
// path. [At] scopes a node to a path and fails the test when the path does
// not select a node:
//
//	doc := yamltest.FirstDocument(t, input)
//	replicas := yamltest.At(t, doc, paths.Doc().Child("spec", "replicas"))
//
// [Document] returns the root of the single document of a
// [*niceyaml.Source] that the test built itself. [AtOrSelf] scopes like
// [At] but returns its node for a path without a selector. A table case
// then names a path only when it checks a scoped node. [UnparsedDocument]
// returns the node of an input that does not parse, for a test of what
// such a document reports. [Documents] and [AllDocuments] return the
// documents of a source and fail the test unless the source holds as many
// as the test names:
//
//	docs := yamltest.Documents(t, source, 2)
//
// [References] creates the option that gives a source reference
// documents from YAML inputs:
//
//	doc := yamltest.FirstDocument(t, "server: *base\n", yamltest.References("base: &base {port: 80}\n"))
//
// [NoSizeLimits] returns the options that turn off the byte limit and the
// depth limit, for a test or a benchmark that reads a large document.
//
// Tests that check how a bound error reads use [Bind]. It binds an error to
// the single document of a [*niceyaml.Source] and fails the test unless the
// source holds exactly one document. [ErrorAt] creates an error at the
// value under a list of keys:
//
//	err := yamltest.Bind(t, source, yamltest.ErrorAt("bad", "spec", "replicas"))
//
// Tests of a [niceyaml.Validator] run it on a scoped node through
// [niceyaml.Node.Validate]. Validate binds the error as a decode does:
//
//	item := yamltest.At(t, doc, paths.Doc().Child("items").Index(1))
//	require.EqualError(t, item.Validate(t.Context(), rule), want)
//
// Tests of alias limits build their documents with [AliasLevels] and
// [MergeLevels]. Each level refers to the level before it ten times. A few
// levels therefore make a small document that holds millions of nodes when
// its aliases expand. A decode expands merge keys every time. It expands an
// alias to a list only where it writes the list out, as in a mapping
// key. A test appends such a key with [AliasKey]:
//
//	input := yamltest.AliasLevels(7) + yamltest.AliasKey("kind", 7)
//
// [WideMerge] builds a mapping whose merge key lists one alias for each
// of its keys, so a lookup of every key reads the whole list.
// [FlowSequence] writes the flow sequence that repeats one item, for a
// test that lays out a document of its own.
//
// Tests and benchmarks that need a long document write it with
// [GenerateLines], from the format of one line:
//
//	input := yamltest.GenerateLines(200, "k%d: v")
//
// A benchmark that does several units of work in each iteration reports
// the time of one unit through [ReportTimePer].
//
// [SchemaDirective] puts a document under the comment that names its
// schema, for tests of schema directives. [Diff] diffs the lines of two
// YAML inputs, for tests of code that renders or searches a diff.
//
// # Error Trees
//
// Tests that read the structure of an error read the tree that
// [niceyaml.NewErrorTree] builds. [ProblemsOf] and [DetailsOf] return the
// children of its root that are problems and details, and [BoundIn]
// returns each binding in the tree:
//
//	for _, problem := range yamltest.ProblemsOf(err) {
//		assert.Same(t, source, problem.Source())
//	}
//
// [ErrsOf] reads the error of each of those nodes, and
// [RequireSourceError] returns the binding of an error or fails the test:
//
//	bound := yamltest.RequireSourceError(t, err)
//
// [BoundNode] and [BoundDocument] return the node and the document that a
// binding is bound to. Each fails the test when the binding is not bound
// to one.
// [AssertBoundTo] asserts that each binding in the tree of an error is
// bound to the source that the test names.
//
// [OnlyProblem] returns the one problem that [niceyaml.Problems] yields
// for an error, even below a summary or a join. [RequireError] returns
// the [*niceyaml.Error] in the chain of an error. [PathOf], [RangeOf],
// and [PositionOf] return the location that an error or a node of the
// tree reports. Each fails the test when its argument does not report a
// location:
//
//	problem := yamltest.OnlyProblem(t, err)
//	assert.Equal(t, yamltest.LineRange(1, 6, 9), yamltest.RangeOf(t, problem))
//
// [ErrorAnnotation] and [ErrorOverlays] return the annotation and the
// overlays that a binding leaves on a line that it marks. A test of the
// excerpt of the binding compares the line with them.
//
// [CheckError], [UpperError], [IndentError], and [RewriteError] model
// wrappers that a caller puts around an error. Each wraps the error and
// changes its text in its own way, for tests of code that keeps the text
// of a wrapper.
//
// # Files
//
// Tests of code that reads files build a file system in memory with
// [MapFS], or write files under a directory with [WriteFile] and
// [WriteFiles]. Each path is slash-separated, and WriteFile creates each
// parent directory that the file lacks:
//
//	yamltest.WriteFiles(t, dir, map[string]string{"configs/app.yaml": "name: a\n"})
//
// [ReadFile] reads a file back. [SymlinkOrSkip] creates a symbolic link
// and skips the test on a platform that refuses one.
// [RemoveWorkingDirectory] removes the working directory of the process,
// for a test of code that resolves a relative path. It changes the
// directory through [testing.TB.Chdir], so such a test does not run in
// parallel.
//
// # Mocks
//
// [NormalizerFunc] adapts a function to the
// [go.jacobcolvin.com/niceyaml/finder.Normalizer] interface, for tests of
// code paths that depend on normalization:
//
//	normalizer := yamltest.NormalizerFunc(strings.ToLower)
//
// A [niceyaml.ValidatorFunc] adapts a function to a [niceyaml.Validator]
// in the same way.
//
// [RoundTripFunc] adapts a function to the transport of an [http.Client],
// so a test answers each request itself:
//
//	client := yamltest.RoundTripFunc(respond).Client()
//
// [Respond] is such a function for one status and body, and
// [CountingClient] counts the requests that a transport receives:
//
//	client, requests := yamltest.CountingClient(yamltest.Respond(http.StatusOK, body))
//
// [RespondBody] answers with the bytes of a reader, for a body that fails
// while the client reads it or that runs past a size limit.
package yamltest
