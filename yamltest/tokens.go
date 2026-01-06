package yamltest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TokenBuilder is a helper for constructing test tokens.
// Chain methods to set fields, and call Build() to get the final token.
// The builder is mutable: each setter modifies the internal state and returns
// the same builder. Build() returns a clone of the current state, so you can
// call Build() multiple times at different points in the chain to get
// independent tokens.
// Create instances with [NewTokenBuilder].
type TokenBuilder struct {
	token *token.Token
}

// NewTokenBuilder creates a new [TokenBuilder] with default values.
func NewTokenBuilder() *TokenBuilder {
	return &TokenBuilder{
		token: &token.Token{
			Position: &token.Position{},
		},
	}
}

// Clone returns a new [TokenBuilder] with a cloned copy of the current token.
// Use this to create independent builder branches from a common base.
func (b *TokenBuilder) Clone() *TokenBuilder {
	return &TokenBuilder{
		token: b.token.Clone(),
	}
}

// Type sets the token type.
func (b *TokenBuilder) Type(t token.Type) *TokenBuilder {
	b.token.Type = t

	return b
}

// CharacterType sets the token character type.
func (b *TokenBuilder) CharacterType(ct token.CharacterType) *TokenBuilder {
	b.token.CharacterType = ct

	return b
}

// Indicator sets the token indicator.
func (b *TokenBuilder) Indicator(i token.Indicator) *TokenBuilder {
	b.token.Indicator = i

	return b
}

// Value sets the token value.
func (b *TokenBuilder) Value(v string) *TokenBuilder {
	b.token.Value = v

	return b
}

// Origin sets the token origin.
func (b *TokenBuilder) Origin(o string) *TokenBuilder {
	b.token.Origin = o

	return b
}

// Error sets the token error.
func (b *TokenBuilder) Error(e string) *TokenBuilder {
	b.token.Error = e

	return b
}

// Position sets the token position.
func (b *TokenBuilder) Position(p token.Position) *TokenBuilder {
	b.token.Position = &p

	return b
}

// PositionLine sets the token position line.
func (b *TokenBuilder) PositionLine(line int) *TokenBuilder {
	b.token.Position.Line = line

	return b
}

// PositionColumn sets the token position column.
func (b *TokenBuilder) PositionColumn(col int) *TokenBuilder {
	b.token.Position.Column = col

	return b
}

// PositionOffset sets the token position offset.
func (b *TokenBuilder) PositionOffset(offset int) *TokenBuilder {
	b.token.Position.Offset = offset

	return b
}

// PositionIndentNum sets the token position indent number.
func (b *TokenBuilder) PositionIndentNum(indentNum int) *TokenBuilder {
	b.token.Position.IndentNum = indentNum

	return b
}

// PositionIndentLevel sets the token position indent level.
func (b *TokenBuilder) PositionIndentLevel(indentLevel int) *TokenBuilder {
	b.token.Position.IndentLevel = indentLevel

	return b
}

// Build returns a clone of the built token.
func (b *TokenBuilder) Build() *token.Token {
	return b.token.Clone()
}

// DumpTokenOrigins concatenates all token Origins into a single string.
func DumpTokenOrigins(tks token.Tokens) string {
	var sb strings.Builder
	for _, tk := range tks {
		sb.WriteString(tk.Origin)
	}

	return sb.String()
}

// RequireTokensValid checks that all tokens and their positions are non-nil.
func RequireTokensValid(t *testing.T, want, got token.Tokens) {
	t.Helper()

	if len(want) != len(got) {
		require.Fail(t, fmt.Sprintf("token count mismatch: want %d, got %d", len(want), len(got)),
			"want tokens:\n%s\ngot tokens:\n%s", FormatTokens(want), FormatTokens(got))
	}

	for i := range want {
		RequireTokenValid(t, want[i], got[i], fmt.Sprintf("token %d", i))
	}
}

