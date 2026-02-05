package tokens_test

import (
	"iter"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func collectDocs(seq iter.Seq2[int, token.Tokens]) []token.Tokens {
	var result []token.Tokens

	for _, tks := range seq {
		result = append(result, tks)
	}

	return result
}

func TestNewSegment(t *testing.T) {
	t.Parallel()

	strTkb := yamltest.NewTokenBuilder().Type(token.StringType)
	source := strTkb.Clone().Value("source").Origin("source\n").Build()
	part := strTkb.Clone().Value("part").Origin("part").Build()

	seg := tokens.NewSegment(source, part)

	// Part() returns a clone, so check deep equality.
	gotPart := seg.Part()
	assert.NotSame(t, part, gotPart) // Should be cloned.
	require.NoError(t, yamltest.ValidateTokenPair(part, gotPart))

	partDiff := yamltest.CompareTokens(part, gotPart)
	require.True(t, partDiff.Equal(), partDiff.String())

	gotSource := seg.Source()
	assert.NotSame(t, source, gotSource) // Should be cloned.
	require.NoError(t, yamltest.ValidateTokenPair(source, gotSource))

	sourceDiff := yamltest.CompareTokens(source, gotSource)
	require.True(t, sourceDiff.Equal(), sourceDiff.String())
}

func TestSegment_Source(t *testing.T) {
	t.Parallel()

	t.Run("returns cloned source", func(t *testing.T) {
		t.Parallel()

		source := yamltest.NewTokenBuilder().Value("test").Origin("test\n").Build()
		part := yamltest.NewTokenBuilder().Value("test").Build()

		seg := tokens.NewSegment(source, part)
		got := seg.Source()

		assert.NotSame(t, source, got) // Should be cloned.
		require.NoError(t, yamltest.ValidateTokenPair(source, got))

		diff := yamltest.CompareTokens(source, got)
		require.True(t, diff.Equal(), diff.String())
	})
}

func TestSegment_SourceEquals(t *testing.T) {
	t.Parallel()

	t.Run("matches source", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("source").Build()
		part := tkb.Clone().Value("part").Build()
		other := tkb.Clone().Value("other").Build()

		seg := tokens.NewSegment(source, part)

		assert.True(t, seg.SourceEquals(source))
		assert.False(t, seg.SourceEquals(other))
	})

	t.Run("does not match part", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("source").Build()
		part := tkb.Clone().Value("part").Build()

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

			seg := tokens.NewSegment(nil, yamltest.NewTokenBuilder().Origin(tc.input).Build())

			got := seg.Width()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSegment_Contains(t *testing.T) {
	t.Parallel()

	t.Run("matches source", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("source").Build()
		part := tkb.Clone().Value("part").Build()
		other := tkb.Clone().Value("other").Build()

		seg := tokens.NewSegment(source, part)

		assert.True(t, seg.Contains(source))
		assert.False(t, seg.Contains(other))
	})

	t.Run("matches part", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("source").Build()
		part := tkb.Clone().Value("part").Build()
		other := tkb.Clone().Value("other").Build()

		seg := tokens.NewSegment(source, part)

		assert.True(t, seg.Contains(part))
		assert.False(t, seg.Contains(other))
	})

	t.Run("nil token", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("source").Build()
		part := tkb.Clone().Value("part").Build()

		seg := tokens.NewSegment(source, part)

		assert.False(t, seg.Contains(nil))
	})
}

