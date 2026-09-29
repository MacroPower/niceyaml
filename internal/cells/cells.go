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
//
// A rendered row also carries the escape sequences of its styles. [Cut]
// cuts such a row to a range of cells, measuring each cluster the same
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

// Row holds the cells each rune column of one line of content takes, and
// the cells before each column, so it measures any column in constant
// time. Its methods treat a negative column as column 0.
//
// Create instances with [NewRow].
type Row struct {
	// For each column up to the end of the content, before holds the
	// cells the columns before it take.
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
	if col < len(r.starts) {
		return r.before[col+1] - r.before[col]
	}

	return 1
}

// Width returns the cells the content takes before col, with a column past
// the end of the content taking one cell. A column inside a cluster
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

// Col returns the first column at or after the cluster holding lo that
// starts a grapheme cluster at or past cell. A cell past the end of the
// content maps to a column past it, one column per cell. The column
// saturates at [math.MaxInt].
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

// total returns the cells the whole content takes.
func (r Row) total() int {
	if len(r.before) == 0 {
		return 0
	}

	return r.before[len(r.before)-1]
}

// Cut returns the grapheme clusters of s that start at or after column
// left and end by column right, or "" when right is not past left. It
// measures each cluster as [ansi.StringWidth] does, so a keycap takes its
// two cells, where [ansi.Cut] counts one for its ASCII base. A cluster
// that straddles either edge drops out whole, so the result can come up
// short of right-left cells. A cluster of no width at column right falls
// outside. Every byte outside a cluster, such as an escape sequence,
// stays, so a style s opens still closes.
func Cut(s string, left, right int) string {
	cut, _ := CutWidth(s, left, right)

	return cut
}

// CutWidth returns what [Cut] returns along with the cells the cut takes,
// the width [ansi.StringWidth] measures for it. A caller that pads the cut
// to a width needs no second pass to measure it.
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
