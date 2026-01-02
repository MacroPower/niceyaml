package tokens_test

import (
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml/tokens"
)

func TestNewSegment(t *testing.T) {
	t.Parallel()

	source := &token.Token{Value: "source", Origin: "source\n", Type: token.StringType}
	part := &token.Token{Value: "part", Origin: "part", Type: token.StringType}

	seg := tokens.NewSegment(source, part)

	// Part() returns a clone, so check deep equality.
	gotPart := seg.Part()
	assert.NotSame(t, part, gotPart) // Should be cloned.
	assertTokenEqual(t, part, gotPart)

	gotSource := seg.Source()
	assert.NotSame(t, source, gotSource) // Should be cloned.
	assertTokenEqual(t, source, gotSource)
}

// assertTokenEqual checks that two tokens have equal field values.
func assertTokenEqual(t *testing.T, want, got *token.Token) {
	t.Helper()
	assert.Equal(t, want.Type, got.Type)
	assert.Equal(t, want.CharacterType, got.CharacterType)
	assert.Equal(t, want.Indicator, got.Indicator)
	assert.Equal(t, want.Value, got.Value)
	assert.Equal(t, want.Origin, got.Origin)
	assertPositionEqual(t, want.Position, got.Position)
}

// assertPositionEqual checks that two token positions have equal field values.
func assertPositionEqual(t *testing.T, want, got *token.Position) {
	t.Helper()
	if want == nil && got == nil {
		return
	}
	if want == nil || got == nil {
		assert.Equal(t, want, got)
		return
	}

	assert.Equal(t, want.Line, got.Line)
	assert.Equal(t, want.Column, got.Column)
	assert.Equal(t, want.Offset, got.Offset)
	assert.Equal(t, want.IndentNum, got.IndentNum)
	assert.Equal(t, want.IndentLevel, got.IndentLevel)
}

func TestSegment_Source(t *testing.T) {
	t.Parallel()

	t.Run("returns cloned source", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "test", Origin: "test\n"}
		part := &token.Token{Value: "test"}

		seg := tokens.NewSegment(source, part)
		got := seg.Source()

		assert.Equal(t, source.Value, got.Value)
		assert.Equal(t, source.Origin, got.Origin)
		assert.NotSame(t, source, got) // Should be cloned.
	})
}

func TestSegment_SourceEquals(t *testing.T) {
	t.Parallel()

	t.Run("matches source", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "source"}
		part := &token.Token{Value: "part"}
		other := &token.Token{Value: "other"}

		seg := tokens.NewSegment(source, part)

		assert.True(t, seg.SourceEquals(source))
		assert.False(t, seg.SourceEquals(other))
	})

	t.Run("does not match part", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "source"}
		part := &token.Token{Value: "part"}

		seg := tokens.NewSegment(source, part)

		assert.False(t, seg.SourceEquals(part))
	})
}

func TestSegment_Width(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  int
	}{
		"simple ascii": {
			input: "hello",
			want:  5,
		},
		"with trailing newline": {
			input: "hello\n",
			want:  5,
		},
		"unicode characters": {
			input: "日本語",
			want:  3,
		},
		"unicode with newline": {
			input: "日本語\n",
			want:  3,
		},
		"emoji": {
			input: "🎉🎊",
			want:  2,
		},
		"empty string": {
			input: "",
			want:  0,
		},
		"only newline": {
			input: "\n",
			want:  0,
		},
		"mixed content": {
			input: "key: 日本語\n",
			want:  8,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			seg := tokens.NewSegment(nil, &token.Token{Origin: tc.input})

			got := seg.Width()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSegment_Contains(t *testing.T) {
	t.Parallel()

	t.Run("matches source", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "source"}
		part := &token.Token{Value: "part"}
		other := &token.Token{Value: "other"}

		seg := tokens.NewSegment(source, part)

		assert.True(t, seg.Contains(source))
		assert.False(t, seg.Contains(other))
	})

	t.Run("matches part", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "source"}
		part := &token.Token{Value: "part"}
		other := &token.Token{Value: "other"}

		seg := tokens.NewSegment(source, part)

		assert.True(t, seg.Contains(part))
		assert.False(t, seg.Contains(other))
	})

	t.Run("nil token", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "source"}
		part := &token.Token{Value: "part"}

		seg := tokens.NewSegment(source, part)

		assert.False(t, seg.Contains(nil))
	})
}