func TestSegments_Append(t *testing.T) {
	t.Parallel()

	strTkb := yamltest.NewTokenBuilder().Type(token.StringType)

	t.Run("append to empty", func(t *testing.T) {
		t.Parallel()

		var segs tokens.Segments

		source := strTkb.Clone().Value("source").Origin("source\n").Build()
		part := strTkb.Clone().Value("part").Origin("part").Build()

		got := segs.Append(source, part)

		require.Len(t, got, 1)
		// Part() returns a clone, so check deep equality.
		require.NoError(t, yamltest.ValidateTokenPair(part, got[0].Part()))
		require.True(
			t,
			yamltest.CompareTokens(part, got[0].Part()).Equal(),
			yamltest.CompareTokens(part, got[0].Part()).String(),
		)
	})

	t.Run("append to existing", func(t *testing.T) {
		t.Parallel()

		source1 := strTkb.Clone().Value("source1").Origin("source1\n").Build()
		part1 := strTkb.Clone().Value("part1").Origin("part1").Build()
		source2 := strTkb.Clone().Value("source2").Origin("source2\n").Build()
		part2 := strTkb.Clone().Value("part2").Origin("part2").Build()

		segs := tokens.Segments{tokens.NewSegment(source1, part1)}
		got := segs.Append(source2, part2)

		require.Len(t, got, 2)
		// Part() returns a clone, so check deep equality.
		require.NoError(t, yamltest.ValidateTokenPair(part1, got[0].Part()))
		require.True(
			t,
			yamltest.CompareTokens(part1, got[0].Part()).Equal(),
			yamltest.CompareTokens(part1, got[0].Part()).String(),
		)
		require.NoError(t, yamltest.ValidateTokenPair(part2, got[1].Part()))
		require.True(
			t,
			yamltest.CompareTokens(part2, got[1].Part()).Equal(),
			yamltest.CompareTokens(part2, got[1].Part()).String(),
		)
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

		source := yamltest.NewTokenBuilder().Value("source").Build()
		part := yamltest.NewTokenBuilder().Value("part").Origin("part\n").Build()

		segs := tokens.Segments{tokens.NewSegment(source, part)}
		got := segs.Clone()

		require.Len(t, got, 1)
		assert.NotSame(t, part, got[0].Part()) // Part should be cloned.
		require.NoError(t, yamltest.ValidateTokenPair(part, got[0].Part()))
		require.True(
			t,
			yamltest.CompareTokens(part, got[0].Part()).Equal(),
			yamltest.CompareTokens(part, got[0].Part()).String(),
		)
	})

	t.Run("preserves source reference", func(t *testing.T) {
		t.Parallel()

		source := yamltest.NewTokenBuilder().Value("source").Build()
		part := yamltest.NewTokenBuilder().Value("part").Build()

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

		source := yamltest.NewTokenBuilder().Value("test").Origin("test\n").Build()
		part := yamltest.NewTokenBuilder().Value("test").Origin("test\n").Build()

		segs := tokens.Segments{tokens.NewSegment(source, part)}
		got := segs.SourceTokens()

		require.Len(t, got, 1)
		assert.NotSame(t, source, got[0]) // Should be cloned.
		require.NoError(t, yamltest.ValidateTokenPair(source, got[0]))

		diff := yamltest.CompareTokens(source, got[0])
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("deduplicates shared source", func(t *testing.T) {
		t.Parallel()

		// Simulate a multiline token split across lines.
		source := yamltest.NewTokenBuilder().Value("multiline").Origin("line1\nline2\n").Build()
		part1 := yamltest.NewTokenBuilder().Value("").Origin("line1\n").Build()
		part2 := yamltest.NewTokenBuilder().Value("multiline").Origin("line2\n").Build()

		segs := tokens.Segments{
			tokens.NewSegment(source, part1),
			tokens.NewSegment(source, part2),
		}
		got := segs.SourceTokens()

		require.Len(t, got, 1) // Should deduplicate.
		require.NoError(t, yamltest.ValidateTokenPair(source, got[0]))

		diff := yamltest.CompareTokens(source, got[0])
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("preserves order with multiple sources", func(t *testing.T) {
		t.Parallel()

		source1 := yamltest.NewTokenBuilder().Value("first").Build()
		source2 := yamltest.NewTokenBuilder().Value("second").Build()
		source3 := yamltest.NewTokenBuilder().Value("third").Build()

		segs := tokens.Segments{
			tokens.NewSegment(source1, yamltest.NewTokenBuilder().Build()),
			tokens.NewSegment(source2, yamltest.NewTokenBuilder().Build()),
			tokens.NewSegment(source3, yamltest.NewTokenBuilder().Build()),
		}
		got := segs.SourceTokens()

		require.Len(t, got, 3)
		require.NoError(t, yamltest.ValidateTokenPair(source1, got[0]))

		diff0 := yamltest.CompareTokens(source1, got[0])
		require.True(t, diff0.Equal(), diff0.String())

		require.NoError(t, yamltest.ValidateTokenPair(source2, got[1]))

		diff1 := yamltest.CompareTokens(source2, got[1])
		require.True(t, diff1.Equal(), diff1.String())

		require.NoError(t, yamltest.ValidateTokenPair(source3, got[2]))

		diff2 := yamltest.CompareTokens(source3, got[2])
		require.True(t, diff2.Equal(), diff2.String())
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

		source := yamltest.NewTokenBuilder().Value("source").Build()
		part1 := yamltest.NewTokenBuilder().Value("part1").Origin("line1\n").Build()
		part2 := yamltest.NewTokenBuilder().Value("part2").Origin("line2\n").Build()

		segs := tokens.Segments{
			tokens.NewSegment(source, part1),
			tokens.NewSegment(source, part2),
		}
		got := segs.PartTokens()

		require.Len(t, got, 2)
		assert.NotSame(t, part1, got[0]) // Should be cloned.
		assert.NotSame(t, part2, got[1])
		require.NoError(t, yamltest.ValidateTokenPair(part1, got[0]))

		diff0 := yamltest.CompareTokens(part1, got[0])
		require.True(t, diff0.Equal(), diff0.String())

		require.NoError(t, yamltest.ValidateTokenPair(part2, got[1]))

		diff1 := yamltest.CompareTokens(part2, got[1])
		require.True(t, diff1.Equal(), diff1.String())
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
			tokens.NewSegment(nil, yamltest.NewTokenBuilder().PositionColumn(5).Build()),
		}
		got := segs.NextColumn()

		assert.Equal(t, 5, got)
	})

	t.Run("multiple segments", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		segs := tokens.Segments{
			tokens.NewSegment(nil, tkb.Clone().PositionColumn(1).Build()),
			tokens.NewSegment(nil, tkb.Clone().PositionColumn(5).Build()),
			tokens.NewSegment(nil, tkb.Clone().PositionColumn(10).Build()),
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

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("source").Build()
		part := tkb.Clone().Value("part").Build()
		other := tkb.Clone().Value("other").Build()

		seg := tokens.NewSegment(source, part)

		assert.True(t, seg.PartEquals(part))
		assert.False(t, seg.PartEquals(other))
	})

	t.Run("does not match source", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("source").Build()
		part := tkb.Clone().Value("part").Build()

		seg := tokens.NewSegment(source, part)

		assert.False(t, seg.PartEquals(source))
	})
}

func TestSegment_Source_NilSource(t *testing.T) {
	t.Parallel()

	seg := tokens.NewSegment(nil, yamltest.NewTokenBuilder().Value("part").Build())
	got := seg.Source()

	assert.Nil(t, got)
}

func TestSegment_Part_NilPart(t *testing.T) {
	t.Parallel()

	seg := tokens.NewSegment(yamltest.NewTokenBuilder().Value("source").Build(), nil)
	got := seg.Part()

	assert.Nil(t, got)
}

func TestSegment_Width_NilPart(t *testing.T) {
	t.Parallel()

	seg := tokens.NewSegment(yamltest.NewTokenBuilder().Value("source").Build(), nil)
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

		source1 := yamltest.NewTokenBuilder().Value("key").Build()
		source2 := yamltest.NewTokenBuilder().Value(":").Build()
		source3 := yamltest.NewTokenBuilder().Value("value").Build()

		segs := tokens.Segments{
			tokens.NewSegment(source1, yamltest.NewTokenBuilder().Origin("key").Build()),   // Width 3, cols 0-2.
			tokens.NewSegment(source2, yamltest.NewTokenBuilder().Origin(": ").Build()),    // Width 2, cols 3-4.
			tokens.NewSegment(source3, yamltest.NewTokenBuilder().Origin("value").Build()), // Width 5, cols 5-9.
		}

		// SourceTokenAt returns clones, so check deep equality.
		require.NoError(t, yamltest.ValidateTokenPair(source1, segs.SourceTokenAt(0)))
		require.True(
			t,
			yamltest.CompareTokens(source1, segs.SourceTokenAt(0)).Equal(),
			yamltest.CompareTokens(source1, segs.SourceTokenAt(0)).String(),
		)
		require.NoError(t, yamltest.ValidateTokenPair(source1, segs.SourceTokenAt(2)))
		require.True(
			t,
			yamltest.CompareTokens(source1, segs.SourceTokenAt(2)).Equal(),
			yamltest.CompareTokens(source1, segs.SourceTokenAt(2)).String(),
		)
		require.NoError(t, yamltest.ValidateTokenPair(source2, segs.SourceTokenAt(3)))
		require.True(
			t,
			yamltest.CompareTokens(source2, segs.SourceTokenAt(3)).Equal(),
			yamltest.CompareTokens(source2, segs.SourceTokenAt(3)).String(),
		)
		require.NoError(t, yamltest.ValidateTokenPair(source2, segs.SourceTokenAt(4)))
		require.True(
			t,
			yamltest.CompareTokens(source2, segs.SourceTokenAt(4)).Equal(),
			yamltest.CompareTokens(source2, segs.SourceTokenAt(4)).String(),
		)
		require.NoError(t, yamltest.ValidateTokenPair(source3, segs.SourceTokenAt(5)))
		require.True(
			t,
			yamltest.CompareTokens(source3, segs.SourceTokenAt(5)).Equal(),
			yamltest.CompareTokens(source3, segs.SourceTokenAt(5)).String(),
		)
		require.NoError(t, yamltest.ValidateTokenPair(source3, segs.SourceTokenAt(9)))
		require.True(
			t,
			yamltest.CompareTokens(source3, segs.SourceTokenAt(9)).Equal(),
			yamltest.CompareTokens(source3, segs.SourceTokenAt(9)).String(),
		)

		// Verify cloning behavior.
		assert.NotSame(t, source1, segs.SourceTokenAt(0))
	})

	t.Run("column out of bounds", func(t *testing.T) {
		t.Parallel()

		source := yamltest.NewTokenBuilder().Value("key").Build()

		segs := tokens.Segments{
			tokens.NewSegment(source, yamltest.NewTokenBuilder().Origin("key").Build()), // Width 3.
		}

		assert.Nil(t, segs.SourceTokenAt(3))
		assert.Nil(t, segs.SourceTokenAt(100))
	})

	t.Run("negative column", func(t *testing.T) {
		t.Parallel()

		source := yamltest.NewTokenBuilder().Value("key").Build()

		segs := tokens.Segments{
			tokens.NewSegment(source, yamltest.NewTokenBuilder().Origin("key").Build()),
		}

		assert.Nil(t, segs.SourceTokenAt(-1))
	})

	t.Run("unicode width", func(t *testing.T) {
		t.Parallel()

		source1 := yamltest.NewTokenBuilder().Value("日本語").Build()
		source2 := yamltest.NewTokenBuilder().Value("end").Build()

		segs := tokens.Segments{
			tokens.NewSegment(source1, yamltest.NewTokenBuilder().Origin("日本語").Build()), // Width 3 (runes).
			tokens.NewSegment(source2, yamltest.NewTokenBuilder().Origin("end").Build()), // Width 3.
		}

		// SourceTokenAt returns clones, so check deep equality.
		require.NoError(t, yamltest.ValidateTokenPair(source1, segs.SourceTokenAt(0)))
		require.True(
			t,
			yamltest.CompareTokens(source1, segs.SourceTokenAt(0)).Equal(),
			yamltest.CompareTokens(source1, segs.SourceTokenAt(0)).String(),
		)
		require.NoError(t, yamltest.ValidateTokenPair(source1, segs.SourceTokenAt(2)))
		require.True(
			t,
			yamltest.CompareTokens(source1, segs.SourceTokenAt(2)).Equal(),
			yamltest.CompareTokens(source1, segs.SourceTokenAt(2)).String(),
		)
		require.NoError(t, yamltest.ValidateTokenPair(source2, segs.SourceTokenAt(3)))
		require.True(
			t,
			yamltest.CompareTokens(source2, segs.SourceTokenAt(3)).Equal(),
			yamltest.CompareTokens(source2, segs.SourceTokenAt(3)).String(),
		)
	})
}

