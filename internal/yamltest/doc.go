// Package yamltest provides test utilities for code that works with go-yaml
// tokens and niceyaml styled output.
//
// Testing YAML tooling presents two main challenges: constructing token
// fixtures is verbose, and comparing styled output cluttered with ANSI escape
// codes is difficult to read. This package addresses both.
//
// # Writing Readable Test Inputs
//
// [Input] strips common indentation from heredoc-style strings, letting you
// write YAML naturally within Go test code:
//
//	got := someFunction(yamltest.Input(`
//		key: value
//		nested:
//		  child: data
//	`))
//
// For expected output, [JoinLF] and [JoinCRLF] construct multi-line strings
// with explicit line endings:
//
//	want := yamltest.JoinLF(
//		"line1",
//		"line2",
//		"line3",
//	)
//
// # Building Token Fixtures
//
// [token.Token] has many fields that must be populated for tests.
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
// call it multiple times to get independent tokens.
//
// Use [TokenBuilder.Clone] to branch from a common base configuration.
//
// # Comparing Tokens
//
// When tokens differ, standard equality checks produce unhelpful output.
//
// This package provides pure comparison functions that return detailed diffs:
//
//	// Validate tokens first (returns error if nil tokens or positions)
//	if err := yamltest.ValidateTokens(want, got); err != nil {
//		t.Fatalf("invalid tokens: %v", err)
//	}
//
//	// Compare tokens and get diff details
//	if diff := yamltest.CompareTokenSlices(want, got); !diff.Equal() {
//		t.Errorf("token mismatch: %s", diff)
//	}
//
// [ValidateTokenPair] and [ValidateTokens] check for nil tokens or positions,
// returning [*TokenValidationError] with the underlying [ErrNilToken] or
// [ErrNilPosition] reason.
//
// [CompareTokens] and [CompareTokenSlices] return [TokenDiff] and [TokensDiff]
// respectively, providing detailed field-by-field comparison results.
//
// For content comparison with normalized line endings, use [CompareContent]:
//
//	if diff := yamltest.CompareContent(want, got); !diff.Equal() {
//		t.Errorf("content mismatch: %s", diff)
//	}
//
// For debugging, [FormatToken] and [FormatTokens] produce readable
// representations, and [DumpTokenOrigins] reconstructs the original source by
// concatenating [token.Token.Origin] fields.
//
// # Testing Styled Output
//
// niceyaml applies terminal styles to YAML syntax elements.
// Testing styled output with raw ANSI codes is often unreadable.
// [XMLStyles] replaces escape codes with XML-like tags:
//
//	styles := yamltest.NewXMLStyles()
//	// Input "key: value" produces:
//	// <name-tag>key</name-tag><punctuation-mapping-value>:</punctuation-mapping-value>\
//	// <text> </text><literal-string>value</literal-string>
//
// # Creating Test Documents
//
// [FirstDocument] and [FirstDocumentWithPath] create [*niceyaml.DocumentDecoder]
// instances for testing schema matchers and validators:
//
//	doc := yamltest.FirstDocument(t, "kind: Deployment")
//	docWithPath := yamltest.FirstDocumentWithPath(t, "on: push", ".github/workflows/ci.yaml")
//
// # Mocking Dependencies
//
// [MockSchemaValidator] and [MockNormalizer] let you test code paths that
// depend on validation or normalization without wiring up real implementations:
//
//	passing := yamltest.NewPassingSchemaValidator()
//	failing := yamltest.NewFailingSchemaValidator(errors.New("invalid"))
//	normalizer := yamltest.NewIdentityNormalizer()
package yamltest
