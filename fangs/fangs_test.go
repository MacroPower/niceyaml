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

// nameValueYAML is the document most handler tests report errors in.
var nameValueYAML = stringtest.Input(`
	name: test
	value: 123
`)

// nameValueSource returns a new source of [nameValueYAML].
func nameValueSource(opts ...niceyaml.SourceOption) *niceyaml.Source {
	return niceyaml.NewSourceFromTokens(tokens.Tokenize(nameValueYAML), opts...)
}

// twoProblems returns an error that heads two problems in [nameValueYAML],
// one at the name key and one at the value.
func twoProblems() error {
	return fmt.Errorf("two problems: %w", errors.Join(
		errAt("bad name", "$.name~"),
		errAt("bad value", "$.value"),
	))
}

// handle returns what a handler created with opts writes for err under
// [testStyles]. The handler prints through [xmlPrinter] unless opts set
// another printer.
func handle(err error, opts ...fangs.Option) string {
	handler := fangs.NewErrorHandler(append([]fangs.Option{fangs.WithPrinter(xmlPrinter())}, opts...)...)

	var buf bytes.Buffer

	handler(&buf, testStyles(), err)

	return buf.String()
}

// handleAs returns what handler writes for err under [testStyles] to a
// terminal that renders profile.
func handleAs(handler fang.ErrorHandler, profile colorprofile.Profile, err error) string {
	var buf bytes.Buffer

	handler(&colorprofile.Writer{Forward: &buf, Profile: profile}, testStyles(), err)

	return buf.String()
}

// xmlPrinter returns a printer that renders styles as XML tags, with no
// gutter and no container frame. NewXMLPrinter in yamltest builds the same
// printer, but the niceyaml version this module requires predates it, and
// the tests must build against that version.
func xmlPrinter() *printer.Printer {
	return printer.New(
		printer.WithStyles(yamltest.NewXMLStyles()),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	)
}

// errAt returns an error that reports msg at the node path selects.
func errAt(msg, path string) error {
	return niceyaml.NewError(msg, niceyaml.AtPath(paths.MustParse(path)))
}

// report returns what a handler writes for msg under [testStyles]: the
// header, each line of msg behind the indent, and a blank line.
func report(msg string) string {
	rows := []string{"Error"}

	for row := range strings.SplitSeq(msg, "\n") {
		rows = append(rows, strings.Repeat(" ", fangs.Indent)+row)
	}

	return stringtest.JoinLF(append(rows, "", "")...)
}

