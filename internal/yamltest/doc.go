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
// [TokenBuilder] provides a fluent API to construct tokens without boilerplate:
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
// positions, so the test calls [ValidateTokens] first. Each diff value reports
// whether its inputs match through Equal and formats itself through String,
// so a test can hand one to t.Error. [CompareContent] returns the same kind
// of value for whole text. Before comparing, it converts CRLF and bare CR
// line endings to LF and trims leading and trailing newlines:
//
//	if diff := yamltest.CompareContent(want, got); !diff.Equal() {
//		t.Error(diff)
//	}
//
// Tests of code that builds a [line.Lines] collection check its integrity
// with [ValidateLines].
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
// # Test Documents
//
// [FirstDocument] parses a YAML input and returns the root [*niceyaml.Node] of
// its first document, and [FirstDocumentWithPath] also gives the source a file
// path. [At] scopes a node to a path and fails the test when nothing matches:
//
//	doc := yamltest.FirstDocument(t, input)
//	replicas := yamltest.At(t, doc, paths.Doc().Child("spec", "replicas"))
//
// Tests that check how a bound error reads use [Bind]. It binds an error to
// the single document of a [*niceyaml.Source] and fails the test unless the
// source holds exactly one document:
//
//	path := paths.Doc().Child("spec", "replicas")
//	err := yamltest.Bind(t, source, niceyaml.NewError("bad", niceyaml.AtPath(path)))
//
// Tests of alias limits build their documents with [AliasLevels] and
// [MergeLevels]. Each level refers to the level before it ten times, so a
// few levels make a small document that holds millions of nodes once its
// aliases expand. A decode expands merge keys every time. It expands an
// alias to a list only where it writes the list out, as in a mapping
// key, so a test appends such a key:
//
//	input := yamltest.AliasLevels(7) + "kind:\n  ? *l7\n  : v\n"
//
// # Mocks
//
// [NormalizerFunc] adapts a function so it can stand in for a normalizer
// in code paths that depend on normalization:
//
//	normalizer := yamltest.NormalizerFunc(strings.ToLower)
//
// A [niceyaml.ValidatorFunc] stands in for a validator.
package yamltest
