package niceyaml_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/jsonschema"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/schema"
)

// hours validates itself with an `@` path that reads from the hours.
type hours struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (h hours) Validate() error {
	if h.Close < h.Open {
		return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Current().Child("close")))
	}

	return nil
}

// wrappedHours validates itself through the check of hours, and adds
// context around the located error that check returns.
type wrappedHours struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (h wrappedHours) Validate() error {
	err := hours(h).Validate()
	if err != nil {
		return fmt.Errorf("hours check: %w", err)
	}

	return nil
}

// anchoredOpen validates itself with an error at a path whose anchor its
// open field names: the `$` path of the root key name for "doc", and the
// `@` path of its own open field otherwise.
type anchoredOpen struct {
	Open string `yaml:"open"`
}

func (a anchoredOpen) Validate() error {
	path := paths.Current().Child("open")
	if a.Open == "doc" {
		path = paths.Doc().Child("name")
	}

	return niceyaml.NewError("bad open", niceyaml.AtPath(path))
}

// schedule validates itself with a summary of its violations, each with an
// `@` path that reads from the schedule.
type schedule struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (s schedule) Validate() error {
	var errs []error

	if s.Open == "" {
		errs = append(errs, niceyaml.NewError("open is empty", niceyaml.AtPath(paths.Current().Child("open"))))
	}

	if s.Close == "" {
		errs = append(errs, niceyaml.NewError("close is empty", niceyaml.AtPath(paths.Current().Child("close"))))
	}

	return niceyaml.NewSummary("invalid schedule", errs...)
}

// conflictingSchedule validates itself with one problem that carries no
// location and names the values in conflict as details, each with an `@`
// path that reads from the schedule.
type conflictingSchedule struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (conflictingSchedule) Validate() error {
	return niceyaml.NewError("schedule conflicts", niceyaml.WithDetails(
		niceyaml.NewError("opens here", niceyaml.AtPath(paths.Current().Child("open"))),
		niceyaml.NewError("closes here", niceyaml.AtPath(paths.Current().Child("close"))),
	))
}