func TestErrorHandler(t *testing.T) {
	t.Parallel()

	src := nameValueSource()

	niceyamlErr := yamltest.Bind(t, src, errAt("invalid name", "$.name~"))

	// Two named sources with the same content, so a joined error names the
	// file each branch came from and renders one excerpt per file.
	fileA := nameValueSource(niceyaml.WithName("a.yaml"))
	fileB := nameValueSource(niceyaml.WithName("b.yaml"))

	badName := yamltest.Bind(t, fileA, errAt("bad name", "$.name~"))
	badValue := yamltest.Bind(t, fileB, errAt("bad value", "$.value"))

	// A source whose name opens with Cobra's wording for an unknown flag,
	// so the message of an error bound to it does too.
	flagLikeFile := nameValueSource(niceyaml.WithName("unknown flag: a.yaml"))

	flagLikeNameErr := yamltest.Bind(t, flagLikeFile, errAt("bad name", "$.name~"))

	emptyMessageErr := yamltest.Bind(t, src, silentError{errAt("bad name", "$.name~")})

	// A wrapper around a join heads the problems of the join, here and in
	// the niceyaml version the go.mod of this module requires.
	nestedErr := yamltest.Bind(t, src, twoProblems())

	// A source that ends with blank lines, which an excerpt of its last
	// key shows as context below the caret.
	spaced := niceyaml.NewSourceFromString(stringtest.LinesLF("name: test", "", ""))
	spacedErr := yamltest.Bind(t, spaced, errAt("bad name", "$.name"))

	// The excerpt of a lone error at the name key.
	nameExcerpt := stringtest.JoinLF(
		"<genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
		"<textError>^^^^</textError>",
		"<nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
	)

	tcs := map[string]struct {
		err  error
		want string
	}{
		"nil error": {err: nil, want: report("")},
		// Spelled out rather than built with report, so a change to the
		// value of fangs.Indent fails a test.
		"simple error": {
			err:  errors.New("something went wrong"),
			want: stringtest.JoinLF("Error", "  something went wrong", "", ""),
		},
		"multi-line error": {
			err:  errors.New(stringtest.JoinLF("line1", "line2", "line3")),
			want: report(stringtest.JoinLF("line1", "line2", "line3")),
		},
		// Cobra indents each suggestion with a tab and ends the list with a
		// line break.
		"usage error unknown command with a suggestion": {
			err: errors.New(stringtest.LinesLF(
				`unknown command "valdate" for "nyaml"`,
				"",
				"Did you mean this?",
				"\tvalidate",
			)),
			want: report(stringtest.JoinLF(
				`unknown command "valdate" for "nyaml"`,
				"",
				"Did you mean this?",
				"    validate",
			)) + "Try --help for usage.\n\n",
		},
		"message ending in line breaks": {
			err:  errors.New("something went wrong\n\n"),
			want: report("something went wrong"),
		},
		// The blank lines belong to the document, so the handler prints
		// them as it prints the rest of the excerpt.
		"excerpt ending in blank lines keeps them": {
			err: spacedErr,
			want: report(stringtest.JoinLF(
				"1:7: $.name: bad name",
				"",
				"<nameTag>name</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>test</genericError>",
				"<textError>      ^^^^</textError>",
				"",
				"",
			)),
		},
		"niceyaml error with source": {
			err: niceyamlErr,
			want: report(stringtest.JoinLF(
				"1:1: $.name~: invalid name",
				"",
				nameExcerpt,
			)),
		},
		"niceyaml error whose source name opens with a usage pattern": {
			err: flagLikeNameErr,
			want: report(stringtest.JoinLF(
				"unknown flag: a.yaml:1:1: $.name~: bad name",
				"",
				nameExcerpt,
			)),
		},
		"wrapped niceyaml error keeps context and annotates once": {
			err: fmt.Errorf("document 0: %w", nestedErr),
			want: report(stringtest.JoinLF(
				"document 0: two problems:",
				"├── 1:1: $.name~: bad name",
				"└── 2:8: $.value: bad value",
				"",
				"<genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"<textError>^^^^ bad name</textError>",
				"<nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>123</genericError>",
				"<textError>       ^^^ bad value</textError>",
			)),
		},
		"niceyaml error with an empty message keeps the wrapper text and the path": {
			err: fmt.Errorf("document 0: %w", emptyMessageErr),
			want: report(stringtest.JoinLF(
				"document 0: 1:1: $.name~:",
				"",
				nameExcerpt,
			)),
		},
		"every joined niceyaml error annotates": {
			err: errors.Join(badName, badValue),
			want: report(stringtest.JoinLF(
				"├── a.yaml:1:1: $.name~: bad name",
				"└── b.yaml:2:8: $.value: bad value",
				"",
				"a.yaml",
				"<genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"<textError>^^^^ bad name</textError>",
				"<nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
				"",
				"b.yaml",
				"<nameTag>name</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"<nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>123</genericError>",
				"<textError>       ^^^ bad value</textError>",
			)),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, handle(tc.err))
		})
	}
}

func TestErrorHandler_UsageHint(t *testing.T) {
	t.Parallel()

	// The handler ends the report of a Cobra usage error with a hint. It
	// matches Cobra's exact wording, so an application error that opens
	// with a word of that wording gets no hint.
	tcs := map[string]struct {
		msg      string
		wantHint bool
	}{
		"flag needs argument": {msg: "flag needs an argument: --config", wantHint: true},
		"unknown flag":        {msg: "unknown flag: --foo", wantHint: true},
		"bad flag syntax":     {msg: "bad flag syntax: ---flag", wantHint: true},
		"exclusive flag group": {
			msg:      "if any flags in the group [a b] are set none of the others can be; [a b] were all set",
			wantHint: true,
		},
		"required flag group": {
			msg:      "at least one of the flags in the group [a b] is required",
			wantHint: true,
		},
		"unknown shorthand flag":            {msg: "unknown shorthand flag: 'x' in -xyz", wantHint: true},
		"unknown command":                   {msg: `unknown command "foo" for "nyaml"`, wantHint: true},
		"invalid argument":                  {msg: `invalid argument "foo" for "--count"`, wantHint: true},
		"requires at least":                 {msg: "requires at least 1 arg(s), only received 0", wantHint: true},
		"accepts at most":                   {msg: "accepts at most 2 arg(s), received 3", wantHint: true},
		"accepts exactly":                   {msg: "accepts 1 arg(s), received 2", wantHint: true},
		"accepts between":                   {msg: "accepts between 1 and 2 arg(s), received 3", wantHint: true},
		"required flag not set":             {msg: `required flag(s) "schema" not set`, wantHint: true},
		"opening with accepts":              {msg: "accepts only .yaml files"},
		"opening with invalid argument":     {msg: "invalid argument: path must be absolute"},
		"opening with requires at least":    {msg: "requires at least one schema"},
		"opening with unknown commands":     {msg: "unknown commands.yaml:1:4: bad"},
		"opening with unknown command-line": {msg: "unknown command-line option in config"},
		"with flag word":                    {msg: "flagged as incorrect"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := report(tc.msg)
			if tc.wantHint {
				want += "Try --help for usage.\n\n"
			}

			assert.Equal(t, want, handle(errors.New(tc.msg)))
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

			err := yamltest.Bind(t, src, errAt("invalid name", "$.name"))

			got := handle(err, fangs.WithPrinter(printer.New(printer.WithWrapWidth(tc.width))))

			// The widest row fills the width, so no row runs past it.
			assert.Equal(t, tc.width, lipgloss.Width(got))
		})
	}
}

