package fangs_test

import (
	"bytes"
	"errors"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/lexers"
	"go.jacobcolvin.com/niceyaml/paths"
)

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

	// Set up source and tokens for niceyaml.Error test case.
	source := stringtest.Input(`
		name: test
		value: 123
	`)
	tokens := lexers.Tokenize(source)
	niceyamlErr := niceyaml.NewError(
		"invalid name",
		niceyaml.WithPath(paths.Root().Child("name").Key()),
		niceyaml.WithSource(niceyaml.NewSourceFromTokens(tokens)),
		niceyaml.WithPrinter(niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter()),
			niceyaml.WithStyle(lipgloss.NewStyle()),
		)),
	)

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
				"  [1:1] invalid name:",
				"  ",
				"  <genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"  <nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
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
