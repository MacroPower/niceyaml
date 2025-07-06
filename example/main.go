// Package main is just an example showcasing niceyaml features.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/lexer"

	"github.com/macropower/niceyaml"
)

func main() {
	// Find the example directory (works from project root or example dir).
	exampleDir := findExampleDir()

	fmt.Println(header("niceyaml Demo"))
	fmt.Println()

	// Demo 1: Highlighting features (syntax, tokens, ranges, blending).
	demoHighlighting(exampleDir)

	// Demo 2: Diffing two YAML files.
	demo2Diffing(exampleDir)

	// Demo 3: String finding.
	demo3StringFinding(exampleDir)

	// Demo 4: Error formatting.
	demo4ErrorFormatting(exampleDir)
}

func header(s string) string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7D56F4")).
		Render("=== " + s + " ===")
}

func subheader(s string) string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#56F4D3")).
		Render("--- " + s + " ---")
}

func findExampleDir() string {
	// Check if we're in the example directory.
	_, err := os.Stat("sample.yaml")
	if err == nil {
		return "."
	}
	// Check if example directory exists from project root.
	_, err = os.Stat("example/sample.yaml")
	if err == nil {
		return "example"
	}

	panic("could not find example directory; run from project root or example/")
}

func mustReadFile(dir, name string) []byte {
	data, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: paths are hardcoded in example.
	if err != nil {
		panic(fmt.Sprintf("error reading %s: %v", name, err))
	}

	return data
}

// Demo 1: Consolidated highlighting demo showcasing syntax, token, range, and color blending.
func demoHighlighting(dir string) {
	fmt.Println(subheader("Demo 1: Syntax & Range Highlighting"))
	fmt.Println("YAML with syntax coloring, token highlights, multi-line ranges, and color blending.")
	fmt.Println()

	source := mustReadFile(dir, "sample.yaml")
	tokens := lexer.Tokenize(string(source))

	printer := niceyaml.NewPrinter(niceyaml.WithLineNumbers())

	// 1. Token highlight: the anchor reference "*year" on copyright line.
	goldStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#FFD700")).
		Foreground(lipgloss.Color("#000000"))
	printer.AddStyleToToken(goldStyle, niceyaml.Position{Line: 18, Col: 12})

	// 2. Single-line range: highlight "Café Yamül" on restaurant.name.
	purpleStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#7D56F4")).
		Foreground(lipgloss.Color("#FFFFFF"))
	printer.AddStyleToRange(purpleStyle, niceyaml.PositionRange{
		Start: niceyaml.Position{Line: 2, Col: 11},
		End:   niceyaml.Position{Line: 2, Col: 23},
	})

	// 3. Multi-line range: highlight the first menu item (matcha pancakes).
	tealStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#065F56")).
		Foreground(lipgloss.Color("#A7F3D0"))
	printer.AddStyleToRange(tealStyle, niceyaml.PositionRange{
		Start: niceyaml.Position{Line: 9, Col: 5},
		End:   niceyaml.Position{Line: 11, Col: 28},
	})

	// 4. Overlapping ranges for color blending on "Småkakor & Kaffe".
	redStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#DC2626"))
	printer.AddStyleToRange(redStyle, niceyaml.PositionRange{
		Start: niceyaml.Position{Line: 14, Col: 13},
		End:   niceyaml.Position{Line: 14, Col: 22}, // "Småkakor".
	})

	blueStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#2563EB"))
	printer.AddStyleToRange(blueStyle, niceyaml.PositionRange{
		Start: niceyaml.Position{Line: 14, Col: 19},
		End:   niceyaml.Position{Line: 14, Col: 30}, // "or & Kaffe" - overlaps at "kor".
	})

	fmt.Println(printer.PrintTokens(tokens))
	fmt.Println()

	// Legend.
	fmt.Println("Highlights applied:")
	fmt.Println("  - Gold: token highlight on anchor reference '*year'")
	fmt.Println("  - Purple: single-line range on 'Café Yamül'")
	fmt.Println("  - Teal: multi-line range spanning matcha pancakes item")
	fmt.Println("  - Red+Blue → Purple blend: overlapping ranges on 'Småkakor & Kaffe'")
	fmt.Println()
}

// Demo 2: Diff two YAML files.
func demo2Diffing(dir string) {
	fmt.Println(subheader("Demo 2: YAML Diffing"))
	fmt.Println("Compare sample.yaml and sample2.yaml to see changes.")
	fmt.Println()

	source1 := mustReadFile(dir, "sample.yaml")
	source2 := mustReadFile(dir, "sample2.yaml")

	tokens1 := lexer.Tokenize(string(source1))
	tokens2 := lexer.Tokenize(string(source2))

	printer := niceyaml.NewPrinter(niceyaml.WithLineNumbers())
	diff := printer.PrintTokenDiff(tokens1, tokens2)

	if diff == "" {
		fmt.Println("No differences found.")
	} else {
		fmt.Println(diff)
	}

	fmt.Println()
}

// Demo 3: Find strings within YAML tokens.
func demo3StringFinding(dir string) {
	fmt.Println(subheader("Demo 3: String Finding"))
	fmt.Println("Find all occurrences of 'ff' and highlight them.")
	fmt.Println()

	source := mustReadFile(dir, "sample.yaml")
	tokens := lexer.Tokenize(string(source))

	finder := niceyaml.NewFinder()
	matches := finder.FindStringsInTokens("ff", tokens)

	fmt.Printf("Found %d occurrence(s) of 'ff':\n", len(matches))

	for i, m := range matches {
		fmt.Printf("  %d. Line %d, Columns %d-%d\n", i+1, m.Start.Line, m.Start.Col, m.End.Col-1)
	}

	fmt.Println()

	// Highlight all matches.
	printer := niceyaml.NewPrinter(niceyaml.WithLineNumbers())
	highlightStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#FFD700")).
		Foreground(lipgloss.Color("#000000"))

	for _, m := range matches {
		printer.AddStyleToRange(highlightStyle, m)
	}

	fmt.Println("With matches highlighted:")
	fmt.Println(printer.PrintTokens(tokens))
	fmt.Println()
}

// Demo 4: Error formatting with source annotation.
func demo4ErrorFormatting(dir string) {
	fmt.Println(subheader("Demo 4: Error Formatting"))
	fmt.Println("Display errors with source context and highlighting.")
	fmt.Println()

	source := mustReadFile(dir, "sample.yaml")

	printer := niceyaml.NewPrinter(niceyaml.WithLineNumbers())
	yamlError := niceyaml.NewErrorWrapper(
		niceyaml.WithPrinter(printer),
		niceyaml.WithSourceLines(2),
	)

	// Simulate a validation error at a specific path.
	path, pathErr := yaml.PathString("$.menu.breakfast[0].price")
	if pathErr != nil {
		fmt.Fprintf(os.Stderr, "Error parsing path: %v\n", pathErr)
		return
	}

	err := yamlError.Wrap(niceyaml.NewError(
		errors.New("price exceeds maximum allowed value"),
		niceyaml.WithSource(source),
		niceyaml.WithPath(path),
	))

	fmt.Println("Simulated validation error:")
	fmt.Println(err.Error())
	fmt.Println()

	// Parse invalid YAML to get a real parsing error.
	fmt.Println("Parsing error from invalid YAML:")

	invalidSource := mustReadFile(dir, "invalid.yaml")

	dec := niceyaml.NewDecoder(bytes.NewReader(invalidSource))

	var data any

	err = dec.Decode(&data)
	if err != nil {
		annotatedErr := yamlError.Wrap(err)
		fmt.Println(annotatedErr.Error())
	}
}
