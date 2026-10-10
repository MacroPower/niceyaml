package niceyaml_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// reportExcerpts returns each excerpt of rep as a renderer draws it: its
// view as plain text, below the name of its source when the excerpt has
// Named set.
func reportExcerpts(rep niceyaml.ErrorReport) []string {
	var got []string

	for _, excerpt := range rep.Excerpts {
		part := excerpt.View.String()
		if excerpt.Named {
			part = excerpt.Source.Name() + "\n" + part
		}

		got = append(got, part)
	}

	return got
}

// reportSourceExcerpts returns each excerpt of rep as its view as plain
// text below the name of its source, whether or not the excerpt has
// Named set.
func reportSourceExcerpts(rep niceyaml.ErrorReport) []string {
	var got []string

	for _, excerpt := range rep.Excerpts {
		got = append(got, excerpt.Source.Name()+"\n"+excerpt.View.String())
	}

	return got
}

// reportUnresolved returns the message of each unresolved binding of rep.
func reportUnresolved(rep niceyaml.ErrorReport) []string {
	var got []string

	for _, bound := range rep.Unresolved {
		got = append(got, bound.Error())
	}

	return got
}

func TestNewErrorReport(t *testing.T) {
	t.Parallel()

	first := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("first.yaml"))
	second := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("second.yaml"))
	unnamed := niceyaml.NewSourceFromString("d: 4\n")
	secret := niceyaml.NewSourceFromString("e: 5\n", niceyaml.WithName("secret.yaml"), niceyaml.WithExcerpts(false))
	empty := niceyaml.NewSourceFromString("", niceyaml.WithName("empty.yaml"))

	at := func(key string) niceyaml.ErrorOption {
		return niceyaml.AtPath(paths.Current().Child(key))
	}
	outside := niceyaml.AtPosition(position.New(99, 0))

	badA := yamltest.Bind(t, first, niceyaml.NewError("bad a", at("a")))
	badB := yamltest.Bind(t, first, niceyaml.NewError("bad b", at("b")))
	badC := yamltest.Bind(t, second, niceyaml.NewError("bad c", at("c")))
	badD := yamltest.Bind(t, unnamed, niceyaml.NewError("bad d", at("d")))
	badE := yamltest.Bind(t, secret, niceyaml.NewError("bad e", at("e")))
	farA := yamltest.Bind(t, first, niceyaml.NewError("far a", outside))
	farC := yamltest.Bind(t, second, niceyaml.NewError("far c", outside))

	// The excerpt of each source among several, below its name.
	namedA := "first.yaml\n   1 | a: 1\n     |    ^ bad a"
	namedC := "second.yaml\n   1 | c: 3\n     |    ^ bad c"

	tcs := map[string]struct {
		err            error
		want           []string
		wantUnresolved []string
	}{
		"a lone binding has one excerpt with no name": {
			err:  fmt.Errorf("check: %w", badA),
			want: []string{"   1 | a: 1\n     |    ^"},
		},
		"bindings of one source share an excerpt with no name": {
			err: errors.Join(badA, badB),
			want: []string{stringtest.JoinLF(
				"   1 | a: 1",
				"     |    ^ bad a",
				"   2 | b: 2",
				"     |    ^ bad b",
			)},
		},
		"the excerpts of several sources are named": {
			err:  errors.Join(badC, badA),
			want: []string{namedC, namedA},
		},
		"a source with no name is not named": {
			err:  errors.Join(badA, badD),
			want: []string{namedA, "   1 | d: 4\n     |    ^ bad d"},
		},
		"a source where nothing resolves still names the other": {
			err:            errors.Join(badA, farC),
			want:           []string{namedA},
			wantUnresolved: []string{"second.yaml: far c"},
		},
		"a source with excerpts off still names the other": {
			err:  errors.Join(badA, badE),
			want: []string{namedA},
		},
		"a location outside the source is unresolved": {
			err:            fmt.Errorf("check: %w", farA),
			wantUnresolved: []string{"first.yaml: far a"},
		},
		"each unresolved binding comes in the order of the bindings": {
			err:            errors.Join(farC, farA),
			wantUnresolved: []string{"second.yaml: far c", "first.yaml: far a"},
		},
		"a binding with no location has nothing to explain": {
			err: yamltest.Bind(t, first, errors.New("plain")),
		},
		"a path into a document with no content has nothing to explain": {
			err: yamltest.Bind(t, empty, niceyaml.NewError("bad a", at("a"))),
		},
		"an unresolved binding whose detail resolves has an excerpt alone": {
			err: yamltest.Bind(t, first, niceyaml.NewError("far", outside,
				niceyaml.WithDetails(niceyaml.NewError("see a", at("a"))),
			)),
			want: []string{"   1 | a: 1\n     |    ^ see a"},
		},
		"an error bound to no source has a tree alone": {
			err: errors.New("plain"),
		},
		"nil has nothing": {},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rep := niceyaml.NewErrorReport(tc.err, niceyaml.WithContextLines(0))

			assert.Equal(t, tc.want, reportExcerpts(rep))
			assert.Equal(t, tc.wantUnresolved, reportUnresolved(rep))
			assert.Equal(t, textTree(niceyaml.NewErrorTree(tc.err)), textTree(rep.Tree))
		})
	}

	t.Run("an excerpt holds its source", func(t *testing.T) {
		t.Parallel()

		rep := niceyaml.NewErrorReport(errors.Join(badA, badC), niceyaml.WithContextLines(0))

		require.Len(t, rep.Excerpts, 2)
		assert.Same(t, first, rep.Excerpts[0].Source)
		assert.Same(t, second, rep.Excerpts[1].Source)
	})

	t.Run("an unresolved binding gives its reason", func(t *testing.T) {
		t.Parallel()

		rep := niceyaml.NewErrorReport(errors.Join(badA, farC), niceyaml.WithContextLines(0))

		require.Len(t, rep.Unresolved, 1)
		require.ErrorIs(t, rep.Unresolved[0], farC)
		require.ErrorIs(t, rep.Unresolved[0].Unresolved(), niceyaml.ErrOutOfRange)
	})

	t.Run("context keeps lines around each mark", func(t *testing.T) {
		t.Parallel()

		rep := niceyaml.NewErrorReport(badB, niceyaml.WithContextLines(1))

		want := []string{stringtest.JoinLF(
			"   1 | a: 1",
			"   2 | b: 2",
			"     |    ^",
		)}

		assert.Equal(t, want, reportExcerpts(rep))
		assert.Equal(
			t,
			reportExcerpts(niceyaml.NewErrorReport(badB, niceyaml.WithContextLines(0))),
			reportExcerpts(niceyaml.NewErrorReport(badB, niceyaml.WithContextLines(-1))),
		)
	})

	t.Run("FormatError draws each part", func(t *testing.T) {
		t.Parallel()

		err := errors.Join(badA, badC, farA)
		rep := niceyaml.NewErrorReport(err, niceyaml.WithContextLines(0))

		want := stringtest.JoinLF(
			"|-- first.yaml:1:4: $.a: bad a",
			"|-- second.yaml:1:4: $.c: bad c",
			"`-- first.yaml: far a",
			"",
			"first.yaml",
			rep.Excerpts[0].View.String(),
			"",
			"second.yaml",
			rep.Excerpts[1].View.String(),
			"",
			"no excerpt: "+rep.Unresolved[0].Unresolved().Error(),
		)

		assert.Equal(t, want, niceyaml.FormatError(err, niceyaml.WithContextLines(0)))
	})
}

