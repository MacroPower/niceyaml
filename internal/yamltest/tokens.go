package yamltest

import (
	"errors"
	"fmt"
	"strings"

	"github.com/goccy/go-yaml/token"
)

// Sentinel errors for token validation.
var (
	// ErrNilToken indicates a token is nil.
	ErrNilToken = errors.New("token is nil")
	// ErrNilPosition indicates a token's position is nil.
	ErrNilPosition = errors.New("token position is nil")
)

// TokenValidationError indicates a token failed validation.
type TokenValidationError struct {
	Reason error  // [ErrNilToken] or [ErrNilPosition].
	Which  string // "want" or "got".
	Index  int    // -1 for single token validation.
}

// Error implements the [error] interface.
func (e *TokenValidationError) Error() string {
	if e.Index < 0 {
		return fmt.Sprintf("%s: %v", e.Which, e.Reason)
	}

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

// Equal returns true if the tokens are equal.
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
	Diffs         []TokenDiff // Per-token [TokenDiff] values (only populated if counts match).
	WantCount     int         // Length of want slice.
	GotCount      int         // Length of got slice.
	CountMismatch bool        // True if slice lengths differ.
}

// Equal returns true if all tokens are equal.
func (d TokensDiff) Equal() bool {
	if d.CountMismatch {
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

	if d.CountMismatch {
		return fmt.Sprintf("token count mismatch: want %d, got %d", d.WantCount, d.GotCount)
	}

	var sb strings.Builder

	for i, diff := range d.Diffs {
		if !diff.Equal() {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}

			sb.WriteString(fmt.Sprintf("token %d: %s", i, diff.String()))
		}
	}

	return sb.String()
}

// ContentDiff represents the difference between two content strings after
// normalization (line endings converted and leading/trailing newlines trimmed).
//
// Use [CompareContent] to create a ContentDiff.
type ContentDiff struct {
	Want string // Expected content after normalization.
	Got  string // Actual content after normalization.
}

// Equal returns true if the content is equal after normalization.
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

// ValidateTokenPair checks that both tokens and their positions are non-nil.
// Returns nil if both tokens are valid, or a [*TokenValidationError] if not.
func ValidateTokenPair(want, got *token.Token) error {
	if want == nil {
		return &TokenValidationError{Index: -1, Which: "want", Reason: ErrNilToken}
	}

	if want.Position == nil {
		return &TokenValidationError{Index: -1, Which: "want", Reason: ErrNilPosition}
	}

	if got == nil {
		return &TokenValidationError{Index: -1, Which: "got", Reason: ErrNilToken}
	}

	if got.Position == nil {
		return &TokenValidationError{Index: -1, Which: "got", Reason: ErrNilPosition}
	}

	return nil
}

// ValidateTokens checks that all tokens and their positions are non-nil.
// Returns nil if all tokens are valid, or the first [*TokenValidationError]
// found. If the slice lengths differ, returns a descriptive error (not a
// [*TokenValidationError]).
func ValidateTokens(want, got token.Tokens) error {
	if len(want) != len(got) {
		return fmt.Errorf("token count mismatch: want %d, got %d\nwant tokens:\n%s\ngot tokens:\n%s",
			len(want), len(got), FormatTokens(want), FormatTokens(got))
	}

	for i := range want {
		if want[i] == nil {
			return &TokenValidationError{Index: i, Which: "want", Reason: ErrNilToken}
		}

		if want[i].Position == nil {
			return &TokenValidationError{Index: i, Which: "want", Reason: ErrNilPosition}
		}

		if got[i] == nil {
			return &TokenValidationError{Index: i, Which: "got", Reason: ErrNilToken}
		}

		if got[i].Position == nil {
			return &TokenValidationError{Index: i, Which: "got", Reason: ErrNilPosition}
		}
	}

	return nil
}

// CompareTokens compares all fields of two tokens and returns a [TokenDiff].
// Assumes both tokens are valid (non-nil with non-nil positions); use
// [ValidateTokenPair] first to check validity.
func CompareTokens(want, got *token.Token) TokenDiff {
	return TokenDiff{
		Fields: DiffTokenFields(want, got),
		Want:   want,
		Got:    got,
	}
}

// CompareTokenSlices compares all fields of two token slices and returns a
// [TokensDiff]. Assumes all tokens are valid (non-nil with non-nil positions);
// use [ValidateTokens] first to check validity.
func CompareTokenSlices(want, got token.Tokens) TokensDiff {
	if len(want) != len(got) {
		return TokensDiff{
			CountMismatch: true,
			WantCount:     len(want),
			GotCount:      len(got),
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

// CompareContent compares two strings for equality and returns a [ContentDiff].
// Both strings are normalized by converting CRLF to LF and trimming
// leading/trailing newlines before comparison.
func CompareContent(want, got string) ContentDiff {
	return ContentDiff{
		Want: normalizeContent(want),
		Got:  normalizeContent(got),
	}
}

// DiffTokenFields returns a list of field names that differ between two tokens.
// Assumes both tokens are valid (non-nil with non-nil positions); use
// [ValidateTokenPair] first to check validity.
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

// TokenBuilder is a helper for constructing test tokens.
//
// Chain methods to set fields, then call [TokenBuilder.Build] to get the final
// token. The builder is mutable: each setter modifies the internal state and
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

// NewTokenBuilder creates a new [TokenBuilder] with default values.
// All position fields are initialized to zero values.
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

// DumpTokenOrigins concatenates all token Origin fields into a single string,
// reconstructing the original source text.
func DumpTokenOrigins(tks token.Tokens) string {
	var sb strings.Builder
	for _, tk := range tks {
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
