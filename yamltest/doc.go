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
// This package provides assertion helpers that show exactly which fields differ:
//
//	yamltest.RequireTokensValid(t, want, got)  // Ensures non-nil tokens/positions.
//	yamltest.AssertTokensEqual(t, want, got)   // Compares all fields with diffs.
//
// See [RequireTokensValid] and [AssertTokensEqual] for details.
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
// # Mocking Dependencies
//
// [MockSchemaValidator] and [MockNormalizer] let you test code paths that
// depend on validation or normalization without wiring up real implementations:
//
//	passing := yamltest.NewPassingSchemaValidator()
//	failing := yamltest.NewFailingSchemaValidator(errors.New("invalid"))
//	normalizer := yamltest.NewIdentityNormalizer()
package yamltest
