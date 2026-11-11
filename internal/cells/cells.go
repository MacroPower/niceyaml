// Package cells measures the terminal cells that a line of content takes
// as the printer renders it.
//
// A rendered row shows each control character in one cell, as the rune
// that [escape.Control] puts in its place. That rune is a picture for a C0
// control or DEL, and U+FFFD for a C1 control. [Tabs] with a width show
// each tab as the spaces up to the next tab stop instead.
//
// A rendered row shows each grapheme cluster at its display width. A
// cluster can hold several runes, as a combining mark, an emoji zero-width
// joiner sequence, or a keycap does, but positions count runes. A [Row]
// maps each rune column to its cells. The first rune of a cluster takes
// the width of the whole cluster, and every other rune of it does not take
// a cell. A marker aimed at any rune of a cluster therefore lands under
// the whole cluster.
//
//	row := cells.NewRow("a: 1️⃣ x", cells.Tabs{})
//	row.Width(7) // 6, the cells before x
//
// [First] measures each cluster, both for a [Row] and for the cut of a
// styled row. It applies the width policy of
// [github.com/charmbracelet/x/ansi.StringWidth], the measure that lipgloss
// lays out rows by. The policy reads the RUNEWIDTH_EASTASIAN variable when the
// program starts, and a true value widens each rune of ambiguous East
// Asian width to two cells. The tests of the package check that the two
// measures agree. The package does not import that ansi package, so the
// packages that place carets in plain text do not link it.
package cells

import (
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/clipperhouse/displaywidth"
	"github.com/clipperhouse/uax29/v2/graphemes"

	"go.jacobcolvin.com/niceyaml/escape"
)

// widths measures each cluster that [First] returns.
var widths = displaywidth.Options{EastAsianWidth: eastAsian()}

// eastAsian reports whether the RUNEWIDTH_EASTASIAN variable holds a true
// value.
func eastAsian() bool {
	ea, err := strconv.ParseBool(os.Getenv("RUNEWIDTH_EASTASIAN"))

	return err == nil && ea
}

// First returns the first grapheme cluster of s, the character that a user
// sees, and the cells it takes. It returns "" and 0 for an empty s.
func First(s string) (string, int) {
	cluster := graphemes.FromString(s).First()

	return cluster, widths.String(cluster)
}

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

// Tabs sets how a [Row] measures a tab. The zero Tabs measures each tab
// as its one-cell picture.
type Tabs struct {
	// Width is the distance between two tab stops, in cells. A tab takes
	// the cells up to the next tab stop, between 1 and Width, inclusive.
	// A Width of 0 or less measures each tab as its picture.
	Width int
	// Start is the cell of the line at which the content starts. The tab
	// stops fall on the multiples of Width counted from the start of the
	// line, so a [Row] of a wrapped row of the line passes the cells that
	// the line takes before the row. A negative Start counts as 0.
	Start int
}

// NewRow creates a new [Row] for content, the text of a line without its
// line ending. It measures each tab as tabs sets.
func NewRow(content string, tabs Tabs) Row {
	r := Row{before: []int{0}}
	start := max(0, tabs.Start)

	for content != "" {
		piece, rest, tab := content, "", false
		if tabs.Width > 0 {
			piece, rest, tab = strings.Cut(content, "\t")
		}

		r = r.add(escape.Control(piece))

		// A tab is a grapheme cluster of its own, so it starts its own
		// column and ends before the next rune.
		if tab {
			total := r.total()
			at := (start%tabs.Width + total%tabs.Width) % tabs.Width
			r.before = append(r.before, total+tabs.Width-at)
			r.starts = append(r.starts, len(r.starts))
		}

		content = rest
	}

	return r
}

// add returns r with the columns of shown, text that [escape.Control]
// escaped, after its own.
func (r Row) add(shown string) Row {
	for shown != "" {
		cluster, width := First(shown)
		start := len(r.starts)

		for i := range utf8.RuneCountInString(cluster) {
			w := 0
			if i == 0 {
				w = width
			}

			r.before = append(r.before, r.total()+w)
			r.starts = append(r.starts, start)
		}

		shown = shown[len(cluster):]
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

// TrimLastCluster returns s without its last grapheme cluster, the
// character that a user sees. It splits s at the grapheme cluster boundaries of
// the raw text. These match the boundaries that [NewRow] uses except
// around control characters. NewRow segments each control character as
// the rune that [escape.Control] puts in its place. A CR LF pair is thus
// one cluster here but two in a [Row].
func TrimLastCluster(s string) string {
	var cluster string

	for rest := s; rest != ""; rest = rest[len(cluster):] {
		cluster, _ = First(rest)
	}

	return s[:len(s)-len(cluster)]
}
