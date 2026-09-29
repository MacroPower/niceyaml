package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// hours validates itself with a path written from its own root.
type hours struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (h hours) Validate() error {
	if h.Close < h.Open {
		return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Root().Child("close")))
	}

	return nil
}

// schedule validates itself with several nested errors, each with a path
// written from its own root.
type schedule struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (s schedule) Validate() error {
	var errs []error

	if s.Open == "" {
		errs = append(errs, niceyaml.NewError("open is empty", niceyaml.AtPath(paths.Root().Child("open"))))
	}

	if s.Close == "" {
		errs = append(errs, niceyaml.NewError("close is empty", niceyaml.AtPath(paths.Root().Child("close"))))
	}

	if len(errs) == 0 {
		return nil
	}

	return niceyaml.NewError("invalid schedule", niceyaml.WithErrors(errs...))
}

// item validates itself through a pointer receiver.
type item struct {
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

func (it *item) Validate() error {
	if it.Price < 0 {
		return niceyaml.NewError("negative price", niceyaml.AtPath(paths.Root().Child("price")))
	}

	return nil
}

// signed validates itself with a message that names its value.
type signed struct {
	N int `yaml:"n"`
}

func (s signed) Validate() error {
	if s.N < 0 {
		return niceyaml.NewError(fmt.Sprintf("negative %d", s.N), niceyaml.AtPath(paths.Root().Child("n")))
	}

	return nil
}

var (
	// The error a value reports when its children ran after it.
	errOrder = errors.New("children ran after the parent")
	// The error every Validate of a ring reports.
	errRing = errors.New("ring")
	// The error every Validate of a loop reports.
	errLoop = errors.New("loop")
)

// Hours is hours under an exported name, for embedding without a tag.
type Hours = hours

// Named requires a name, for embedding.
type Named struct {
	Name string `yaml:"name"`
}

func (n Named) Validate() error {
	if n.Name == "" {
		return niceyaml.NewError("name required", niceyaml.AtPath(paths.Root().Child("name")))
	}

	return nil
}

// Counted counts the runs of its Validate, for embedding.
type Counted struct {
	Name string `yaml:"name"`

	runs int
}

func (c *Counted) Validate() error {
	c.runs++

	return nil
}

// Positive decodes itself from an integer and rejects a negative one,
// for embedding.
type Positive int

func (p *Positive) UnmarshalYAML(b []byte) error {
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return fmt.Errorf("positive: %w", err)
	}

	*p = Positive(n)

	return nil
}

func (p Positive) Validate() error {
	if p < 0 {
		return niceyaml.NewError("negative")
	}

	return nil
}

// innerPositive is Positive under an unexported name, for embedding as
// an unexported field.
type innerPositive = Positive

// PositiveWrapper decodes itself through the Positive it embeds.
type PositiveWrapper struct {
	Positive
}

// PositivePointer decodes itself through the Positive it points to, so
// it holds no Positive until a document sets it.
type PositivePointer struct {
	*Positive
}

// declaredDecode gets Validate from the Positive it embeds, but decodes
// itself through an UnmarshalYAML of its own that leaves Positive
// negative.
type declaredDecode struct {
	Positive
}

func (d *declaredDecode) UnmarshalYAML([]byte) error {
	d.Positive = -1

	return nil
}

// textDecode declares an UnmarshalText, but go-yaml decodes it through
// the UnmarshalYAML of the Positive it embeds, which it checks first.
type textDecode struct {
	Positive
}

func (*textDecode) UnmarshalText([]byte) error {
	return errors.New("text ran")
}

// declaredValidate embeds Named and declares a Validate of its own on
// its value.
type declaredValidate struct {
	Named
}

func (declaredValidate) Validate() error {
	return errors.New("parent ran")
}

// declaredPointerValidate embeds Named and declares a Validate of its
// own on its pointer.
type declaredPointerValidate struct {
	Named
}

func (*declaredPointerValidate) Validate() error {
	return errors.New("parent ran")
}

// nested holds SelfValidator values at every depth and shape.
type nested struct {
	Hours    hours            `yaml:"hours"`
	Items    []item           `yaml:"items"`
	ByName   map[string]item  `yaml:"by_name"`
	ByID     map[int]item     `yaml:"by_id"`
	Backup   *hours           `yaml:"backup"`
	Untagged hours            // decodes under "untagged"
	Ignored  hours            `yaml:"-"`
	private  hours            //nolint:unused // An unexported field is not decoded.
	Any      any              `yaml:"any"`
	Fixed    [2]item          `yaml:"fixed"`
	Deep     map[string][]any `yaml:"deep"`
}

