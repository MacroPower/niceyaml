// Package finder locates strings within the lines of a [line.Sequence].
//
// A [Finder] maps matches back to [position.Ranges] in the original lines,
// even when normalization changes the character count, so the ranges can
// highlight matches in rendered output. Create one with [New], build an
// [Index] over the lines once with [Finder.Load], and search the index any
// number of times with [Index.Find]:
//
//	idx := finder.New().Load(source.Lines())
//	view := source.View()
//	view.BlendOverlay(kind.GenericHighlight, idx.Find("search term")...)
//
// Load takes the [line.Lines] of a whole file or a [*line.View]. A view
// loads the lines it holds, and the ranges of its matches are in the
// coordinates of its content, as every range a view takes is. A search of
// one document of a file that holds several therefore marks the view of
// that document:
//
//	view := doc.View()
//	idx := finder.New().Load(view)
//	view.BlendOverlay(kind.GenericHighlight, idx.Find("search term")...)
//
// A search folds case and ignores diacritics by default, through the
// [normalizer.Normalizer] that [normalizer.New] builds. [WithNormalizer]
// applies a [Normalizer] of the caller's own to both the loaded text and
// the search string, one character at a time. With a nil one, a search
// matches characters exactly and still reads invalid bytes and line
// endings as [Index.Find] describes.
package finder

import (
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"go.jacobcolvin.com/niceyaml/internal/nilness"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/normalizer"
	"go.jacobcolvin.com/niceyaml/position"
)

// Normalizer transforms strings for comparison (e.g., removing diacritics).
//
// A [Finder] and every [Index] it builds share one normalizer, so goroutines
// that load with the Finder or search any of those Indexes at the same time
// call Normalize concurrently. A normalizer used that way must be safe for
// concurrent use, as [normalizer.Normalizer] is.
//
// See [normalizer.Normalizer] for an implementation.
type Normalizer interface {
	Normalize(in string) string
}

// Finder builds an [Index] over the lines of a [line.Sequence], so the
// [position.Ranges] of a search can highlight matches in rendered output.
//
// The typical use case is search-as-you-type highlighting. The user views
// YAML content, types a search term, and sees matching text highlighted
// in place.
//
// Finder uses a load-once, search-many design. [Finder.Load] reads the
// lines once and returns an [Index] that maps offsets in the loaded text
// back to [position.Position] values in the original lines, and
// [Index.Find] uses that map on every call without re-reading the lines.
//
// A Finder holds only its settings and never changes after [New], so it is
// safe for concurrent use when its [Normalizer] is, as the default from
// [normalizer.New] is, and so is every Index it builds.
//
// Example:
//
//	idx := finder.New().Load(source.Lines())
//	view := source.View()
//	view.BlendOverlay(kind.GenericHighlight, idx.Find("search term")...)
//	lipgloss.Println(p.Print(view))
//
// By default, a search folds case and ignores diacritics, so "cafe"
// matches "Café", through the [normalizer.Normalizer] that
// [normalizer.New] builds. [WithNormalizer] sets a normalizer of the
// caller's own, and [WithNormalizer] with nil matches characters exactly,
// with no case folding or diacritic stripping.
//
// Create instances with [New].
type Finder struct {
	normalizer Normalizer
}

