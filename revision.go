package niceyaml

// Revisions is the ordered history of one document. Index 0 holds the
// original [*Source] and the last element holds the latest. It is a plain
// slice, so append adds a revision and indexing reads one:
//
//	revs := niceyaml.Revisions{original}
//	revs = append(revs, modified)
//	result := niceyaml.Diff(revs[0], revs[1])
//
// A single revision is valid; multiple revisions are not required.
type Revisions []*Source

// Len returns the number of revisions.
func (r Revisions) Len() int {
	return len(r)
}

// At returns the revision at the given zero-based index, or nil when the
// index is outside the history.
func (r Revisions) At(index int) *Source {
	if index < 0 || index >= len(r) {
		return nil
	}

	return r[index]
}

// Names returns the names of all revisions in order from the original to the
// latest.
func (r Revisions) Names() []string {
	if len(r) == 0 {
		return nil
	}

	names := make([]string, len(r))
	for i, s := range r {
		names[i] = s.Name()
	}

	return names
}
