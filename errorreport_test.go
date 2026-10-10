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
			want: niceyaml.ErrorTree{Text: "f.yaml: \n"},
		},
		"a join of typed-nil errors holds its message": {
			err:  errors.Join(nilErr, nilErr),
			want: niceyaml.ErrorTree{Text: "\n"},
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
