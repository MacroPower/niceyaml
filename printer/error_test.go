package printer_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// excerptOf returns the part of a rendered error after its tree, which
// holds its excerpts.
func excerptOf(t *testing.T, rendered string) string {
	t.Helper()

	_, excerpt, ok := strings.Cut(rendered, "\n\n")
	require.True(t, ok)

	return excerpt
}

// formattedExcerpt returns the excerpt [niceyaml.FormatError] draws for err,
// without the gutter in front of each row.
func formattedExcerpt(t *testing.T, err error, opts ...niceyaml.ExcerptOption) string {
	t.Helper()

	rows := strings.Split(excerptOf(t, niceyaml.FormatError(err, opts...)), "\n")
	for i, row := range rows {
		_, rows[i], _ = strings.Cut(row, " | ")
	}

	return stringtest.JoinLF(rows...)
}

// errAt returns an error that reports msg at the value under keys, a
// path from the node the error binds to.
func errAt(msg string, keys ...string) error {
	return niceyaml.NewError(msg, niceyaml.AtPath(paths.Current().Child(keys...)))
}

// itemNames returns n names, from "item00" on.
func itemNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("item%02d", i)
	}

	return names
}

// itemsInput returns a YAML line that holds [itemNames] of n as a flow
// sequence under the key "items", long enough to wrap or clip.
func itemsInput(n int) string {
	return "items: [" + strings.Join(itemNames(n), ", ") + "]\n"
}

func TestPrinter_PrintError_ExcerptsOff(t *testing.T) {
	t.Parallel()

	// The printer draws no color, so a test reads each row as text.
	p := testPrinterWithGutter(printer.DefaultGutter)

	// Every line but the one that holds the port holds a secret, so any
	// excerpt of the source shows one.
	input := stringtest.LinesLF("password: hunter2", "port: 0", "token: hunter2-token")
	secrets := niceyaml.NewSourceFromString(input, niceyaml.WithName("secrets.yaml"), niceyaml.WithExcerpts(false))
	open := niceyaml.NewSourceFromString("host: example.com\nport: 0\n", niceyaml.WithName("open.yaml"))

	port := paths.Doc().Child("port")
	lowPort := niceyaml.NewError("port must be at least 1", niceyaml.AtPath(port))

	badPort := yamltest.Bind(t, secrets, lowPort)
	openPort := yamltest.Bind(t, open, lowPort)

	_, syntaxErr := niceyaml.NewSourceFromString(
		stringtest.LinesLF("password: hunter2", "list: [1, 2", "port: 1"),
		niceyaml.WithName("secrets.yaml"), niceyaml.WithExcerpts(false),
	).File()
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
				lowPort,
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

		shown := niceyaml.NewSourceFromString(input, niceyaml.WithName("secrets.yaml"))
		err := yamltest.Bind(t, shown, lowPort)

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
	p := testPrinterWithGutter(printer.DefaultGutter)

	first := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("first.yaml"))
	second := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("second.yaml"))

	badB := yamltest.Bind(t, first, niceyaml.NewError("bad b", niceyaml.AtPath(paths.Doc().Child("b"))))
	badC := yamltest.Bind(t, second, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Doc().Child("c"))))
	far := yamltest.Bind(t, first, niceyaml.NewError("far", niceyaml.AtPosition(position.New(99, 0))))

	err := errors.Join(badB, badC, far)

	// PrintError draws the report built with the same options: the tree,
	// each excerpt below its name, then each reason.
	rep := niceyaml.NewErrorReport(err, niceyaml.WithContextLines(1))

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

	assert.Equal(t, want, p.PrintError(err, niceyaml.WithContextLines(1)))
	assert.Equal(t, want, p.PrintReport(rep))
}

func TestPrinter_PrintError_Excerpt(t *testing.T) {
	t.Parallel()

	// The printer draws no color and no gutter, so a test reads each row
	// as the text of the source.
	p := testPrinter()

	// The marked line sits in the middle of a source with more lines on
	// each side than the default shows, so the count changes the excerpt.
	nine := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"b: 2",
		"c: 3",
		"d: 4",
		"e: 5",
		"f: 6",
		"g: 7",
		"h: 8",
		"i: 9",
	))
	bound := yamltest.Bind(t, nine, errAt("bad", "e"))

	// Among several bindings, each message sits beside the carets of its
	// line, so without color the carets still show the extent of each
	// range, as FormatError draws them.
	ports := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"ports: {http: eighty, https: ninety}",
		"name: Web-App",
		"port: 8",
	))

	tcs := map[string]struct {
		err  error
		opts []niceyaml.ExcerptOption
		want []string
	}{
		"default context": {
			err: bound,
			want: []string{
				"5:4: $.e: bad",
				"",
				"c: 3",
				"d: 4",
				"e: 5",
				"   ^",
				"f: 6",
				"g: 7",
			},
		},
		"no context": {
			err:  bound,
			opts: []niceyaml.ExcerptOption{niceyaml.WithContextLines(0)},
			want: []string{
				"5:4: $.e: bad",
				"",
				"e: 5",
				"   ^",
			},
		},
		"more context": {
			err:  bound,
			opts: []niceyaml.ExcerptOption{niceyaml.WithContextLines(3)},
			want: []string{
				"5:4: $.e: bad",
				"",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: 5",
				"   ^",
				"f: 6",
				"g: 7",
				"h: 8",
			},
		},
		"last option wins": {
			err:  bound,
			opts: []niceyaml.ExcerptOption{niceyaml.WithContextLines(3), niceyaml.WithContextLines(0)},
			want: []string{
				"5:4: $.e: bad",
				"",
				"e: 5",
				"   ^",
			},
		},
		"bindings on one line": {
			err: yamltest.Bind(t, ports, errors.Join(
				errAt("not a number", "ports", "http"),
				errAt("also not a number", "ports", "https"),
			)),
			want: []string{
				"├── 1:15: $.ports.http: not a number",
				"└── 1:30: $.ports.https: also not a number",
				"",
				"ports: {http: eighty, https: ninety}",
				"              ^^^^^^         ^^^^^^ not a number; also not a number",
				"name: Web-App",
				"port: 8",
			},
		},
		"bindings on separate lines": {
			err: yamltest.Bind(t, ports, errors.Join(
				errAt("not a number", "ports", "https"),
				errAt("reserved name", "name"),
				errAt("too small", "port"),
			)),
			want: []string{
				"├── 1:30: $.ports.https: not a number",
				"├── 2:7: $.name: reserved name",
				"└── 3:7: $.port: too small",
				"",
				"ports: {http: eighty, https: ninety}",
				"                             ^^^^^^ not a number",
				"name: Web-App",
				"      ^^^^^^^ reserved name",
				"port: 8",
				"      ^ too small",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := p.PrintError(tc.err, tc.opts...)

			assert.Equal(t, stringtest.JoinLF(tc.want...), got)

			// PrintError and FormatError draw the same report, so the
			// excerpt of FormatError shows the same lines behind a gutter of
			// its own, below a tree with ASCII connectors.
			assert.Equal(t, formattedExcerpt(t, tc.err, tc.opts...), excerptOf(t, got))
		})
	}
}