func TestDocument_Decode_NestedSelfValidator(t *testing.T) {
	t.Parallel()

	t.Run("a nested field reports its path", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			hours:
			  open: "09:00"
			  close: "08:00"
		`), niceyaml.WithName("cafe.yaml"))

		doc, err := source.Document()
		require.NoError(t, err)

		_, err = doc.Decode[nested](t.Context())
		require.EqualError(t, err, "cafe.yaml:3:10: $.hours.close: closes before it opens")

		var e *niceyaml.Error

		require.ErrorAs(t, err, &e)

		p, ok := e.Path()
		require.True(t, ok)
		assert.Equal(t, "$.hours.close", p.String())
	})

	t.Run("errors nested under a field report the joined path", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			schedule:
			  open: ""
			  close: ""
		`), niceyaml.WithName("cafe.yaml"))

		doc, err := source.Document()
		require.NoError(t, err)

		_, err = doc.Decode[struct {
			Schedule schedule `yaml:"schedule"`
		}](t.Context())
		require.EqualError(t, err, "cafe.yaml:2:3: $.schedule: invalid schedule")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		require.Len(t, bound.Errors(), 2)

		assert.Equal(t, "cafe.yaml:2:9: $.schedule.open: open is empty", bound.Errors()[0].Error())
		assert.Equal(t, "cafe.yaml:3:10: $.schedule.close: close is empty", bound.Errors()[1].Error())

		p, ok := bound.Errors()[1].Path()
		require.True(t, ok)
		assert.Equal(t, "$.schedule.close", p.String())

		assert.Equal(t, stringtest.JoinLF(
			"cafe.yaml:2:3: $.schedule: invalid schedule",
			"|-- 2:9: $.schedule.open: open is empty",
			"`-- 3:10: $.schedule.close: close is empty",
			"",
			"   1 | schedule:",
			`   2 |   open: ""`,
			"     |   ^^^^  ^^ open is empty",
			`   3 |   close: ""`,
			"     |          ^^ close is empty",
		), niceyaml.FormatError(err, 2))
	})

	t.Run("elements and entries report their index or key", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			items:
			  - name: tea
			    price: 1
			  - name: coffee
			    price: -1
			by_name:
			  scone:
			    price: -2
			by_id:
			  7:
			    price: -3
			fixed:
			  - price: 0
			  - price: -4
			deep:
			  menu:
			    - {price: -5}
		`), niceyaml.WithName("cafe.yaml"))

		doc, err := source.Document()
		require.NoError(t, err)

		_, err = doc.Decode[nested](t.Context())
		require.Error(t, err)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		var got []string

		for _, child := range bound.Errors() {
			got = append(got, child.Error())
		}

		// The deep any holds maps and slices of any, which hold no item.
		assert.ElementsMatch(t, []string{
			"cafe.yaml:5:12: $.items[1].price: negative price",
			"cafe.yaml:8:12: $.by_name.scone.price: negative price",
			"cafe.yaml:11:12: $.by_id.7.price: negative price",
			"cafe.yaml:14:12: $.fixed[1].price: negative price",
		}, got)
	})

	t.Run("a pointer walks when set and skips when nil", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			hours: {open: "09:00", close: "17:00"}
		`))

		_, err := dd.Decode[nested](t.Context())
		require.NoError(t, err)

		dd = yamltest.FirstDocument(t, stringtest.Input(`
			hours: {open: "09:00", close: "17:00"}
			backup: {open: "09:00", close: "08:00"}
		`))

		_, err = dd.Decode[nested](t.Context())
		require.EqualError(t, err, "2:32: $.backup.close: closes before it opens")
	})

	t.Run("a field without a tag decodes under its lowercased name", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			hours: {open: "09:00", close: "17:00"}
			untagged: {open: "09:00", close: "08:00"}
		`))

		_, err := dd.Decode[nested](t.Context())
		require.EqualError(t, err, "2:34: $.untagged.close: closes before it opens")
	})

	t.Run("a json tag names the field when there is no yaml tag", func(t *testing.T) {
		t.Parallel()

		type jsonTagged struct {
			Opening hours `json:"opening"`
		}

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			opening: {open: "09:00", close: "08:00"}
		`))

		_, err := dd.Decode[jsonTagged](t.Context())
		require.EqualError(t, err, "1:33: $.opening.close: closes before it opens")
	})

	t.Run("an inline field keeps the path of its parent", func(t *testing.T) {
		t.Parallel()

		type withInline struct {
			Name  string `yaml:"name"`
			Hours hours  `yaml:",inline"`
		}

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			name: x
			open: "09:00"
			close: "08:00"
		`))

		_, err := dd.Decode[withInline](t.Context())
		require.EqualError(t, err, "3:8: $.close: closes before it opens")
	})

	t.Run("an inline field its parent shadows does not validate", func(t *testing.T) {
		t.Parallel()

		type base struct {
			Port positive `yaml:"port"`
			Host string   `yaml:"host"`
		}

		type mid struct {
			Base base   `yaml:",inline"`
			Host string `yaml:"host"`
		}

		type server struct {
			Base base     `yaml:",inline"`
			Port positive `yaml:"port"`
		}

		type pointer struct {
			Base *base    `yaml:",inline"`
			Port positive `yaml:"port"`
		}

		type deep struct {
			Mid  mid      `yaml:",inline"`
			Port positive `yaml:"port"`
		}

		tcs := map[string]struct {
			decode func(*niceyaml.Node) error
			input  string
			err    string
		}{
			"struct": {
				input: "host: a\nport: 8080\n",
				decode: func(n *niceyaml.Node) error {
					_, err := n.Decode[server](t.Context())

					return err
				},
			},
			"pointer": {
				input: "host: a\nport: 8080\n",
				decode: func(n *niceyaml.Node) error {
					_, err := n.Decode[pointer](t.Context())

					return err
				},
			},
			"parent field still validates": {
				input: "host: a\nport: 0\n",
				err:   "2:7: $.port: must be positive",
				decode: func(n *niceyaml.Node) error {
					_, err := n.Decode[server](t.Context())

					return err
				},
			},
			"a field below a deeper inline struct validates": {
				input: "host: a\nport: 0\n",
				err:   "$.port: must be positive\n$.port: must be positive",
				decode: func(n *niceyaml.Node) error {
					_, err := n.Decode[deep](t.Context())

					return err
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := tc.decode(yamltest.FirstDocument(t, tc.input))
				if tc.err != "" {
					require.EqualError(t, err, tc.err)

					return
				}

				require.NoError(t, err)
			})
		}
	})

	t.Run("an embedded struct without inline decodes under its name", func(t *testing.T) {
		t.Parallel()

		type embedded struct {
			Name string `yaml:"name"`
			Hours
		}

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			name: x
			hours: {open: "09:00", close: "08:00"}
		`))

		_, err := dd.Decode[embedded](t.Context())
		require.EqualError(t, err, "2:31: $.hours.close: closes before it opens")
	})

	t.Run("a nil embedded pointer does not validate", func(t *testing.T) {
		t.Parallel()

		type inline struct {
			*Named `yaml:",inline"`

			Spec string `yaml:"spec"`
		}

		type named struct {
			*Named

			Spec string `yaml:"spec"`
		}

		tcs := map[string]struct {
			decode func(*niceyaml.Node) error
			input  string
		}{
			"inline": {
				input: "# empty\n",
				decode: func(n *niceyaml.Node) error {
					_, err := n.Decode[inline](t.Context())

					return err
				},
			},
			"named": {
				input: "spec: x\n",
				decode: func(n *niceyaml.Node) error {
					_, err := n.Decode[named](t.Context())

					return err
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				require.NoError(t, tc.decode(yamltest.FirstDocument(t, tc.input)))
			})
		}
	})

	t.Run("an ignored embedded field does not validate", func(t *testing.T) {
		t.Parallel()

		type ignored struct {
			Named `yaml:"-"`

			Title string `yaml:"name"`
		}

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			name: hello
		`))

		_, err := dd.Decode[ignored](t.Context())
		require.NoError(t, err)
	})

	t.Run("an embedded validator runs once, at its own path", func(t *testing.T) {
		t.Parallel()

		type inline struct {
			Counted `yaml:",inline"`
		}

		type named struct {
			Named
		}

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			name: x
		`))

		got, err := dd.Decode[inline](t.Context())
		require.NoError(t, err)
		assert.Equal(t, 1, got.runs)

		dd = yamltest.FirstDocument(t, stringtest.Input(`
			named: {name: ""}
		`))

		_, err = dd.Decode[named](t.Context())
		require.EqualError(t, err, "1:15: $.named.name: name required")
	})

	t.Run("a struct that embeds a validator runs its own Validate", func(t *testing.T) {
		t.Parallel()

		type parent struct {
			Value   declaredValidate        `yaml:"value"`
			Pointer declaredPointerValidate `yaml:"pointer"`
		}

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			value: {named: {name: x}}
			pointer: {named: {name: x}}
		`))

		_, err := dd.Decode[parent](t.Context())
		require.EqualError(t, err, "$.value: parent ran\n$.pointer: parent ran")
	})

	t.Run("an embedded field that decodes its struct validates at the struct", func(t *testing.T) {
		t.Parallel()

		type wrapped struct {
			Positive
		}

		// Go-yaml calls the UnmarshalYAML of Positive ahead of the
		// UnmarshalText of time.Time.
		type both struct {
			Positive
			time.Time
		}

		type parent struct {
			Wrapped  wrapped         `yaml:"wrapped"`
			Twice    PositiveWrapper `yaml:"twice"`
			Pointer  PositivePointer `yaml:"pointer"`
			Declared declaredDecode  `yaml:"declared"`
			Text     textDecode      `yaml:"text"`
			Both     both            `yaml:"both"`
		}

		tcs := map[string]struct {
			input string
			err   string
		}{
			"embedded value": {
				input: "wrapped: -1\n",
				err:   "1:10: $.wrapped: negative",
			},
			"embedded through another embedded struct": {
				input: "twice: -1\n",
				err:   "1:8: $.twice: negative",
			},
			"nil embedded pointer": {
				input: "wrapped: 1\n",
			},
			"struct that declares its own unmarshaler": {
				input: "declared: -1\n",
			},
			"struct that declares an unmarshaler go-yaml checks later": {
				input: "text: -1\n",
				err:   "1:7: $.text: negative",
			},
			"embedded field beside one with a later unmarshaler": {
				input: "both: -1\n",
				err:   "1:7: $.both: negative",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := yamltest.FirstDocument(t, tc.input).Decode[parent](t.Context())
				if tc.err == "" {
					require.NoError(t, err)

					return
				}

				require.EqualError(t, err, tc.err)
			})
		}
	})

	t.Run("an unexported embedded field validates", func(t *testing.T) {
		t.Parallel()

		type wrapped struct {
			innerPositive
		}

		type parent struct {
			hours

			Wrapped wrapped            `yaml:"wrapped"`
			ByName  map[string]wrapped `yaml:"by_name"`
		}

		tcs := map[string]struct {
			input string
			start parent
			err   string
		}{
			"field go-yaml leaves as it was": {
				input: "wrapped: 1\n",
				start: parent{hours: hours{Open: "09:00", Close: "08:00"}},
				err:   "$.hours.close: closes before it opens",
			},
			"field that decodes its struct": {
				input: "wrapped: -1\n",
				err:   "1:10: $.wrapped: negative",
			},
			"field that decodes a map value": {
				input: "by_name: {a: -1}\n",
				err:   "1:14: $.by_name.a: negative",
			},
			"valid fields": {
				input: "wrapped: 1\nby_name: {a: 1}\n",
				start: parent{hours: hours{Open: "09:00", Close: "17:00"}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got := tc.start

				err := yamltest.FirstDocument(t, tc.input).DecodeInto(t.Context(), &got)
				if tc.err == "" {
					require.NoError(t, err)

					return
				}

				require.EqualError(t, err, tc.err)
			})
		}
	})

	t.Run("an ignored field does not validate", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			hours: {open: "09:00", close: "17:00"}
		`))

		cfg := nested{Ignored: hours{Open: "09:00", Close: "08:00"}}
		require.NoError(t, dd.DecodeInto(t.Context(), &cfg))
	})

	t.Run("children validate before the parent, which runs only when they pass", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			hours: {open: "09:00", close: "08:00"}
			backup: {open: "09:00", close: "17:00"}
		`))

		// The parent reports the child that failed, and nothing of its own.
		_, err := dd.Decode[ordered](t.Context())
		require.EqualError(t, err, "1:31: $.hours.close: closes before it opens")
		require.NotErrorIs(t, err, errOrder)

		dd = yamltest.FirstDocument(t, stringtest.Input(`
			hours: {open: "09:00", close: "17:00"}
			backup: {open: "09:00", close: "17:00"}
		`))

		_, err = dd.Decode[ordered](t.Context())
		require.EqualError(t, err, "1:1: $: parent ran last")
	})

	t.Run("every value that fails is reported", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			hours: {open: "09:00", close: "08:00"}
			backup: {open: "09:00", close: "08:00"}
		`), niceyaml.WithName("cafe.yaml"))

		doc, err := source.Document()
		require.NoError(t, err)

		_, err = doc.Decode[nested](t.Context())
		require.Error(t, err)

		assert.Equal(t, stringtest.JoinLF(
			"|-- cafe.yaml:1:31: $.hours.close: closes before it opens",
			"`-- cafe.yaml:2:32: $.backup.close: closes before it opens",
			"",
			`   1 | hours: {open: "09:00", close: "08:00"}`,
			"     |                               ^^^^^^^ closes before it opens",
			`   2 | backup: {open: "09:00", close: "08:00"}`,
			"     |                                ^^^^^^^ closes before it opens",
		), niceyaml.FormatError(err, 2))
	})

	t.Run("WithSelfValidation false switches the walk off", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			hours: {open: "09:00", close: "08:00"}
		`))

		_, err := dd.Decode[nested](t.Context(), niceyaml.WithSelfValidation(false))
		require.NoError(t, err)
	})

	t.Run("a scoped decode writes paths from the scope", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			spec:
			  items:
			    - price: 1
			    - price: -1
		`), niceyaml.WithName("cafe.yaml"))

		doc, err := source.Document()
		require.NoError(t, err)

		_, err = yamltest.At(t, doc, paths.Root().Child("spec")).Decode[nested](t.Context())
		require.EqualError(t, err, "cafe.yaml:4:14: $.items[1].price: negative price")
	})

	t.Run("a value that refers back to itself walks once", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: ring\n")

		ring := &ring{}
		ring.Next = ring

		require.ErrorIs(t, dd.DecodeInto(t.Context(), ring), errRing)
	})

	t.Run("a value two paths share fails the parent on each", func(t *testing.T) {
		t.Parallel()

		// The alias makes B the same pointer as A. The value reports
		// under the first path alone, and the parent of the second path
		// still does not run, since its child failed.
		type shared struct {
			A *item `yaml:"a"`
			B *item `yaml:"b"`
		}

		dd := yamltest.FirstDocument(t, "a: &x {name: n, price: -1}\nb: *x\n")

		got, err := dd.Decode[shared](t.Context())
		require.Same(t, got.A, got.B)
		require.EqualError(t, err, "1:24: $.a.price: negative price")

		_, err = dd.Decode[sharedParent](t.Context())
		require.EqualError(t, err, "1:24: $.a.price: negative price")
	})

	t.Run("chained aliases walk in linear time", func(t *testing.T) {
		t.Parallel()

		// Every node holds the node before it twice, so a walk that
		// revisits a shared value under every path takes 2^40 steps. The
		// decode turns the alias limit off, which refuses such a document.
		var sb strings.Builder

		sb.WriteString("n0: &n0 {}\n")

		for i := 1; i <= 40; i++ {
			fmt.Fprintf(&sb, "n%d: &n%d {a: *n%d, b: *n%d}\n", i, i, i-1, i-1)
		}

		dd := yamltest.FirstDocument(t, sb.String())

		_, err := dd.Decode[map[string]*chain](t.Context(), niceyaml.WithAliasLimit(false))
		require.NoError(t, err)
	})

	t.Run("distinct empty values each validate", func(t *testing.T) {
		t.Parallel()

		// Every empty slice and every pointer to a zero-size value can
		// share one address, which must not make them one value.
		type withRules struct {
			A rules `yaml:"a"`
			B rules `yaml:"b"`
		}

		dd := yamltest.FirstDocument(t, "a: []\nb: []\n")

		_, err := dd.Decode[withRules](t.Context())
		require.EqualError(t, err, stringtest.JoinLF(
			"$.a: no rules",
			"$.b: no rules",
		))

		type withFlags struct {
			A *flag `yaml:"a"`
			B *flag `yaml:"b"`
		}

		dd = yamltest.FirstDocument(t, "a: {}\nb: {}\n")

		_, err = dd.Decode[withFlags](t.Context())
		require.EqualError(t, err, stringtest.JoinLF(
			"$.a: flag set",
			"$.b: flag set",
		))
	})

	t.Run("a nil slice or map still validates", func(t *testing.T) {
		t.Parallel()

		// Every nil slice or map of one type shares one address, which
		// must not make them one value.
		type withEmpty struct {
			A rules `yaml:"a"`
			B rules `yaml:"b"`
			M names `yaml:"m"`
			N names `yaml:"n"`
		}

		dd := yamltest.FirstDocument(t, "a: null\nm: null\n")

		_, err := dd.Decode[withEmpty](t.Context())
		require.EqualError(t, err, stringtest.JoinLF(
			"$.a: no rules",
			"$.b: no rules",
			"$.m: no names",
			"$.n: no names",
		))

		dd = yamltest.FirstDocument(t, "null\n")

		_, err = dd.Decode[rules](t.Context())
		require.EqualError(t, err, "1:1: $: no rules")
	})

	t.Run("a value that refers back through a map walks once", func(t *testing.T) {
		t.Parallel()

		type withExtra struct {
			Name  string         `yaml:"name"`
			Extra map[string]any `yaml:"extra"`
		}

		extra := map[string]any{}
		extra["self"] = extra

		got := withExtra{Extra: extra}
		dd := yamltest.FirstDocument(t, "name: x\n")

		require.NoError(t, dd.DecodeInto(t.Context(), &got))
	})

	t.Run("a cycle through shared values scans in linear time", func(t *testing.T) {
		t.Parallel()

		// Every mesh links to the next one twice, and the last links back
		// to the first, so a scan that reads a shared value again on every
		// path takes 2^40 steps.
		type withMesh struct {
			Name string `yaml:"name"`
			Mesh *mesh  `yaml:"mesh"`
		}

		tcs := map[string]struct {
			port int
			err  string
		}{
			"no port is set": {
				port: -1,
			},
			"a port at the far end fails": {
				port: 40,
				err: "$.mesh" + strings.Repeat(".next[0]", 40) +
					".port: port out of range",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				nodes := meshChain(40)
				if tc.port >= 0 {
					bad := port(-1)
					nodes[tc.port].Port = &bad
				}

				dd := yamltest.FirstDocument(t, "name: x\n")

				err := dd.DecodeInto(t.Context(), &withMesh{Mesh: nodes[0]})
				if tc.err == "" {
					require.NoError(t, err)

					return
				}

				require.EqualError(t, err, tc.err)
			})
		}
	})

	t.Run("a value that leads back to a failed validator fails", func(t *testing.T) {
		t.Parallel()

		// The loop holds no validator below it but itself, so its scan
		// stops. The stop below it still leads back to the loop, which
		// failed, so the stops that hold it do not run.
		type withLoop struct {
			Loop  loop  `yaml:"loop"`
			Stops stops `yaml:"stops"`
		}

		s := &stop{}
		s.Back = loop{s}

		dd := yamltest.FirstDocument(t, "name: x\n")

		err := dd.DecodeInto(t.Context(), &withLoop{Loop: s.Back, Stops: stops{s}})
		require.ErrorIs(t, err, errLoop)
		assert.NotContains(t, err.Error(), "stops ran")
	})

	t.Run("map entries report in key order", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "d: {price: -1}\nb: {price: -1}\nc: {price: -1}\na: {price: -1}\n")

		want := stringtest.JoinLF(
			"$.a.price: negative price",
			"$.b.price: negative price",
			"$.c.price: negative price",
			"$.d.price: negative price",
		)

		for range 20 {
			_, err := dd.Decode[map[string]item](t.Context())
			require.EqualError(t, err, want)
		}
	})

	t.Run("keys of one text order by the types they hold", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("1: {n: -1}\n\"1\": {n: -2}\n", niceyaml.WithAllowDuplicateKeys(true))

		dd, err := source.Document()
		require.NoError(t, err)

		// The string key sorts before the uint64 key.
		want := stringtest.JoinLF(
			"$.1.n: negative -2",
			"$.1.n: negative -1",
		)

		for range 20 {
			_, err = dd.Decode[map[any]signed](t.Context())
			require.EqualError(t, err, want)
		}
	})

	t.Run("a map key reports the text the document spells it with", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			want  string
		}{
			"float with a trailing zero": {
				input: "1.50: {price: -1}\n",
				want:  "1:15: $.'1.50'.price: negative price",
			},
			"hexadecimal int": {
				input: "0x10: {price: -1}\n",
				want:  "1:15: $.0x10.price: negative price",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				_, err := dd.Decode[map[float64]item](t.Context())
				require.EqualError(t, err, tc.want)
			})
		}

		// An alias key reports the content of its anchor, and a block
		// scalar key reports its content rather than its indicator.
		type named struct {
			M map[string]item    `yaml:"m"`
			F map[float64]item   `yaml:"f"`
			T map[time.Time]item `yaml:"t"`
		}

		namedTcs := map[string]struct {
			input string
			want  string
		}{
			"alias": {
				input: "base: &k n\nm:\n  *k : {price: -1}\n",
				want:  "3:16: $.m.n.price: negative price",
			},
			"alias to a hexadecimal int": {
				input: "base: &k 0x10\nf:\n  *k : {price: -1}\n",
				want:  "3:16: $.f.0x10.price: negative price",
			},
			"alias to a float with a trailing zero": {
				input: "base: &k 1.50\nf:\n  *k : {price: -1}\n",
				want:  "3:16: $.f.'1.50'.price: negative price",
			},
			"string alias to a hexadecimal int": {
				input: "base: &k 0x10\nm:\n  *k : {price: -1}\n",
				want:  "3:16: $.m.0x10.price: negative price",
			},
			"block scalar": {
				input: "m:\n  ? |-\n    n\n  : {price: -1}\n",
				want:  "4:13: $.m.n.price: negative price",
			},
			"time with an offset of whole hours": {
				input: "t:\n  2024-01-01T00:00:00+05:00: {price: -1}\n",
				want:  "2:38: $.t.'2024-01-01T00:00:00+05:00'.price: negative price",
			},
			"time with an offset of part of an hour": {
				input: "t:\n  2024-01-01T00:00:00+05:30: {price: -1}\n",
				want:  "2:38: $.t.'2024-01-01T00:00:00+05:30'.price: negative price",
			},
		}

		for name, tc := range namedTcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				_, err := dd.Decode[named](t.Context())
				require.EqualError(t, err, tc.want)
			})
		}

		// A key a merge brings in reports the text of the mapping it
		// comes from.
		dd := yamltest.FirstDocument(t, "base: &b {0x10: {price: -1}}\nm: {<<: *b}\n")

		_, err := dd.Decode[map[string]map[float64]item](t.Context())

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		var got []string

		for _, child := range bound.Errors() {
			got = append(got, child.Error())
		}

		assert.Equal(t, []string{
			"1:25: $.base.0x10.price: negative price",
			"1:25: $.m.0x10.price: negative price",
		}, got)

		// Keys an alias to a list of sources or a second merge key brings
		// in report the text of their own mappings too.
		type merged struct {
			M map[float64]item `yaml:"m"`
		}

		mergeTcs := map[string]struct {
			input string
			opts  []niceyaml.SourceOption
		}{
			"alias to a sequence": {
				input: "a: &a {0x10: {price: -1}}\nb: &b {0x20: {price: -1}}\nl: &l [*a, *b]\nm: {<<: *l}\n",
			},
			"repeated merge keys": {
				input: "a: &a {0x10: {price: -1}}\nb: &b {0x20: {price: -1}}\nm: {<<: *a, <<: *b}\n",
				opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			},
		}

		for name, tc := range mergeTcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd, err := niceyaml.NewSourceFromString(tc.input, tc.opts...).Document()
				require.NoError(t, err)

				_, err = dd.Decode[merged](t.Context())

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)

				var got []string

				for _, child := range bound.Errors() {
					got = append(got, child.Error())
				}

				assert.Equal(t, []string{
					"1:22: $.m.0x10.price: negative price",
					"2:22: $.m.0x20.price: negative price",
				}, got)
			})
		}

		// Where a key of the mapping and a merged key decode to the same
		// value, the later entry holds the value the decode keeps, and
		// the error reports its text.
		precedenceTcs := map[string]struct {
			input string
			want  string
		}{
			"merge after a key of the mapping": {
				input: "b: &b {0x10: {price: -1}}\nm: {16: {price: 1}, <<: *b}\n",
				want:  "1:22: $.m.0x10.price: negative price",
			},
			"key of the mapping after a merge": {
				input: "b: &b {0x10: {price: 1}}\nm: {<<: *b, 16: {price: -1}}\n",
				want:  "2:25: $.m.16.price: negative price",
			},
		}

		for name, tc := range precedenceTcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				_, err := dd.Decode[merged](t.Context())
				require.EqualError(t, err, tc.want)
			})
		}
	})

	t.Run("a map key validates at the key", func(t *testing.T) {
		t.Parallel()

		type byPort struct {
			M map[port]string `yaml:"m"`
		}

		type portsByPort struct {
			M map[port]port `yaml:"m"`
		}

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			input  string
			want   []string
		}{
			"top-level map": {
				input: "70000: x\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[map[port]string](ctx)

					return err
				},
				want: []string{"1:1: $.70000~: port out of range"},
			},
			"map in a field": {
				input: "m:\n  70000: x\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[byPort](ctx)

					return err
				},
				want: []string{"2:3: $.m.70000~: port out of range"},
			},
			"key and value both fail": {
				input: "m:\n  70001: 70002\n  70000: 1\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[portsByPort](ctx)

					return err
				},
				want: []string{
					"3:3: $.m.70000~: port out of range",
					"2:3: $.m.70001~: port out of range",
					"2:10: $.m.70001: port out of range",
				},
			},
			"valid key": {
				input: "80: x\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[map[port]string](ctx)

					return err
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				if tc.want == nil {
					require.NoError(t, err)

					return
				}

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)

				got := []string{err.Error()}
				if children := bound.Errors(); len(children) > 0 {
					got = got[:0]

					for _, child := range children {
						got = append(got, child.Error())
					}
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("an entry under a NaN key validates", func(t *testing.T) {
		t.Parallel()

		// NaN equals no value, itself included, so no lookup by the key
		// finds its entry.
		tcs := map[string]struct {
			target any
		}{
			"float key": {
				target: &map[float64]item{},
			},
			"key behind an interface": {
				target: &map[any]item{},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, ".nan: {price: -1}\n1.5: {price: -1}\n")

				err := dd.DecodeInto(t.Context(), tc.target)

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)

				var got []string

				for _, child := range bound.Errors() {
					got = append(got, child.Error())
				}

				assert.Equal(t, []string{
					"1:15: $.'.nan'.price: negative price",
					"2:14: $.'1.5'.price: negative price",
				}, got)
			})
		}
	})

	t.Run("several NaN keys report no position and come back in one order", func(t *testing.T) {
		t.Parallel()

		// A map holds every NaN key the document spells, and no name
		// tells them apart, so no error takes the line of one key.
		tcs := map[string]struct {
			target func() any
		}{
			"float key": {
				target: func() any { return &map[float64]signed{} },
			},
			"key behind an interface": {
				target: func() any { return &map[any]signed{} },
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, ".nan: {n: -2}\n.NaN: {n: -1}\n")

				// The map iterates in a new order on each decode.
				for range 20 {
					err := dd.DecodeInto(t.Context(), tc.target())

					var bound *niceyaml.SourceError

					require.ErrorAs(t, err, &bound)

					var got []string

					for _, child := range bound.Errors() {
						got = append(got, child.Error())
					}

					assert.Equal(t, []string{
						"$.NaN.n: negative -1",
						"$.NaN.n: negative -2",
					}, got)
				}
			})
		}
	})

	t.Run("several NaN keys order a value that refers to itself", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "{}\n")

		cyc := map[string]any{}
		cyc["self"] = cyc

		// The document leaves the map as the caller filled it.
		target := struct {
			M map[float64]any `yaml:"m"`
		}{M: map[float64]any{
			math.NaN(): cyc,
			math.NaN(): signed{N: -1},
		}}

		err := dd.DecodeInto(t.Context(), &target)
		require.EqualError(t, err, "$.m.NaN.n: negative -1")
	})

	t.Run("time keys of one instant and zone report no position", func(t *testing.T) {
		t.Parallel()

		// Each key decodes to its own location, so the map holds both,
		// and no name tells them apart.
		dd := yamltest.FirstDocument(t, stringtest.Input(`
			2024-01-01T00:00:00+05:30: {price: -1}
			2024-01-01T00:00:00.0+05:30: {price: -2}
		`))

		_, err := dd.Decode[map[time.Time]item](t.Context())

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		var got []string

		for _, child := range bound.Errors() {
			got = append(got, child.Error())
		}

		assert.Equal(t, []string{
			"$.'2024-01-01 00:00:00 +0530 +0530'.price: negative price",
			"$.'2024-01-01 00:00:00 +0530 +0530'.price: negative price",
		}, got)
	})

	t.Run("a key below a wide map reports the text the document spells it with", func(t *testing.T) {
		t.Parallel()

		// The walk reads the keys of every map below the wide one from the
		// document, and each of those maps sits among 2000 siblings.
		var sb strings.Builder

		for i := range 2000 {
			fmt.Fprintf(&sb, "k%d: {1: {price: 1}}\n", i)
		}

		sb.WriteString("last: {0x10: {price: -1}}\n")

		dd := yamltest.FirstDocument(t, sb.String())

		_, err := dd.Decode[map[string]map[float64]item](t.Context())
		require.EqualError(t, err, "2001:22: $.last.0x10.price: negative price")
	})

	t.Run("the keys of every map decode with one go-yaml decoder", func(t *testing.T) {
		t.Parallel()

		// A go-yaml decoder applies its options once, when it first
		// decodes, and reads any reference files then too.
		var sb strings.Builder

		for i := range 50 {
			fmt.Fprintf(&sb, "k%d: {1: {price: 1}}\n", i)
		}

		dd := yamltest.FirstDocument(t, sb.String())

		decoders := 0
		count := yaml.DecodeOption(func(*yaml.Decoder) error {
			decoders++

			return nil
		})

		_, err := dd.Decode[map[string]map[float64]item](t.Context(), niceyaml.WithYAMLDecodeOptions(count))
		require.NoError(t, err)

		// One decoder decodes the value, and one reads its keys.
		assert.LessOrEqual(t, decoders, 2)
	})

	t.Run("a key decodes with the context of the decode", func(t *testing.T) {
		t.Parallel()

		type spaced struct {
			M map[spacedKey]item `yaml:"m"`
		}

		ctx := context.WithValue(t.Context(), spaceCtxKey{}, "prod")

		tcs := map[string]struct {
			input string
			want  map[spacedKey]item
			err   string
		}{
			"passes": {
				input: "m:\n  web: {price: 1}\n",
				want:  map[spacedKey]item{"prod/web": {Price: 1}},
			},
			"fails": {
				input: "m:\n  web: {price: 1}\n  db: {price: -1}\n",
				err:   "3:15: $.m.db.price: negative price",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := dd.Decode[spaced](ctx)
				if tc.err != "" {
					require.EqualError(t, err, tc.err)

					return
				}

				require.NoError(t, err)
				assert.Equal(t, tc.want, got.M)
			})
		}
	})

	t.Run("a key whose second decode panics reports the entry", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "db: {price: -1}\n")

		ctx := context.WithValue(t.Context(), onceCtxKey{}, new(atomic.Int32))

		_, err := dd.Decode[map[onceKey]item](ctx)
		require.EqualError(t, err, "1:13: $.db.price: negative price")
	})

	t.Run("a map whose values hold no validator reads no keys", func(t *testing.T) {
		t.Parallel()

		// No entry below the map can fail, so no error needs the key of an
		// entry.
		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node, opts ...niceyaml.DecodeOption) error
			input  string
		}{
			// The map validates itself, and its type holds no validator
			// below it.
			"map that validates itself": {
				input: "a: x\nb: y\n",
				decode: func(ctx context.Context, dd *niceyaml.Node, opts ...niceyaml.DecodeOption) error {
					_, err := dd.Decode[labels](ctx, opts...)

					return err
				},
			},
			// Each map validates itself, and the values go-yaml decodes into
			// its interface values hold no validator.
			"list of maps that validate themselves": {
				input: "- {a: 1}\n- {b: {c: 1}}\n",
				decode: func(ctx context.Context, dd *niceyaml.Node, opts ...niceyaml.DecodeOption) error {
					_, err := dd.Decode[[]anyLabels](ctx, opts...)

					return err
				},
			},
			// The type of the values may hold a validator, but the values
			// go-yaml decodes into an interface hold none.
			"map of any": {
				input: "a: {b: 1}\nc: [{d: 1}]\n",
				decode: func(ctx context.Context, dd *niceyaml.Node, opts ...niceyaml.DecodeOption) error {
					_, err := dd.Decode[map[string]any](ctx, opts...)

					return err
				},
			},
			"any": {
				input: "- {a: 1}\n- {b: {c: 1}}\n",
				decode: func(ctx context.Context, dd *niceyaml.Node, opts ...niceyaml.DecodeOption) error {
					_, err := dd.Decode[any](ctx, opts...)

					return err
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				decoders := 0
				count := yaml.DecodeOption(func(*yaml.Decoder) error {
					decoders++

					return nil
				})

				require.NoError(t, tc.decode(t.Context(), dd, niceyaml.WithYAMLDecodeOptions(count)))

				// One decoder decodes the value, and none reads its keys.
				assert.Equal(t, 1, decoders)
			})
		}
	})

	t.Run("a leaf type validates itself", func(t *testing.T) {
		t.Parallel()

		type withPort struct {
			Ports []port `yaml:"ports"`
		}

		dd := yamltest.FirstDocument(t, "ports: [80, 70000]\n")

		_, err := dd.Decode[withPort](t.Context())
		require.EqualError(t, err, "1:13: $.ports[1]: port out of range")
	})

	t.Run("an unlocated error points at the value that owns it", func(t *testing.T) {
		t.Parallel()

		// The decode root is a value like any other, so its Validate
		// reports at its own node as a field does.
		type withPort struct {
			Port port `yaml:"port"`
		}

		dd := yamltest.FirstDocument(t, "port: 70000\n")

		_, err := dd.Decode[withPort](t.Context())
		require.EqualError(t, err, "1:7: $.port: port out of range")

		scoped := yamltest.At(t, dd, paths.Root().Child("port"))

		_, err = scoped.Decode[port](t.Context())
		require.EqualError(t, err, "1:7: $: port out of range")
	})

	t.Run("a byte-sized leaf type validates itself", func(t *testing.T) {
		t.Parallel()

		type withLevels struct {
			Levels []level `yaml:"levels"`
		}

		type withFixed struct {
			Fixed [2]level `yaml:"fixed"`
		}

		type withData struct {
			Data []byte `yaml:"data"`
		}

		tcs := map[string]struct {
			target any
			input  string
			want   string
		}{
			"slice": {
				target: &withLevels{},
				input:  "levels: [1, 9]\n",
				want:   "1:13: $.levels[1]: level above 5",
			},
			"array": {
				target: &withFixed{},
				input:  "fixed: [1, 9]\n",
				want:   "1:12: $.fixed[1]: level above 5",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := dd.DecodeInto(t.Context(), tc.target)
				require.EqualError(t, err, tc.want)
			})
		}

		// The bytes of a plain []byte hold nothing to validate.
		dd := yamltest.FirstDocument(t, "data: [1, 9]\n")

		_, err := dd.Decode[withData](t.Context())
		require.NoError(t, err)
	})

	t.Run("a value validates whatever its type holds below it", func(t *testing.T) {
		t.Parallel()

		type withLabels struct {
			Labels labels `yaml:"labels"`
		}

		type withExtra struct {
			Name  string         `yaml:"name"`
			Extra map[string]any `yaml:"extra"`
		}

		loop := map[string]any{"v": signed{N: -1}}
		loop["self"] = map[string]any{"back": loop}

		tcs := map[string]struct {
			target any
			input  string
			want   string
		}{
			"map of strings": {
				target: &labels{},
				input:  "a: x\nb: \"\"\n",
				want:   "2:4: $.b: empty label",
			},
			"map of strings behind a field": {
				target: &withLabels{},
				input:  "labels: {a: x, b: \"\"}\n",
				want:   "1:19: $.labels.b: empty label",
			},
			"map of strings in a map of any the caller filled": {
				target: &withExtra{Extra: map[string]any{"l": labels{"b": ""}}},
				input:  "name: x\n",
				want:   "$.extra.l.b: empty label",
			},
			"validator deep in a map of any the caller filled": {
				target: &withExtra{Extra: map[string]any{
					"list": []any{map[string]any{"a": 1}, map[string]any{"s": signed{N: -1}}},
					"z":    map[string]any{"b": map[string]any{"c": 2}},
				}},
				input: "name: x\n",
				want:  "$.extra.list[1].s.n: negative -1",
			},
			"validator beside a map of any that holds itself": {
				target: &withExtra{Extra: loop},
				input:  "name: x\n",
				want:   "$.extra.v.n: negative -1",
			},
			"type that holds itself": {
				target: &tree{},
				input:  "kids:\n  - port: 70000\nport: 1\n",
				want:   "2:11: $.kids[0].port: port out of range",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := dd.DecodeInto(t.Context(), tc.target)
				require.EqualError(t, err, tc.want)
			})
		}
	})

	t.Run("a join of nil pointers fails at every depth", func(t *testing.T) {
		t.Parallel()

		// The join holds two nil pointers, which is not a nil error, so
		// the value fails at the root and under a field alike, and the
		// parent of the field does not run.
		dd := yamltest.FirstDocument(t, "open: x\n")

		_, err := dd.Decode[nothingJoined](t.Context())
		require.Error(t, err)

		dd = yamltest.FirstDocument(t, "hours:\n  open: x\n")

		_, err = dd.Decode[withNothingJoined](t.Context())
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "parent ran")

		var e *niceyaml.Error

		require.ErrorAs(t, err, &e)

		p, ok := e.Path()
		require.True(t, ok)
		assert.Equal(t, "$.hours", p.String())
	})

	t.Run("a value that decodes itself validates itself alone", func(t *testing.T) {
		t.Parallel()

		type withSelfDecoding struct {
			Bytes selfDecodingBytes `yaml:"bytes"`
			Text  selfDecodingText  `yaml:"text"`
		}

		dd := yamltest.FirstDocument(t, "bytes: anything\ntext: anything\n")

		var got withSelfDecoding

		// Each unmarshaler fills its hours with a close before its open,
		// which no Validate reports, since the walk stops at a value that
		// decodes itself. Each value still validates itself.
		require.NoError(t, dd.DecodeInto(t.Context(), &got))
		assert.True(t, got.Bytes.validated)
		assert.True(t, got.Text.validated)
		assert.Equal(t, hours{Open: "17:00", Close: "09:00"}, got.Bytes.Inner)
		assert.Equal(t, hours{Open: "17:00", Close: "09:00"}, got.Text.Inner)

		type withNestedFailure struct {
			Bytes failingSelfDecoding `yaml:"bytes"`
		}

		_, err := dd.Decode[withNestedFailure](t.Context())
		require.EqualError(t, err, "1:8: $.bytes: rejected")
	})

	t.Run("an ast.Node value validates itself alone", func(t *testing.T) {
		t.Parallel()

		type withSpec struct {
			Hours *seenHours `yaml:"hours"`
			Spec  ast.Node   `yaml:"spec"`
		}

		// Go-yaml sets an ast.Node to the node itself, whose tokens link to
		// every other token of the file, so the walk stops at the node
		// rather than read the keys after it.
		var sb strings.Builder

		sb.WriteString("hours: {open: \"09:00\", close: \"17:00\"}\nspec:\n  a: 1\n")

		for i := range 200 {
			fmt.Fprintf(&sb, "k%d: v\n", i)
		}

		dd := yamltest.FirstDocument(t, sb.String())

		got, err := dd.Decode[withSpec](t.Context())
		require.NoError(t, err)
		require.NotNil(t, got.Hours)
		assert.True(t, got.Hours.seen)
		assert.IsType(t, &ast.MappingNode{}, got.Spec)

		// The walk reaches no value below a node, such as a validator a
		// tree built by hand holds.
		dd = yamltest.FirstDocument(t, "hours: {open: \"09:00\", close: \"17:00\"}\n")

		built := withSpec{Spec: &ast.MappingNode{
			Values: []*ast.MappingValueNode{{Value: walkedNode{}}},
		}}

		require.NoError(t, dd.DecodeInto(t.Context(), &built))
	})
}

