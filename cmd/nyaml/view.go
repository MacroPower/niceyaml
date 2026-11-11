package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	tea "charm.land/bubbletea/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func viewCmd() *cobra.Command {
	var flags *viewerFlags

	cmd := &cobra.Command{
		Use:   "view file.yaml [file.yaml...]",
		Short: "View YAML files with syntax highlighting",
		Long: "Show YAML files with syntax highlighting.\n\n" +
			"On a terminal, view opens an interactive viewer on the first file. " +
			"Tab and shift+tab step through the files in the order given, and " +
			"m turns on a diff of each file against the file before it. To " +
			"open on the diff of the last two files, run nyaml diff instead.\n\n" +
			"When stdout is not a terminal, or with --print, view prints each " +
			"file to stdout and exits. With several files, a \"==> file <==\" " +
			"line names each file above its content. The colors fit the " +
			"terminal, and a pipe or a file gets plain text.\n\n" +
			"Supports glob patterns like *.yaml.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, sources, err := flags.load(args)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			tty, isTerm := terminal(out)
			if flags.print || !isTerm {
				return printFiles(cmd.Context(), out, flags.lineNumbers, paths, sources)
			}

			m := newModel(&modelOptions{
				lineNumbers: flags.lineNumbers,
				search:      flags.search,
				sources:     sources,
			})

			return runViewer(cmd.Context(), tty, m)
		},
	}

	flags = addViewerFlags(cmd)

	return cmd
}

// viewerFlags holds the flags that the view and diff commands share.
type viewerFlags struct {
	limits      *limitFlags
	search      string
	lineNumbers bool
	print       bool
}

// addViewerFlags adds the flags of [viewerFlags] to cmd.
func addViewerFlags(cmd *cobra.Command) *viewerFlags {
	f := &viewerFlags{}

	// A bare shorthand on a flag that defaults to true can only repeat the
	// default, so the long flag stands alone. With --line-numbers=false,
	// the gutter shows only diff markers.
	cmd.Flags().BoolVar(&f.lineNumbers, "line-numbers", true, "show line numbers")
	cmd.Flags().StringVarP(&f.search, "search", "s", "", "initial search term of the interactive viewer")
	cmd.Flags().BoolVarP(&f.print, "print", "p", false,
		"print to stdout and exit, as when stdout is not a terminal")

	f.limits = addLimitFlags(cmd)

	return f
}

// load expands the glob patterns of args and reads each file it names
// with the limits of the flags. It returns the paths of the files, each as
// the user typed it or as the glob matched it, and their sources.
func (f *viewerFlags) load(args []string) ([]string, []*niceyaml.Source, error) {
	sourceOpts, err := f.limits.options()
	if err != nil {
		return nil, nil, err
	}

	paths, err := expandPaths(args...)
	if err != nil {
		return nil, nil, err
	}

	sources, err := loadSources(paths, sourceOpts...)
	if err != nil {
		return nil, nil, err
	}

	return paths, sources, nil
}

// runViewer runs the interactive viewer m on the terminal tty until the
// user quits or ctx ends.
func runViewer(ctx context.Context, tty io.Writer, m tea.Model) error {
	// The context that notifyContext in main cancels on SIGINT or SIGTERM
	// ends the viewer the way it ends validate. The program runs without
	// Bubble Tea's own signal handler, because that handler turns SIGTERM
	// into a clean quit with exit status 0. Ctrl+C at the keyboard still reaches the model as a key press,
	// because Bubble Tea puts the terminal in raw mode.
	p := tea.NewProgram(m,
		tea.WithContext(ctx),
		tea.WithoutSignalHandler(),
		tea.WithOutput(tty),
	)

	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("run program: %w", err)
	}

	return nil
}

// loadSources reads the file at each path into a [*niceyaml.Source] and
// names each Source with the status bar label that [revisionLabels] gives
// its path. A read error from [readSource] already names the path, so
// loadSources does not add a name of its own.
func loadSources(paths []string, opts ...niceyaml.SourceOption) ([]*niceyaml.Source, error) {
	labels := revisionLabels(paths)
	sources := make([]*niceyaml.Source, 0, len(paths))

	for i, path := range paths {
		source, err := readSource(path, append([]niceyaml.SourceOption{niceyaml.WithName(labels[i])}, opts...)...)
		if err != nil {
			return nil, err
		}

		sources = append(sources, source)
	}

	return sources, nil
}

// printFiles writes each source to w with syntax highlighting, in the
// order of sources. With several sources, a "==> path <==" line above each
// names its file, and a blank line separates the files. The path at the
// index of a source in paths names its file. An empty file prints nothing
// below its name.
//
// When ctx ends, printFiles stops before the next file and returns an
// error that wraps the error of ctx.
func printFiles(ctx context.Context, w io.Writer, lineNumbers bool, paths []string, sources []*niceyaml.Source) error {
	out, p := newPrintOutput(w, lineNumbers)

	for i, source := range sources {
		err := ctx.Err()
		if err != nil {
			return fmt.Errorf("print %s: %w", escape.Line(paths[i]), err)
		}

		var sb strings.Builder

		if len(sources) > 1 {
			if i > 0 {
				sb.WriteString("\n")
			}

			fmt.Fprintf(&sb, "==> %s <==\n", escape.Line(paths[i]))
		}

		if view := p.Print(source.View()); view != "" {
			sb.WriteString(view + "\n")
		}

		_, err = out.WriteString(sb.String())
		if err != nil {
			return fmt.Errorf("write %s: %w", escape.Line(paths[i]), err)
		}
	}

	return nil
}

// newPrintOutput returns a writer to w that fits colors to w, and the
// printer that highlights what the commands print to it. The writer
// converts each color to one that w supports. It drops the colors under
// NO_COLOR, and it drops every escape sequence when w is not a terminal.
//
// The printer draws [theme.Charm] on a dark terminal and
// [theme.CharmLight] on a light one. It leaves out the surface of each
// theme, so the text shows the terminal's own background. When w is not a terminal, or
// when the terminal does not report its background, the printer draws
// [theme.Charm]. The gutter shows line numbers and diff markers, or diff
// markers alone when lineNumbers is false.
func newPrintOutput(w io.Writer, lineNumbers bool) (*colorprofile.Writer, *printer.Printer) {
	out := colorprofile.NewWriter(w, os.Environ())

	styles := theme.Charm

	// The query of the background waits for the terminal to answer, so a
	// run without color skips it.
	if f, ok := terminal(w); ok && out.Profile > colorprofile.ASCII &&
		!lipgloss.HasDarkBackground(replyInput(f), f) {
		styles = theme.CharmLight
	}

	gutter := printer.DefaultGutter
	if !lineNumbers {
		gutter = printer.DiffGutter
	}

	p := printer.New(
		printer.WithStyles(styles.Styles().With(style.WithSurface(nil))),
		printer.WithGutter(gutter),
	)

	return out, p
}

// terminal returns the terminal that w writes to, and whether w writes to
// one.
func terminal(w io.Writer) (term.File, bool) {
	f, ok := w.(term.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		return nil, false
	}

	return f, true
}

// replyInput returns the file that [lipgloss.HasDarkBackground] reads the
// reply of the terminal behind f from. On Unix, the terminal device of f
// reads as well as it writes, so f serves, even when stdin is a pipe. On
// Windows, a console reads its replies from its input handle and never
// from the output handle of f. Stdin is that input handle.
func replyInput(f term.File) term.File {
	if runtime.GOOS == "windows" {
		return os.Stdin
	}

	return f
}