func TestSegments_Append(t *testing.T) {
	t.Parallel()

	t.Run("append to empty", func(t *testing.T) {
		t.Parallel()

		var segs tokens.Segments

		source := &token.Token{Value: "source", Origin: "source\n", Type: token.StringType}
		part := &token.Token{Value: "part", Origin: "part", Type: token.StringType}

		got := segs.Append(source, part)

		require.Len(t, got, 1)
		// Part() returns a clone, so check deep equality.
		assertTokenEqual(t, part, got[0].Part())
	})

	t.Run("append to existing", func(t *testing.T) {
		t.Parallel()

		source1 := &token.Token{Value: "source1", Origin: "source1\n", Type: token.StringType}
		part1 := &token.Token{Value: "part1", Origin: "part1", Type: token.StringType}
		source2 := &token.Token{Value: "source2", Origin: "source2\n", Type: token.StringType}
		part2 := &token.Token{Value: "part2", Origin: "part2", Type: token.StringType}

		segs := tokens.Segments{tokens.NewSegment(source1, part1)}
		got := segs.Append(source2, part2)

		require.Len(t, got, 2)
		// Part() returns a clone, so check deep equality.
		assertTokenEqual(t, part1, got[0].Part())
		assertTokenEqual(t, part2, got[1].Part())
	})
}

func TestSegments_Clone(t *testing.T) {
	t.Parallel()

	t.Run("empty segments", func(t *testing.T) {
		t.Parallel()

		var segs tokens.Segments

		got := segs.Clone()

		assert.Nil(t, got)
	})

	t.Run("clones segments", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "source"}
		part := &token.Token{Value: "part", Origin: "part\n"}

		segs := tokens.Segments{tokens.NewSegment(source, part)}
		got := segs.Clone()

		require.Len(t, got, 1)
		assert.Equal(t, part.Value, got[0].Part().Value)
		assert.NotSame(t, part, got[0].Part()) // Part should be cloned.
	})

	t.Run("preserves source reference", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "source"}
		part := &token.Token{Value: "part"}

		segs := tokens.Segments{tokens.NewSegment(source, part)}
		got := segs.Clone()

		assert.True(t, got[0].SourceEquals(source))
	})
}

func TestSegments_SourceTokens(t *testing.T) {
	t.Parallel()

	t.Run("empty segments", func(t *testing.T) {
		t.Parallel()

		var segs tokens.Segments

		got := segs.SourceTokens()

		assert.Nil(t, got)
	})

	t.Run("single segment", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "test", Origin: "test\n"}
		part := &token.Token{Value: "test", Origin: "test\n"}

		segs := tokens.Segments{tokens.NewSegment(source, part)}
		got := segs.SourceTokens()

		require.Len(t, got, 1)
		assert.Equal(t, source.Value, got[0].Value)
		assert.NotSame(t, source, got[0]) // Should be cloned.
	})

	t.Run("deduplicates shared source", func(t *testing.T) {
		t.Parallel()

		// Simulate a multiline token split across lines.
		source := &token.Token{Value: "multiline", Origin: "line1\nline2\n"}
		part1 := &token.Token{Value: "", Origin: "line1\n"}
		part2 := &token.Token{Value: "multiline", Origin: "line2\n"}

		segs := tokens.Segments{
			tokens.NewSegment(source, part1),
			tokens.NewSegment(source, part2),
		}
		got := segs.SourceTokens()

		require.Len(t, got, 1) // Should deduplicate.
		assert.Equal(t, source.Value, got[0].Value)
	})

	t.Run("preserves order with multiple sources", func(t *testing.T) {
		t.Parallel()

		source1 := &token.Token{Value: "first"}
		source2 := &token.Token{Value: "second"}
		source3 := &token.Token{Value: "third"}

		segs := tokens.Segments{
			tokens.NewSegment(source1, &token.Token{}),
			tokens.NewSegment(source2, &token.Token{}),
			tokens.NewSegment(source3, &token.Token{}),
		}
		got := segs.SourceTokens()

		require.Len(t, got, 3)
		assert.Equal(t, "first", got[0].Value)
		assert.Equal(t, "second", got[1].Value)
		assert.Equal(t, "third", got[2].Value)
	})
}

func TestSegments_PartTokens(t *testing.T) {
	t.Parallel()

	t.Run("empty segments", func(t *testing.T) {
		t.Parallel()

		var segs tokens.Segments

		got := segs.PartTokens()

		assert.Nil(t, got)
	})

	t.Run("returns all parts cloned", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "source"}
		part1 := &token.Token{Value: "part1", Origin: "line1\n"}
		part2 := &token.Token{Value: "part2", Origin: "line2\n"}

		segs := tokens.Segments{
			tokens.NewSegment(source, part1),
			tokens.NewSegment(source, part2),
		}
		got := segs.PartTokens()

		require.Len(t, got, 2)
		assert.Equal(t, "part1", got[0].Value)
		assert.Equal(t, "part2", got[1].Value)
		assert.NotSame(t, part1, got[0]) // Should be cloned.
		assert.NotSame(t, part2, got[1])
	})
}

