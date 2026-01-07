package niceyaml

import (
	"log/slog"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/macropower/niceyaml/position"
)

// Normalizer transforms strings for comparison (e.g., removing diacritics).
// See [StandardNormalizer] for an implementation.
type Normalizer interface {
	Normalize(in string) string
}

// StandardNormalizer removes diacritics and lowercases strings for
// case-insensitive matching. For example, "Ö" becomes "o".
// Note that [unicode.Mn] is the unicode key for nonspacing marks.
// Create instances with [NewStandardNormalizer].
type StandardNormalizer struct{}

// NewStandardNormalizer creates a new [StandardNormalizer].
func NewStandardNormalizer() StandardNormalizer {
	return StandardNormalizer{}
}

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
// Create instances with [NewFinder], then call [Finder.Load] to provide
// the source data. The Finder preprocesses the source when loaded,
// allowing efficient repeated searches with different search strings.
type Finder struct {
	normalizer Normalizer
	prevLines  LineIterator
	posMap     *positionMap
	source     string
	mu         sync.RWMutex
}

// NewFinder creates a new [Finder] with the given options.
// Call [Finder.Load] to provide a [LineIterator] before searching.
// By default, no normalization is applied. Use [WithNormalizer] to enable
// case-insensitive or diacritic-insensitive matching.
func NewFinder(opts ...FinderOption) *Finder {
	f := &Finder{}
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

// Load preprocesses the given [LineIterator], building the internal source
// string and position map for searching. This method must be called
// before using [Finder.Find].
func (f *Finder) Load(lines LineIterator) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.prevLines == lines {
		return
	}

	f.prevLines = lines
	f.source, f.posMap = f.buildSourceAndPositionMap(lines)
}

// Find finds all occurrences of the search string in the preprocessed source.
// It returns a slice of [position.Range] indicating the start and end positions of each match.
// Positions are 0-indexed. The slice is provided in the order the matches appear in the source.
// Returns nil if the search string is empty or the finder has no source data.
func (f *Finder) Find(search string) []position.Range {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if search == "" || f.source == "" {
		return nil
	}

	// Normalize search string if normalizer is set.
	// Source is already normalized during construction.
	searchStr := search
	if f.normalizer != nil {
		searchStr = f.normalizer.Normalize(search)
	}

	var results []position.Range

	offset := 0
	for {
		idx := strings.Index(f.source[offset:], searchStr)
		if idx == -1 {
			break
		}

		matchStart := offset + idx
		matchEnd := matchStart + len(searchStr)

		// Convert byte offsets to character offsets for position map lookup.
		matchStartChar := utf8.RuneCountInString(f.source[:matchStart])
		matchEndChar := matchStartChar + utf8.RuneCountInString(searchStr) - 1

		startPos := f.posMap.lookup(matchStartChar)
		endPos := f.posMap.lookup(matchEndChar)
		// End column is exclusive, so add 1.
		endPos.Col++

		results = append(results, position.Range{Start: startPos, End: endPos})
		offset = matchEnd
	}

	return results
}

// buildSourceAndPositionMap concatenates all token Origins and builds a position map.
// When a normalizer is set, the returned source is normalized and the position map
// maps normalized character indices to original positions. This ensures correct
// position lookup when searching in normalized text.
func (f *Finder) buildSourceAndPositionMap(lines LineIterator) (string, *positionMap) {
	var sb strings.Builder

	pm := &positionMap{}

	if lines == nil || lines.IsEmpty() {
		return "", pm
	}

	normalizedCharIndex := 0

	// Cache normalized forms per unique rune to avoid repeated transform calls.
	var normalizedCache map[rune]string
	if f.normalizer != nil {
		normalizedCache = make(map[rune]string)
	}

	for pos, r := range lines.Runes() {
		// Get normalized form of this rune (or original if no normalizer).
		var normalized string
		if f.normalizer != nil {
			if cached, ok := normalizedCache[r]; ok {
				normalized = cached
			} else {
				normalized = f.normalizer.Normalize(string(r))
				normalizedCache[r] = normalized
			}
		} else {
			normalized = string(r)
		}

		// Map each normalized char back to original position.
		for _, nr := range normalized {
			pm.add(normalizedCharIndex, pos)
			sb.WriteRune(nr)

			normalizedCharIndex++
		}
	}

	return sb.String(), pm
}

// positionMap maps character indices in a concatenated string to original [position.Position] values.
type positionMap struct {
	indices   []int
	positions []position.Position
}

// add records a character index and its corresponding position.
func (m *positionMap) add(charIndex int, pos position.Position) {
	m.indices = append(m.indices, charIndex)
	m.positions = append(m.positions, pos)
}

// lookup finds the [position.Position] for a given character index using binary search.
func (m *positionMap) lookup(charIndex int) position.Position {
	if len(m.indices) == 0 {
		return position.New(0, 0)
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
