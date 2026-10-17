package main

import (
	"context"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/diff/lcs"
	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
)

// exampleBefore holds the before revision as its author wrote it, with
// padding that aligns the values of the menu. Prettier formats every YAML
// file in the repository and strips such padding, so this revision is a
// constant.
const exampleBefore = `---
restaurant:
  name: Café Yamüll
  opens: "07:00"
---
menu:
  - item:  "Småkakor & Kaffe"
    price: "200 kr"
  - item:  "Kanelbulle"
    price: "45 SEK"
  - item:  "Prinsesstårta"
    price: "8 EUR"
  - item:  "Kardemummabulle"
    price: "45 kr"
`

var (
	//go:embed after.yaml
	exampleAfter string

	// A [diff.Side] prints as "before" or "after". This map names each
	// revision after its file instead.
	sides = map[diff.Side]string{diff.Before: "before.yaml", diff.After: "after.yaml"}
)

func main() {
	ctx := context.Background()
	p := printer.New()

	before := niceyaml.NewSourceFromString(exampleBefore, niceyaml.WithName(sides[diff.Before]))
	after := niceyaml.NewSourceFromString(exampleAfter, niceyaml.WithName(sides[diff.After]))

	// The second document of each file holds the menu. The change also
	// edits the first document, but a review of the menu leaves it out.
	menuBefore, menuAfter := before.Documents()[1], after.Documents()[1]

	// Check each revision, then mark its errors on a view of its menu.
	rule := niceyaml.Each(paths.Current().Child("menu").IndexAll(), niceyaml.ValidatorFunc(inKronor))
	errBefore := menuBefore.Validate(ctx, rule)
	errAfter := menuAfter.Validate(ctx, rule)

	viewBefore, viewAfter := menuBefore.View(), menuAfter.View()
	niceyaml.Annotate(errBefore, viewBefore)
	niceyaml.Annotate(errAfter, viewAfter)

	fmt.Println("\nCount the changes to the menu:")

	// A Node view holds the lines of one document with the numbers they
	// have in the file. A diff of two such views covers that document
	// alone, and its gutter and hunk headers print those numbers.
	exact := diff.Diff(viewBefore, viewAfter).Stats()
	fmt.Printf("default:        +%d -%d\n", exact.Added, exact.Removed)

	// A formatter removed the padding from the after revision, so the
	// default algorithm counts every `item:` line as changed. An algorithm
	// that ignores the amount of whitespace counts only the edits here. It
	// also hides a change to the whitespace inside a quoted value. The rest
	// of the review uses its diff.
	differ := diff.New(diff.WithAlgorithm(spacing{lcs.NewHirschberg()}))
	result := differ.Diff(viewBefore, viewAfter)
	loose := result.Stats()
	fmt.Printf("ignore spacing: +%d -%d\n", loose.Added, loose.Removed)

	fmt.Println("\nPrint the hunks with the errors of both revisions:")

	// A diff reads the lines of its inputs and ignores their marks. Carry
	// copies the marks of each input view onto the rows that show its
	// lines. An unchanged row shows the line of the after revision, so it
	// takes the marks of that revision alone.
	hunks := result.Hunks(1)
	result.Carry(hunks, viewBefore, viewAfter)
	lipgloss.Println(p.Print(hunks))

	fmt.Println("\nFind the row that shows each error:")

	// A position in a revision is not a row of the diff. A row is an index
	// of the unified view, which counts from 0 at the `---` that opens the
	// menu document. The hunks keep those rows, but their gutter prints the
	// lines of the file.
	//
	// UnifiedRow maps the line of an error to its row, as a viewer that
	// scrolls to the error does. The input is a Node view, so an Origin
	// counts lines in the file, and the line of the error goes into it
	// unchanged. A line that both revisions hold maps to one row.
	// UnifiedRow reports false for a line that its input does not hold. A
	// viewer skips such an error, as it skips an error without a position.
	//
	// Hunks pick their rows from the changes alone, and Carry adds marks
	// but does not add rows. An error on an unchanged line outside the
	// context of every change thus stays out of the hunks. Contains reports
	// whether a hunk shows the row.
	errs := map[diff.Side]error{diff.Before: errBefore, diff.After: errAfter}
	for _, side := range []diff.Side{diff.Before, diff.After} {
		for problem := range niceyaml.Problems(errs[side]) {
			pos, ok := problem.Position()
			if !ok {
				continue
			}

			row, ok := result.UnifiedRow(diff.Origin{Side: side, Index: pos.Line})
			if !ok {
				continue
			}

			// Each problem reports the name of its source. Here each name
			// comes from sides, but a program that names its sources after
			// files prints names that it did not write. Such a name can hold
			// a control character, such as a line feed. Control draws each
			// control character as one visible rune, so a line feed in a name
			// cannot split an entry of the listing.
			name := escape.Control(problem.Name())
			fmt.Printf("%s:%d\trow %2d\tin a hunk: %t\n", name, pos.Line+1, row, hunks.Contains(row))
		}
	}

	fmt.Println("\nName the line that each changed row shows:")

	// UnifiedOrigin maps a row back to the revision and the index of the
	// line it shows, as a viewer does when a reader clicks the row. The
	// index counts lines in the file, as the source and its errors do. A
	// click below the last row lands outside the view, and UnifiedOrigin
	// reports false for it.
	for row := range hunks.All() {
		if hunks.Flag(row) == line.FlagDefault {
			continue
		}

		origin, ok := result.UnifiedOrigin(row)
		if !ok {
			continue
		}

		fmt.Printf("row %2d\t%s:%d\n", row, sides[origin.Side], origin.Index+1)
	}

	fmt.Println("\nPrint the revisions side by side:")

	// Result.Before and Result.After return panes of equal length, with an
	// empty row where the other revision holds a line that this one lacks.
	// Each pane shows every line of its revision and takes the marks of
	// that revision. PrintSideBySide keeps line i of each pane beside line
	// i of the other, so an error row below a line in one pane gets a blank
	// row in the other.
	left, right := result.Before(), result.After()
	result.Carry(left, viewBefore, viewAfter)
	result.Carry(right, viewBefore, viewAfter)
	lipgloss.Println(p.PrintSideBySide(left, right))
}

