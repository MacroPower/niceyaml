package main

import (
	"fmt"

	"github.com/spf13/cobra"

	tea "charm.land/bubbletea/v2"
)

func viewCmd() *cobra.Command {
	var (
		lineNumbers bool
		search      string
	)

	cmd := &cobra.Command{
		Use:   "view file.yaml [pattern...]",
		Short: "View YAML files with syntax highlighting",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			files, err := expandGlobs(args)
			if err != nil {
				return err
			}

			opts := modelOptions{
				lineNumbers: lineNumbers,
				search:      search,
				files:       files,
			}

			m := newModel(&opts)

			p := tea.NewProgram(m)

			_, err = p.Run()
			if err != nil {
				return fmt.Errorf("run program: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&lineNumbers, "line-numbers", "n", true, "show line numbers")
	cmd.Flags().StringVarP(&search, "search", "s", "", "initial search term")

	return cmd
}
