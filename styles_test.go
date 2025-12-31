package niceyaml_test

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml/lexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/position"
)

func TestStyles_GetStyle(t *testing.T) {
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

			got := tc.styles.GetStyle(tc.query)

			require.NotNil(t, got)
			assert.Equal(t, tc.wantBold, got.GetBold())
			assert.Equal(t, tc.wantItalic, got.GetItalic())
		})
	}
}

func TestPrinter_BlendColors(t *testing.T) {
	t.Parallel()

	// Test color blending through overlapping range styles on diff lines.
	// When alwaysBlend is true (used for diff lines), overlapping ranges blend colors.

	input := "key: value"
	tks := lexer.Tokenize(input)

	// Create two overlapping ranges with different colors.
	style1 := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")) // Red.
	style2 := lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")) // Blue.

	p := niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithGutter(niceyaml.NoGutter),
	)

	// Add overlapping ranges.
	p.AddStyleToRange(&style1, position.NewRange(
		position.New(0, 0),
		position.New(0, 5),
	))
	p.AddStyleToRange(&style2, position.NewRange(
		position.New(0, 2),
		position.New(0, 7),
	))

	// When we create a diff line (which uses alwaysBlend=true),
	// the overlapping region should show blended colors.
	before := "key: old"
	after := "key: value"

	beforeLines := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterLines := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	revBefore := niceyaml.NewRevision(beforeLines)
	revAfter := niceyaml.NewRevision(afterLines)

	diff := niceyaml.NewFullDiff(revBefore, revAfter)

	// The print should succeed without panic.
	got := p.Print(diff.Lines())
	require.NotEmpty(t, got)

	// Also test the non-diff path for blending.
	p2 := niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithGutter(niceyaml.NoGutter),
	)

	// Add overlapping ranges.
	p2.AddStyleToRange(&style1, position.NewRange(
		position.New(0, 0),
		position.New(0, 5),
	))
	p2.AddStyleToRange(&style2, position.NewRange(
		position.New(0, 2),
		position.New(0, 7),
	))

	got2 := p2.Print(niceyaml.NewSourceFromTokens(tks))
	require.NotEmpty(t, got2)
}

func TestPrinter_BlendStyles_WithTransforms(t *testing.T) {
	t.Parallel()

	// Test that transforms are composed when blending styles.
	input := "key: value"

	transform1 := func(s string) string { return "[" + s }
	transform2 := func(s string) string { return s + "]" }

	style1 := lipgloss.NewStyle().Transform(transform1)
	style2 := lipgloss.NewStyle().Transform(transform2)

	// Create a diff to trigger alwaysBlend=true path.
	before := "key: old"
	after := "key: value"

	beforeLines := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterLines := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	p := niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithGutter(niceyaml.NoGutter),
	)

	// Add overlapping ranges with transforms.
	p.AddStyleToRange(&style1, position.NewRange(
		position.New(0, 0),
		position.New(0, 5),
	))
	p.AddStyleToRange(&style2, position.NewRange(
		position.New(0, 2),
		position.New(0, 7),
	))

	revBefore := niceyaml.NewRevision(beforeLines)
	revAfter := niceyaml.NewRevision(afterLines)

	diff := niceyaml.NewFullDiff(revBefore, revAfter)

	got := p.Print(diff.Lines())

	// Should contain transformed output.
	require.NotEmpty(t, got)
	// The test verifies that the transform composition doesn't panic
	// and produces some output.
	assert.Contains(t, got, input)
}

func TestPrinter_OverrideColor_InvisibleColor(t *testing.T) {
	t.Parallel()

	// Test the invisible color case in overrideColor.
	// When a color is technically valid but invisible (e.g., fully transparent),
	// it should fall back to the base color.

	input := "key: value"
	tks := lexer.Tokenize(input)

	// Create a style with no foreground (NoColor).
	styleNoColor := lipgloss.NewStyle()
	// Create a style with a valid foreground.
	styleWithColor := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))

	p := niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{
			niceyaml.StyleDefault: styleWithColor,
		}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithGutter(niceyaml.NoGutter),
	)

	// Add a range with no color (should not override).
	p.AddStyleToRange(&styleNoColor, position.NewRange(
		position.New(0, 0),
		position.New(0, 3),
	))

	got := p.Print(niceyaml.NewSourceFromTokens(tks))

	// Should still render content without panic.
	require.NotEmpty(t, got)
	assert.Contains(t, got, "key")
}

