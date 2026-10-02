package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// The error tier reports for text it does not know.
var errUnknownTier = errors.New("unknown tier")

// tier decodes itself from text and reports errUnknownTier for any text
// but "low" and "high".
type tier int

func (t *tier) UnmarshalText(text []byte) error {
	switch string(text) {
	case "low":
		*t = 1
	case "high":
		*t = 2
	default:
		return fmt.Errorf("%w %q", errUnknownTier, text)
	}

	return nil
}

// tierServer holds a value of each kind the decoder returns the error of
// with no token.
type tierServer struct {
	Name    string        `yaml:"name"`
	Timeout time.Duration `yaml:"timeout"`
	Tier    tier          `yaml:"tier"`
}

// mirrored decodes itself through a second type with the same fields, so
// the error of a field comes back as the field reported it.
type mirrored struct {
	Name    string       `yaml:"name"`
	Servers []tierServer `yaml:"servers"`
}

func (m *mirrored) UnmarshalYAML(unmarshal func(any) error) error {
	type plain mirrored

	return unmarshal((*plain)(m))
}

// prefixing decodes itself through a second type and puts text of its
// own in front of the error, so no field below it reports that message.
type prefixing struct {
	Server tierServer
}

func (p *prefixing) UnmarshalYAML(unmarshal func(any) error) error {
	err := unmarshal(&p.Server)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}

	return nil
}

// opaqueValue decodes itself from YAML bytes and rejects any that hold
// the word bad, so its fields mirror nothing in the document.
type opaqueValue struct{}

func (*opaqueValue) UnmarshalYAML(data []byte) error {
	if strings.Contains(string(data), "bad") {
		return errUnmarshal
	}

	return nil
}

// jsonDecoded decodes itself from JSON, which the decoder hands it only
// under yaml.UseJSONUnmarshaler.
type jsonDecoded struct {
	V string `yaml:"v"`
}

func (*jsonDecoded) UnmarshalJSON([]byte) error {
	return errUnmarshal
}

// jsonFielded has an UnmarshalJSON method the decoder calls only under
// yaml.UseJSONUnmarshaler. Without that option the decoder reads its
// field.
type jsonFielded struct {
	Tier tier `yaml:"tier"`
}

func (*jsonFielded) UnmarshalJSON([]byte) error {
	return nil
}

// lockable decodes itself from text unless the value it decodes into is
// locked already, which only a value the caller filled can be.
type lockable struct {
	Text   string
	Locked bool
}

func (l *lockable) UnmarshalText(text []byte) error {
	if l.Locked {
		return errUnmarshal
	}

	l.Text = string(text)

	return nil
}

// positioned decodes itself from its node and reports an error at the
// position of that node.
type positioned struct{}

func (*positioned) UnmarshalYAML(node ast.Node) error {
	return niceyaml.WrapError(errUnmarshal, niceyaml.AtPosition(position.NewFromToken(node.GetToken())))
}

// optionDecoded has no method of its own. A yaml.CustomUnmarshaler
// option decodes it.
type optionDecoded struct{}

// cancelKey is the context key of the function canceling calls.
type cancelKey struct{}

// canceling decodes itself with the context of the decode, which it ends
// before it reports errUnmarshal.
type canceling struct{}

func (*canceling) UnmarshalYAML(ctx context.Context, _ []byte) error {
	cancel, ok := ctx.Value(cancelKey{}).(context.CancelFunc)
	if ok {
		cancel()
	}

	return errUnmarshal
}