// item validates itself through a pointer receiver.
type item struct {
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

func (it *item) Validate() error {
	if it.Price < 0 {
		return niceyaml.NewError("negative price", niceyaml.AtPath(paths.Current().Child("price")))
	}

	return nil
}

// signed validates itself with a message that names its value.
type signed struct {
	N int `yaml:"n"`
}

func (s signed) Validate() error {
	if s.N < 0 {
		return niceyaml.NewError(fmt.Sprintf("negative %d", s.N), niceyaml.AtPath(paths.Current().Child("n")))
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

	// Decodes a grade from its name, the only form a document gives it.
	gradeNames = niceyaml.WithCustomUnmarshaler(func(_ context.Context, g *grade, decode func(any) error) error {
		var name string

		err := decode(&name)
		if err != nil {
			return err
		}

		if name != "high" {
			return fmt.Errorf("unknown grade %q", name)
		}

		*g = gradeHigh

		return nil
	})

	// The documents the locator tests share.
	// The key 0x10 sets the member 16, and items holds an element.
	respelledDoc = stringtest.LinesLF(
		"ports:",
		"  0x10:",
		`    name: ""`,
		"items:",
		`  - name: ""`,
	)
	// The mapping under 0x10 holds no name.
	lackingDoc = stringtest.LinesLF(
		"ports:",
		"  0x10:",
		"    proto: tcp",
	)
	// The key 3.10 sets the member 3.1, so no member has the name 3.10.
	pythonDoc = stringtest.LinesLF(
		"python:",
		"  3.10:",
		"    image: a",
	)
	// Both keys of m set the member 16, and a decode keeps b.
	sameNameDoc = stringtest.LinesLF(
		"m:",
		"  16: aaaa",
		"  0x10: b",
	)
	// The merge key sets the member 16 to x. The quoted key sets the
	// member 0x10, and a path through 0x10 selects that entry.
	hiddenDoc = stringtest.LinesLF(
		"user:",
		"  <<: {0x10: x}",
		`  "0x10": 1`,
	)
	// The merge key of m sets the member true to b, the last value b1
	// gives that name.
	mergeDoc = stringtest.LinesLF(
		"b0: &b0",
		"  True: xxxx",
		"b1: &b1",
		"  <<: *b0",
		"  true: aaaa",
		"  True: b",
		"m:",
		"  <<: *b1",
	)
	// A sequence, a mapping with keys of digits, an alias to the
	// sequence, a sequence in a sequence, a scalar, and a null.
	shapesDoc = stringtest.LinesLF(
		"seq: &s",
		"  - a",
		"  - b",
		"map:",
		"  0: x",
		"  1: y",
		"al: *s",
		"nested:",
		"  - - p",
		"    - q",
		"scalar: v",
		"none: ~",
	)
	// The anchors a document of the tests refers to through
	// [niceyaml.WithReferences].
	referenceDoc = stringtest.LinesLF(
		"defaults: &defaults",
		"  timeout: 0",
		"  3.10: r",
		"list: &list [u, {0x10: v}]",
	)
	// The mapping server merges an anchor of referenceDoc, and items is
	// an alias to one.
	referringDoc = stringtest.LinesLF(
		"server:",
		"  <<: *defaults",
		"  port: 0x10",
		"  3.10: mine",
		"items: *list",
	)
	// The tagged alias key of python has no name the locator can tell, so
	// it may set a member of any name, such as one a key before it sets.
	unnamedKeyDoc = stringtest.LinesLF(
		"k: &k 0x10",
		"python:",
		"  3.10: {image: a}",
		"  a: 1",
		"  !!str *k : v",
		"  b: 2",
	)
	// The key a/b~c holds both characters a JSON Pointer escapes, and the
	// empty key takes an empty name.
	pointerDoc = stringtest.LinesLF(
		`"a/b~c":`,
		`  "": x`,
		"items:",
		"  - 0x10: y",
	)
)

// countStrings returns an option whose function decodes every string of
// a decode into a typed value, the keys of its maps among them, and
// counts its calls in n.
func countStrings(n *int) niceyaml.DecodeOption {
	return niceyaml.WithCustomUnmarshaler(func(_ context.Context, s *string, decode func(any) error) error {
		// The function decodes a string, so its decode reads a second
		// type that the function does not decode.
		type text string

		*n++

		return decode((*text)(s))
	})
}

// Hours is hours under an exported name, for embedding without a tag.
type Hours = hours

// Named requires a name, for embedding.
type Named struct {
	Name string `yaml:"name"`
}

func (n Named) Validate() error {
	if n.Name == "" {
		return niceyaml.NewError("name required", niceyaml.AtPath(paths.Current().Child("name")))
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

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			hours:
			  open: "09:00"
			  close: "08:00"
		`), niceyaml.WithName("cafe.yaml"))

		_, err := doc.Decode[nested](t.Context())
		require.EqualError(t, err, "cafe.yaml:3:10: $.hours.close: closes before it opens")

		assert.Equal(t, "$.hours.close", errorPath(t, errorIn(t, err)).String())
	})

	t.Run("context a Validate adds around a located error follows the path", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			hours:
			  open: "09:00"
			  close: "08:00"
		`), niceyaml.WithName("cafe.yaml"))

		_, err := doc.Decode[struct {
			Hours wrappedHours `yaml:"hours"`
		}](t.Context())

		// The wrapper holds the message alone, so the decode names the
		// joined path once, in front of the context.
		require.EqualError(t, err, "cafe.yaml:3:10: $.hours.close: hours check: closes before it opens")

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, "hours check: closes before it opens", bound.Message())

		assert.Equal(t, "$.hours.close", errorPath(t, bound).String())

		// The Validate alone returns the message with no path in it, and
		// FormatError reads the path from the Error it wraps.
		unbound := wrappedHours{Open: "09:00", Close: "08:00"}.Validate()
		require.EqualError(t, unbound, "hours check: closes before it opens")
		assert.Equal(
			t,
			"@.close: hours check: closes before it opens",
			niceyaml.FormatError(unbound, niceyaml.WithContextLines(0)),
		)
	})

	t.Run("the errors a summary under a field heads report the joined path", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			schedule:
			  open: ""
			  close: ""
		`), niceyaml.WithName("cafe.yaml"))

		_, err := doc.Decode[struct {
			Schedule schedule `yaml:"schedule"`
		}](t.Context())
		// The summary is a heading, so the decode rebases it under the field
		// without giving it a location.
		require.EqualError(t, err, stringtest.JoinLF(
			"cafe.yaml: invalid schedule",
			"cafe.yaml:2:9: $.schedule.open: open is empty",
			"cafe.yaml:3:10: $.schedule.close: close is empty",
		))

		bound := yamltest.RequireSourceError(t, err)
		require.Len(t, yamltest.ProblemsOf(bound), 2)

		assert.Equal(t, "cafe.yaml:2:9: $.schedule.open: open is empty", yamltest.ProblemsOf(bound)[0].Bound.Error())
		assert.Equal(t, "cafe.yaml:3:10: $.schedule.close: close is empty", yamltest.ProblemsOf(bound)[1].Bound.Error())

		assert.Equal(t, "$.schedule.close", errorPath(t, yamltest.ProblemsOf(bound)[1]).String())

		assert.Equal(t, stringtest.JoinLF(
			"cafe.yaml: invalid schedule",
			"|-- 2:9: $.schedule.open: open is empty",
			"`-- 3:10: $.schedule.close: close is empty",
			"",
			"   1 | schedule:",
			`   2 |   open: ""`,
			"     |         ^^ open is empty",
			`   3 |   close: ""`,
			"     |          ^^ close is empty",
		), niceyaml.FormatError(err))
	})

	t.Run("a problem with no location under a field binds as a scoped bind does", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			schedule:
			  open: "09:00"
			  close: "08:00"
		`), niceyaml.WithName("cafe.yaml"))

		_, err := doc.Decode[struct {
			Schedule conflictingSchedule `yaml:"schedule"`
		}](t.Context())

		want := stringtest.JoinLF(
			"cafe.yaml:1:1: $.schedule: schedule conflicts",
			"|-- 2:9: $.schedule.open: opens here",
			"`-- 3:10: $.schedule.close: closes here",
		)

		// The problem takes the location of the field whatever its details
		// carry, and the details keep their own.
		require.EqualError(t, err, "cafe.yaml:1:1: $.schedule: schedule conflicts")
		assert.Equal(t, want, report(err))

		scoped := yamltest.At(t, doc, paths.Current().Child("schedule")).Bind(conflictingSchedule{}.Validate())
		require.EqualError(t, scoped, err.Error())
		assert.Equal(
			t,
			niceyaml.FormatError(err, niceyaml.WithContextLines(1)),
			niceyaml.FormatError(scoped, niceyaml.WithContextLines(1)),
		)
	})

	t.Run("elements and entries report their index or key", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
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

		_, err := doc.Decode[nested](t.Context())
		require.Error(t, err)

		// The deep any holds maps and slices of any, which hold no item.
		assert.ElementsMatch(t, []string{
			"cafe.yaml:5:12: $.items[1].price: negative price",
			"cafe.yaml:8:12: $.by_name.scone.price: negative price",
			"cafe.yaml:11:12: $.by_id.7.price: negative price",
			"cafe.yaml:14:12: $.fixed[1].price: negative price",
		}, errorTexts(yamltest.ProblemsOf(err)))
	})

	t.Run("a field reports at the path its tags give it", func(t *testing.T) {
		t.Parallel()

		type jsonTagged struct {
			Opening hours `json:"opening"`
		}

		type withInline struct {
			Name  string `yaml:"name"`
			Hours hours  `yaml:",inline"`
		}

		type embedded struct {
			Name string `yaml:"name"`
			Hours
		}

		type named struct {
			Named
		}

		tcs := map[string]struct {
			target any
			input  string
			err    string
		}{
			"a pointer walks when set": {
				target: new(nested),
				input: stringtest.Input(`
					hours: {open: "09:00", close: "17:00"}
					backup: {open: "09:00", close: "08:00"}
				`),
				err: "2:32: $.backup.close: closes before it opens",
			},
			"a pointer skips when nil": {
				target: new(nested),
				input:  `hours: {open: "09:00", close: "17:00"}`,
			},
			"a field without a tag decodes under its lowercased name": {
				target: new(nested),
				input: stringtest.Input(`
					hours: {open: "09:00", close: "17:00"}
					untagged: {open: "09:00", close: "08:00"}
				`),
				err: "2:34: $.untagged.close: closes before it opens",
			},
			"a json tag names the field when there is no yaml tag": {
				target: new(jsonTagged),
				input:  `opening: {open: "09:00", close: "08:00"}`,
				err:    "1:33: $.opening.close: closes before it opens",
			},
			"an inline field keeps the path of its parent": {
				target: new(withInline),
				input: stringtest.Input(`
					name: x
					open: "09:00"
					close: "08:00"
				`),
				err: "3:8: $.close: closes before it opens",
			},
			"an embedded struct without inline decodes under its name": {
				target: new(embedded),
				input: stringtest.Input(`
					name: x
					hours: {open: "09:00", close: "08:00"}
				`),
				err: "2:31: $.hours.close: closes before it opens",
			},
			"an embedded validator reports under its name": {
				target: new(named),
				input:  `named: {name: ""}`,
				err:    "1:15: $.named.name: name required",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.FirstDocument(t, tc.input).DecodeInto(t.Context(), tc.target)
				requireValidation(t, err, tc.err)
			})
		}
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
			target any
			input  string
			err    string
		}{
			"struct": {
				input:  "host: a\nport: 8080\n",
				target: new(server),
			},
			"pointer": {
				input:  "host: a\nport: 8080\n",
				target: new(pointer),
			},
			"parent field still validates": {
				input:  "host: a\nport: 0\n",
				err:    "2:7: $.port: must be positive",
				target: new(server),
			},
			"a field below a deeper inline struct validates": {
				input: "host: a\nport: 0\n",
				err: stringtest.JoinLF(
					"2:7: $.port: must be positive",
					"2:7: $.port: must be positive",
				),
				target: new(deep),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.FirstDocument(t, tc.input).DecodeInto(t.Context(), tc.target)
				requireValidation(t, err, tc.err)
			})
		}
	})

	t.Run("a field that is nil or ignored does not validate", func(t *testing.T) {
		t.Parallel()

		type inline struct {
			*Named `yaml:",inline"`

			Spec string `yaml:"spec"`
		}

		type named struct {
			*Named

			Spec string `yaml:"spec"`
		}

		type ignored struct {
			Named `yaml:"-"`

			Title string `yaml:"name"`
		}

		tcs := map[string]struct {
			target any
			input  string
		}{
			"nil inline embedded pointer": {
				input:  "# empty\n",
				target: new(inline),
			},
			"nil named embedded pointer": {
				input:  "spec: x\n",
				target: new(named),
			},
			"ignored embedded field": {
				input:  "name: hello",
				target: new(ignored),
			},
			"ignored field the caller set": {
				input:  `hours: {open: "09:00", close: "17:00"}`,
				target: &nested{Ignored: hours{Open: "09:00", Close: "08:00"}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				require.NoError(t, yamltest.FirstDocument(t, tc.input).DecodeInto(t.Context(), tc.target))
			})
		}
	})

	t.Run("an embedded validator runs once", func(t *testing.T) {
		t.Parallel()

		type inline struct {
			Counted `yaml:",inline"`
		}

		got, err := yamltest.FirstDocument(t, "name: x").Decode[inline](t.Context())
		require.NoError(t, err)
		assert.Equal(t, 1, got.runs)
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
		require.EqualError(t, err, stringtest.JoinLF(
			"1:1: $.value: parent ran",
			"2:1: $.pointer: parent ran",
		))
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

		// Go promotes the UnmarshalYAML of the Positive that shallow
		// embeds, which sits one field above the Positive in wrapped.
		type shallow struct {
			wrapped //nolint:unused // Only reflect reads the field.
			Positive
		}

		// The embedded pointer to looped reaches a second Positive, one
		// field deeper.
		type looped struct {
			*looped //nolint:unused // Only reflect reads the field.
			Positive
		}

		type parent struct {
			Wrapped  wrapped         `yaml:"wrapped"`
			Twice    PositiveWrapper `yaml:"twice"`
			Pointer  PositivePointer `yaml:"pointer"`
			Declared declaredDecode  `yaml:"declared"`
			Text     textDecode      `yaml:"text"`
			Both     both            `yaml:"both"`
			Shallow  shallow         `yaml:"shallow"`
			Looped   looped          `yaml:"looped"`
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
			"struct that declares its own unmarshaler validates the field at its own path": {
				input: "declared: -1\n",
				err:   "1:11: $.declared.positive: negative",
			},
			"struct that declares an unmarshaler go-yaml checks later": {
				input: "text: -1\n",
				err:   "1:7: $.text: negative",
			},
			"embedded field beside one with a later unmarshaler": {
				input: "both: -1\n",
				err:   "1:7: $.both: negative",
			},
			"embedded field above another with the same unmarshaler": {
				input: "shallow: -1\n",
				err:   "1:10: $.shallow: negative",
			},
			"embedded field beside a pointer to its own struct": {
				input: "looped: -1\n",
				err:   "1:9: $.looped: negative",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := yamltest.FirstDocument(t, tc.input).Decode[parent](t.Context())
				requireValidation(t, err, tc.err)
			})
		}
	})

	t.Run("an unexported embedded field validates", func(t *testing.T) {
		t.Parallel()

		type wrapped struct {
			innerPositive
		}

		type positives map[string]Positive

		// The scan reads the map in a bag through an unexported field, so
		// reflect refuses to set its entries into another value.
		type bag struct {
			positives //nolint:unused // Only reflect reads the field.
		}

		type opened struct {
			hours
		}

		type parent struct {
			hours

			Wrapped wrapped            `yaml:"wrapped"`
			ByName  map[string]wrapped `yaml:"by_name"`
			Bags    []bag              `yaml:"bags"`
			// Any holds a value the walk cannot take the address of.
			Any any `yaml:"any"`
		}

		tcs := map[string]struct {
			input string
			start parent
			err   string
		}{
			"field go-yaml leaves as it was": {
				input: "wrapped: 1\n",
				start: parent{hours: hours{Open: "09:00", Close: "08:00"}},
				err:   "1:1: $.hours.close: closes before it opens",
			},
			"field that decodes its struct": {
				input: "wrapped: -1\n",
				err:   "1:10: $.wrapped: negative",
			},
			"field that decodes a map value": {
				input: "by_name: {a: -1}\n",
				err:   "1:14: $.by_name.a: negative",
			},
			"map field under a slice go-yaml leaves as it was": {
				input: "wrapped: 1\n",
				start: parent{Bags: []bag{{positives: positives{"a": -1}}}},
				err:   "$.bags[0].positives.a: negative",
			},
			"field of a struct in an interface": {
				input: "wrapped: 1\n",
				start: parent{Any: opened{hours: hours{Open: "09:00", Close: "08:00"}}},
				err:   "1:1: $.any.hours.close: closes before it opens",
			},
			"field that decodes a struct in an interface": {
				input: "wrapped: 1\n",
				start: parent{Any: wrapped{innerPositive: -1}},
				err:   "1:1: $.any: negative",
			},
			"field that decodes a struct in an array in an interface": {
				input: "wrapped: 1\n",
				start: parent{Any: [1]wrapped{{innerPositive: -1}}},
				err:   "$.any[0]: negative",
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
				requireValidation(t, err, tc.err)
			})
		}
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

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			hours: {open: "09:00", close: "08:00"}
			backup: {open: "09:00", close: "08:00"}
		`), niceyaml.WithName("cafe.yaml"))

		_, err := doc.Decode[nested](t.Context())
		require.Error(t, err)

		assert.Equal(t, stringtest.JoinLF(
			"|-- cafe.yaml:1:31: $.hours.close: closes before it opens",
			"`-- cafe.yaml:2:32: $.backup.close: closes before it opens",
			"",
			`   1 | hours: {open: "09:00", close: "08:00"}`,
			"     |                               ^^^^^^^ closes before it opens",
			`   2 | backup: {open: "09:00", close: "08:00"}`,
			"     |                                ^^^^^^^ closes before it opens",
		), niceyaml.FormatError(err))
	})

	t.Run("WithSelfValidation false switches the walk off", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			hours: {open: "09:00", close: "08:00"}
		`))

		_, err := dd.Decode[nested](t.Context(), niceyaml.WithSelfValidation(false))
		require.NoError(t, err)
	})

	t.Run("a scoped decode reports paths from the document root", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			spec:
			  items:
			    - price: 1
			    - price: -1
		`), niceyaml.WithName("cafe.yaml"))

		_, err := yamltest.At(t, doc, paths.Current().Child("spec")).Decode[nested](t.Context())
		require.EqualError(t, err, "cafe.yaml:4:14: $.spec.items[1].price: negative price")
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

		// The alias makes the child of p2 the same pointer as the child
		// of p1. The value reports under the first path alone, and the
		// parent on the second path still does not run, since its child
		// failed. DecodeInto keeps the decoded value on an error, so the
		// test can compare the pointers.
		type shared struct {
			P1 sharedParent `yaml:"p1"`
			P2 sharedParent `yaml:"p2"`
		}

		dd := yamltest.FirstDocument(t, stringtest.LinesLF(
			"p1: {x: &x {name: n, price: -1}}",
			"p2: {x: *x}",
		))

		var got shared

		err := dd.DecodeInto(t.Context(), &got)
		require.EqualError(t, err, "1:29: $.p1.x.price: negative price")
		require.NotNil(t, got.P1.X)
		require.Same(t, got.P1.X, got.P2.X)
	})

	t.Run("chained aliases walk in linear time", func(t *testing.T) {
		t.Parallel()

		// Every node holds the node before it twice, so a walk that
		// revisits a shared value under every path takes 2^40 steps. The
		// source turns the alias limit off, which refuses such a document.
		var sb strings.Builder

		sb.WriteString("n0: &n0 {}\n")

		for i := 1; i <= 40; i++ {
			fmt.Fprintf(&sb, "n%d: &n%d {a: *n%d, b: *n%d}\n", i, i, i-1, i-1)
		}

		dd := yamltest.FirstDocument(t, sb.String(), niceyaml.WithAliasLimit(false))

		_, err := dd.Decode[map[string]*chain](t.Context())
		require.NoError(t, err)
	})

	t.Run("empty and nil values each validate", func(t *testing.T) {
		t.Parallel()

		// Every empty slice, every pointer to a zero-size value, and every
		// nil slice or map of one type can share one address, which must
		// not make them one value.
		type withRules struct {
			A rules `yaml:"a"`
			B rules `yaml:"b"`
		}

		type withFlags struct {
			A *flag `yaml:"a"`
			B *flag `yaml:"b"`
		}

		type withEmpty struct {
			A rules `yaml:"a"`
			B rules `yaml:"b"`
			M names `yaml:"m"`
			N names `yaml:"n"`
		}

		tcs := map[string]struct {
			target any
			input  string
			err    string
		}{
			"empty slices": {
				target: new(withRules),
				input:  "a: []\nb: []\n",
				err: stringtest.JoinLF(
					"1:1: $.a: no rules",
					"2:1: $.b: no rules",
				),
			},
			"pointers to zero-size values": {
				target: new(withFlags),
				input:  "a: {}\nb: {}\n",
				err: stringtest.JoinLF(
					"1:1: $.a: flag set",
					"2:1: $.b: flag set",
				),
			},
			// A field the document leaves out binds at the mapping that lacks
			// it, and the message lists the errors by position.
			"nil slices and maps": {
				target: new(withEmpty),
				input:  "a: null\nm: null\n",
				err: stringtest.JoinLF(
					"1:1: $.b: no rules",
					"1:1: $.n: no names",
					"1:4: $.a: no rules",
					"2:4: $.m: no names",
				),
			},
			"nil slice at the root": {
				target: new(rules),
				input:  "null\n",
				err:    "1:1: $: no rules",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.FirstDocument(t, tc.input).DecodeInto(t.Context(), tc.target)
				requireValidation(t, err, tc.err)
			})
		}
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
				requireValidation(t, err, tc.err)
			})
		}
	})

	t.Run("a cycle through shared map values scans in linear time", func(t *testing.T) {
		t.Parallel()

		// Every mesh links to the next one under two keys, and the last
		// links back to the first, so a scan that reads a shared value
		// again on every path takes 2^40 steps.
		type withMesh struct {
			Name string   `yaml:"name"`
			Mesh *mapMesh `yaml:"mesh"`
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
				err: "1:1: $.mesh" + strings.Repeat(".next.a", 40) +
					".port: port out of range",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				nodes := mapMeshChain(40)
				if tc.port >= 0 {
					bad := port(-1)
					nodes[tc.port].Port = &bad
				}

				dd := yamltest.FirstDocument(t, "name: x\n")

				err := dd.DecodeInto(t.Context(), &withMesh{Mesh: nodes[0]})
				requireValidation(t, err, tc.err)
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

	t.Run("a value in a cycle takes the result of the cycle", func(t *testing.T) {
		t.Parallel()

		// The link leads back to the head, so the walk of the link stops
		// there and passes before the port of the head fails. The parent
		// that holds the link still runs only when the whole cycle
		// passed, whichever field the walk enters first.
		type headFirst struct {
			Head   *cycleHead  `yaml:"head"`
			Parent cycleParent `yaml:"parent"`
		}

		type parentFirst struct {
			Parent cycleParent `yaml:"parent"`
			Head   *cycleHead  `yaml:"head"`
		}

		tcs := map[string]struct {
			port        port
			parentFirst bool
			err         string
		}{
			"the head walks first": {
				port: -1,
				err:  "1:1: $.head.port: port out of range",
			},
			"the parent walks first": {
				port:        -1,
				parentFirst: true,
				err:         "1:1: $.parent.link.back.port: port out of range",
			},
			"a cycle that passes": {
				port: 80,
				err:  "1:1: $.parent: parent ran",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				head := &cycleHead{Port: tc.port}
				head.Link = &cycleLink{Back: head}
				parent := cycleParent{Link: head.Link}

				var v any = &headFirst{Head: head, Parent: parent}

				if tc.parentFirst {
					v = &parentFirst{Parent: parent, Head: head}
				}

				dd := yamltest.FirstDocument(t, "{}\n")

				require.EqualError(t, dd.DecodeInto(t.Context(), v), tc.err)
			})
		}
	})

	t.Run("map entries report by position", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.LinesLF(
			"d: {price: -1}",
			"b: {price: -1}",
			"c: {price: -1}",
			"a: {price: -1}",
		))

		// The message and the tree both list the errors by position,
		// whatever order the walk reports them in, so every decode reads
		// the same.
		want := []string{
			"1:12: $.d.price: negative price",
			"2:12: $.b.price: negative price",
			"3:12: $.c.price: negative price",
			"4:12: $.a.price: negative price",
		}

		for range 20 {
			_, err := dd.Decode[map[string]item](t.Context())
			require.EqualError(t, err, stringtest.JoinLF(want...))
			assert.Equal(t, want, errorTexts(yamltest.ProblemsOf(err)))
		}
	})

	t.Run("keys of one text order by the types they hold", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(
			t,
			stringtest.LinesLF("1: {n: -1}", `"1": {n: -2}`),
			niceyaml.WithAllowDuplicateKeys(true),
		)

		// The string key sorts before the uint64 key.
		want := stringtest.JoinLF(
			"$.1.n: negative -2",
			"$.1.n: negative -1",
		)

		for range 20 {
			_, err := dd.Decode[map[any]signed](t.Context())
			require.EqualError(t, err, want)
		}
	})

	t.Run("keys of one text report no position", func(t *testing.T) {
		t.Parallel()

		// The path of either key resolves to the entry the mapping
		// spells, so no error takes the line of that entry.
		type merged struct {
			M    map[any]signed `yaml:"m"`
			Base map[string]any `yaml:"base"`
		}

		tcs := map[string]struct {
			input string
			opts  []niceyaml.SourceOption
			want  []string
		}{
			"merged int beside a string that passes": {
				input: stringtest.LinesLF(
					"base: &b",
					"  1: {n: -1}",
					"m:",
					"  <<: *b",
					`  "1": {n: 5}`,
				),
				want: []string{"$.m.1.n: negative -1"},
			},
			"merged int beside a string": {
				input: stringtest.LinesLF(
					"base: &b",
					"  1: {n: -1}",
					"m:",
					"  <<: *b",
					`  "1": {n: -2}`,
				),
				want: []string{"$.m.1.n: negative -2", "$.m.1.n: negative -1"},
			},
			"merged null beside a string": {
				input: stringtest.LinesLF(
					"base: &b",
					"  null: {n: -1}",
					"m:",
					"  <<: *b",
					`  "null": {n: -2}`,
				),
				want: []string{"$.m.null.n: negative -1", "$.m.null.n: negative -2"},
			},
			"merged bool beside a string": {
				input: stringtest.LinesLF(
					"base: &b",
					"  true: {n: -1}",
					"m:",
					"  <<: *b",
					`  "true": {n: -2}`,
				),
				want: []string{"$.m.true.n: negative -1", "$.m.true.n: negative -2"},
			},
			"duplicate keys": {
				input: stringtest.LinesLF(
					"m:",
					"  1: {n: -1}",
					`  "1": {n: -2}`,
				),
				opts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
				want: []string{"$.m.1.n: negative -2", "$.m.1.n: negative -1"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input, tc.opts...)

				_, err := dd.Decode[merged](t.Context())
				require.Error(t, err)

				assert.Equal(t, tc.want, ambiguousTexts(t, err))
			})
		}
	})

	t.Run("a map key reports the text the document spells it with", func(t *testing.T) {
		t.Parallel()

		type named struct {
			M map[string]item    `yaml:"m"`
			F map[float64]item   `yaml:"f"`
			I map[int]item       `yaml:"i"`
			B map[bytesKey]item  `yaml:"bytes"`
			T map[time.Time]item `yaml:"t"`
		}

		type merged struct {
			M map[float64]item `yaml:"m"`
		}

		tcs := map[string]struct {
			input string
			want  string
			// A reference document for the source, which may hold a key
			// twice beside it.
			references string
			// A decode into each target reports want.
			targets []any
		}{
			"float with a trailing zero": {
				input:   "1.50: {price: -1}\n",
				targets: []any{new(map[float64]item)},
				want:    "1:15: $.'1.50'.price: negative price",
			},
			"hexadecimal int": {
				input:   "0x10: {price: -1}\n",
				targets: []any{new(map[float64]item)},
				want:    "1:15: $.0x10.price: negative price",
			},
			// An integer key under a !!int tag decodes into an integer type as
			// the integer alone does.
			"int-tagged hexadecimal int": {
				input:   "!!int 0x10: {price: -1}\n",
				targets: []any{new(map[int]item), new(map[uint64]item)},
				want:    "1:21: $.0x10.price: negative price",
			},
			"anchored int-tagged hexadecimal int": {
				input:   "&a !!int 0x10: {price: -1}\n",
				targets: []any{new(map[int]item), new(map[uint64]item)},
				want:    "1:24: $.0x10.price: negative price",
			},
			"int tag on an anchored hexadecimal int": {
				input:   "!!int &a 0x10: {price: -1}\n",
				targets: []any{new(map[int]item), new(map[uint64]item)},
				want:    "1:24: $.0x10.price: negative price",
			},
			"int-tagged hexadecimal int after another key": {
				input:   stringtest.LinesLF("1: {price: 1}", "!!int 0x10: {price: -1}"),
				targets: []any{new(map[int]item), new(map[uint64]item)},
				want:    "2:21: $.0x10.price: negative price",
			},
			// A key type that reads YAML bytes or a node takes the !!int tag
			// with the integer, as it does in the decode.
			"unmarshaled int-tagged hexadecimal int": {
				input:   "!!int 0x10: {price: -1}\n",
				targets: []any{new(map[bytesKey]item), new(map[nodeKey]item)},
				want:    "1:21: $.0x10.price: negative price",
			},
			"unmarshaled int tag with spaces after it": {
				input:   "!!int   0x10: {price: -1}\n",
				targets: []any{new(map[bytesKey]item), new(map[nodeKey]item)},
				want:    "1:23: $.0x10.price: negative price",
			},
			// An alias key reports the content of its anchor, and a block
			// scalar key reports its content rather than its indicator.
			"alias": {
				input: stringtest.LinesLF(
					"base: &k n",
					"m:",
					"  *k : {price: -1}",
				),
				targets: []any{new(named)},
				want:    "3:16: $.m.n.price: negative price",
			},
			"alias to a hexadecimal int": {
				input: stringtest.LinesLF(
					"base: &k 0x10",
					"f:",
					"  *k : {price: -1}",
				),
				targets: []any{new(named)},
				want:    "3:16: $.f.0x10.price: negative price",
			},
			"alias to an int-tagged hexadecimal int": {
				input: stringtest.LinesLF(
					"base: &k !!int 0x10",
					"i:",
					"  *k : {price: -1}",
				),
				targets: []any{new(named)},
				want:    "3:16: $.i.0x10.price: negative price",
			},
			"bytes alias to an int-tagged hexadecimal int": {
				input: stringtest.LinesLF(
					"base: &k !!int 0x10",
					"bytes:",
					"  *k : {price: -1}",
				),
				targets: []any{new(named)},
				want:    "3:16: $.bytes.0x10.price: negative price",
			},
			"alias to a float with a trailing zero": {
				input: stringtest.LinesLF(
					"base: &k 1.50",
					"f:",
					"  *k : {price: -1}",
				),
				targets: []any{new(named)},
				want:    "3:16: $.f.'1.50'.price: negative price",
			},
			"string alias to a hexadecimal int": {
				input: stringtest.LinesLF(
					"base: &k 0x10",
					"m:",
					"  *k : {price: -1}",
				),
				targets: []any{new(named)},
				want:    "3:16: $.m.0x10.price: negative price",
			},
			"tagged alias": {
				input: stringtest.LinesLF(
					"base: &k 0x10",
					"m:",
					"  !!int *k : {price: -1}",
				),
				targets: []any{new(named)},
				want:    "3:22: $.m.0x10.price: negative price",
			},
			"alias to a tagged alias": {
				input: stringtest.LinesLF(
					"base: &k 0x10",
					"b: &j !!int *k",
					"m:",
					"  *j : {price: -1}",
				),
				targets: []any{new(named)},
				want:    "4:16: $.m.0x10.price: negative price",
			},
			"key of a mapping behind an alias": {
				input:   stringtest.LinesLF("base: &b {0x10: {price: -1}}", "f: *b"),
				targets: []any{new(named)},
				want:    "1:25: $.f.0x10.price: negative price",
			},
			"key of a mapping behind a tagged alias": {
				input:   stringtest.LinesLF("base: &b {0x10: {price: -1}}", "f: !t *b"),
				targets: []any{new(named)},
				want:    "1:25: $.f.0x10.price: negative price",
			},
			"block scalar": {
				input: stringtest.LinesLF(
					"m:",
					"  ? |-",
					"    n",
					"  : {price: -1}",
				),
				targets: []any{new(named)},
				want:    "4:13: $.m.n.price: negative price",
			},
			"time with an offset of whole hours": {
				input: stringtest.LinesLF(
					"t:",
					"  2024-01-01T00:00:00+05:00: {price: -1}",
				),
				targets: []any{new(named)},
				want:    "2:38: $.t.'2024-01-01T00:00:00+05:00'.price: negative price",
			},
			"time with an offset of part of an hour": {
				input: stringtest.LinesLF(
					"t:",
					"  2024-01-01T00:00:00+05:30: {price: -1}",
				),
				targets: []any{new(named)},
				want:    "2:38: $.t.'2024-01-01T00:00:00+05:30'.price: negative price",
			},
			// Where a key of the mapping and a merged key decode to the same
			// value, the later entry holds the value the decode keeps, and
			// the error reports its text.
			"merge after a key of the mapping": {
				input: stringtest.LinesLF(
					"b: &b {0x10: {price: -1}}",
					"m: {16: {price: 1}, <<: *b}",
				),
				targets: []any{new(merged)},
				want:    "1:22: $.m.0x10.price: negative price",
			},
			"key of the mapping after a merge": {
				input: stringtest.LinesLF(
					"b: &b {0x10: {price: 1}}",
					"m: {<<: *b, 16: {price: -1}}",
				),
				targets: []any{new(merged)},
				want:    "2:25: $.m.16.price: negative price",
			},
			"merge after a key of the mapping with the same spelling": {
				input: stringtest.LinesLF(
					"b: &b {16: {price: -1}}",
					"m: {16: {price: 1}, <<: *b}",
				),
				targets: []any{new(merged)},
				want:    "1:20: $.m.16.price: negative price",
			},
			"key of the mapping with the same spelling after a merge": {
				input: stringtest.LinesLF(
					"b: &b {16: {price: 1}}",
					"m: {<<: *b, 16: {price: -1}}",
				),
				targets: []any{new(merged)},
				want:    "2:25: $.m.16.price: negative price",
			},
			"merge source repeated after a source that merges it": {
				input: stringtest.LinesLF(
					"a: &a {16: {price: -1}}",
					"b: &b {<<: *a, 0x10: {price: 1}}",
					"m: {<<: [*b, *a]}",
				),
				targets: []any{new(merged)},
				want:    "1:20: $.m.16.price: negative price",
			},
			// A merge whose alias does not resolve in the document, as one to
			// an anchor of WithReferences, may set any key. No entry before it
			// names a key, so the error reports the decoded value instead.
			"key of the mapping before the merge": {
				input:      "m: {0x10: {price: 1}, <<: *r}\n",
				targets:    []any{new(merged)},
				references: "r: &r {16: {price: -1}}\n",
				want:       "$.m.16.price: negative price",
			},
			"key of the mapping after the merge": {
				input:      "m: {<<: *r, 0x10: {price: -1}}\n",
				targets:    []any{new(merged)},
				references: "r: &r {16: {price: 1}}\n",
				want:       "1:27: $.m.0x10.price: negative price",
			},
			"merge inside a source": {
				input:      stringtest.LinesLF("s: &s {<<: *r}", "m: {0x10: {price: 1}, <<: *s}"),
				targets:    []any{new(merged)},
				references: "r: &r {16: {price: -1}}\n",
				want:       "$.m.16.price: negative price",
			},
			"resolvable merge after the merge": {
				input:      stringtest.LinesLF("b: &b {0x10: {price: -1}}", "m: {<<: *r, <<: *b}"),
				targets:    []any{new(merged)},
				references: "r: &r {16: {price: 1}}\n",
				want:       "1:22: $.m.0x10.price: negative price",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var opts []niceyaml.SourceOption

				if tc.references != "" {
					opts = []niceyaml.SourceOption{
						niceyaml.WithAllowDuplicateKeys(true),
						niceyaml.WithReferences(niceyaml.NewSourceFromString(tc.references)),
					}
				}

				dd := yamltest.FirstDocument(t, tc.input, opts...)

				for _, target := range tc.targets {
					require.EqualError(t, dd.DecodeInto(t.Context(), target), tc.want)
				}
			})
		}

		// A key a merge brings in reports the text of the mapping it
		// comes from.
		dd := yamltest.FirstDocument(t, stringtest.LinesLF(
			"base: &b {0x10: {price: -1}}",
			"m: {<<: *b}",
		))

		_, err := dd.Decode[map[string]map[float64]item](t.Context())

		assert.Equal(t, []string{
			"1:25: $.base.0x10.price: negative price",
			"1:25: $.m.0x10.price: negative price",
		}, errorTexts(yamltest.ProblemsOf(err)))

		// Keys an alias to a list of sources or a second merge key brings
		// in report the text of their own mappings too.
		mergeTcs := map[string]struct {
			input string
			opts  []niceyaml.SourceOption
		}{
			"alias to a sequence": {
				input: stringtest.LinesLF(
					"a: &a {0x10: {price: -1}}",
					"b: &b {0x20: {price: -1}}",
					"l: &l [*a, *b]",
					"m: {<<: *l}",
				),
			},
			"repeated merge keys": {
				input: stringtest.LinesLF(
					"a: &a {0x10: {price: -1}}",
					"b: &b {0x20: {price: -1}}",
					"m: {<<: *a, <<: *b}",
				),
				opts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			},
		}

		for name, tc := range mergeTcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input, tc.opts...)

				_, err := dd.Decode[merged](t.Context())

				assert.Equal(t, []string{
					"1:22: $.m.0x10.price: negative price",
					"2:22: $.m.0x20.price: negative price",
				}, errorTexts(yamltest.ProblemsOf(err)))
			})
		}
	})

	t.Run("a pointer map key reports the text the document spells it with", func(t *testing.T) {
		t.Parallel()

		type pointers struct {
			S map[*string]signed  `yaml:"s"`
			I map[*int]signed     `yaml:"i"`
			F map[*float64]signed `yaml:"f"`
		}

		tcs := map[string]struct {
			input string
			want  []string
		}{
			"strings by position": {
				input: stringtest.LinesLF(
					"s:",
					"  b: {n: -2}",
					"  a: {n: -1}",
				),
				want: []string{
					"2:10: $.s.b.n: negative -2",
					"3:10: $.s.a.n: negative -1",
				},
			},
			"hexadecimal int": {
				input: stringtest.LinesLF(
					"i:",
					"  0x10: {n: -1}",
				),
				want: []string{"2:13: $.i.0x10.n: negative -1"},
			},
			"NaN": {
				input: stringtest.LinesLF(
					"f:",
					"  .nan: {n: -1}",
				),
				want: []string{"2:13: $.f.'.nan'.n: negative -1"},
			},
			"null beside a string": {
				input: stringtest.LinesLF(
					"s:",
					"  ~: {n: -1}",
					"  b: {n: -2}",
				),
				want: []string{
					"2:10: $.s.'~'.n: negative -1",
					"3:10: $.s.b.n: negative -2",
				},
			},
			"anchored null": {
				input: stringtest.LinesLF(
					"s:",
					"  &k ~: {n: -1}",
				),
				want: []string{"2:13: $.s.'~'.n: negative -1"},
			},
			"keys of one value": {
				input: stringtest.LinesLF(
					"i:",
					"  0x10: {n: -1}",
					"  16: {n: -2}",
				),
				want: []string{
					"$.i.16.n: negative -1",
					"$.i.16.n: negative -2",
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				_, err := dd.Decode[pointers](t.Context())
				require.Error(t, err)

				assert.Equal(t, tc.want, validationTexts(err))
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
			target any
			input  string
			want   []string
		}{
			"top-level map": {
				input:  "70000: x\n",
				target: new(map[port]string),
				want:   []string{"1:1: $.70000~: port out of range"},
			},
			"map in a field": {
				input: stringtest.LinesLF(
					"m:",
					"  70000: x",
				),
				target: new(byPort),
				want:   []string{"2:3: $.m.70000~: port out of range"},
			},
			"key and value both fail": {
				input: stringtest.LinesLF(
					"m:",
					"  70001: 70002",
					"  70000: 1",
				),
				target: new(portsByPort),
				want: []string{
					"2:3: $.m.70001~: port out of range",
					"2:10: $.m.70001: port out of range",
					"3:3: $.m.70000~: port out of range",
				},
			},
			"valid key": {
				input:  "80: x\n",
				target: new(map[port]string),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := dd.DecodeInto(t.Context(), tc.target)
				if tc.want == nil {
					require.NoError(t, err)

					return
				}

				assert.Equal(t, tc.want, validationTexts(yamltest.RequireSourceError(t, err)))
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

				dd := yamltest.FirstDocument(t, stringtest.LinesLF(".nan: {price: -1}", "1.5: {price: -1}"))

				err := dd.DecodeInto(t.Context(), tc.target)

				assert.Equal(t, []string{
					"1:15: $.'.nan'.price: negative price",
					"2:14: $.'1.5'.price: negative price",
				}, errorTexts(yamltest.ProblemsOf(err)))
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

					assert.Equal(t, []string{
						"$.NaN.n: negative -1",
						"$.NaN.n: negative -2",
					}, errorTexts(yamltest.ProblemsOf(err)))
				}
			})
		}
	})

	t.Run("several NaN keys report no position beside a key spelled NaN", func(t *testing.T) {
		t.Parallel()

		// Every NaN key takes the path segment NaN, which the first key
		// spells, so that path resolves to the first entry alone.
		dd := yamltest.FirstDocument(t, "NaN: {n: -1}\n.nan: {n: -2}\n")

		_, err := dd.Decode[map[float64]signed](t.Context())
		require.Error(t, err)

		assert.Equal(t, []string{
			"$.NaN.n: negative -1",
			"$.NaN.n: negative -2",
		}, ambiguousTexts(t, err))
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

	t.Run("a pointer key that stays a pointer reports its type", func(t *testing.T) {
		t.Parallel()

		// Each call returns a pointer to a new slice that holds itself,
		// which no formatting finishes.
		cycle := func() *[]any {
			s := []any{nil}
			s[0] = s

			return &s
		}

		type pointers struct {
			M map[*[]any]signed `yaml:"m"`
		}

		type pointersToPointers struct {
			M map[**[]any]signed `yaml:"m"`
		}

		type interfaces struct {
			M map[any]signed `yaml:"m"`
		}

		type point struct{ X, Y int }

		// The document leaves each map as the caller filled it.
		tcs := map[string]struct {
			target func() any
			want   string
		}{
			"slice": {
				target: func() any {
					return &pointers{M: map[*[]any]signed{{1, 2}: {N: -1}}}
				},
				want: "1:1: $.m.'(*[]interface {})'.n: negative -1",
			},
			"slices of one type": {
				target: func() any {
					return &pointers{M: map[*[]any]signed{{2}: {N: -2}, {1}: {N: -1}}}
				},
				want: stringtest.JoinLF(
					"$.m.'(*[]interface {})'.n: negative -1",
					"$.m.'(*[]interface {})'.n: negative -2",
				),
			},
			"slice that holds itself": {
				target: func() any {
					return &pointers{M: map[*[]any]signed{cycle(): {N: -1}}}
				},
				want: "1:1: $.m.'(*[]interface {})'.n: negative -1",
			},
			"pointer to a slice that holds itself": {
				target: func() any {
					p := cycle()

					return &pointersToPointers{M: map[**[]any]signed{&p: {N: -1}}}
				},
				want: "1:1: $.m.'(*[]interface {})'.n: negative -1",
			},
			"slice that holds itself behind an interface": {
				target: func() any {
					return &interfaces{M: map[any]signed{cycle(): {N: -1}}}
				},
				want: "1:1: $.m.'(*[]interface {})'.n: negative -1",
			},
			"struct behind an interface": {
				target: func() any {
					return &interfaces{M: map[any]signed{&point{X: 1, Y: 2}: {N: -1}}}
				},
				want: "1:1: $.m.'(*niceyaml_test.point)'.n: negative -1",
			},
			"int behind an interface": {
				target: func() any {
					n := 1

					return &interfaces{M: map[any]signed{&n: {N: -1}}}
				},
				want: "1:1: $.m.'(*int)'.n: negative -1",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, "{}\n")

				err := dd.DecodeInto(t.Context(), tc.target())
				require.EqualError(t, err, tc.want)
			})
		}
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

		assert.Equal(t, []string{
			"$.'2024-01-01 00:00:00 +0530 +0530'.price: negative price",
			"$.'2024-01-01 00:00:00 +0530 +0530'.price: negative price",
		}, errorTexts(yamltest.ProblemsOf(err)))
	})

	t.Run("a key below a wide map reports the text the document spells it with", func(t *testing.T) {
		t.Parallel()

		// The walk reads the keys of every map below the wide one from the
		// document, and each of those maps sits among 2000 siblings.
		input := yamltest.GenerateLines(2000, "k%d: {1: {price: 1}}") + "last: {0x10: {price: -1}}\n"
		dd := yamltest.FirstDocument(t, input)

		_, err := dd.Decode[map[string]map[float64]item](t.Context())
		require.EqualError(t, err, "2001:22: $.last.0x10.price: negative price")
	})

	t.Run("a nested map reports its keys as the document spells them", func(t *testing.T) {
		t.Parallel()

		// The walk resolves the node of each value from the node of the
		// value above it, so these maps resolve from a parent node rather
		// than from the root, whether a map, a struct, or a list holds
		// them.
		type link struct {
			Next  map[string]link  `yaml:"next"`
			Items map[float64]item `yaml:"items"`
		}

		type chain struct {
			Next  *chain           `yaml:"next"`
			Items map[float64]item `yaml:"items"`
			Ports map[port]string  `yaml:"ports"`
			List  []chain          `yaml:"list"`
		}

		const depth = 1000

		deep := strings.Repeat("{next: {a: ", depth) + "{items: {0x10: {price: -1}}}" + strings.Repeat("}}", depth)
		deepChain := strings.Repeat("{next: ", depth) + "{items: {0x10: {price: -1}}}" + strings.Repeat("}", depth)

		tcs := map[string]struct {
			target any
			input  string
			at     paths.Path
			want   []string
		}{
			"deep chain": {
				input:  deep,
				target: new(link),
				want: []string{fmt.Sprintf(
					"1:%d: $%s.items.0x10.price: negative price",
					strings.Index(deep, "-1")+1, strings.Repeat(".next.a", depth),
				)},
			},
			"deep struct chain": {
				input:  deepChain,
				target: new(chain),
				want: []string{fmt.Sprintf(
					"1:%d: $%s.items.0x10.price: negative price",
					strings.Index(deepChain, "-1")+1, strings.Repeat(".next", depth),
				)},
			},
			"maps at every link of a chain": {
				input: stringtest.LinesLF(
					"items: {0x10: {price: -1}}",
					"next:",
					"  items: {0x10: {price: -2}}",
					"  next:",
					"    items: {0x10: {price: -3}}",
				),
				target: new(chain),
				want: []string{
					"1:23: $.items.0x10.price: negative price",
					"3:25: $.next.items.0x10.price: negative price",
					"5:27: $.next.next.items.0x10.price: negative price",
				},
			},
			"key below list elements": {
				input: stringtest.LinesLF(
					"list:",
					"  - {}",
					"  - list:",
					"      - ports: {70000: x}",
				),
				target: new(chain),
				want:   []string{"4:17: $.list[1].list[0].ports.70000~: port out of range"},
			},
			"key below two maps": {
				input: stringtest.LinesLF(
					"a:",
					"  b:",
					"    70000: x",
				),
				target: new(map[string]map[string]map[port]string),
				want:   []string{"3:5: $.a.b.70000~: port out of range"},
			},
			"merge below a map": {
				input: stringtest.LinesLF(
					"defs: {base: &b {0x10: {price: -1}}}",
					"m: {n: {<<: *b}}",
				),
				target: new(map[string]map[string]map[float64]item),
				want: []string{
					"1:32: $.defs.base.0x10.price: negative price",
					"1:32: $.m.n.0x10.price: negative price",
				},
			},
			"maps below a node at a path": {
				input: stringtest.LinesLF(
					"spec:",
					"  a:",
					"    0x10: {price: -1}",
				),
				at:     paths.Current().Child("spec"),
				target: new(map[string]map[float64]item),
				want:   []string{"3:19: $.spec.a.0x10.price: negative price"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.At(t, yamltest.FirstDocument(t, tc.input), tc.at)

				err := dd.DecodeInto(t.Context(), tc.target)

				assert.Equal(t, tc.want, validationTexts(yamltest.RequireSourceError(t, err)))
			})
		}
	})

	t.Run("the walk decodes each key once", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, yamltest.GenerateLines(50, "k%d: {1: {price: 1}}"))

		keys := 0
		count := countStrings(&keys)

		_, err := dd.Decode[map[string]map[float64]item](t.Context(), count, niceyaml.WithSelfValidation(false))
		require.NoError(t, err)
		require.Equal(t, 50, keys)

		keys = 0

		_, err = dd.Decode[map[string]map[float64]item](t.Context(), count)
		require.NoError(t, err)

		// The decode reads each key of the outer map, and the walk reads
		// each one again to spell it.
		assert.Equal(t, 100, keys)
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
				input: stringtest.LinesLF(
					"m:",
					"  web: {price: 1}",
				),
				want: map[spacedKey]item{"prod/web": {Price: 1}},
			},
			"fails": {
				input: stringtest.LinesLF(
					"m:",
					"  web: {price: 1}",
					"  db: {price: -1}",
				),
				err: "3:15: $.m.db.price: negative price",
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
			target func() any
			input  string
		}{
			// The map validates itself, and its type holds no validator
			// below it.
			"map that validates itself": {
				input:  "a: x\nb: y\n",
				target: func() any { return new(labels) },
			},
			// Each map validates itself, and the values go-yaml decodes into
			// its interface values hold no validator.
			"list of maps that validate themselves": {
				input:  "- {a: 1}\n- {b: {c: 1}}\n",
				target: func() any { return new([]anyLabels) },
			},
			// The type of the values may hold a validator, but the values
			// go-yaml decodes into an interface hold none.
			"map of any": {
				input:  "a: {b: 1}\nc: [{d: 1}]\n",
				target: func() any { return new(map[string]any) },
			},
			"any": {
				input:  "- {a: 1}\n- {b: {c: 1}}\n",
				target: func() any { return new(any) },
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				decoded := 0
				count := countStrings(&decoded)

				require.NoError(t, dd.DecodeInto(t.Context(), tc.target(), count, niceyaml.WithSelfValidation(false)))

				want := decoded
				decoded = 0

				require.NoError(t, dd.DecodeInto(t.Context(), tc.target(), count))

				// The decode reads each key, and the walk reads none again.
				assert.Equal(t, want, decoded)
			})
		}
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

		scoped := yamltest.At(t, dd, paths.Current().Child("port"))

		_, err = scoped.Decode[port](t.Context())
		require.EqualError(t, err, "1:7: $.port: port out of range")
	})

	t.Run("a leaf type validates itself", func(t *testing.T) {
		t.Parallel()

		type withPort struct {
			Ports []port `yaml:"ports"`
		}

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
			err    string
		}{
			"slice": {
				target: &withPort{},
				input:  "ports: [80, 70000]\n",
				err:    "1:13: $.ports[1]: port out of range",
			},
			"slice of a byte-sized type": {
				target: &withLevels{},
				input:  "levels: [1, 9]\n",
				err:    "1:13: $.levels[1]: level above 5",
			},
			"array of a byte-sized type": {
				target: &withFixed{},
				input:  "fixed: [1, 9]\n",
				err:    "1:12: $.fixed[1]: level above 5",
			},
			// The bytes of a plain []byte hold nothing to validate.
			"plain bytes": {
				target: &withData{},
				input:  "data: [1, 9]\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.FirstDocument(t, tc.input).DecodeInto(t.Context(), tc.target)
				requireValidation(t, err, tc.err)
			})
		}
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

		type withAnyKeys struct {
			Name  string      `yaml:"name"`
			Extra map[any]any `yaml:"extra"`
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
				input:  stringtest.LinesLF("a: x", `b: ""`),
				want:   "2:4: $.b: empty label",
			},
			"map of strings behind a field": {
				target: &withLabels{},
				input:  `labels: {a: x, b: ""}` + "\n",
				want:   "1:19: $.labels.b: empty label",
			},
			"map of strings in a map of any the caller filled": {
				target: &withExtra{Extra: map[string]any{"l": labels{"b": ""}}},
				input:  "name: x\n",
				want:   "1:1: $.extra.l.b: empty label",
			},
			"validator deep in a map of any the caller filled": {
				target: &withExtra{Extra: map[string]any{
					"list": []any{map[string]any{"a": 1}, map[string]any{"s": signed{N: -1}}},
					"z":    map[string]any{"b": map[string]any{"c": 2}},
				}},
				input: "name: x\n",
				want:  "$.extra.list[1].s.n: negative -1",
			},
			"validator key in a map of any the caller filled": {
				target: &withAnyKeys{Extra: map[any]any{"a": 1, port(70000): 2}},
				input:  "name: x\n",
				want:   "$.extra.70000~: port out of range",
			},
			"validator beside a map of any that holds itself": {
				target: &withExtra{Extra: loop},
				input:  "name: x\n",
				want:   "1:1: $.extra.v.n: negative -1",
			},
			"type that holds itself": {
				target: &tree{},
				input: stringtest.LinesLF(
					"kids:",
					"  - port: 70000",
					"port: 1",
				),
				want: "2:11: $.kids[0].port: port out of range",
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

		dd = yamltest.FirstDocument(t, stringtest.LinesLF(
			"hours:",
			"  open: x",
		))

		_, err = dd.Decode[withNothingJoined](t.Context())
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "parent ran")

		assert.Equal(t, "$.hours", errorPath(t, errorIn(t, err)).String())
	})

	t.Run("the values below a value that decodes itself validate", func(t *testing.T) {
		t.Parallel()

		type withSelfDecoding struct {
			Bytes selfDecodingBytes `yaml:"bytes"`
			Text  selfDecodingText  `yaml:"text"`
		}

		dd := yamltest.FirstDocument(t, "bytes: anything\ntext: anything\n")

		var got withSelfDecoding

		// Each unmarshaler fills its hours with a close before its open.
		// The document holds no node at the path of that close, so each
		// error binds at the value that decodes itself. That value does
		// not validate, since a value below it failed.
		err := dd.DecodeInto(t.Context(), &got)
		require.EqualError(t, err, stringtest.JoinLF(
			"1:8: $.bytes.inner.close: closes before it opens",
			"2:7: $.text.inner.close: closes before it opens",
		))
		assert.False(t, got.Bytes.validated)
		assert.False(t, got.Text.validated)
		assert.Equal(t, hours{Open: "17:00", Close: "09:00"}, got.Bytes.Inner)
		assert.Equal(t, hours{Open: "17:00", Close: "09:00"}, got.Text.Inner)

		type withOwnFailure struct {
			Bytes failingSelfDecoding `yaml:"bytes"`
		}

		_, err = dd.Decode[withOwnFailure](t.Context())
		require.EqualError(t, err, "1:8: $.bytes: rejected")
	})

	t.Run("the values below a value an option gives an unmarshaler validate", func(t *testing.T) {
		t.Parallel()

		type withOptionDecoded struct {
			Custom optionDecodedHours `yaml:"custom"`
			JSON   jsonDecodedHours   `yaml:"json"`
		}

		// The function fills its hours with a close before its open, as
		// the UnmarshalJSON method does. The document holds no node at the
		// path of that close, so each error binds at the value the option
		// gives an unmarshaler.
		opts := niceyaml.DecodeOptions(
			niceyaml.WithCustomUnmarshaler(func(_ context.Context, o *optionDecodedHours, _ func(any) error) error {
				o.Inner = hours{Open: "17:00", Close: "09:00"}

				return nil
			}),
			niceyaml.WithJSONUnmarshalers(true),
		)

		want := stringtest.JoinLF(
			"1:9: $.custom.inner.close: closes before it opens",
			"2:7: $.json.inner.close: closes before it opens",
		)

		dd := yamltest.FirstDocument(t, "custom: anything\njson: anything\n")

		_, err := dd.Decode[withOptionDecoded](t.Context(), opts)
		require.EqualError(t, err, want)

		var got withOptionDecoded

		require.NoError(t, dd.DecodeInto(t.Context(), &got, opts, niceyaml.WithSelfValidation(false)))
		assert.Equal(t, hours{Open: "17:00", Close: "09:00"}, got.Custom.Inner)
		assert.Equal(t, hours{Open: "17:00", Close: "09:00"}, got.JSON.Inner)

		// SelfValidate binds each error at the same value under the same
		// options. Without them, it reads each type as one that decodes
		// field by field, so the errors have no value to bind at.
		require.EqualError(t, dd.SelfValidate(t.Context(), &got, opts), want)

		err = dd.SelfValidate(t.Context(), &got)
		require.EqualError(t, err, stringtest.JoinLF(
			"$.custom.inner.close: closes before it opens",
			"$.json.inner.close: closes before it opens",
		))
	})

	t.Run("a struct that gets UnmarshalJSON from an embedded field validates the field", func(t *testing.T) {
		t.Parallel()

		// The method of the embedded field decodes the document into that
		// field, so the field validates at the path of the struct, as a
		// field that gives its struct an UnmarshalYAML method does. The
		// hours the method fills thus report under the struct, with no
		// name of the field in their path.
		type embedsJSON struct {
			jsonDecodedHours

			Name string `yaml:"name"`
		}

		dd := yamltest.FirstDocument(t, "shop: anything\n")

		_, err := dd.Decode[map[string]*embedsJSON](t.Context(), niceyaml.WithJSONUnmarshalers(true))
		require.EqualError(t, err, "1:7: $.shop.inner.close: closes before it opens")
	})

	t.Run("a value a registered function decodes walks as its fields", func(t *testing.T) {
		t.Parallel()

		// Nothing shows which types yaml.RegisterCustomUnmarshaler gave a
		// function, so the walk reads the type as one that decodes field
		// by field. It reads the hours below the value as it does below
		// any type, at a path the document does not hold, and the error
		// has no value that decodes itself to bind at.
		yaml.RegisterCustomUnmarshaler(func(r *registeredHours, _ []byte) error {
			r.Inner = hours{Open: "17:00", Close: "09:00"}

			return nil
		})

		dd := yamltest.FirstDocument(t, "registered: anything\n")

		_, err := dd.Decode[struct {
			Registered registeredHours `yaml:"registered"`
		}](t.Context())
		require.EqualError(t, err, "$.registered.inner.close: closes before it opens")
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
		opened := `hours: {open: "09:00", close: "17:00"}` + "\n"
		dd := yamltest.FirstDocument(t, opened+stringtest.LinesLF(
			"spec:",
			"  a: 1",
		)+yamltest.GenerateLines(200, "k%d: v"))

		got, err := dd.Decode[withSpec](t.Context())
		require.NoError(t, err)
		require.NotNil(t, got.Hours)
		assert.True(t, got.Hours.seen)
		assert.IsType(t, &ast.MappingNode{}, got.Spec)

		// The walk reaches no value below a node, such as a validator a
		// tree built by hand holds.
		dd = yamltest.FirstDocument(t, opened)

		built := withSpec{Spec: &ast.MappingNode{
			Values: []*ast.MappingValueNode{{Value: walkedNode{}}},
		}}

		require.NoError(t, dd.DecodeInto(t.Context(), &built))
	})

	t.Run("a struct embedding an ast.Node validates its fields", func(t *testing.T) {
		t.Parallel()

		// Each struct gets the methods of an ast.Node from its embedded
		// field, but go-yaml decodes it field by field, so the walk reaches
		// the fields.
		type embedsNode struct {
			ast.Node `yaml:"-"`

			Hours hours `yaml:"hours"`
		}

		type embedsStringNode struct {
			*ast.StringNode `yaml:"-"`

			Hours hours `yaml:"hours"`
		}

		type parent struct {
			Node       embedsNode       `yaml:"node"`
			StringNode embedsStringNode `yaml:"string"`
		}

		tcs := map[string]struct {
			input string
			err   string
		}{
			"embedded ast.Node": {
				input: `node: {hours: {open: "17:00", close: "09:00"}}` + "\n",
				err:   "1:38: $.node.hours.close: closes before it opens",
			},
			"embedded *ast.StringNode": {
				input: `string: {hours: {open: "17:00", close: "09:00"}}` + "\n",
				err:   "1:40: $.string.hours.close: closes before it opens",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := yamltest.FirstDocument(t, tc.input).Decode[parent](t.Context())
				require.EqualError(t, err, tc.err)
			})
		}
	})
}

