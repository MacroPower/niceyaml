package niceyaml_test

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
)

func TestStyles_Style(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		styles     niceyaml.Styles
		query      niceyaml.Style
		wantBold   bool
		wantItalic bool
	}{
		"returns defined style": {
			styles: niceyaml.Styles{
				niceyaml.StyleKey: lipgloss.NewStyle().Bold(true),
			},
			query:    niceyaml.StyleKey,
			wantBold: true,
		},
		"falls back to default when style not defined": {
			styles: niceyaml.Styles{
				niceyaml.StyleDefault: lipgloss.NewStyle().Italic(true),
			},
			query:      niceyaml.StyleKey,
			wantItalic: true,
		},
		"returns empty style when no default defined": {
			styles: niceyaml.Styles{},
			query:  niceyaml.StyleKey,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.styles.Style(tc.query)

			require.NotNil(t, got)
			assert.Equal(t, tc.wantBold, got.GetBold())
			assert.Equal(t, tc.wantItalic, got.GetItalic())
		})
	}
}
