// Package cellcut cuts a rendered row, one that carries the escape
// sequences of its styles, to a range of terminal cells.
//
// It measures each grapheme cluster with [cells.First], so a cut agrees
// with the [cells.Row] of the same text. It keeps every cell in place, so
// cell left+k of the row is cell k of the cut.
//
// The package is separate from [cells] because it walks escape sequences
// with [parser], and only the printer renders rows that hold them.
package cellcut

import (
	"strings"

	"github.com/charmbracelet/x/ansi/parser"

	"go.jacobcolvin.com/niceyaml/internal/cells"
)

// Cut returns cells left through right-1 of s, or "" when right is not
// past left. A negative left counts as 0. Cut measures each cluster with
// [cells.First], so a keycap takes its two cells.
// [github.com/charmbracelet/x/ansi.Cut] counts one cell for the ASCII base
// of a keycap.
//
// A cluster that straddles either edge becomes a space for each of its
// cells inside the range. The spaces take the place of the cluster, so
// they keep the style that s has there, and cell left+k of s is cell k of
// the result. A zero-width cluster at column right falls outside. Cut
// keeps every byte outside a cluster, such as an escape sequence, so a
// style that s opens still closes.
func Cut(s string, left, right int) string {
	cut, _ := CutWidth(s, left, right)

	return cut
}

// CutWidth returns what [Cut] returns along with the cells that the cut
// takes, the width that [github.com/charmbracelet/x/ansi.StringWidth]
// measures for it. Like Cut, it counts a negative left as 0. The width is
// right-left when s reaches cell right, and the cells s has past left
// otherwise. A caller that pads the cut to a width does not need a second
// pass to measure it.
func CutWidth(s string, left, right int) (string, int) {
	left = max(0, left)
	if right <= left {
		return "", 0
	}

	var b strings.Builder

	state := parser.GroundState
	col, width := 0, 0

	// Each cluster that the cut drops or fills moves kept to the byte after
	// it. The bytes from kept up to the next such cluster all stay, so
	// they go to b as one span, and a cut that keeps every cluster whole
	// returns s itself.
	kept := 0

	for i := 0; i < len(s); {
		next, action := parser.Table.Transition(state, s[i])
		if action != parser.PrintAction && next != parser.Utf8State {
			state = next
			i++

			continue
		}

		cluster, w := cells.First(s[i:])

		// The cells of the cluster inside the range. A cluster that
		// straddles an edge leaves a space on each of them, in the place
		// of its bytes, so the spaces keep the style that s has there.
		lo, hi := max(col, left), min(col+w, right)

		switch {
		case col >= left && col < right && col+w <= right:
			width += w

		case hi > lo:
			b.WriteString(s[kept:i])
			b.WriteString(strings.Repeat(" ", hi-lo))

			kept = i + len(cluster)
			width += hi - lo

		default:
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
