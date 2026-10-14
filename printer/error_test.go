package printer_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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

// itemErrorAt creates a [*niceyaml.Error] with the message msg at item
// index of [itemsInput].
func itemErrorAt(msg string, index int) *niceyaml.Error {
	return niceyaml.NewError(msg, niceyaml.AtPath(paths.Current().Child("items").Index(index)))
}

func TestPrinter_PrintError(t *testing.T) {
	t.Parallel()

	p := yamltest.NewXMLPrinter()

	source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2"))
	bound := yamltest.Bind(t, source, yamltest.ErrorAt("bad", "b"))

	// The rows of source as the printer draws them, plain or with an
	// error mark on the value.
	rowA := "<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>1</literalNumberInteger>"
	rowB := "<nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>2</literalNumberInteger>"
	markedA := "<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>1</genericError>"
	markedB := "<nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>2</genericError>"

	other := niceyaml.NewSourceFromString("c: 3\n")
	nested := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a:",
		"  b: 2",
	))
	empty := niceyaml.NewSourceFromString(stringtest.LinesLF("a:", "b: 2"))
	flags := niceyaml.NewSourceFromString("flags: 🇺🇸🇫🇷\n")

	// Two sources whose one document has no content.
	blank := niceyaml.NewSourceFromString("")
	comments := niceyaml.NewSourceFromString("# defaults apply\n", niceyaml.WithName("app.yaml"))

	_, tabErr := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a:",
		"\tb: 1",
	)).File()

	// The root has no message beside its range, so a caret run under the
	// range shows its extent without color.
	excerpt := stringtest.JoinLF(rowA, markedB, "<textError>   ^</textError>")

	// Among several bindings, the message of each sits beside its caret.
	labeled := stringtest.JoinLF(rowA, markedB, "<textError>   ^ bad</textError>")

	otherLabeled := stringtest.JoinLF(
		"<nameTag>c</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>3</genericError>",
		"<textError>   ^ bad</textError>",
	)

	// Two sources with names, one of which holds a tab and a control
	// character.
	named := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("c.yaml"))
	tabbed := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("t\t\x1b.yaml"))

	// The style of the indent adds text in front of the second line, and
	// the caret still lands under the value.
	nestedExcerpt := stringtest.JoinLF(
		"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
		"<text>  </text><nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue>"+
			"<text> </text><genericError>2</genericError>",
		"<textError>     ^</textError>",
	)

	// Both values marked, each with its message below it.
	annotated := stringtest.JoinLF(
		markedA,
		"<textError>   ^ bad a</textError>",
		markedB,
		"<textError>   ^ bad b</textError>",
	)

	tcs := map[string]struct {
		err  error
		want string
	}{
		"nil": {
			err:  nil,
			want: "",
		},
		"error without a source": {
			err:  errors.New("boom"),
			want: "boom",
		},
		"bound error": {
			err:  bound,
			want: stringtest.JoinLF("2:4: $.b: bad", "", excerpt),
		},
		"wrapped bound error keeps the wrapper's context": {
			err:  fmt.Errorf("document 0: %w", bound),
			want: stringtest.JoinLF("document 0: 2:4: $.b: bad", "", excerpt),
		},
		"bound error on an indented line": {
			err:  yamltest.Bind(t, nested, yamltest.ErrorAt("bad", "a", "b")),
			want: stringtest.JoinLF("2:6: $.a.b: bad", "", nestedExcerpt),
		},
		"bound error without a location": {
			err:  yamltest.Bind(t, source, niceyaml.NewError("bad")),
			want: "bad",
		},
		// A location with no token under it covers no column, so a lone
		// caret marks its spot.
		"bound error at an empty value": {
			err: yamltest.Bind(t, empty, yamltest.ErrorAt("required", "a")),
			want: stringtest.JoinLF(
				"1:3: $.a: required",
				"",
				"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<textError>  ^</textError>",
				rowB,
			),
		},
		"bound error past the end of a line": {
			err: yamltest.Bind(t, source, niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 500)))),
			want: stringtest.JoinLF(
				"1:501: bad",
				"",
				rowA,
				"<textError>    ^</textError>",
				rowB,
			),
		},
		// The caret run starts under the second flag, not under the
		// second rune of the first flag.
		"bound error after a multi-rune cluster": {
			err: yamltest.Bind(t, flags, niceyaml.NewError("", niceyaml.AtRange(
				yamltest.LineRange(0, 9, 11),
			))),
			want: stringtest.JoinLF(
				"1:10:",
				"",
				"<nameTag>flags</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text>"+
					"<literalString>🇺🇸</literalString><genericError>🇫🇷</genericError>",
				"<textError>         ^^</textError>",
			),
		},
		"bound error with an empty message": {
			err:  yamltest.Bind(t, source, yamltest.ErrorAt("", "b")),
			want: stringtest.JoinLF("2:4: $.b:", "", excerpt),
		},
		"joined bound errors print one excerpt per source": {
			err: errors.Join(
				fmt.Errorf("first: %w", bound),
				fmt.Errorf("second: %w", yamltest.Bind(t, other, yamltest.ErrorAt("bad", "c"))),
			),
			want: stringtest.JoinLF(
				"├── first: 2:4: $.b: bad",
				"└── second: 1:4: $.c: bad",
				"",
				labeled,
				"",
				otherLabeled,
			),
		},
		"joined bound errors of one source share an excerpt": {
			err: errors.Join(
				yamltest.Bind(t, source, yamltest.ErrorAt("bad a", "a")),
				yamltest.Bind(t, source, yamltest.ErrorAt("bad b", "b")),
			),
			want: stringtest.JoinLF(
				"├── 1:4: $.a: bad a",
				"└── 2:4: $.b: bad b",
				"",
				annotated,
			),
		},
		"joined bound errors of named sources print each name": {
			err: errors.Join(
				yamltest.Bind(t, named, yamltest.ErrorAt("bad", "c")),
				yamltest.Bind(t, tabbed, yamltest.ErrorAt("bad", "c")),
			),
			want: stringtest.JoinLF(
				"├── c.yaml:1:4: $.c: bad",
				"└── t    ␛.yaml:1:4: $.c: bad",
				"",
				"c.yaml",
				otherLabeled,
				"",
				"t    ␛.yaml",
				otherLabeled,
			),
		},
		"joined bound error that marks nothing names its reason last": {
			err: errors.Join(
				yamltest.Bind(
					t,
					source,
					niceyaml.NewError("gone", niceyaml.AtPath(paths.Current().Child("x").Index(0))),
				),
				bound,
			),
			want: stringtest.JoinLF(
				"├── $.x[0]: gone",
				"└── 2:4: $.b: bad",
				"",
				labeled,
				"",
				"no excerpt: resolve $.x[0]: not found",
			),
		},
		// No path resolves in a document with no content, so the tree
		// names each path and no reason follows it.
		"bound error in a source with no content": {
			err:  yamltest.Bind(t, blank, yamltest.ErrorAt("bad", "b")),
			want: "$.b: bad",
		},
		"bound errors in a source with no content": {
			err: yamltest.Bind(t, blank, niceyaml.NewSummary("2 problems",
				yamltest.ErrorAt("bad a", "a"),
				yamltest.ErrorAt("bad b", "b"),
			)),
			want: stringtest.JoinLF("2 problems", "├── $.a: bad a", "└── $.b: bad b"),
		},
		"bound error at the root of a file of comments": {
			err:  yamltest.Bind(t, comments, yamltest.ErrorAt("bad")),
			want: "app.yaml: $: bad",
		},
		// A position names a line, which a source with no content does not
		// hold, so its reason stays.
		"bound error at a position in a source with no content": {
			err:  yamltest.Bind(t, blank, niceyaml.NewError("bad", niceyaml.AtPosition(position.New(3, 0)))),
			want: stringtest.JoinLF("bad", "", "no excerpt: location outside source: line 4 of an empty source"),
		},
		// A row drops its trailing spaces: the space after "b", and the
		// blank indent in front of the blank line of the last branch.
		"joined error with a blank line in a branch": {
			err:  errors.Join(errors.New("a"), errors.New("b \n\nc")),
			want: stringtest.JoinLF("├── a", "└── b", "", "    c"),
		},
		// Each tab becomes four spaces, wherever it falls in its row.
		"tabs in messages expand to spaces": {
			err: errors.Join(
				errors.New("a\tb"),
				errors.New("Did you mean this?\n\tvalidate"),
			),
			want: stringtest.JoinLF(
				"├── a    b",
				"└── Did you mean this?",
				"        validate",
			),
		},
		"tab in a message with no nested errors": {
			err:  errors.New("Did you mean this?\n\tvalidate"),
			want: stringtest.JoinLF("Did you mean this?", "    validate"),
		},
		// The excerpt spells the message beside the caret as the tree
		// spells it.
		"tab in a message beside a caret": {
			err: yamltest.Bind(t, source, niceyaml.NewError("invalid",
				niceyaml.WithDetails(yamltest.ErrorAt("bad\ta", "a")),
			)),
			want: stringtest.JoinLF(
				"invalid",
				"└── 1:4: $.a: bad    a",
				"",
				markedA,
				"<textError>   ^ bad    a</textError>",
				rowB,
			),
		},
		// The go-yaml scanner spells the tab it rejects as a raw tab. The
		// message names it by its picture, as the excerpt shows it, rather
		// than as the four spaces a tab in a message lays out as.
		"syntax error names a rejected tab by its picture": {
			err: tabErr,
			want: stringtest.JoinLF(
				"2:2: found character '\u2409' that cannot start any token",
				"",
				"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>\u2409</text><genericError>b</genericError><punctuationMappingValue>:</punctuationMappingValue>"+
					"<text> </text><literalNumberInteger>1</literalNumberInteger>",
				"<textError> ^</textError>",
			),
		},
		"nested errors draw as branches in position order": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("2 problems",
				yamltest.ErrorAt("bad b", "b"),
				yamltest.ErrorAt("bad a", "a"),
			)),
			want: stringtest.JoinLF(
				"2 problems",
				"├── 1:4: $.a: bad a",
				"└── 2:4: $.b: bad b",
				"",
				annotated,
			),
		},
		"joined nested errors draw as a forest of subtrees": {
			err: errors.Join(
				yamltest.Bind(t, source, niceyaml.NewSummary("2 problems",
					yamltest.ErrorAt("bad a", "a"),
					yamltest.ErrorAt("bad b", "b"),
				)),
				yamltest.Bind(t, other, yamltest.ErrorAt("bad", "c")),
			),
			want: stringtest.JoinLF(
				"├── 2 problems",
				"│   ├── 1:4: $.a: bad a",
				"│   └── 2:4: $.b: bad b",
				"└── 1:4: $.c: bad",
				"",
				annotated,
				"",
				otherLabeled,
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, p.PrintError(tc.err))
		})
	}
}

