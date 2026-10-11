package main

import (
	"errors"
	"fmt"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
)

// broken leaves a flow sequence open, so the parser rejects it. It is a
// constant because prettier checks every YAML file in the repository and
// cannot read one that does not parse.
const broken = `table: 7
order: [kaffe, kanelbulle
`

//go:embed demo.yaml
var example string

func main() {
	p := printer.New()

	fmt.Println("\nPrint a syntax error:")

	// The source binds the parser's error at the token the parser rejects.
	_, err := niceyaml.NewSourceFromString(broken, niceyaml.WithName("order.yaml")).Document()
	lipgloss.Println(p.PrintError(err))
	fmt.Println("Matches ErrSyntax:", errors.Is(err, niceyaml.ErrSyntax))

	fmt.Println("\nPrint errors built from paths:")

	// An error names a path and holds no source, so a check builds one
	// without the document. A detail explains the error that carries it.
	breakfast := paths.Doc().Child("menu", "breakfast")
	duplicate := niceyaml.NewError("duplicate item",
		niceyaml.AtPath(breakfast.Index(2).Child("item")),
		niceyaml.WithDetails(
			niceyaml.NewError("first listed here", niceyaml.AtPath(breakfast.Index(0).Child("item"))),
		),
	)
	currency := niceyaml.NewError("price is not in kr", niceyaml.AtPath(breakfast.Index(1).Child("price")))

	// A summary heads several problems. Binding resolves each path to a
	// position in the source, and the excerpt comes from that position.
	source := niceyaml.NewSourceFromString(example, niceyaml.WithName("demo.yaml"))
	err = source.Bind(niceyaml.NewSummary("2 menu problems", duplicate, currency))
	lipgloss.Println(p.PrintError(err, niceyaml.WithContextLines(1)))

	fmt.Println("\nFormat the same errors as plain text:")

	// FormatError writes no escape sequences, so its output suits a log.
	fmt.Println(niceyaml.FormatError(err, niceyaml.WithContextLines(0)))

	fmt.Println("\nAnnotate the whole document:")

	// Annotate marks every bound error on a view of its source.
	view := source.View()
	niceyaml.Annotate(err, view)
	lipgloss.Println(p.Print(view))

	fmt.Println("\nList each problem as a row:")

	// A detail is no problem of its own, so the walk yields two rows.
	for problem := range niceyaml.Problems(err) {
		pos, _ := problem.Position()
		path, _ := problem.Path()
		fmt.Printf("%s:%d:%d\t%s\t%s\n", problem.Source().Name(), pos.Line+1, pos.Col+1, path, problem.Message())
	}
}
