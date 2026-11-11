package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/escape"
)

// errTooFewFiles is the error of a diff command whose arguments name fewer
// than two files after the glob patterns expand.
var errTooFewFiles = errors.New("diff needs at least two files")

// hunkContext is the number of unchanged lines that the printed diff shows
// around each change.
const hunkContext = 3

func diffCmd() *cobra.Command {
	var flags *viewerFlags

	cmd := &cobra.Command{
		Use:   "diff before.yaml after.yaml [file.yaml...]",
		Short: "Diff YAML files with syntax highlighting",
		Long: "Compare YAML files in the order given, each with the file before it.\n\n" +
			"On a terminal, diff opens an interactive viewer on the diff of the " +
			"last two files. Tab and shift+tab step through the files, each " +
			"shown as a diff against the file before it, and m switches the " +
			"diff to the first file or turns it off.\n\n" +
			"When stdout is not a terminal, or with --print, diff prints the " +
			"changes of each pair of adjacent files to stdout and exits. " +
			"The \"---\" line of a pair names the earlier file, the \"+++\" " +
			"line names the later one, and the hunks below them show 3 lines " +
			"of context around each change. A pair of files with the same lines prints nothing. The " +
			"colors fit the terminal, and a pipe or a file gets plain text.\n\n" +
			"The exit status does not depend on whether the files differ.\n\n" +
			"Supports glob patterns like *.yaml. A file that the arguments " +
			"name twice counts once, so diff needs at least two files after " +
			"the patterns expand.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, sources, err := loadFiles(args)
			if err != nil {
				return err
			}

			// A glob pattern can name several files, so the count of
			// files counts after the expansion.
			if len(sources) < 2 {
				return fmt.Errorf("%w, got %d", errTooFewFiles, len(sources))
			}

			out := cmd.OutOrStdout()

			tty, isTerm := terminal(out)
			if flags.print || !isTerm {
				return printDiffs(cmd.Context(), out, flags.lineNumbers, paths, sources)
			}

			m := newModel(&modelOptions{
				lineNumbers: flags.lineNumbers,
				search:      flags.search,
				sources:     sources,
				diff:        true,
			})

			return runViewer(cmd.Context(), tty, m)
		},
	}

	flags = addViewerFlags(cmd)

	return cmd
}

// printDiffs writes the hunks of the diff of each pair of adjacent sources
// to w, with [hunkContext] lines of context. Two lines above the hunks
// name the files of the pair. The "---" line names the earlier file, and
// the "+++" line names the later one. A blank line separates the pairs.
// The path at the index of a source in paths names its file. A pair
// without changes prints nothing.
//
// When ctx ends, printDiffs stops and returns an error that wraps the
// error of ctx.
func printDiffs(ctx context.Context, w io.Writer, lineNumbers bool, paths []string, sources []*niceyaml.Source) error {
	out, p := newPrintOutput(w, lineNumbers)

	printed := false

	for i := 1; i < len(sources); i++ {
		before, after := escape.Line(paths[i-1]), escape.Line(paths[i])

		result, err := diff.Diff(ctx, sources[i-1].View(), sources[i].View())
		if err != nil {
			return fmt.Errorf("diff %s and %s: %w", before, after, err)
		}

		if !result.Stats().Changed() {
			continue
		}

		var sb strings.Builder

		if printed {
			sb.WriteString("\n")
		}

		fmt.Fprintf(&sb, "--- %s\n+++ %s\n", before, after)
		sb.WriteString(p.Print(result.Hunks(hunkContext)) + "\n")

		_, err = out.WriteString(sb.String())
		if err != nil {
			return fmt.Errorf("write the diff of %s and %s: %w", before, after, err)
		}

		printed = true
	}

	return nil
}