func TestNewErrorReport_JoinOfNothing(t *testing.T) {
	t.Parallel()

	var nilErr *niceyaml.Error

	source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml"))
	unnamed := niceyaml.NewSourceFromString("a: 1\n")

	nothing := source.Bind(errors.Join(nilErr, nilErr))
	require.Error(t, nothing)

	// The message of this binding is empty, so it has no row in a tree,
	// and its location lies outside the source.
	silent := yamltest.Bind(t, unnamed, niceyaml.NewError("", niceyaml.AtPosition(position.New(99, 0))))

	tcs := map[string]struct {
		err            error
		want           niceyaml.ErrorTree
		wantUnresolved int
	}{
		"a bound join of typed-nil errors holds its message": {
			err:  nothing,
			want: niceyaml.ErrorTree{Label: "f.yaml: \n"},
		},
		"a join of typed-nil errors holds its message": {
			err:  errors.Join(nilErr, nilErr),
			want: niceyaml.ErrorTree{Label: "\n"},
		},
		"an error with no message holds nothing": {
			err: errors.New(""),
		},
		"a reason below an empty tree leaves the tree empty": {
			err:            errors.Join(nothing, silent),
			wantUnresolved: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rep := niceyaml.NewErrorReport(tc.err)

			// The tree of the error itself stays empty, so a report of its
			// problems lists none.
			assert.Equal(t, niceyaml.ErrorTree{}, niceyaml.NewErrorTree(tc.err))

			assert.Equal(t, tc.want, rep.Tree)
			assert.Empty(t, rep.Excerpts)
			assert.Len(t, rep.Unresolved, tc.wantUnresolved)
		})
	}
}

