package main

import (
	"fmt"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// maxWidth is the widest line the menu board fits, in columns.
const maxWidth = 50

//go:embed demo.yaml
var example string

func main() {
	source := niceyaml.NewSourceFromString(example)

	// New uses the default styles, gutter, and annotation rows.
	p := printer.New()

	// A view carries the notes and marks of one rendering of the source.
	view := source.View()

	// Lines and columns count from 0, so index 6 is the line the gutter
	// numbers 7. A note above a line starts at the first column it labels.
	view.Annotate(6, line.Annotation{
		Content:   "Bestseller.",
		Kind:      kind.TextOK,
		Placement: line.Above,
		Cols:      position.NewSpan(6, 6),
	})

	// A range is half-open, so one that ends at column 0 of the next line
	// covers the whole line. A blended overlay keeps the token colors.
	view.BlendOverlay(kind.GenericHighlight, position.NewRange(position.New(6, 0), position.New(7, 0)))

	// Mark what each line holds past the width of the board.
	for i, ln := range view.All() {
		over := ln.Width() - maxWidth
		if over <= 0 {
			continue
		}

		// This overlay replaces the token colors. A note below a line draws
		// carets under the columns it labels, then its text.
		rng := position.NewRange(position.New(i, maxWidth), position.New(i, ln.Width()))
		view.AddOverlay(kind.GenericError, rng)
		view.Annotate(i, line.Annotation{
			Content:   fmt.Sprintf("%d columns over", over),
			Kind:      kind.TextError,
			Placement: line.Below,
			Cols:      position.NewSpan(maxWidth, ln.Width()),
		})
	}

	fmt.Println("\nPrint with annotations and overlays:")
	lipgloss.Println(p.Print(view))

	// With derives a printer, so the same view prints another way. The
	// width counts the gutter and the container as well as the content.
	narrow := p.With(printer.WithWrapWidth(48))

	fmt.Println("\nWrap to 48 columns:")
	lipgloss.Println(narrow.Print(view))

	// A gutter renders the left edge of every row. Annotation rows and
	// wrapped continuations are rows too, so they get no number.
	gutter := printer.GutterFunc(func(ctx printer.GutterContext) string {
		text := "    │ "
		if !ctx.Annotation && !ctx.Soft {
			text = fmt.Sprintf("%3d │ ", ctx.Number)
		}

		return ctx.Styles.Style(kind.UILineNumber).Render(text)
	})

	fmt.Println("\nPrint with a custom gutter:")
	lipgloss.Println(narrow.With(printer.WithGutter(gutter)).Print(view))
}
