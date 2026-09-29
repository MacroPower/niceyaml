package yamltest

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/require"
)

// Names for the two sides of a token comparison.
const (
	whichWant = "want"
	whichGot  = "got"
)

// Sentinel errors for token validation.
var (
	// ErrNilToken indicates a token is nil.
	ErrNilToken = errors.New("token is nil")
	// ErrNilPosition indicates a token's position is nil.
	ErrNilPosition = errors.New("token position is nil")
	// ErrTokenCountMismatch indicates two token slices differ in length.
	ErrTokenCountMismatch = errors.New("token count mismatch")
)

// TokenValidationError indicates a token failed validation.
type TokenValidationError struct {
	Reason error  // [ErrNilToken] or [ErrNilPosition].
	Which  string // "want" or "got".
	Index  int    // Index of the offending token.
}

// Error implements the [error] interface.
func (e *TokenValidationError) Error() string {
	return fmt.Sprintf("token %d %s: %v", e.Index, e.Which, e.Reason)
}

// Unwrap returns the underlying Reason field.
func (e *TokenValidationError) Unwrap() error {
	return e.Reason
}

// TokenDiff represents the differences between two tokens.
//
// Use [CompareTokens] to create a TokenDiff.
type TokenDiff struct {
	Want   *token.Token // Expected token.
	Got    *token.Token // Actual token.
	Fields []string     // Field names that differ (see [DiffTokenFields]).
}

// Equal reports whether the tokens are equal.
func (d TokenDiff) Equal() bool {
	return len(d.Fields) == 0
}

// String returns a human-readable representation of the diff using
// [FormatToken] to display token details.
func (d TokenDiff) String() string {
	if d.Equal() {
		return "tokens equal"
	}

	return fmt.Sprintf("token mismatch:\n  want: %s\n  got:  %s\n  differences: %s",
		FormatToken(d.Want), FormatToken(d.Got), strings.Join(d.Fields, ", "))
}

// TokensDiff represents the differences between two token slices.
//
// Use [CompareTokenSlices] to create a TokensDiff.
type TokensDiff struct {
	Diffs     []TokenDiff // Per-token [TokenDiff] values, empty when the counts differ.
	WantCount int         // Length of want slice.
	GotCount  int         // Length of got slice.
}

// CountMismatch reports whether the want and got slices differ in length.
func (d TokensDiff) CountMismatch() bool {
	return d.WantCount != d.GotCount
}

// Equal reports whether all tokens are equal.
func (d TokensDiff) Equal() bool {
	if d.CountMismatch() {
		return false
	}

	for _, diff := range d.Diffs {
		if !diff.Equal() {
			return false
		}
	}

	return true
}

// String returns a human-readable representation of the diff.
func (d TokensDiff) String() string {
	if d.Equal() {
		return "token slices equal"
	}

	if d.CountMismatch() {
		return fmt.Sprintf("token count mismatch: want %d, got %d", d.WantCount, d.GotCount)
	}

	var sb strings.Builder

	for i, diff := range d.Diffs {
		if !diff.Equal() {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}

			fmt.Fprintf(&sb, "token %d: %s", i, diff.String())
		}
	}

	return sb.String()
}

// ContentDiff represents the difference between two content strings after
// normalization, which converts CRLF and bare CR line endings to LF and
// trims leading/trailing newlines.
//
// Use [CompareContent] to create a ContentDiff.
type ContentDiff struct {
	Want string // Expected content after normalization.
	Got  string // Actual content after normalization.
}

// Equal reports whether the content is equal after normalization.
func (d ContentDiff) Equal() bool {
	return d.Want == d.Got
}

// String returns a human-readable representation of the diff.
func (d ContentDiff) String() string {
	if d.Equal() {
		return "content equal"
	}

	return fmt.Sprintf("content mismatch:\n  want: %q\n  got:  %q", d.Want, d.Got)
}

// ValidateTokens checks that all tokens and their positions are non-nil.
// It returns the first [*TokenValidationError] it finds, or nil when every
// token is valid. When the slice lengths differ, it instead returns an
// error that wraps [ErrTokenCountMismatch], reports both counts, and lists
// both slices.
func ValidateTokens(want, got token.Tokens) error {
	if len(want) != len(got) {
		return fmt.Errorf("%w: want %d, got %d\nwant tokens:\n%s\ngot tokens:\n%s",
			ErrTokenCountMismatch, len(want), len(got), FormatTokens(want), FormatTokens(got))
	}

	for i := range want {
		if want[i] == nil {
			return &TokenValidationError{Index: i, Which: whichWant, Reason: ErrNilToken}
		}

		if want[i].Position == nil {
			return &TokenValidationError{Index: i, Which: whichWant, Reason: ErrNilPosition}
		}

		if got[i] == nil {
			return &TokenValidationError{Index: i, Which: whichGot, Reason: ErrNilToken}
		}

		if got[i].Position == nil {
			return &TokenValidationError{Index: i, Which: whichGot, Reason: ErrNilPosition}
		}
	}

	return nil
}

// CompareTokens compares two tokens with [DiffTokenFields] and returns a
// [TokenDiff]. It assumes both tokens are non-nil and carry positions.
// [ValidateTokens] checks this for token slices.
func CompareTokens(want, got *token.Token) TokenDiff {
	return TokenDiff{
		Fields: DiffTokenFields(want, got),
		Want:   want,
		Got:    got,
	}
}