func TestDocument_Decode_SelfValidatorAnchors(t *testing.T) {
	t.Parallel()

	type config struct {
		Spec struct {
			Hours anchoredOpen `yaml:"hours"`
		} `yaml:"spec"`
	}

	tcs := map[string]struct {
		open string
		// The text FormatError gives the error the value returns, and the
		// message of the error a decode returns.
		wantUnbound string
		want        string
	}{
		"relative path": {
			open:        "current",
			wantUnbound: "@.open: bad open",
			want:        "c.yaml:3:11: $.spec.hours.open: bad open",
		},
		"absolute path": {
			open:        "doc",
			wantUnbound: "$.name: bad open",
			want:        "c.yaml:4:7: $.name: bad open",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t,
				stringtest.LinesLF("spec:", "  hours:", "    open: "+tc.open, "name: x"),
				niceyaml.WithName("c.yaml"),
			)

			unbound := anchoredOpen{Open: tc.open}.Validate()
			assert.Equal(t, tc.wantUnbound, niceyaml.FormatError(unbound, niceyaml.WithContextLines(0)))

			_, err := doc.Decode[config](t.Context())
			require.EqualError(t, err, tc.want)

			// A decode of the hours alone reports the same place.
			hours := yamltest.At(t, doc, paths.Doc().Child("spec", "hours"))

			_, err = hours.Decode[anchoredOpen](t.Context())
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestNode_Decode_SelfValidatorContext(t *testing.T) {
	t.Parallel()

	canceled := func(context.CancelFunc) error { return context.Canceled }

	// Each of the four elements runs the next step of the probe when it
	// validates. The error of a context that ended stops the walk and
	// comes back alone, with no location.
	tcs := map[string]struct {
		steps        []func(cancel context.CancelFunc) error
		cancelBefore bool
		calls        int32
		is           error
		err          string
	}{
		"a Validate returns the error of a context": {
			steps: []func(context.CancelFunc) error{canceled},
			calls: 1,
			is:    context.Canceled,
			err:   "cfg.yaml: context canceled",
		},
		"a Validate wraps the error of a context": {
			steps: []func(context.CancelFunc) error{
				func(context.CancelFunc) error {
					return fmt.Errorf("look up name: %w", context.DeadlineExceeded)
				},
			},
			calls: 1,
			is:    context.DeadlineExceeded,
			err:   "cfg.yaml: look up name: context deadline exceeded",
		},
		"a failure before the error of a context": {
			steps: []func(context.CancelFunc) error{
				func(context.CancelFunc) error { return niceyaml.NewError("unknown name") },
				canceled,
			},
			calls: 2,
			is:    context.Canceled,
			err:   "cfg.yaml: context canceled",
		},
		"the context ends during the walk": {
			steps: []func(context.CancelFunc) error{
				func(cancel context.CancelFunc) error {
					cancel()

					return nil
				},
			},
			calls: 1,
			is:    context.Canceled,
			err:   "cfg.yaml: context canceled",
		},
		"the context ends before the decode": {
			cancelBefore: true,
			calls:        0,
			is:           context.Canceled,
			err:          "cfg.yaml: context canceled",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, stringtest.LinesLF(
				"- a",
				"- b",
				"- c",
				"- d",
			), niceyaml.WithName("cfg.yaml"))

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			p := &probe{steps: tc.steps, cancel: cancel}
			ctx = context.WithValue(ctx, probeCtxKey{}, p)

			if tc.cancelBefore {
				cancel()
			}

			_, err := doc.Decode[[]probed](ctx)
			require.ErrorIs(t, err, tc.is)
			require.EqualError(t, err, tc.err)
			assert.Equal(t, tc.calls, p.calls.Load())

			bound := yamltest.RequireSourceError(t, err)

			_, ok := bound.Position()
			assert.False(t, ok)
			assert.Empty(t, yamltest.ProblemsOf(bound))
		})
	}
}

func ExampleNode_SelfValidate() {
	ctx := context.Background()

	doc, err := niceyaml.NewSourceFromString(`db:
  host: localhost
`, niceyaml.WithName("app.yaml")).Document()
	if err != nil {
		log.Fatal(err)
	}

	var cfg layeredConfig

	err = doc.DecodeInto(ctx, &cfg, niceyaml.WithSelfValidation(false))
	if err != nil {
		log.Fatal(err)
	}

	// The file leaves the password to the environment.
	fmt.Println(doc.SelfValidate(ctx, &cfg))

	applyEnv(&cfg, map[string]string{"DB_PASSWORD": "s3cret"})

	fmt.Println(doc.SelfValidate(ctx, &cfg))

	// Output:
	// app.yaml:1:1: $.db.password: password is required
	// <nil>
}

func TestNode_SelfValidate(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		db:
		  host: localhost
		upstreams:
		  a:
		    url: http://a
		items:
		  - url: http://x
		  - url: http://y
		port: 80
	`)

	// Each layer changes the value a decode with the walk off filled from
	// the file, as the environment, the flags, or the defaults of a
	// program would.
	tcs := map[string]struct {
		layer  func(cfg *layeredConfig)
		target func(cfg *layeredConfig) any
		cancel bool
		err    string
	}{
		"the file lacks a value no layer sets": {
			err: "app.yaml:1:1: $.db.password: password is required",
		},
		"a layer sets the value the file lacks": {
			layer: func(cfg *layeredConfig) {
				cfg.DB.Password = "s3cret"
			},
		},
		"an added map entry binds at the key of its map": {
			layer: func(cfg *layeredConfig) {
				cfg.DB.Password = "s3cret"
				cfg.Upstreams["c"] = upstream{URL: "ftp://c"}
			},
			err: `app.yaml:3:1: $.upstreams.c.url: url "ftp://c" is not http`,
		},
		"an added field binds at the mapping that lacks it": {
			layer: func(cfg *layeredConfig) {
				cfg.DB.Password = "s3cret"
				cfg.Backup = &database{Host: "backup"}
			},
			err: "app.yaml:1:1: $.backup.password: password is required",
		},
		"an appended element binds with no position": {
			layer: func(cfg *layeredConfig) {
				cfg.DB.Password = "s3cret"
				cfg.Items = append(cfg.Items, upstream{URL: "ftp://z"})
			},
			err: `app.yaml: $.items[2].url: url "ftp://z" is not http`,
		},
		"a prepended element marks the element the document holds there": {
			layer: func(cfg *layeredConfig) {
				cfg.DB.Password = "s3cret"
				cfg.Items = append([]upstream{{URL: "ftp://w"}}, cfg.Items...)
			},
			err: `app.yaml:7:10: $.items[0].url: url "ftp://w" is not http`,
		},
		"an overridden scalar marks the value the document holds": {
			layer: func(cfg *layeredConfig) {
				cfg.DB.Password = "s3cret"
				cfg.Port = 70000
			},
			err: "app.yaml:9:7: $.port: port out of range",
		},
		"a field go-yaml ignores does not validate": {
			layer: func(cfg *layeredConfig) {
				cfg.DB.Password = "s3cret"
				cfg.Ignored = database{}
			},
		},
		"a value that is no pointer validates": {
			target: func(cfg *layeredConfig) any {
				return *cfg
			},
			err: "app.yaml:1:1: $.db.password: password is required",
		},
		"a pointer to a nil pointer validates nothing": {
			target: func(*layeredConfig) any {
				var cfg *layeredConfig

				return &cfg
			},
		},
		"a context that ended stops the walk": {
			cancel: true,
			err:    "app.yaml: context canceled",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, input, niceyaml.WithName("app.yaml"))

			var cfg layeredConfig

			err := doc.DecodeInto(t.Context(), &cfg, niceyaml.WithSelfValidation(false))
			require.NoError(t, err)

			if tc.layer != nil {
				tc.layer(&cfg)
			}

			var target any = &cfg

			if tc.target != nil {
				target = tc.target(&cfg)
			}

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			if tc.cancel {
				cancel()
			}

			err = doc.SelfValidate(ctx, target)
			requireValidation(t, err, tc.err)
		})
	}
}

func TestNode_SelfValidate_Target(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		v   any
		err string
	}{
		"nil": {
			v:   nil,
			err: "app.yaml: self-validation target is nil",
		},
		"a nil pointer": {
			v:   (*layeredConfig)(nil),
			err: "app.yaml: self-validation target is nil: got *niceyaml_test.layeredConfig",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, "port: 80\n", niceyaml.WithName("app.yaml"))

			err := doc.SelfValidate(t.Context(), tc.v)
			require.ErrorIs(t, err, niceyaml.ErrSelfValidateTarget)
			require.EqualError(t, err, tc.err)

			bound := yamltest.RequireSourceError(t, err)

			_, ok := bound.Position()
			assert.False(t, ok)
		})
	}
}

func TestNode_SelfValidate_Node(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		db:
		  host: localhost
		upstreams:
		  a:
		    url: http://a
	`)

	// Scoped returns a function that scopes the document of the input to
	// one of its keys.
	scoped := func(key string) func(t *testing.T) *niceyaml.Node {
		return func(t *testing.T) *niceyaml.Node {
			t.Helper()

			doc := yamltest.FirstDocument(t, input, niceyaml.WithName("app.yaml"))

			return yamltest.At(t, doc, paths.Current().Child(key))
		}
	}

	tcs := map[string]struct {
		node       func(t *testing.T) *niceyaml.Node
		v          any
		unresolved error
		err        string
	}{
		"a scoped Node puts its path in front": {
			node: scoped("db"),
			v:    &database{Host: "localhost"},
			err:  "app.yaml:1:1: $.db.password: password is required",
		},
		"an entry added under a scoped Node binds at the key of its map": {
			node: scoped("upstreams"),
			v: &map[string]upstream{
				"a": {URL: "http://a"},
				"c": {URL: "ftp://c"},
			},
			err: `app.yaml:3:1: $.upstreams.c.url: url "ftp://c" is not http`,
		},
		"an empty document binds with no position": {
			node: func(t *testing.T) *niceyaml.Node {
				t.Helper()

				return yamltest.FirstDocument(t, "", niceyaml.WithName("env"))
			},
			v:          &layeredConfig{},
			unresolved: paths.ErrNotFound,
			err:        "env: $.db.password: password is required",
		},
		"a document that did not parse binds with no position": {
			node: func(t *testing.T) *niceyaml.Node {
				t.Helper()

				broken := yamltest.UnparsedDocument(t, "db: [\n", niceyaml.WithName("bad.yaml"))
				require.ErrorIs(t, broken.Err(), niceyaml.ErrSyntax)

				return broken
			},
			v:          &layeredConfig{},
			unresolved: niceyaml.ErrPathNeedsDocument,
			err:        "bad.yaml: $.db.password: password is required",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.node(t).SelfValidate(t.Context(), tc.v)
			require.EqualError(t, err, tc.err)

			if tc.unresolved == nil {
				return
			}

			bound := yamltest.RequireSourceError(t, err)

			_, ok := bound.Position()
			assert.False(t, ok)
			require.ErrorIs(t, bound.Unresolved(), tc.unresolved)
		})
	}
}

