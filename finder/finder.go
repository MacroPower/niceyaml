// Package finder locates strings within [line.View] content.
//
// A [Finder] maps matches back to [position.Ranges] in the original lines,
// even when normalization changes the character count, so the ranges can
// highlight matches in rendered output. Create one with [New], build an
// [Index] over a view once with [Finder.Load], and search the index any
// number of times with [Index.Find]:
//
//	f := finder.New(finder.WithNormalizer(normalizer.New()))
//	view := source.Lines()
//	idx := f.Load(view)
//	view.BlendOverlay(style.GenericHighlight, idx.Find("search term")...)
//
// Searches are exact by default. [WithNormalizer] applies a [Normalizer] to
// both the loaded text and the search string, one character at a time, and
// the normalizer package provides one that folds case and strips diacritics.
package finder

import (
	"sort"
	"strings"
	"unicode/utf8"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
)

// Normalizer transforms strings for comparison (e.g., removing diacritics).
//
// See [normalizer.Normalizer] for an implementation.
type Normalizer interface {
	Normalize(in string) string
}

// Finder builds an [Index] over [line.View] content, so the [position.Ranges]
// of a search can highlight matches in rendered output.
//
// The typical use case is search-as-you-type highlighting: the user views
// YAML content and types a search term, and matching text is highlighted in
// place.
//
// Finder uses a load-once, search-many design. [Finder.Load] reads the
// lines once and returns an [Index] that maps character positions in the
// search text back to [position.Position] values in the original lines, and
// [Index.Find] uses that map on every call without re-reading the lines.
//
// A Finder holds only its settings and never changes after [New], so it is
// safe for concurrent use, as is every Index it builds.
//
// Example:
//
//	f := finder.New(
//		finder.WithNormalizer(normalizer.New()),
//	)
//	view := source.Lines()
//	idx := f.Load(view)
//	view.AddOverlay(highlightStyle, idx.Find("search term")...)
//	fmt.Println(p.Print(view))
//
// By default, searches are exact (case-sensitive, no normalization).
//
// Use [WithNormalizer] with [normalizer.Normalizer] for case-insensitive
// matching that also ignores diacritics (e.g., "cafe" matches "Café").
//
// Create instances with [New].
type Finder struct {
	normalizer Normalizer
}

// New creates a new [*Finder].
// Call [Finder.Load] to build an [Index] over a [line.View] before searching.
//
// By default, no normalization is applied. Use [WithNormalizer] to enable
// case-insensitive or diacritic-insensitive matching.
func New(opts ...Option) *Finder {
	f := &Finder{}
	for _, opt := range opts {
		opt(f)
	}

	return f
}

// Option configures a [Finder].
//
// Available options:
//   - [WithNormalizer]
type Option func(*Finder)

// WithNormalizer is a [Option] that sets a [Normalizer] applied to both
// the search string and the loaded text before matching.
//
// The normalizer receives one character at a time, on both sides, so the
// same character always normalizes the same way wherever it appears. A
// transformer whose output depends on surrounding characters, such as title
// casing, sees none and behaves as it would on a one-character string.
//
// See [normalizer.Normalizer] for an implementation.
func WithNormalizer(normalizer Normalizer) Option {
	return func(f *Finder) {
		f.normalizer = normalizer
	}
}

// Load reads the given [line.View] and returns an [Index] over it, built
// from the search text and a map from its positions back to the lines.
//
// Each call builds a new Index and leaves the Finder as it was, so load once
// per distinct content and call [Index.Find] as many times as needed.
// Overlays do not affect the index, so highlighting matches does not require
// reloading.
func (f *Finder) Load(lines line.View) *Index {
	idx := &Index{normalizer: f.normalizer}
	idx.text, idx.posMap = f.buildTextAndPositionMap(lines)
	idx.buildByteToRuneIndex()

	return idx
}

// Index is the search text of one [line.View] together with the map from
// its characters back to [position.Position] values in the lines. It never
// changes after [Finder.Load] builds it, so it is safe for concurrent use.
//
// Create instances with [Finder.Load].
type Index struct {
	normalizer Normalizer
	posMap     *positionMap
	text       string
	byteToRune []int
}

