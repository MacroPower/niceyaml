package edit_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/paths"
)

// services returns a document of n services, each with an image, a
// comment, a count of replicas, and a sequence of ports.
func services(n int) string {
	var b strings.Builder

	b.WriteString("services:\n")

	for i := range n {
		fmt.Fprintf(
			&b,
			"  svc%d:\n    image: \"nginx:1.%d\" # pinned\n    replicas: 1\n    ports:\n      - 80\n      - 443\n",
			i,
			i,
		)
	}

	return b.String()
}

// setTime returns how long an Apply that sets the one value at path to 3
// takes on a new Doc of text. The Apply must fail when fails is true and
// pass otherwise. Tests bound the time of a larger Apply by a multiple of
// it, so the load of the machine scales the bound too.
func setTime(t *testing.T, text string, path paths.Path, fails bool) time.Duration {
	t.Helper()

	d := newDoc(t, text)
	start := time.Now()

	err := d.Apply(t.Context(), path, func(ctx context.Context, n *edit.Node) error {
		return n.Set(ctx, 3)
	})
	elapsed := time.Since(start)

	if fails {
		require.Error(t, err)
	} else {
		require.NoError(t, err)
	}

	return elapsed
}

func TestDoc_Apply_Queue(t *testing.T) {
	t.Parallel()

	setTo := func(v any) func(*edit.Doc) edit.Func {
		return func(*edit.Doc) edit.Func {
			return func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, v) }
		}
	}

	// The Func of setBy sets each place to the value that its key names in
	// vs. It leaves a place whose key vs lacks.
	setBy := func(vs map[string]any) func(*edit.Doc) edit.Func {
		return func(*edit.Doc) edit.Func {
			return func(ctx context.Context, n *edit.Node) error {
				v, ok := vs[n.Key()]
				if !ok {
					return nil
				}

				return n.Set(ctx, v)
			}
		}
	}

	// The Func of setThenRead sets the scalar at the path set to "xyz". It
	// then returns the error of a decode of the path read.
	setThenRead := func(set, read string) func(*edit.Doc) edit.Func {
		return func(*edit.Doc) edit.Func {
			return func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse(set)).Set(ctx, "xyz")
				if err != nil {
					return err
				}

				_, err = n.At(paths.MustParse(read)).Decode[any](ctx)

				return err
			}
		}
	}

	// Each row runs on a Doc whose Sets wait for the end of the Apply, and
	// on a Doc that parses and checks the text after each call. Both must
	// fail with the same message or leave the same text.
	tcs := map[string]struct {
		fn     func(d *edit.Doc) edit.Func
		before string
		path   string
		// The text after the Apply, or "" for a row that only compares.
		want string
		// A part of the message of the error of the Apply, or "".
		err string
		dup bool
	}{
		"set of each count of replicas": {
			before: services(3),
			path:   "$.services.*.replicas",
			fn:     setTo(3),
			want:   strings.ReplaceAll(services(3), "replicas: 1", "replicas: 3"),
		},
		"set of each port": {
			before: services(2),
			path:   "$.services.*.ports[*]",
			fn:     setTo(8080),
			want:   strings.ReplaceAll(strings.ReplaceAll(services(2), "- 80\n", "- 8080\n"), "- 443\n", "- 8080\n"),
		},
		"set of each quoted value": {
			before: stringtest.LinesLF(`a: "x" # one`, "b: 'y'", "c: z  # three"),
			path:   "$.*",
			fn:     setTo("w"),
			want:   stringtest.LinesLF(`a: "w" # one`, "b: 'w'", "c: w  # three"),
		},
		"set of strings that need quotes": {
			before: stringtest.LinesLF("a: 1", "b: 2", "c: 3", "d: 4", "e: 5"),
			path:   "$.*",
			fn:     setBy(map[string]any{"a": "yes", "b": "", "c": "#x", "d": "- x", "e": "1.0"}),
		},
		"set of values of each kind": {
			before: stringtest.LinesLF("a: x", "b: 1", "c: true", "d: 1.5", "e: .inf"),
			path:   "$.*",
			fn: setBy(map[string]any{
				"a": int64(-7), "b": "one", "c": uint64(18446744073709551615), "d": false, "e": float32(2.5),
			}),
		},
		"set of a string on two lines": {
			before: stringtest.LinesLF("a: 1", "b: 2", "c: 3"),
			path:   "$.*",
			fn:     setBy(map[string]any{"a": "x", "b": "one\ntwo", "c": "z"}),
		},
		"set of a long string": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   "$.*",
			fn:     setTo(strings.Repeat("word ", 60)),
		},
		"set of the same values": {
			before: stringtest.LinesLF("a: 1 # one", "b: x", "c: 3"),
			path:   "$.*",
			fn:     setBy(map[string]any{"a": 1, "b": "x", "c": 4}),
			want:   stringtest.LinesLF("a: 1 # one", "b: x", "c: 4"),
		},
		"set of a control character between two sets": {
			before: stringtest.LinesLF("a: 1", "b: 2", "c: 3"),
			path:   "$.*",
			fn:     setBy(map[string]any{"a": "x", "b": "y\x01z", "c": "w"}),
			err:    "go-yaml's encoder writes U+0001 as it is",
		},
		"set of a float that go-yaml reads as a string": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   "$.*",
			fn:     setBy(map[string]any{"a": 2, "b": 1e-07}),
			err:    "$.b",
		},
		"set of a protected value": {
			before: stringtest.LinesLF("a: 1", "kind: Cafe", "b: 2"),
			path:   "$.*",
			fn: func(*edit.Doc) edit.Func {
				return func(ctx context.Context, n *edit.Node) error {
					if n.Key() == "b" {
						return n.At(paths.Doc().Child("kind")).Set(ctx, "x")
					}

					return n.Set(ctx, "x")
				}
			},
			err: "protected",
		},
		"set that the decode of the user's text fails": {
			// The decode after the Set of $.b fails on $.a, and the problem
			// names the Set of $.b, which came first.
			before: stringtest.LinesLF("a: !!bool x", "b: 1", "c: 2"),
			path:   "$.*",
			fn:     setBy(map[string]any{"b": 5, "c": 6}),
			err:    "`-- cafe.jacobcolvin.com/v1 sets $.b",
		},
		"text with CRLF line breaks": {
			before: stringtest.JoinCRLF("a: 1", "b:", "  - 2", "  - 3", ""),
			path:   "$..*",
			fn: func(*edit.Doc) edit.Func {
				return func(ctx context.Context, n *edit.Node) error {
					if n.Kind() != niceyaml.NodeScalar {
						return nil
					}

					return n.Set(ctx, "v")
				}
			},
			want: stringtest.JoinCRLF("a: v", "b:", "  - v", "  - v", ""),
		},
		"text without a final newline": {
			before: stringtest.JoinLF("a: 1", "b: 2"),
			path:   "$.*",
			fn:     setTo(9),
			want:   stringtest.JoinLF("a: 9", "b: 9"),
		},
		"text after a byte order mark": {
			before: "\uFEFFa: 1\nb: 2\n",
			path:   "$.*",
			fn:     setTo(9),
			want:   "a: 9\nb: 9\n",
		},
		"text of the first of two documents": {
			before: stringtest.LinesLF("a: 1", "b: 2", "# next", "---", "a: 3"),
			path:   "$.*",
			fn:     setTo(9),
			want:   stringtest.LinesLF("a: 9", "b: 9"),
		},
		"set beside a block scalar at the end of a text without a final newline": {
			before: stringtest.JoinLF("a: 1", "b: |", "  x"),
			path:   "$.a",
			fn:     setTo(2),
			want:   stringtest.JoinLF("a: 2", "b: |", "  x"),
		},
		"set beside a flow mapping and a tag": {
			before: stringtest.LinesLF("a: {x: 1}", "b: !t 2", "c: 3"),
			path:   "$..*",
			fn: func(*edit.Doc) edit.Func {
				return func(ctx context.Context, n *edit.Node) error {
					if n.Kind() != niceyaml.NodeScalar {
						return nil
					}

					return n.Set(ctx, 7)
				}
			},
		},
		"set beside an anchor": {
			before: stringtest.LinesLF("a: &x 1", "b: 2", "c: *x"),
			path:   "$.*",
			fn:     setBy(map[string]any{"b": 5}),
			want:   stringtest.LinesLF("a: &x 1", "b: 5", "c: *x"),
		},
		"set of a key that the Source repeats": {
			before: stringtest.LinesLF("a: 1", "b: 2", "a: 3"),
			path:   "$.*",
			fn:     setTo(9),
			err:    "a key that its mapping repeats",
			dup:    true,
		},
		"decode of each place before its set": {
			before: stringtest.LinesLF("a: 1", "b: 2", "c: 3"),
			path:   "$.*",
			fn: func(*edit.Doc) edit.Func {
				return func(ctx context.Context, n *edit.Node) error {
					v, err := n.Decode[int](ctx)
					if err != nil {
						return err
					}

					return n.Set(ctx, v*10)
				}
			},
			want: stringtest.LinesLF("a: 10", "b: 20", "c: 30"),
		},
		"reads of a place after its set": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   "$.*",
			fn: func(*edit.Doc) edit.Func {
				return func(ctx context.Context, n *edit.Node) error {
					err := n.Set(ctx, "x"+n.Key())
					if err != nil {
						return err
					}

					v, err := n.Decode[string](ctx)
					if err != nil {
						return err
					}

					text, ok := n.Scalar()
					if v != "x"+n.Key() || !ok || text != v || n.Kind() != niceyaml.NodeScalar {
						return fmt.Errorf("the reads of %s give %q and %q", n.Path(), v, text)
					}

					return nil
				}
			},
			want: stringtest.LinesLF("a: xa", "b: xb"),
		},
		"decode of the mapping after a set inside it": {
			before: stringtest.LinesLF("m:", "  a: 1", "  b: 2"),
			path:   "$.m",
			fn: func(*edit.Doc) edit.Func {
				return func(ctx context.Context, n *edit.Node) error {
					err := n.At(paths.Current().Child("a")).Set(ctx, 5)
					if err != nil {
						return err
					}

					m, err := n.Decode[map[string]int](ctx)
					if err != nil {
						return err
					}

					return n.At(paths.Current().Child("b")).Set(ctx, m["a"]+1)
				}
			},
			want: stringtest.LinesLF("m:", "  a: 5", "  b: 6"),
		},
		"text of the document after a set": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   "$.*",
			fn: func(d *edit.Doc) edit.Func {
				return func(ctx context.Context, n *edit.Node) error {
					err := n.Set(ctx, 7)
					if err != nil {
						return err
					}

					if !strings.Contains(d.Text(), n.Key()+": 7") || !d.Edited() {
						return fmt.Errorf("the Doc does not show the set of %s", n.Path())
					}

					return nil
				}
			},
			want: stringtest.LinesLF("a: 7", "b: 7"),
		},
		"decode of a key below a scalar after its set": {
			before: stringtest.LinesLF("a: 1", "b: 2", "c:", "  - 1"),
			path:   "$",
			fn:     setThenRead("$.a", "$.a.b"),
			err:    "sets $.a from this value",
		},
		"decode of an item below a scalar after its set": {
			before: stringtest.LinesLF("a: 1", "b: 2", "c:", "  - 1"),
			path:   "$",
			fn:     setThenRead("$.a", "$.a[3]"),
			err:    "sets $.a from this value",
		},
		"decode of a path two steps below a scalar after its set": {
			before: stringtest.LinesLF("a: 1", "b: 2", "c:", "  - 1"),
			path:   "$",
			fn:     setThenRead("$.a", "$.a[0].x"),
			err:    "sets $.a from this value",
		},
		"decode of a missing key beside a scalar after its set": {
			before: stringtest.LinesLF("a: 1", "b: 2", "c:", "  - 1"),
			path:   "$",
			fn:     setThenRead("$.a", "$.d"),
			err:    "$.d",
		},
		"decode of a missing item after a set": {
			before: stringtest.LinesLF("a: 1", "b: 2", "c:", "  - 1"),
			path:   "$",
			fn:     setThenRead("$.a", "$.c[4]"),
			err:    "$.c[4]",
		},
		"decode of a missing key of a mapping after a set inside it": {
			before: stringtest.LinesLF("a: 1", "m:", "  b: 2", "  c: 3"),
			path:   "$",
			fn:     setThenRead("$.m.b", "$.m.x"),
			err:    "$.m.x",
		},
		"decode of a missing item of a sequence after a set inside it": {
			before: stringtest.LinesLF("a: 1", "s:", "  - 2", "  - 3"),
			path:   "$",
			fn:     setThenRead("$.s[0]", "$.s[5]"),
			err:    "$.s[5]",
		},
		"two sets of one place": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   "$.*",
			fn: func(*edit.Doc) edit.Func {
				return func(ctx context.Context, n *edit.Node) error {
					err := n.Set(ctx, 3)
					if err != nil {
						return err
					}

					return n.Set(ctx, "x")
				}
			},
			want: stringtest.LinesLF("a: x", "b: x"),
		},
		"set and then a removal": {
			before: stringtest.LinesLF("a: 1", "b: 2", "c: 3"),
			path:   "$.*",
			fn: func(*edit.Doc) edit.Func {
				return func(ctx context.Context, n *edit.Node) error {
					if n.Key() == "b" {
						return n.Remove()
					}

					return n.Set(ctx, 0)
				}
			},
			want: stringtest.LinesLF("a: 0", "c: 0"),
		},
		"set and then a path through the scalar": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   "$.*",
			fn: func(*edit.Doc) edit.Func {
				return func(ctx context.Context, n *edit.Node) error {
					err := n.Set(ctx, "x")
					if err != nil {
						return err
					}

					return n.At(paths.Current().Child("c")).Set(ctx, 1)
				}
			},
			err: "$.a",
		},
		"set and then an error of the func": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   "$.*",
			fn: func(*edit.Doc) edit.Func {
				return func(ctx context.Context, n *edit.Node) error {
					err := n.Set(ctx, 5)
					if err != nil || n.Key() == "a" {
						return err
					}

					return errors.New("stop")
				}
			},
			err: "stop",
		},
		"set of a control character and then an error of the func": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   "$.*",
			fn: func(*edit.Doc) edit.Func {
				return func(ctx context.Context, n *edit.Node) error {
					if n.Key() == "a" {
						return n.Set(ctx, "\x01")
					}

					return errors.New("stop")
				}
			},
			err: "go-yaml's encoder writes U+0001 as it is",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(tc.dup)}
			path := paths.MustParse(tc.path)

			run := func(d *edit.Doc) error {
				return d.Apply(t.Context(), path, tc.fn(d), label)
			}

			d := newDoc(t, tc.before, opts...)
			err := run(d)

			requireEachCall(t, d, err, tc.before, opts, run)

			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, niceyaml.FormatError(err), tc.err)
				assert.Equal(t, tc.before, d.Text())

				return
			}

			require.NoError(t, err)

			if tc.want != "" {
				assert.Equal(t, tc.want, d.Text())
			}
		})
	}
}