func TestNode_SelfValidate_KeyOptions(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		by_grade:
		  high: {url: http://h}
	`)

	// A grade decodes from its name only under gradeNames, so the key of
	// the document matches the key of the map only under that option.
	tcs := map[string]struct {
		opts []niceyaml.DecodeOption
		err  string
	}{
		"the options of the decode match the key of the document": {
			opts: []niceyaml.DecodeOption{gradeNames},
			err:  `app.yaml:2:15: $.by_grade.high.url: url "ftp://h" is not http`,
		},
		"without them, the error binds at the key of the map": {
			err: `app.yaml:1:1: $.by_grade.3.url: url "ftp://h" is not http`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, input, niceyaml.WithName("app.yaml"))

			var cfg gradedConfig

			err := doc.DecodeInto(t.Context(), &cfg, gradeNames, niceyaml.WithSelfValidation(false))
			require.NoError(t, err)

			cfg.ByGrade[gradeHigh] = upstream{URL: "ftp://h"}

			err = doc.SelfValidate(t.Context(), &cfg, tc.opts...)
			require.EqualError(t, err, tc.err)
		})
	}
}

func TestNode_SelfValidate_MatchesDecode(t *testing.T) {
	t.Parallel()

	// A decode with the walk on fills one new value, and a decode with the
	// walk off fills another, which SelfValidate then checks. Both report
	// err for the node the at path leads to.
	tcs := map[string]struct {
		value  func() any
		input  string
		at     paths.Path
		source []niceyaml.SourceOption
		opts   []niceyaml.DecodeOption
		err    string
	}{
		"nested fields, elements, and entries": {
			value: func() any { return new(nested) },
			input: stringtest.Input(`
				hours: {open: "09:00", close: "08:00"}
				items:
				  - price: -1
				by_name:
				  scone: {price: -2}
				by_id:
				  0x10: {price: -3}
			`),
			err: stringtest.JoinLF(
				"1:31: $.hours.close: closes before it opens",
				"3:12: $.items[0].price: negative price",
				"5:18: $.by_name.scone.price: negative price",
				"7:17: $.by_id.0x10.price: negative price",
			),
		},
		"keys the document spells in another form": {
			value: func() any { return new(gradedConfig) },
			input: stringtest.Input(`
				by_weight:
				  1.50: {url: ftp://w}
				  2e0: {url: ftp://x}
			`),
			err: stringtest.JoinLF(
				`2:15: $.by_weight.'1.50'.url: url "ftp://w" is not http`,
				`3:14: $.by_weight.2e0.url: url "ftp://x" is not http`,
			),
		},
		"keys that share a path": {
			value: func() any { return new(gradedConfig) },
			input: stringtest.Input(`
				base: &base {1: {url: ftp://a}}
				by_any:
				  <<: *base
				  "1": {url: ftp://b}
			`),
			err: stringtest.JoinLF(
				`$.by_any.1.url: url "ftp://b" is not http`,
				`$.by_any.1.url: url "ftp://a" is not http`,
			),
		},
		"keys that a custom unmarshaler decodes": {
			value: func() any { return new(gradedConfig) },
			input: stringtest.Input(`
				by_grade:
				  high: {url: ftp://h}
			`),
			opts: []niceyaml.DecodeOption{gradeNames},
			err:  `2:15: $.by_grade.high.url: url "ftp://h" is not http`,
		},
		// SelfValidate decodes the keys with the references of the source,
		// as the decode does, and the caller passes them to neither.
		"keys beside a merge of a reference document": {
			value: func() any { return new(nested) },
			input: stringtest.Input(`
				by_id:
				  <<: *r
				  0x10: {price: -1}
			`),
			source: []niceyaml.SourceOption{
				niceyaml.WithReferences(niceyaml.NewSourceFromString("r: &r {16: {price: 1}, 32: {price: -2}}\n")),
			},
			err: stringtest.JoinLF(
				"3:17: $.by_id.0x10.price: negative price",
				"$.by_id.32.price: negative price",
			),
		},
		"key an alias reads from a reference document": {
			value: func() any { return new(nested) },
			input: stringtest.LinesLF(
				"by_id:",
				"  *k : {price: -1}",
			),
			source: []niceyaml.SourceOption{
				niceyaml.WithReferences(niceyaml.NewSourceFromString("k: &k 0x10\n")),
			},
			err: "1:1: $.by_id.16.price: negative price",
		},
		// The document holds no line for the value, so the error binds at
		// the alias.
		"values an alias reads from a reference document": {
			value: func() any { return new(nested) },
			input: "by_id: *ids\n",
			source: []niceyaml.SourceOption{
				niceyaml.WithReferences(niceyaml.NewSourceFromString("ids: &ids {0x10: {price: -3}}\n")),
			},
			err: "1:8: $.by_id.16.price: negative price",
		},
		"a scoped Node": {
			value: func() any { return new(nested) },
			input: stringtest.Input(`
				spec:
				  hours: {open: "09:00", close: "08:00"}
			`),
			at:  paths.Current().Child("spec"),
			err: "2:33: $.spec.hours.close: closes before it opens",
		},
		"nothing fails": {
			value: func() any { return new(nested) },
			input: `hours: {open: "09:00", close: "17:00"}` + "\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			n := yamltest.AtOrSelf(t, yamltest.FirstDocument(t, tc.input, tc.source...), tc.at)

			decoded := n.DecodeInto(t.Context(), tc.value(), tc.opts...)

			v := tc.value()
			off := append([]niceyaml.DecodeOption{niceyaml.WithSelfValidation(false)}, tc.opts...)
			require.NoError(t, n.DecodeInto(t.Context(), v, off...))

			validated := n.SelfValidate(t.Context(), v, tc.opts...)
			if tc.err == "" {
				require.NoError(t, decoded)
				require.NoError(t, validated)

				return
			}

			require.EqualError(t, decoded, tc.err)
			require.Error(t, validated)
			assert.Equal(t, decoded.Error(), validated.Error())
			assert.Equal(
				t,
				niceyaml.FormatError(decoded, niceyaml.WithContextLines(1)),
				niceyaml.FormatError(validated, niceyaml.WithContextLines(1)),
			)
		})
	}
}

func TestNode_SelfValidate_ValueChecks(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		services:
		  0x10:
		    addr: 127.0.0.1
	`), niceyaml.WithName("app.yaml"))

	doc := yamltest.Document(t, source)
	noLoopback := niceyaml.WithValueCheck(rejectLoopback)

	// The decode turns the walk off, so it runs no check either.
	var cfg checkedServices

	err := doc.DecodeInto(t.Context(), &cfg, noLoopback, niceyaml.WithSelfValidation(false))
	require.NoError(t, err)

	tcs := map[string]struct {
		validate func(ctx context.Context) error
		err      string
	}{
		"Node.SelfValidate with the check": {
			validate: func(ctx context.Context) error { return doc.SelfValidate(ctx, &cfg, noLoopback) },
			err:      "app.yaml:3:11: $.services.0x10.addr: loopback address 127.0.0.1",
		},
		"Node.SelfValidate without the check": {
			validate: func(ctx context.Context) error { return doc.SelfValidate(ctx, &cfg) },
		},
		"Source.SelfValidate with the check": {
			validate: func(ctx context.Context) error { return source.SelfValidate(ctx, &cfg, noLoopback) },
			err:      "app.yaml:3:11: $.services.0x10.addr: loopback address 127.0.0.1",
		},
		"Source.SelfValidate without the check": {
			validate: func(ctx context.Context) error { return source.SelfValidate(ctx, &cfg) },
		},
		"a scoped Node with the check": {
			validate: func(ctx context.Context) error {
				svc := cfg.Services[16]

				return yamltest.At(t, doc, paths.Doc().Child("services", "0x10")).SelfValidate(ctx, &svc, noLoopback)
			},
			err: "app.yaml:3:11: $.services.0x10.addr: loopback address 127.0.0.1",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.validate(t.Context())
			if tc.err == "" {
				require.NoError(t, err)

				return
			}

			require.EqualError(t, err, tc.err)
			assert.True(t, niceyaml.IsInvalid(err))
		})
	}
}

func ExampleSelfValidateValue() {
	ctx := context.Background()

	// The value came from no document, as the body of a request does.
	cfg := layeredConfig{
		DB:    database{Host: "localhost", Password: "s3cret"},
		Items: []upstream{{URL: "http://x"}, {URL: "ftp://y"}},
	}

	err := niceyaml.SelfValidateValue(ctx, &cfg)
	fmt.Println(err)

	// A document that holds the value under a key places the error later.
	doc, docErr := niceyaml.NewSourceFromString(`config:
  items:
    - url: http://x
    - url: ftp://y
`, niceyaml.WithName("app.yaml")).Document()
	if docErr != nil {
		log.Fatal(docErr)
	}

	fmt.Println(doc.Bind(niceyaml.Rebase(err, paths.Doc().Child("config"))))

	// Output:
	// $.items[1].url: url "ftp://y" is not http
	// app.yaml:4:12: $.config.items[1].url: url "ftp://y" is not http
}

func TestSelfValidateValue(t *testing.T) {
	t.Parallel()

	// A value that passes every check, which each case changes.
	valid := func() *layeredConfig {
		return &layeredConfig{DB: database{Host: "localhost", Password: "s3cret"}}
	}

	tcs := map[string]struct {
		value  func() any
		cancel bool
		err    string
	}{
		"a value that passes": {
			value: func() any { return valid() },
		},
		"a field reports its path from the value": {
			value: func() any {
				cfg := valid()
				cfg.DB.Password = ""

				return cfg
			},
			err: "$.db.password: password is required",
		},
		"an element reports its index": {
			value: func() any {
				cfg := valid()
				cfg.Items = []upstream{{URL: "http://x"}, {URL: "ftp://y"}}

				return cfg
			},
			err: `$.items[1].url: url "ftp://y" is not http`,
		},
		"a map entry reports the text of its Go key": {
			value: func() any {
				return &gradedConfig{ByGrade: map[grade]upstream{gradeHigh: {URL: "ftp://g"}}}
			},
			err: `$.by_grade.3.url: url "ftp://g" is not http`,
		},
		"several values report together": {
			value: func() any {
				cfg := valid()
				cfg.DB.Password = ""
				cfg.Upstreams = map[string]upstream{"a": {URL: "ftp://a"}}
				cfg.Port = 70000

				return cfg
			},
			err: stringtest.JoinLF(
				"$.db.password: password is required",
				`$.upstreams.a.url: url "ftp://a" is not http`,
				"$.port: port out of range",
			),
		},
		"a field go-yaml ignores does not validate": {
			value: func() any {
				cfg := valid()
				cfg.Ignored = database{}

				return cfg
			},
		},
		"the values below a type that decodes itself report their paths": {
			value: func() any {
				return map[string]any{
					"servers": []defaultedServer{{Host: "a"}, {Host: "b", Port: 70000}},
					"limits":  sortedLimits{{N: 1}, {N: -1}},
				}
			},
			err: stringtest.JoinLF(
				"$.limits[1].n: negative -1",
				"$.servers[1].port: port out of range",
			),
		},
		"a value that is no pointer validates": {
			value: func() any {
				cfg := valid()
				cfg.DB.Password = ""

				return *cfg
			},
			err: "$.db.password: password is required",
		},
		"a pointer to a nil pointer validates nothing": {
			value: func() any {
				var cfg *layeredConfig

				return &cfg
			},
		},
		"a context that ended stops the walk": {
			value: func() any {
				cfg := valid()
				cfg.DB.Password = ""

				return cfg
			},
			cancel: true,
			err:    "context canceled",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			if tc.cancel {
				cancel()
			}

			err := niceyaml.SelfValidateValue(ctx, tc.value())
			if tc.err == "" {
				require.NoError(t, err)

				return
			}

			require.EqualError(t, err, tc.err)
			assert.Equal(t, !tc.cancel, niceyaml.IsInvalid(err))

			// The result stands in no document, as one from BindValue does.
			for _, bound := range yamltest.BoundIn(err) {
				_, ok := bound.Node()
				assert.False(t, ok)

				_, ok = bound.Document()
				assert.False(t, ok)

				_, ok = bound.Position()
				assert.False(t, ok)
			}
		})
	}
}

func TestSelfValidateValue_Target(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		v   any
		err string
	}{
		"nil": {
			v:   nil,
			err: "self-validation target is nil",
		},
		"a nil pointer": {
			v:   (*layeredConfig)(nil),
			err: "self-validation target is nil: got *niceyaml_test.layeredConfig",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := niceyaml.SelfValidateValue(t.Context(), tc.v)
			require.ErrorIs(t, err, niceyaml.ErrSelfValidateTarget)
			require.EqualError(t, err, tc.err)
			assert.False(t, niceyaml.IsInvalid(err))

			bound := yamltest.RequireSourceError(t, err)

			_, ok := bound.Node()
			assert.False(t, ok)

			_, ok = bound.Position()
			assert.False(t, ok)
		})
	}
}

func TestSelfValidateValue_SharedPath(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		value func() any
		want  []string
	}{
		"keys of two types with one text": {
			value: func() any {
				return map[any]signed{1: {N: -1}, "1": {N: -2}}
			},
			want: []string{"$.1.n: negative -1", "$.1.n: negative -2"},
		},
		"several NaN keys": {
			value: func() any {
				m := map[float64]signed{}
				m[math.NaN()] = signed{N: -1}
				m[math.NaN()] = signed{N: -2}

				return m
			},
			want: []string{"$.NaN.n: negative -1", "$.NaN.n: negative -2"},
		},
		"a map below such a key": {
			value: func() any {
				return map[any]map[string]signed{
					1:   {"a": {N: -1}},
					"1": {"a": {N: -2}},
				}
			},
			want: []string{"$.1.a.n: negative -1", "$.1.a.n: negative -2"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The map iterates in a new order on each walk.
			for range 20 {
				err := niceyaml.SelfValidateValue(t.Context(), tc.value())
				require.Error(t, err)
				assert.Equal(t, tc.want, ambiguousTexts(t, err))
			}
		})
	}
}

func TestSelfValidateValue_Place(t *testing.T) {
	t.Parallel()

	// Each value stands under the request key of its document. The walk
	// held no document, so Rebase and Bind place its errors there.
	type request struct {
		ByPort map[int]upstream `yaml:"by_port"`
		ByAny  map[any]signed   `yaml:"by_any"`
		Items  []upstream       `yaml:"items"`
	}

	tcs := map[string]struct {
		input string
		// The error through the Node of the value, which reads each key
		// as the document spells it, when it differs from want.
		through string
		want    string
		opts    []niceyaml.SourceOption
		value   request
		shared  bool
	}{
		"an element places at its line": {
			input: stringtest.LinesLF(
				"request:",
				"  items:",
				"    - url: http://x",
				"    - url: ftp://y",
			),
			value: request{Items: []upstream{{URL: "http://x"}, {URL: "ftp://y"}}},
			want:  `app.yaml:4:12: $.request.items[1].url: url "ftp://y" is not http`,
		},
		"an element the document lacks places with no position": {
			input: stringtest.LinesLF(
				"request:",
				"  items:",
				"    - url: http://x",
			),
			value: request{Items: []upstream{{URL: "http://x"}, {URL: "ftp://y"}}},
			want:  `app.yaml: $.request.items[1].url: url "ftp://y" is not http`,
		},
		"a key the document spells as the Go value places at its line": {
			input: stringtest.LinesLF(
				"request:",
				"  by_port:",
				"    16:",
				"      url: ftp://y",
			),
			value: request{ByPort: map[int]upstream{16: {URL: "ftp://y"}}},
			want:  `app.yaml:4:12: $.request.by_port.16.url: url "ftp://y" is not http`,
		},
		"a key the document spells another way places at the key of its map": {
			input: stringtest.LinesLF(
				"request:",
				"  by_port:",
				"    0x10:",
				"      url: ftp://y",
			),
			value:   request{ByPort: map[int]upstream{16: {URL: "ftp://y"}}},
			want:    `app.yaml:2:3: $.request.by_port.16.url: url "ftp://y" is not http`,
			through: `app.yaml:4:12: $.request.by_port.0x10.url: url "ftp://y" is not http`,
		},
		"keys that share a path place with no position": {
			input: stringtest.LinesLF(
				"request:",
				"  by_any:",
				"    1: {n: -1}",
				`    "1": {n: -2}`,
			),
			opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			value: request{ByAny: map[any]signed{1: {N: -1}, "1": {N: -2}}},
			want: stringtest.JoinLF(
				"app.yaml: $.request.by_any.1.n: negative -1",
				"app.yaml: $.request.by_any.1.n: negative -2",
			),
			shared: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := append([]niceyaml.SourceOption{niceyaml.WithName("app.yaml")}, tc.opts...)
			doc := yamltest.FirstDocument(t, tc.input, opts...)
			base := paths.Doc().Child("request")

			err := niceyaml.SelfValidateValue(t.Context(), &tc.value)
			require.Error(t, err)

			placed := doc.Bind(niceyaml.Rebase(err, base))
			require.EqualError(t, placed, tc.want)
			assert.True(t, niceyaml.IsInvalid(placed))

			for _, bound := range yamltest.BoundIn(placed) {
				assert.Same(t, doc.Source(), bound.Source())

				if _, ok := bound.Path(); ok && tc.shared {
					require.ErrorIs(t, bound.Unresolved(), niceyaml.ErrAmbiguousPath)
				}
			}

			// Placing leaves the result as it is, so it still names each
			// path from the value.
			assert.NotContains(t, err.Error(), "app.yaml")

			through := yamltest.At(t, doc, base).SelfValidate(t.Context(), &tc.value)
			require.EqualError(t, through, cmp.Or(tc.through, tc.want))
		})
	}
}

func TestSelfValidateValue_Options(t *testing.T) {
	t.Parallel()

	// CheckedRing leads from each element to the next, and the last
	// element leads back to the first.
	type checkedRing struct {
		Next   *checkedRing  `yaml:"next"`
		Mirror checkedMirror `yaml:"mirror"`
	}

	t.Run("runs the checks it gets", func(t *testing.T) {
		t.Parallel()

		// The value of the first two cases, which holds one mirror.
		services := func() any {
			return &checkedServices{Services: map[int]checkedService{16: {Mirror: checkedMirror{URL: "ftp://m"}}}}
		}

		tcs := map[string]struct {
			value func() any
			err   string
			calls int
			check bool
		}{
			"a map entry reports the text of its Go key": {
				value: services,
				check: true,
				err:   `$.services.16.mirror.url: url "ftp://m" is not http`,
				calls: 1,
			},
			"no check without the option": {
				value: services,
			},
			"a value two paths share checks once, under the first": {
				value: func() any {
					m := &checkedMirror{URL: "ftp://m"}

					return &struct {
						A *checkedMirror `yaml:"a"`
						B *checkedMirror `yaml:"b"`
					}{A: m, B: m}
				},
				check: true,
				err:   `$.a.url: url "ftp://m" is not http`,
				calls: 1,
			},
			"a cycle checks each value once": {
				value: func() any {
					first := &checkedRing{Mirror: checkedMirror{URL: "ftp://a"}}
					first.Next = &checkedRing{Mirror: checkedMirror{URL: "http://b"}, Next: first}

					return first
				},
				check: true,
				err:   `$.mirror.url: url "ftp://a" is not http`,
				calls: 2,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var (
					calls int
					opts  []niceyaml.DecodeOption
				)

				if tc.check {
					opts = append(opts, niceyaml.WithValueCheck(func(ctx context.Context, m *checkedMirror) error {
						calls++

						return requireHTTP(ctx, m)
					}))
				}

				err := niceyaml.SelfValidateValue(t.Context(), tc.value(), opts...)
				assert.Equal(t, tc.calls, calls)

				if tc.err == "" {
					require.NoError(t, err)

					return
				}

				require.EqualError(t, err, tc.err)
				assert.True(t, niceyaml.IsInvalid(err))
			})
		}
	})

	// A function from WithCustomUnmarshaler sorts the numbers, so their
	// indices need not match the document, and an error below them binds
	// at the list.
	t.Run("reads the unmarshalers it gets", func(t *testing.T) {
		t.Parallel()

		type sortedNums []int

		type request struct {
			Nums sortedNums `yaml:"nums"`
		}

		sorted := niceyaml.WithCustomUnmarshaler(func(_ context.Context, s *sortedNums, decode func(any) error) error {
			var nums []int

			err := decode(&nums)
			if err != nil {
				return err
			}

			slices.Sort(nums)

			*s = nums

			return nil
		})

		small := niceyaml.WithValueCheck(func(_ context.Context, n *int) error {
			if *n > 2 {
				return niceyaml.NewError(fmt.Sprintf("%d is more than 2", *n))
			}

			return nil
		})

		tcs := map[string]struct {
			want string
			opts []niceyaml.DecodeOption
		}{
			"with the unmarshaler": {
				opts: []niceyaml.DecodeOption{small, sorted},
				want: "app.yaml:2:3: $.request.nums[2]: 3 is more than 2",
			},
			"without the unmarshaler": {
				opts: []niceyaml.DecodeOption{small},
				want: "app.yaml:2:16: $.request.nums[2]: 3 is more than 2",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, "request:\n  nums: [3, 1, 2]\n", niceyaml.WithName("app.yaml"))
				value := request{Nums: sortedNums{1, 2, 3}}

				err := niceyaml.SelfValidateValue(t.Context(), &value, tc.opts...)
				require.EqualError(t, err, "$.nums[2]: 3 is more than 2")

				placed := doc.Bind(niceyaml.Rebase(err, paths.Doc().Child("request")))
				require.EqualError(t, placed, tc.want)
			})
		}
	})
}

func TestSelfValidateValue_Validate(t *testing.T) {
	t.Parallel()

	// A decode skips the field pool tags `-`, so the Validate of pool
	// walks its upstreams. That walk holds no document, and the decode
	// places what it returns at the pool.
	type config struct {
		Pool  *pool  `yaml:"pool"`
		Pools []pool `yaml:"pools"`
	}

	tcs := map[string]struct {
		input string
		err   string
	}{
		"upstreams that pass": {
			input: stringtest.Input(`
				pool:
				  spec:
				    upstreams:
				      - url: http://a
			`),
		},
		"an upstream reports its line and the path from the root": {
			input: stringtest.Input(`
				pool:
				  spec:
				    upstreams:
				      - url: http://a
				      - url: ftp://b
			`),
			err: `app.yaml:5:14: $.pool.spec.upstreams[1].url: url "ftp://b" is not http`,
		},
		"each pool of a sequence reports under its own index": {
			input: stringtest.Input(`
				pools:
				  - spec:
				      upstreams:
				        - url: ftp://a
				  - spec:
				      upstreams:
				        - url: http://b
				        - url: ftp://c
			`),
			err: stringtest.JoinLF(
				`app.yaml:4:16: $.pools[0].spec.upstreams[0].url: url "ftp://a" is not http`,
				`app.yaml:8:16: $.pools[1].spec.upstreams[1].url: url "ftp://c" is not http`,
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithName("app.yaml"))

			var cfg config

			err := doc.DecodeInto(t.Context(), &cfg)
			if tc.err == "" {
				require.NoError(t, err)

				return
			}

			require.EqualError(t, err, tc.err)
			assert.True(t, niceyaml.IsInvalid(err))

			// The walk on its own reports what the decode reports.
			validated := doc.SelfValidate(t.Context(), &cfg)
			require.EqualError(t, validated, tc.err)
		})
	}
}