// New creates a new [*Finder].
// Call [Finder.Load] to build an [Index] over a [line.Sequence] before
// searching.
//
// Without options, the Finder normalizes with [normalizer.New], which
// folds case and strips diacritics. [WithNormalizer] replaces that
// normalizer, or removes it for exact matching.
func New(opts ...Option) *Finder {
	f := &Finder{normalizer: normalizer.New()}
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

// WithNormalizer is an [Option] that sets the [Normalizer] applied to
// both the search string and the loaded text before matching, in place
// of the one [normalizer.New] builds. A nil normalizer removes
// normalization, so a search then matches characters exactly:
//
//	exact := finder.New(finder.WithNormalizer(nil))
//
// A nil [*normalizer.Normalizer] works the same way, so a normalizer
// built only on some condition can pass through as it is:
//
//	var n *normalizer.Normalizer
//	if fold {
//		n = normalizer.New()
//	}
//
//	f := finder.New(finder.WithNormalizer(n))
//
// Even then, invalid bytes read as U+FFFD and line endings read as "\n",
// as [Index.Find] describes.
//
// The normalizer receives one character at a time, on both sides, so the
// same character always normalizes the same way wherever it appears. A
// character here is a unit that Unicode normalization composes as a whole:
// a rune with the combining marks that follow it, or the conjoining jamo of
// one Hangul syllable. A precomposed "ガ" and a "カ" followed by the voicing
// mark U+3099 each reach the normalizer whole, so a normalizer that composes
// them, as the default does, finds either spelling with the other. A
// transformer whose output depends on surrounding characters, such as title
// casing, sees none and behaves as it would on a one-character string.
// Line endings reach the normalizer as "\n" too, so a normalizer that
// drops "\n", such as one that removes all whitespace, lets a match run
// from the end of one line into the next.
//
// The [Finder] and every [Index] it builds share n, so goroutines that use
// any of them at once need a normalizer that is safe for concurrent use.
//
// See [normalizer.Normalizer] for an implementation.
func WithNormalizer(n Normalizer) Option {
	return func(f *Finder) {
		f.normalizer = n
	}
}

// Load reads the lines of a [line.Sequence] and returns an [Index] over
// them. The Index holds the loaded text and a map from its positions back
// to the lines.
//
// [line.Lines] loads every line. A [*line.View] loads the lines it holds,
// and [Index.Find] reports each match in the coordinates of the view's
// content, so the ranges apply to the view and to any other view over that
// content. A view holds whole lines. The view of a node that shares its
// line with other nodes, such as host in `server: {host: h, port: 80}`,
// loads that whole line, so a search of it for "port" finds a match there.
//
// A view can skip lines of its content, as the hunks of a diff do. No
// match runs from the line before the skipped lines into the line after
// them.
//
// Each call builds a new Index and leaves the Finder as it was, so load once
// per distinct content and call [Index.Find] as many times as needed. The
// lines never change, so the index stays valid however callers decorate the
// views over them. A nil sequence loads no lines, and so does one that
// holds a nil pointer, such as a nil [*line.View].
func (f *Finder) Load(lines line.Sequence) *Index {
	idx := &Index{normalizer: f.normalizer}
	idx.text, idx.posMap = f.buildTextAndPositionMap(lines)

	return idx
}

// Index is the loaded text of one [line.Sequence] together with the map
// from its characters back to [position.Position] values in the lines. It
// never changes after [Finder.Load] builds it, so it is safe for concurrent
// use when the Finder's [Normalizer] is. Every Index from one Finder shares
// that normalizer.
//
// Create instances with [Finder.Load].
type Index struct {
	normalizer Normalizer
	posMap     *positionMap
	text       string
}

// Find finds all occurrences of the search string in the loaded text.
//
// It returns the [position.Ranges] of each match, in the order the matches
// appear in the text.
//
// The search string goes through the same per-character normalization as
// the loaded text, so Find finds any run of whole characters that appears
// in the source, as [WithNormalizer] defines a character.
// Bytes that are not valid UTF-8 read as U+FFFD on both sides, and a CRLF
// or bare CR line ending reads as "\n" on both sides. Every loaded line but
// the last reads as ending in "\n", even one with no line ending of its
// own, such as the last line of a diff revision or a placeholder row. That
// "\n" keeps a match from joining two lines unless the normalizer drops it.
// A normalizer that removes all whitespace drops it, so "1b" then matches
// across the lines "a: 1" and "b: 2". Where the loaded lines skip lines of
// their content, as a [line.View] of hunks does, no match joins the lines
// on either side, whatever the normalizer drops.
//
// Every match starts at a source character, and every range covers whole
// characters, so an exact search for "cafe" in a "café" that spells its
// accent as a combining mark covers the accent too. When normalization
// expands one character into several, as case folding turns "ß" into "ss",
// a needle that matches only the tail of the expansion is not a match, and
// a match that ends inside an expansion covers the whole character. When
// it drops a character, as a normalizer that removes whitespace drops a
// space, a match that ends right before the dropped character covers it
// too, so the range ends where the next character begins.
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

	var results position.Ranges

	offset := 0
	for {
		idx := strings.Index(i.text[offset:], searchStr)
		if idx == -1 {
			break
		}

		matchStart := offset + idx
		matchEnd := matchStart + len(searchStr)

		// A match that starts inside the normalized form of one source
		// character has no entry to start at, and one that crosses a gap
		// covers lines the Index never loaded. Skip one rune of the loaded
		// text for either, so a later match that overlaps this one still
		// counts.
		start, ok := i.posMap.at(matchStart)
		if !ok || i.posMap.crossesGap(matchStart, matchEnd) {
			_, size := utf8.DecodeRuneInString(i.text[matchStart:])
			offset = matchStart + size

			continue
		}

		// The last byte of the match sits at matchEnd-1, and end finds the
		// source character that holds it.
		startPos := i.posMap.positions[start]
		endPos := i.posMap.end(matchEnd - 1)

		results = append(results, position.Range{Start: startPos, End: endPos})
		offset = matchEnd
	}

	return results
}

