package layermerge_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/layermerge"
)

func TestText(t *testing.T) {
	t.Parallel()

	// Each case holds the value under the key a, as the layer spells it,
	// and the text the merged document writes for it.
	tcs := map[string]struct {
		value string
		want  string
	}{
		"a plain scalar":           {value: "x", want: "x"},
		"a number keeps its text":  {value: "0x10", want: "0x10"},
		"a single-quoted string":   {value: "'it''s'", want: "'it''s'"},
		"a double-quoted string":   {value: `"x"`, want: `"x"`},
		"a tab takes an escape":    {value: `"a\tb"`, want: `"a\tb"`},
		"a dash and a space":       {value: `"- x"`, want: `"- x"`},
		"a colon and a space":      {value: `"a: b"`, want: `"a: b"`},
		"a comment marker":         {value: `"a #b"`, want: `"a #b"`},
		"a document marker":        {value: `"---"`, want: `"---"`},
		"a block scalar":           {value: "|\n    two\n    lines", want: `"two\nlines\n"`},
		"a tag":                    {value: "!!str 1", want: "!!str 1"},
		"an empty mapping":         {value: "{}", want: "{}"},
		"an empty sequence":        {value: "[]", want: "[]"},
		"a null with no text":      {value: "", want: ""},
		"a leading flow indicator": {value: `"[x"`, want: `"[x"`},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v, err := read(t, "a: "+tc.value+"\n", 0)
			require.NoError(t, err)

			want := "a: " + tc.want + "\n"
			if tc.want == "" {
				want = "a:\n"
			}

			assert.Equal(t, want, layermerge.Text("", v))
		})
	}
}

func TestText_Preamble(t *testing.T) {
	t.Parallel()

	v, err := read(t, stringtest.LinesLF("a:", "  - b: 1", "    c: !t {}"), 0)
	require.NoError(t, err)

	assert.Equal(t,
		stringtest.LinesLF("# head", "a:", "  - b: 1", "    c: !t {}"),
		layermerge.Text("# head\n", v),
	)
	assert.Equal(t, "# head\n", layermerge.Text("# head\n", nil))
}
