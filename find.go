package niceyaml

import (
	"log/slog"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Normalizer transforms strings for comparison (e.g., removing diacritics).
type Normalizer interface {
	Normalize(in string) string
}

// StandardNormalizer removes diacritics and lowercases strings for
// case-insensitive matching. For example, "Ö" becomes "o".
// Note that [unicode.Mn] is the unicode key for nonspacing marks.
type StandardNormalizer struct{}

// Normalize implements [Normalizer].
func (StandardNormalizer) Normalize(in string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC, cases.Lower(language.Und))
	out, _, err := transform.String(t, in)
	if err != nil {
		slog.Debug("normalize string: %w", slog.Any("error", err))
		return in
	}

	return out
}

// Finder searches for strings within YAML tokens.
// Use [NewFinder] to create an instance with optional configuration.
type Finder struct {
	normalizer Normalizer
	search     string
}

// NewFinder creates a new [Finder] with the given search string and options.
// By default, no normalization is applied to search strings.
func NewFinder(search string, opts ...FinderOption) *Finder {
	f := &Finder{search: search}

	for _, opt := range opts {
		opt(f)
	}

	return f
}

// FinderOption configures a [Finder].
type FinderOption func(*Finder)

// WithNormalizer sets a [Normalizer] applied to both the search string
// and source text before matching. See [StandardNormalizer] for an example.
func WithNormalizer(normalizer Normalizer) FinderOption {
	return func(f *Finder) {
		f.normalizer = normalizer
	}
}

// Find finds all occurrences of the search string in the provided lines.
// It returns a slice of PositionRange indicating the start and end positions of each match.
// The slice is provided in the order the matches appear in the lines.
func (f *Finder) Find(lines *Lines) []PositionRange {
	if f.search == "" || lines == nil || lines.IsEmpty() {
		return nil
	}

	// Build concatenated source and position map.
	// When a normalizer is set, source is already normalized and posMap maps
	// normalized character indices to original positions.
	source, posMap := f.buildSourceAndPositionMap(lines)
	if source == "" {
		return nil
	}

	// Normalize search string if normalizer is set.
	// Source is already normalized by buildSourceAndPositionMap.
	searchStr := f.search
	if f.normalizer != nil {
		searchStr = f.normalizer.Normalize(f.search)
	}

	var results []PositionRange

	offset := 0
	for {
		idx := strings.Index(source[offset:], searchStr)
		if idx == -1 {
			break
		}

		matchStart := offset + idx
		matchEnd := matchStart + len(searchStr)

		// Convert byte offsets to character offsets for position map lookup.
		matchStartChar := utf8.RuneCountInString(source[:matchStart])
		matchEndChar := matchStartChar + utf8.RuneCountInString(searchStr) - 1

		startPos := posMap.lookup(matchStartChar)
		endPos := posMap.lookup(matchEndChar)
		// End column is exclusive, so add 1.
		endPos.Col++

		results = append(results, PositionRange{Start: startPos, End: endPos})
		offset = matchEnd
	}

	return results
}

// buildSourceAndPositionMap concatenates all token Origins and builds a position map.
// When a normalizer is set, the returned source is normalized and the position map
// maps normalized character indices to original positions. This ensures correct
// position lookup when searching in normalized text.
func (f *Finder) buildSourceAndPositionMap(lines *Lines) (string, *positionMap) {
	var sb strings.Builder

	pm := &positionMap{}

	if lines == nil || lines.IsEmpty() {
		return "", pm
	}

	normalizedCharIndex := 0

	lines.EachRune(func(r rune, pos Position) {
		// Get normalized form of this rune (or original if no normalizer).
		var normalized string
		if f.normalizer != nil {
			normalized = f.normalizer.Normalize(string(r))
		} else {
			normalized = string(r)
		}

		// Map each normalized char back to original position.
		for _, nr := range normalized {
			pm.add(normalizedCharIndex, pos)
			sb.WriteRune(nr)

			normalizedCharIndex++
		}
	})

	return sb.String(), pm
}

// positionMap maps character indices in a concatenated string to original Position values.
type positionMap struct {
	indices   []int
	positions []Position
}

// add records a character index and its corresponding position.
func (m *positionMap) add(charIndex int, pos Position) {
	m.indices = append(m.indices, charIndex)
	m.positions = append(m.positions, pos)
}

// lookup finds the Position for a given character index using binary search.
func (m *positionMap) lookup(charIndex int) Position {
	if len(m.indices) == 0 {
		return Position{Line: 1, Col: 1}
	}

	// Find the largest index that is <= the target index.
	idx := sort.Search(len(m.indices), func(i int) bool {
		return m.indices[i] > charIndex
	})
	if idx > 0 {
		idx--
	}

	return m.positions[idx]
}