func TestSegments_Merge(t *testing.T) {
	t.Parallel()

	t.Run("merge with empty", func(t *testing.T) {
		t.Parallel()

		source := yamltest.NewTokenBuilder().Value("source").Build()
		part := yamltest.NewTokenBuilder().Value("part").Origin("part").Build()

		segs := tokens.Segments{tokens.NewSegment(source, part)}

		var empty tokens.Segments

		got := segs.Merge(empty)

		require.Len(t, got, 1)
		assert.True(t, got[0].SourceEquals(source))
	})

	t.Run("merge empty with segments", func(t *testing.T) {
		t.Parallel()

		source := yamltest.NewTokenBuilder().Value("source").Build()
		part := yamltest.NewTokenBuilder().Value("part").Origin("part").Build()

		var segs tokens.Segments

		other := tokens.Segments{tokens.NewSegment(source, part)}

		got := segs.Merge(other)

		require.Len(t, got, 1)
		assert.True(t, got[0].SourceEquals(source))
	})

	t.Run("merge single segments", func(t *testing.T) {
		t.Parallel()

		source1 := yamltest.NewTokenBuilder().Value("source1").Build()
		part1 := yamltest.NewTokenBuilder().Value("part1").Origin("part1").Build()
		source2 := yamltest.NewTokenBuilder().Value("source2").Build()
		part2 := yamltest.NewTokenBuilder().Value("part2").Origin("part2").Build()

		segs1 := tokens.Segments{tokens.NewSegment(source1, part1)}
		segs2 := tokens.Segments{tokens.NewSegment(source2, part2)}

		got := segs1.Merge(segs2)

		require.Len(t, got, 2)
		assert.True(t, got[0].SourceEquals(source1))
		assert.True(t, got[1].SourceEquals(source2))
	})

	t.Run("merge multiple segments", func(t *testing.T) {
		t.Parallel()

		source1 := yamltest.NewTokenBuilder().Value("s1").Build()
		source2 := yamltest.NewTokenBuilder().Value("s2").Build()
		source3 := yamltest.NewTokenBuilder().Value("s3").Build()

		segs1 := tokens.Segments{tokens.NewSegment(source1, yamltest.NewTokenBuilder().Origin("p1").Build())}
		segs2 := tokens.Segments{tokens.NewSegment(source2, yamltest.NewTokenBuilder().Origin("p2").Build())}
		segs3 := tokens.Segments{tokens.NewSegment(source3, yamltest.NewTokenBuilder().Origin("p3").Build())}

		got := segs1.Merge(segs2, segs3)

		require.Len(t, got, 3)
		assert.True(t, got[0].SourceEquals(source1))
		assert.True(t, got[1].SourceEquals(source2))
		assert.True(t, got[2].SourceEquals(source3))
	})

	t.Run("preserves source pointer identity", func(t *testing.T) {
		t.Parallel()

		sharedSource := yamltest.NewTokenBuilder().Value("shared").Build()
		part1 := yamltest.NewTokenBuilder().Value("part1").Origin("part1").Build()
		part2 := yamltest.NewTokenBuilder().Value("part2").Origin("part2").Build()

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

		tkb := yamltest.NewTokenBuilder()
		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(tkb.Clone().Build(), tkb.Clone().Origin("test").Build())},
		}

		got := s2.TokenRangesAt(-1, 0)

		assert.Nil(t, got)
	})

	t.Run("idx out of bounds", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(tkb.Clone().Build(), tkb.Clone().Origin("test").Build())},
		}

		got := s2.TokenRangesAt(5, 0)

		assert.Nil(t, got)
	})

	t.Run("no segment at column", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		s2 := tokens.Segments2{
			tokens.Segments{
				tokens.NewSegment(tkb.Clone().Build(), tkb.Clone().Origin("abc").Build()),
			}, // Width 3, cols 0-2.
		}

		got := s2.TokenRangesAt(0, 10) // Column 10 is out of bounds.

		assert.Nil(t, got)
	})

	t.Run("single line single segment", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("test").Build()
		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(source, tkb.Clone().Origin("test").Build())}, // Width 4.
		}

		got := s2.TokenRangesAt(0, 0)
		require.Len(t, got, 1)
		assert.Equal(t, 0, got[0].Start.Line)
		assert.Equal(t, 0, got[0].Start.Col)
		assert.Equal(t, 0, got[0].End.Line)
		assert.Equal(t, 4, got[0].End.Col)
	})

	t.Run("single line multiple segments", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source1 := tkb.Clone().Value("key").Build()
		source2 := tkb.Clone().Value(":").Build()
		source3 := tkb.Clone().Value("val").Build()

		s2 := tokens.Segments2{
			tokens.Segments{
				tokens.NewSegment(source1, tkb.Clone().Origin("key").Build()), // Width 3, cols 0-2.
				tokens.NewSegment(source2, tkb.Clone().Origin(": ").Build()),  // Width 2, cols 3-4.
				tokens.NewSegment(source3, tkb.Clone().Origin("val").Build()), // Width 3, cols 5-7.
			},
		}

		// Click on "key".
		got := s2.TokenRangesAt(0, 1)
		require.Len(t, got, 1)
		assert.Equal(t, 0, got[0].Start.Col)
		assert.Equal(t, 3, got[0].End.Col)

		// Click on ":".
		got = s2.TokenRangesAt(0, 3)
		require.Len(t, got, 1)
		assert.Equal(t, 3, got[0].Start.Col)
		assert.Equal(t, 5, got[0].End.Col)

		// Click on "val".
		got = s2.TokenRangesAt(0, 6)
		require.Len(t, got, 1)
		assert.Equal(t, 5, got[0].Start.Col)
		assert.Equal(t, 8, got[0].End.Col)
	})

	t.Run("multiline token shared source", func(t *testing.T) {
		t.Parallel()

		// Simulate a multiline string spanning 3 lines.
		tkb := yamltest.NewTokenBuilder()
		sharedSource := tkb.Clone().Value("multiline").Build()

		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(sharedSource, tkb.Clone().Origin("line1").Build())}, // Width 5.
			tokens.Segments{tokens.NewSegment(sharedSource, tkb.Clone().Origin("line2").Build())}, // Width 5.
			tokens.Segments{tokens.NewSegment(sharedSource, tkb.Clone().Origin("line3").Build())}, // Width 5.
		}

		// Click on line 1 should highlight all 3 lines.
		got := s2.TokenRangesAt(0, 2)
		require.Len(t, got, 3)

		// Line 0.
		assert.Equal(t, 0, got[0].Start.Line)
		assert.Equal(t, 0, got[0].Start.Col)
		assert.Equal(t, 0, got[0].End.Line)
		assert.Equal(t, 5, got[0].End.Col)

		// Line 1.
		assert.Equal(t, 1, got[1].Start.Line)
		assert.Equal(t, 0, got[1].Start.Col)
		assert.Equal(t, 1, got[1].End.Line)
		assert.Equal(t, 5, got[1].End.Col)

		// Line 2.
		assert.Equal(t, 2, got[2].Start.Line)
		assert.Equal(t, 0, got[2].Start.Col)
		assert.Equal(t, 2, got[2].End.Line)
		assert.Equal(t, 5, got[2].End.Col)
	})

	t.Run("zero width segments skipped", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("test").Build()
		s2 := tokens.Segments2{
			tokens.Segments{
				tokens.NewSegment(source, tkb.Clone().Origin("").Build()),    // Width 0.
				tokens.NewSegment(source, tkb.Clone().Origin("abc").Build()), // Width 3.
			},
		}

		got := s2.TokenRangesAt(0, 0)
		// Only the non-zero-width segment should be included.
		require.Len(t, got, 1)
		assert.Equal(t, 0, got[0].Start.Col)
		assert.Equal(t, 3, got[0].End.Col)
	})

	t.Run("empty segments2", func(t *testing.T) {
		t.Parallel()

		var s2 tokens.Segments2

		got := s2.TokenRangesAt(0, 0)

		assert.Nil(t, got)
	})
}

