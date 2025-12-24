package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	tea "charm.land/bubbletea/v2"
)

func viewCmd() *cobra.Command {
	var (
		lineNumbers bool
		wrap        bool
		search      string
	)

	cmd := &cobra.Command{
		Use:   "view file.yaml [file.yaml...]",
		Short: "View YAML files with syntax highlighting",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			// Read all files.
			var contents [][]byte
			for _, arg := range args {
				content, err := os.ReadFile(arg) //nolint:gosec // User-provided file paths are intentional.
				if err != nil {
					return fmt.Errorf("read file %s: %w", arg, err)
				}

				contents = append(contents, content)
			}

			opts := modelOptions{
				lineNumbers: lineNumbers,
				wrap:        wrap,
				search:      search,
				contents:    contents,
			}

			m := newModel(&opts)

			p := tea.NewProgram(m)

			_, err := p.Run()
			if err != nil {
				return fmt.Errorf("run program: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&lineNumbers, "line-numbers", "n", true, "show line numbers")
	cmd.Flags().BoolVarP(&wrap, "wrap", "w", false, "wrap long lines")
	cmd.Flags().StringVarP(&search, "search", "s", "", "initial search term")

	return cmd
}