func TestSegments_NextColumn(t *testing.T) {
	t.Parallel()

	t.Run("empty segments", func(t *testing.T) {
		t.Parallel()

		var segs tokens.Segments

		got := segs.NextColumn()

		assert.Equal(t, 0, got)
	})

	t.Run("single segment", func(t *testing.T) {
		t.Parallel()

		segs := tokens.Segments{
			tokens.NewSegment(nil, &token.Token{
				Position: &token.Position{Column: 5},
			}),
		}
		got := segs.NextColumn()

		assert.Equal(t, 5, got)
	})

	t.Run("multiple segments", func(t *testing.T) {
		t.Parallel()

		segs := tokens.Segments{
			tokens.NewSegment(nil, &token.Token{
				Position: &token.Position{Column: 1},
			}),
			tokens.NewSegment(nil, &token.Token{
				Position: &token.Position{Column: 5},
			}),
			tokens.NewSegment(nil, &token.Token{
				Position: &token.Position{Column: 10},
			}),
		}
		got := segs.NextColumn()

		assert.Equal(t, 10, got)
	})

	t.Run("nil position", func(t *testing.T) {
		t.Parallel()

		segs := tokens.Segments{
			tokens.NewSegment(nil, &token.Token{Position: nil}),
		}
		got := segs.NextColumn()

		assert.Equal(t, 0, got)
	})

	t.Run("nil part", func(t *testing.T) {
		t.Parallel()

		segs := tokens.Segments{
			tokens.NewSegment(nil, nil),
		}
		got := segs.NextColumn()

		assert.Equal(t, 0, got)
	})
}

func TestSegment_PartEquals(t *testing.T) {
	t.Parallel()

	t.Run("matches part", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "source"}
		part := &token.Token{Value: "part"}
		other := &token.Token{Value: "other"}

		seg := tokens.NewSegment(source, part)

		assert.True(t, seg.PartEquals(part))
		assert.False(t, seg.PartEquals(other))
	})

	t.Run("does not match source", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "source"}
		part := &token.Token{Value: "part"}

		seg := tokens.NewSegment(source, part)

		assert.False(t, seg.PartEquals(source))
	})
}

func TestSegment_Source_NilSource(t *testing.T) {
	t.Parallel()

	seg := tokens.NewSegment(nil, &token.Token{Value: "part"})
	got := seg.Source()

	assert.Nil(t, got)
}

func TestSegment_Part_NilPart(t *testing.T) {
	t.Parallel()

	seg := tokens.NewSegment(&token.Token{Value: "source"}, nil)
	got := seg.Part()

	assert.Nil(t, got)
}

func TestSegment_Width_NilPart(t *testing.T) {
	t.Parallel()

	seg := tokens.NewSegment(&token.Token{Value: "source"}, nil)
	got := seg.Width()

	assert.Equal(t, 0, got)
}

func TestSegments_SourceTokenAt(t *testing.T) {
	t.Parallel()

	t.Run("empty segments", func(t *testing.T) {
		t.Parallel()

		var segs tokens.Segments

		got := segs.SourceTokenAt(0)

		assert.Nil(t, got)
	})

	t.Run("finds token at column", func(t *testing.T) {
		t.Parallel()

		source1 := &token.Token{Value: "key"}
		source2 := &token.Token{Value: ":"}
		source3 := &token.Token{Value: "value"}

		segs := tokens.Segments{
			tokens.NewSegment(source1, &token.Token{Origin: "key"}),   // Width 3, cols 0-2.
			tokens.NewSegment(source2, &token.Token{Origin: ": "}),    // Width 2, cols 3-4.
			tokens.NewSegment(source3, &token.Token{Origin: "value"}), // Width 5, cols 5-9.
		}

		// SourceTokenAt returns clones, so check deep equality.
		assertTokenEqual(t, source1, segs.SourceTokenAt(0))
		assertTokenEqual(t, source1, segs.SourceTokenAt(2))
		assertTokenEqual(t, source2, segs.SourceTokenAt(3))
		assertTokenEqual(t, source2, segs.SourceTokenAt(4))
		assertTokenEqual(t, source3, segs.SourceTokenAt(5))
		assertTokenEqual(t, source3, segs.SourceTokenAt(9))

		// Verify cloning behavior.
		assert.NotSame(t, source1, segs.SourceTokenAt(0))
	})

	t.Run("column out of bounds", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "key"}

		segs := tokens.Segments{
			tokens.NewSegment(source, &token.Token{Origin: "key"}), // Width 3.
		}

		assert.Nil(t, segs.SourceTokenAt(3))
		assert.Nil(t, segs.SourceTokenAt(100))
	})

	t.Run("negative column", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "key"}

		segs := tokens.Segments{
			tokens.NewSegment(source, &token.Token{Origin: "key"}),
		}

		assert.Nil(t, segs.SourceTokenAt(-1))
	})

	t.Run("unicode width", func(t *testing.T) {
		t.Parallel()

		source1 := &token.Token{Value: "日本語"}
		source2 := &token.Token{Value: "end"}

		segs := tokens.Segments{
			tokens.NewSegment(source1, &token.Token{Origin: "日本語"}), // Width 3 (runes).
			tokens.NewSegment(source2, &token.Token{Origin: "end"}), // Width 3.
		}

		// SourceTokenAt returns clones, so check deep equality.
		assertTokenEqual(t, source1, segs.SourceTokenAt(0))
		assertTokenEqual(t, source1, segs.SourceTokenAt(2))
		assertTokenEqual(t, source2, segs.SourceTokenAt(3))
	})
}

