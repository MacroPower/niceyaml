// Package yamltest provides helpers for testing niceyaml and/or go-yaml.
//
// It includes utilities for:
//   - Building test tokens with [TokenBuilder]
//   - Comparing [token.Tokens] field by field
//   - Formatting tokens for debug output
//   - Mocking schema validators with [MockSchemaValidator] and normalizers with [MockNormalizer]
//   - Processing test want/got strings with [Input], [JoinLF], and [JoinCRLF]
//   - Normalizing content for string comparison
//   - Rendering styled output for tests with [XMLStyles]
package yamltest
