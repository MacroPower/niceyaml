// Package main provides the nyaml CLI for viewing and validating YAML files.
package main

import (
	"context"
	"fmt"
	"image/color"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "nyaml",
		Short: "A terminal YAML utility with syntax highlighting",
	}

	rootCmd.AddCommand(viewCmd())
	rootCmd.AddCommand(validateCmd())

	err := fang.Execute(context.Background(), rootCmd,
		fang.WithErrorHandler(fangErrorHandler),
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

//nolint:gocritic // hugeParam: required by [fang.ErrorHandler] signature.
func fangErrorHandler(w io.Writer, styles fang.Styles, err error) {
	mustN(fmt.Fprintln(w, styles.ErrorHeader.String()))
	mustN(fmt.Fprintln(w, lipgloss.NewStyle().MarginLeft(2).Render(err.Error())))
	mustN(fmt.Fprintln(w))
	if isUsageError(err) {
		mustN(fmt.Fprintln(w, lipgloss.JoinHorizontal(
			lipgloss.Left,
			styles.ErrorText.UnsetWidth().Render("Try"),
			styles.Program.Flag.Render("--help"),
			styles.ErrorText.UnsetWidth().UnsetMargins().UnsetTransform().PaddingLeft(1).Render("for usage."),
		)))
		mustN(fmt.Fprintln(w))
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func mustN(_ int, err error) {
	must(err)
}

// XXX: this is a hack to detect usage errors.
// See: https://github.com/spf13/cobra/pull/2266
func isUsageError(err error) bool {
	s := err.Error()
	for _, prefix := range []string{
		"flag needs an argument:",
		"unknown flag:",
		"unknown shorthand flag:",
		"unknown command",
		"invalid argument",
	} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}

	return false
}