// normalizeText normalizes s the way [Finder.Load] normalizes the loaded
// text, one character at a time, so a search string and the text Find
// compares it against pass through the normalizer identically. Line endings
// collapse to "\n" first, since the loaded text reads every line ending that
// way.
func (i *Index) normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	var sb strings.Builder

	// The conversion reads each byte that is not valid UTF-8 as U+FFFD, as
	// the index reads the lines.
	eachCharacter([]rune(s), func(char string, _, _ int) {
		writeRunes(&sb, normalize(i.normalizer, char))
	})

	return sb.String()
}

// eachCharacter calls fn with each character of rs in order, along with the
// half-open range [start, end) of the indexes its runes hold in rs. A
// character is a unit that Unicode normalization composes as a whole: a rune
// with the combining marks that follow it, or the conjoining jamo of one
// Hangul syllable. A "\n" always ends a character, as it ends a line of the
// loaded text, so a search string splits the same way the lines do.
func eachCharacter(rs []rune, fn func(char string, start, end int)) {
	s := string(rs)
	start := 0

	for s != "" {
		n := norm.NFC.NextBoundaryInString(s, true)
		if i := strings.IndexByte(s[:n], '\n'); i >= 0 {
			n = i + 1
		}

		end := start + utf8.RuneCountInString(s[:n])
		fn(s[:n], start, end)

		s = s[n:]
		start = end
	}
}

// normalize returns the text for one character: the character itself when n
// is nil, and its normalized form otherwise.
func normalize(n Normalizer, char string) string {
	if n == nil {
		return char
	}

	return n.Normalize(char)
}

// writeRunes writes s to sb rune by rune, so a byte that is not valid UTF-8,
// which a normalizer can emit, becomes U+FFFD on both sides.
func writeRunes(sb *strings.Builder, s string) {
	for _, r := range s {
		sb.WriteRune(r)
	}
}