func TestPrinter_PrintError_ControlCharacters(t *testing.T) {
	t.Parallel()

	p := yamltest.NewXMLPrinter()

	err := fmt.Errorf("outer: %w", errors.Join(
		errors.New("bad \x1b[31mred\x07 thing"),
		errors.New("second"),
	))

	got := p.PrintError(err)
	assert.NotContains(t, got, "\x1b")
	assert.NotContains(t, got, "\x07")
	assert.Contains(t, got, "bad \u241b[31mred\u2407 thing")

	// An error with no nested errors is the root of a tree of one node, so
	// its message gets the same treatment as a branch.
	assert.Equal(t, "bad \u241b[31mred\u2407 thing", p.PrintError(errors.New("bad \x1b[31mred\x07 thing")))

	// The reason a location did not resolve names the path, which spells a
	// key of the document and writes a control character in it as an
	// escape, in the reason as in the tree.
	source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml"))
	bound := source.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("mi\x1b[31mss").Index(0))))

	got = p.PrintError(bound)
	assert.NotContains(t, got, "\x1b")
	assert.Contains(t, got, "no excerpt: ")
	assert.Contains(t, got, `mi\u001b[31mss`)

	// A tab in the key is an escape in the reason as it is in the tree, so
	// neither lays out a tab.
	named := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("cfg.yaml"))
	bound = named.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("ab\tc").Index(0))))

	got = p.PrintError(bound)
	assert.NotContains(t, got, "\u2409")
	assert.Equal(t, stringtest.JoinLF(
		`cfg.yaml: $.'ab\tc'[0]: bad`,
		"",
		`no excerpt: resolve $.'ab\tc'[0]: not found`,
	), got)

	// A line feed in the key is an escape too, so the key starts no row of
	// its own.
	bound = named.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("x\ny\tz").Index(0))))

	assert.Equal(t, stringtest.JoinLF(
		`cfg.yaml: $.'x\ny\tz'[0]: bad`,
		"",
		`no excerpt: resolve $.'x\ny\tz'[0]: not found`,
	), p.PrintError(bound))
}

