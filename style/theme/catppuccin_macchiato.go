package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// CatppuccinMacchiato returns [style.Styles] using catppuccin-macchiato colors.
func CatppuccinMacchiato() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#cad3f5")).
		Background(lipgloss.Color("#24273a"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#6e738d")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#cad3f5")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#ed8796")).Background(lipgloss.Color("#363a4f")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ed8796")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#a6da95")).Background(lipgloss.Color("#363a4f")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#a6da95")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#a6da95")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#a6da95")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#cad3f5")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#8aadf4")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#c6a0f6")),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#6e738d")).Bold(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#91d7e3")).Bold(true),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#24273a")).
				Background(lipgloss.Color("#c6a0f6")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#24273a"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#cad3f5"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#24273a"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#c6a0f6"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#c6a0f6")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#cad3f5"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#cad3f5")),
		),
		style.Set(
			style.GenericHighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#24273a"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#24273a"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#24273a")).
				Background(lipgloss.Color("#a6da95")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#24273a")).
				Background(lipgloss.Color("#eed49f")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#24273a")).
				Background(lipgloss.Color("#ed8796")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#a6da95")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#eed49f")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#ed8796")),
		),
	)
}
