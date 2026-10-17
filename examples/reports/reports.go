package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
)

var (
	//go:embed menus/*.yaml
	menus embed.FS

	// A workflow command reads a percent sign, a carriage return, and a
	// line feed in its message as markup. A property value also reads a
	// colon and a comma as markup.
	messageEscaper  = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	propertyEscaper = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C")
)

func main() {
	ctx := context.Background()
	p := printer.New()

	names, err := fs.Glob(menus, "menus/*.yaml")
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Println("\nCheck each file:")

	// The program reports every file at the end, so it keeps the error of
	// each file after it drops the source.
	var kept []error

	for _, path := range names {
		source, err := niceyaml.NewSourceFromFS(menus, path)
		if err != nil {
			lipgloss.Println(p.PrintError(err))

			return
		}

		// Each file gets a collector of its own. Report returns every
		// warning that the collector holds, so a shared collector adds the
		// warnings of earlier files to the report of this one. A collector
		// also keeps the source of each of its warnings alive until the
		// program drops the collector.
		warnings := niceyaml.NewWarnings()
		err = source.ValidateDocuments(warnings.Context(ctx), niceyaml.ValidatorFunc(checkMenu))

		// The call returns its errors and hands its warnings to the
		// collector. An error fails the file and a warning does not, so the
		// outcome comes from err alone. Report joins the warnings to err.
		// It returns the warnings of a file that passed, so the program
		// calls Report even when err is nil.
		failed := err != nil

		err = warnings.Report(err)
		if err != nil {
			// A bound error keeps its source alive, with every line and
			// token of it. Detach keeps a copy of the lines that the
			// excerpts show, so the garbage collector can free the source of
			// this file. The error of a file that does not parse still holds
			// the tokens of the parse.
			err = niceyaml.Detach(err, niceyaml.WithContextLines(1))
			kept = append(kept, err)
		}

		switch {
		case failed:
			fmt.Printf("%s: failed\n", path)
		case err != nil:
			fmt.Printf("%s: passed with warnings\n", path)
		default:
			fmt.Printf("%s: valid\n", path)
		}
	}

	all := errors.Join(kept...)

	fmt.Println("\nPrint every problem at the end:")

	// A detached error renders as it did with its source, with an excerpt
	// for each file under the name of the file. The printer asks for 5
	// context lines, but each excerpt keeps the 1 that Detach got.
	lipgloss.Println(p.PrintError(all, niceyaml.WithContextLines(5)))

	fmt.Println("\nWrite GitHub workflow commands:")

	// A step writes each workflow command to its output in the form
	// "::{level} file={name},line={line},col={col},title={title}::{message}".
	// GitHub turns each command into an annotation on the line of the file
	// that the line property names. The level is error, warning, or notice,
	// and the Warning field of each problem picks between error and warning.
	// Each child of a problem points at a related place, and the loop writes
	// it as a notice. The title of the notice holds the message of the
	// problem, so a reviewer who reads only the notice knows which problem it
	// explains.
	for problem := range niceyaml.Problems(all) {
		level := "error"
		if problem.Warning {
			level = "warning"
		}

		fmt.Println(workflowCommand(level, problem, ""))

		for _, detail := range problem.Children {
			fmt.Println(workflowCommand("notice", detail, problem.Message()))
		}
	}

	fmt.Println("\nWrite a Markdown summary:")

	// An ErrorReport holds the parts that PrintError draws, so a renderer
	// of another format draws the same tree and the same excerpts. A job
	// writes such a summary to the file that GITHUB_STEP_SUMMARY names.
	rep := niceyaml.NewErrorReport(all)
	markdownTree(rep.Tree, 0)

	for _, excerpt := range rep.Excerpts {
		// String does not end the excerpt with a line feed, so the format
		// string puts one before the closing fence.
		fmt.Printf("\n%s\n\n```text\n%s\n```\n", escape.Control(excerpt.Name), excerpt.View.String())
	}
}

// workflowCommand formats a node as a GitHub Actions workflow command at
// level, with title above its message when title is not empty.
func workflowCommand(level string, node niceyaml.ErrorTree, title string) string {
	var props []string

	// A node that is not bound to a source does not have a file. A node
	// whose location did not resolve does not have a position. The command
	// leaves out each property that the node lacks. GitHub then uses the
	// default of each missing property, which is 1 for line and .github for
	// file.
	if name := node.Name(); name != "" {
		props = append(props, "file="+propertyEscaper.Replace(name))
	}

	// A position counts from 0, and a workflow command counts from 1.
	if pos, ok := node.Position(); ok {
		props = append(props, fmt.Sprintf("line=%d", pos.Line+1), fmt.Sprintf("col=%d", pos.Col+1))
	}

	if title != "" {
		props = append(props, "title="+propertyEscaper.Replace(title))
	}

	command := "::" + level
	if len(props) > 0 {
		command += " " + strings.Join(props, ",")
	}

	return command + "::" + messageEscaper.Replace(node.Message())
}

// markdownTree writes the rows of node as a nested Markdown list. A node
// without a label, such as a join, does not add a row, and its children
// take its place.
func markdownTree(node niceyaml.ErrorTree, depth int) {
	if node.Label != "" {
		// A label holds the message as the check wrote it and the name of
		// the source as the program gave it. Either might hold a control
		// character.
		fmt.Printf("%s- %s\n", strings.Repeat("  ", depth), escape.Control(node.Label))

		depth++
	}

	for _, child := range node.Children {
		markdownTree(child, depth)
	}
}

// checkMenu rejects an item that the menu lists twice and warns about an
// item that sets its price under the old key.
func checkMenu(ctx context.Context, n *niceyaml.Node) error {
	items, err := n.Nodes(paths.Current().Child("menu").IndexAll())
	if err != nil {
		return fmt.Errorf("select items: %w", err)
	}

	var errs []error

	first := map[string]paths.Path{}

	for _, item := range items {
		itemPath := item.Path().Child("item")

		name, err := item.DecodeAt[string](ctx, paths.Current().Child("item"))
		if err != nil {
			return fmt.Errorf("decode item: %w", err)
		}

		if seen, ok := first[name]; ok {
			errs = append(errs, niceyaml.NewError("item is listed twice", niceyaml.AtPath(itemPath),
				niceyaml.WithDetails(niceyaml.NewError("first listed here", niceyaml.AtPath(seen)))))
		} else {
			first[name] = itemPath
		}

		// Nodes selects nothing for an item without the old key, so the
		// loop warns only where "pris" appears. Key picks the key of the
		// entry rather than its value. A warning without a location binds
		// at the Node that reports it, so the excerpt marks the key that
		// the message names. The warning goes to the collector of the
		// context and does not fail the check.
		legacy, err := item.Nodes(paths.Current().Child("pris").Key())
		if err != nil {
			return fmt.Errorf("select old price key: %w", err)
		}

		for _, key := range legacy {
			key.Warn(ctx, niceyaml.NewWarning(`"pris" is the old name of "price"`))
		}
	}

	return errors.Join(errs...)
}