func TestPrinter_PrintError_Wrap(t *testing.T) {
	t.Parallel()

	long := "a very long error message that certainly exceeds the width of the terminal"

	tcs := map[string]struct {
		err   error
		want  string
		width int
	}{
		// A wrapped row of a nested message stays under its connector, so
		// the second row of the first branch starts with the indent of the
		// tree, and no row carries padding after its text.
		"nested messages wrap under their connectors": {
			err: fmt.Errorf("%s: %w", long, errors.Join(
				errors.New(long),
				fmt.Errorf("%s: %w", long, errors.New(long)),
			)),
			width: 24,
			want: stringtest.JoinLF(
				"a very long error",
				"message that certainly",
				"exceeds the width of the",
				"terminal:",
				"├── a very long error",
				"│   message that",
				"│   certainly exceeds",
				"│   the width of the",
				"│   terminal",
				"└── a very long error",
				"    message that",
				"    certainly exceeds",
				"    the width of the",
				"    terminal: a very",
				"    long error message",
				"    that certainly",
				"    exceeds the width of",
				"    the terminal",
			),
		},
		"error with no nested errors wraps to the full width": {
			err:   errors.New(long),
			width: 24,
			want: stringtest.JoinLF(
				"a very long error",
				"message that certainly",
				"exceeds the width of the",
				"terminal",
			),
		},
		"message drops the space before a breakpoint": {
			err:   errors.New("key: value -x"),
			width: 10,
			want:  stringtest.JoinLF("key: value", "-x"),
		},
		// The blank row between the message lines stays, since the message
		// asks for it.
		"message keeps its blank row at one column": {
			err:   errors.New("日本語\n\nnext"),
			width: 1,
			want:  stringtest.JoinLF("日", "本", "語", "", "n", "e", "x", "t"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The tags of XMLStyles show any style a row takes.
			p := yamltest.NewXMLPrinter(printer.WithWrapWidth(tc.width))

			assert.Equal(t, tc.want, p.PrintError(tc.err))
		})
	}
}

