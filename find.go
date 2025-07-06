package niceyaml

import (
	"log/slog"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/goccy/go-yaml/token"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Finder searches for strings within YAML tokens.
// Use [NewFinder] to create an instance with optional configuration.
type Finder struct {
	normalizer func(string) string
}

// NewFinder creates a new [Finder] with the given options.
// By default, no normalization is applied to search strings.
func NewFinder(opts ...FinderOption) *Finder {
	f := &Finder{}

	for _, opt := range opts {
		opt(f)
	}

	return f
}

// FinderOption configures a [Finder].
type FinderOption func(*Finder)

// WithNormalizer sets a normalizer function applied to both the search string
// and source text before matching. See [StandardNormalizer] for an example.
func WithNormalizer(normalizer func(string) string) FinderOption {
	return func(f *Finder) {
		f.normalizer = normalizer
	}
}

// StandardNormalizer removes diacritics, e.g. "ö" becomes "o".
// Note that [unicode.Mn] is the unicode key for nonspacing marks.
func StandardNormalizer(in string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, in)
	if err != nil {
		slog.Debug("failed to normalize string", slog.Any("error", err))
		return in
	}

	return out
}

// FindStringsInTokens finds all occurrences of the given string in the provided tokens.
// It returns a slice of PositionRange indicating the start and end positions of each match.
// The slice is provided in the order the matches appear in the tokens.
func (f *Finder) FindStringsInTokens(in string, tokens token.Tokens) []PositionRange {
	if in == "" || len(tokens) == 0 {
		return nil
	}

	// Build concatenated source and position map.
	// The position map uses character indices (not byte indices) so it works
	// correctly with normalizers that preserve character counts but change byte lengths.
	source, posMap := f.buildSourceAndPositionMap(tokens)
	if source == "" {
		return nil
	}

	// Apply normalizer to search string if set.
	searchStr := in
	searchSource := source
	if f.normalizer != nil {
		searchStr = f.normalizer(in)
		searchSource = f.normalizer(source)
	}

	// Find all matches.
	var results []PositionRange

	offset := 0
	for {
		idx := strings.Index(searchSource[offset:], searchStr)
		if idx == -1 {
			break
		}

		matchStart := offset + idx
		matchEnd := matchStart + len(searchStr)

		// Convert byte offsets to character offsets for position map lookup.
		// This ensures correct mapping when normalizers change byte lengths.
		matchStartChar := utf8.RuneCountInString(searchSource[:matchStart])
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
// The position map uses character indices (not byte indices) so it works correctly
// with normalizers that preserve character counts but may change byte lengths.
func (f *Finder) buildSourceAndPositionMap(tokens token.Tokens) (string, *positionMap) {
	var sb strings.Builder

	pm := &positionMap{}

	if len(tokens) == 0 {
		return "", pm
	}

	pt := NewPositionTrackerFromTokens(tokens)

	// Track position continuously across all tokens using character indices.
	charIndex := 0

	for _, tk := range tokens {
		for _, r := range tk.Origin {
			// Record position for this character using character index.
			pm.add(charIndex, pt.Position())

			charIndex++

			sb.WriteRune(r)
			pt.Advance(r)
		}
	}

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