func TestDoc_Apply_Queue_Deadline(t *testing.T) {
	t.Parallel()

	// A Set of each of 1,500 counts in 140 KiB of text parses and checks
	// the text once. So the Apply takes a few times as long as a Set of one
	// count and ends well before the deadline. A parse and a check of the
	// whole text after each Set takes about 1,500 times as long. The slower
	// of two single Sets sets the deadline, so that a load on the machine
	// in one of them does not shrink it.
	before := services(1500)
	one := paths.MustParse("$.services.svc0.replicas")
	unit := max(setTime(t, before, one, false), setTime(t, before, one, false))
	d := newDoc(t, before)

	ctx, cancel := context.WithTimeout(t.Context(), 100*unit)
	defer cancel()

	err := d.Apply(ctx, paths.MustParse("$.services.*.replicas"), func(ctx context.Context, n *edit.Node) error {
		return n.Set(ctx, 3)
	})
	require.NoError(t, err)

	assert.Equal(t, strings.ReplaceAll(before, "replicas: 1", "replicas: 3"), d.Text())
}

func TestDoc_Apply_Queue_ContextEnded(t *testing.T) {
	t.Parallel()

	// The Set of $.a returns nil. The decode of the text after it fails on
	// $.x, but the Apply finds that problem only after the next call ended
	// the context. The Apply fails with the problem of the Set, which came
	// first.
	before := stringtest.LinesLF("x: !!bool y", "a: 1", "b: 2", "c: 3")
	d := newDoc(t, before)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var calls []string

	err := d.Apply(ctx, paths.MustParse("$.*"), func(ctx context.Context, n *edit.Node) error {
		calls = append(calls, n.Key())

		switch n.Key() {
		case "x":
			return nil

		case "b":
			cancel()

			return nil

		default:
			return n.Set(ctx, 5)
		}
	})
	require.Error(t, err)
	require.NotErrorIs(t, err, context.Canceled)
	assert.Contains(t, niceyaml.FormatError(err), stringtest.JoinLF(
		`test.yaml:1:11: $.x: cannot convert "y" to boolean`,
		"`-- the edit sets $.a",
	))

	assert.Equal(t, []string{"x", "a", "b"}, calls)
	assert.Equal(t, before, d.Text())
}