func TestDocument_Decode_UnmarshalerError(t *testing.T) {
	t.Parallel()

	t.Run("binds at the value that reported it", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			input  string
			want   string
			path   string
		}{
			"duration field": {
				input: "name: api\ntimeout: soon\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[tierServer](ctx)

					return err
				},
				want: `2:10: $.timeout: time: invalid duration "soon"`,
				path: "$.timeout",
			},
			"text unmarshaler in a sequence of structs": {
				input: "- name: a\n  tier: low\n- name: b\n  tier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[[]tierServer](ctx)

					return err
				},
				want: `4:9: $[1].tier: unknown tier "mid"`,
				path: "$[1].tier",
			},
			"pointer element": {
				input: "tiers: [low, mid]\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Tiers []*tier `yaml:"tiers"`
					}](ctx)

					return err
				},
				want: `1:14: $.tiers[1]: unknown tier "mid"`,
				path: "$.tiers[1]",
			},
			"map key": {
				input: "hosts:\n  10.0.0.1: a\n  nope: b\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Hosts map[netip.Addr]string `yaml:"hosts"`
					}](ctx)

					return err
				},
				want: `3:3: $.hosts.nope~: ParseAddr("nope"): unable to parse IP`,
				path: "$.hosts.nope~",
			},
			"map value": {
				input: "a: low\nb: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[map[string]tier](ctx)

					return err
				},
				want: `2:4: $.b: unknown tier "mid"`,
				path: "$.b",
			},
			// The path through a merge key resolves to the entry of the
			// mapping the key brings in.
			"map value a merge key brings in": {
				input: "base: &base\n  timeout: soon\nservers:\n  api:\n    <<: *base\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Servers map[string]tierServer `yaml:"servers"`
					}](ctx)

					return err
				},
				want: `2:12: $.servers.api.timeout: time: invalid duration "soon"`,
				path: "$.servers.api.timeout",
			},
			"map entry a merge key brings in": {
				input: "base: &base\n  b: mid\ntiers:\n  a: low\n  <<: *base\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Tiers map[string]tier `yaml:"tiers"`
					}](ctx)

					return err
				},
				want: `2:6: $.tiers.b: unknown tier "mid"`,
				path: "$.tiers.b",
			},
			// The path of a value an alias holds points at the alias.
			"alias": {
				input: "label: &label mid\ntiers: [low, *label]\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Label string `yaml:"label"`
						Tiers []tier `yaml:"tiers"`
					}](ctx)

					return err
				},
				want: `2:14: $.tiers[1]: unknown tier "mid"`,
				path: "$.tiers[1]",
			},
			"inline struct": {
				input: "id: 1\nname: api\ntier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Server tierServer `yaml:",inline"`
						ID     int        `yaml:"id"`
					}](ctx)

					return err
				},
				want: `3:7: $.tier: unknown tier "mid"`,
				path: "$.tier",
			},
			"inline map": {
				input: "id: low\nextra: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Tiers map[string]tier `yaml:",inline"`
						ID    string          `yaml:"id"`
					}](ctx)

					return err
				},
				want: `2:8: $.extra: unknown tier "mid"`,
				path: "$.extra",
			},
			// The decoder reads the fields of a struct in the order the
			// struct declares them, so the first field that fails reports
			// the error, wherever the document puts it.
			"first field in struct order": {
				input: "tier: mid\ntimeout: soon\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[tierServer](ctx)

					return err
				},
				want: `2:10: $.timeout: time: invalid duration "soon"`,
				path: "$.timeout",
			},
			"value that decodes through a second type": {
				input: "name: prod\nservers:\n  - name: a\n    tier: low\n  - name: b\n    tier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[mirrored](ctx)

					return err
				},
				want: `6:11: $.servers[1].tier: unknown tier "mid"`,
				path: "$.servers[1].tier",
			},
			"field that decodes through a second type": {
				input: "prod:\n  servers:\n    - timeout: soon\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Prod mirrored `yaml:"prod"`
					}](ctx)

					return err
				},
				want: `3:16: $.prod.servers[0].timeout: time: invalid duration "soon"`,
				path: "$.prod.servers[0].timeout",
			},
			// No field below the value reports the message with the text
			// the value put in front, so the error points at the value.
			"value that adds text to the error of a field": {
				input: "main:\n  name: api\n  tier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Main prefixing `yaml:"main"`
					}](ctx)

					return err
				},
				want: `2:3: $.main: server: unknown tier "mid"`,
				path: "$.main",
			},
			"value whose fields mirror nothing": {
				input: "name: api\nvalue:\n  a: 1\n  b: bad\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Name  string      `yaml:"name"`
						Value opaqueValue `yaml:"value"`
					}](ctx)

					return err
				},
				want: `3:3: $.value: unmarshaler rejected the value`,
				path: "$.value",
			},
			"json unmarshaler under its option": {
				input: "name: api\nvalue:\n  v: 1\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Name  string      `yaml:"name"`
						Value jsonDecoded `yaml:"value"`
					}](ctx, niceyaml.WithYAMLDecodeOptions(yaml.UseJSONUnmarshaler()))

					return err
				},
				want: `3:3: $.value: unmarshaler rejected the value`,
				path: "$.value",
			},
			// The decoder reads the value field by field, so the field
			// reported the error.
			"json unmarshaler without its option": {
				input: "name: api\nvalue:\n  tier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Name  string      `yaml:"name"`
						Value jsonFielded `yaml:"value"`
					}](ctx)

					return err
				},
				want: `3:9: $.value.tier: unknown tier "mid"`,
				path: "$.value.tier",
			},
			// The scalar the decode reads is the value, so the error
			// points at the node itself, which the path names from the
			// root of the document.
			"scalar the decode reads": {
				input: "name: api\ntier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					node, err := dd.At(paths.Root().Child("tier"))
					if err != nil {
						return err //nolint:wrapcheck // The test inspects the error as it is.
					}

					_, err = node.Decode[tier](ctx)

					return err
				},
				want: `2:7: $.tier: unknown tier "mid"`,
				path: "$.tier",
			},
			"value below a scoped node": {
				input: "servers:\n  - name: a\n    tier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					node, err := dd.At(paths.Root().Child("servers").Index(0))
					if err != nil {
						return err //nolint:wrapcheck // The test inspects the error as it is.
					}

					_, err = node.Decode[tierServer](ctx)

					return err
				},
				want: `3:11: $.servers[0].tier: unknown tier "mid"`,
				path: "$.servers[0].tier",
			},
			"decoder": {
				input: "name: api\ntier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					var v tierServer

					return niceyaml.NewDecoder().DecodeInto(ctx, dd, &v)
				},
				want: `2:7: $.tier: unknown tier "mid"`,
				path: "$.tier",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)

				var srcErr *niceyaml.SourceError

				require.ErrorAs(t, err, &srcErr)

				path, ok := srcErr.Path()
				require.True(t, ok, "the error carries no path")
				assert.Equal(t, tc.path, path.String())

				_, ok = srcErr.Range()
				assert.True(t, ok, "the error carries no location")
			})
		}
	})

	t.Run("comes back with no location", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			input  string
			want   string
		}{
			// The fields of the value mirror nothing, and the mapping the
			// decode reads is the whole of what the caller asked for.
			"mapping the decode reads": {
				input: "a: 1\nb: bad\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[opaqueValue](ctx)

					return err
				},
				want: "unmarshaler rejected the value",
			},
			"value that adds text at the node the decode reads": {
				input: "name: api\ntier: mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[prefixing](ctx)

					return err
				},
				want: `server: unknown tier "mid"`,
			},
			// The second decode reads a new value, which is not locked.
			"unmarshaler that reads the value the caller filled": {
				input: "a: x\nb: y\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					v := struct {
						A lockable `yaml:"a"`
						B lockable `yaml:"b"`
					}{B: lockable{Locked: true}}

					return dd.DecodeInto(ctx, &v)
				},
				want: "unmarshaler rejected the value",
			},
			// The decoder shows no type an option decodes.
			"type an option decodes": {
				input: "name: api\nvalue: bad\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						Name  string        `yaml:"name"`
						Value optionDecoded `yaml:"value"`
					}](ctx, niceyaml.WithYAMLDecodeOptions(yaml.CustomUnmarshaler(
						func(*optionDecoded, []byte) error { return errUnmarshal },
					)))

					return err
				},
				want: "unmarshaler rejected the value",
			},
			// No path resolves through an alias to a reference document.
			"value an alias reads from a reference document": {
				input: "timeout: *limit\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[tierServer](ctx, niceyaml.WithReferences([]byte("limit: &limit soon\n")))

					return err
				},
				want: `time: invalid duration "soon"`,
			},
			// Every decode of the node fails the same way, whatever the
			// target, so no value of the target reported the error.
			"error of the decoder itself": {
				input: "timeout: soon\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[tierServer](ctx, niceyaml.WithYAMLDecodeOptions(
						yaml.ReferenceFiles("testdata/no-such-reference.yaml"),
					))

					return err
				},
				want: "open testdata/no-such-reference.yaml: no such file or directory",
			},
			// The walk decodes nothing once the context has ended.
			"context an unmarshaler ends": {
				input: "- low\n- mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					ctx, cancel := context.WithCancel(ctx)
					defer cancel()

					_, err := dd.Decode[[]canceling](context.WithValue(ctx, cancelKey{}, cancel))

					return err
				},
				want: "unmarshaler rejected the value",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)
				require.NotErrorIs(t, err, niceyaml.ErrDecodeRejected)

				var srcErr *niceyaml.SourceError

				require.ErrorAs(t, err, &srcErr)

				_, ok := srcErr.Path()
				assert.False(t, ok, "the error took a path")

				_, ok = srcErr.Range()
				assert.False(t, ok, "the error took a location")
			})
		}
	})

	t.Run("only a duration matches the rejection", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			input  string
			want   string
		}{
			"field": {
				input: "name: api\ntimeout: 5 minutes\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[tierServer](ctx)

					return err
				},
				want: `2:10: $.timeout: time: unknown unit " minutes" in duration "5 minutes"`,
			},
			"scalar the decode reads": {
				input: "5 minutes\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[time.Duration](ctx)

					return err
				},
				want: `1:1: $: time: unknown unit " minutes" in duration "5 minutes"`,
			},
			"map key": {
				input: "5 minutes: a\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[map[time.Duration]string](ctx)

					return err
				},
				want: `1:1: $.'5 minutes'~: time: unknown unit " minutes" in duration "5 minutes"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecodeRejected)
			})
		}

		// The error of an unmarshaler is the value's own, so it keeps what
		// it matched and gains nothing.
		dd := yamltest.FirstDocument(t, "name: api\ntier: mid\n")

		_, err := dd.Decode[tierServer](t.Context())
		require.ErrorIs(t, err, errUnknownTier)
		require.NotErrorIs(t, err, niceyaml.ErrDecodeRejected)
	})

	t.Run("keeps the location the error carries", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: api\nvalue: [a, b]\n")

		_, err := dd.Decode[struct {
			Name  string     `yaml:"name"`
			Value positioned `yaml:"value"`
		}](t.Context())
		require.EqualError(t, err, "2:8: unmarshaler rejected the value")

		var srcErr *niceyaml.SourceError

		require.ErrorAs(t, err, &srcErr)

		_, ok := srcErr.Path()
		assert.False(t, ok, "the error took a path")
	})

	t.Run("leaves the decoded value alone", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "a: x\nb: y\nc: z\n")

		// The second decode fills values of its own, so the value the
		// caller holds keeps what the first decode set.
		var v struct {
			A lockable `yaml:"a"`
			B tier     `yaml:"b"`
			C lockable `yaml:"c"`
		}

		err := dd.DecodeInto(t.Context(), &v)
		require.EqualError(t, err, `2:4: $.b: unknown tier "y"`)
		assert.Equal(t, lockable{Text: "x"}, v.A)
		assert.Equal(t, lockable{Text: "z"}, v.C)
	})
}