func TestPrinter_PrintError_MarksWithoutStyles(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.LinesLF("ports: {http: eighty, https: ninety}", "name: x"))
	items := niceyaml.NewSourceFromString(itemsInput(12))

	// A childless bound error has no message beside its range, so without
	// color the carets on the row below are all that shows its extent.
	sla := niceyaml.NewSourceFromString(stringtest.LinesLF("spec:", "  sla: 99", "  hours:"))
	badSLA := yamltest.Bind(t, sla, yamltest.ErrorAt("bad", "spec", "sla"))

	// A line of twenty items is longer than the default excerpt width, so
	// the printer shows whole lines to keep every wrapped row of it.
	twenty := niceyaml.NewSourceFromString(itemsInput(20))
	twentyRows := []string{
		"   1  items: [item00, item01, item02,",
		"   -  item03, item04, item05, item06,",
		"   -  item07, item08, item09, item10,",
		"   -  item11, item12, item13, item14,",
		"   -  item15, item16, item17, item18,",
		"   -  item19]",
	}
	badItem := func(index int) error {
		return yamltest.Bind(t, twenty, niceyaml.NewError("outer",
			niceyaml.WithDetails(itemErrorAt("expected string", index)),
		))
	}

	// A line of forty items is long enough for an excerpt to clip it.
	forty := itemNames(40)
	badForty := yamltest.Bind(t, niceyaml.NewSourceFromString(itemsInput(40)), itemErrorAt("expected number", 20))

	tcs := map[string]struct {
		err    error
		gutter printer.Gutter // Nil for no gutter.
		opts   []niceyaml.ExcerptOption
		want   string
		width  int
	}{
		"range": {
			err: badSLA,
			want: stringtest.JoinLF(
				"2:8: $.spec.sla: bad",
				"",
				"spec:",
				"  sla: 99",
				"       ^^",
				"  hours:",
			),
		},
		"range behind line numbers": {
			err:    badSLA,
			gutter: printer.DefaultGutter,
			want: stringtest.JoinLF(
				"2:8: $.spec.sla: bad",
				"",
				"   1  spec:",
				"   2    sla: 99",
				"             ^^",
				"   3    hours:",
			),
		},
		// The message sits beside the carets of the wrapped row that holds
		// its column, here the second.
		"message beside the carets of a later row": {
			err: yamltest.Bind(t, source, errors.Join(
				yamltest.ErrorAt("not a number", "ports", "https"),
				yamltest.ErrorAt("bad name", "name"),
			)),
			width: 30,
			want: stringtest.JoinLF(
				"├── 1:30: $.ports.https: not a",
				"│   number",
				"└── 2:7: $.name: bad name",
				"",
				"ports: {http: eighty, https:",
				"ninety}",
				"^^^^^^ not a number",
				"name: x",
				"      ^ bad name",
			),
		},
		// The messages of the first row join beside the carets of both
		// values, which leave them too little room, so they move to the
		// rows below the carets.
		"bindings on one row": {
			err: yamltest.Bind(t, source, errors.Join(
				yamltest.ErrorAt("not a number", "ports", "http"),
				yamltest.ErrorAt("also not a number", "ports", "https"),
				yamltest.ErrorAt("bad name", "name"),
			)),
			width: 40,
			want: stringtest.JoinLF(
				"├── 1:15: $.ports.http: not a number",
				"├── 1:30: $.ports.https: also not a",
				"│   number",
				"└── 2:7: $.name: bad name",
				"",
				"ports: {http: eighty, https: ninety}",
				"              ^^^^^^         ^^^^^^",
				"                    not a number; also",
				"                    not a number",
				"name: x",
				"      ^ bad name",
			),
		},
		// A range across a break gets carets below each row it covers, and
		// the message sits with the carets of the first.
		"range across a break": {
			err: yamltest.Bind(t, source, errors.Join(
				niceyaml.NewError("bad", niceyaml.AtRange(yamltest.LineRange(0, 14, 35))),
				yamltest.ErrorAt("bad name", "name"),
			)),
			width: 30,
			want: stringtest.JoinLF(
				"├── 1:15: bad",
				"└── 2:7: $.name: bad name",
				"",
				"ports: {http: eighty, https:",
				"              ^^^^^^^^^^^^^^",
				"          bad",
				"ninety}",
				"^^^^^^",
				"name: x",
				"      ^ bad name",
			),
		},
		// The range runs from item02 on the first row to item05 on the
		// second, so each of those rows gets carets below it under its own
		// part of the range.
		"range across a break behind line numbers": {
			err: yamltest.Bind(t, items, niceyaml.NewError("bad", niceyaml.AtRange(
				yamltest.LineRange(0, 24, 54),
			))),
			gutter: printer.DefaultGutter,
			width:  40,
			want: stringtest.JoinLF(
				"1:25: bad",
				"",
				"   1  items: [item00, item01, item02,",
				strings.Repeat(" ", 30)+"^^^^^^^",
				"   -  item03, item04, item05, item06,",
				strings.Repeat(" ", 6)+strings.Repeat("^", 22),
				"   -  item07, item08, item09, item10,",
				"   -  item11]",
			),
		},
		// The message sits below the wrapped row that holds the item, whether
		// or not that row is the last, so the carets land under the item. On
		// the earlier row, the message wraps, and its second row starts under
		// its first word.
		"message below an earlier wrapped row": {
			err:    badItem(5),
			gutter: printer.DefaultGutter,
			opts:   []niceyaml.ExcerptOption{niceyaml.WithExcerptWidth(0)},
			width:  40,
			want: stringtest.JoinLF(slices.Concat(
				[]string{"outer", "└── 1:49: $.items[5]: expected string", ""},
				twentyRows[:2],
				[]string{
					strings.Repeat(" ", 22) + "^^^^^^ expected",
					strings.Repeat(" ", 29) + "string",
				},
				twentyRows[2:],
			)...),
		},
		"message below the wrapped row before the last": {
			err:    badItem(16),
			gutter: printer.DefaultGutter,
			opts:   []niceyaml.ExcerptOption{niceyaml.WithExcerptWidth(0)},
			width:  40,
			want: stringtest.JoinLF(slices.Concat(
				[]string{"outer", "└── 1:137: $.items[16]: expected string", ""},
				twentyRows[:5],
				[]string{strings.Repeat(" ", 14) + "^^^^^^ expected string"},
				twentyRows[5:],
			)...),
		},
		"long line clipped to the default width": {
			err:    badForty,
			gutter: printer.DefaultGutter,
			want: stringtest.JoinLF(
				"1:169: $.items[20]: expected number",
				"",
				"   1  ..."+strings.Join(forty[10:30], ", ")+", ...",
				strings.Repeat(" ", 6+3+80)+"^^^^^^",
			),
		},
		"long line clipped to the width from the options": {
			err:    badForty,
			gutter: printer.DefaultGutter,
			opts:   []niceyaml.ExcerptOption{niceyaml.WithExcerptWidth(24)},
			want: stringtest.JoinLF(
				"1:169: $.items[20]: expected number",
				"",
				"   1  ...18, item19, item20, item...",
				strings.Repeat(" ", 6+3+12)+"^^^^^^",
			),
		},
		"long line shown whole": {
			err:    badForty,
			gutter: printer.DefaultGutter,
			opts:   []niceyaml.ExcerptOption{niceyaml.WithExcerptWidth(0)},
			want: stringtest.JoinLF(
				"1:169: $.items[20]: expected number",
				"",
				"   1  items: ["+strings.Join(forty, ", ")+"]",
				strings.Repeat(" ", 6+168)+"^^^^^^",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(tc.gutter).With(printer.WithWrapWidth(tc.width))

			assert.Equal(t, tc.want, p.PrintError(tc.err, tc.opts...))
		})
	}
}

