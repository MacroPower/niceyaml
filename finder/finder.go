// Package finder locates strings within the lines of a [line.Sequence].
//
// A [Finder] maps matches back to [position.Ranges] in the original lines,
// even when folding changes the character count. The ranges can then
// highlight matches in rendered output. Create one with [New], build an
// [Index] over the lines once with [Finder.Load], and search the index any
// number of times with [Index.Find]:
//
//	idx := finder.New().Load(source.Lines())
//	view := source.View()
//	view.BlendOverlay(kind.GenericHighlight, idx.Find("search term")...)
//
// Load takes the [line.Lines] of a whole file or a [*line.View]. A view
// loads the lines it holds. The ranges of its matches are in the
// coordinates of its content, as every range a view takes is. A search of
// one document of a file that holds several therefore marks the view of
// that document:
//
//	view := doc.View()
//	idx := finder.New().Load(view)
//	view.BlendOverlay(kind.GenericHighlight, idx.Find("search term")...)
//
// A search folds case and ignores diacritics by default. [WithCaseFold],
// [WithDiacriticFold], and [WithWidthFold] turn each built-in fold on or
// off, and [Exact] turns them all off. [WithFold] adds a fold of the
// caller's own, which runs after the built-in folds:
//
//	idx := finder.New(finder.WithDiacriticFold(false)).Load(source.Lines())
//	idx.Find("CAFÉ") // matches "Café", but "cafe" does not
//
// Every fold applies to both the loaded text and the search string, one
// character at a time, as [WithFold] describes. Whatever the folds, a
// search reads invalid bytes and line endings as [Index.Find] describes.
package finder

import (
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"go.jacobcolvin.com/niceyaml/internal/nilness"
	"go.jacobcolvin.com/niceyaml/internal/normalizer"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
)

// Finder builds an [Index] over the lines of a [line.Sequence], so the
// [position.Ranges] of a search can highlight matches in rendered output.
//
// The typical use case is search-as-you-type highlighting. The user views
// YAML content, types a search term, and sees matching text highlighted
// in place.
//
// Finder uses a load-once, search-many design. [Finder.Load] reads the
// lines once and returns an [Index] that maps offsets in the loaded text
// back to [position.Position] values in the original lines. [Index.Find]
// uses that map on every call without reading the lines again.
//
// A Finder holds only its settings and never changes after [New]. It is
// therefore safe for concurrent use when every fold that [WithFold] adds
// is, and so is every Index it builds. Without such a fold, it is always
// safe for concurrent use.
//
// Example:
//
//	idx := finder.New().Load(source.Lines())
//	view := source.View()
//	view.BlendOverlay(kind.GenericHighlight, idx.Find("search term")...)
//	lipgloss.Println(p.Print(view))
//
// By default, a search folds case and ignores diacritics, so "cafe"
// matches "Café". [Exact] makes a search match characters exactly. A zero
// Finder searches as one from [New] without options does.
//
// Create instances with [New].
type Finder struct {
	config
}

// New creates a new [*Finder].
// Call [Finder.Load] to build an [Index] over a [line.Sequence] before
// searching.
//
// Without options, the Finder folds case and ignores diacritics.
func New(opts ...Option) *Finder {
	f := &Finder{}
	for _, opt := range opts {
		opt(&f.config)
	}

	return f
}

// Option configures a [Finder].
//
// Available options:
//   - [Exact]
//   - [WithCaseFold]
//   - [WithDiacriticFold]
//   - [WithFold]
//   - [WithWidthFold]
//
// The options that set a built-in fold apply in order, so a later one
// overrides an earlier one. [Exact] followed by [WithCaseFold] with true
// folds case only, but [WithCaseFold] followed by [Exact] folds nothing.
type Option func(*config)

// config holds the settings an [Option] configures. An option takes it in
// place of the [Finder] that embeds it, so only [New] can apply one. Its
// zero value holds the defaults, so a zero [Finder] searches as one from
// [New] does.
type config struct {
	folds          []func(char string) string
	keepCase       bool
	keepDiacritics bool
	widthFold      bool
}