func TestNode_Decode_SelfDecoding(t *testing.T) {
	t.Parallel()

	type nestedFleet struct {
		Servers []defaultedServer `yaml:"servers"`
	}

	type withFleet struct {
		Fleet growingFleet `yaml:"fleet"`
	}

	type withReversed struct {
		Fleet reversedFleet `yaml:"fleet"`
	}

	type withAddr struct {
		Name string   `yaml:"name"`
		Addr hostPort `yaml:"addr"`
	}

	type withPeers struct {
		Peers hostPorts `yaml:"peers"`
	}

	type withLazy struct {
		Lazy lazyServer `yaml:"lazy"`
	}

	type withHours struct {
		Label string     `yaml:"label"`
		Hours rangeHours `yaml:"hours"`
	}

	type withShared struct {
		Label string         `yaml:"label"`
		Hours sharedKeysList `yaml:"hours"`
	}

	type withSpec struct {
		Spec nodeSpec `yaml:"spec"`
	}

	// A fleet whose servers fail four checks, at four lines.
	fleetInput := stringtest.Input(`
		servers:
		  - {host: a, limit: {n: -1}}
		  - host: b
		    port: 70000
		    limit:
		      n: -2
		  - host: c
		    hours:
		      open: "17:00"
		      close: "09:00"
	`)

	fleetErr := stringtest.JoinLF(
		"app.yaml:2:26: $.servers[0].limit.n: negative -1",
		"app.yaml:4:11: $.servers[1].port: port out of range",
		"app.yaml:6:10: $.servers[1].limit.n: negative -2",
		"app.yaml:10:14: $.servers[2].hours.close: closes before it opens",
	)

	// Two stores, each a mapping whose type key names its type.
	storesInput := stringtest.Input(`
		stores:
		  - type: bucket
		    name: ""
		  - type: bucket
		    name: b
		    limit:
		      n: -3
	`)

	// The stores report at the box when no tag says where the store sits.
	boxErr := stringtest.JoinLF(
		"app.yaml:2:3: $.stores[0].store.name: name is required",
		"app.yaml:4:3: $.stores[1].store.limit.n: negative -3",
	)

	boxNearest := map[string]string{
		"$.stores[0].store.name":    "$.stores[0]",
		"$.stores[1].store.limit.n": "$.stores[1]",
	}

	storesErr := stringtest.JoinLF(
		"app.yaml:3:11: $.stores[0].name: name is required",
		"app.yaml:7:10: $.stores[1].limit.n: negative -3",
	)

	// Each target holds a value whose type decodes itself. The nearest
	// paths are those SourceError.Nearest returns, by the path of each
	// error that bound at a value above the one its path names.
	tcs := map[string]struct {
		target  func() any
		nearest map[string]string
		input   string
		err     string
	}{
		"a struct that decodes field by field": {
			target: func() any { return new(fleet) },
			input:  fleetInput,
			err:    fleetErr,
		},
		"a defaults method below a list reports the same lines": {
			target: func() any { return new(nestedFleet) },
			input:  fleetInput,
			err:    fleetErr,
		},
		"a defaults method at the root reports the same lines": {
			target: func() any { return new(defaultedFleet) },
			input:  fleetInput,
			err:    fleetErr,
		},
		"a Validate that walks the fields of its struct reports each once": {
			target: func() any { return new(map[string][]rewalkedServer) },
			input:  fleetInput,
			err:    fleetErr,
		},
		"values that pass": {
			target: func() any { return new(nestedFleet) },
			input: stringtest.LinesLF(
				"servers:",
				"  - {host: a, limit: {n: 1}}",
			),
		},
		"a struct parsed from text reports its own error at the text": {
			target:  func() any { return new(withAddr) },
			input:   "name: x\naddr: db:0\n",
			err:     "app.yaml:2:7: $.addr.port: port must be at least 1",
			nearest: map[string]string{"$.addr.port": "$.addr"},
		},
		"a value below a struct parsed from text binds at the text": {
			target:  func() any { return new(withAddr) },
			input:   "name: x\naddr: db:70000\n",
			err:     "app.yaml:2:7: $.addr.port: port out of range",
			nearest: map[string]string{"$.addr.port": "$.addr"},
		},
		"a value binds at the nearest value above it that the document holds": {
			target:  func() any { return new(withPeers) },
			input:   "peers: a:1,b:0\n",
			err:     "app.yaml:1:8: $.peers.addrs[1].port: port must be at least 1",
			nearest: map[string]string{"$.peers.addrs[1].port": "$.peers"},
		},
		"an element the method adds binds at its struct": {
			target: func() any { return new(withFleet) },
			input: stringtest.LinesLF(
				"fleet:",
				"  servers:",
				"    - host: a",
			),
			err:     "app.yaml:1:1: $.fleet.servers[1].port: port out of range",
			nearest: map[string]string{"$.fleet.servers[1].port": "$.fleet"},
		},
		"an element the method moves reports at the element of its index": {
			target: func() any { return new(withReversed) },
			input: stringtest.LinesLF(
				"fleet:",
				"  servers:",
				"    - {host: a, port: 80}",
				"    - {host: b, port: 70000}",
			),
			err: "app.yaml:3:23: $.fleet.servers[0].port: port out of range",
		},
		"a field the method leaves unset validates": {
			target:  func() any { return new(withLazy) },
			input:   "lazy: later\n",
			err:     "app.yaml:1:7: $.lazy.server.password: password is required",
			nearest: map[string]string{"$.lazy.server.password": "$.lazy"},
		},
		"a summary, a detail, and a path from the root": {
			target: func() any { return new(withHours) },
			input:  "label: x\nhours: 17-09\n",
			err: stringtest.JoinLF(
				"app.yaml: invalid hours",
				"app.yaml:1:8: $.label: see the label",
				"app.yaml:2:8: $.hours.open: opens late",
				"app.yaml:2:8: $.hours: closes early",
			),
			nearest: map[string]string{"$.hours.open": "$.hours", "$.hours.close": "$.hours"},
		},
		"a sorted list binds at the list": {
			target: func() any { return new(map[string]sortedLimits) },
			input: stringtest.LinesLF(
				"limits:",
				"  - {n: 3}",
				"  - {n: -1}",
			),
			err:     "app.yaml:1:1: $.limits[0].n: negative -1",
			nearest: map[string]string{"$.limits[0].n": "$.limits"},
		},
		"a list with an element in front binds at the list": {
			target: func() any { return new(map[string]leadingLimits) },
			input: stringtest.LinesLF(
				"limits:",
				"  - {n: 2}",
				"  - {n: -1}",
			),
			err:     "app.yaml:1:1: $.limits[2].n: negative -1",
			nearest: map[string]string{"$.limits[2].n": "$.limits"},
		},
		"a swapped array binds at the array": {
			target:  func() any { return new(map[string]swappedLimits) },
			input:   "limits: [{n: -1}, {n: 2}]\n",
			err:     "app.yaml:1:1: $.limits[1].n: negative -1",
			nearest: map[string]string{"$.limits[1].n": "$.limits"},
		},
		"a map built from a list binds at the list": {
			target: func() any { return new(map[string]serversByHost) },
			input: stringtest.LinesLF(
				"servers:",
				"  - {host: a, port: 70000}",
			),
			err:     "app.yaml:1:1: $.servers.a.port: port out of range",
			nearest: map[string]string{"$.servers.a.port": "$.servers"},
		},
		"keys that share a path below a map bind at the map": {
			target: func() any { return new(map[string]limitsByAny) },
			input: stringtest.LinesLF(
				"limits:",
				"  - {n: -1}",
				"  - {n: -2}",
			),
			err: stringtest.JoinLF(
				"app.yaml:1:1: $.limits.1.n: negative -1",
				"app.yaml:1:1: $.limits.1.n: negative -2",
			),
			nearest: map[string]string{"$.limits.1.n": "$.limits"},
		},
		"keys that share a path below a Validate of a list bind at the list": {
			target: func() any { return new(withShared) },
			input: stringtest.LinesLF(
				"label: x",
				"hours:",
				"  - 17-09,-2",
			),
			err: stringtest.JoinLF(
				"app.yaml:2:1: $.hours[0].1.n: negative -2",
				"app.yaml: invalid hours",
				"app.yaml:1:8: $.label: see the label",
				"app.yaml:2:1: $.hours[0].1.open: opens late",
				"app.yaml:2:1: $.hours[0].1: closes early",
			),
			nearest: map[string]string{
				"$.hours[0].1":       "$.hours",
				"$.hours[0].1.open":  "$.hours",
				"$.hours[0].1.close": "$.hours",
				"$.hours[0].1.n":     "$.hours",
			},
		},
		"a store in a field with no tag binds at the box, once": {
			target:  func() any { return new(map[string][]storeBox) },
			input:   storesInput,
			err:     boxErr,
			nearest: boxNearest,
		},
		"a store in an inline field reports its lines": {
			target: func() any { return new(map[string][]inlineStoreBox) },
			input:  storesInput,
			err:    storesErr,
		},
		"a store in a skipped field reports the lines its Validate finds": {
			target: func() any { return new(map[string][]skippedStoreBox) },
			input:  storesInput,
			err:    storesErr,
		},
		"a node below a struct that decodes itself validates nothing": {
			target: func() any {
				return &withSpec{Spec: nodeSpec{Node: &ast.MappingNode{
					Values: []*ast.MappingValueNode{{Value: walkedNode{}}},
				}}}
			},
			input: `spec: {hours: {open: "09:00", close: "17:00"}}` + "\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithName("app.yaml"))

			// A walk on its own checks a value that a decode with the walk
			// off filled.
			v := tc.target()
			require.NoError(t, doc.DecodeInto(t.Context(), v, niceyaml.WithSelfValidation(false)))

			err := doc.DecodeInto(t.Context(), tc.target())
			if tc.err == "" {
				require.NoError(t, err)
				require.NoError(t, doc.SelfValidate(t.Context(), v))
				require.NoError(t, niceyaml.SelfValidateValue(t.Context(), v))

				return
			}

			require.EqualError(t, err, tc.err)
			assert.True(t, niceyaml.IsInvalid(err))

			var nearest map[string]string

			for _, bound := range yamltest.BoundIn(err) {
				near, ok := bound.Nearest()
				if !ok {
					continue
				}

				// The error has a position, and no reason it lacks one.
				_, located := bound.Position()
				assert.True(t, located)
				require.NoError(t, bound.Unresolved())

				if nearest == nil {
					nearest = map[string]string{}
				}

				nearest[errorPath(t, bound).String()] = near.String()
			}

			assert.Equal(t, tc.nearest, nearest)

			// Node.SelfValidate reports what the decode reports, and so does
			// a document that places the result of SelfValidateValue.
			require.EqualError(t, doc.SelfValidate(t.Context(), v), tc.err)

			unplaced := niceyaml.SelfValidateValue(t.Context(), v)
			require.Error(t, unplaced)
			assert.NotContains(t, unplaced.Error(), "app.yaml")
			require.EqualError(t, doc.Bind(unplaced), tc.err)
		})
	}
}

// selfDecodingBytes decodes itself from the YAML bytes, so its fields
// need not mirror the document. It fills Inner with hours that close
// before they open, at a path the document does not hold.
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

// optionDecodedHours has no unmarshaler method. A function from
// niceyaml.WithCustomUnmarshaler decodes it, so its fields need not
// mirror the document.
type optionDecodedHours struct {
	Inner hours `yaml:"inner"`
}

// jsonDecodedHours decodes itself from JSON under
// niceyaml.WithJSONUnmarshalers, so its fields need not mirror the
// document. It fills Inner with hours that close before they open, at a
// path the document does not hold.
type jsonDecodedHours struct {
	Inner hours `yaml:"inner"`
}

func (j *jsonDecodedHours) UnmarshalJSON([]byte) error {
	j.Inner = hours{Open: "17:00", Close: "09:00"}

	return nil
}

// registeredHours has no unmarshaler method. A function from
// yaml.RegisterCustomUnmarshaler decodes it in every decode of the
// program.
type registeredHours struct {
	Inner hours `yaml:"inner"`
}

// failingSelfDecoding decodes itself and rejects itself. The hours it
// fills pass, so its own Validate runs, at its own path.
type failingSelfDecoding struct {
	Inner hours
}