// selfDecodingBytes decodes itself from the YAML bytes, so its fields
// need not mirror the document. It fills Inner with hours that close
// before they open, which the walk must not report.
type selfDecodingBytes struct {
	Inner     hours
	validated bool
}

func (s *selfDecodingBytes) UnmarshalYAML([]byte) error {
	s.Inner = hours{Open: "17:00", Close: "09:00"}

	return nil
}

func (s *selfDecodingBytes) Validate() error {
	s.validated = true

	return nil
}

// selfDecodingText is selfDecodingBytes through encoding.TextUnmarshaler.
type selfDecodingText struct {
	Inner     hours
	validated bool
}

func (s *selfDecodingText) UnmarshalText([]byte) error {
	s.Inner = hours{Open: "17:00", Close: "09:00"}

	return nil
}

func (s *selfDecodingText) Validate() error {
	s.validated = true

	return nil
}

// failingSelfDecoding decodes itself and rejects itself, so its own
// Validate still runs at its own path.
type failingSelfDecoding struct {
	Inner hours
}

func (f *failingSelfDecoding) UnmarshalYAML([]byte) error {
	f.Inner = hours{Open: "17:00", Close: "09:00"}

	return nil
}

func (failingSelfDecoding) Validate() error {
	return niceyaml.NewError("rejected")
}

