// Package main provides the nyaml CLI for viewing and validating YAML files.
package main

import (
	"context"
	"image/color"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/spf13/cobra"

	"github.com/macropower/niceyaml/fangs"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "nyaml",
		Short: "A terminal YAML utility with syntax highlighting",
	}

	rootCmd.AddCommand(viewCmd())
	rootCmd.AddCommand(validateCmd())

	err := fang.Execute(context.Background(), rootCmd,
		fang.WithErrorHandler(fangs.ErrorHandler),
		fang.WithColorSchemeFunc(fangColorScheme),
	)
	if err != nil {
		os.Exit(1)
	}
}

func fangColorScheme(c lipgloss.LightDarkFunc) fang.ColorScheme {
	return fang.ColorScheme{
		Base:           c(charmtone.Charcoal, charmtone.Ash),
		Title:          charmtone.Charple,
		Codeblock:      c(charmtone.Salt, lipgloss.Color("#2F2E36")),
		Program:        c(charmtone.Malibu, charmtone.Guppy),
		Command:        c(charmtone.Pony, charmtone.Cheeky),
		DimmedArgument: c(charmtone.Squid, charmtone.Oyster),
		Comment:        c(charmtone.Squid, lipgloss.Color("#747282")),
		Flag:           c(lipgloss.Color("#0CB37F"), charmtone.Guac),
		Argument:       c(charmtone.Charcoal, charmtone.Ash),
		Description:    c(charmtone.Charcoal, charmtone.Ash), // Flag and command descriptions.
		FlagDefault:    c(charmtone.Smoke, charmtone.Squid),  // Flag default values in descriptions.
		QuotedString:   c(charmtone.Coral, charmtone.Salmon),
		ErrorHeader: [2]color.Color{
			charmtone.Butter,
			charmtone.Cherry,
		},
	}
}
