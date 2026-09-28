package main

import (
	"fmt"

	"github.com/spf13/cobra"

	tea "charm.land/bubbletea/v2"

	"go.jacobcolvin.com/niceyaml"
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
			paths, err := expandPaths(args...)
			if err != nil {
				return err
			}

			sources, err := loadSources(paths)
			if err != nil {
				return err
			}

			opts := modelOptions{
				lineNumbers: lineNumbers,
				search:      search,
				sources:     sources,
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

	// A bare shorthand on a flag that defaults to true can only repeat the
	// default, so the long flag stands alone. With --line-numbers=false,
	// the gutter shows diff markers only.
	cmd.Flags().BoolVar(&lineNumbers, "line-numbers", true, "show line numbers")
	cmd.Flags().StringVarP(&search, "search", "s", "", "initial search term")

	return cmd
}

// loadSources reads the file at each path into a [*niceyaml.Source] and
// names each Source with the status bar label that [revisionLabels] gives
// its path. A read error already names the path, so loadSources returns
// it unchanged.
func loadSources(paths []string) ([]*niceyaml.Source, error) {
	labels := revisionLabels(paths)
	sources := make([]*niceyaml.Source, 0, len(paths))

	for i, path := range paths {
		source, err := niceyaml.NewSourceFromFile(path, niceyaml.WithName(labels[i]))
		if err != nil {
			return nil, err
		}

		sources = append(sources, source)
	}

	return sources, nil
}
