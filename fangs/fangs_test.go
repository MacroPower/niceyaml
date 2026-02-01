package fangs_test

import (
	"bytes"
	"errors"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
	"github.com/stretchr/testify/assert"

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
	source := yamltest.Input(`
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
			want: yamltest.JoinLF(
				"Error",
				"  something went wrong",
				"",
				"",
			),
		},
		"multi-line error": {
			err: errors.New("line1\nline2\nline3"),
			want: yamltest.JoinLF(
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
			want: yamltest.JoinLF(
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
			want: yamltest.JoinLF(
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
			want: yamltest.JoinLF(
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
			want: yamltest.JoinLF(
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
			want: yamltest.JoinLF(
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
			want: yamltest.JoinLF(
				"Error",
				"  flagged as incorrect",
				"",
				"",
			),
		},
		"niceyaml error with source": {
			err: niceyamlErr,
			want: yamltest.JoinLF(
				"Error",
				"  [1:1] invalid name:",
				"  ",
				"  <generic-error>name</generic-error><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>test</literal-string>",
				"  <name-tag>value</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-number-integer>123</literal-number-integer>",
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
