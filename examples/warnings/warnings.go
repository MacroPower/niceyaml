package main

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
)

//go:embed demo.yaml
var example string

// item is one entry of the fika menu.
type item struct {
	Item    string   `yaml:"item"`
	Until   string   `yaml:"until"`
	Tags    []string `yaml:"tags"`
	Dietary []string `yaml:"dietary"`
	Price   int      `yaml:"price"`
}

// menu is the type the document decodes into.
type menu struct {
	Name string `yaml:"name"`
	Fika []item `yaml:"fika"`
}

func main() {
	ctx := context.Background()
	source := niceyaml.NewSourceFromString(example, niceyaml.WithName("demo.yaml"))
	p := printer.New()

	// The decode warns about each key that no field of menu reads, so the
	// misspelled dietry key passes with a warning.
	warnUnknown := niceyaml.WithUnknownFields(niceyaml.UnknownFieldsWarn)

	fmt.Println("\nDecode without a collector:")

	// A decode hands its warnings to the collector on its context. This
	// context does not carry one, so the decode drops both warnings.
	m, err := source.Decode[menu](ctx, warnUnknown)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Printf("%s: decoded %d items\n", source.Name(), len(m.Fika))

	fmt.Println("\nPrint the warnings of a decode that passes:")

	// Context returns a copy of ctx that carries the collector. The decode
	// still returns nil, and Err returns the warnings that the collector
	// holds.
	warnings := niceyaml.NewWarnings()

	_, err = source.Decode[menu](warnings.Context(ctx), warnUnknown)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	lipgloss.Println(p.PrintError(warnings.Err()))

	fmt.Println("\nReport the warnings beside the errors:")

	// The budget rule applies to this call alone, so the call passes it as
	// an option and not as a Validate method. WithValueCheck runs the rule
	// in the step where the items validate themselves. The decode thus
	// still calls Semla's Validate method, and the seasonal warning reaches
	// the collector. A Validator that fails ends the decode before that
	// step.
	budget := niceyaml.WithValueCheck(func(_ context.Context, it *item) error {
		if it.Price > 190 {
			return niceyaml.NewError("costs more than 190 kr", niceyaml.AtPath(paths.Current().Child("price")))
		}

		return nil
	})

	// A call hands its warnings to the collector whether it passes or
	// fails, and it returns only its errors. Report lists both in the
	// order of the source, under a heading that counts each severity.
	// When err is nil, Report returns the same warnings that Err returns.
	warnings = niceyaml.NewWarnings()
	_, err = source.Decode[menu](warnings.Context(ctx), warnUnknown, budget)
	report := warnings.Report(err)
	lipgloss.Println(p.PrintError(report))

	fmt.Println("\nTreat every warning as an error:")

	// A program in strict mode marks each warning an error. The rows lose
	// the "warning:" label, and the heading recounts the problems, so
	// "1 error, 2 warnings" reads "3 errors".
	lipgloss.Println(p.PrintError(niceyaml.AsErrors(report)))

	fmt.Println("\nList each problem with its severity:")

	// The errors.Is function matches a warning as it matches an error, so
	// the loop reads the severity of each problem from its Warning field.
	// Control keeps each problem on one row, as in examples/errors.
	for problem := range niceyaml.Problems(report) {
		severity := "error"
		if problem.Warning {
			severity = "warning"
		}

		pos, _ := problem.Position()
		name := escape.Control(problem.Name())
		msg := escape.Control(problem.Message())
		fmt.Printf("%s:%d:%d\t%s\t%s\n", name, pos.Line+1, pos.Col+1, severity, msg)
	}

	fmt.Println("\nCall a Validate method directly:")

	// Outside a decode, nothing takes the warnings out of the result.
	// Semla's Validate method returns only a warning, so its result is not
	// nil, and HasErrors reports that the item passed.
	semla := m.Fika[1]
	err = semla.Validate()
	fmt.Printf("%s: err != nil is %t, HasErrors is %t\n", semla.Item, err != nil, niceyaml.HasErrors(err))
}

// Validate checks one item and returns its warnings beside its errors.
func (i item) Validate() error {
	var problems []error

	if i.Price < 0 {
		problems = append(problems, niceyaml.NewError("price is less than 0 kr",
			niceyaml.AtPath(paths.Current().Child("price"))))
	}

	// NewWarning takes the options of NewError, so a warning carries a path
	// that reads from the item.
	if slices.Contains(i.Tags, "seasonal") && i.Until == "" {
		problems = append(problems, niceyaml.NewWarning("a seasonal item without until stays on the menu all year",
			niceyaml.AtPath(paths.Current().Child("tags"))))
	}

	// Each problem in the join keeps its own severity, so a decode returns
	// the errors of the join and hands its warnings to the collector.
	return errors.Join(problems...)
}