func TestPrinter_PrintError_MarksLongWrappedValue(t *testing.T) {
	t.Parallel()

	// A long base64 value wraps into many rows, and each gets a caret row
	// below it as wide as the part of the value it shows. The printer
	// shows whole lines, so the excerpt keeps the value whole.
	tcs := map[string]struct {
		value string
		width int
	}{
		"ascii": {
			value: strings.Repeat("QUJD", 2000),
			width: 80,
		},
		"wide runes": {
			value: strings.Repeat("日本", 500),
			width: 41,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("data: " + tc.value + "\n")
			err := yamltest.Bind(t, source, yamltest.ErrorAt("bad", "data"))

			p := testPrinter().With(printer.WithWrapWidth(tc.width))

			rows := strings.Split(p.PrintError(err, niceyaml.WithExcerptWidth(0)), "\n")[2:]
			carets := 0

			// A caret row follows every row that shows part of the value,
			// under all of that part. The key is not part of the range, so
			// its row gets no carets when the value wraps off it.
			for i, row := range rows {
				value := strings.TrimPrefix(row, "data:")
				if strings.Trim(value, " ") == "" || strings.Trim(row, " ^") == "" {
					continue
				}

				require.Less(t, i+1, len(rows), "row %d has no caret row", i)

				mark := rows[i+1]
				assert.Equal(t, strings.Repeat("^", lipgloss.Width(strings.TrimLeft(value, " "))),
					strings.TrimLeft(mark, " "), "row %d", i)
				assert.Equal(t, lipgloss.Width(row), lipgloss.Width(mark), "row %d", i)

				carets += strings.Count(mark, "^")
			}

			assert.Equal(t, lipgloss.Width(tc.value), carets)
		})
	}
}

