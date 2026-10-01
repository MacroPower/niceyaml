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
		RunE: func(cmd *cobra.Command, args []string) error {
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

			// The context that notifyContext in main cancels on SIGINT or
			// SIGTERM ends the viewer the way it ends validate. Bubble
			// Tea's own signal handler would turn SIGTERM into a clean
			// quit, and when it lost the race for a signal to
			// notifyContext, the viewer would hang on exit. Ctrl+C at the
			// keyboard still reaches the model as a key press, since
			// Bubble Tea puts the terminal in raw mode.
			p := tea.NewProgram(m,
				tea.WithContext(cmd.Context()),
				tea.WithoutSignalHandler(),
			)

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
// its path. A read error from [readSource] already names the path, so
// loadSources adds no name of its own.
func loadSources(paths []string) ([]*niceyaml.Source, error) {
	labels := revisionLabels(paths)
	sources := make([]*niceyaml.Source, 0, len(paths))

	for i, path := range paths {
		source, err := readSource(path, niceyaml.WithName(labels[i]))
		if err != nil {
			return nil, err
		}

		sources = append(sources, source)
	}

	return sources, nil
}