// walkedNode is a syntax tree node that reports any walk that reaches it.
type walkedNode struct {
	ast.Node
}

func (walkedNode) Validate() error {
	return errors.New("walked below an ast.Node")
}

// nothingJoined validates itself with a join of two nil pointers, which
// is not a nil error.
type nothingJoined struct {
	Open string `yaml:"open"`
}

func (nothingJoined) Validate() error {
	var openErr, closeErr *niceyaml.Error

	return errors.Join(openErr, closeErr)
}

// withNothingJoined holds a nothingJoined, and reports that it ran.
type withNothingJoined struct {
	Hours nothingJoined `yaml:"hours"`
}

func (withNothingJoined) Validate() error {
	return errors.New("parent ran")
}

// ordered validates after its fields, and reports errOrder when a field
// had not run yet.
type ordered struct {
	Hours  hours      `yaml:"hours"`
	Backup *seenHours `yaml:"backup"`
}

func (o ordered) Validate() error {
	if o.Backup != nil && !o.Backup.seen {
		return errOrder
	}

	return errors.New("parent ran last")
}

// seenHours records that its Validate ran.
type seenHours struct {
	Hours `yaml:",inline"`

	seen bool
}

func (h *seenHours) Validate() error {
	h.seen = true

	return h.Hours.Validate()
}

