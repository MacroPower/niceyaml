// Package main provides the nyaml CLI for viewing YAML files.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	tea "charm.land/bubbletea/v2"
)

func main() {
	var (
		lineNumbers bool
		wrap        bool
		search      string
	)

	cmd := &cobra.Command{
		Use:   "nyaml <file> [file2]",
		Short: "A terminal YAML viewer with syntax highlighting",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			content, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read file: %w", err)
			}

			opts := modelOptions{
				lineNumbers: lineNumbers,
				wrap:        wrap,
				search:      search,
			}

			// Diff mode: two files provided.
			if len(args) == 2 {
				content2, err := os.ReadFile(args[1])
				if err != nil {
					return fmt.Errorf("read file: %w", err)
				}

				opts.diffMode = true
				opts.beforeContent = content
				opts.afterContent = content2
				opts.beforeFilename = args[0]
				opts.afterFilename = args[1]
			}

			m := newModel(args[0], content, &opts)

			p := tea.NewProgram(m)

			_, err = p.Run()
			if err != nil {
				return fmt.Errorf("run program: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&lineNumbers, "line-numbers", "n", false, "show line numbers")
	cmd.Flags().BoolVarP(&wrap, "wrap", "w", false, "wrap long lines")
	cmd.Flags().StringVarP(&search, "search", "s", "", "initial search term")

	err := fang.Execute(context.Background(), cmd)
	if err != nil {
		os.Exit(1)
	}
}
