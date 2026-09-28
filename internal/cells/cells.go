// Package cells measures the terminal cells a line of content takes as the
// printer renders it.
//
// A rendered row shows each control character as a one-cell picture and
// each grapheme cluster at its display width. A cluster can hold several
// runes, as a combining mark, an emoji ZWJ sequence, or a keycap does,
// while positions count runes. A [Row] maps each rune column to its cells.
// The first rune of a cluster takes the width of the whole cluster, and
// every other rune of it takes none, so a marker aimed at any rune of a
// cluster lands under the whole cluster.
//
//	row := cells.NewRow("a: 1️⃣ x")
//	row.Width(7) // 6, the cells before x
package cells

import (
	"math"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"go.jacobcolvin.com/niceyaml/internal/escape"
)

// Row holds the cells each rune column of one line of content takes. Its
// methods treat a negative column as column 0.
//
// Create instances with [NewRow].
type Row struct {
	widths []int
	starts []int
}

// NewRow creates a new [Row] for content, the text of a line without its
// line ending.
func NewRow(content string) Row {
	var r Row

	rest := escape.Control(content)

	for rest != "" {
		cluster, width := ansi.FirstGraphemeCluster(rest, ansi.GraphemeWidth)
		start := len(r.widths)

		for i := range utf8.RuneCountInString(cluster) {
			r.widths = append(r.widths, 0)
			r.starts = append(r.starts, start)

			if i == 0 {
				r.widths[start] = width
			}
		}

		rest = rest[len(cluster):]
	}

	return r
}

// Start returns the column of the rune that starts the cluster holding
// col. A column past the end of the content starts its own cluster.
func (r Row) Start(col int) int {
	col = max(0, col)
	if col < len(r.starts) {
		return r.starts[col]
	}

	return col
}

// Cells returns the cells col takes: the width of its cluster on the first
// rune of a cluster, zero on every other rune of it, and one past the end
// of the content.
func (r Row) Cells(col int) int {
	col = max(0, col)
	if col < len(r.widths) {
		return r.widths[col]
	}

	return 1
}

// Width returns the cells the content takes before col, with a column past
// the end of the content taking one cell. A column inside a cluster
// measures up to the start of its cluster. The width saturates at
// [math.MaxInt].
func (r Row) Width(col int) int {
	col = r.Start(col)

	var width int

	for _, w := range r.widths[:min(col, len(r.widths))] {
		width += w
	}

	return width + min(max(0, col-len(r.widths)), math.MaxInt-width)
}

// TrimLastCluster returns s without its last grapheme cluster, the
// character a user sees. It splits s at the grapheme cluster boundaries of
// the raw text. These match the boundaries [NewRow] uses except around
// control characters, which NewRow segments as their pictures, so a CR LF
// pair is one cluster here but two in a [Row].
func TrimLastCluster(s string) string {
	var cluster string

	for rest := s; rest != ""; rest = rest[len(cluster):] {
		cluster, _ = ansi.FirstGraphemeCluster(rest, ansi.GraphemeWidth)
	}

	return s[:len(s)-len(cluster)]
}