func (f *failingSelfDecoding) UnmarshalYAML([]byte) error {
	f.Inner = hours{Open: "09:00", Close: "17:00"}

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

// sharedParent validates after its field, which an alias can make the
// same pointer as the field of another sharedParent, and reports that it
// ran.
type sharedParent struct {
	X *item `yaml:"x"`
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

// requireValidation requires err to have the message want, and to be nil
// when want is empty.
func requireValidation(t *testing.T, err error, want string) {
	t.Helper()

	if want == "" {
		require.NoError(t, err)

		return
	}

	require.EqualError(t, err, want)
}

// validationTexts returns the message of each problem that err heads, or
// the message of err when it heads none.
func validationTexts(err error) []string {
	if texts := errorTexts(yamltest.ProblemsOf(err)); texts != nil {
		return texts
	}

	return []string{err.Error()}
}

// ambiguousTexts returns the message of each binding in the tree of err
// that carries a path, and requires that no path selects one node alone.
func ambiguousTexts(t *testing.T, err error) []string {
	t.Helper()

	var texts []string

	for _, bound := range yamltest.BoundIn(err) {
		if _, ok := bound.Path(); !ok {
			continue
		}

		require.ErrorIs(t, bound.Unresolved(), niceyaml.ErrAmbiguousPath)

		texts = append(texts, bound.Error())
	}

	return texts
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

// mapMesh is a node of a graph that links to other nodes through a map,
// and holds a port that validates itself.
type mapMesh struct {
	Next map[string]*mapMesh `yaml:"next"`
	Port *port               `yaml:"port"`
}

// mapMeshChain returns the meshes n0 to nk, where each mesh links to the
// next one under the keys a and b, and nk links back to n0.
func mapMeshChain(k int) []*mapMesh {
	nodes := make([]*mapMesh, k+1)
	for i := range nodes {
		nodes[i] = &mapMesh{}
	}

	for i := range k {
		nodes[i].Next = map[string]*mapMesh{"a": nodes[i+1], "b": nodes[i+1]}
	}

	nodes[k].Next = map[string]*mapMesh{"a": nodes[0]}

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

// cycleHead leads through a link back to itself, and holds a port it
// walks after the link.
type cycleHead struct {
	Link *cycleLink `yaml:"link"`
	Port port       `yaml:"port"`
}

// cycleLink leads back to the head of its cycle.
type cycleLink struct {
	Back *cycleHead `yaml:"back"`
}

// cycleParent holds a link of a cycle and reports that it ran.
type cycleParent struct {
	Link *cycleLink `yaml:"link"`
}

func (cycleParent) Validate() error {
	return errors.New("parent ran")
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
			return niceyaml.NewError("empty label", niceyaml.AtPath(paths.Current().Child(k)))
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

// bytesKey holds the YAML text go-yaml hands it.
type bytesKey string

func (k *bytesKey) UnmarshalYAML(b []byte) error {
	*k = bytesKey(b)

	return nil
}

// nodeKey holds the text of the node go-yaml hands it.
type nodeKey string

func (k *nodeKey) UnmarshalYAML(node ast.Node) error {
	*k = nodeKey(node.String())

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

// probeCtxKey keys the [probe] a [probed] reads from the context.
type probeCtxKey struct{}

// probe runs one step for each Validate of a [probed] that holds it, in
// order, and counts the calls. A call past the last step passes.
type probe struct {
	cancel context.CancelFunc
	steps  []func(cancel context.CancelFunc) error
	calls  atomic.Int32
}

// probed takes the [probe] of the decode context, so its Validate runs a
// step that the test chose.
type probed struct {
	probe *probe
}

func (p *probed) UnmarshalYAML(ctx context.Context, _ []byte) error {
	pr, ok := ctx.Value(probeCtxKey{}).(*probe)
	if !ok {
		return errors.New("no probe in the context")
	}

	p.probe = pr

	return nil
}

func (p probed) Validate() error {
	n := int(p.probe.calls.Add(1))
	if n > len(p.probe.steps) {
		return nil
	}

	return p.probe.steps[n-1](p.probe.cancel)
}

// database requires a password, which a layer after the file may set.
type database struct {
	Host     string `yaml:"host"`
	Password string `yaml:"password"`
}

func (d database) Validate() error {
	if d.Password == "" {
		return niceyaml.NewError("password is required", niceyaml.AtPath(paths.Current().Child("password")))
	}

	return nil
}

// upstream requires a URL with an http scheme.
type upstream struct {
	URL string `yaml:"url"`
}

func (u upstream) Validate() error {
	if !strings.HasPrefix(u.URL, "http") {
		return niceyaml.NewError(
			fmt.Sprintf("url %q is not http", u.URL),
			niceyaml.AtPath(paths.Current().Child("url")),
		)
	}

	return nil
}

// layeredConfig is a configuration that the layers after the file can
// change before it validates.
type layeredConfig struct {
	DB        database            `yaml:"db"`
	Backup    *database           `yaml:"backup"`
	Upstreams map[string]upstream `yaml:"upstreams"`
	Items     []upstream          `yaml:"items"`
	Ignored   database            `yaml:"-"`
	Port      port                `yaml:"port"`
}

// applyEnv sets the fields of cfg that env names, as a program sets them
// from its environment.
func applyEnv(cfg *layeredConfig, env map[string]string) {
	if password, ok := env["DB_PASSWORD"]; ok {
		cfg.DB.Password = password
	}
}

// grade is a level that a document names, and that only gradeNames
// decodes.
type grade int

// gradeHigh is the grade a document names high.
const gradeHigh grade = 3

// gradedConfig holds maps whose keys the document spells in a form that
// differs from the text of their Go values.
type gradedConfig struct {
	ByGrade  map[grade]upstream   `yaml:"by_grade"`
	ByWeight map[float64]upstream `yaml:"by_weight"`
	ByAny    map[any]upstream     `yaml:"by_any"`
}

// pool decodes its upstreams from the upstreams key under spec, a place
// no tag names. It tags the field `-`, so a decode leaves the field to
// its Validate, which walks the upstreams and puts them under that key.
type pool struct {
	Upstreams []upstream `yaml:"-"`
}

func (p *pool) UnmarshalYAML(unmarshal func(any) error) error {
	var raw struct {
		Spec struct {
			Upstreams []upstream `yaml:"upstreams"`
		} `yaml:"spec"`
	}

	err := unmarshal(&raw)
	if err != nil {
		return err
	}

	p.Upstreams = raw.Spec.Upstreams

	return nil
}

func (p *pool) Validate() error {
	err := niceyaml.SelfValidateValue(context.Background(), p.Upstreams)

	//nolint:wrapcheck // The decode places the error as it is.
	return niceyaml.Rebase(err, paths.Current().Child("spec", "upstreams"))
}

// server holds values that validate themselves, and decodes field by
// field.
type server struct {
	Host  string `yaml:"host"`
	Port  port   `yaml:"port"`
	Hours hours  `yaml:"hours"`
	Limit signed `yaml:"limit"`
}

// defaultedServer is a server that sets a default port and then decodes
// its fields, as a type that declares its defaults does.
type defaultedServer server

func (s *defaultedServer) UnmarshalYAML(unmarshal func(any) error) error {
	type plain defaultedServer

	*s = defaultedServer{Port: 8080}

	return unmarshal((*plain)(s))
}

// rewalkedServer is a defaultedServer whose Validate walks its own
// fields, which a decode walks too.
type rewalkedServer server

func (s *rewalkedServer) UnmarshalYAML(unmarshal func(any) error) error {
	type plain rewalkedServer

	*s = rewalkedServer{Port: 8080}

	return unmarshal((*plain)(s))
}

func (s rewalkedServer) Validate() error {
	//nolint:wrapcheck // The decode places the error as it is.
	return niceyaml.SelfValidateValue(context.Background(), server(s))
}

// fleet holds servers under a name, and decodes field by field.
type fleet struct {
	Name    string   `yaml:"name"`
	Servers []server `yaml:"servers"`
}

// defaultedFleet is a fleet that sets a default name and then decodes its
// fields.
type defaultedFleet fleet

func (f *defaultedFleet) UnmarshalYAML(unmarshal func(any) error) error {
	type plain defaultedFleet

	*f = defaultedFleet{Name: "main"}

	return unmarshal((*plain)(f))
}

// growingFleet is a fleet that decodes its fields and then adds a server
// the document does not hold.
type growingFleet fleet

func (f *growingFleet) UnmarshalYAML(unmarshal func(any) error) error {
	type plain growingFleet

	err := unmarshal((*plain)(f))
	if err != nil {
		return err
	}

	f.Servers = append(f.Servers, server{Host: "spare", Port: 70000})

	return nil
}

// reversedFleet is a fleet that decodes its fields and then reverses its
// servers, so each index names another server of the document.
type reversedFleet fleet

func (f *reversedFleet) UnmarshalYAML(unmarshal func(any) error) error {
	type plain reversedFleet

	err := unmarshal((*plain)(f))
	if err != nil {
		return err
	}

	slices.Reverse(f.Servers)

	return nil
}

// nodeSpec decodes its hours and keeps a syntax tree beside them, which
// holds a value that reports any walk that reaches it.
type nodeSpec struct {
	Node  ast.Node `yaml:"node"`
	Hours hours    `yaml:"hours"`
}

func (s *nodeSpec) UnmarshalYAML(unmarshal func(any) error) error {
	var raw struct {
		Hours hours `yaml:"hours"`
	}

	err := unmarshal(&raw)
	if err != nil {
		return err
	}

	s.Hours = raw.Hours
	s.Node = &ast.MappingNode{Values: []*ast.MappingValueNode{{Value: walkedNode{}}}}

	return nil
}

// hostPort parses itself from the text "host:port", so the document
// holds a scalar where its fields name keys. It requires a port.
type hostPort struct {
	Host string `yaml:"host"`
	Port port   `yaml:"port"`
}

func (a *hostPort) UnmarshalText(text []byte) error {
	host, portText, ok := strings.Cut(string(text), ":")
	if !ok {
		return errors.New("want host:port")
	}

	n, err := strconv.Atoi(portText)
	if err != nil {
		return fmt.Errorf("port: %w", err)
	}

	a.Host, a.Port = host, port(n)

	return nil
}

func (a hostPort) Validate() error {
	if a.Port < 1 {
		return niceyaml.NewError("port must be at least 1", niceyaml.AtPath(paths.Current().Child("port")))
	}

	return nil
}

// hostPorts parses a list of hostPort values from one text, with a comma
// between them.
type hostPorts struct {
	Addrs []hostPort `yaml:"addrs"`
}

func (h *hostPorts) UnmarshalText(text []byte) error {
	for part := range strings.SplitSeq(string(text), ",") {
		var addr hostPort

		err := addr.UnmarshalText([]byte(part))
		if err != nil {
			return err
		}

		h.Addrs = append(h.Addrs, addr)
	}

	return nil
}

// sortedLimits decodes a list and sorts it, so its indexes are not those
// of the document.
type sortedLimits []signed

func (s *sortedLimits) UnmarshalYAML(unmarshal func(any) error) error {
	var raw []signed

	err := unmarshal(&raw)
	if err != nil {
		return err
	}

	slices.SortFunc(raw, func(a, b signed) int { return cmp.Compare(a.N, b.N) })

	*s = raw

	return nil
}

// leadingLimits decodes a list behind a limit the document does not
// hold, so the list holds one element more than the document.
type leadingLimits []signed

func (l *leadingLimits) UnmarshalYAML(unmarshal func(any) error) error {
	var raw []signed

	err := unmarshal(&raw)
	if err != nil {
		return err
	}

	*l = append(leadingLimits{{N: 1}}, raw...)

	return nil
}

// swappedLimits decodes two limits and swaps them.
type swappedLimits [2]signed

func (s *swappedLimits) UnmarshalYAML(unmarshal func(any) error) error {
	var raw [2]signed

	err := unmarshal(&raw)
	if err != nil {
		return err
	}

	s[0], s[1] = raw[1], raw[0]

	return nil
}

// serversByHost decodes a list of servers into a map that keys each by
// its host.
type serversByHost map[string]server

func (m *serversByHost) UnmarshalYAML(unmarshal func(any) error) error {
	var raw []server

	err := unmarshal(&raw)
	if err != nil {
		return err
	}

	*m = serversByHost{}
	for _, s := range raw {
		(*m)[s.Host] = s
	}

	return nil
}

// limitsByAny decodes two limits into a map whose keys share a path.
type limitsByAny map[any]signed

func (m *limitsByAny) UnmarshalYAML(unmarshal func(any) error) error {
	var raw [2]signed

	err := unmarshal(&raw)
	if err != nil {
		return err
	}

	*m = limitsByAny{1: raw[0], "1": raw[1]}

	return nil
}

// bucket is the one kind of store a document names, and requires a name.
type bucket struct {
	Type  string `yaml:"type"`
	Name  string `yaml:"name"`
	Limit signed `yaml:"limit"`
}

func (b bucket) Validate() error {
	if b.Name == "" {
		return niceyaml.NewError("name is required", niceyaml.AtPath(paths.Current().Child("name")))
	}

	return nil
}

// decodeStore decodes the store a mapping holds, by the type its type key
// names.
func decodeStore(unmarshal func(any) error) (any, error) {
	var head struct {
		Type string `yaml:"type"`
	}

	err := unmarshal(&head)
	if err != nil {
		return nil, err
	}

	if head.Type != "bucket" {
		return nil, fmt.Errorf("unknown store type %q", head.Type)
	}

	var b bucket

	err = unmarshal(&b)
	if err != nil {
		return nil, err
	}

	return b, nil
}

// storeBox holds the store its mapping names in a field with no tag, so
// a decode looks for the store under a key the mapping does not hold.
// Its Validate walks the store, which the decode walks too.
type storeBox struct {
	Store any
}

func (b *storeBox) UnmarshalYAML(unmarshal func(any) error) error {
	store, err := decodeStore(unmarshal)
	b.Store = store

	return err
}

func (b storeBox) Validate() error {
	//nolint:wrapcheck // The decode places the error as it is.
	return niceyaml.SelfValidateValue(context.Background(), b.Store)
}

// inlineStoreBox holds its store in a field tagged inline, so a decode
// reads the store from the mapping of the box.
type inlineStoreBox struct {
	Store any `yaml:",inline"`
}

func (b *inlineStoreBox) UnmarshalYAML(unmarshal func(any) error) error {
	store, err := decodeStore(unmarshal)
	b.Store = store

	return err
}

// skippedStoreBox is storeBox with its field tagged `-`, so a decode
// leaves the store to the Validate of the box.
type skippedStoreBox struct {
	Store any `yaml:"-"`
}

func (b *skippedStoreBox) UnmarshalYAML(unmarshal func(any) error) error {
	store, err := decodeStore(unmarshal)
	b.Store = store

	return err
}

func (b skippedStoreBox) Validate() error {
	//nolint:wrapcheck // The decode places the error as it is.
	return niceyaml.SelfValidateValue(context.Background(), b.Store)
}

// lazyServer decodes its name from a scalar and leaves its server unset.
type lazyServer struct {
	Name   string   `yaml:"name"`
	Server database `yaml:"server"`
}

func (l *lazyServer) UnmarshalYAML(unmarshal func(any) error) error {
	return unmarshal(&l.Name)
}

// rangeHours parses hours from the text "open-close", and reports them
// with a summary, a detail, and an error at a path from the root.
type rangeHours struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (r *rangeHours) UnmarshalText(text []byte) error {
	r.Open, r.Close, _ = strings.Cut(string(text), "-")

	return nil
}

func (r rangeHours) Validate() error {
	if r.Close >= r.Open {
		return nil
	}

	return niceyaml.NewSummary("invalid hours",
		niceyaml.NewError("opens late", niceyaml.AtPath(paths.Current().Child("open"))),
		niceyaml.NewError("closes early", niceyaml.WithDetails(
			niceyaml.NewError("closes here", niceyaml.AtPath(paths.Current().Child("close"))),
		)),
		niceyaml.NewError("see the label", niceyaml.AtPath(paths.Doc().Child("label"))),
	)
}

// sharedKeys parses a rangeHours and a limit from one text, with a comma
// between them, and holds them under keys that share a path. A decode
// skips the field, and the Validate of sharedKeys walks it.
type sharedKeys struct {
	ByKey map[any]any `yaml:"-"`
}

func (s *sharedKeys) UnmarshalText(text []byte) error {
	first, second, _ := strings.Cut(string(text), ",")

	var h rangeHours

	err := h.UnmarshalText([]byte(first))
	if err != nil {
		return err
	}

	n, err := strconv.Atoi(second)
	if err != nil {
		return fmt.Errorf("limit: %w", err)
	}

	s.ByKey = map[any]any{1: h, "1": signed{N: n}}

	return nil
}

func (s sharedKeys) Validate() error {
	//nolint:wrapcheck // The decode places the error as it is.
	return niceyaml.SelfValidateValue(context.Background(), s.ByKey)
}

// sharedKeysList decodes a list of sharedKeys, as a list that decodes
// itself.
type sharedKeysList []sharedKeys

func (l *sharedKeysList) UnmarshalYAML(unmarshal func(any) error) error {
	var raw []sharedKeys

	err := unmarshal(&raw)
	if err != nil {
		return err
	}

	*l = raw

	return nil
}

// locatorDoc returns the first document of input, from a source named
// f.yaml that resolves the anchors of [referenceDoc].
func locatorDoc(tb testing.TB, input string) *niceyaml.Node {
	tb.Helper()

	return yamltest.FirstDocument(tb, input,
		niceyaml.WithName("f.yaml"),
		niceyaml.WithReferences(niceyaml.NewSourceFromString(referenceDoc)),
	)
}

// located binds an error with the location opt gives it through n. It
// returns the message of the binding, which puts the position and the
// path in front. It also returns the path of the node the error binds at
// when the document holds no value at its own path, as
// [niceyaml.SourceError.Nearest] reports it.
func located(tb testing.TB, n *niceyaml.Node, opt niceyaml.ErrorOption) (string, string) {
	tb.Helper()

	bound := yamltest.RequireSourceError(tb, n.Bind(niceyaml.NewError("x", opt)))

	near := ""
	if path, ok := bound.Nearest(); ok {
		near = path.String()
	}

	return bound.Error(), near
}

// pointerNames returns the names in a JSON Pointer, as the documentation
// of [niceyaml.DataLocator] splits one.
func pointerNames(pointer string) []string {
	if pointer == "" {
		return nil
	}

	unescape := strings.NewReplacer("~1", "/", "~0", "~")

	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for i, part := range parts {
		parts[i] = unescape.Replace(part)
	}

	return parts
}

func ExampleDataLocator() {
	doc, err := niceyaml.NewSourceFromString(
		stringtest.LinesLF(
			"ports:",
			"  0x10:",
			`    name: ""`,
			"items:",
			`  - name: ""`,
		),
		niceyaml.WithName("cfg.yaml"),
	).Document()
	if err != nil {
		log.Fatal(err)
	}

	// A check of the decoded data reports each finding as a JSON Pointer.
	findings := []string{"/ports/16/name", "/items/0/name"}

	loc := doc.DataLocator()

	for _, pointer := range findings {
		fmt.Println(doc.Bind(niceyaml.NewError("must not be empty", loc.At(pointerNames(pointer)...))))
	}

	// Output:
	// cfg.yaml:3:11: $.ports.0x10.name: must not be empty
	// cfg.yaml:5:11: $.items[0].name: must not be empty
}

func TestDataLocator_At(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		names []string
		want  string
		// The path of the node the error binds at, for names the document
		// holds no value for: the mapping that lacks a member, or an alias
		// the document cannot follow.
		wantNear string
	}{
		"key the decoder respells": {
			input: respelledDoc,
			names: []string{"ports", "16", "name"},
			want:  "f.yaml:3:11: $.ports.0x10.name: x",
		},
		"element of a sequence": {
			input: respelledDoc,
			names: []string{"items", "0", "name"},
			want:  "f.yaml:5:11: $.items[0].name: x",
		},
		"no names": {
			input: respelledDoc,
			want:  "f.yaml:1:1: $: x",
		},
		"float key with a trailing zero": {
			input: pythonDoc,
			names: []string{"python", "3.1", "image"},
			want:  "f.yaml:3:12: $.python.'3.10'.image: x",
		},
		"last of two keys with one name": {
			input: sameNameDoc,
			names: []string{"m", "16"},
			want:  "f.yaml:3:9: $.m.0x10: x",
		},
		"last merge source with the name": {
			input: mergeDoc,
			names: []string{"m", "true"},
			want:  "f.yaml:6:9: $.m.True: x",
		},
		"member no path selects": {
			input: hiddenDoc,
			names: []string{"user", "16"},
			want:  "f.yaml:2:14: $.user.16: x",
		},
		"member beside one no path selects": {
			input: hiddenDoc,
			names: []string{"user", "0x10"},
			want:  "f.yaml:3:11: $.user.0x10: x",
		},
		"element of a root sequence": {
			input: "- name: \"\"\n",
			names: []string{"0", "name"},
			want:  "f.yaml:1:9: $[0].name: x",
		},
		"element behind an alias": {
			input: shapesDoc,
			names: []string{"al", "1"},
			want:  "f.yaml:3:5: $.al[1]: x",
		},
		"element of an element": {
			input: shapesDoc,
			names: []string{"nested", "0", "1"},
			want:  "f.yaml:10:7: $.nested[0][1]: x",
		},
		"digits in a mapping name a member": {
			input: shapesDoc,
			names: []string{"map", "1"},
			want:  "f.yaml:6:6: $.map.1: x",
		},

		// A member the data lacks.
		"member a mapping lacks": {
			input:    lackingDoc,
			names:    []string{"ports", "16", "name"},
			want:     "f.yaml:2:3: $.ports.0x10.name: x",
			wantNear: "$.ports.0x10",
		},
		"names below a member a mapping lacks": {
			input:    lackingDoc,
			names:    []string{"ports", "16", "tls", "0"},
			want:     "f.yaml:2:3: $.ports.0x10.tls.0: x",
			wantNear: "$.ports.0x10",
		},
		"member the root lacks": {
			input:    lackingDoc,
			names:    []string{"nope", "x"},
			want:     "f.yaml:1:1: $.nope.x: x",
			wantNear: "$",
		},
		"member a null lacks": {
			input:    shapesDoc,
			names:    []string{"none", "x"},
			want:     "f.yaml:12:1: $.none.x: x",
			wantNear: "$.none",
		},
		"name a key spells for another member": {
			input: pythonDoc,
			names: []string{"python", "3.10"},
			want:  "f.yaml:1:1: $.python.'3.10': x",
		},
		"names below a name a key spells for another member": {
			input: pythonDoc,
			names: []string{"python", "3.10", "image"},
			want:  "f.yaml:1:1: $.python.'3.10'.image: x",
		},
		"member lacking below one no path selects": {
			input: stringtest.LinesLF("user:", "  <<: {0x10: {a: b}}", `  "0x10": 1`),
			names: []string{"user", "16", "name"},
			want:  "f.yaml:2:8: $.user.16.name: x",
		},
		"name of a decode into map[int]": {
			input: stringtest.LinesLF(
				"m:",
				"  1e3:",
				`    n: ""`,
			),
			names:    []string{"m", "1000", "n"},
			want:     "f.yaml:1:1: $.m.1000.n: x",
			wantNear: "$.m",
		},
		"name of a decode into map[string]": {
			input: stringtest.LinesLF(
				"m:",
				"  ~:",
				`    n: ""`,
			),
			names:    []string{"m", "", "n"},
			want:     "f.yaml:1:1: $.m.''.n: x",
			wantNear: "$.m",
		},

		// A name that is no index of its sequence, or lies below a scalar.
		"index past the end": {
			input: shapesDoc,
			names: []string{"seq", "9"},
			want:  "f.yaml: $.seq[9]: x",
		},
		"names below an index past the end": {
			input: shapesDoc,
			names: []string{"seq", "9", "x"},
			want:  "f.yaml: $.seq[9].x: x",
		},
		"index with a leading zero": {
			input: shapesDoc,
			names: []string{"seq", "01"},
			want:  "f.yaml: $.seq.01: x",
		},
		"index with a sign": {
			input: shapesDoc,
			names: []string{"seq", "+1"},
			want:  "f.yaml: $.seq.+1: x",
		},
		"index too large for an int": {
			input: shapesDoc,
			names: []string{"seq", "99999999999999999999"},
			want:  "f.yaml: $.seq.99999999999999999999: x",
		},
		"member name in a sequence": {
			input: shapesDoc,
			names: []string{"seq", "name"},
			want:  "f.yaml: $.seq.name: x",
		},
		"name below a scalar": {
			input: shapesDoc,
			names: []string{"scalar", "x"},
			want:  "f.yaml: $.scalar.x: x",
		},
		"index past the end below a member no path selects": {
			input: stringtest.LinesLF("user:", "  <<: {0x10: [a]}", `  "0x10": 1`),
			names: []string{"user", "16", "1"},
			want:  "f.yaml:2:8: $.user.16[1]: x",
		},

		// A value only a reference document holds, which the locator
		// cannot follow. A path that enters the alias binds there.
		"member after a merge of a reference document": {
			input: referringDoc,
			names: []string{"server", "port"},
			want:  "f.yaml:3:9: $.server.port: x",
		},
		"member a merge of a reference document sets": {
			input: referringDoc,
			names: []string{"server", "timeout"},
			want:  "f.yaml: $.server.timeout: x",
		},
		"name a key spells beside a merge of a reference document": {
			input: referringDoc,
			names: []string{"server", "3.10"},
			want:  "f.yaml:1:1: $.server.'3.10': x",
		},
		"member below an alias to a reference document": {
			input:    referringDoc,
			names:    []string{"items", "1", "16"},
			want:     "f.yaml:5:8: $.items.1.16: x",
			wantNear: "$.items",
		},

		// A mapping with a key the locator cannot name.
		"member before a key with no name": {
			input: unnamedKeyDoc,
			names: []string{"python", "a"},
			want:  "f.yaml:4:6: $.python.a: x",
		},
		"member after a key with no name": {
			input: unnamedKeyDoc,
			names: []string{"python", "b"},
			want:  "f.yaml:6:6: $.python.b: x",
		},
		"name a key spells before a key with no name": {
			input: unnamedKeyDoc,
			names: []string{"python", "3.10"},
			want:  "f.yaml:2:1: $.python.'3.10': x",
		},

		// A document with no mapping at its root.
		"member a null root lacks": {
			input:    "~\n",
			names:    []string{"a"},
			want:     "f.yaml:1:1: $.a: x",
			wantNear: "$",
		},
		"name below a scalar root": {
			input: "scalar\n",
			names: []string{"a", "0"},
			want:  "f.yaml: $.a.0: x",
		},
		"empty document": {
			input: "",
			names: []string{"a", "0"},
			want:  "f.yaml: $.a.0: x",
		},
		"document of a comment alone": {
			input: "# empty\n",
			names: []string{"a"},
			want:  "f.yaml: $.a: x",
		},

		// The names of a JSON Pointer.
		"pointer with an escaped slash and tilde": {
			input: pointerDoc,
			names: pointerNames("/a~1b~0c"),
			want:  "f.yaml:1:1: $.'a/b~c': x",
		},
		"pointer with an empty name": {
			input: pointerDoc,
			names: pointerNames("/a~1b~0c/"),
			want:  "f.yaml:2:7: $.'a/b~c'.'': x",
		},
		"pointer with an index and a respelled key": {
			input: pointerDoc,
			names: pointerNames("/items/0/16"),
			want:  "f.yaml:4:11: $.items[0].0x10: x",
		},
		"pointer with an escape of an escape": {
			input:    pointerDoc,
			names:    pointerNames("/~01"),
			want:     "f.yaml:1:1: $.'~1': x",
			wantNear: "$",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := locatorDoc(t, tc.input)

			got, gotNear := located(t, doc, doc.DataLocator().At(tc.names...))

			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantNear, gotNear)
		})
	}
}

func TestDataLocator_AtKey(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input    string
		names    []string
		want     string
		wantNear string
	}{
		"key the decoder respells": {
			input: respelledDoc,
			names: []string{"ports", "16"},
			want:  "f.yaml:2:3: $.ports.0x10~: x",
		},
		"explicit key": {
			input: "? 0x10\n: x\n",
			names: []string{"16"},
			want:  "f.yaml:1:3: $.0x10~: x",
		},
		"key of a merge source": {
			input: mergeDoc,
			names: []string{"m", "true"},
			want:  "f.yaml:6:3: $.m.True~: x",
		},
		"key no path selects": {
			input: hiddenDoc,
			names: []string{"user", "16"},
			want:  "f.yaml:2:8: $.user.16~: x",
		},
		"element has no key": {
			input: respelledDoc,
			names: []string{"items", "0"},
			want:  "f.yaml:5:3: $.items[0]~: x",
		},
		"no names": {
			input: respelledDoc,
			want:  "f.yaml:1:1: $~: x",
		},
		"member a mapping lacks": {
			input:    lackingDoc,
			names:    []string{"ports", "16", "name"},
			want:     "f.yaml:2:3: $.ports.0x10.name: x",
			wantNear: "$.ports.0x10",
		},
		"name a key spells for another member": {
			input: pythonDoc,
			names: []string{"python", "3.10"},
			want:  "f.yaml:1:1: $.python.'3.10': x",
		},
		"index past the end": {
			input: shapesDoc,
			names: []string{"seq", "9"},
			want:  "f.yaml: $.seq[9]: x",
		},
		"member below an alias to a reference document": {
			input:    referringDoc,
			names:    []string{"items", "1", "16"},
			want:     "f.yaml:5:8: $.items.1.16~: x",
			wantNear: "$.items",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := locatorDoc(t, tc.input)

			got, gotNear := located(t, doc, doc.DataLocator().AtKey(tc.names...))

			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantNear, gotNear)
		})
	}
}

func TestDataLocator_At_Options(t *testing.T) {
	t.Parallel()

	doc := locatorDoc(t, hiddenDoc)
	loc := doc.DataLocator()

	other := niceyaml.AtPath(paths.Doc().Child("other"))
	marked := niceyaml.AtRange(yamltest.LineRange(0, 0, 4))

	tcs := map[string]struct {
		want string
		opts []niceyaml.ErrorOption
	}{
		"replaces a path set before it": {
			opts: []niceyaml.ErrorOption{other, loc.At("user", "0x10")},
			want: "f.yaml:3:11: $.user.0x10: x",
		},
		"a later path replaces it": {
			opts: []niceyaml.ErrorOption{loc.At("user", "0x10"), niceyaml.AtPath(paths.Doc().Child("user"))},
			want: "f.yaml:1:1: $.user: x",
		},
		"keeps a range set before it where the path selects the value": {
			opts: []niceyaml.ErrorOption{marked, loc.At("user", "0x10")},
			want: "f.yaml:1:1: $.user.0x10: x",
		},
		"replaces a range set before it where no path selects the value": {
			opts: []niceyaml.ErrorOption{marked, loc.At("user", "16")},
			want: "f.yaml:2:14: $.user.16: x",
		},
		"a later range replaces its position": {
			opts: []niceyaml.ErrorOption{loc.At("user", "16"), marked},
			want: "f.yaml:1:1: $.user.16: x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := doc.Bind(niceyaml.NewError("x", tc.opts...))

			require.EqualError(t, err, tc.want)
		})
	}
}

func TestDataLocator_Names(t *testing.T) {
	t.Parallel()

	// The names a locator reads are those go-yaml gives the keys of a
	// decode into any, so each key here pins one of them.
	input := stringtest.LinesLF(
		"0x10: a",
		"3.10: b",
		"True: c",
		"~: d",
		"1234567.0: e",
		"!!binary aGk=: f",
		"1e3: g",
		`"0x20": h`,
	)
	doc := locatorDoc(t, input)

	// Each name, with the line of the key that sets its member.
	want := map[string]int{
		"16":           1,
		"3.1":          2,
		"true":         3,
		"null":         4,
		"1.234567e+06": 5,
		"[104 105]":    6,
		"1e3":          7,
		"0x20":         8,
	}

	data, err := doc.Decode[any](t.Context())
	require.NoError(t, err)

	members, ok := data.(map[string]any)
	require.True(t, ok, "decode into any gave %T", data)

	assert.ElementsMatch(t, slices.Collect(maps.Keys(want)), slices.Collect(maps.Keys(members)))

	loc := doc.DataLocator()

	for name, line := range want {
		bound := yamltest.RequireSourceError(t, doc.Bind(niceyaml.NewError("x", loc.AtKey(name))))

		pos, ok := bound.Position()
		require.True(t, ok, "name %q binds with no position", name)
		assert.Equal(t, line-1, pos.Line, "name %q", name)
	}

	// A decode into another type names some of those keys another way,
	// so the names of such a decode lead nowhere.
	t.Run("typed decodes", func(t *testing.T) {
		t.Parallel()

		ints, err := locatorDoc(t, "1e3: a\n").Decode[map[int]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[int]string{1000: "a"}, ints)

		texts, err := locatorDoc(t, "~: a\n").Decode[map[string]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"": "a"}, texts)
	})
}

func TestDataLocator_Scope(t *testing.T) {
	t.Parallel()

	doc := locatorDoc(t, stringtest.LinesLF(
		`- name: ""`,
		"- spec:",
		"    ports:",
		"      0x10:",
		`        name: ""`,
	))
	spec := yamltest.At(t, doc, paths.Doc().Index(1).Child("spec"))

	// The names read from the scoped Node, and the path starts at the root
	// of the document.
	err := niceyaml.NewError("x", spec.DataLocator().At("ports", "16", "name"))

	path, ok := err.Path()
	require.True(t, ok)
	assert.Equal(t, "$[1].spec.ports.0x10.name", path.String())

	const want = "f.yaml:5:15: $[1].spec.ports.0x10.name: x"

	require.EqualError(t, spec.Bind(err), want)
	require.EqualError(t, doc.Bind(err), want)

	// A base moves `@` paths alone, so the error binds at the same value
	// under any base.
	require.EqualError(t, doc.Bind(niceyaml.Rebase(err, paths.Doc().Child("other"))), want)

	t.Run("names of the document miss from the scope", func(t *testing.T) {
		t.Parallel()

		got, _ := located(t, spec, spec.DataLocator().At("1", "spec", "ports"))

		assert.Equal(t, "f.yaml:2:3: $[1].spec.1.spec.ports: x", got)
	})

	t.Run("scope behind an alias", func(t *testing.T) {
		t.Parallel()

		aliased := locatorDoc(t, stringtest.LinesLF("base: &base", "  0x10: x", "al: *base"))
		al := yamltest.At(t, aliased, paths.Doc().Child("al"))

		got, _ := located(t, al, al.DataLocator().At("16"))

		assert.Equal(t, "f.yaml:2:9: $.al.0x10: x", got)
	})
}

func TestDataLocator_SyntaxError(t *testing.T) {
	t.Parallel()

	doc := yamltest.UnparsedDocument(t, "a: [1\n", niceyaml.WithName("f.yaml"))
	require.ErrorIs(t, doc.Err(), niceyaml.ErrSyntax)

	bound := yamltest.RequireSourceError(t, doc.Bind(niceyaml.NewError("x", doc.DataLocator().At("a", "0"))))
	require.EqualError(t, bound, "f.yaml: $.a.0: x")
	require.ErrorIs(t, bound.Unresolved(), niceyaml.ErrPathNeedsDocument)
}

// schemaLocations returns where each violation of v in doc binds: its
// position, its path, and the mapping it binds at for a member the
// mapping lacks.
func schemaLocations(t *testing.T, doc *niceyaml.Node, v *jsonschema.Validator) []string {
	t.Helper()

	err := doc.Validate(t.Context(), schema.FromJSONSchema(v))
	require.Error(t, err)
	require.True(t, niceyaml.IsInvalid(err), "validation did not run: %v", err)

	var got []string

	// The summary of several violations is no violation, so Problems
	// passes over it.
	for problem := range niceyaml.Problems(err) {
		got = append(got, describeLocation(problem.Bound))
	}

	slices.Sort(got)

	return got
}