func TestSplitDocuments(t *testing.T) {
	t.Parallel()

	t.Run("nil input", func(t *testing.T) {
		t.Parallel()

		got := collectDocs(tokens.SplitDocuments(nil))

		assert.Empty(t, got)
	})

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()

		got := collectDocs(tokens.SplitDocuments(token.Tokens{}))

		assert.Empty(t, got)
	})

	t.Run("single doc no header", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 3)

		diff := yamltest.CompareTokenSlices(input, got[0])
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("single doc with header", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build(),
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 4)

		diff := yamltest.CompareTokenSlices(input, got[0])
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("two docs", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		key1 := tkb.Clone().Type(token.StringType).Value("key1").Build()
		colon1 := tkb.Clone().Type(token.MappingValueType).Value(":").Build()
		value1 := tkb.Clone().Type(token.StringType).Value("v1").Build()
		header := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		key2 := tkb.Clone().Type(token.StringType).Value("key2").Build()
		colon2 := tkb.Clone().Type(token.MappingValueType).Value(":").Build()
		value2 := tkb.Clone().Type(token.StringType).Value("v2").Build()

		input := token.Tokens{key1, colon1, value1, header, key2, colon2, value2}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 2)
		require.Len(t, got[0], 3)
		require.Len(t, got[1], 4)

		diff0 := yamltest.CompareTokenSlices(token.Tokens{key1, colon1, value1}, got[0])
		require.True(t, diff0.Equal(), diff0.String())

		diff1 := yamltest.CompareTokenSlices(token.Tokens{header, key2, colon2, value2}, got[1])
		require.True(t, diff1.Equal(), diff1.String())
	})

	t.Run("three docs with headers", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		header1 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").Build()
		header2 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").Build()
		header3 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc3 := tkb.Clone().Type(token.StringType).Value("doc3").Build()

		input := token.Tokens{header1, doc1, header2, doc2, header3, doc3}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 3)

		diff0 := yamltest.CompareTokenSlices(token.Tokens{header1, doc1}, got[0])
		require.True(t, diff0.Equal(), diff0.String())

		diff1 := yamltest.CompareTokenSlices(token.Tokens{header2, doc2}, got[1])
		require.True(t, diff1.Equal(), diff1.String())

		diff2 := yamltest.CompareTokenSlices(token.Tokens{header3, doc3}, got[2])
		require.True(t, diff2.Equal(), diff2.String())
	})

	t.Run("doc with end marker", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
			tkb.Clone().Type(token.DocumentEndType).Value("...").Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 4)

		diff := yamltest.CompareTokenSlices(input, got[0])
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("doc end followed by new doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").Build()
		docEnd := tkb.Clone().Type(token.DocumentEndType).Value("...").Build()
		header := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").Build()

		input := token.Tokens{doc1, docEnd, header, doc2}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 2)

		diff0 := yamltest.CompareTokenSlices(token.Tokens{doc1, docEnd}, got[0])
		require.True(t, diff0.Equal(), diff0.String())

		diff1 := yamltest.CompareTokenSlices(token.Tokens{header, doc2}, got[1])
		require.True(t, diff1.Equal(), diff1.String())
	})

	t.Run("early termination at first doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").Build()
		header := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").Build()

		input := token.Tokens{doc1, header, doc2}

		var got []token.Tokens
		for _, tks := range tokens.SplitDocuments(input) {
			got = append(got, tks)

			break // Early termination after first document.
		}

		require.Len(t, got, 1)

		diff := yamltest.CompareTokenSlices(token.Tokens{doc1}, got[0])
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("early termination at second doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").Build()
		header1 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").Build()
		header2 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc3 := tkb.Clone().Type(token.StringType).Value("doc3").Build()

		input := token.Tokens{doc1, header1, doc2, header2, doc3}

		var got []token.Tokens

		count := 0
		for _, tks := range tokens.SplitDocuments(input) {
			got = append(got, tks)
			count++
			if count == 2 {
				break // Early termination after second document.
			}
		}

		require.Len(t, got, 2)

		diff0 := yamltest.CompareTokenSlices(token.Tokens{doc1}, got[0])
		require.True(t, diff0.Equal(), diff0.String())

		diff1 := yamltest.CompareTokenSlices(token.Tokens{header1, doc2}, got[1])
		require.True(t, diff1.Equal(), diff1.String())
	})

	t.Run("early termination single doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("only").Build(),
		}

		var got []token.Tokens
		for _, tks := range tokens.SplitDocuments(input) {
			got = append(got, tks)

			break // Early termination on single document.
		}

		require.Len(t, got, 1)
		require.Len(t, got[0], 1)
	})
}

