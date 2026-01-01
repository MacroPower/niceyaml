package line

import "github.com/goccy/go-yaml/token"

// SegmentedToken represents a token that has been segmented for line-by-line processing.
// For multiline tokens, multiple SegmentedTokens share the same Source pointer while each
// has a unique Part token containing the per-line content and position.
type SegmentedToken struct {
	// Source is a reference to the original token from the lexer.
	// Multiple SegmentedTokens may share the same Source pointer when a multiline
	// token is segmented across lines. Source should never be modified.
	Source *token.Token

	// For single-line tokens, Part contains content identical to Source.
	// Part is the per-line segment with adjusted Position for the current line.
	// For multiline tokens, Part contains the portion of content on this line.
	Part *token.Token
}

// SegmentedTokens is a collection of [SegmentedToken]s.
// The unique Source pointers represent the complete document token stream.
// For multiline tokens, multiple consecutive segments share the same Source pointer.
type SegmentedTokens []SegmentedToken

// SourceTokens returns clones of unique [SegmentedToken.Source] tokens in order.
// This is the inverse of segmentation: segments that share a Source pointer
// are deduplicated to return a clone of each original token once.
func (s SegmentedTokens) SourceTokens() token.Tokens {
	if len(s) == 0 {
		return nil
	}

	result := token.Tokens{}

	var lastSource *token.Token

	for _, seg := range s {
		if seg.Source != lastSource {
			result.Add(seg.Source.Clone())

			lastSource = seg.Source
		}
	}

	return result
}

// PartTokens returns a clone of all [SegmentedToken.Part] tokens in order.
func (s SegmentedTokens) PartTokens() token.Tokens {
	if len(s) == 0 {
		return nil
	}

	result := make(token.Tokens, 0, len(s))
	for _, seg := range s {
		result.Add(seg.Part.Clone())
	}

	return result
}