// locatorLocations returns where a [niceyaml.DataLocator] of doc binds an
// error for each failure v finds in the data of doc. The locator takes
// the instance location of the failure as names. A failure about a
// missing member takes the name of that member too. One about a key,
// which additionalProperties and propertyNames report, goes through
// [niceyaml.DataLocator.AtKey].
func locatorLocations(t *testing.T, doc *niceyaml.Node, v *jsonschema.Validator) []string {
	t.Helper()

	data, err := doc.Decode[any](t.Context())
	require.NoError(t, err)

	var failure *jsonschema.ValidationError

	require.ErrorAs(t, v.Validate(t.Context(), data), &failure)

	loc := doc.DataLocator()

	var got []string

	for _, leaf := range failure.Leaves() {
		var names []string

		for _, seg := range leaf.InstanceSegments() {
			if seg.IsIndex {
				names = append(names, strconv.Itoa(seg.Index))
			} else {
				names = append(names, seg.Key)
			}
		}

		// Each failure takes one lookup, as it does in the schema, so both
		// read the merge sources of a document the same number of times.
		var opt niceyaml.ErrorOption

		quoted, required := strings.CutPrefix(leaf.Message, "missing required property ")

		switch {
		case required:
			missing, err := strconv.Unquote(quoted)
			require.NoError(t, err)

			opt = loc.At(append(names, missing)...)

		case leaf.Keyword == jsonschema.KeywordAdditionalProperties,
			leaf.Keyword == jsonschema.KeywordPropertyNames:
			opt = loc.AtKey(names...)

		default:
			opt = loc.At(names...)
		}

		bound := yamltest.RequireSourceError(t, doc.Bind(niceyaml.NewError("x", opt)))

		got = append(got, describeLocation(bound))
	}

	slices.Sort(got)

	return got
}

// describeLocation returns where bound binds: its position, its path,
// and the mapping it binds at when its path names a key the document
// leaves out.
func describeLocation(bound *niceyaml.SourceError) string {
	at := "-"
	if pos, ok := bound.Position(); ok {
		at = fmt.Sprintf("%d:%d", pos.Line+1, pos.Col+1)
	}

	path, _ := bound.Path()

	near := "-"
	if nearest, ok := bound.Nearest(); ok {
		near = nearest.String()
	}

	return fmt.Sprintf("%s %s near %s", at, path, near)
}

func TestDataLocator_Schema(t *testing.T) {
	t.Parallel()

	// Each schema fails many values of a document: every scalar, every
	// key, or every mapping, for members it lacks. Some of those members
	// have the name another key is spelled with.
	schemas := map[string]string{
		"every scalar": `{
			"$ref": "#/$defs/node",
			"$defs": {"node": {
				"if": {"type": ["object", "array"]},
				"then": {
					"additionalProperties": {"$ref": "#/$defs/node"},
					"items": {"$ref": "#/$defs/node"}
				},
				"else": false
			}}
		}`,
		"every key": `{
			"$ref": "#/$defs/node",
			"$defs": {"node": {
				"propertyNames": false,
				"additionalProperties": {"$ref": "#/$defs/node"},
				"items": {"$ref": "#/$defs/node"}
			}}
		}`,
		"every mapping": `{
			"$ref": "#/$defs/node",
			"$defs": {"node": {
				"required": ["zz", "0x10", "16", "3.10", "True", "~", "0", "name"],
				"additionalProperties": {"$ref": "#/$defs/node"},
				"items": {"$ref": "#/$defs/node"}
			}}
		}`,
	}

	inputs := map[string]string{
		"respelled keys":           respelledDoc,
		"missing member":           lackingDoc,
		"key spelled as no member": pythonDoc,
		"two keys with one name":   sameNameDoc,
		"member no path selects":   hiddenDoc,
		"merge precedence":         mergeDoc,
		"sequences and scalars":    shapesDoc,
		"key with no name":         unnamedKeyDoc,
		"alias key": stringtest.LinesLF(
			"k: &k 0x10",
			"m:",
			"  *k : {0x20: v}",
			`  "16": w`,
		),
		"merge sources in a list": stringtest.LinesLF(
			"a: &a {0x10: 1, b: 2}",
			"b: &b {16: 3, 0x10: 4}",
			"m:",
			"  <<: [*a, *b]",
			"  b: 5",
			"  <<: *a",
		),
		"merge key after its keys": stringtest.LinesLF(
			"base: &base {0x10: {a: 1}}",
			"m:",
			"  0x10: {b: 2}",
			"  0o20: {c: 3}",
			"  <<: *base",
		),
		"keys below one no path selects": stringtest.LinesLF(
			"user:",
			"  <<: {0x10: {0x20: [x, {0x30: y}]}}",
			`  "0x10": 1`,
		),
		"aliased mapping in a sequence": stringtest.LinesLF(
			"base: &base {0x10: x, 3.10: y}",
			"seq:",
			"  - *base",
			"  - [*base, {True: z}]",
		),
		"null and bool keys": stringtest.LinesLF(
			"~: a",
			"null: b",
			"True: {true: c}",
			"false: [d]",
		),
		// The lookups of both runs pass the limit on the merge sources they
		// read, so each writes the same names past it.
		"more merge reads than the limit allows": yamltest.WideMerge(2000),
	}

	for schemaName, schemaJSON := range schemas {
		v, err := jsonschema.CompileJSON(t.Context(), []byte(schemaJSON))
		require.NoError(t, err)

		for inputName, input := range inputs {
			t.Run(schemaName+"/"+inputName, func(t *testing.T) {
				t.Parallel()

				// The second key of some documents repeats the member of
				// the first.
				doc := yamltest.FirstDocument(t, input, niceyaml.WithAllowDuplicateKeys(true))

				want := schemaLocations(t, doc, v)
				require.NotEmpty(t, want)

				got := locatorLocations(t, doc, v)
				require.Len(t, got, len(want))

				// A document of thousands of locations reports the first
				// few that differ.
				var differ []string

				for i := range want {
					if want[i] != got[i] && len(differ) < 10 {
						differ = append(differ, fmt.Sprintf("schema %q, locator %q", want[i], got[i]))
					}
				}

				assert.Empty(t, differ)
			})
		}
	}
}

func TestDataLocator_Concurrent(t *testing.T) {
	t.Parallel()

	// Each mapping of the document respells its keys, so every lookup
	// reads a mapping and asks which entry a spelling selects.
	const (
		mappings = 24
		members  = 24
	)

	var sb strings.Builder

	sb.WriteString("base: &base {0x1: x}\n")

	for m := range mappings {
		fmt.Fprintf(&sb, "m%d:\n  <<: *base\n", m)

		for i := range members {
			fmt.Fprintf(&sb, "  %#x: {name: \"\"}\n", 0x10+i)
		}
	}

	doc := locatorDoc(t, sb.String())

	names := func(m, i int) []string {
		return []string{"m" + strconv.Itoa(m), strconv.Itoa(0x10 + i), "name"}
	}

	// One goroutine finds every location first.
	single := doc.DataLocator()
	want := make([]string, 0, mappings*members)

	for m := range mappings {
		for i := range members {
			got, _ := located(t, doc, single.At(names(m, i)...))
			want = append(want, got)
		}
	}

	require.Equal(t, "f.yaml:4:16: $.m0.0x10.name: x", want[0])

	shared := doc.DataLocator()
	results := make([][]niceyaml.ErrorOption, 8)

	var wg sync.WaitGroup

	for g := range results {
		wg.Go(func() {
			opts := make([]niceyaml.ErrorOption, 0, mappings*members)

			for m := range mappings {
				for i := range members {
					opts = append(opts, shared.At(names(m, i)...))
				}
			}

			// Each goroutine also reads members the others do not.
			_ = shared.AtKey(names(g, g)...)
			_ = shared.At("m"+strconv.Itoa(g), "1", "missing")

			results[g] = opts
		})
	}

	wg.Wait()

	for _, opts := range results {
		got := make([]string, 0, len(opts))

		for _, opt := range opts {
			message, _ := located(t, doc, opt)
			got = append(got, message)
		}

		assert.Equal(t, want, got)
	}
}

func TestDataLocator_MergeReads(t *testing.T) {
	t.Parallel()

	// To spell a key of m, the locator reads the whole list of its merge
	// key, and the locations of one locator share one limit on those
	// reads. Past it, each path keeps the decoded name of its key, and the
	// error still binds at the value, by its position.
	const members = 2000

	doc := locatorDoc(t, yamltest.WideMerge(members))
	loc := doc.DataLocator()

	spelled := 0

	for i := range members {
		err := niceyaml.NewError("x", loc.At("m", strconv.Itoa(0x1000+i)))

		path, ok := err.Path()
		require.True(t, ok)

		bound := yamltest.RequireSourceError(t, doc.Bind(err))

		pos, ok := bound.Position()
		require.True(t, ok, "member %d binds with no position", i)
		require.Equal(t, i+2, pos.Line, "member %d", i)

		if strings.HasPrefix(path.String(), "$.m.0x") {
			spelled++

			continue
		}

		require.Equal(t, "$.m."+strconv.Itoa(0x1000+i), path.String())
	}

	assert.Positive(t, spelled)
	assert.Less(t, spelled, members)
}

// TestValidator_DirectCall calls the Check method of every validator the
// library ships. Each returns the error as the validator it wraps wrote
// it, and [niceyaml.Node.Validate] binds it. A Registry binds the error
// of its lookup through the root of the document itself and runs the
// schema it finds through Node.Validate, so a direct call to it returns
// what Node.Validate returns.
func TestValidator_DirectCall(t *testing.T) {
	t.Parallel()

	itemPath := paths.Doc().Child("items").Index(1)

	// A rule of a type of its own that leaves its error unbound, as
	// [niceyaml.Validator] asks.
	rule := &fieldValidator{
		err: niceyaml.NewError("reserved name", niceyaml.AtPath(paths.Current().Child("name"))),
	}

	nameSchema := schema.MustCompile([]byte(`{
		"type": "object",
		"properties": {"name": {"not": {"const": "admin"}}}
	}`))
	reg := schema.NewRegistry(schema.WithResolvers(nameSchema))

	tcs := map[string]struct {
		v niceyaml.Validator
		// The Node the validator runs on.
		path paths.Path
		// The message of the error a direct call to Check returns.
		raw string
		// The message of the error Node.Validate returns.
		want string
	}{
		"Schema": {
			v:    nameSchema,
			path: itemPath,
			raw:  "should not validate against the schema",
			want: "c.yaml:4:11: $.items[1].name: should not validate against the schema",
		},
		"Registry refuses a scoped Node": {
			v:    reg,
			path: itemPath,
			raw:  "c.yaml: registry needs a whole document: node is scoped to $.items[1]",
			want: "c.yaml: registry needs a whole document: node is scoped to $.items[1]",
		},
		"Registry at the root": {
			v:    reg,
			path: paths.Doc(),
			raw:  "c.yaml:1:7: $.name: should not validate against the schema",
			want: "c.yaml:1:7: $.name: should not validate against the schema",
		},
		"ValidatorFunc": {
			v:    niceyaml.ValidatorFunc(rule.Check),
			path: itemPath,
			raw:  "reserved name",
			want: "c.yaml:4:11: $.items[1].name: reserved name",
		},
		"MultiValidator": {
			v:    niceyaml.MultiValidator(rule, nameSchema),
			path: itemPath,
			raw:  stringtest.JoinLF("reserved name", "should not validate against the schema"),
			want: stringtest.JoinLF(
				"c.yaml:4:11: $.items[1].name: reserved name",
				"c.yaml:4:11: $.items[1].name: should not validate against the schema",
			),
		},
		"ChainValidator": {
			v:    niceyaml.ChainValidator(rule, nameSchema),
			path: itemPath,
			raw:  "reserved name",
			want: "c.yaml:4:11: $.items[1].name: reserved name",
		},
		"SkipEmpty": {
			v:    niceyaml.SkipEmpty(rule),
			path: itemPath,
			raw:  "reserved name",
			want: "c.yaml:4:11: $.items[1].name: reserved name",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, stringtest.Input(`
				name: admin
				items:
				  - name: soup
				  - name: admin
			`), niceyaml.WithName("c.yaml"))
			node := yamltest.At(t, doc, tc.path)

			require.EqualError(t, tc.v.Check(t.Context(), node), tc.raw)
			require.EqualError(t, node.Validate(t.Context(), tc.v), tc.want)
		})
	}
}

func TestValidatorFunc_Check(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		price: 5
		items:
		  - price: 1
		  - price: -2
	`), niceyaml.WithName("menu.yaml"))

	other := yamltest.FirstDocument(t, "# other\nprice: 0\n", niceyaml.WithName("other.yaml"))

	pricePath := paths.Current().Child("price")
	item := yamltest.At(t, doc, paths.Current().Child("items").Index(1))
	negative := niceyaml.NewError("negative price", niceyaml.AtPath(pricePath))

	var (
		nilError       *niceyaml.Error
		nilSourceError *niceyaml.SourceError
	)

	tcs := map[string]struct {
		// The error the function returns, and the node it runs on, or none
		// for doc.
		returns error
		node    *niceyaml.Node
		// The message of the bound error, or none for no error.
		want string
	}{
		"an unbound error binds through the node": {
			returns: negative,
			want:    "menu.yaml:1:8: $.price: negative price",
		},
		"a scoped node puts its path in front": {
			returns: negative,
			node:    item,
			want:    "menu.yaml:4:12: $.items[1].price: negative price",
		},
		"an error with no location names the source": {
			returns: errNameRequired,
			want:    "menu.yaml: name is required",
		},
		"an error bound through another node stays as it is": {
			returns: item.Bind(negative),
			want:    "menu.yaml:4:12: $.items[1].price: negative price",
		},
		"an error bound to another source stays as it is": {
			returns: other.Bind(negative),
			want:    "other.yaml:2:8: $.price: negative price",
		},
		"no error is no error":                  {},
		"a nil Error pointer is no error":       {returns: nilError},
		"a nil SourceError pointer is no error": {returns: nilSourceError},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := cmp.Or(tc.node, doc)
			validator := rejectingValidator(tc.returns)

			// A direct call returns the error as the function wrote it.
			assert.Equal(t, tc.returns, validator.Check(t.Context(), node))

			err := node.Validate(t.Context(), validator)
			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			require.EqualError(t, err, tc.want)

			require.ErrorAs(t, err, new(*niceyaml.SourceError))
		})
	}

	t.Run("returns what the same body returns as the method of a type", func(t *testing.T) {
		t.Parallel()

		method := negativePrice{}
		fn := niceyaml.ValidatorFunc(method.Check)

		tcs := map[string]struct {
			node *niceyaml.Node
		}{
			"a value the rule accepts":     {node: doc},
			"a value the rule rejects":     {node: item},
			"a value that does not decode": {node: yamltest.FirstDocument(t, "price: high\n")},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, method.Check(t.Context(), tc.node), fn.Check(t.Context(), tc.node))
				assert.Equal(t, tc.node.Validate(t.Context(), method), tc.node.Validate(t.Context(), fn))
			})
		}
	})

	t.Run("a validator that runs another on each element reports the element", func(t *testing.T) {
		t.Parallel()

		perItem := niceyaml.ValidatorFunc(negativePrice{}.Check)
		each := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			items, err := n.Nodes(paths.Current().Child("items").IndexAll())
			if err != nil {
				return err //nolint:wrapcheck // The test inspects the error as it is.
			}

			for _, it := range items {
				err := it.Validate(ctx, perItem)
				if err != nil {
					return err //nolint:wrapcheck // The test inspects the error as it is.
				}
			}

			return nil
		})

		want := "menu.yaml:4:12: $.items[1].price: negative price"

		require.EqualError(t, doc.Validate(t.Context(), each), want)

		_, err := doc.Decode[map[string]any](t.Context(), niceyaml.WithValidator(each))
		require.EqualError(t, err, want)
	})

	t.Run("a validator that checks another document names that document", func(t *testing.T) {
		t.Parallel()

		zero := rejectingValidator(niceyaml.NewError("no price", niceyaml.AtPath(pricePath)))
		include := niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
			return other.Validate(ctx, zero)
		})

		require.EqualError(t, doc.Validate(t.Context(), include), "other.yaml:2:8: $.price: no price")
	})

	t.Run("context a wrapper adds stands in front of the position", func(t *testing.T) {
		t.Parallel()

		wrapped := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			return fmt.Errorf("menu check: %w", n.Validate(ctx, rejectingValidator(negative)))
		})

		err := doc.Validate(t.Context(), wrapped)
		require.EqualError(t, err, "menu check: menu.yaml:1:8: $.price: negative price")

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, doc, yamltest.BoundNode(t, bound))
	})
}

func TestMultiValidator(t *testing.T) {
	t.Parallel()

	errB := errors.New("bad b")
	errC := errors.New("bad c")

	badB := rejectingValidator(niceyaml.Invalid(errB, niceyaml.AtPath(paths.Current().Child("a", "b"))))
	badC := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
		// A validator that binds its own error, as [niceyaml.Validator] allows.
		return n.Bind(niceyaml.Invalid(errC, niceyaml.AtPath(paths.Current().Child("a", "c"))))
	})
	// A validator of a type of its own that leaves its error unbound.
	unboundB := &fieldValidator{err: niceyaml.Invalid(errB, niceyaml.AtPath(paths.Current().Child("a", "b")))}
	passing := passingValidator()
	input := stringtest.LinesLF(
		"a:",
		"  b: 1",
		"  c: 2",
	)
	dd := yamltest.FirstDocument(t, input)
	// The same document under a file path.
	doc := yamltest.FirstDocument(t, input, niceyaml.WithFilePath("x.yaml"))

	t.Run("runs every validator and reports every failure in order", func(t *testing.T) {
		t.Parallel()

		var order []string

		multi := niceyaml.MultiValidator(
			recordingValidator(&order, "first"),
			badB,
			recordingValidator(&order, "second"),
			badC,
		)

		err := dd.Validate(t.Context(), multi)
		require.ErrorIs(t, err, errB)
		require.ErrorIs(t, err, errC)
		assert.Equal(t, []string{"first", "second"}, order)

		var got []string

		for _, b := range yamltest.BoundIn(err) {
			if rng, ok := b.Range(); ok {
				got = append(got, rng.Start.String()+" "+b.Message())
			}
		}

		assert.Equal(t, []string{"1:5 bad b", "2:5 bad c"}, got)
	})

	t.Run("renders as one tree", func(t *testing.T) {
		t.Parallel()

		err := dd.Validate(t.Context(), niceyaml.MultiValidator(badB, badC))
		require.Error(t, err)

		assert.Equal(t, stringtest.JoinLF(
			"|-- 2:6: $.a.b: bad b",
			"`-- 3:6: $.a.c: bad c",
		), report(err))
	})

	t.Run("no failure is no error", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, dd.Validate(t.Context(), niceyaml.MultiValidator(passing, passing)))
		require.NoError(t, dd.Validate(t.Context(), niceyaml.MultiValidator()))

		// A typed nil pointer reports no failure, as it does from a
		// validator given alone.
		typedNil := typedNilValidator()

		require.NoError(t, dd.Validate(t.Context(), typedNil))
		require.NoError(t, dd.Validate(t.Context(), niceyaml.MultiValidator(typedNil, passing)))
	})

	t.Run("skips a nil validator", func(t *testing.T) {
		t.Parallel()

		var order []string

		err := dd.Validate(t.Context(), niceyaml.MultiValidator(
			badB,
			nil,
			(*fieldValidator)(nil),
			niceyaml.ValidatorFunc(nil),
			recordingValidator(&order, "after"),
		))
		require.ErrorIs(t, err, errB)
		assert.Equal(t, []string{"after"}, order)
	})

	t.Run("keeps its validators when the caller edits the slice", func(t *testing.T) {
		t.Parallel()

		vs := []niceyaml.Validator{badB}
		multi := niceyaml.MultiValidator(vs...)
		vs[0] = passing

		err := dd.Validate(t.Context(), multi)
		require.ErrorIs(t, err, errB)
	})

	t.Run("WithValidator carries it to a decode", func(t *testing.T) {
		t.Parallel()

		multi := niceyaml.WithValidator(niceyaml.MultiValidator(badB, badC))

		_, err := dd.Decode[map[string]any](t.Context(), multi)
		require.ErrorIs(t, err, errB)
		require.ErrorIs(t, err, errC)
	})

	t.Run("each line of the message carries its source and position", func(t *testing.T) {
		t.Parallel()

		want := stringtest.JoinLF(
			"x.yaml:2:6: $.a.b: bad b",
			"x.yaml:3:6: $.a.c: bad c",
		)

		tcs := map[string]struct {
			first niceyaml.Validator
		}{
			"a ValidatorFunc that leaves its error unbound":   {first: badB},
			"a type of its own that leaves its error unbound": {first: unboundB},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				multi := niceyaml.MultiValidator(tc.first, badC)

				require.EqualError(t, doc.Validate(t.Context(), multi), want)

				// A direct call joins the failures as the validators wrote
				// them, so the one badC bound keeps its position and the
				// other has none.
				require.EqualError(t, multi.Check(t.Context(), doc), stringtest.JoinLF(
					"bad b",
					"x.yaml:3:6: $.a.c: bad c",
				))
			})
		}
	})

	t.Run("a lone failure keeps its position", func(t *testing.T) {
		t.Parallel()

		err := doc.Validate(t.Context(), niceyaml.MultiValidator(badB, passing))
		require.ErrorIs(t, err, errB)
		assert.Equal(t, "x.yaml:2:6: $.a.b: bad b", err.Error())

		serr := yamltest.RequireSourceError(t, err)

		_, ok := serr.Range()
		assert.True(t, ok)
		assert.Empty(t, yamltest.ProblemsOf(serr))

		// A validator that binds its own error names the source once.
		err = doc.Validate(t.Context(), niceyaml.MultiValidator(passing, badC))
		require.ErrorIs(t, err, errC)
		assert.Equal(t, "x.yaml:3:6: $.a.c: bad c", err.Error())

		// A validator that leaves its error unbound reads the same.
		err = doc.Validate(t.Context(), niceyaml.MultiValidator(passing, unboundB))
		require.ErrorIs(t, err, errB)
		assert.Equal(t, "x.yaml:2:6: $.a.b: bad b", err.Error())
	})

	t.Run("a sentinel around a binding stands in front of its position", func(t *testing.T) {
		t.Parallel()

		errInvalid := errors.New("invalid")
		classified := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			return fmt.Errorf("%w: %w", errInvalid, badC.Check(ctx, n))
		})

		err := doc.Validate(t.Context(), niceyaml.MultiValidator(classified, badB))
		require.ErrorIs(t, err, errInvalid)
		assert.Equal(t, stringtest.JoinLF(
			"x.yaml:2:6: $.a.b: bad b",
			"invalid: x.yaml:3:6: $.a.c: bad c",
		), err.Error())
	})

	t.Run("a context that ends stops the run", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())

		calls := 0
		canceling := niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
			calls++

			cancel()

			return ctx.Err()
		})

		err := dd.Validate(ctx, niceyaml.MultiValidator(badB, canceling, badC))
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, errB)
		assert.Equal(t, 1, calls)

		require.ErrorIs(t, dd.Validate(ctx, niceyaml.MultiValidator(canceling)), context.Canceled)
		assert.Equal(t, 1, calls, "an ended context runs no validator")
	})
}