// sharedParent validates after its fields, which an alias makes one
// pointer, and reports that it ran.
type sharedParent struct {
	A *item `yaml:"a"`
	B *item `yaml:"b"`
}

func (sharedParent) Validate() error {
	return errors.New("parent ran")
}

// chain holds two links to the node before it.
type chain struct {
	A *chain `yaml:"a"`
	B *chain `yaml:"b"`
}

// ring points at itself once decoded, and reports errRing from each
// Validate that runs.
type ring struct {
	Name string `yaml:"name"`
	Next *ring  `yaml:"next"`
}

func (r *ring) Validate() error {
	return errRing
}

// mesh links to other meshes, and holds a port.
type mesh struct {
	Next []*mesh `yaml:"next"`
	Port *port   `yaml:"port"`
}

// meshChain returns the meshes n0 to nk, where each mesh links to the
// next one twice, and nk links back to n0.
func meshChain(k int) []*mesh {
	nodes := make([]*mesh, k+1)
	for i := range nodes {
		nodes[i] = &mesh{}
	}

	for i := range k {
		nodes[i].Next = []*mesh{nodes[i+1], nodes[i+1]}
	}

	nodes[k].Next = []*mesh{nodes[0]}

	return nodes
}

// loop is a list of stops that fails.
type loop []*stop