func TestSegments_Merge(t *testing.T) {
	t.Parallel()

	t.Run("merge with empty", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "source"}
		part := &token.Token{Value: "part", Origin: "part"}

		segs := tokens.Segments{tokens.NewSegment(source, part)}

		var empty tokens.Segments

		got := segs.Merge(empty)

		require.Len(t, got, 1)
		assert.True(t, got[0].SourceEquals(source))
	})

	t.Run("merge empty with segments", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "source"}
		part := &token.Token{Value: "part", Origin: "part"}

		var segs tokens.Segments

		other := tokens.Segments{tokens.NewSegment(source, part)}

		got := segs.Merge(other)

		require.Len(t, got, 1)
		assert.True(t, got[0].SourceEquals(source))
	})

	t.Run("merge single segments", func(t *testing.T) {
		t.Parallel()

		source1 := &token.Token{Value: "source1"}
		part1 := &token.Token{Value: "part1", Origin: "part1"}
		source2 := &token.Token{Value: "source2"}
		part2 := &token.Token{Value: "part2", Origin: "part2"}

		segs1 := tokens.Segments{tokens.NewSegment(source1, part1)}
		segs2 := tokens.Segments{tokens.NewSegment(source2, part2)}

		got := segs1.Merge(segs2)

		require.Len(t, got, 2)
		assert.True(t, got[0].SourceEquals(source1))
		assert.True(t, got[1].SourceEquals(source2))
	})

	t.Run("merge multiple segments", func(t *testing.T) {
		t.Parallel()

		source1 := &token.Token{Value: "s1"}
		source2 := &token.Token{Value: "s2"}
		source3 := &token.Token{Value: "s3"}

		segs1 := tokens.Segments{tokens.NewSegment(source1, &token.Token{Origin: "p1"})}
		segs2 := tokens.Segments{tokens.NewSegment(source2, &token.Token{Origin: "p2"})}
		segs3 := tokens.Segments{tokens.NewSegment(source3, &token.Token{Origin: "p3"})}

		got := segs1.Merge(segs2, segs3)

		require.Len(t, got, 3)
		assert.True(t, got[0].SourceEquals(source1))
		assert.True(t, got[1].SourceEquals(source2))
		assert.True(t, got[2].SourceEquals(source3))
	})

	t.Run("preserves source pointer identity", func(t *testing.T) {
		t.Parallel()

		sharedSource := &token.Token{Value: "shared"}
		part1 := &token.Token{Value: "part1", Origin: "part1"}
		part2 := &token.Token{Value: "part2", Origin: "part2"}

		segs1 := tokens.Segments{tokens.NewSegment(sharedSource, part1)}
		segs2 := tokens.Segments{tokens.NewSegment(sharedSource, part2)}

		got := segs1.Merge(segs2)

		require.Len(t, got, 2)
		// Both segments should share the same source pointer.
		assert.True(t, got[0].SourceEquals(sharedSource))
		assert.True(t, got[1].SourceEquals(sharedSource))
	})
}

