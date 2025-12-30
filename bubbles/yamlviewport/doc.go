// Package yamlviewport provides a Bubble Tea component for viewing and
// navigating YAML data structures.
//
// The main type is [Model], which implements [tea.Model] for use with Bubble
// Tea applications. It supports:
//
//   - Revision history with navigation between versions
//   - Three diff modes ([DiffModeAdjacent], [DiffModeOrigin], [DiffModeNone])
//   - Search highlighting via the [Finder] interface
//   - Keyboard and mouse navigation via [KeyMap]
//   - Syntax highlighting via [niceyaml.Printer]
//
// Create a new viewport with [New] and configure it using functional options
// like [WithPrinter], [WithStyle], and [WithSearchStyle]. Content is set via
// [Model.SetTokens] or [Model.AppendRevision] using [niceyaml.Source] values.
package yamlviewport