func TestSplitDocuments_WithResetPositions(t *testing.T) {
	t.Parallel()

	t.Run("single doc resets to line 1", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").
				PositionLine(5).PositionColumn(6).PositionOffset(103).Build(),
			tkb.Clone().Type(token.StringType).Value("value").
				PositionLine(5).PositionColumn(8).PositionOffset(105).Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input, tokens.WithResetPositions()))

		require.Len(t, got, 1)
		require.Len(t, got[0], 3)

		// All tokens should be on line 1 now.
		assert.Equal(t, 1, got[0][0].Position.Line)
		assert.Equal(t, 1, got[0][0].Position.Column)
		assert.Equal(t, 0, got[0][0].Position.Offset)

		assert.Equal(t, 1, got[0][1].Position.Line)
		assert.Equal(t, 4, got[0][1].Position.Column)
		assert.Equal(t, 3, got[0][1].Position.Offset)

		assert.Equal(t, 1, got[0][2].Position.Line)
		assert.Equal(t, 6, got[0][2].Position.Column)
		assert.Equal(t, 5, got[0][2].Position.Offset)
	})

	t.Run("multi doc each starts at line 1", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		// First document at line 1.
		doc1Key := tkb.Clone().Type(token.StringType).Value("key1").
			PositionLine(1).PositionColumn(1).PositionOffset(0).Build()
		// Second document starts at line 3.
		header := tkb.Clone().Type(token.DocumentHeaderType).Value("---").
			PositionLine(3).PositionColumn(1).PositionOffset(15).Build()
		doc2Key := tkb.Clone().Type(token.StringType).Value("key2").
			PositionLine(4).PositionColumn(1).PositionOffset(20).Build()

		input := token.Tokens{doc1Key, header, doc2Key}

		got := collectDocs(tokens.SplitDocuments(input, tokens.WithResetPositions()))

		require.Len(t, got, 2)

		// First document should start at line 1.
		require.Len(t, got[0], 1)
		assert.Equal(t, 1, got[0][0].Position.Line)
		assert.Equal(t, 1, got[0][0].Position.Column)
		assert.Equal(t, 0, got[0][0].Position.Offset)

		// Second document should also start at line 1.
		require.Len(t, got[1], 2)
		assert.Equal(t, 1, got[1][0].Position.Line) // Header.
		assert.Equal(t, 1, got[1][0].Position.Column)
		assert.Equal(t, 0, got[1][0].Position.Offset)

		assert.Equal(t, 2, got[1][1].Position.Line) // Key on next line.
		assert.Equal(t, 1, got[1][1].Position.Column)
		assert.Equal(t, 5, got[1][1].Position.Offset)
	})

	t.Run("preserves original tokens when option not used", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 1)

		// Position should be unchanged.
		assert.Equal(t, 5, got[0][0].Position.Line)
		assert.Equal(t, 3, got[0][0].Position.Column)
		assert.Equal(t, 100, got[0][0].Position.Offset)

		// Should be the same pointer (not cloned).
		assert.Same(t, input[0], got[0][0])
	})

	t.Run("clones tokens when reset", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		original := tkb.Clone().Type(token.StringType).Value("key").
			PositionLine(5).PositionColumn(3).PositionOffset(100).Build()
		input := token.Tokens{original}

		got := collectDocs(tokens.SplitDocuments(input, tokens.WithResetPositions()))

		require.Len(t, got, 1)
		require.Len(t, got[0], 1)

		// Should be a different pointer (cloned).
		assert.NotSame(t, original, got[0][0])

		// Original should be unchanged.
		assert.Equal(t, 5, original.Position.Line)
		assert.Equal(t, 3, original.Position.Column)
		assert.Equal(t, 100, original.Position.Offset)
	})

	t.Run("handles multiline tokens within doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		// A multiline block scalar starting at line 10.
		blockIndicator := tkb.Clone().Type(token.LiteralType).Value("|").
			PositionLine(10).PositionColumn(5).PositionOffset(50).Build()
		blockContent := tkb.Clone().Type(token.StringType).Value("line1\nline2").
			PositionLine(11).PositionColumn(5).PositionOffset(52).Build()

		input := token.Tokens{blockIndicator, blockContent}

		got := collectDocs(tokens.SplitDocuments(input, tokens.WithResetPositions()))

		require.Len(t, got, 1)
		require.Len(t, got[0], 2)

		// Block indicator should be at line 1.
		assert.Equal(t, 1, got[0][0].Position.Line)
		assert.Equal(t, 1, got[0][0].Position.Column)
		assert.Equal(t, 0, got[0][0].Position.Offset)

		// Content should be at line 2 (relative to doc start).
		assert.Equal(t, 2, got[0][1].Position.Line)
		assert.Equal(t, 5, got[0][1].Position.Column)
		assert.Equal(t, 2, got[0][1].Position.Offset)
	})

	t.Run("handles nil position", func(t *testing.T) {
		t.Parallel()

		// Token with nil position should not cause panic.
		input := token.Tokens{
			&token.Token{Type: token.StringType, Value: "test", Position: nil},
		}

		got := collectDocs(tokens.SplitDocuments(input, tokens.WithResetPositions()))

		require.Len(t, got, 1)
		require.Len(t, got[0], 1)
		assert.Nil(t, got[0][0].Position)
	})

	t.Run("handles empty input", func(t *testing.T) {
		t.Parallel()

		got := collectDocs(tokens.SplitDocuments(nil, tokens.WithResetPositions()))

		assert.Empty(t, got)
	})

	t.Run("three docs all reset independently", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		// Doc 1 at line 1.
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").
			PositionLine(1).PositionColumn(1).PositionOffset(0).Build()
		// Doc 2 at line 5.
		header2 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").
			PositionLine(5).PositionColumn(1).PositionOffset(20).Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").
			PositionLine(6).PositionColumn(1).PositionOffset(25).Build()
		// Doc 3 at line 10.
		header3 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").
			PositionLine(10).PositionColumn(1).PositionOffset(40).Build()
		doc3 := tkb.Clone().Type(token.StringType).Value("doc3").
			PositionLine(11).PositionColumn(1).PositionOffset(45).Build()

		input := token.Tokens{doc1, header2, doc2, header3, doc3}

		got := collectDocs(tokens.SplitDocuments(input, tokens.WithResetPositions()))

		require.Len(t, got, 3)

		// Doc 1 starts at line 1.
		assert.Equal(t, 1, got[0][0].Position.Line)

		// Doc 2 starts at line 1 (header) then line 2 (content).
		assert.Equal(t, 1, got[1][0].Position.Line)
		assert.Equal(t, 2, got[1][1].Position.Line)

		// Doc 3 starts at line 1 (header) then line 2 (content).
		assert.Equal(t, 1, got[2][0].Position.Line)
		assert.Equal(t, 2, got[2][1].Position.Line)
	})
}