func TestPrinter_PrintError_WrappedAnnotation(t *testing.T) {
	t.Parallel()

	key := strings.Repeat("a", 66)

	tcs := map[string]struct {
		source string
		err    error    // The detail of the bound error.
		want   []string // Rows the output holds, without the space around them.
		width  int
	}{
		// The sequence wraps over several rows, and item 16 sits near the
		// start of its row, so the message fits beside the caret.
		"message beside the carets": {
			source: itemsInput(20),
			err:    itemErrorAt("expected string", 16),
			width:  40,
			want:   []string{"^^^^^^ expected string"},
		},
		// The value starts near the right edge, so the message has too
		// little room beside the caret and moves to the rows below it. The
		// header holds the whole message on one row, so these two rows come
		// from the annotation.
		"message below carets near the edge": {
			source: key + ": value\n",
			err:    yamltest.ErrorAt("string does not match pattern", key),
			width:  80,
			want:   []string{"string does not", "match pattern"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := printer.New(
				printer.WithWrapWidth(tc.width),
				printer.WithContainerStyle(lipgloss.NewStyle()),
			)

			source := niceyaml.NewSourceFromString(tc.source)
			err := yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(tc.err)))

			got := p.PrintError(err)
			assertRowsFit(t, got, tc.width)

			var trimmed []string

			for row := range strings.SplitSeq(ansi.Strip(got), "\n") {
				trimmed = append(trimmed, strings.TrimSpace(row))
			}

			for _, want := range tc.want {
				assert.Contains(t, trimmed, want)
			}
		})
	}
}

