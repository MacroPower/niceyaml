package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	tea "charm.land/bubbletea/v2"

	"go.jacobcolvin.com/niceyaml/internal/filepaths"
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
			paths, err := filepaths.Expand(args...)
			if err != nil {
				return err
			}

			files := make([]fileEntry, 0, len(paths))
			for _, path := range paths {
				content, err := os.ReadFile(path) //nolint:gosec // User-provided file paths are intentional.
				if err != nil {
					return fmt.Errorf("read file %s: %w", path, err)
				}

				files = append(files, fileEntry{path: path, content: content})
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