func (loop) Validate() error {
	return errLoop
}

// stop leads back to a loop.
type stop struct {
	Back loop `yaml:"back"`
}

// stops is a list of stops that reports that it ran.
type stops []*stop

func (stops) Validate() error {
	return errors.New("stops ran")
}

// port is a scalar that validates itself.
type port int

func (p port) Validate() error {
	if p < 0 || p > 65535 {
		return fmt.Errorf("port out of range")
	}

	return nil
}

// positive is a scalar that requires a value above zero.
type positive int

func (p positive) Validate() error {
	if p <= 0 {
		return errors.New("must be positive")
	}

	return nil
}

// level is a byte-sized scalar that validates itself.
type level uint8

func (l level) Validate() error {
	if l > 5 {
		return niceyaml.NewError("level above 5")
	}

	return nil
}

// rules is a list that must not be empty.
type rules []int

func (r rules) Validate() error {
	if len(r) == 0 {
		return niceyaml.NewError("no rules")
	}

	return nil
}

// names is a map that must not be empty.
type names map[string]int

func (n names) Validate() error {
	if len(n) == 0 {
		return niceyaml.NewError("no names")
	}

	return nil
}

// labels is a map of strings with no empty value, which holds no
// validator below it.
type anyLabels map[string]any

func (anyLabels) Validate() error {
	return nil
}