func TestDoc_Apply_Queue_ContextEndedLater(t *testing.T) {
	t.Parallel()

	// A Set of a key after $.k0 fails the check, and the call of $.x ends
	// the context. The Sets of $.k0 to $.k9 wait, so the Apply finds the
	// problem only after the context ended. The Set of $.k0 runs again and
	// passes, and the Apply then fails with the error of the context. A Doc
	// whose Sets do not wait fails at the Set, before the call of $.x.
	before := keys(10) + "x: !!bool y\n"

	run := func(d *edit.Doc, calls *[]string) error {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		return d.Apply(ctx, paths.MustParse("$.*"), func(ctx context.Context, n *edit.Node) error {
			*calls = append(*calls, n.Key())

			if n.Key() == "x" {
				cancel()
			}

			return setKeys(ctx, n)
		})
	}

	var calls []string

	d := newDoc(t, before)
	err := run(d, &calls)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, []string{"k0", "k1", "k2", "k3", "k4", "k5", "k6", "k7", "k8", "k9", "x"}, calls)
	assert.Equal(t, before, d.Text())

	_, oracle := newSourceDocWith(t, before, []edit.Option{eachCall})
	calls = nil
	err = run(oracle, &calls)
	require.Error(t, err)
	require.NotErrorIs(t, err, context.Canceled)
	assert.Contains(t, niceyaml.FormatError(err), `$.x: cannot convert "y" to boolean`)
	assert.NotContains(t, calls, "x")
	assert.Equal(t, before, oracle.Text())
}

