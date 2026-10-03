package fangs_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// silentError wraps another error and blanks its message, so the
// [niceyaml.SourceError] holding it renders the position and the path alone
// while still resolving a detail.
type silentError struct{ err error }

func (s silentError) Error() string { return "" }
func (s silentError) Unwrap() error { return s.err }

// unwritableWriter is the writer an error handler gets after the stream it
// reports on closes.
type unwritableWriter struct{}

func (unwritableWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func testStyles() fang.Styles {
	return fang.Styles{
		ErrorHeader: lipgloss.NewStyle().SetString("Error"),
		ErrorText:   lipgloss.NewStyle(),
		Program: fang.Program{
			Flag: lipgloss.NewStyle(),
		},
	}
}

func TestErrorHandler(t *testing.T) {
	t.Parallel()

	// Set up the source the niceyaml error cases render against.
	source := stringtest.Input(`
		name: test
		value: 123
	`)
	xmlPrinter := func() *printer.Printer {
		return printer.New(
			printer.WithStyles(yamltest.NewXMLStyles()),
			printer.WithGutter(printer.NoGutter),
			printer.WithContainerStyle(lipgloss.NewStyle()),
		)
	}

	src := niceyaml.NewSourceFromTokens(tokens.Tokenize(source))

	niceyamlErr := yamltest.Bind(t, src, niceyaml.NewError(
		"invalid name",
		niceyaml.AtPath(paths.MustParse("$.name~")),
	))

	// Two named sources with the same content, so a joined error names the
	// file each branch came from and renders one excerpt per file.
	fileA := niceyaml.NewSourceFromTokens(tokens.Tokenize(source), niceyaml.WithName("a.yaml"))
	fileB := niceyaml.NewSourceFromTokens(tokens.Tokenize(source), niceyaml.WithName("b.yaml"))

	badName := yamltest.Bind(t, fileA, niceyaml.NewError(
		"bad name",
		niceyaml.AtPath(paths.MustParse("$.name~")),
	))

	badValue := yamltest.Bind(t, fileB, niceyaml.NewError(
		"bad value",
		niceyaml.AtPath(paths.MustParse("$.value")),
	))

	// A source whose name opens with Cobra's wording for an unknown flag,
	// so the message of an error bound to it does too.
	flagLikeFile := niceyaml.NewSourceFromTokens(
		tokens.Tokenize(source),
		niceyaml.WithName("unknown flag: a.yaml"),
	)

	flagLikeNameErr := yamltest.Bind(t, flagLikeFile, niceyaml.NewError(
		"bad name",
		niceyaml.AtPath(paths.MustParse("$.name~")),
	))

	emptyMessageErr := yamltest.Bind(t, src, niceyaml.WrapError(
		silentError{niceyaml.NewError("bad name", niceyaml.AtPath(paths.MustParse("$.name~")))},
	))

	// A wrapper around a join heads the problems of the join, here and in
	// the niceyaml version the go.mod of this module requires.
	nestedErr := yamltest.Bind(t, src, fmt.Errorf("two problems: %w", errors.Join(
		niceyaml.NewError("bad name", niceyaml.AtPath(paths.MustParse("$.name~"))),
		niceyaml.NewError("bad value", niceyaml.AtPath(paths.MustParse("$.value"))),
	)))

	// A source that ends with blank lines, which an excerpt of its last
	// key shows as context below the caret.
	spaced := niceyaml.NewSourceFromString("name: test\n\n\n")
	spacedErr := yamltest.Bind(t, spaced, niceyaml.NewError(
		"bad name",
		niceyaml.AtPath(paths.MustParse("$.name")),
	))

	tcs := map[string]struct {
		err  error
		want string
	}{
		"nil error": {
			err: nil,
			want: stringtest.JoinLF(
				"Error",
				"  ",
				"",
				"",
			),
		},
		"simple error": {
			err: errors.New("something went wrong"),
			want: stringtest.JoinLF(
				"Error",
				"  something went wrong",
				"",
				"",
			),
		},
		"multi-line error": {
			err: errors.New("line1\nline2\nline3"),
			want: stringtest.JoinLF(
				"Error",
				"  line1",
				"  line2",
				"  line3",
				"",
				"",
			),
		},
		"usage error flag needs argument": {
			err: errors.New("flag needs an argument: --config"),
			want: stringtest.JoinLF(
				"Error",
				"  flag needs an argument: --config",
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		"usage error unknown flag": {
			err: errors.New("unknown flag: --foo"),
			want: stringtest.JoinLF(
				"Error",
				"  unknown flag: --foo",
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		"usage error bad flag syntax": {
			err: errors.New("bad flag syntax: ---flag"),
			want: stringtest.JoinLF(
				"Error",
				"  bad flag syntax: ---flag",
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		"usage error exclusive flag group": {
			err: errors.New("if any flags in the group [a b] are set none of the others can be; [a b] were all set"),
			want: stringtest.JoinLF(
				"Error",
				"  if any flags in the group [a b] are set none of the others can be; [a b] were all set",
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		"usage error required flag group": {
			err: errors.New("at least one of the flags in the group [a b] is required"),
			want: stringtest.JoinLF(
				"Error",
				"  at least one of the flags in the group [a b] is required",
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		"usage error unknown shorthand flag": {
			err: errors.New("unknown shorthand flag: 'x' in -xyz"),
			want: stringtest.JoinLF(
				"Error",
				"  unknown shorthand flag: 'x' in -xyz",
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		"usage error unknown command": {
			err: errors.New(`unknown command "foo" for "nyaml"`),
			want: stringtest.JoinLF(
				"Error",
				`  unknown command "foo" for "nyaml"`,
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		// Cobra indents each suggestion with a tab and ends the list with a
		// line break.
		"usage error unknown command with a suggestion": {
			err: errors.New("unknown command \"valdate\" for \"nyaml\"\n\nDid you mean this?\n\tvalidate\n"),
			want: stringtest.JoinLF(
				"Error",
				`  unknown command "valdate" for "nyaml"`,
				"  ",
				"  Did you mean this?",
				"      validate",
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		"message ending in line breaks": {
			err: errors.New("something went wrong\n\n"),
			want: stringtest.JoinLF(
				"Error",
				"  something went wrong",
				"",
				"",
			),
		},
		// The blank lines belong to the document, so the handler prints
		// them as it prints the rest of the excerpt.
		"excerpt ending in blank lines keeps them": {
			err: spacedErr,
			want: stringtest.JoinLF(
				"Error",
				"  1:7: $.name: bad name",
				"  ",
				"  <nameTag>name</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>test</genericError>",
				"  <textError>      ^^^^</textError>",
				"  ",
				"  ",
				"",
				"",
			),
		},
		"usage error invalid argument": {
			err: errors.New(`invalid argument "foo" for "--count"`),
			want: stringtest.JoinLF(
				"Error",
				`  invalid argument "foo" for "--count"`,
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		"usage error requires at least": {
			err: errors.New("requires at least 1 arg(s), only received 0"),
			want: stringtest.JoinLF(
				"Error",
				"  requires at least 1 arg(s), only received 0",
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		"usage error accepts at most": {
			err: errors.New("accepts at most 2 arg(s), received 3"),
			want: stringtest.JoinLF(
				"Error",
				"  accepts at most 2 arg(s), received 3",
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		"usage error accepts exactly": {
			err: errors.New("accepts 1 arg(s), received 2"),
			want: stringtest.JoinLF(
				"Error",
				"  accepts 1 arg(s), received 2",
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		"usage error accepts between": {
			err: errors.New("accepts between 1 and 2 arg(s), received 3"),
			want: stringtest.JoinLF(
				"Error",
				"  accepts between 1 and 2 arg(s), received 3",
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		"usage error required flag not set": {
			err: errors.New(`required flag(s) "schema" not set`),
			want: stringtest.JoinLF(
				"Error",
				`  required flag(s) "schema" not set`,
				"",
				"Try --help for usage.",
				"",
				"",
			),
		},
		"non-usage error opening with accepts": {
			err: errors.New("accepts only .yaml files"),
			want: stringtest.JoinLF(
				"Error",
				"  accepts only .yaml files",
				"",
				"",
			),
		},
		"non-usage error opening with invalid argument": {
			err: errors.New("invalid argument: path must be absolute"),
			want: stringtest.JoinLF(
				"Error",
				"  invalid argument: path must be absolute",
				"",
				"",
			),
		},
		"non-usage error opening with requires at least": {
			err: errors.New("requires at least one schema"),
			want: stringtest.JoinLF(
				"Error",
				"  requires at least one schema",
				"",
				"",
			),
		},
		"non-usage error opening with unknown commands": {
			err: errors.New("unknown commands.yaml:1:4: bad"),
			want: stringtest.JoinLF(
				"Error",
				"  unknown commands.yaml:1:4: bad",
				"",
				"",
			),
		},
		"non-usage error opening with unknown command-line": {
			err: errors.New("unknown command-line option in config"),
			want: stringtest.JoinLF(
				"Error",
				"  unknown command-line option in config",
				"",
				"",
			),
		},
		"non-usage error with flag word": {
			err: errors.New("flagged as incorrect"),
			want: stringtest.JoinLF(
				"Error",
				"  flagged as incorrect",
				"",
				"",
			),
		},
		"niceyaml error with source": {
			err: niceyamlErr,
			want: stringtest.JoinLF(
				"Error",
				"  1:1: $.name~: invalid name",
				"  ",
				"  <genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <textError>^^^^</textError>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
				"",
				"",
			),
		},
		"niceyaml error whose source name opens with a usage pattern": {
			err: flagLikeNameErr,
			want: stringtest.JoinLF(
				"Error",
				"  unknown flag: a.yaml:1:1: $.name~: bad name",
				"  ",
				"  <genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <textError>^^^^</textError>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
				"",
				"",
			),
		},
		"wrapped niceyaml error keeps context and annotates once": {
			err: fmt.Errorf("document 0: %w", nestedErr),
			want: stringtest.JoinLF(
				"Error",
				"  document 0: two problems:",
				"  ├── 1:1: $.name~: bad name",
				"  └── 2:8: $.value: bad value",
				"  ",
				"  <genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <textError>^^^^ bad name</textError>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>123</genericError>",
				"  <textError>       ^^^ bad value</textError>",
				"",
				"",
			),
		},
		"niceyaml error with an empty message keeps the wrapper text and the path": {
			err: fmt.Errorf("document 0: %w", emptyMessageErr),
			want: stringtest.JoinLF(
				"Error",
				"  document 0: 1:1: $.name~:",
				"  ",
				"  <genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <textError>^^^^</textError>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
				"",
				"",
			),
		},
		"every joined niceyaml error annotates": {
			err: errors.Join(badName, badValue),
			want: stringtest.JoinLF(
				"Error",
				"  ├── a.yaml:1:1: $.name~: bad name",
				"  └── b.yaml:2:8: $.value: bad value",
				"  ",
				"  a.yaml",
				"  <genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <textError>^^^^ bad name</textError>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
				"  ",
				"  b.yaml",
				"  <nameTag>name</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>123</genericError>",
				"  <textError>       ^^^ bad value</textError>",
				"",
				"",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			styles := testStyles()
			fangs.NewErrorHandler(fangs.WithPrinter(xmlPrinter()))(&buf, styles, tc.err)

			assert.Equal(t, tc.want, buf.String())
		})
	}
}

func TestNewErrorHandler_Width(t *testing.T) {
	t.Parallel()

	// The value is a single token three times the terminal width, so the
	// excerpt wraps it into rows that fill the width the printer wraps at.
	tcs := map[string]struct {
		width int
	}{
		"40 columns": {width: 40},
		"80 columns": {width: 80},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := niceyaml.NewSourceFromTokens(tokens.Tokenize(
				"name: " + strings.Repeat("x", 3*tc.width) + "\n",
			))

			err := yamltest.Bind(t, src, niceyaml.NewError(
				"invalid name",
				niceyaml.AtPath(paths.MustParse("$.name")),
			))

			p := printer.New(printer.WithWrap(tc.width - fangs.Indent))

			var buf bytes.Buffer

			fangs.NewErrorHandler(fangs.WithPrinter(p))(&buf, testStyles(), err)

			widest := 0

			for row := range strings.SplitSeq(buf.String(), "\n") {
				assert.LessOrEqual(t, lipgloss.Width(row), tc.width, "row %q", row)

				widest = max(widest, lipgloss.Width(row))
			}

			assert.Equal(t, tc.width, widest)
		})
	}
}

func TestErrorHandler_ColorProfile(t *testing.T) {
	t.Parallel()

	src := niceyaml.NewSourceFromTokens(tokens.Tokenize(stringtest.Input(`
		name: test
		value: 123
	`)))

	err := yamltest.Bind(t, src, fmt.Errorf("two problems: %w", errors.Join(
		niceyaml.NewError("bad name", niceyaml.AtPath(paths.MustParse("$.name~"))),
		niceyaml.NewError("bad value", niceyaml.AtPath(paths.MustParse("$.value"))),
	)))

	p := printer.New(
		printer.WithStyles(yamltest.NewXMLStyles()),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithContextLines(0),
	)

	// The handler writes the header, each line of the message behind the
	// indent, and a blank line.
	output := func(msg string) string {
		rows := []string{"Error"}

		for row := range strings.SplitSeq(msg, "\n") {
			rows = append(rows, strings.Repeat(" ", fangs.Indent)+row)
		}

		return stringtest.JoinLF(append(rows, "", "")...)
	}

	styled := output(p.PrintError(err))
	plain := output(niceyaml.FormatError(err, 0))

	// The plain form holds no style and draws a caret run under each range.
	assert.NotContains(t, plain, "<")
	assert.Contains(t, plain, "^^^^ bad name")
	assert.Contains(t, styled, "<genericError>name</genericError>")

	tcs := map[string]struct {
		want    string
		profile colorprofile.Profile
	}{
		"true color": {
			profile: colorprofile.TrueColor,
			want:    styled,
		},
		"256 colors": {
			profile: colorprofile.ANSI256,
			want:    styled,
		},
		"16 colors": {
			profile: colorprofile.ANSI,
			want:    styled,
		},
		"terminal without color": {
			profile: colorprofile.ASCII,
			want:    plain,
		},
		"not a terminal": {
			profile: colorprofile.NoTTY,
			want:    plain,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			w := &colorprofile.Writer{Forward: &buf, Profile: tc.profile}
			fangs.NewErrorHandler(fangs.WithPrinter(p))(w, testStyles(), err)

			assert.Equal(t, tc.want, buf.String())
		})
	}
}

func TestErrorHandler_UnwritableWriter(t *testing.T) {
	t.Parallel()

	// An error handler has nowhere to report a write of its own, so it drops
	// the write result rather than taking the process down with it.
	tcs := map[string]struct {
		err error
	}{
		"plain error": {
			err: errors.New("something went wrong"),
		},
		"usage error, which writes the help hint too": {
			err: errors.New("unknown flag: --foo"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.NotPanics(t, func() {
				fangs.ErrorHandler(unwritableWriter{}, testStyles(), tc.err)
			})
		})
	}
}