type labels map[string]string

func (l labels) Validate() error {
	for k, v := range l {
		if v == "" {
			return niceyaml.NewError("empty label", niceyaml.AtPath(paths.Root().Child(k)))
		}
	}

	return nil
}

// tree holds trees like itself, and a port that validates itself.
type tree struct {
	Kids []tree `yaml:"kids"`
	Port port   `yaml:"port"`
}

// flag is a zero-size value that always reports itself.
type flag struct{}

func (flag) Validate() error {
	return niceyaml.NewError("flag set")
}

// spaceCtxKey keys the namespace a [spacedKey] reads from the context.
type spaceCtxKey struct{}

// spacedKey prefixes its text with the namespace the context of the
// decode holds, and panics when the context holds none.
type spacedKey string

func (k *spacedKey) UnmarshalYAML(ctx context.Context, b []byte) error {
	ns, ok := ctx.Value(spaceCtxKey{}).(string)
	if !ok {
		panic("no namespace in the context")
	}

	*k = spacedKey(ns + "/" + strings.TrimSpace(string(b)))

	return nil
}

// onceCtxKey keys the counter an [onceKey] reads from the context.
type onceCtxKey struct{}

// onceKey decodes its text once per counter in the context, and panics
// when it decodes again.
type onceKey string

func (k *onceKey) UnmarshalYAML(ctx context.Context, b []byte) error {
	calls, ok := ctx.Value(onceCtxKey{}).(*atomic.Int32)
	if !ok || calls.Add(1) > 1 {
		panic("decoded twice")
	}

	*k = onceKey(strings.TrimSpace(string(b)))

	return nil
}