func TestPrinter_PrintReport(t *testing.T) {
	t.Parallel()

	// The printer draws no color, so a test reads each row as text.
	p := testPrinterWithGutter(printer.DefaultGutter)

	first := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("first.yaml"))
	badB := yamltest.Bind(t, first, niceyaml.NewError("bad b", niceyaml.AtPath(paths.Doc().Child("b"))))
	far := yamltest.Bind(t, first, niceyaml.NewError("far", niceyaml.AtPosition(position.New(99, 0))))

	t.Run("zero report", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, p.PrintReport(niceyaml.ErrorReport{}))
		assert.Equal(t, p.PrintError(nil), p.PrintReport(niceyaml.NewErrorReport(nil)))
	})

	t.Run("report of an error", func(t *testing.T) {
		t.Parallel()

		rep := niceyaml.NewErrorReport(errors.Join(badB, far), niceyaml.WithContextLines(0))

		require.Len(t, rep.Excerpts, 1)
		require.Len(t, rep.Unresolved, 1)

		want := stringtest.JoinLF(
			"├── first.yaml:2:4: $.b: bad b",
			"└── first.yaml: far",
			"",
			"   2  b: 2",
			"         ^ bad b",
			"",
			"no excerpt: "+rep.Unresolved[0].Unresolved().Error(),
		)

		assert.Equal(t, want, p.PrintReport(rep))
		assert.Equal(t, want, p.PrintError(errors.Join(badB, far), niceyaml.WithContextLines(0)))
	})

	t.Run("report the caller trimmed", func(t *testing.T) {
		t.Parallel()

		// PrintReport draws the parts the report holds, so a caller that
		// drops the unresolved bindings prints no reason for them.
		rep := niceyaml.NewErrorReport(errors.Join(badB, far), niceyaml.WithContextLines(0))
		rep.Unresolved = nil

		assert.Equal(t, stringtest.JoinLF(
			"├── first.yaml:2:4: $.b: bad b",
			"└── first.yaml: far",
			"",
			"   2  b: 2",
			"         ^ bad b",
		), p.PrintReport(rep))
	})
}

func TestPrinter_PrintError_Warnings(t *testing.T) {
	t.Parallel()

	item := niceyaml.NewSourceFromString("name: semla\nspec: {price: 0, legacyPricing: true}\n",
		niceyaml.WithName("item.yaml"))

	err := yamltest.Bind(t, item, errors.Join(
		niceyaml.NewError("0 is less than 1", niceyaml.AtPath(paths.Doc().Child("spec", "price"))),
		niceyaml.NewWarning(`deprecated field "legacyPricing"`,
			niceyaml.AtPath(paths.Doc().Child("spec", "legacyPricing").Key())),
	))

	t.Run("each severity gets its own row", func(t *testing.T) {
		t.Parallel()

		p := testPrinterWithGutter(printer.DefaultGutter)

		assert.Equal(t, stringtest.JoinLF(
			"├── item.yaml:2:15: $.spec.price: 0 is less than 1",
			`└── item.yaml:2:18: warning: $.spec.legacyPricing~: deprecated field "legacyPricing"`,
			"",
			"   1  name: semla",
			"   2  spec: {price: 0, legacyPricing: true}",
			"                    ^ 0 is less than 1",
			`                       ^^^^^^^^^^^^^ warning: deprecated field "legacyPricing"`,
		), p.PrintError(err))
	})

	t.Run("the label renders in the warning text style", func(t *testing.T) {
		t.Parallel()

		p := testPrinterWithStyles(style.Set(kind.TextWarn, bracketStyle("<", ">")))

		tree, _, _ := strings.Cut(p.PrintError(err), "\n\n")
		assert.Equal(t, stringtest.JoinLF(
			"├── item.yaml:2:15: $.spec.price: 0 is less than 1",
			`└── item.yaml:2:18: <warning:> $.spec.legacyPricing~: deprecated field "legacyPricing"`,
		), tree)
	})
}
