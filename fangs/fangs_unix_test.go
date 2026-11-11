//go:build unix

package fangs_test

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// endOfOutput is the text the test writes to the terminal after the
// handler returns, so the reader knows that the output is complete.
const endOfOutput = "<end of output>"

// backgroundQuery is what a query for the background of a terminal writes
// to it: a request for the background color, then one for the device
// attributes, which every terminal answers.
const backgroundQuery = "\x1b]11;?\a\x1b[c"

// throughColorProfile returns the writer that fang hands an error handler
// for tty: a [*colorprofile.Writer] with a profile that supports color.
func throughColorProfile(tty *os.File) io.Writer {
	return &colorprofile.Writer{Forward: tty, Profile: colorprofile.TrueColor}
}

// direct returns tty itself, as a caller that hands an error handler the
// terminal without fang does.
func direct(tty *os.File) io.Writer {
	return tty
}

// handleOnTerminal returns what handler writes for err under [testStyles]
// to a pseudo-terminal that is cols columns wide. The handler writes to
// what writer returns for the terminal. The terminal answers a
// query for its background with bg, an XParseColor color such as
// "rgb:ffff/ffff/ffff". The returned text leaves out that query, which a
// terminal does not show, and ends its lines with "\n" alone.
func handleOnTerminal(
	t *testing.T,
	handler fang.ErrorHandler,
	writer func(tty *os.File) io.Writer,
	cols uint16,
	bg string,
	err error,
) string {
	t.Helper()

	ptmx, tty, openErr := pty.Open()
	require.NoError(t, openErr)

	t.Cleanup(func() {
		_ = tty.Close()  //nolint:errcheck // Best effort.
		_ = ptmx.Close() //nolint:errcheck // Best effort.
	})

	require.NoError(t, pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: cols}))

	// The terminal blocks the handler when its buffer fills, so the test
	// reads everything the handler writes as it arrives. It answers the
	// query for the background and the device attributes that follow it.
	out := make(chan string, 1)

	go func() {
		var acc bytes.Buffer

		answered := false
		buf := make([]byte, 4096)

		for {
			n, readErr := ptmx.Read(buf)
			acc.Write(buf[:n])

			if !answered && bytes.Contains(acc.Bytes(), []byte(backgroundQuery)) {
				answered = true

				// A failed write leaves the handler on dark, which the
				// comparison of the output reports.
				_, _ = ptmx.WriteString("\x1b]11;" + bg + "\x07\x1b[?62c") //nolint:errcheck // See above.
			}

			if before, _, found := strings.Cut(acc.String(), endOfOutput); found || readErr != nil {
				out <- before

				return
			}
		}
	}()

	handler(writer(tty), testStyles(), err)

	_, writeErr := tty.WriteString(endOfOutput)
	require.NoError(t, writeErr)

	select {
	case got := <-out:
		got = strings.ReplaceAll(got, backgroundQuery, "")

		return strings.ReplaceAll(got, "\r\n", "\n")

	case <-time.After(30 * time.Second):
		t.Fatal("the terminal did not show the output")

		return ""
	}
}

// Light and dark backgrounds, as a terminal reports them.
const (
	lightBackground = "rgb:ffff/ffff/ffff"
	darkBackground  = "rgb:0000/0000/0000"
)

