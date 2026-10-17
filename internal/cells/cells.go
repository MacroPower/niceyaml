// Package cells measures the terminal cells that a line of content takes
// as the printer renders it.
//
// A rendered row shows each control character in one cell, as the rune
// that [escape.Control] puts in its place. That rune is a picture for a C0
// control or DEL, and U+FFFD for a C1 control.
//
// A rendered row shows each grapheme cluster at its display width. A
// cluster can hold several runes, as a combining mark, an emoji zero-width
// joiner sequence, or a keycap does, but positions count runes. A [Row]
// maps each rune column to its cells. The first rune of a cluster takes
// the width of the whole cluster, and every other rune of it does not take
// a cell. A marker aimed at any rune of a cluster therefore lands under
// the whole cluster.
//
//	row := cells.NewRow("a: 1️⃣ x")
//	row.Width(7) // 6, the cells before x
//
// A rendered row also carries the escape sequences of its styles. [Cut]
// cuts such a row to a range of cells. It measures each cluster the same
// way, so the width of what it returns agrees with [ansi.StringWidth].
package cells

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"

	"go.jacobcolvin.com/niceyaml/internal/escape"
)

// Row holds the cells that each rune column of one line of content takes,
// and the cells before each column. It therefore measures any column in
// constant time. Its methods treat a negative column as column 0.
//
// Create instances with [NewRow].
type Row struct {
	// For each column up to the end of the content, before holds the
	// cells that the columns before it take.
	before []int
	starts []int
}

// NewRow creates a new [Row] for content, the text of a line without its
// line ending.
func NewRow(content string) Row {
	r := Row{before: []int{0}}

	rest := escape.Control(content)

	for rest != "" {
		cluster, width := ansi.FirstGraphemeCluster(rest, ansi.GraphemeWidth)
		start := len(r.starts)

		for i := range utf8.RuneCountInString(cluster) {
			w := 0
			if i == 0 {
				w = width
			}

			r.before = append(r.before, r.total()+w)
			r.starts = append(r.starts, start)
		}

		rest = rest[len(cluster):]
	}

	return r
}

// Start returns the column of the rune that starts the cluster that holds
// col. A column past the end of the content starts its own cluster.
func (r Row) Start(col int) int {
	col = max(0, col)
	if col < len(r.starts) {
		return r.starts[col]
	}

	return col
}

// Cells returns the cells that col takes. The first rune of a cluster
// takes the width of its cluster, and every other rune of it takes zero.
// A column past the end of the content takes one.
func (r Row) Cells(col int) int {
	col = max(0, col)
	if col < len(r.starts) {
		return r.before[col+1] - r.before[col]
	}

	return 1
}

// Width returns the cells that the content takes before col. A column past
// the end of the content takes one cell. A column inside a cluster
// measures up to the start of its cluster. The width saturates at
// [math.MaxInt].
func (r Row) Width(col int) int {
	col = r.Start(col)
	if col < len(r.before) {
		return r.before[col]
	}

	total := r.total()

	return total + min(col-len(r.starts), math.MaxInt-total)
}

// Col returns the first column that starts a grapheme cluster at or past
// cell. It searches from the start of the cluster that holds lo. A cell
// past the end of the content maps to a column past it, one column per
// cell. The column saturates at [math.MaxInt].
func (r Row) Col(lo, cell int) int {
	lo = r.Start(lo)
	n := len(r.starts)

	total := r.total()
	if cell > total {
		return max(lo, n+min(cell-total, math.MaxInt-n))
	}

	if lo > n {
		return lo
	}

	// The later runes of a cluster share the offset of the next cluster,
	// so the search can land on one of them.
	return r.Next(lo + sort.SearchInts(r.before[lo:], cell))
}

// Next returns the first column at or after col that starts a grapheme
// cluster. A column past the end of the content starts its own cluster.
func (r Row) Next(col int) int {
	col = max(0, col)
	for col < len(r.starts) && r.starts[col] != col {
		col++
	}

	return col
}

// total returns the cells that the whole content takes.
func (r Row) total() int {
	if len(r.before) == 0 {
		return 0
	}

	return r.before[len(r.before)-1]
}

// Cut returns the grapheme clusters of s that start at or after column
// left and end by column right, or "" when right is not past left. It
// measures each cluster as [ansi.StringWidth] does, so a keycap takes its
// two cells. [ansi.Cut] counts one cell for the ASCII base of a keycap.
// Cut drops a cluster that straddles either edge whole, so the result can
// hold fewer than right-left cells. A zero-width cluster at column right
// falls outside. Cut keeps every byte outside a cluster, such as an
// escape sequence, so a style that s opens still closes.
func Cut(s string, left, right int) string {
	cut, _ := CutWidth(s, left, right)

	return cut
}

// CutWidth returns what [Cut] returns along with the cells that the cut
// takes, the width that [ansi.StringWidth] measures for it. A caller that
// pads the cut to a width does not need a second pass to measure it.
func CutWidth(s string, left, right int) (string, int) {
	if right <= left {
		return "", 0
	}

	var b strings.Builder

	state := parser.GroundState
	col, width := 0, 0

	// Each dropped cluster moves kept to the byte just past it. The bytes
	// from kept up to the next dropped cluster all stay, so they go to b
	// as one span, and a cut that drops nothing returns s itself.
	kept := 0

	for i := 0; i < len(s); {
		next, action := parser.Table.Transition(state, s[i])
		if action != parser.PrintAction && next != parser.Utf8State {
			state = next
			i++

			continue
		}

		cluster, w := ansi.FirstGraphemeCluster(s[i:], ansi.GraphemeWidth)
		if col >= left && col < right && col+w <= right {
			width += w
		} else {
			b.WriteString(s[kept:i])

			kept = i + len(cluster)
		}

		state = parser.GroundState
		col += w
		i += len(cluster)
	}

	if kept == 0 {
		return s, width
	}

	b.WriteString(s[kept:])

	return b.String(), width
}

// TrimLastCluster returns s without its last grapheme cluster, the
// character that a user sees. It splits s at the grapheme cluster boundaries of
// the raw text. These match the boundaries that [NewRow] uses except
// around control characters. NewRow segments each control character as
// the rune that [escape.Control] puts in its place. A CR LF pair is thus
// one cluster here but two in a [Row].
func TrimLastCluster(s string) string {
	var cluster string

	for rest := s; rest != ""; rest = rest[len(cluster):] {
		cluster, _ = ansi.FirstGraphemeCluster(rest, ansi.GraphemeWidth)
	}

	return s[:len(s)-len(cluster)]
}