func TestChainValidator(t *testing.T) {
	t.Parallel()

	errFirst := errors.New("first rule")
	errSecond := errors.New("second rule")

	namePath := paths.Current().Child("name")

	// Both validators fail at the same value, as a schema and a check
	// that decodes the node do.
	first := rejectingValidator(niceyaml.Invalid(errFirst, niceyaml.AtPath(namePath)))
	second := rejectingValidator(niceyaml.Invalid(errSecond, niceyaml.AtPath(namePath)))
	passing := passingValidator()

	// The message of both failures, as a MultiValidator joins them.
	both := stringtest.JoinLF(
		"x.yaml:3:7: $.name: first rule",
		"x.yaml:3:7: $.name: second rule",
	)
	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"meta:",
		"  name: 5",
		"name: 7",
	), niceyaml.WithName("x.yaml"))
	meta := yamltest.At(t, doc, paths.Current().Child("meta"))

	t.Run("runs the validators in order and stops at the first that fails", func(t *testing.T) {
		t.Parallel()

		var order []string

		chain := niceyaml.ChainValidator(
			recordingValidator(&order, "before"),
			first,
			recordingValidator(&order, "unreached"),
			second,
		)

		err := doc.Validate(t.Context(), chain)
		require.EqualError(t, err, "x.yaml:3:7: $.name: first rule")
		require.NotErrorIs(t, err, errSecond)
		assert.Equal(t, []string{"before"}, order)
	})

	t.Run("reports the first of two failures where MultiValidator reports both", func(t *testing.T) {
		t.Parallel()

		err := doc.Validate(t.Context(), niceyaml.ChainValidator(first, second))
		require.EqualError(t, err, "x.yaml:3:7: $.name: first rule")

		err = doc.Validate(t.Context(), niceyaml.ChainValidator(second, first))
		require.EqualError(t, err, "x.yaml:3:7: $.name: second rule")

		err = doc.Validate(t.Context(), niceyaml.MultiValidator(first, second))
		require.EqualError(t, err, both)
	})

	t.Run("a later validator runs once the ones before it pass", func(t *testing.T) {
		t.Parallel()

		var order []string

		// A typed nil pointer is no failure, so the chain goes on.
		err := doc.Validate(t.Context(), niceyaml.ChainValidator(
			recordingValidator(&order, "before"),
			typedNilValidator(),
			niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
				var e *niceyaml.SourceError

				return e
			}),
			recordingValidator(&order, "after"),
			second,
		))
		require.EqualError(t, err, "x.yaml:3:7: $.name: second rule")
		assert.Equal(t, []string{"before", "after"}, order)
	})

	t.Run("no failure is no error", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, doc.Validate(t.Context(), niceyaml.ChainValidator()))
		require.NoError(t, niceyaml.ChainValidator().Check(t.Context(), doc))
		require.NoError(t, doc.Validate(t.Context(), niceyaml.ChainValidator(passing, passing)))
		require.NoError(t, doc.Validate(t.Context(), niceyaml.ChainValidator(typedNilValidator())))
	})

	t.Run("skips a nil validator", func(t *testing.T) {
		t.Parallel()

		var order []string

		err := doc.Validate(t.Context(), niceyaml.ChainValidator(
			recordingValidator(&order, "before"),
			nil,
			(*fieldValidator)(nil),
			niceyaml.ValidatorFunc(nil),
			recordingValidator(&order, "after"),
			first,
		))
		require.ErrorIs(t, err, errFirst)
		assert.Equal(t, []string{"before", "after"}, order)

		require.NoError(t, doc.Validate(t.Context(), niceyaml.ChainValidator(nil)))
	})

	t.Run("keeps its validators when the caller edits the slice", func(t *testing.T) {
		t.Parallel()

		vs := []niceyaml.Validator{first}
		chain := niceyaml.ChainValidator(vs...)
		vs[0] = passing

		require.ErrorIs(t, doc.Validate(t.Context(), chain), errFirst)
	})

	t.Run("a document that did not parse returns its syntax error", func(t *testing.T) {
		t.Parallel()

		docs := niceyaml.NewSourceFromString("a: [\n").AllDocuments()
		require.Len(t, docs, 1)
		require.ErrorIs(t, docs[0].Err(), niceyaml.ErrSyntax)

		var order []string

		chain := niceyaml.ChainValidator(recordingValidator(&order, "unreached"))

		assert.Same(t, docs[0].Err(), docs[0].Validate(t.Context(), chain))
		assert.Same(t, docs[0].Err(), chain.Check(t.Context(), docs[0]))
		assert.Empty(t, order)
	})

	t.Run("returns the failure as the validator wrote it", func(t *testing.T) {
		t.Parallel()

		located := niceyaml.Invalid(errFirst, niceyaml.AtPath(namePath))

		tcs := map[string]struct {
			v    niceyaml.Validator
			node *niceyaml.Node
			want string
		}{
			"a ValidatorFunc that leaves its error unbound": {
				v:    first,
				node: doc,
				want: "x.yaml:3:7: $.name: first rule",
			},
			"a validator that leaves its error unbound": {
				v:    &fieldValidator{err: located},
				node: doc,
				want: "x.yaml:3:7: $.name: first rule",
			},
			"an unbound error with an @ path on a scoped Node": {
				v:    &fieldValidator{err: located},
				node: meta,
				want: "x.yaml:2:9: $.meta.name: first rule",
			},
			"an unbound error with no location on a scoped Node": {
				v:    &fieldValidator{err: niceyaml.Invalid(errFirst)},
				node: meta,
				want: "x.yaml:1:1: $.meta: first rule",
			},
			"an unbound plain error on a scoped Node": {
				v:    &fieldValidator{err: errFirst},
				node: meta,
				want: "x.yaml:1:1: $.meta: first rule",
			},
			"an unbound plain error on the root": {
				v:    &fieldValidator{err: errFirst},
				node: doc,
				want: "x.yaml: first rule",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				alone := tc.node.Validate(t.Context(), tc.v)
				require.EqualError(t, alone, tc.want)

				chain := niceyaml.ChainValidator(passing, tc.v, second)

				// A direct call returns the error as the validator wrote it.
				assert.Equal(t, tc.v.Check(t.Context(), tc.node), chain.Check(t.Context(), tc.node))

				err := tc.node.Validate(t.Context(), chain)
				require.EqualError(t, err, tc.want)
				assert.Equal(t, niceyaml.IsInvalid(alone), niceyaml.IsInvalid(err))
				assert.Equal(t, niceyaml.FormatError(alone), niceyaml.FormatError(err))

				bound := yamltest.RequireSourceError(t, err)
				assert.Same(t, tc.node, yamltest.BoundNode(t, bound))
			})
		}
	})

	t.Run("returns a bound error as it is", func(t *testing.T) {
		t.Parallel()

		want := meta.Bind(niceyaml.Invalid(errFirst, niceyaml.AtPath(namePath)))

		assert.Same(t, want, doc.Validate(t.Context(), niceyaml.ChainValidator(&fieldValidator{err: want})))
		assert.Same(t, want, doc.Validate(t.Context(), niceyaml.ChainValidator(rejectingValidator(want))))
	})

	t.Run("a decode runs repeated WithValidator options the same way", func(t *testing.T) {
		t.Parallel()

		unboundFirst := &fieldValidator{err: niceyaml.Invalid(errFirst, niceyaml.AtPath(namePath))}
		unboundSecond := &fieldValidator{err: niceyaml.Invalid(errSecond, niceyaml.AtPath(namePath))}

		tcs := map[string]struct {
			a, b niceyaml.Validator
			want string
			// Whether the Node at $.meta decodes, and not the root.
			scoped bool
		}{
			"both fail": {
				a:    first,
				b:    second,
				want: "x.yaml:3:7: $.name: first rule",
			},
			"both fail and leave their errors unbound": {
				a:    unboundFirst,
				b:    unboundSecond,
				want: "x.yaml:3:7: $.name: first rule",
			},
			"the second fails": {
				a:    passing,
				b:    second,
				want: "x.yaml:3:7: $.name: second rule",
			},
			"the second fails and leaves its error unbound": {
				a:    typedNilValidator(),
				b:    unboundSecond,
				want: "x.yaml:3:7: $.name: second rule",
			},
			"the first is nil": {
				a:    nil,
				b:    second,
				want: "x.yaml:3:7: $.name: second rule",
			},
			"an unbound error with an @ path on a scoped Node": {
				a:      unboundFirst,
				b:      unboundSecond,
				scoped: true,
				want:   "x.yaml:2:9: $.meta.name: first rule",
			},
			"an unbound plain error on a scoped Node": {
				a:      passing,
				b:      &fieldValidator{err: errSecond},
				scoped: true,
				want:   "x.yaml:1:1: $.meta: second rule",
			},
			"neither fails": {
				a: passing,
				b: typedNilValidator(),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				node := doc
				if tc.scoped {
					node = meta
				}

				chain := niceyaml.ChainValidator(tc.a, tc.b)

				_, chainedDecode := node.Decode[map[string]any](t.Context(), niceyaml.WithValidator(chain))
				_, repeatedDecode := node.Decode[map[string]any](t.Context(),
					niceyaml.WithValidator(tc.a),
					niceyaml.WithValidator(tc.b),
				)

				pairs := map[string][2]error{
					"Decode":   {chainedDecode, repeatedDecode},
					"Validate": {node.Validate(t.Context(), chain), repeatedDecode},
				}

				for step, pair := range pairs {
					got, want := pair[0], pair[1]

					if tc.want == "" {
						require.NoError(t, got, step)
						require.NoError(t, want, step)

						continue
					}

					require.EqualError(t, want, tc.want, step)
					require.EqualError(t, got, tc.want, step)
					assert.Equal(t, niceyaml.FormatError(want), niceyaml.FormatError(got), step)
					assert.Equal(t, fmt.Sprintf("%+v", want), fmt.Sprintf("%+v", got), step)
					assert.Equal(t, niceyaml.IsInvalid(want), niceyaml.IsInvalid(got), step)

					var gotBound, wantBound *niceyaml.SourceError

					require.ErrorAs(t, got, &gotBound, step)
					require.ErrorAs(t, want, &wantBound, step)
					assert.Same(t, node, yamltest.BoundNode(t, wantBound), step)
					assert.Same(t, node, yamltest.BoundNode(t, gotBound), step)

					wantPath, wantOK := wantBound.Path()
					gotPath, gotOK := gotBound.Path()
					assert.Equal(t, wantOK, gotOK, step)
					assert.Equal(t, wantPath.String(), gotPath.String(), step)
				}
			})
		}
	})

	t.Run("takes its place inside MultiValidator and SkipEmpty", func(t *testing.T) {
		t.Parallel()

		// Inside a MultiValidator, the chain reports its first failure
		// beside the failures of the validators around it.
		multi := niceyaml.MultiValidator(niceyaml.ChainValidator(first, second), second)
		require.EqualError(t, doc.Validate(t.Context(), multi), both)

		// Around one, the chain stops once the MultiValidator fails.
		var order []string

		chain := niceyaml.ChainValidator(
			niceyaml.MultiValidator(first, second),
			recordingValidator(&order, "unreached"),
		)
		require.EqualError(t, doc.Validate(t.Context(), chain), both)
		assert.Empty(t, order)

		// A chain inside a chain runs as one chain.
		nested := niceyaml.ChainValidator(niceyaml.ChainValidator(passing, second), first)
		require.EqualError(t, doc.Validate(t.Context(), nested), "x.yaml:3:7: $.name: second rule")

		empty := yamltest.FirstDocument(t, "# only a comment\n")

		// Inside a SkipEmpty, no validator of the chain runs on a document
		// with no content.
		require.NoError(t, empty.Validate(t.Context(), niceyaml.SkipEmpty(niceyaml.ChainValidator(first, second))))
		require.ErrorIs(
			t,
			doc.Validate(t.Context(), niceyaml.SkipEmpty(niceyaml.ChainValidator(first, second))),
			errFirst,
		)

		// Around one, the validators after it still run.
		skipFirst := niceyaml.ChainValidator(niceyaml.SkipEmpty(first), second)
		require.ErrorIs(t, empty.Validate(t.Context(), skipFirst), errSecond)
		require.ErrorIs(t, doc.Validate(t.Context(), skipFirst), errFirst)
	})

	t.Run("a context that ends stops the run at the validator that reports it", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())

		var order []string

		canceling := niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
			cancel()

			return ctx.Err()
		})

		err := doc.Validate(ctx, niceyaml.ChainValidator(canceling, recordingValidator(&order, "unreached")))
		require.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, order)
	})
}

func TestSkipEmpty(t *testing.T) {
	t.Parallel()

	errBad := errors.New("bad")

	// Rejecting returns a validator that fails every Node and counts its
	// runs.
	rejecting := func(calls *int) niceyaml.Validator {
		return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
			*calls++

			return niceyaml.Invalid(errBad)
		})
	}

	t.Run("passes a document with no content", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
		}{
			"empty file":           {input: ""},
			"whitespace":           {input: "\n"},
			"comments alone":       {input: "# only a comment\n"},
			"header alone":         {input: "---\n"},
			"header and comment":   {input: "---\n# Source: chart/templates/empty.yaml\n"},
			"directive and header": {input: "%YAML 1.2\n---\n"},
			"end marker alone":     {input: "...\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input)

				var calls int

				skip := niceyaml.SkipEmpty(rejecting(&calls))

				require.NoError(t, doc.Validate(t.Context(), skip))
				require.NoError(t, skip.Check(t.Context(), doc))
				assert.Zero(t, calls)

				// The validator alone still runs on the document.
				require.ErrorIs(t, doc.Validate(t.Context(), rejecting(&calls)), errBad)
				assert.Equal(t, 1, calls)
			})
		}
	})

	t.Run("runs the validator on a document that holds a value", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
		}{
			"null":                 {input: "null\n"},
			"tilde":                {input: "~\n"},
			"null below a header":  {input: "--- null\n"},
			"empty mapping":        {input: "{}\n"},
			"empty sequence":       {input: "[]\n"},
			"empty string":         {input: "\"\"\n"},
			"alias with no anchor": {input: "--- *nope\n"},
			"mapping":              {input: "a: 1\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input)

				var calls int

				err := doc.Validate(t.Context(), niceyaml.SkipEmpty(rejecting(&calls)))
				require.ErrorIs(t, err, errBad)
				assert.Equal(t, 1, calls)
			})
		}
	})

	t.Run("runs the validator on a scoped Node", func(t *testing.T) {
		t.Parallel()

		// The key holds no value of its own, and its Node is no document.
		value := yamltest.At(t, yamltest.FirstDocument(t, "a:\n"), paths.Current().Child("a"))

		var calls int

		err := value.Validate(t.Context(), niceyaml.SkipEmpty(rejecting(&calls)))
		require.ErrorIs(t, err, errBad)
		assert.Equal(t, 1, calls)
	})

	t.Run("a document that did not parse returns its syntax error", func(t *testing.T) {
		t.Parallel()

		for _, input := range []string{"a: [\n", "---\na: [\n"} {
			docs := niceyaml.NewSourceFromString(input).AllDocuments()
			require.Len(t, docs, 1, input)
			require.ErrorIs(t, docs[0].Err(), niceyaml.ErrSyntax, input)

			var calls int

			skip := niceyaml.SkipEmpty(rejecting(&calls))

			assert.Same(t, docs[0].Err(), docs[0].Validate(t.Context(), skip), input)
			assert.Same(t, docs[0].Err(), skip.Check(t.Context(), docs[0]), input)
			assert.Zero(t, calls, input)
		}
	})

	t.Run("runs the validator and the Node binds its error", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF("a:", "  b: 1"), niceyaml.WithFilePath("x.yaml"))

		located := niceyaml.Invalid(errBad, niceyaml.AtPath(paths.Current().Child("a", "b")))
		want := "x.yaml:2:6: $.a.b: bad"

		tcs := map[string]struct {
			v niceyaml.Validator
		}{
			"a type of its own": {v: &fieldValidator{err: located}},
			"a ValidatorFunc":   {v: rejectingValidator(located)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				skip := niceyaml.SkipEmpty(tc.v)

				// A direct call returns the error as the validator wrote it.
				require.Same(t, located, skip.Check(t.Context(), doc))
				require.EqualError(t, doc.Validate(t.Context(), skip), want)
			})
		}
	})

	t.Run("a nil validator passes every Node", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "a: 1\n")

		for _, v := range []niceyaml.Validator{nil, (*fieldValidator)(nil), niceyaml.ValidatorFunc(nil)} {
			require.NoError(t, doc.Validate(t.Context(), niceyaml.SkipEmpty(v)))
		}

		// A typed nil pointer reports no failure, as it does from a
		// validator given alone.
		typedNil := &fieldValidator{err: (*niceyaml.Error)(nil)}

		require.NoError(t, doc.Validate(t.Context(), niceyaml.SkipEmpty(typedNil)))
	})

	t.Run("a decode of an empty document returns the zero value", func(t *testing.T) {
		t.Parallel()

		type config struct {
			Name string `yaml:"name"`
		}

		named := schema.MustCompile([]byte(`{"type": "object", "required": ["name"]}`))

		for name, input := range map[string]string{
			"empty file":     "",
			"comments alone": "# Defaults apply.\n",
			"header alone":   "---\n",
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, input)

				got, err := doc.Decode[config](t.Context(), niceyaml.WithValidator(niceyaml.SkipEmpty(named)))
				require.NoError(t, err)
				assert.Equal(t, config{}, got)

				// The schema alone rejects the null the document decodes to.
				_, err = doc.Decode[config](t.Context(), niceyaml.WithValidator(named))
				require.ErrorContains(t, err, `expected "object", got "null"`)
				assert.True(t, niceyaml.IsInvalid(err))
			})
		}

		// A file with content still answers to the schema.
		_, err := yamltest.FirstDocument(t, "port: 1\n").
			Decode[config](t.Context(), niceyaml.WithValidator(niceyaml.SkipEmpty(named)))
		require.ErrorContains(t, err, `missing required property "name"`)
	})

	t.Run("wraps one validator and takes its place among several", func(t *testing.T) {
		t.Parallel()

		empty := yamltest.FirstDocument(t, "# only a comment\n")

		var first, second int

		// Around a MultiValidator, it passes the document for all of them.
		all := niceyaml.SkipEmpty(niceyaml.MultiValidator(rejecting(&first), rejecting(&second)))
		require.NoError(t, empty.Validate(t.Context(), all))
		assert.Zero(t, first)
		assert.Zero(t, second)

		// Inside one, the validators beside it still run.
		one := niceyaml.MultiValidator(niceyaml.SkipEmpty(rejecting(&first)), rejecting(&second))
		require.ErrorIs(t, empty.Validate(t.Context(), one), errBad)
		assert.Zero(t, first)
		assert.Equal(t, 1, second)
	})
}

func TestNode_Validate(t *testing.T) {
	t.Parallel()

	// The document of the subtests that scope a Node to its key meta.
	input := stringtest.LinesLF("meta:", "  name: test")

	t.Run("runs the validator on the receiver", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)
		scoped := yamltest.At(t, dd, paths.Current().Child("meta"))

		var got []*niceyaml.Node

		err := scoped.Validate(t.Context(), niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			got = append(got, n)

			return errNameRequired
		}))
		require.ErrorIs(t, err, errNameRequired)
		require.Len(t, got, 1)
		assert.Same(t, scoped, got[0])
	})

	t.Run("the validator decides how many failures the node reports", func(t *testing.T) {
		t.Parallel()

		var order []string

		doc := yamltest.FirstDocument(t, input, niceyaml.WithName("x.yaml"))

		scoped := yamltest.At(t, doc, paths.Current().Child("meta"))

		namePath := paths.Current().Child("name")
		reserved := &fieldValidator{err: niceyaml.NewError("reserved name", niceyaml.AtPath(namePath))}
		short := &fieldValidator{err: niceyaml.NewError("name is too short", niceyaml.AtPath(namePath))}

		// A ChainValidator stops at the first validator that fails.
		err := scoped.Validate(t.Context(), niceyaml.ChainValidator(
			recordingValidator(&order, "first"),
			reserved,
			recordingValidator(&order, "unreached"),
			short,
		))
		require.EqualError(t, err, "x.yaml:2:9: $.meta.name: reserved name")
		assert.Equal(t, []string{"first"}, order)

		order = nil

		// A MultiValidator runs every one and joins the failures.
		err = scoped.Validate(t.Context(), niceyaml.MultiValidator(
			recordingValidator(&order, "first"),
			reserved,
			recordingValidator(&order, "second"),
			short,
		))
		require.EqualError(t, err, stringtest.JoinLF(
			"x.yaml:2:9: $.meta.name: reserved name",
			"x.yaml:2:9: $.meta.name: name is too short",
		))
		assert.Equal(t, []string{"first", "second"}, order)
	})

	t.Run("a typed nil pointer is no failure", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\n")

		for _, v := range []niceyaml.Validator{
			typedNilValidator(),
			&fieldValidator{err: (*niceyaml.Error)(nil)},
			&fieldValidator{err: (*niceyaml.SourceError)(nil)},
		} {
			require.NoError(t, dd.Validate(t.Context(), v))
		}
	})

	t.Run("binds the error a validator leaves unbound", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, input, niceyaml.WithName("x.yaml"))

		scoped := yamltest.At(t, doc, paths.Current().Child("meta"))
		unbound := &fieldValidator{
			err: niceyaml.Invalid(errNameRequired, niceyaml.AtPath(paths.Current().Child("name"))),
		}

		// A direct call returns the error as the validator wrote it, and
		// the Node binds it from its own scope.
		require.EqualError(t, unbound.Check(t.Context(), scoped), "name is required")

		err := scoped.Validate(t.Context(), unbound)
		require.EqualError(t, err, "x.yaml:2:9: $.meta.name: name is required")

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, scoped, yamltest.BoundNode(t, bound))
	})

	t.Run("returns a bound error as it is", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)
		scoped := yamltest.At(t, dd, paths.Current().Child("meta"))

		want := scoped.Bind(niceyaml.Invalid(errNameRequired, niceyaml.AtPath(paths.Current().Child("name"))))

		assert.Same(t, want, dd.Validate(t.Context(), &fieldValidator{err: want}))
		assert.Same(t, want, dd.Validate(t.Context(), rejectingValidator(want)))
	})

	t.Run("a nil validator returns what Err returns", func(t *testing.T) {
		t.Parallel()

		docs := niceyaml.NewSourceFromString(stringtest.LinesLF(
			"name: test",
			"---",
			"name: [",
		), niceyaml.WithName("f.yaml")).AllDocuments()
		require.Len(t, docs, 2)
		require.NoError(t, docs[0].Err())
		require.EqualError(t, docs[1].Err(), "f.yaml:3:7: sequence end token ']' not found")

		scoped := yamltest.At(t, docs[0], paths.Current().Child("name"))

		// A nil pointer to a fieldValidator panics if it runs.
		for _, v := range []niceyaml.Validator{nil, (*fieldValidator)(nil), niceyaml.ValidatorFunc(nil)} {
			require.NoError(t, docs[0].Validate(t.Context(), v))
			require.NoError(t, scoped.Validate(t.Context(), v))
			assert.Same(t, docs[1].Err(), docs[1].Validate(t.Context(), v))
		}

		// A context that ended changes neither result.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		require.NoError(t, docs[0].Validate(ctx, nil))
		assert.Same(t, docs[1].Err(), docs[1].Validate(ctx, nil))
	})

	t.Run("a validator of one call reaches no other decode", func(t *testing.T) {
		t.Parallel()

		var order []string

		docs := niceyaml.NewSourceFromString(stringtest.LinesLF(
			"name: a",
			"---",
			"name: b",
		)).Documents()

		_, err := docs[0].Decode[map[string]any](
			t.Context(),
			niceyaml.WithValidator(recordingValidator(&order, "call")),
		)
		require.NoError(t, err)

		order = nil

		_, err = docs[1].Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Empty(t, order, "the validator of one call ran on another")
	})
}

func TestDocument_Validate_NodePaths(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		spec:
		  containers:
		    - name: a
		      image: ""
		    - name: b
		      image: nginx
	`), niceyaml.WithFilePath("m.yaml"))

	// The check names each image by the path its Node reports, which starts
	// at `$`, and binds through the Node it got.
	images := func(selector paths.Path) niceyaml.Validator {
		return niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			nodes, err := n.Nodes(selector)
			if err != nil {
				return fmt.Errorf("images: %w", err)
			}

			var errs []error

			for _, image := range nodes {
				got, err := image.Decode[string](ctx)
				if err != nil {
					return fmt.Errorf("image: %w", err)
				}

				if got == "" {
					errs = append(errs, n.Bind(niceyaml.NewError("image is empty", niceyaml.AtPath(image.Path()))))
				}
			}

			return niceyaml.NewSummary(fmt.Sprintf("%d empty images", len(errs)), errs...)
		})
	}

	const want = "m.yaml:4:14: $.spec.containers[0].image: image is empty"

	tcs := map[string]struct {
		scope    paths.Path
		selector paths.Path
	}{
		"root": {
			scope:    paths.Doc(),
			selector: paths.Current().Child("spec", "containers").IndexAll().Child("image"),
		},
		"scoped": {
			scope:    paths.Doc().Child("spec"),
			selector: paths.Current().Child("containers").IndexAll().Child("image"),
		},
		"scoped with an absolute selector": {
			scope:    paths.Doc().Child("spec"),
			selector: paths.Doc().Child("spec", "containers").IndexAll().Child("image"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := yamltest.At(t, doc, tc.scope)

			err := node.Validate(t.Context(), images(tc.selector))
			require.EqualError(t, err, want)

			bound := yamltest.RequireSourceError(t, err)

			path, ok := bound.Path()
			require.True(t, ok)
			assert.Equal(t, paths.MustParse("$.spec.containers[0].image"), path)

			pos, ok := bound.Position()
			require.True(t, ok)
			assert.Equal(t, position.New(3, 13), pos)

			_, err = node.Decode[any](t.Context(), niceyaml.WithValidator(images(tc.selector)))
			require.EqualError(t, err, want)
		})
	}
}
