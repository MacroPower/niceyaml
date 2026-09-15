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
	"go.jacobcolvin.com/niceyaml/cmd/nyaml/fangs"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/lexers"
	"go.jacobcolvin.com/niceyaml/paths"
)

// silentError wraps another error without adding a message of its own, which
// is what makes the [niceyaml.SourceError] holding it render an empty message
// while still resolving a detail.
type silentError struct{ err error }

func (s silentError) Error() string { return "" }
func (s silentError) Unwrap() error { return s.err }

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
	xmlPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		)
	}

	src := niceyaml.NewSourceFromTokens(
		lexers.Tokenize(source),
		niceyaml.WithErrorOptions(niceyaml.WithPrinter(xmlPrinter())),
	)

	niceyamlErr := src.WrapError(niceyaml.NewError(
		"invalid name",
		niceyaml.WithPath(paths.Root().Child("name").Key()),
	))

	badName := src.WrapError(niceyaml.NewError(
		"bad name",
		niceyaml.WithPath(paths.Root().Child("name").Key()),
	))

	badValue := src.WrapError(niceyaml.NewError(
		"bad value",
		niceyaml.WithPath(paths.Root().Child("value").Value()),
	))

	emptyMessageErr := src.WrapError(niceyaml.NewErrorFrom(
		silentError{niceyaml.NewError("bad name", niceyaml.WithPath(paths.Root().Child("name").Key()))},
	))

	nestedErr := src.WrapError(niceyaml.NewError(
		"two problems",
		niceyaml.WithErrors(
			niceyaml.NewError("bad name", niceyaml.WithPath(paths.Root().Child("name").Key())),
			niceyaml.NewError("bad value", niceyaml.WithPath(paths.Root().Child("value").Value())),
		),
	))

	tcs := map[string]struct {
		err  error
		want string
	}{
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
				"  [1:1] invalid name",
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
				"  ",
				"  <genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <comment>^ bad name</comment>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>123</genericError>",
				"  <comment>       ^ bad value</comment>",
				"",
				"",
			),
		},
		"niceyaml error with an empty message keeps the wrapper text": {
			err: fmt.Errorf("document 0: %w", emptyMessageErr),
			want: stringtest.JoinLF(
				"Error",
				"  document 0: ",
				"  ",
				"  <genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
				"",
				"",
			),
		},
		"every joined niceyaml error annotates": {
			err: errors.Join(
				fmt.Errorf("a.yaml: %w", badName),
				fmt.Errorf("b.yaml: %w", badValue),
			),
			want: stringtest.JoinLF(
				"Error",
				"  a.yaml: [1:1] bad name",
				"  ",
				"  <genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
				"  b.yaml: [2:8] bad value",
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
			fangs.ErrorHandler(&buf, styles, tc.err)

			assert.Equal(t, tc.want, buf.String())
		})
	}
}