// Exact is an [Option] that turns off every built-in fold, so a search
// matches characters exactly, without case folding, diacritic stripping,
// or width folding:
//
//	exact := finder.New(finder.Exact())
//
// Exact leaves the folds that [WithFold] adds in place, so Exact with
// WithFold runs only the caller's own folds.
func Exact() Option {
	return func(c *config) {
		c.keepCase = true
		c.keepDiacritics = true
		c.widthFold = false
	}
}

// WithCaseFold is an [Option] that turns Unicode case folding on or off.
// When enabled is true, the default, "CAFE" matches "cafe". Case folding
// differs from lowercasing for some characters, so "Straße" matches
// "STRASSE".
func WithCaseFold(enabled bool) Option {
	return func(c *config) {
		c.keepCase = !enabled
	}
}

// WithDiacriticFold is an [Option] that turns diacritic stripping on or
// off. When enabled is true, the default, a search ignores combining
// diacritical marks such as accents, umlauts, and cedillas, so "cafe"
// matches "Café". Marks that form part of a letter stay, so "ガ" keeps its
// voicing mark, and Thai and Devanagari vowel signs stay too.
//
// Diacritic folding also composes each character to one canonical
// spelling. When enabled is false, a precomposed character and its
// decomposed spelling do not match each other, so "é" does not find
// "e" followed by U+0301.
func WithDiacriticFold(enabled bool) Option {
	return func(c *config) {
		c.keepDiacritics = !enabled
	}
}

// WithWidthFold is an [Option] that turns Unicode width folding on or off.
// When enabled is true, fullwidth and halfwidth characters fold to their
// canonical forms, so "ａｂｃ" matches "abc". The default is false.
func WithWidthFold(enabled bool) Option {
	return func(c *config) {
		c.widthFold = enabled
	}
}

// WithFold is an [Option] that adds fold to the folds applied to both the
// search string and the loaded text before matching. It runs after the
// built-in folds, so case folding and diacritic stripping still apply
// unless [Exact] or another option turns them off. This fold ignores
// spaces:
//
//	finder.WithFold(func(char string) string {
//		if r, _ := utf8.DecodeRuneInString(char); unicode.Is(unicode.Zs, r) {
//			return ""
//		}
//
//		return char
//	})
//
// Each call adds one more fold, and the folds run in the order of the
// calls. A nil fold adds nothing.
//
// A fold receives one character at a time, on both sides, so the same
// character always folds the same way wherever it appears. A character
// here is a unit that Unicode normalization composes as a whole: a rune
// with the combining marks that follow it, or the conjoining jamo of one
// Hangul syllable. A precomposed "ガ" and a "カ" followed by the voicing
// mark U+3099 each reach the fold whole. Diacritic folding, on by default,
// composes them, so either spelling finds the other.
//
// The string that fold receives is what the folds before it made of one
// character. It might hold more than one rune, as case folding turns "ß"
// into "ss", but it is never empty. When an earlier fold returns "", the
// later folds do not run for that character. A fold whose output depends
// on surrounding characters does not see any, and it behaves as it does on
// a one-character string. Title casing is one such fold.
//
// Line endings reach the fold as "\n" too. A fold that drops "\n", such
// as one that removes all whitespace, lets a match run from the end of one
// line into the next.
//
// The [Finder] and every [Index] it builds share fold. When goroutines use
// any of them at once, fold must be safe for concurrent use.
func WithFold(fold func(char string) string) Option {
	return func(c *config) {
		if fold == nil {
			return
		}

		c.folds = append(c.folds, fold)
	}
}