// buildTextAndPositionMap concatenates the runes of every line into the
// loaded text and builds a position map. [line.Line.Runes] yields every
// line ending as a single "\n", and every line but the last without an
// ending of its own gets a "\n" at column [line.Line.Width], where an
// ending would sit, so no match joins the text of two lines unless the
// normalizer drops "\n". The position map records a gap where a line
// begins whose index does not follow the index of the line before it. No
// match crosses a gap, so the lines on either side of lines the sequence
// skips never join, even when the normalizer drops "\n".
//
// When the Finder has a normalizer, the normalizer transforms the returned
// text one character at a time, as [eachCharacter] splits each line. The
// position map records where each source character begins in the
// normalized text, so lookups in normalized text resolve to the right place.
func (f *Finder) buildTextAndPositionMap(lines line.Sequence) (string, *positionMap) {
	var sb strings.Builder

	pm := &positionMap{}

	if nilness.IsNil(lines) {
		return "", pm
	}

	// Cache normalized forms per unique character to avoid repeated
	// normalizer calls.
	normalizedCache := make(map[string]string)

	var rs []rune

	// Add appends l, the line at index i, to the loaded text and the
	// position map. Every line but the last ends in "\n".
	add := func(i int, l *line.Line, last bool) {
		// Runes yields the columns 0, 1, 2, and so on, so the index of a
		// rune in rs is its column.
		rs = rs[:0]
		for _, r := range l.Runes() {
			rs = append(rs, r)
		}

		if !last && (len(rs) == 0 || rs[len(rs)-1] != '\n') {
			rs = append(rs, '\n')
		}

		eachCharacter(rs, func(char string, start, end int) {
			normalized, ok := normalizedCache[char]
			if !ok {
				normalized = normalize(f.normalizer, char)
				normalizedCache[char] = normalized
			}

			// Record the byte offset where this source character begins in
			// the normalized text, so every byte of its normalized form
			// resolves to the same position. The offset comes from the
			// builder because WriteRune writes a byte that is not valid
			// UTF-8 as the 3-byte U+FFFD. The entry runs over the rest of
			// the character's runes. A character that normalizes to
			// nothing has no entry of its own, so the character before it
			// on the line extends over it and a match ending there covers
			// it too.
			for col := start; col < end; col++ {
				if col == start && normalized != "" {
					pm.add(sb.Len(), position.New(i, col))
				} else {
					pm.extend(position.New(i, col))
				}
			}

			writeRunes(&sb, normalized)
		})
	}

	// A sequence does not say which of its lines is the last, so each line
	// waits for the one after it. That line shows that the waiting one is
	// not the last, and its index shows whether the sequence skips lines
	// between the two.
	var (
		prev    *line.Line
		prevIdx int
	)

	for i, l := range lines.All() {
		if prev != nil {
			add(prevIdx, prev, false)

			if i != prevIdx+1 {
				pm.gaps = append(pm.gaps, sb.Len())
			}
		}

		prev, prevIdx = l, i
	}

	if prev != nil {
		add(prevIdx, prev, true)
	}

	return sb.String(), pm
}

// positionMap maps byte offsets in the loaded text to original
// [position.Position] values in the loaded lines. It holds one entry per
// source character whose normalized form is non-empty, at the offset where
// that form begins, in increasing order. A character that normalizes to
// nothing has no entry of its own and extends the entry before it on its
// line.
type positionMap struct {
	offsets   []int
	positions []position.Position
	// The column just past each entry's source character and the
	// characters after it on its line that normalize to nothing.
	ends []int
	// The offset where each line begins that does not follow the line
	// loaded before it in the content, in increasing order.
	gaps []int
}

// crossesGap reports whether the bytes of the loaded text from start up to
// end hold text from both sides of a gap, which is so when a gap lies
// after start and before end.
func (m *positionMap) crossesGap(start, end int) bool {
	idx := sort.SearchInts(m.gaps, start+1)

	return idx < len(m.gaps) && m.gaps[idx] < end
}

// add records the byte offset at which a source character begins and the
// position of its first rune.
func (m *positionMap) add(offset int, pos position.Position) {
	m.offsets = append(m.offsets, offset)
	m.positions = append(m.positions, pos)
	m.ends = append(m.ends, pos.Col+1)
}

// extend records a source rune at pos that has no entry of its own, so the
// entry before it on the same line runs past it. Such a rune is a later rune
// of the character before it, or a rune of a character that normalizes to
// nothing. A rune with no entry before it on its line extends nothing,
// since no match starts at it.
func (m *positionMap) extend(pos position.Position) {
	last := len(m.positions) - 1
	if last >= 0 && m.positions[last].Line == pos.Line {
		m.ends[last] = pos.Col + 1
	}
}

// end returns the position just past the source character that holds the
// byte at offset, past any characters after it on the line that normalize
// to nothing. The offset must lie at or after the first entry.
func (m *positionMap) end(offset int) position.Position {
	idx := m.floor(offset)

	return position.New(m.positions[idx].Line, m.ends[idx])
}

// floor returns the entry of the source character that holds the byte at
// offset, the last entry that begins at or before offset. It returns -1
// when the map is empty.
func (m *positionMap) floor(offset int) int {
	idx := sort.Search(len(m.offsets), func(i int) bool {
		return m.offsets[i] > offset
	})

	return idx - 1
}

// at returns the entry of the source character whose normalized form begins
// at offset, and false when no entry begins there.
func (m *positionMap) at(offset int) (int, bool) {
	idx := m.floor(offset)

	return idx, idx >= 0 && m.offsets[idx] == offset
}
