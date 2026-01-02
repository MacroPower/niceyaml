// Package yamltest provides helpers for testing niceyaml and/or go-yaml.
//
// It includes utilities for:
//   - Building test tokens with [TokenBuilder]
//   - Comparing [token.Tokens] field by field
//   - Formatting tokens for debug output
//   - Mocking validators with [MockValidator] and normalizers with [MockNormalizer]
//   - Processing test input strings with [Input]
//   - Normalizing content for string comparison
package yamltest