// folder returns the function that folds one character for an [Index]:
// the built-in folds the config turns on, then the folds of [WithFold].
func (c *config) folder() func(char string) string {
	var builtin *normalizer.Normalizer

	if !c.keepCase || !c.keepDiacritics || c.widthFold {
		builtin = normalizer.New(
			normalizer.WithCaseFold(!c.keepCase),
			normalizer.WithDiacriticFold(!c.keepDiacritics),
			normalizer.WithWidthFold(c.widthFold),
		)
	}

	folds := c.folds

	return func(char string) string {
		if builtin != nil {
			char = builtin.Normalize(char)
		}

		for _, fold := range folds {
			if char == "" {
				break
			}

			char = fold(char)
		}

		return char
	}
}

// Load reads the lines of a [line.Sequence] and returns an [Index] over
// them. The Index holds the loaded text and a map from its positions back
// to the lines.
//
// [line.Lines] loads every line. A [*line.View] loads the lines it holds,
// and [Index.Find] reports each match in the coordinates of the view's
// content. The ranges then apply to the view and to any other view over
// that content. A view holds whole lines. A node might share its line with
// other nodes, such as host in `server: {host: h, port: 80}`. The view of
// such a node loads that whole line, so a search of it for "port" finds a
// match there.
//
// A view can skip lines of its content, as the hunks of a diff do.
// Matches never run from the line before the skipped lines into the line
// after them.
//
// Each call builds a new Index and leaves the Finder as it was. Load once
// per distinct content and call [Index.Find] for each search. The lines
// never change, so the index stays valid however callers decorate the
// views over them. Load returns an empty Index for a nil sequence and for
// one that holds a nil pointer, such as a nil [*line.View].
func (f *Finder) Load(lines line.Sequence) *Index {
	idx := &Index{fold: f.folder()}
	idx.text, idx.posMap = buildTextAndPositionMap(lines, idx.fold)

	return idx
}

// Index is the loaded text of one [line.Sequence] together with the map
// from its characters back to [position.Position] values in the lines. It
// never changes after [Finder.Load] builds it, so it is safe for concurrent
// use when every fold that [WithFold] adds to the Finder is. Every Index
// from one Finder shares those folds.
//
// Create instances with [Finder.Load].
type Index struct {
	fold   func(char string) string
	posMap *positionMap
	text   string
}

