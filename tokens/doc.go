// Package tokens provides utilities for organizing YAML tokens into lines
// with support for revision tracking and unified diff generation.
//
// # Line Management
//
// The core abstraction is [Lines], which organizes YAML tokens by line number.
// A [Lines] value can be created from various sources:
//
//   - [NewLinesFromString]: Parse YAML from a string
//   - [NewLinesFromFile]: Extract tokens from an [ast.File]
//   - [NewLinesFromToken]: Build from a single token
//   - [NewLinesFromTokens]: Build from a token slice
//
// Each [Line] contains the tokens for that line, along with optional metadata:
//
//   - [Annotation]: Extra content such as error messages or diff headers
//   - [Flag]: Category markers for diff display ([FlagInserted], [FlagDeleted])
//
// # Revision Tracking
//
// [Revision] represents a version of [Lines] in a doubly-linked chain,
// enabling navigation through document history:
//
//	origin := tokens.NewRevision(original)
//	tip := origin.Append(modified)
//	tip.Origin() // returns origin
//
// # Diff Generation
//
// [FullDiff] and [SummaryDiff] compute differences between two revisions
// using a longest common subsequence (LCS) algorithm:
//
//	full := tokens.NewFullDiff(origin, tip)
//	full.Lines()  // all lines with inserted/deleted flags
//
//	summary := tokens.NewSummaryDiff(origin, tip, 3)
//	summary.Lines()  // changed lines with 3 lines of context
//
// The summary output follows unified diff format with hunk headers.
package tokens