// AssertTokensEqual compares all fields of two token slices.
// Use [RequireTokensValid] first to ensure that tokens are valid.
func AssertTokensEqual(t *testing.T, want, got token.Tokens) {
	t.Helper()

	for i := range want {
		AssertTokenEqual(t, want[i], got[i], fmt.Sprintf("token %d", i))
	}
}

// RequireTokenValid checks that both tokens and their positions are non-nil.
func RequireTokenValid(t *testing.T, want, got *token.Token, prefix string) {
	t.Helper()

	require.NotNil(t, want, prefix+" want cannot be nil")
	require.NotNil(t, want.Position, prefix+" want.Position cannot be nil")

	require.NotNil(t, got, prefix+" got cannot be nil")
	require.NotNil(t, got.Position, prefix+" got.Position cannot be nil")
}

// AssertTokenEqual compares all fields of two tokens.
// Use [RequireTokenValid] first to ensure that tokens are valid.
func AssertTokenEqual(t *testing.T, want, got *token.Token, prefix string) {
	t.Helper()

	if diffs := diffTokenFields(want, got); len(diffs) > 0 {
		assert.Fail(t, prefix+" mismatch",
			"want:\t%s\ngot:\t%s\ndifferences: %s",
			FormatToken(want), FormatToken(got), strings.Join(diffs, ", "))
	}
}

// diffTokenFields returns a list of field names that differ between two tokens.
func diffTokenFields(want, got *token.Token) []string {
	var diffs []string

	if want.Type != got.Type {
		diffs = append(diffs, "Type")
	}
	if want.Value != got.Value {
		diffs = append(diffs, "Value")
	}
	if want.Origin != got.Origin {
		diffs = append(diffs, "Origin")
	}
	if want.CharacterType != got.CharacterType {
		diffs = append(diffs, "CharacterType")
	}
	if want.Indicator != got.Indicator {
		diffs = append(diffs, "Indicator")
	}
	if want.Position.Column != got.Position.Column {
		diffs = append(diffs, "Position.Column")
	}
	if want.Position.Line != got.Position.Line {
		diffs = append(diffs, "Position.Line")
	}
	if want.Position.Offset != got.Position.Offset {
		diffs = append(diffs, "Position.Offset")
	}
	if want.Position.IndentNum != got.Position.IndentNum {
		diffs = append(diffs, "Position.IndentNum")
	}
	if want.Position.IndentLevel != got.Position.IndentLevel {
		diffs = append(diffs, "Position.IndentLevel")
	}

	return diffs
}

// AssertContentEqual compares two strings for equality, normalizing line endings
// and trimming leading/trailing newlines.
func AssertContentEqual(t *testing.T, want, got string) {
	t.Helper()
	assert.Equal(t, normalizeContent(want), normalizeContent(got), "content mismatch")
}

// FormatTokenPosition formats a [*token.Position] for debug output.
func FormatTokenPosition(pos *token.Position) string {
	if pos == nil {
		return "<nil>"
	}

	return fmt.Sprintf("Line=%d Col=%d Offset=%d IndentNum=%d IndentLevel=%d",
		pos.Line, pos.Column, pos.Offset, pos.IndentNum, pos.IndentLevel)
}

// FormatToken formats a [*token.Token] for debug output.
func FormatToken(tk *token.Token) string {
	if tk == nil {
		return "<nil>"
	}

	return fmt.Sprintf(`Type=%s Value=%q Origin=%q Indicator=%s CharacterType=%s Position=(%s)`,
		tk.Type,
		tk.Value,
		tk.Origin,
		tk.Indicator,
		tk.CharacterType,
		FormatTokenPosition(tk.Position),
	)
}

// FormatTokens formats [token.Tokens] for debug output.
func FormatTokens(tks token.Tokens) string {
	if len(tks) == 0 {
		return "<empty>"
	}

	var sb strings.Builder
	for i, tk := range tks {
		if i > 0 {
			sb.WriteString("\n")
		}

		sb.WriteString(fmt.Sprintf("[%d]\n%s", i, FormatToken(tk)))
	}

	return sb.String()
}

func normalizeContent(s string) string {
	return strings.Trim(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}