func TestPrinter_PrintError_JoinOfNothing(t *testing.T) {
	t.Parallel()

	// A bound join whose branches all carry nothing renders as an empty
	// tree and no excerpt, so the message stands in rather than nothing.
	// It prints as the tree of one node that holds the message, with
	// control characters as their pictures, as a plain error with the
	// same message prints.
	var nilErr *niceyaml.Error

	tcs := map[string]struct {
		name string
		want string
	}{
		"plain name": {
			name: "f.yaml",
			want: "f.yaml:\n",
		},
		"name with an escape sequence": {
			name: "f\x1b[31m.yaml",
			want: "f\u241b[31m.yaml:\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName(tc.name))
			err := source.Bind(errors.Join(nilErr, nilErr))
			require.Error(t, err)

			p := testPrinter()
			got := p.PrintError(err)

			assert.Equal(t, tc.want, got)
			assert.Equal(t, p.PrintError(errors.New(err.Error())), got)
			assert.NotContains(t, got, "\x1b")
		})
	}
}

func TestPrinter_PrintError_ExcerptsOff(t *testing.T) {
	t.Parallel()

	p := testPrinterWithGutter(printer.DefaultGutter)

	// Every line but the one that holds the port holds a secret, so any
	// excerpt of the source shows one.
	input := stringtest.LinesLF("password: hunter2", "port: 0", "token: hunter2-token")
	secrets := niceyaml.NewSourceFromString(input, niceyaml.WithName("secrets.yaml"), niceyaml.WithExcerpts(false))
	open := niceyaml.NewSourceFromString(
		stringtest.LinesLF("host: example.com", "port: 0"),
		niceyaml.WithName("open.yaml"),
	)

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
		err   error
		want  string
		shown bool // The source shows its excerpts, and so the secret.
	}{
		"a binding prints its message alone": {
			err:  badPort,
			want: "secrets.yaml:2:7: $.port: port must be at least 1",
		},
		"a summary prints its tree alone": {
			err: yamltest.Bind(t, secrets, niceyaml.NewSummary("2 problems",
				lowPort,
				yamltest.ErrorAt("too short", "password"),
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
		"a source with excerpts on shows the secret": {
			err:   yamltest.Bind(t, niceyaml.NewSourceFromString(input, niceyaml.WithName("secrets.yaml")), lowPort),
			shown: true,
			want: stringtest.JoinLF(
				"secrets.yaml:2:7: $.port: port must be at least 1",
				"",
				"   1  password: hunter2",
				"   2  port: 0",
				"            ^",
				"   3  token: hunter2-token",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := p.PrintError(tc.err)

			assert.Equal(t, tc.want, got)

			if !tc.shown {
				assert.NotContains(t, got, "hunter2")
			}
		})
	}
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
	bound := yamltest.Bind(t, nine, yamltest.ErrorAt("bad", "e"))

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
				yamltest.ErrorAt("not a number", "ports", "http"),
				yamltest.ErrorAt("also not a number", "ports", "https"),
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
				yamltest.ErrorAt("not a number", "ports", "https"),
				yamltest.ErrorAt("reserved name", "name"),
				yamltest.ErrorAt("too small", "port"),
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

	p := testPrinterWithGutter(printer.DefaultGutter)

	first := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2"), niceyaml.WithName("first.yaml"))
	second := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("second.yaml"))

	badB := yamltest.Bind(t, first, yamltest.ErrorAt("bad b", "b"))
	badC := yamltest.Bind(t, second, yamltest.ErrorAt("bad c", "c"))
	far := yamltest.Bind(t, first, niceyaml.NewError("far", niceyaml.AtPosition(position.New(99, 0))))

	noContext := []niceyaml.ExcerptOption{niceyaml.WithContextLines(0)}

	// The zero report prints nothing.
	assert.Empty(t, p.PrintReport(niceyaml.ErrorReport{}))

	tcs := map[string]struct {
		err  error
		opts []niceyaml.ExcerptOption
		want string
		trim bool // Drops the unresolved bindings from the report.
	}{
		"report of no error": {
			err:  nil,
			want: "",
		},
		"report of several sources": {
			err:  errors.Join(badB, badC, far),
			opts: []niceyaml.ExcerptOption{niceyaml.WithContextLines(1)},
			want: stringtest.JoinLF(
				"├── first.yaml:2:4: $.b: bad b",
				"├── second.yaml:1:4: $.c: bad c",
				"└── first.yaml: far",
				"",
				"first.yaml",
				"   1  a: 1",
				"   2  b: 2",
				"         ^ bad b",
				"",
				"second.yaml",
				"   1  c: 3",
				"         ^ bad c",
				"",
				"no excerpt: location outside source: line 100 not in lines 1-2",
			),
		},
		"report of an error": {
			err:  errors.Join(badB, far),
			opts: noContext,
			want: stringtest.JoinLF(
				"├── first.yaml:2:4: $.b: bad b",
				"└── first.yaml: far",
				"",
				"   2  b: 2",
				"         ^ bad b",
				"",
				"no excerpt: location outside source: line 100 not in lines 1-2",
			),
		},
		// PrintReport draws the parts the report holds, so a caller that
		// drops the unresolved bindings prints no reason for them.
		"report the caller trimmed": {
			err:  errors.Join(badB, far),
			opts: noContext,
			trim: true,
			want: stringtest.JoinLF(
				"├── first.yaml:2:4: $.b: bad b",
				"└── first.yaml: far",
				"",
				"   2  b: 2",
				"         ^ bad b",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rep := niceyaml.NewErrorReport(tc.err, tc.opts...)
			if tc.trim {
				rep.Unresolved = nil
			} else {
				// PrintError draws the report built with the same options:
				// the tree, each excerpt below its name, then each reason.
				assert.Equal(t, tc.want, p.PrintError(tc.err, tc.opts...))
			}

			assert.Equal(t, tc.want, p.PrintReport(rep))
		})
	}
}

func TestPrinter_PrintError_Warnings(t *testing.T) {
	t.Parallel()

	item := niceyaml.NewSourceFromString(
		stringtest.LinesLF("name: semla", "spec: {price: 0, legacyPricing: true}"),
		niceyaml.WithName("item.yaml"),
	)

	err := yamltest.Bind(t, item, errors.Join(
		yamltest.ErrorAt("0 is less than 1", "spec", "price"),
		niceyaml.NewWarning(`deprecated field "legacyPricing"`,
			niceyaml.AtPath(paths.Doc().Child("spec", "legacyPricing").Key())),
	))

	tcs := map[string]struct {
		p    *printer.Printer
		want string
	}{
		"each severity gets its own row": {
			p: testPrinterWithGutter(printer.DefaultGutter),
			want: stringtest.JoinLF(
				"├── item.yaml:2:15: $.spec.price: 0 is less than 1",
				`└── item.yaml:2:18: warning: $.spec.legacyPricing~: deprecated field "legacyPricing"`,
				"",
				"   1  name: semla",
				"   2  spec: {price: 0, legacyPricing: true}",
				"                    ^ 0 is less than 1",
				`                       ^^^^^^^^^^^^^ warning: deprecated field "legacyPricing"`,
			),
		},
		"the label renders in the warning text style": {
			p: testPrinterWithStyles(style.Set(kind.TextWarn, bracketStyle("<", ">"))),
			want: stringtest.JoinLF(
				"├── item.yaml:2:15: $.spec.price: 0 is less than 1",
				`└── item.yaml:2:18: <warning:> $.spec.legacyPricing~: deprecated field "legacyPricing"`,
				"",
				"name: semla",
				"spec: {price: 0, <legacyPricing>: true}",
				"              ^ 0 is less than 1",
				`<                 ^^^^^^^^^^^^^ warning: deprecated field "legacyPricing">`,
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.p.PrintError(err))
		})
	}
}
