// Package yamlscan is a copy of the scanner of go-yaml.
//
// The copy holds go-yaml's scanner package and the Tokenize function of its
// lexer package at the commit that UPSTREAM_COMMIT names. It emits the
// [github.com/goccy/go-yaml/token.Token] values of the go-yaml module that
// go.mod requires, so the commit of the copy must match that version.
// LICENSE holds the license of go-yaml.
//
// # Sync
//
// The sync.sh script merges a new commit of go-yaml into the copy. It
// rewrites only the package clause of each file and the reference of
// Tokenize to the Scanner. Each patch to the copy is a commit of niceyaml
// that changes the files that sync.sh writes. So the patch series sits on
// top of the unchanged copy. The history of go-yaml holds the unchanged copy
// of each commit, and sync.sh reads it from there. To sync the copy:
//
//  1. Check out the new commit in a clone of go-yaml whose history also
//     holds the commit that UPSTREAM_COMMIT names.
//  2. Require the new commit of go-yaml in go.mod. The copy of go-yaml's
//     decoder in internal/yamldecode builds on the same module, so sync that
//     copy too.
//  3. Run sync.sh with the path of the clone. Its merge takes the unchanged
//     copy of the old commit as the base. One side is the copy in this
//     package, and the other side is the unchanged copy of the new commit.
//     Resolve each conflict that sync.sh reports.
//  4. Run the tests of this package. The corpus test reads each input under
//     testdata/corpus with the copy and with the lexer of go-yaml.
package yamlscan