func TestNewErrorReport_SeveralSources(t *testing.T) {
	t.Parallel()

	manifest := niceyaml.NewSourceFromString("kind: App\nvalues: values.yaml\n", niceyaml.WithFilePath("manifest.yaml"))
	values := niceyaml.NewSourceFromString("port: many\nhost: 7\n", niceyaml.WithFilePath("values.yaml"))

	port := yamltest.Bind(t, values, niceyaml.NewError("not a number", niceyaml.AtPath(paths.Current().Child("port"))))
	host := yamltest.Bind(t, values, niceyaml.NewError("not a name", niceyaml.AtPath(paths.Current().Child("host"))))
	err := yamltest.Bind(t, manifest, niceyaml.NewError(
		"bad values",
		niceyaml.AtPath(paths.Current().Child("values")),
		niceyaml.WithDetails(port, host),
	))

	bound := yamltest.RequireSourceError(t, err)

	t.Run("one excerpt per source, the binding's source first", func(t *testing.T) {
		t.Parallel()

		var (
			sources  []string
			excerpts []string
		)

		for _, excerpt := range niceyaml.NewErrorReport(bound, niceyaml.WithContextLines(0)).Excerpts {
			sources = append(sources, excerpt.Source.Name())
			excerpts = append(excerpts, excerpt.View.String())
		}

		assert.Equal(t, []string{"manifest.yaml", "values.yaml"}, sources)
		require.Len(t, excerpts, 2)

		assert.Equal(t, "   2 | values: values.yaml\n     |         ^^^^^^^^^^^", excerpts[0])

		// The sibling children bound to the other source share one
		// excerpt of it, each with its message beside its line.
		assert.Equal(t, stringtest.JoinLF(
			"   1 | port: many",
			"     |       ^^^^ not a number",
			"   2 | host: 7",
			"     |       ^ not a name",
		), excerpts[1])
	})

	t.Run("Excerpt keeps to the source of the binding", func(t *testing.T) {
		t.Parallel()

		excerpt, ok := bound.Excerpt(niceyaml.WithContextLines(0))
		require.True(t, ok)
		assert.Equal(t, 1, excerpt.Count())
	})

	t.Run("Annotate marks the lines of any source the view holds", func(t *testing.T) {
		t.Parallel()

		view := values.View()
		require.True(t, bound.Annotate(view), "the details are bound to the source of the view")

		assert.Equal(t, stringtest.JoinLF(
			"   1 | port: many",
			"     |       ^^^^ not a number",
			"   2 | host: 7",
			"     |       ^ not a name",
		), view.String())

		// The view of the binding's own source takes the mark of the
		// binding and none of the details.
		own := bound.Source().View()
		require.True(t, bound.Annotate(own))
		assert.Equal(t, []int{2}, lineNumbers(own.Hunks(0)))
	})

	t.Run("FormatError renders every excerpt", func(t *testing.T) {
		t.Parallel()

		got := niceyaml.FormatError(bound, niceyaml.WithContextLines(0))
		assert.Equal(t, stringtest.JoinLF(
			"manifest.yaml:2:9: $.values: bad values",
			"|-- values.yaml:1:7: $.port: not a number",
			"`-- values.yaml:2:7: $.host: not a name",
			"",
			"manifest.yaml",
			"   2 | values: values.yaml",
			"     |         ^^^^^^^^^^^",
			"",
			"values.yaml",
			"   1 | port: many",
			"     |       ^^^^ not a number",
			"   2 | host: 7",
			"     |       ^ not a name",
		), got)
	})

	t.Run("children that alternate between sources mark their own", func(t *testing.T) {
		t.Parallel()

		srcs := []*niceyaml.Source{
			niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("s0.yaml")),
			niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("s1.yaml")),
			niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("s2.yaml")),
		}

		// Each source gets two children on line 1 and one on line 2, in
		// turn, so the tree leaves every source and comes back to it.
		var children []error

		for _, spec := range []struct{ key, msg string }{
			{"a", "first"}, {"a", "second"}, {"b", "third"},
		} {
			for i, src := range srcs {
				children = append(children, yamltest.Bind(t, src, niceyaml.NewError(
					fmt.Sprintf("%s %d", spec.msg, i),
					niceyaml.AtPath(paths.Current().Child(spec.key)),
				)))
			}
		}

		var mixed *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, srcs[1], niceyaml.NewError(
			"root",
			niceyaml.AtPath(paths.Current().Child("b")),
			niceyaml.WithDetails(children...),
		)), &mixed)

		got := map[string]string{}

		var names []string

		for _, excerpt := range niceyaml.NewErrorReport(mixed, niceyaml.WithContextLines(0)).Excerpts {
			names = append(names, excerpt.Source.Name())
			got[excerpt.Source.Name()] = excerpt.View.String()
		}

		assert.Equal(t, []string{"s1.yaml", "s0.yaml", "s2.yaml"}, names)
		assert.Equal(t, map[string]string{
			"s0.yaml": stringtest.JoinLF(
				"   1 | a: 1",
				"     |    ^ first 0; second 0",
				"   2 | b: 2",
				"     |    ^ third 0",
			),
			"s1.yaml": stringtest.JoinLF(
				"   1 | a: 1",
				"     |    ^ first 1; second 1",
				"   2 | b: 2",
				"     |    ^ third 1",
			),
			"s2.yaml": stringtest.JoinLF(
				"   1 | a: 1",
				"     |    ^ first 2; second 2",
				"   2 | b: 2",
				"     |    ^ third 2",
			),
		}, got)
	})

	t.Run("a tree in one source yields one excerpt", func(t *testing.T) {
		t.Parallel()

		var one *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, values, niceyaml.NewError("bad", niceyaml.WithDetails(
			niceyaml.NewError("not a number", niceyaml.AtPath(paths.Current().Child("port"))),
		))), &one)

		assert.Len(t, niceyaml.NewErrorReport(one, niceyaml.WithContextLines(0)).Excerpts, 1)
	})

	t.Run("nothing resolved yields nothing", func(t *testing.T) {
		t.Parallel()

		var none *niceyaml.SourceError

		require.ErrorAs(
			t,
			yamltest.Bind(
				t,
				values,
				niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("missing").Index(0))),
			),
			&none,
		)

		assert.Empty(t, niceyaml.NewErrorReport(none, niceyaml.WithContextLines(0)).Excerpts)

		var nilErr *niceyaml.SourceError

		assert.Empty(t, niceyaml.NewErrorReport(nilErr, niceyaml.WithContextLines(0)).Excerpts)
	})
}

