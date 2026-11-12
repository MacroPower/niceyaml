//go:build unix

package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// backgroundQuery is the query of the terminal background that view
// --print sends before it prints in color.
const backgroundQuery = "\x1b]11;?"

// printedOnTerminal returns what view --print writes for a file that holds
// input when the terminal has the color profile profile and the printer
// draws styles. Each line ends with a line feed, which the terminal turns
// into a carriage return and a line feed.
func printedOnTerminal(t *testing.T, input string, styles theme.Theme, profile colorprofile.Profile) string {
	t.Helper()

	p := printer.New(printer.WithStyles(styles.Styles().With(style.WithSurface(nil))))

	var sb strings.Builder

	w := &colorprofile.Writer{Forward: &sb, Profile: profile}

	_, err := w.WriteString(p.Print(niceyaml.NewSourceFromString(input).View()) + "\n")
	require.NoError(t, err)

	return sb.String()
}

func TestViewPrintOnTerminal(t *testing.T) {
	t.Parallel()

	if runMainHelper(t) {
		return
	}

	const input = "a: 1\n"

	// With --print, view prints the file to the terminal and exits rather
	// than open the interactive viewer. Its colors match the background
	// that the terminal reports, and NO_COLOR skips the query of the
	// background.
	tcs := map[string]struct {
		styles theme.Theme
		// The theme that the printer must not draw.
		other theme.Theme
		// The answer of the terminal to the query of its background, and
		// to the query of its attributes that follows it. Empty when view
		// must not query the background.
		reply   string
		env     []string
		profile colorprofile.Profile
	}{
		"no color": {
			env:     []string{"NO_COLOR=1"},
			styles:  theme.Charm(),
			profile: colorprofile.ASCII,
		},
		"light background": {
			env:     []string{"COLORTERM=truecolor"},
			reply:   "\x1b]11;rgb:ffff/ffff/ffff\x07\x1b[?62c",
			styles:  theme.CharmLight(),
			other:   theme.Charm(),
			profile: colorprofile.TrueColor,
		},
		"dark background": {
			env:     []string{"COLORTERM=truecolor"},
			reply:   "\x1b]11;rgb:0000/0000/0000\x07\x1b[?62c",
			styles:  theme.Charm(),
			other:   theme.CharmLight(),
			profile: colorprofile.TrueColor,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := yamltest.WriteFile(t, t.TempDir(), "a.yaml", input)

			run := startOnTerminal(t, "TestViewPrintOnTerminal", tc.env, "view", "--print", path)

			if tc.reply != "" {
				run.readUntil(t, func(output string) bool {
					return strings.Contains(output, backgroundQuery)
				})

				_, err := run.ptmx.WriteString(tc.reply)
				require.NoError(t, err)
			}

			output, err := run.wait(t)
			require.NoError(t, err)

			got := strings.ReplaceAll(output, "\r\n", "\n")

			want := printedOnTerminal(t, input, tc.styles, tc.profile)
			assert.Contains(t, got, want)
			assert.Contains(t, ansi.Strip(got), "   1  a: 1\n")

			if tc.reply == "" {
				assert.NotContains(t, got, backgroundQuery)

				return
			}

			other := printedOnTerminal(t, input, tc.other, tc.profile)
			require.NotEqual(t, want, other)
			assert.NotContains(t, got, other)
		})
	}
}

func TestViewCmdOnTerminal(t *testing.T) {
	t.Parallel()

	if runMainHelper(t) {
		return
	}

	// On a terminal, view opens the interactive viewer on the first file,
	// with the diff off.
	dir := t.TempDir()
	a := yamltest.WriteFile(t, dir, "a.yaml", "a: 1\n")
	b := yamltest.WriteFile(t, dir, "b.yaml", "a: 2\n")

	run := startOnTerminal(t, "TestViewCmdOnTerminal", nil, "view", a, b)

	run.readUntil(t, func(output string) bool {
		return strings.Contains(ansi.Strip(output), "rev 1/2")
	})

	_, err := run.ptmx.WriteString("q")
	require.NoError(t, err)

	_, err = run.wait(t)
	require.NoError(t, err)
}