func TestCloneWithResetPositions(t *testing.T) {
	t.Parallel()

	t.Run("resets positions to line 1 column 1", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").
				PositionLine(5).PositionColumn(6).PositionOffset(103).Build(),
			tkb.Clone().Type(token.StringType).Value("value").
				PositionLine(5).PositionColumn(8).PositionOffset(105).Build(),
		}

		got := tokens.CloneWithResetPositions(input)

		require.Len(t, got, 3)

		// First token should be at line 1, column 1, offset 0.
		assert.Equal(t, 1, got[0].Position.Line)
		assert.Equal(t, 1, got[0].Position.Column)
		assert.Equal(t, 0, got[0].Position.Offset)

		// Second token: same line, column adjusted relatively.
		assert.Equal(t, 1, got[1].Position.Line)
		assert.Equal(t, 4, got[1].Position.Column) // 6 - 3 + 1 = 4
		assert.Equal(t, 3, got[1].Position.Offset) // 103 - 100 = 3

		// Third token: same line, column adjusted relatively.
		assert.Equal(t, 1, got[2].Position.Line)
		assert.Equal(t, 6, got[2].Position.Column) // 8 - 3 + 1 = 6
		assert.Equal(t, 5, got[2].Position.Offset) // 105 - 100 = 5
	})

	t.Run("handles multiline tokens", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(10).PositionColumn(5).PositionOffset(50).Build(),
			tkb.Clone().Type(token.StringType).Value("value").
				PositionLine(11).PositionColumn(5).PositionOffset(55).Build(),
		}

		got := tokens.CloneWithResetPositions(input)

		require.Len(t, got, 2)

		// First token at line 1.
		assert.Equal(t, 1, got[0].Position.Line)
		assert.Equal(t, 1, got[0].Position.Column)
		assert.Equal(t, 0, got[0].Position.Offset)

		// Second token at line 2 (relative).
		assert.Equal(t, 2, got[1].Position.Line)
		assert.Equal(t, 5, got[1].Position.Column) // Column preserved for non-first lines.
		assert.Equal(t, 5, got[1].Position.Offset) // 55 - 50 = 5
	})

	t.Run("returns original slice for empty input", func(t *testing.T) {
		t.Parallel()

		input := token.Tokens{}
		got := tokens.CloneWithResetPositions(input)

		assert.Empty(t, got)
	})

	t.Run("returns original slice for nil input", func(t *testing.T) {
		t.Parallel()

		got := tokens.CloneWithResetPositions(nil)

		assert.Nil(t, got)
	})

	t.Run("clones tokens", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		original := tkb.Clone().Type(token.StringType).Value("key").
			PositionLine(5).PositionColumn(3).PositionOffset(100).Build()
		input := token.Tokens{original}

		got := tokens.CloneWithResetPositions(input)

		require.Len(t, got, 1)

		// Should be a different pointer.
		assert.NotSame(t, original, got[0])

		// Original should be unchanged.
		assert.Equal(t, 5, original.Position.Line)
		assert.Equal(t, 3, original.Position.Column)
		assert.Equal(t, 100, original.Position.Offset)
	})

	t.Run("handles nil position", func(t *testing.T) {
		t.Parallel()

		input := token.Tokens{
			&token.Token{Type: token.StringType, Value: "test", Position: nil},
		}

		got := tokens.CloneWithResetPositions(input)

		require.Len(t, got, 1)
		assert.Nil(t, got[0].Position)
	})

	t.Run("skips tokens with nil position when finding start", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			&token.Token{Type: token.StringType, Value: "nil-pos", Position: nil},
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
		}

		got := tokens.CloneWithResetPositions(input)

		require.Len(t, got, 2)

		// First token still has nil position.
		assert.Nil(t, got[0].Position)

		// Second token should be reset to line 1.
		assert.Equal(t, 1, got[1].Position.Line)
		assert.Equal(t, 1, got[1].Position.Column)
		assert.Equal(t, 0, got[1].Position.Offset)
	})

	t.Run("preserves token values", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("mykey").Origin("mykey").
				PositionLine(5).PositionColumn(3).Build(),
		}

		got := tokens.CloneWithResetPositions(input)

		require.Len(t, got, 1)
		assert.Equal(t, token.StringType, got[0].Type)
		assert.Equal(t, "mykey", got[0].Value)
		assert.Equal(t, "mykey", got[0].Origin)
	})
}