func TestErrorHandler_TerminalWidth(t *testing.T) {
	t.Parallel()

	// The value is a single token longer than any width, so the excerpt
	// wraps it into rows that fill the width the printer wraps at.
	src := niceyaml.NewSourceFromTokens(tokens.Tokenize("name: " + strings.Repeat("x", 300) + "\n"))
	err := yamltest.Bind(t, src, errAt("invalid name", "$.name"))

	tcs := map[string]struct {
		handler fang.ErrorHandler
		writer  func(tty *os.File) io.Writer
		cols    uint16
		want    int
	}{
		"the default handler fits the terminal": {
			handler: fangs.ErrorHandler,
			cols:    40,
			want:    40,
		},
		"a terminal handed to the handler directly": {
			handler: fangs.ErrorHandler,
			writer:  direct,
			cols:    50,
			want:    50,
		},
		"a printer without a width fits the terminal": {
			handler: fangs.NewErrorHandler(fangs.WithPrinter(printer.New())),
			cols:    60,
			want:    60,
		},
		"a narrower width of the printer wins": {
			handler: fangs.NewErrorHandler(fangs.WithPrinter(printer.New(printer.WithWrapWidth(30)))),
			cols:    60,
			want:    30,
		},
		"a wider width of the printer wins": {
			handler: fangs.NewErrorHandler(fangs.WithPrinter(printer.New(printer.WithWrapWidth(70)))),
			cols:    40,
			want:    70,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			writer := tc.writer
			if writer == nil {
				writer = throughColorProfile
			}

			got := handleOnTerminal(t, tc.handler, writer, tc.cols, darkBackground, err)

			// The widest row fills the width, so no row runs past it.
			assert.Equal(t, tc.want, lipgloss.Width(got))
		})
	}
}

func TestNewErrorHandler_WithLightDarkStylesOnTerminal(t *testing.T) {
	t.Parallel()

	err := yamltest.Bind(t, nameValueSource(), errAt("bad name", "$.name~"))

	// The light styles tag only errors, so output in them differs from
	// output in the dark styles.
	light := yamltest.NewXMLStyles(yamltest.XMLStyleInclude(kind.GenericError))
	dark := yamltest.NewXMLStyles()

	charmLight := theme.CharmLight.Styles()
	plainCharmLight := charmLight.With(style.WithSurface(nil))

	const cols = 80

	tcs := map[string]struct {
		writer func(tty *os.File) io.Writer
		light  style.Styler
		want   style.Styler
		bg     string
	}{
		"light terminal": {
			writer: throughColorProfile,
			light:  light,
			bg:     lightBackground,
			want:   light,
		},
		"dark terminal": {
			writer: throughColorProfile,
			light:  light,
			bg:     darkBackground,
			want:   dark,
		},
		"light terminal handed to the handler directly": {
			writer: direct,
			light:  light,
			bg:     lightBackground,
			want:   light,
		},
		"nil light selects the default palette": {
			writer: throughColorProfile,
			bg:     lightBackground,
			want:   style.Default(false),
		},
		"nil pointer light selects the default palette": {
			writer: throughColorProfile,
			light:  (*style.Styles)(nil),
			bg:     lightBackground,
			want:   style.Default(false),
		},
		"light theme without its surface": {
			writer: throughColorProfile,
			light:  theme.CharmLight,
			bg:     lightBackground,
			want:   plainCharmLight,
		},
		"light styles value without its surface": {
			writer: throughColorProfile,
			light:  charmLight,
			bg:     lightBackground,
			want:   plainCharmLight,
		},
		"light styles pointer without its surface": {
			writer: throughColorProfile,
			light:  &charmLight,
			bg:     lightBackground,
			want:   plainCharmLight,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := xmlPrinter()
			handler := fangs.NewErrorHandler(fangs.WithPrinter(p), fangs.WithLightDarkStyles(tc.light, dark))

			want := report(p.With(
				printer.WithStyles(tc.want),
				printer.WithWrapWidth(cols-fangs.Indent),
			).PrintError(err))

			assert.Equal(t, want, handleOnTerminal(t, handler, tc.writer, cols, tc.bg, err))
		})
	}

	t.Run("the surface of the light theme changes the output", func(t *testing.T) {
		t.Parallel()

		p := xmlPrinter()

		assert.NotEqual(t,
			p.With(printer.WithStyles(charmLight)).PrintError(err),
			p.With(printer.WithStyles(plainCharmLight)).PrintError(err),
		)
	})

	t.Run("the default palette differs from the dark styles", func(t *testing.T) {
		t.Parallel()

		p := xmlPrinter()

		assert.NotEqual(t,
			p.With(printer.WithStyles(style.Default(false))).PrintError(err),
			p.With(printer.WithStyles(dark)).PrintError(err),
		)
	})
}