// buildByteToRuneIndex builds a lookup table mapping byte offsets to rune counts.
// This enables O(1) byte-to-rune conversion during Find instead of O(n) scanning.
func (i *Index) buildByteToRuneIndex() {
	if i.text == "" {
		i.byteToRune = nil
		return
	}

	i.byteToRune = make([]int, len(i.text)+1)
	runeCount := 0

	for b := 0; b < len(i.text); {
		i.byteToRune[b] = runeCount
		_, size := utf8.DecodeRuneInString(i.text[b:])
		b += size
		runeCount++
	}

	i.byteToRune[len(i.text)] = runeCount
}

// Find finds all occurrences of the search string in the indexed text.
//
// It returns the [position.Ranges] of each match, in the order the matches
// appear in the text.
//
// The search string goes through the same per-character normalization as
// the loaded text, so a string found in the source is found by Find. Bytes
// that are not valid UTF-8 read as U+FFFD on both sides.
//
// Returns nil if the search string is empty, or normalizes to empty, or the
// Index is nil or holds no text.
func (i *Index) Find(search string) position.Ranges {
	if i == nil || search == "" || i.text == "" {
		return nil
	}

	searchStr := i.normalizeText(search)

	// A search of only combining marks normalizes to nothing, and an empty
	// needle would match at every offset without advancing.
	if searchStr == "" {
		return nil
	}

	searchRuneCount := utf8.RuneCountInString(searchStr)

	var results position.Ranges

	offset := 0
	for {
		idx := strings.Index(i.text[offset:], searchStr)
		if idx == -1 {
			break
		}

		matchStart := offset + idx
		matchEnd := matchStart + len(searchStr)

		// Convert byte offsets to character offsets for position map lookup.
		matchStartChar := i.byteToRune[matchStart]
		matchEndChar := matchStartChar + searchRuneCount - 1

		startPos := i.posMap.lookup(matchStartChar)
		endPos := i.posMap.lookup(matchEndChar)
		// End column is exclusive, so add 1.
		endPos.Col++

		results = append(results, position.Range{Start: startPos, End: endPos})
		offset = matchEnd
	}

	return results
}

// normalizeText normalizes s the way [Finder.Load] normalizes the loaded
// text, one rune at a time, so a search string and the text it is compared
// against pass through the normalizer identically.
func (i *Index) normalizeText(s string) string {
	var sb strings.Builder

	for _, r := range s {
		sb.WriteString(normalizeRune(i.normalizer, r))
	}

	return sb.String()
}

// normalizeRune returns the search text for one rune: the rune itself when
// n is nil, and its normalized form otherwise.
func normalizeRune(n Normalizer, r rune) string {
	if n == nil {
		return string(r)
	}

	return n.Normalize(string(r))
}

// buildTextAndPositionMap concatenates all token Origins into the search text
// and builds a position map.
//
// When a normalizer is set, it normalizes the returned text, and the position
// map maps normalized character indices to original positions so lookups in
// normalized text resolve to the right place.
func (f *Finder) buildTextAndPositionMap(lines line.View) (string, *positionMap) {
	var sb strings.Builder

	pm := &positionMap{}

	if lines == nil || lines.Len() == 0 {
		return "", pm
	}

	normalizedCharIndex := 0

	// Cache normalized forms per unique rune to avoid repeated transform calls.
	normalizedCache := make(map[rune]string)

	for pos, r := range lines.AllRunes() {
		normalized, ok := normalizedCache[r]
		if !ok {
			normalized = normalizeRune(f.normalizer, r)
			normalizedCache[r] = normalized
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

// positionMap maps character indices in a concatenated string to original
// [position.Position] values in the loaded lines.
type positionMap struct {
	indices   []int
	positions []position.Position
}

// add records a character index and its corresponding position.
func (m *positionMap) add(charIndex int, pos position.Position) {
	m.indices = append(m.indices, charIndex)
	m.positions = append(m.positions, pos)
}

// lookup finds the [position.Position] for a given character index using
// binary search.
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