func TestNewErrorReport_Excerpts(t *testing.T) {
	t.Parallel()

	first := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("first.yaml"))
	second := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("second.yaml"))

	badA := yamltest.Bind(t, first, niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))))
	badB := yamltest.Bind(t, first, niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b"))))
	badC := yamltest.Bind(t, second, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c"))))
	gone := yamltest.Bind(t, second, niceyaml.NewError("gone", niceyaml.AtPath(paths.Current().Child("x").Index(0))))

	tcs := map[string]struct {
		err  error
		want []string
	}{
		"bindings of one source share an excerpt": {
			err: errors.Join(badA, badB),
			want: []string{stringtest.JoinLF(
				"first.yaml",
				"   1 | a: 1",
				"     |    ^ bad a",
				"   2 | b: 2",
				"     |    ^ bad b",
			)},
		},
		"sources come in the order the bindings reach them": {
			err: errors.Join(badC, badA, badB),
			want: []string{
				"second.yaml\n   1 | c: 3\n     |    ^ bad c",
				stringtest.JoinLF(
					"first.yaml",
					"   1 | a: 1",
					"     |    ^ bad a",
					"   2 | b: 2",
					"     |    ^ bad b",
				),
			},
		},
		"a lone binding keeps its caret bare": {
			err:  fmt.Errorf("check: %w", badA),
			want: []string{"first.yaml\n   1 | a: 1\n     |    ^"},
		},
		"a binding the error reaches twice marks its line once": {
			err:  errors.Join(badA, fmt.Errorf("again: %w", badA)),
			want: []string{"first.yaml\n   1 | a: 1\n     |    ^ bad a"},
		},
		"a nested binding joined beside its parent marks its line once": {
			err: errors.Join(
				yamltest.Bind(t, first, niceyaml.NewError("parent", niceyaml.WithDetails(badA))),
				badA,
			),
			want: []string{"first.yaml\n   1 | a: 1\n     |    ^ bad a"},
		},
		"a source with nothing resolved yields nothing": {
			err:  errors.Join(badA, gone),
			want: []string{"first.yaml\n   1 | a: 1\n     |    ^ bad a"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rep := niceyaml.NewErrorReport(tc.err, niceyaml.WithContextLines(0))

			assert.Equal(t, tc.want, reportSourceExcerpts(rep))
		})
	}

	t.Run("each excerpt is a view of its own", func(t *testing.T) {
		t.Parallel()

		rep := niceyaml.NewErrorReport(errors.Join(badA, badB), niceyaml.WithContextLines(0))
		require.Len(t, rep.Excerpts, 1)

		// The source keeps no decoration from the excerpt.
		assert.Equal(t, "   1 | a: 1\n   2 | b: 2", first.View().String())
	})
}

func TestNewErrorReport_ExcerptsOff(t *testing.T) {
	t.Parallel()

	secrets := niceyaml.NewSourceFromString("password: hunter2\nport: 0\n",
		niceyaml.WithName("secrets.yaml"), niceyaml.WithExcerpts(false))
	open := niceyaml.NewSourceFromString("port: 0\n", niceyaml.WithName("open.yaml"))

	port := paths.Doc().Child("port")

	badPort := yamltest.Bind(t, secrets, niceyaml.NewError("bad port", niceyaml.AtPath(port)))
	openPort := yamltest.Bind(t, open, niceyaml.NewError("open port", niceyaml.AtPath(port)))

	tcs := map[string]struct {
		err error
		// The excerpts of the report of err.
		want []string
		// The excerpts of the report of the first binding in err.
		wantOwn []string
	}{
		"a binding in the source yields nothing": {
			err: badPort,
		},
		"several bindings in the source yield nothing": {
			err: errors.Join(badPort, yamltest.Bind(t, secrets,
				niceyaml.NewError("too short", niceyaml.AtPath(paths.Doc().Child("password"))))),
		},
		"a binding in another source yields its excerpt": {
			err:  errors.Join(badPort, openPort),
			want: []string{"open.yaml\n   1 | port: 0\n     |       ^ open port"},
		},
		"a detail in another source yields its excerpt": {
			err: yamltest.Bind(t, secrets, niceyaml.NewError(
				"ports differ", niceyaml.AtPath(port), niceyaml.WithDetails(openPort),
			)),
			want:    []string{"open.yaml\n   1 | port: 0\n     |       ^ open port"},
			wantOwn: []string{"open.yaml\n   1 | port: 0\n     |       ^ open port"},
		},
		"a detail in the source leaves the excerpt of its parent": {
			err: yamltest.Bind(t, open, niceyaml.NewError(
				"ports differ", niceyaml.AtPath(port), niceyaml.WithDetails(badPort),
			)),
			want:    []string{"open.yaml\n   1 | port: 0\n     |       ^"},
			wantOwn: []string{"open.yaml\n   1 | port: 0\n     |       ^"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, reportSourceExcerpts(niceyaml.NewErrorReport(tc.err)))

			bound := yamltest.RequireSourceError(t, tc.err)
			assert.Equal(t, tc.wantOwn, reportSourceExcerpts(niceyaml.NewErrorReport(bound)))
		})
	}

	t.Run("Excerpt reports false for a location that resolved", func(t *testing.T) {
		t.Parallel()

		bound := yamltest.RequireSourceError(t, badPort)

		excerpt, ok := bound.Excerpt()
		assert.False(t, ok)
		assert.Nil(t, excerpt)

		// The binding still names where it is, and it has no reason to
		// print in place of the excerpt.
		pos, ok := bound.Position()
		require.True(t, ok)
		assert.Equal(t, position.New(1, 6), pos)
		require.NoError(t, bound.Unresolved())
	})

	t.Run("Annotate marks a view the caller supplies", func(t *testing.T) {
		t.Parallel()

		view := secrets.View()
		require.True(t, niceyaml.Annotate(badPort, view))

		assert.Equal(t, stringtest.JoinLF(
			"   1 | password: hunter2",
			"   2 | port: 0",
			"     |       ^ bad port",
		), view.String())
	})
}