// keys returns a mapping of n keys, each with the value 1 and a comment.
func keys(n int) string {
	var b strings.Builder

	for i := range n {
		fmt.Fprintf(&b, "k%d: 1 # count\n", i)
	}

	return b.String()
}

// setKeys returns a Func that sets each place to 3, except the place of
// the key x.
func setKeys(ctx context.Context, n *edit.Node) error {
	if n.Key() == "x" {
		return nil
	}

	return n.Set(ctx, 3)
}

func TestDoc_Apply_Queue_FirstProblem(t *testing.T) {
	t.Parallel()

	// The decode after the Set of $.k0 fails on $.x, so the check of the
	// 800 Sets that wait fails. The Sets run again one at a time only until
	// the first of them fails, and the Apply fails with its problem. An
	// Apply of the Set of $.k0 alone does the same parses and checks, so
	// the Apply takes a few times as long. A run of each of the 800 Sets
	// with a parse and a check of the text takes hundreds of times as
	// long. The Apply of $.k0 runs before and after. The slower of the two
	// sets the bound, so that the load on the machine scales it.
	before := "x: !!bool y\n" + keys(800)
	path := paths.MustParse("$.*")
	one := paths.MustParse("$.k0")

	run := func(d *edit.Doc) error {
		return d.Apply(t.Context(), path, setKeys)
	}

	unit := setTime(t, before, one, true)
	d := newDoc(t, before)
	start := time.Now()
	err := run(d)
	elapsed := time.Since(start)
	unit = max(unit, setTime(t, before, one, true))

	require.Error(t, err)
	assert.Contains(t, niceyaml.FormatError(err), stringtest.JoinLF(
		`test.yaml:1:11: $.x: cannot convert "y" to boolean`,
		"`-- the edit sets $.k0",
	))
	assert.Less(t, elapsed, 50*unit)
	assert.Equal(t, before, d.Text())

	requireEachCall(t, d, err, before, nil, run)
}

func TestDoc_Apply_Queue_ReplayDeadline(t *testing.T) {
	t.Parallel()

	// The decode after the Set of a key near the end fails on $.x, so the
	// check of the 800 Sets that wait fails. The Sets would then run again
	// one at a time, each with a parse and a check of the text, which takes
	// hundreds of times as long as an Apply of the Set of $.k0 alone. The
	// Func cancels the context of the Apply at $.x, the last place, so the
	// run of the Sets one at a time stops a Set after it starts. The Apply
	// of $.k0 runs before and after, and the slower of the two bounds the
	// time of the Apply.
	before := keys(800) + "x: !!bool y\n"
	one := paths.MustParse("$.k0")
	unit := setTime(t, before, one, true)
	d := newDoc(t, before)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	start := time.Now()
	err := d.Apply(ctx, paths.MustParse("$.*"), func(ctx context.Context, n *edit.Node) error {
		if n.Key() == "x" {
			cancel()
		}

		return setKeys(ctx, n)
	})
	elapsed := time.Since(start)
	unit = max(unit, setTime(t, before, one, true))

	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, elapsed, 40*unit)
	assert.Equal(t, before, d.Text())
}
