package fangs_test

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// silentError wraps another error without adding a message of its own, which
// is what makes the [niceyaml.SourceError] holding it render an empty message
// while still resolving a detail.
type silentError struct{ err error }

func (s silentError) Error() string { return "" }
func (s silentError) Unwrap() error { return s.err }

// unwritableWriter is the writer an error handler gets when the stream it
// reports on is closed.
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
		niceyaml.WithPath(paths.Root().Child("name").Key()),
	))

	// Two named sources with the same content, so a joined error names the
	// file each branch came from and renders one excerpt per file.
	fileA := niceyaml.NewSourceFromTokens(tokens.Tokenize(source), niceyaml.WithName("a.yaml"))
	fileB := niceyaml.NewSourceFromTokens(tokens.Tokenize(source), niceyaml.WithName("b.yaml"))

	badName := yamltest.Bind(t, fileA, niceyaml.NewError(
		"bad name",
		niceyaml.WithPath(paths.Root().Child("name").Key()),
	))

	badValue := yamltest.Bind(t, fileB, niceyaml.NewError(
		"bad value",
		niceyaml.WithPath(paths.Root().Child("value")),
	))

	emptyMessageErr := yamltest.Bind(t, src, niceyaml.NewErrorFrom(
		silentError{niceyaml.NewError("bad name", niceyaml.WithPath(paths.Root().Child("name").Key()))},
	))

	nestedErr := yamltest.Bind(t, src, niceyaml.NewError(
		"two problems",
		niceyaml.WithErrors(
			niceyaml.NewError("bad name", niceyaml.WithPath(paths.Root().Child("name").Key())),
			niceyaml.NewError("bad value", niceyaml.WithPath(paths.Root().Child("value"))),
		),
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
				"  1:1: $.name: invalid name",
				"  ",
				"  <genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
				"",
				"",
			),
		},
		"wrapped niceyaml error keeps context and annotates once": {
			err: fmt.Errorf("document 0: %w", nestedErr),
			want: stringtest.JoinLF(
				"Error",
				"  document 0: two problems",
				"  ├── 1:1: $.name: bad name",
				"  └── 2:8: $.value: bad value",
				"  ",
				"  <genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <textError>^ bad name</textError>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>123</genericError>",
				"  <textError>       ^ bad value</textError>",
				"",
				"",
			),
		},
		"niceyaml error with an empty message keeps the wrapper text": {
			err: fmt.Errorf("document 0: %w", emptyMessageErr),
			want: stringtest.JoinLF(
				"Error",
				"  document 0: 1:1:",
				"  ",
				"  <genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
				"",
				"",
			),
		},
		"every joined niceyaml error annotates": {
			err: errors.Join(badName, badValue),
			want: stringtest.JoinLF(
				"Error",
				"  ├── a.yaml:1:1: $.name: bad name",
				"  └── b.yaml:2:8: $.value: bad value",
				"  ",
				"  <genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
				"  ",
				"  <nameTag>name</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>123</genericError>",
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