// CompareTokenSlices compares two token slices index by index with
// [CompareTokens] and returns a [TokensDiff]. It assumes every token is
// non-nil and carries a position, so call [ValidateTokens] first.
func CompareTokenSlices(want, got token.Tokens) TokensDiff {
	if len(want) != len(got) {
		return TokensDiff{
			WantCount: len(want),
			GotCount:  len(got),
		}
	}

	diffs := make([]TokenDiff, len(want))
	for i := range want {
		diffs[i] = CompareTokens(want[i], got[i])
	}

	return TokensDiff{
		WantCount: len(want),
		GotCount:  len(got),
		Diffs:     diffs,
	}
}

// RequireTokensEqual fails the test unless want and got hold the same tokens.
// It checks both slices with [ValidateTokens] and then compares them with
// [CompareTokenSlices], so the failure message names the token count
// mismatch, the nil token or position, or the fields that differ.
func RequireTokensEqual(tb testing.TB, want, got token.Tokens) {
	tb.Helper()

	require.NoError(tb, ValidateTokens(want, got))

	diff := CompareTokenSlices(want, got)
	require.True(tb, diff.Equal(), diff.String())
}

// CompareContent compares two strings for equality and returns a [ContentDiff].
// It converts CRLF and bare CR line endings to LF and trims leading/trailing
// newlines in both strings before comparing.
func CompareContent(want, got string) ContentDiff {
	return ContentDiff{
		Want: normalizeContent(want),
		Got:  normalizeContent(got),
	}
}

// DiffTokenFields returns the names of the fields that differ between two
// tokens. It compares Type, Value, Origin, CharacterType, Indicator, Error,
// and each Position field. It skips the Next and Prev links, since tokens
// built apart never point at the same neighbors. It assumes both tokens
// are non-nil and carry positions. [ValidateTokens] checks this for token
// slices.
func DiffTokenFields(want, got *token.Token) []string {
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

	if want.Error != got.Error {
		diffs = append(diffs, "Error")
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

// TokenBuilder builds test tokens.
//
// Chain methods to set fields, then call [TokenBuilder.Build] to get the final
// token. The builder is mutable. Each setter modifies the internal state and
// returns the same builder for chaining.
//
// [TokenBuilder.Build] returns a clone, so you can call it multiple times at
// different points in the chain to produce independent tokens. Use
// [TokenBuilder.Clone] to branch from a common base configuration.
//
// Create instances with [NewTokenBuilder].
type TokenBuilder struct {
	token *token.Token
}

// NewTokenBuilder creates a new [*TokenBuilder] with default values.
// All position fields start at zero.
func NewTokenBuilder() *TokenBuilder {
	return &TokenBuilder{
		token: &token.Token{
			Position: &token.Position{},
		},
	}
}

// Clone returns a new [*TokenBuilder] with a copy of the current token state.
// Use this to branch from a common base configuration.
func (b *TokenBuilder) Clone() *TokenBuilder {
	return &TokenBuilder{
		token: b.token.Clone(),
	}
}

// Type sets the [token.Type].
func (b *TokenBuilder) Type(t token.Type) *TokenBuilder {
	b.token.Type = t

	return b
}

// CharacterType sets the [token.CharacterType].
func (b *TokenBuilder) CharacterType(ct token.CharacterType) *TokenBuilder {
	b.token.CharacterType = ct

	return b
}

// Indicator sets the [token.Indicator].
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

// Position sets the full [token.Position].
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

// Build returns a clone of the current token state.
// You can call Build multiple times to produce independent tokens.
func (b *TokenBuilder) Build() *token.Token {
	return b.token.Clone()
}

// DumpTokenOrigins joins the Origin fields of tks into one string.
//
// For a stream from [go.jacobcolvin.com/niceyaml/tokens.Tokenize], the
// result matches the source except where Tokenize leaves the lexer's text,
// as its doc describes. The result lacks the byte order marks that
// Tokenize drops and keeps any other mark, such as one inside a scalar.
// When a lone "!" ends a file that holds other text, the result also lacks
// the "!" and whitespace in front of it, which can include the line
// breaks. A file of a lone "!" comes back whole. The result repeats the
// line ending after a tag that ends its line and after text that follows
// a block scalar header, and a blank line between the two loses its
// spaces. The text around a tab used as indentation keeps the lexer's
// shape, which can drop a ":" indicator.
// A raw stream from [github.com/goccy/go-yaml/lexer.Tokenize] keeps every
// byte order mark, but it also loses the final line ending, trailing
// spaces, the spaces of whitespace-only lines, and the letter and hex
// digits of a "\x", "\u", or "\U" escape. It repeats the last rune of
// text that follows a block scalar header when that text ends the file.
//
// A nil token contributes nothing rather than a placeholder such as "<nil>",
// so the output holds only source text.
func DumpTokenOrigins(tks token.Tokens) string {
	var sb strings.Builder

	for _, tk := range tks {
		if tk == nil {
			continue
		}

		sb.WriteString(tk.Origin)
	}

	return sb.String()
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

	return fmt.Sprintf(`Type=%s Value=%q Origin=%q Indicator=%s CharacterType=%s Error=%q Position=(%s)`,
		tk.Type,
		tk.Value,
		tk.Origin,
		tk.Indicator,
		tk.CharacterType,
		tk.Error,
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

		fmt.Fprintf(&sb, "[%d]\n%s", i, FormatToken(tk))
	}

	return sb.String()
}

func normalizeContent(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	return strings.Trim(s, "\n")
}
