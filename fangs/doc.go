// Package fangs provides utilities for integrating niceyaml with
// [github.com/charmbracelet/fang], a Cobra companion library.
//
// The primary export is [ErrorHandler], a custom error handler that preserves
// multi-line error formatting. This is particularly useful for niceyaml errors,
// which include source context and annotations spanning multiple lines.
package fangs