func TestPrinter_BlendColors_NilColors(t *testing.T) {
	t.Parallel()

	// Test blending when one or both colors are nil/NoColor.
	input := "key: value"
	tks := lexer.Tokenize(input)

	// Style with no colors.
	noColorStyle := lipgloss.NewStyle()
	// Style with only foreground.
	fgOnlyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	// Style with only background.
	bgOnlyStyle := lipgloss.NewStyle().Background(lipgloss.Color("#0000FF"))

	// Create diff to trigger blending.
	before := "key: old"
	after := "key: value"

	beforeLines := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterLines := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	p := niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithGutter(niceyaml.NoGutter),
	)

	// Add overlapping ranges with different color configurations.
	p.AddStyleToRange(&noColorStyle, position.NewRange(
		position.New(0, 0),
		position.New(0, 3),
	))
	p.AddStyleToRange(&fgOnlyStyle, position.NewRange(
		position.New(0, 2),
		position.New(0, 6),
	))
	p.AddStyleToRange(&bgOnlyStyle, position.NewRange(
		position.New(0, 4),
		position.New(0, 8),
	))

	revBefore := niceyaml.NewRevision(beforeLines)
	revAfter := niceyaml.NewRevision(afterLines)

	diff := niceyaml.NewFullDiff(revBefore, revAfter)

	got := p.Print(diff.Lines())

	// Should render without panic.
	require.NotEmpty(t, got)

	// Also test with regular source (non-diff path).
	p2 := niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithGutter(niceyaml.NoGutter),
	)

	p2.AddStyleToRange(&noColorStyle, position.NewRange(
		position.New(0, 0),
		position.New(0, 3),
	))
	p2.AddStyleToRange(&fgOnlyStyle, position.NewRange(
		position.New(0, 2),
		position.New(0, 6),
	))

	got2 := p2.Print(niceyaml.NewSourceFromTokens(tks))
	require.NotEmpty(t, got2)
}

func TestPrinter_BlendStyles_BothTransformsNil(t *testing.T) {
	t.Parallel()

	// Test the case where neither style has a transform.
	input := "key: value"

	// Styles with colors but no transforms.
	style1 := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	style2 := lipgloss.NewStyle().Background(lipgloss.Color("#0000FF"))

	// Create diff to trigger blending.
	before := "key: old"
	after := "key: value"

	beforeLines := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterLines := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	p := niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithGutter(niceyaml.NoGutter),
	)

	// Add overlapping ranges.
	p.AddStyleToRange(&style1, position.NewRange(
		position.New(0, 0),
		position.New(0, 5),
	))
	p.AddStyleToRange(&style2, position.NewRange(
		position.New(0, 2),
		position.New(0, 7),
	))

	revBefore := niceyaml.NewRevision(beforeLines)
	revAfter := niceyaml.NewRevision(afterLines)

	diff := niceyaml.NewFullDiff(revBefore, revAfter)

	got := p.Print(diff.Lines())

	// Should render content.
	require.NotEmpty(t, got)
	assert.Contains(t, got, input)
}

func TestPrinter_BlendStyles_OnlyOverlayTransform(t *testing.T) {
	t.Parallel()

	// Test the case where only the overlay style has a transform.

	// Base style with no transform.
	style1 := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	// Overlay style with transform.
	transform := func(s string) string { return "<<" + s + ">>" }
	style2 := lipgloss.NewStyle().Transform(transform)

	// Create diff to trigger blending.
	before := "key: old"
	after := "key: value"

	beforeLines := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterLines := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	p := niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithGutter(niceyaml.NoGutter),
	)

	// Add overlapping ranges.
	p.AddStyleToRange(&style1, position.NewRange(
		position.New(0, 0),
		position.New(0, 5),
	))
	p.AddStyleToRange(&style2, position.NewRange(
		position.New(0, 2),
		position.New(0, 7),
	))

	revBefore := niceyaml.NewRevision(beforeLines)
	revAfter := niceyaml.NewRevision(afterLines)

	diff := niceyaml.NewFullDiff(revBefore, revAfter)

	got := p.Print(diff.Lines())

	// Should render content with transformed portion.
	require.NotEmpty(t, got)
	// The overlapping region should be transformed.
	assert.Contains(t, got, "<<")
}
