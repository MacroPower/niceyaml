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

var (
	// The error a value reports when its children ran after it.
	errOrder = errors.New("children ran after the parent")
	// The error every Validate of a ring reports.
	errRing = errors.New("ring")
)

// Hours is hours under an exported name, for embedding without a tag.
type Hours = hours

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
		require.EqualError(t, err, "parent ran last")
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

	t.Run("a leaf type validates itself", func(t *testing.T) {
		t.Parallel()

		type withPort struct {
			Ports []port `yaml:"ports"`
		}

		dd := yamltest.FirstDocument(t, "ports: [80, 70000]\n")

		_, err := dd.Decode[withPort](t.Context())
		require.EqualError(t, err, "1:13: $.ports[1]: port out of range")
	})
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

// ring points at itself once decoded, and reports errRing from each
// Validate that runs.
type ring struct {
	Name string `yaml:"name"`
	Next *ring  `yaml:"next"`
}

func (r *ring) Validate() error {
	return errRing
}

// port is a scalar that validates itself.
type port int

func (p port) Validate() error {
	if p < 0 || p > 65535 {
		return fmt.Errorf("port out of range")
	}

	return nil
}
