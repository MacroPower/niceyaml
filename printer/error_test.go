package printer_test

import (
	"errors"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
)

func TestPrinter_PrintError_ExcerptsOff(t *testing.T) {
	t.Parallel()

	// The printer draws no color, so a test reads each row as text.
	p := printer.New(
		printer.WithStyles(style.New(lipgloss.NewStyle())),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithContextLines(2),
	)

	// Every line but the one that holds the port holds a secret, so any
	// excerpt of the source shows one.
	secrets := niceyaml.NewSourceFromString("password: hunter2\nport: 0\ntoken: hunter2-token\n",
		niceyaml.WithName("secrets.yaml"), niceyaml.WithExcerpts(false))
	open := niceyaml.NewSourceFromString("host: example.com\nport: 0\n", niceyaml.WithName("open.yaml"))

	port := paths.Doc().Child("port")

	badPort := yamltest.Bind(t, secrets, niceyaml.NewError("port must be at least 1", niceyaml.AtPath(port)))
	openPort := yamltest.Bind(t, open, niceyaml.NewError("port must be at least 1", niceyaml.AtPath(port)))

	_, syntaxErr := niceyaml.NewSourceFromString("password: hunter2\nlist: [1, 2\nport: 1\n",
		niceyaml.WithName("secrets.yaml"), niceyaml.WithExcerpts(false)).Documents()
	require.Error(t, syntaxErr)

	tcs := map[string]struct {
		err  error
		want string
	}{
		"a binding prints its message alone": {
			err:  badPort,
			want: "secrets.yaml:2:7: $.port: port must be at least 1",
		},
		"a summary prints its tree alone": {
			err: yamltest.Bind(t, secrets, niceyaml.NewSummary("2 problems",
				niceyaml.NewError("port must be at least 1", niceyaml.AtPath(port)),
				niceyaml.NewError("too short", niceyaml.AtPath(paths.Doc().Child("password"))),
			)),
			want: stringtest.JoinLF(
				"secrets.yaml: 2 problems",
				"├── 1:11: $.password: too short",
				"└── 2:7: $.port: port must be at least 1",
			),
		},
		"a document that did not parse prints no line": {
			err:  syntaxErr,
			want: "secrets.yaml:3:1: ',' or ']' must be specified",
		},
		// The reason names the path and no text of the source.
		"a location that does not resolve keeps its reason": {
			err: yamltest.Bind(t, secrets, niceyaml.NewError("not a list", niceyaml.AtPath(port.Index(3)))),
			want: stringtest.JoinLF(
				"secrets.yaml: $.port[3]: not a list",
				"",
				"no excerpt: resolve $.port[3]: not found",
			),
		},
		"a binding in another source keeps its excerpt": {
			err: errors.Join(badPort, openPort),
			want: stringtest.JoinLF(
				"├── secrets.yaml:2:7: $.port: port must be at least 1",
				"└── open.yaml:2:7: $.port: port must be at least 1",
				"",
				"open.yaml",
				"   1  host: example.com",
				"   2  port: 0",
				"            ^ port must be at least 1",
			),
		},
		"a detail in the source adds no excerpt to its parent": {
			err: yamltest.Bind(t, open, niceyaml.NewError(
				"ports differ", niceyaml.AtPath(port), niceyaml.WithDetails(badPort),
			)),
			want: stringtest.JoinLF(
				"open.yaml:2:7: $.port: ports differ",
				"└── secrets.yaml:2:7: $.port: port must be at least 1",
				"",
				"open.yaml",
				"   1  host: example.com",
				"   2  port: 0",
				"            ^",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := p.PrintError(tc.err)

			assert.Equal(t, tc.want, got)
			assert.NotContains(t, got, "hunter2")
		})
	}

	t.Run("the same error shows the secret with excerpts on", func(t *testing.T) {
		t.Parallel()

		shown := niceyaml.NewSourceFromString("password: hunter2\nport: 0\ntoken: hunter2-token\n",
			niceyaml.WithName("secrets.yaml"))
		err := yamltest.Bind(t, shown, niceyaml.NewError("port must be at least 1", niceyaml.AtPath(port)))

		assert.Equal(t, stringtest.JoinLF(
			"secrets.yaml:2:7: $.port: port must be at least 1",
			"",
			"   1  password: hunter2",
			"   2  port: 0",
			"            ^",
			"   3  token: hunter2-token",
		), p.PrintError(err))
	})
}

func TestPrinter_PrintError_Report(t *testing.T) {
	t.Parallel()

	// The printer draws no color, so a test reads each row as text.
	p := printer.New(
		printer.WithStyles(style.New(lipgloss.NewStyle())),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithContextLines(1),
	)

	first := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("first.yaml"))
	second := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("second.yaml"))

	badB := yamltest.Bind(t, first, niceyaml.NewError("bad b", niceyaml.AtPath(paths.Doc().Child("b"))))
	badC := yamltest.Bind(t, second, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Doc().Child("c"))))
	far := yamltest.Bind(t, first, niceyaml.NewError("far", niceyaml.AtPosition(position.New(99, 0))))

	err := errors.Join(badB, badC, far)

	// PrintError draws the report built with the context lines of the
	// printer: the tree, each excerpt below its name, then each reason.
	rep := niceyaml.NewErrorReport(err, p.ContextLines())

	require.Len(t, rep.Excerpts, 2)
	require.Len(t, rep.Unresolved, 1)
	require.Equal(t, 2, rep.Excerpts[0].View.Count())

	want := stringtest.JoinLF(
		"├── first.yaml:2:4: $.b: bad b",
		"├── second.yaml:1:4: $.c: bad c",
		"└── first.yaml: far",
		"",
		"first.yaml",
		p.Print(rep.Excerpts[0].View),
		"",
		"second.yaml",
		p.Print(rep.Excerpts[1].View),
		"",
		"no excerpt: "+rep.Unresolved[0].Unresolved().Error(),
	)

	assert.Equal(t, want, p.PrintError(err))
}