// spacing is an [lcs.Algorithm] that ignores changes in the amount of
// whitespace inside a line and leaves the diff to the algorithm it wraps.
// Unlike diff -b, it compares the indentation exactly, because the
// indentation carries meaning in YAML. The ops it returns index the
// original lines, so the diff shows each line as its revision wrote it.
type spacing struct {
	inner lcs.Algorithm
}

// Diff squeezes the whitespace of each line and diffs the result with the
// algorithm that spacing wraps.
func (s spacing) Diff(before, after []string) []lcs.Op {
	return s.inner.Diff(squeeze(before), squeeze(after))
}

// squeeze returns lines that keep their indentation. In the text after
// the indentation, each run of whitespace becomes one space, and the
// whitespace at the end of the line goes away.
func squeeze(lines []string) []string {
	out := make([]string, len(lines))
	for i, text := range lines {
		body := strings.TrimLeft(text, " ")
		out[i] = text[:len(text)-len(body)] + strings.Join(strings.Fields(body), " ")
	}

	return out
}

// inKronor rejects a menu item whose price is not in kr.
func inKronor(ctx context.Context, n *niceyaml.Node) error {
	pricePath := paths.Current().Child("price")

	price, err := n.DecodeAt[string](ctx, pricePath)
	if err != nil {
		return fmt.Errorf("decode price: %w", err)
	}

	if !strings.HasSuffix(price, " kr") {
		return niceyaml.NewError("price is not in kr", niceyaml.AtPath(pricePath))
	}

	return nil
}
