package tokens_test

import (
	"iter"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml/tokens"
	"github.com/macropower/niceyaml/yamltest"
)

func collectDocs(seq iter.Seq2[int, token.Tokens]) []token.Tokens {
	var result []token.Tokens //nolint:prealloc // Size unknown from iterator.

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
	yamltest.RequireTokenValid(t, part, gotPart, "part")
	yamltest.AssertTokenEqual(t, part, gotPart, "part")

	gotSource := seg.Source()
	assert.NotSame(t, source, gotSource) // Should be cloned.
	yamltest.RequireTokenValid(t, source, gotSource, "source")
	yamltest.AssertTokenEqual(t, source, gotSource, "source")
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
		yamltest.RequireTokenValid(t, source, got, "source")
		yamltest.AssertTokenEqual(t, source, got, "source")
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
		yamltest.RequireTokenValid(t, part, got[0].Part(), "part")
		yamltest.AssertTokenEqual(t, part, got[0].Part(), "part")
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
		yamltest.RequireTokenValid(t, part1, got[0].Part(), "part1")
		yamltest.AssertTokenEqual(t, part1, got[0].Part(), "part1")
		yamltest.RequireTokenValid(t, part2, got[1].Part(), "part2")
		yamltest.AssertTokenEqual(t, part2, got[1].Part(), "part2")
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
		yamltest.RequireTokenValid(t, part, got[0].Part(), "part")
		yamltest.AssertTokenEqual(t, part, got[0].Part(), "part")
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
		yamltest.RequireTokenValid(t, source, got[0], "source")
		yamltest.AssertTokenEqual(t, source, got[0], "source")
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
		yamltest.RequireTokenValid(t, source, got[0], "source")
		yamltest.AssertTokenEqual(t, source, got[0], "source")
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
		yamltest.RequireTokenValid(t, source1, got[0], "source1")
		yamltest.AssertTokenEqual(t, source1, got[0], "source1")
		yamltest.RequireTokenValid(t, source2, got[1], "source2")
		yamltest.AssertTokenEqual(t, source2, got[1], "source2")
		yamltest.RequireTokenValid(t, source3, got[2], "source3")
		yamltest.AssertTokenEqual(t, source3, got[2], "source3")
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
		yamltest.RequireTokenValid(t, part1, got[0], "part1")
		yamltest.AssertTokenEqual(t, part1, got[0], "part1")
		yamltest.RequireTokenValid(t, part2, got[1], "part2")
		yamltest.AssertTokenEqual(t, part2, got[1], "part2")
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
		yamltest.RequireTokenValid(t, source1, segs.SourceTokenAt(0), "source1 at 0")
		yamltest.AssertTokenEqual(t, source1, segs.SourceTokenAt(0), "source1 at 0")
		yamltest.RequireTokenValid(t, source1, segs.SourceTokenAt(2), "source1 at 2")
		yamltest.AssertTokenEqual(t, source1, segs.SourceTokenAt(2), "source1 at 2")
		yamltest.RequireTokenValid(t, source2, segs.SourceTokenAt(3), "source2 at 3")
		yamltest.AssertTokenEqual(t, source2, segs.SourceTokenAt(3), "source2 at 3")
		yamltest.RequireTokenValid(t, source2, segs.SourceTokenAt(4), "source2 at 4")
		yamltest.AssertTokenEqual(t, source2, segs.SourceTokenAt(4), "source2 at 4")
		yamltest.RequireTokenValid(t, source3, segs.SourceTokenAt(5), "source3 at 5")
		yamltest.AssertTokenEqual(t, source3, segs.SourceTokenAt(5), "source3 at 5")
		yamltest.RequireTokenValid(t, source3, segs.SourceTokenAt(9), "source3 at 9")
		yamltest.AssertTokenEqual(t, source3, segs.SourceTokenAt(9), "source3 at 9")

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
		yamltest.RequireTokenValid(t, source1, segs.SourceTokenAt(0), "source1 at 0")
		yamltest.AssertTokenEqual(t, source1, segs.SourceTokenAt(0), "source1 at 0")
		yamltest.RequireTokenValid(t, source1, segs.SourceTokenAt(2), "source1 at 2")
		yamltest.AssertTokenEqual(t, source1, segs.SourceTokenAt(2), "source1 at 2")
		yamltest.RequireTokenValid(t, source2, segs.SourceTokenAt(3), "source2 at 3")
		yamltest.AssertTokenEqual(t, source2, segs.SourceTokenAt(3), "source2 at 3")
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
		tkb := yamltest.NewTokenBuilder()
		sharedSource := tkb.Clone().Value("multiline").Build()

		s2 := tokens.Segments2{
			tokens.Segments{tokens.NewSegment(sharedSource, tkb.Clone().Origin("line1").Build())}, // Width 5.
			tokens.Segments{tokens.NewSegment(sharedSource, tkb.Clone().Origin("line2").Build())}, // Width 5.
			tokens.Segments{tokens.NewSegment(sharedSource, tkb.Clone().Origin("line3").Build())}, // Width 5.
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

		tkb := yamltest.NewTokenBuilder()
		source := tkb.Clone().Value("test").Build()
		s2 := tokens.Segments2{
			tokens.Segments{
				tokens.NewSegment(source, tkb.Clone().Origin("").Build()),    // Width 0.
				tokens.NewSegment(source, tkb.Clone().Origin("abc").Build()), // Width 3.
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
		yamltest.AssertTokensEqual(t, input, got[0])
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
		yamltest.AssertTokensEqual(t, input, got[0])
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

		yamltest.AssertTokensEqual(t, token.Tokens{key1, colon1, value1}, got[0])
		yamltest.AssertTokensEqual(t, token.Tokens{header, key2, colon2, value2}, got[1])
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
		yamltest.AssertTokensEqual(t, token.Tokens{header1, doc1}, got[0])
		yamltest.AssertTokensEqual(t, token.Tokens{header2, doc2}, got[1])
		yamltest.AssertTokensEqual(t, token.Tokens{header3, doc3}, got[2])
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
		yamltest.AssertTokensEqual(t, input, got[0])
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
		yamltest.AssertTokensEqual(t, token.Tokens{doc1, docEnd}, got[0])
		yamltest.AssertTokensEqual(t, token.Tokens{header, doc2}, got[1])
	})
}
