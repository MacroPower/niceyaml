// Package severity lays out the rows that a renderer draws below a line
// that holds both the marks of an error and those of a warning.
//
// Each severity gets a row of its own below the line, errors first, and
// each row draws carets under the locations of its own severity alone, so
// no caret sits beside the message of the other. An error marks its line
// with [kind.GenericError] overlays and [kind.TextError] annotations, and
// a warning with [kind.GenericWarn] overlays and [kind.TextWarn]
// annotations. A line that holds one severity keeps the rows it has.
package severity

import (
	"slices"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

// Order returns the indices of kinds, the kinds that the groups of
// annotations below a line render in, in the order a renderer draws them.
// It keeps the order given, apart from a [kind.TextWarn] group, which
// moves after a [kind.TextError] group, so errors come first.
func Order(kinds []kind.Kind) []int {
	order := make([]int, len(kinds))
	for i := range order {
		order[i] = i
	}

	warn, errs := slices.Index(kinds, kind.TextWarn), slices.Index(kinds, kind.TextError)
	if warn < 0 || errs < 0 || warn > errs {
		return order
	}

	order = slices.Delete(order, warn, warn+1)

	return slices.Insert(order, errs, warn)
}

// Split reports whether kinds, the kinds that the groups of annotations
// below a line render in, hold both severities, so each group draws the
// carets of its own severity alone, as [Draws] decides.
func Split(kinds []kind.Kind) bool {
	return slices.Contains(kinds, kind.TextWarn) && slices.Contains(kinds, kind.TextError)
}

// Draws reports whether the row of a group that renders in group draws
// carets under an overlay of kind overlay, on a line whose groups [Split].
// A warning row draws the [kind.GenericWarn] overlays, and every other row
// draws the rest.
func Draws(group, overlay kind.Kind) bool {
	if group == kind.TextWarn {
		return overlay == kind.GenericWarn
	}

	return overlay != kind.GenericWarn
}