func TestTypeStyle(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		setup func() *token.Token
		want  style.Style
	}{
		"basic string type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.StringType).Build()
			},
			want: style.LiteralString,
		},
		"bool type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.BoolType).Build()
			},
			want: style.LiteralBoolean,
		},
		"comment type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.CommentType).Build()
			},
			want: style.Comment,
		},
		"mapping value type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.MappingValueType).Build()
			},
			want: style.PunctuationMappingValue,
		},
		"string followed by colon becomes mapping key": {
			setup: func() *token.Token {
				key := yamltest.NewTokenBuilder().Type(token.StringType).Value("key").Build()
				colon := yamltest.NewTokenBuilder().Type(token.MappingValueType).Value(":").Build()
				key.Next = colon
				colon.Prev = key

				return key
			},
			want: style.NameTag, // Mapping key style.
		},
		"string not followed by colon stays string": {
			setup: func() *token.Token {
				str := yamltest.NewTokenBuilder().Type(token.StringType).Value("value").Build()
				newline := yamltest.NewTokenBuilder().Type(token.StringType).Value("other").Build()
				str.Next = newline
				newline.Prev = str

				return str
			},
			want: style.LiteralString,
		},
		"token preceded by anchor inherits anchor style": {
			setup: func() *token.Token {
				anchor := yamltest.NewTokenBuilder().Type(token.AnchorType).Value("&").Build()
				name := yamltest.NewTokenBuilder().Type(token.StringType).Value("myanchor").Build()
				anchor.Next = name
				name.Prev = anchor

				return name
			},
			want: style.NameAnchor,
		},
		"token preceded by alias inherits alias style": {
			setup: func() *token.Token {
				alias := yamltest.NewTokenBuilder().Type(token.AliasType).Value("*").Build()
				name := yamltest.NewTokenBuilder().Type(token.StringType).Value("myalias").Build()
				alias.Next = name
				name.Prev = alias

				return name
			},
			want: style.NameAlias,
		},
		"unknown type returns text style": {
			setup: func() *token.Token {
				// Token with a type not in the map.
				tk := yamltest.NewTokenBuilder().Build()
				tk.Type = token.Type(999) // Arbitrary type not in map.

				return tk
			},
			want: style.Text,
		},
		"anchor type itself": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.AnchorType).Value("&").Build()
			},
			want: style.NameAnchor,
		},
		"alias type itself": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.AliasType).Value("*").Build()
			},
			want: style.NameAlias,
		},
		"integer type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.IntegerType).Value("42").Build()
			},
			want: style.LiteralNumberInteger,
		},
		"null type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.NullType).Value("null").Build()
			},
			want: style.LiteralNull,
		},
		"float type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.FloatType).Value("3.14").Build()
			},
			want: style.LiteralNumberFloat,
		},
		"double quote type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.DoubleQuoteType).Value("quoted").Build()
			},
			want: style.LiteralStringDouble,
		},
		"single quote type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.SingleQuoteType).Value("quoted").Build()
			},
			want: style.LiteralStringSingle,
		},
		"sequence entry type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.SequenceEntryType).Value("-").Build()
			},
			want: style.PunctuationSequenceEntry,
		},
		"sequence start type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.SequenceStartType).Value("[").Build()
			},
			want: style.PunctuationSequenceStart,
		},
		"sequence end type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.SequenceEndType).Value("]").Build()
			},
			want: style.PunctuationSequenceEnd,
		},
		"mapping start type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.MappingStartType).Value("{").Build()
			},
			want: style.PunctuationMappingStart,
		},
		"mapping end type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.MappingEndType).Value("}").Build()
			},
			want: style.PunctuationMappingEnd,
		},
		"tag type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.TagType).Value("!mytag").Build()
			},
			want: style.NameDecorator,
		},
		"directive type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.DirectiveType).Value("%YAML").Build()
			},
			want: style.CommentPreproc,
		},
		"document header type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.DocumentHeaderType).Value("---").Build()
			},
			want: style.PunctuationHeading,
		},
		"document end type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.DocumentEndType).Value("...").Build()
			},
			want: style.PunctuationHeading,
		},
		"literal block type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.LiteralType).Value("|").Build()
			},
			want: style.PunctuationBlockLiteral,
		},
		"folded block type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.FoldedType).Value(">").Build()
			},
			want: style.PunctuationBlockFolded,
		},
		"hex integer type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.HexIntegerType).Value("0xFF").Build()
			},
			want: style.LiteralNumberHex,
		},
		"octet integer type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.OctetIntegerType).Value("0o777").Build()
			},
			want: style.LiteralNumberOct,
		},
		"binary integer type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.BinaryIntegerType).Value("0b1010").Build()
			},
			want: style.LiteralNumberBin,
		},
		"infinity type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.InfinityType).Value(".inf").Build()
			},
			want: style.LiteralNumberInfinity,
		},
		"nan type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.NanType).Value(".nan").Build()
			},
			want: style.LiteralNumberNaN,
		},
		"merge key type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.MergeKeyType).Value("<<").Build()
			},
			want: style.NameAliasMerge,
		},
		"collect entry type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.CollectEntryType).Value(",").Build()
			},
			want: style.PunctuationCollectEntry,
		},
		"implicit null type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.ImplicitNullType).Value("").Build()
			},
			want: style.LiteralNullImplicit,
		},
		"space type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.SpaceType).Value(" ").Build()
			},
			want: style.Text,
		},
		"invalid type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.InvalidType).Value("???").Build()
			},
			want: style.GenericErrorInvalid,
		},
		"unknown type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.UnknownType).Value("???").Build()
			},
			want: style.GenericErrorUnknown,
		},
		"mapping key type": {
			setup: func() *token.Token {
				return yamltest.NewTokenBuilder().Type(token.MappingKeyType).Value("?").Build()
			},
			want: style.NameTag,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk := tc.setup()
			got := tokens.TypeStyle(tk)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestValueOffset(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		value  string
		origin string
		want   int
	}{
		"simple value matches origin": {
			value:  "hello",
			origin: "hello",
			want:   0,
		},
		"value with trailing newline": {
			value:  "hello",
			origin: "hello\n",
			want:   0,
		},
		"quoted string double": {
			value:  "hello",
			origin: `"hello"`,
			want:   1,
		},
		"quoted string single": {
			value:  "hello",
			origin: "'hello'",
			want:   1,
		},
		"value with leading spaces": {
			value:  "hello",
			origin: "  hello",
			want:   2,
		},
		"anchor with value": {
			value:  "myanchor",
			origin: "&myanchor",
			want:   1,
		},
		"alias with value": {
			value:  "myalias",
			origin: "*myalias",
			want:   1,
		},
		"multiline origin uses first line": {
			value:  "first",
			origin: "first\nsecond\nthird",
			want:   0,
		},
		"multiline with value on first line": {
			value:  "value",
			origin: "key: value\nmore",
			want:   5,
		},
		"empty origin": {
			value:  "test",
			origin: "",
			want:   0,
		},
		"value not found in origin": {
			value:  "notfound",
			origin: "something else",
			want:   0,
		},
		"empty value": {
			value:  "",
			origin: "test",
			want:   0,
		},
		"block scalar indicator": {
			value:  "content",
			origin: "|\n  content",
			want:   0, // Value not on first line.
		},
		"tag with value": {
			value:  "!mytag",
			origin: "!mytag",
			want:   0,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk := yamltest.NewTokenBuilder().Value(tc.value).Origin(tc.origin).Build()
			got := tokens.ValueOffset(tk)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestContentRangesAt(t *testing.T) {
	t.Parallel()

	t.Run("negative idx", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(tkb.Clone().Build(), tkb.Clone().Origin("test").Build())},
		}

		got := s2.ContentRangesAt(-1, 0)

		assert.Nil(t, got)
	})

	t.Run("idx out of bounds", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(tkb.Clone().Build(), tkb.Clone().Origin("test").Build())},
		}

		got := s2.ContentRangesAt(5, 0)

		assert.Nil(t, got)
	})

	t.Run("no segment at column", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		s2 := tokens.Segments2{
			tokens.Segments{
				tokens.NewSegment(tkb.Clone().Build(), tkb.Clone().Origin("abc").Build()),
			}, // Width 3, cols 0-2.
		}

		got := s2.ContentRangesAt(0, 10) // Column 10 is out of bounds.

		assert.Nil(t, got)
	})

	t.Run("simple content no spaces", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("test").Build()
		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(source, tkb.Clone().Origin("test").Build())}, // Width 4.
		}

		got := s2.ContentRangesAt(0, 0)

		require.Len(t, got, 1)
		assert.Equal(t, 0, got[0].Start.Line)
		assert.Equal(t, 0, got[0].Start.Col)
		assert.Equal(t, 0, got[0].End.Line)
		assert.Equal(t, 4, got[0].End.Col)
	})

	t.Run("content with leading spaces", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("value").Build()
		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(source, tkb.Clone().Origin("  value").Build())}, // Width 7.
		}

		got := s2.ContentRangesAt(0, 0)

		require.Len(t, got, 1)
		assert.Equal(t, 0, got[0].Start.Line)
		assert.Equal(t, 2, got[0].Start.Col) // Skips 2 leading spaces.
		assert.Equal(t, 0, got[0].End.Line)
		assert.Equal(t, 7, got[0].End.Col)
	})

	t.Run("content with trailing spaces", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("value").Build()
		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(source, tkb.Clone().Origin("value  ").Build())}, // Width 7.
		}

		got := s2.ContentRangesAt(0, 0)

		require.Len(t, got, 1)
		assert.Equal(t, 0, got[0].Start.Line)
		assert.Equal(t, 0, got[0].Start.Col)
		assert.Equal(t, 0, got[0].End.Line)
		assert.Equal(t, 5, got[0].End.Col) // Excludes 2 trailing spaces.
	})

	t.Run("content with both leading and trailing spaces", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("val").Build()
		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(source, tkb.Clone().Origin("  val  ").Build())}, // Width 7.
		}

		got := s2.ContentRangesAt(0, 0)

		require.Len(t, got, 1)
		assert.Equal(t, 0, got[0].Start.Line)
		assert.Equal(t, 2, got[0].Start.Col) // Skips 2 leading.
		assert.Equal(t, 0, got[0].End.Line)
		assert.Equal(t, 5, got[0].End.Col) // Excludes 2 trailing.
	})

	t.Run("all whitespace returns nil", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("").Build()
		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(source, tkb.Clone().Origin("    ").Build())}, // Width 4, all spaces.
		}

		got := s2.ContentRangesAt(0, 0)

		assert.Nil(t, got)
	})

	t.Run("multiline token shared source", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		sharedSource := tkb.Clone().Value("multiline").Build()

		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(sharedSource, tkb.Clone().Origin("  line1").Build())}, // Width 7.
			tokens.Segments{tokens.NewSegment(sharedSource, tkb.Clone().Origin("line2  ").Build())}, // Width 7.
			tokens.Segments{tokens.NewSegment(sharedSource, tkb.Clone().Origin(" mid ").Build())},   // Width 5.
		}

		got := s2.ContentRangesAt(0, 2)

		require.Len(t, got, 3)
		// Line 0: "  line1" -> content starts at col 2, ends at 7.
		assert.Equal(t, 0, got[0].Start.Line)
		assert.Equal(t, 2, got[0].Start.Col)
		assert.Equal(t, 0, got[0].End.Line)
		assert.Equal(t, 7, got[0].End.Col)
		// Line 1: "line2  " -> content starts at col 0, ends at 5.
		assert.Equal(t, 1, got[1].Start.Line)
		assert.Equal(t, 0, got[1].Start.Col)
		assert.Equal(t, 1, got[1].End.Line)
		assert.Equal(t, 5, got[1].End.Col)
		// Line 2: " mid " -> content starts at col 1, ends at 4.
		assert.Equal(t, 2, got[2].Start.Line)
		assert.Equal(t, 1, got[2].Start.Col)
		assert.Equal(t, 2, got[2].End.Line)
		assert.Equal(t, 4, got[2].End.Col)
	})

	t.Run("content with trailing newline stripped", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("test").Build()
		s2 := tokens.Segments2{
			tokens.Segments{
				tokens.NewSegment(source, tkb.Clone().Origin("test\n").Build()),
			}, // Width 4 (newline stripped).
		}

		got := s2.ContentRangesAt(0, 0)

		require.Len(t, got, 1)
		assert.Equal(t, 0, got[0].Start.Col)
		assert.Equal(t, 4, got[0].End.Col)
	})

	t.Run("content with carriage return stripped", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("test").Build()
		// NewSegment computes width as 5 ("test\r" after stripping only "\n").
		// ContentRangesAt strips both \r and \n from origin but uses the segment's
		// pre-computed width for content width calculation.
		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(source, tkb.Clone().Origin("test\r\n").Build())},
		}

		got := s2.ContentRangesAt(0, 0)

		require.Len(t, got, 1)
		assert.Equal(t, 0, got[0].Start.Col)
		// End col is 5 because width comes from NewSegment which keeps the \r.
		assert.Equal(t, 5, got[0].End.Col)
	})

	t.Run("empty segments2", func(t *testing.T) {
		t.Parallel()

		var s2 tokens.Segments2

		got := s2.ContentRangesAt(0, 0)

		assert.Nil(t, got)
	})

	t.Run("multiple segments on same line", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		source1 := tkb.Clone().Value("key").Build()
		source2 := tkb.Clone().Value(":").Build()
		source3 := tkb.Clone().Value("val").Build()

		s2 := tokens.Segments2{
			tokens.Segments{
				tokens.NewSegment(source1, tkb.Clone().Origin("key").Build()),  // Width 3, cols 0-2.
				tokens.NewSegment(source2, tkb.Clone().Origin(": ").Build()),   // Width 2, cols 3-4.
				tokens.NewSegment(source3, tkb.Clone().Origin(" val").Build()), // Width 4, cols 5-8.
			},
		}

		// Click on "val" segment (cols 5-8) which has leading space.
		got := s2.ContentRangesAt(0, 6)

		require.Len(t, got, 1)
		assert.Equal(t, 0, got[0].Start.Line)
		assert.Equal(t, 6, got[0].Start.Col) // Skips leading space at col 5.
		assert.Equal(t, 0, got[0].End.Line)
		assert.Equal(t, 9, got[0].End.Col)
	})
}
