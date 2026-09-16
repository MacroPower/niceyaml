// Package revision keeps the versions of a document in order.
//
// A [History] holds the [*niceyaml.Source] values of one document from the
// original to the latest. It is a plain slice, so append adds a revision,
// indexing reads one, and any two revisions can be handed to the differ
// package:
//
//	revs := revision.History{original, modified}
//	result := differ.Diff(revs[0], revs[1])
package revision

import "go.jacobcolvin.com/niceyaml"

// History is the ordered revisions of one document. Index 0 holds the
// original [*niceyaml.Source] and the last element holds the latest. It is a
// plain slice, so append adds a revision and indexing reads one:
//
//	revs := revision.History{original}
//	revs = append(revs, modified)
//	result := differ.Diff(revs[0], revs[1])
//
// A single revision is valid; multiple revisions are not required.
type History []*niceyaml.Source

// Len returns the number of revisions.
func (r History) Len() int {
	return len(r)
}

// At returns the revision at the given zero-based index, or nil when the
// index is outside the history.
func (r History) At(index int) *niceyaml.Source {
	if index < 0 || index >= len(r) {
		return nil
	}

	return r[index]
}

// Names returns the names of all revisions in order from the original to the
// latest.
func (r History) Names() []string {
	if len(r) == 0 {
		return nil
	}

	names := make([]string, len(r))
	for i, s := range r {
		names[i] = s.Name()
	}

	return names
}