func TestSegments2_TokenRangesAt(t *testing.T) {
	t.Parallel()

	t.Run("negative idx", func(t *testing.T) {
		t.Parallel()

		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(&token.Token{}, &token.Token{Origin: "test"})},
		}

		got := s2.TokenRangesAt(-1, 0)

		assert.Nil(t, got)
	})

	t.Run("idx out of bounds", func(t *testing.T) {
		t.Parallel()

		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(&token.Token{}, &token.Token{Origin: "test"})},
		}

		got := s2.TokenRangesAt(5, 0)

		assert.Nil(t, got)
	})

	t.Run("no segment at column", func(t *testing.T) {
		t.Parallel()

		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(&token.Token{}, &token.Token{Origin: "abc"})}, // Width 3, cols 0-2.
		}

		got := s2.TokenRangesAt(0, 10) // Column 10 is out of bounds.

		assert.Nil(t, got)
	})

	t.Run("single line single segment", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "test"}
		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(source, &token.Token{Origin: "test"})}, // Width 4.
		}

		got := s2.TokenRangesAt(0, 0)

		require.NotNil(t, got)

		ranges := got.Values()
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Equal(t, 4, ranges[0].End.Col)
	})

	t.Run("single line multiple segments", func(t *testing.T) {
		t.Parallel()

		source1 := &token.Token{Value: "key"}
		source2 := &token.Token{Value: ":"}
		source3 := &token.Token{Value: "val"}

		s2 := tokens.Segments2{
			tokens.Segments{
				tokens.NewSegment(source1, &token.Token{Origin: "key"}), // Width 3, cols 0-2.
				tokens.NewSegment(source2, &token.Token{Origin: ": "}),  // Width 2, cols 3-4.
				tokens.NewSegment(source3, &token.Token{Origin: "val"}), // Width 3, cols 5-7.
			},
		}

		// Click on "key".
		got := s2.TokenRangesAt(0, 1)
		require.NotNil(t, got)

		ranges := got.Values()
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 3, ranges[0].End.Col)

		// Click on ":".
		got = s2.TokenRangesAt(0, 3)
		require.NotNil(t, got)

		ranges = got.Values()
		require.Len(t, ranges, 1)
		assert.Equal(t, 3, ranges[0].Start.Col)
		assert.Equal(t, 5, ranges[0].End.Col)

		// Click on "val".
		got = s2.TokenRangesAt(0, 6)
		require.NotNil(t, got)

		ranges = got.Values()
		require.Len(t, ranges, 1)
		assert.Equal(t, 5, ranges[0].Start.Col)
		assert.Equal(t, 8, ranges[0].End.Col)
	})

	t.Run("multiline token shared source", func(t *testing.T) {
		t.Parallel()

		// Simulate a multiline string spanning 3 lines.
		sharedSource := &token.Token{Value: "multiline"}

		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(sharedSource, &token.Token{Origin: "line1"})}, // Width 5.
			tokens.Segments{tokens.NewSegment(sharedSource, &token.Token{Origin: "line2"})}, // Width 5.
			tokens.Segments{tokens.NewSegment(sharedSource, &token.Token{Origin: "line3"})}, // Width 5.
		}

		// Click on line 1 should highlight all 3 lines.
		got := s2.TokenRangesAt(0, 2)

		require.NotNil(t, got)

		ranges := got.Values()
		require.Len(t, ranges, 3)

		// Line 0.
		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Equal(t, 5, ranges[0].End.Col)

		// Line 1.
		assert.Equal(t, 1, ranges[1].Start.Line)
		assert.Equal(t, 0, ranges[1].Start.Col)
		assert.Equal(t, 1, ranges[1].End.Line)
		assert.Equal(t, 5, ranges[1].End.Col)

		// Line 2.
		assert.Equal(t, 2, ranges[2].Start.Line)
		assert.Equal(t, 0, ranges[2].Start.Col)
		assert.Equal(t, 2, ranges[2].End.Line)
		assert.Equal(t, 5, ranges[2].End.Col)
	})

	t.Run("zero width segments skipped", func(t *testing.T) {
		t.Parallel()

		source := &token.Token{Value: "test"}
		s2 := tokens.Segments2{
			tokens.Segments{
				tokens.NewSegment(source, &token.Token{Origin: ""}),    // Width 0.
				tokens.NewSegment(source, &token.Token{Origin: "abc"}), // Width 3.
			},
		}

		got := s2.TokenRangesAt(0, 0)

		require.NotNil(t, got)

		ranges := got.Values()
		// Only the non-zero-width segment should be included.
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 3, ranges[0].End.Col)
	})

	t.Run("empty segments2", func(t *testing.T) {
		t.Parallel()

		var s2 tokens.Segments2

		got := s2.TokenRangesAt(0, 0)

		assert.Nil(t, got)
	})
}
