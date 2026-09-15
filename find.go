package niceyaml

import (
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"go.jacobcolvin.com/niceyaml/position"
)

// Normalizer transforms strings for comparison (e.g., removing diacritics).
//
// See [normalizer.Normalizer] for an implementation.
type Normalizer interface {
	Normalize(in string) string
}

// Finder finds strings within YAML tokens, returning [position.Ranges] that can
// be used to highlight matches in rendered output.
//
// The typical use case is search-as-you-type highlighting: the user views YAML
// content and types a search term, and matching text is highlighted in place.
//
// [Finder] solves the challenge of mapping string matches back to their
// original line and column positions, even when normalization (case folding,
// diacritic removal) changes the character count.
//
// Finder uses a load-once, search-many design. Call [Finder.Load] once with
// the lines to search. Load builds an index that maps character positions in
// the search text back to [position.Position] values in the original lines,
// and [Finder.Find] uses that index on every call without re-parsing.
//
// Finder is safe for concurrent use. Multiple goroutines may call
// [Finder.Find] simultaneously, and [Finder.Load] uses locking to safely
// update internal state.
//
// Example:
//
//	// Create finder with case-insensitive matching.
//	finder := niceyaml.NewFinder(
//		niceyaml.WithNormalizer(normalizer.New()),
//	)
//	finder.Load(source)
//
//	// Find matches and highlight them.
//	for _, rng := range finder.Find("search term") {
//		source.AddOverlay(highlightStyle, rng)
//	}
//	fmt.Println(printer.Print(source))
//
// By default, searches are exact (case-sensitive, no normalization).
//
// Use [WithNormalizer] with [normalizer.Normalizer] for case-insensitive
// matching that also ignores diacritics (e.g., "cafe" matches "Café").
//
// Create instances with [NewFinder].
type Finder struct {
	normalizer Normalizer
	posMap     *positionMap
	text       string
	byteToRune []int
	mu         sync.RWMutex
}

// NewFinder creates a new [*Finder].
// Call [Finder.Load] to provide a [LineIterator] before searching.
//
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
//
// Available options:
//   - [WithNormalizer]
type FinderOption func(*Finder)

// WithNormalizer is a [FinderOption] that sets a [Normalizer] applied to both
// the search string and the loaded text before matching.
//
// See [normalizer.Normalizer] for an implementation.
func WithNormalizer(normalizer Normalizer) FinderOption {
	return func(f *Finder) {
		f.normalizer = normalizer
	}
}

// Load preprocesses the given [LineIterator], building the search text and
// position map.
//
// Every call rebuilds the index, so call Load once per distinct content and
// [Finder.Find] as many times as needed. Overlays do not affect the index, so
// highlighting matches does not require reloading.
//
// This method must be called before using [Finder.Find].
func (f *Finder) Load(lines LineIterator) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.text, f.posMap = f.buildTextAndPositionMap(lines)
	f.buildByteToRuneIndex()
}

// buildByteToRuneIndex builds a lookup table mapping byte offsets to rune counts.
// This enables O(1) byte-to-rune conversion during Find instead of O(n) scanning.
func (f *Finder) buildByteToRuneIndex() {
	if f.text == "" {
		f.byteToRune = nil
		return
	}

	f.byteToRune = make([]int, len(f.text)+1)
	runeCount := 0

	for i := 0; i < len(f.text); {
		f.byteToRune[i] = runeCount
		_, size := utf8.DecodeRuneInString(f.text[i:])
		i += size
		runeCount++
	}

	f.byteToRune[len(f.text)] = runeCount
}

// Find finds all occurrences of the search string in the loaded text.
//
// It returns the [position.Ranges] of each match, in the order the matches
// appear in the text.
//
// Returns nil if the search string is empty or the finder has no loaded text.
func (f *Finder) Find(search string) position.Ranges {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if search == "" || f.text == "" {
		return nil
	}

	// Normalize search string if normalizer is set.
	// Source is already normalized during construction.
	searchStr := search
	if f.normalizer != nil {
		searchStr = f.normalizer.Normalize(search)
	}

	searchRuneCount := utf8.RuneCountInString(searchStr)

	var results position.Ranges

	offset := 0
	for {
		idx := strings.Index(f.text[offset:], searchStr)
		if idx == -1 {
			break
		}

		matchStart := offset + idx
		matchEnd := matchStart + len(searchStr)

		// Convert byte offsets to character offsets for position map lookup.
		matchStartChar := f.byteToRune[matchStart]
		matchEndChar := matchStartChar + searchRuneCount - 1

		startPos := f.posMap.lookup(matchStartChar)
		endPos := f.posMap.lookup(matchEndChar)
		// End column is exclusive, so add 1.
		endPos.Col++

		results = append(results, position.Range{Start: startPos, End: endPos})
		offset = matchEnd
	}

	return results
}

// buildTextAndPositionMap concatenates all token Origins into the search text
// and builds a position map.
//
// When a normalizer is set, it normalizes the returned text, and the position
// map maps normalized character indices to original positions so lookups in
// normalized text resolve to the right place.
func (f *Finder) buildTextAndPositionMap(lines LineIterator) (string, *positionMap) {
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

	for pos, r := range lines.AllRunes() {
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
