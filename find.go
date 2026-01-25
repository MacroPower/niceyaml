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

	"jacobcolvin.com/niceyaml/position"
)

// Normalizer transforms strings for comparison (e.g., removing diacritics).
//
// See [StandardNormalizer] for an implementation.
type Normalizer interface {
	Normalize(in string) string
}

// StandardNormalizer removes diacritics and lowercases strings for
// case-insensitive matching. For example, "Ö" becomes "o".
// Note that [unicode.Mn] is the unicode key for nonspacing marks.
// Create instances with [NewStandardNormalizer].
type StandardNormalizer struct {
	transformer transform.Transformer
}

// NewStandardNormalizer creates a new [*StandardNormalizer].
func NewStandardNormalizer() *StandardNormalizer {
	return &StandardNormalizer{
		transformer: transform.Chain(
			norm.NFD,
			runes.Remove(runes.In(unicode.Mn)),
			norm.NFC,
			cases.Lower(language.Und),
		),
	}
}

// Normalize implements [Normalizer].
func (n *StandardNormalizer) Normalize(in string) string {
	n.transformer.Reset()

	out, _, err := transform.String(n.transformer, in)
	if err != nil {
		slog.Debug("normalize string", slog.Any("error", err))
		return in
	}

	return out
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
// your source data; this builds an internal index that maps character
// positions in the searchable text back to [position.Position] values in the
// original. Subsequent calls to [Finder.Find] use this index for efficient
// lookups without re-parsing.
//
// Finder is safe for concurrent use. Multiple goroutines may call
// [Finder.Find] simultaneously, and [Finder.Load] uses locking to safely
// update internal state.
//
// Example:
//
//	// Create finder with case-insensitive matching.
//	finder := niceyaml.NewFinder(
//		niceyaml.WithNormalizer(niceyaml.NewStandardNormalizer()),
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
// Use [WithNormalizer] with [StandardNormalizer] for case-insensitive matching
// that also ignores diacritics (e.g., "cafe" matches "Café").
//
// Create instances with [NewFinder].
type Finder struct {
	normalizer Normalizer
	prevLines  LineIterator
	posMap     *positionMap
	source     string
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
// the search string and source text before matching.
//
// See [StandardNormalizer] for an implementation.
func WithNormalizer(normalizer Normalizer) FinderOption {
	return func(f *Finder) {
		f.normalizer = normalizer
	}
}

// Load preprocesses the given [LineIterator], building the internal source
// string and position map for searching.
//
// This method must be called before using [Finder.Find].
func (f *Finder) Load(lines LineIterator) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.prevLines == lines {
		return
	}

	f.prevLines = lines
	f.source, f.posMap = f.buildSourceAndPositionMap(lines)
	f.buildByteToRuneIndex()
}

// buildByteToRuneIndex builds a lookup table mapping byte offsets to rune counts.
// This enables O(1) byte-to-rune conversion during Find instead of O(n) scanning.
func (f *Finder) buildByteToRuneIndex() {
	if f.source == "" {
		f.byteToRune = nil
		return
	}

	f.byteToRune = make([]int, len(f.source)+1)
	runeCount := 0

	for i := 0; i < len(f.source); {
		f.byteToRune[i] = runeCount
		_, size := utf8.DecodeRuneInString(f.source[i:])
		i += size
		runeCount++
	}

	f.byteToRune[len(f.source)] = runeCount
}

// Find finds all occurrences of the search string in the preprocessed source.
//
// It returns a slice of [position.Range] indicating the start and end positions
// of each match. The slice is provided in the order the matches appear in the
// source.
//
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

	searchRuneCount := utf8.RuneCountInString(searchStr)

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

// buildSourceAndPositionMap concatenates all token Origins and builds a
// position map.
//
// When a normalizer is set, the returned source is normalized and the position
// map maps normalized character indices to original positions.
//
// This ensures correct position lookup when searching in normalized text.
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
// [position.Position] values in the source lines.
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
