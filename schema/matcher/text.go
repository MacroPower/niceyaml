package matcher

import (
	"context"
	"reflect"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// textMatcher matches documents by the text of a single YAML scalar.
type textMatcher struct {
	match func(text string) bool
	path  paths.Path
}

// Text creates a new [Matcher] that matches documents whose scalar at
// path holds text that match accepts.
//
// Match calls match with the string the scalar holds, quoted or plain.
// A plain number or bool keeps its spelling, so swagger: 2.0 gives "2.0"
// and python: 3.10 gives "3.10", where a decode into a string gives "2"
// and "3.1". A quoted scalar gives the characters its escapes name, so
// "a\tb" gives a tab between its letters. A block scalar gives its lines
// with the line breaks its header keeps, so a `|` scalar that holds 1.10
// gives "1.10\n". A tag that makes a quoted scalar a number or a bool
// respells it, so !!float "1.10" gives "1.1".
//
// A null, a mapping, and a sequence hold no text, so none of them
// matches and Match calls match for none. The same goes for a document
// without the path. The path resolves as it does for [Content]. Match
// returns an error where [Content] does, and it refuses the documents
// that [Content] refuses for their aliases.
//
// Panics if match is nil.
//
//	// Matches apiVersion: apps/v1 and apiVersion: apps/v1beta1.
//	matcher.Text(paths.Doc().Child("apiVersion"), func(text string) bool {
//	    return strings.HasPrefix(text, "apps/")
//	})
//
//	// Matches swagger: 2.0 and swagger: "2.0".
//	matcher.Text(paths.Doc().Child("swagger"), func(text string) bool {
//	    return strings.HasPrefix(text, "2.")
//	})
func Text(path paths.Path, match func(text string) bool) Matcher {
	if match == nil {
		panic("matcher.Text: match is nil")
	}

	return &textMatcher{path: path, match: match}
}

// Match implements [Matcher].
func (m *textMatcher) Match(ctx context.Context, doc *niceyaml.Node) (bool, error) {
	node, raw, err := readScalar(ctx, doc, m.path, reflect.TypeFor[string]())
	if err != nil || raw == nil {
		return false, err
	}

	// A decode that fails means the node holds no text, as a mapping or
	// a sequence does not, which is a no rather than a failure.
	decoded, err := decodeScalar[string](ctx, node)
	if declines(err) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return m.match(writtenText(node, decoded)), nil
}
