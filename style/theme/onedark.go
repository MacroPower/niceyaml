package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Onedark returns [style.Styles] using onedark colors.
func Onedark() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ABB2BF")).
		Background(lipgloss.Color("#282C34"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#7F848E")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#E06C75")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#98C379")).Bold(true),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#D19A66")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#98C379")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#E06C75")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#61AFEF")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#E06C75")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#ABB2BF")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#E5C07B")),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282C34")).
				Background(lipgloss.Color("#E06C75")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#282C34"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#ABB2BF"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#282C34"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#E06C75"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#E06C75")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#ABB2BF"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#ABB2BF")),
		),
		style.Set(
			style.GenericHighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282C34"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282C34"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282C34")).
				Background(lipgloss.Color("#98C379")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282C34")).
				Background(lipgloss.Color("#e5c07b")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282C34")).
				Background(lipgloss.Color("#E06C75")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#98C379")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#e5c07b")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#E06C75")),
		),
	)
}