// Find finds all occurrences of the search string in the loaded text.
//
// It returns the [position.Ranges] of each match, in the order the matches
// appear in the text.
//
// The search string goes through the same per-character folds as the
// loaded text. Find then finds any run of whole characters that appears in
// the source, as [WithFold] defines a character. Bytes
// that are not valid UTF-8 read as U+FFFD on both sides. A CRLF or bare CR
// line ending reads as "\n" on both sides.
//
// Every loaded line but the last reads as ending in "\n", even one without
// a line ending of its own. The last line of a diff revision and a
// placeholder row are such lines. A match joins two lines only when a
// fold drops that "\n". A fold that removes all whitespace drops it, so
// "1b" then matches across the lines "a: 1" and "b: 2". Where the loaded
// lines skip lines of their content, as a [line.View] of hunks does,
// matches never join the lines on either side, whatever the folds drop.
//
// Every match starts at a source character, and every range covers whole
// characters. An exact search for "cafe" in a "café" that spells its
// accent as a combining mark therefore covers the accent too.
//
// When folding expands one character into several, as case folding turns
// "ß" into "ss", a search string that matches only the tail of the
// expansion is not a match. A match that ends inside an expansion covers
// the whole character. When a fold drops a character, as a fold that
// removes whitespace drops a space, a match that ends
// immediately before the dropped character covers it too. The range then
// ends where the next character begins.
//
// Find returns nil when the search string is empty or folds to empty,
// and when the Index is nil or its loaded text is empty.
func (i *Index) Find(search string) position.Ranges {
	if i == nil || search == "" || i.text == "" {
		return nil
	}

	searchStr := i.foldText(search)

	// A search of only combining marks folds to nothing, and an empty
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

		// A match that starts inside the folded form of one source
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

// foldText folds s the way [Finder.Load] folds the loaded text, one
// character at a time. A search string and the text that Find compares it
// against then pass through the folds identically. Line
// endings collapse to "\n" first, because the loaded text reads every line
// ending that way.
func (i *Index) foldText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	var sb strings.Builder

	// The conversion reads each byte that is not valid UTF-8 as U+FFFD, as
	// the index reads the lines.
	eachCharacter([]rune(s), func(char string, _, _ int) {
		writeRunes(&sb, i.fold(char))
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

// writeRunes writes s to sb rune by rune. A fold might emit a byte
// that is not valid UTF-8, and writing rune by rune turns that byte into
// U+FFFD on both sides.
func writeRunes(sb *strings.Builder, s string) {
	for _, r := range s {
		sb.WriteRune(r)
	}
}

// buildTextAndPositionMap concatenates the runes of every line into the
// loaded text and builds a position map. [line.Line.Runes] yields every
// line ending as a single "\n". Each line other than the last gets a "\n"
// at column [line.Line.Width] when it lacks an ending of its own. A match
// then joins the text of two lines only when a fold drops "\n".
//
// The position map records a gap at the start of each line whose index
// does not follow the index of the line before it. [Index.Find] skips
// every match that crosses a gap. The lines on either side of a skipped run
// of lines therefore never join, even when a fold drops "\n".
//
// Fold transforms the returned text one character at a time, as
// [eachCharacter] splits each line. The position map records where each
// source character begins in the folded text, so lookups in folded text
// resolve to the right place.
func buildTextAndPositionMap(lines line.Sequence, fold func(char string) string) (string, *positionMap) {
	var sb strings.Builder

	pm := &positionMap{}

	if nilness.IsNil(lines) {
		return "", pm
	}

	// Cache folded forms per unique character to avoid repeated fold
	// calls.
	foldedCache := make(map[string]string)

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
			folded, ok := foldedCache[char]
			if !ok {
				folded = fold(char)
				foldedCache[char] = folded
			}

			// Record the byte offset where this source character begins in
			// the folded text, so every byte of its folded form resolves to
			// the same position. The offset comes from the builder because
			// WriteRune writes a byte that is not valid UTF-8 as the 3-byte
			// U+FFFD. The entry runs over the rest of the character's runes.
			// A character that folds to nothing has no entry of its own, so
			// the character before it on the line extends over it and a
			// match ending there covers it too.
			for col := start; col < end; col++ {
				if col == start && folded != "" {
					pm.add(sb.Len(), position.New(i, col))
				} else {
					pm.extend(position.New(i, col))
				}
			}

			writeRunes(&sb, folded)
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
// source character whose folded form is non-empty, at the offset where
// that form begins, in increasing order. A character that folds to
// nothing does not get an entry of its own and extends the entry before it
// on its line.
type positionMap struct {
	offsets   []int
	positions []position.Position
	// The column after the source character of each entry, past the
	// characters after it on its line that fold to nothing.
	ends []int
	// The starting offset of each line that does not follow the line
	// loaded before it in the content, in increasing order.
	gaps []int
}

// crossesGap reports whether the bytes of the loaded text from start up to
// end hold text from both sides of a gap. They do when a gap lies after
// start and before end.
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

// extend records a source rune at pos that lacks an entry of its own, so
// the entry before it on the same line runs past it. Such a rune is a later
// rune of the character before it, or a rune of a character that
// folds to nothing. Matches never start at a rune without an entry
// before it on its line, so extend leaves the map unchanged for that rune.
func (m *positionMap) extend(pos position.Position) {
	last := len(m.positions) - 1
	if last >= 0 && m.positions[last].Line == pos.Line {
		m.ends[last] = pos.Col + 1
	}
}

// end returns the position after the source character that holds the
// byte at offset, past any characters after it on the line that fold
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

// at returns the entry of the source character whose folded form begins
// at offset. It reports whether an entry begins at offset.
func (m *positionMap) at(offset int) (int, bool) {
	idx := m.floor(offset)

	return idx, idx >= 0 && m.offsets[idx] == offset
}