func TestErrorHandler_ColorProfile(t *testing.T) {
	t.Parallel()

	err := yamltest.Bind(t, nameValueSource(), twoProblems())

	p := xmlPrinter()

	styled := report(p.PrintError(err, niceyaml.WithContextLines(0)))
	plain := report(niceyaml.FormatError(err, niceyaml.WithContextLines(0)))

	// The plain form holds no style and draws a caret run under each range.
	assert.NotContains(t, plain, "<")
	assert.Contains(t, plain, "^^^^ bad name")
	assert.Contains(t, styled, "<genericError>name</genericError>")

	tcs := map[string]struct {
		want    string
		profile colorprofile.Profile
	}{
		"true color":             {profile: colorprofile.TrueColor, want: styled},
		"256 colors":             {profile: colorprofile.ANSI256, want: styled},
		"16 colors":              {profile: colorprofile.ANSI, want: styled},
		"terminal without color": {profile: colorprofile.ASCII, want: plain},
		"not a terminal":         {profile: colorprofile.NoTTY, want: plain},
	}

	handler := fangs.NewErrorHandler(fangs.WithPrinter(p), fangs.WithExcerptOptions(niceyaml.WithContextLines(0)))

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, handleAs(handler, tc.profile, err))
		})
	}
}

func TestNewErrorHandler_WithExcerptOptions(t *testing.T) {
	t.Parallel()

	// The marked line sits in the middle of a source with more lines on
	// each side than the default shows, so the count changes the excerpt.
	src := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1", "b: 2", "c: 3", "d: 4", "e: 5", "f: 6", "g: 7", "h: 8", "i: 9",
	))
	err := yamltest.Bind(t, src, errAt("bad", "$.e"))

	p := xmlPrinter()

	tcs := map[string]struct {
		opts   []fangs.Option
		want   []niceyaml.ExcerptOption
		shown  []string
		hidden []string
	}{
		"default": {
			shown:  []string{"c: 3", "g: 7"},
			hidden: []string{"b: 2", "h: 8"},
		},
		"no context": {
			opts:   []fangs.Option{fangs.WithExcerptOptions(niceyaml.WithContextLines(0))},
			want:   []niceyaml.ExcerptOption{niceyaml.WithContextLines(0)},
			shown:  []string{"e: 5"},
			hidden: []string{"d: 4", "f: 6"},
		},
		"more context": {
			opts:   []fangs.Option{fangs.WithExcerptOptions(niceyaml.WithContextLines(3))},
			want:   []niceyaml.ExcerptOption{niceyaml.WithContextLines(3)},
			shown:  []string{"b: 2", "h: 8"},
			hidden: []string{"a: 1", "i: 9"},
		},
		"later call replaces the earlier": {
			opts: []fangs.Option{
				fangs.WithExcerptOptions(niceyaml.WithContextLines(3)),
				fangs.WithExcerptOptions(niceyaml.WithContextLines(0)),
			},
			want:   []niceyaml.ExcerptOption{niceyaml.WithContextLines(0)},
			shown:  []string{"e: 5"},
			hidden: []string{"d: 4", "f: 6"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler := fangs.NewErrorHandler(append([]fangs.Option{fangs.WithPrinter(p)}, tc.opts...)...)

			plain := handleAs(handler, colorprofile.ASCII, err)

			// Both paths draw the report the options build, so each shows
			// the lines of its renderer at those settings.
			assert.Equal(t, report(p.PrintError(err, tc.want...)), handleAs(handler, colorprofile.TrueColor, err))
			assert.Equal(t, report(niceyaml.FormatError(err, tc.want...)), plain)

			// The plain output holds each line of the source as text, where
			// the styled output splits it into tags.
			for _, line := range tc.shown {
				assert.Contains(t, plain, line)
			}

			for _, line := range tc.hidden {
				assert.NotContains(t, plain, line)
			}
		})
	}

	t.Run("an edit of the slice the caller passed changes nothing", func(t *testing.T) {
		t.Parallel()

		opts := []niceyaml.ExcerptOption{niceyaml.WithContextLines(0)}
		handler := fangs.NewErrorHandler(fangs.WithPrinter(p), fangs.WithExcerptOptions(opts...))

		opts[0] = niceyaml.WithContextLines(3)

		want := report(niceyaml.FormatError(err, niceyaml.WithContextLines(0)))
		assert.Equal(t, want, handleAs(handler, colorprofile.ASCII, err))
	})
}

func TestErrorHandler_UnwritableWriter(t *testing.T) {
	t.Parallel()

	// An error handler has nowhere to report a write of its own, so it drops
	// the write result rather than taking the process down with it.
	tcs := map[string]struct {
		err error
	}{
		"plain error": {err: errors.New("something went wrong")},
		"usage error, which writes the help hint too": {err: errors.New("unknown flag: --foo")},
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
