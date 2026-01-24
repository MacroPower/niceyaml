package niceyaml_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/line"
	"github.com/macropower/niceyaml/yamltest"
)

func TestFullDiff_Source(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		after  string
		want   string
	}{
		"no changes": {
			before: "key: value\n",
			after:  "key: value\n",
			want:   "   1 | key: value",
		},
		"simple insertion": {
			before: "a: 1\n",
			after:  "a: 1\nb: 2\n",
			want: yamltest.JoinLF(
				"   1 | a: 1",
				"   2 | b: 2",
			),
		},
		"simple deletion": {
			before: "a: 1\nb: 2\n",
			after:  "a: 1\n",
			want: yamltest.JoinLF(
				"   1 | a: 1",
				"   2 | b: 2",
			),
		},
		"modification": {
			before: "key: old\n",
			after:  "key: new\n",
			want: yamltest.JoinLF(
				"   1 | key: old",
				"   1 | key: new",
			),
		},
		"empty before": {
			before: "",
			after:  "key: value\n",
			want:   "   1 | key: value",
		},
		"empty after": {
			before: "key: value\n",
			after:  "",
			want:   "   1 | key: value",
		},
		"both empty": {
			before: "",
			after:  "",
			want:   "",
		},
		"multi-line modification": {
			before: "first: 1\nsecond: 2\nthird: 3\n",
			after:  "first: 1\nsecond: changed\nthird: 3\n",
			want: yamltest.JoinLF(
				"   1 | first: 1",
				"   2 | second: 2",
				"   2 | second: changed",
				"   3 | third: 3",
			),
		},
		"change with surrounding context": {
			before: yamltest.Input(`
				header: value
				unchanged1: foo
				unchanged2: bar
				middle: old
				unchanged3: baz
				unchanged4: qux
				footer: end
			`),
			after: yamltest.Input(`
				header: value
				unchanged1: foo
				unchanged2: bar
				middle: new
				unchanged3: baz
				unchanged4: qux
				footer: end
			`),
			want: yamltest.JoinLF(
				"   1 | header: value",
				"   2 | unchanged1: foo",
				"   3 | unchanged2: bar",
				"   4 | middle: old",
				"   4 | middle: new",
				"   5 | unchanged3: baz",
				"   6 | unchanged4: qux",
				"   7 | footer: end",
			),
		},
		"multiple scattered changes": {
			before: yamltest.Input(`
				first: 1
				second: 2
				third: 3
				fourth: 4
				fifth: 5
			`),
			after: yamltest.Input(`
				first: changed1
				second: 2
				third: changed3
				fourth: 4
				fifth: changed5
			`),
			want: yamltest.JoinLF(
				"   1 | first: 1",
				"   1 | first: changed1",
				"   2 | second: 2",
				"   3 | third: 3",
				"   3 | third: changed3",
				"   4 | fourth: 4",
				"   5 | fifth: 5",
				"   5 | fifth: changed5",
			),
		},
		"consecutive insertions": {
			before: yamltest.Input(`
				before: 1
				after: 2
			`),
			after: yamltest.Input(`
				before: 1
				new1: a
				new2: b
				new3: c
				after: 2
			`),
			want: yamltest.JoinLF(
				"   1 | before: 1",
				"   2 | new1: a",
				"   3 | new2: b",
				"   4 | new3: c",
				"   5 | after: 2",
			),
		},
		"consecutive deletions": {
			before: yamltest.Input(`
				before: 1
				old1: a
				old2: b
				old3: c
				after: 2
			`),
			after: yamltest.Input(`
				before: 1
				after: 2
			`),
			want: yamltest.JoinLF(
				"   1 | before: 1",
				"   2 | old1: a",
				"   3 | old2: b",
				"   4 | old3: c",
				"   2 | after: 2",
			),
		},
		"nested yaml structure": {
			before: yamltest.Input(`
				metadata:
				  name: myapp
				  namespace: default
				spec:
				  replicas: 3
				  template:
				    image: nginx:1.19
			`),
			after: yamltest.Input(`
				metadata:
				  name: myapp
				  namespace: production
				spec:
				  replicas: 5
				  template:
				    image: nginx:1.21
			`),
			want: yamltest.JoinLF(
				"   1 | metadata:",
				"   2 |   name: myapp",
				"   3 |   namespace: default",
				"   3 |   namespace: production",
				"   4 | spec:",
				"   5 |   replicas: 3",
				"   5 |   replicas: 5",
				"   6 |   template:",
				"   7 |     image: nginx:1.19",
				"   7 |     image: nginx:1.21",
			),
		},
		"change at beginning": {
			before: yamltest.Input(`
				first: old
				second: 2
				third: 3
				fourth: 4
			`),
			after: yamltest.Input(`
				first: new
				second: 2
				third: 3
				fourth: 4
			`),
			want: yamltest.JoinLF(
				"   1 | first: old",
				"   1 | first: new",
				"   2 | second: 2",
				"   3 | third: 3",
				"   4 | fourth: 4",
			),
		},
		"change at end": {
			before: yamltest.Input(`
				first: 1
				second: 2
				third: 3
				fourth: old
			`),
			after: yamltest.Input(`
				first: 1
				second: 2
				third: 3
				fourth: new
			`),
			want: yamltest.JoinLF(
				"   1 | first: 1",
				"   2 | second: 2",
				"   3 | third: 3",
				"   4 | fourth: old",
				"   4 | fourth: new",
			),
		},
		"yaml with list items": {
			before: yamltest.Input(`
				items:
				  - name: item1
				    value: 100
				  - name: item2
				    value: 200
			`),
			after: yamltest.Input(`
				items:
				  - name: item1
				    value: 150
				  - name: item2
				    value: 200
			`),
			want: yamltest.JoinLF(
				"   1 | items:",
				"   2 |   - name: item1",
				"   3 |     value: 100",
				"   3 |     value: 150",
				"   4 |   - name: item2",
				"   5 |     value: 200",
			),
		},
		"insert and delete in same region": {
			before: yamltest.Input(`
				keep1: a
				delete1: x
				delete2: y
				keep2: b
			`),
			after: yamltest.Input(`
				keep1: a
				insert1: p
				insert2: q
				keep2: b
			`),
			want: yamltest.JoinLF(
				"   1 | keep1: a",
				"   2 | delete1: x",
				"   3 | delete2: y",
				"   2 | insert1: p",
				"   3 | insert2: q",
				"   4 | keep2: b",
			),
		},
		"large context around small change": {
			before: yamltest.Input(`
				line1: 1
				line2: 2
				line3: 3
				line4: 4
				line5: 5
				line6: 6
				line7: 7
				line8: 8
				line9: 9
				line10: old
				line11: 11
				line12: 12
				line13: 13
				line14: 14
				line15: 15
			`),
			after: yamltest.Input(`
				line1: 1
				line2: 2
				line3: 3
				line4: 4
				line5: 5
				line6: 6
				line7: 7
				line8: 8
				line9: 9
				line10: new
				line11: 11
				line12: 12
				line13: 13
				line14: 14
				line15: 15
			`),
			want: yamltest.JoinLF(
				"   1 | line1: 1",
				"   2 | line2: 2",
				"   3 | line3: 3",
				"   4 | line4: 4",
				"   5 | line5: 5",
				"   6 | line6: 6",
				"   7 | line7: 7",
				"   8 | line8: 8",
				"   9 | line9: 9",
				"  10 | line10: old",
				"  10 | line10: new",
				"  11 | line11: 11",
				"  12 | line12: 12",
				"  13 | line13: 13",
				"  14 | line14: 14",
				"  15 | line15: 15",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			beforeTokens := niceyaml.NewSourceFromString(tc.before, niceyaml.WithName("a"))
			afterTokens := niceyaml.NewSourceFromString(tc.after, niceyaml.WithName("b"))

			revA := niceyaml.NewRevision(beforeTokens)
			revB := niceyaml.NewRevision(afterTokens)
			diff := niceyaml.NewFullDiff(revA, revB)

			got := diff.Build()

			assert.Equal(t, "a..b", got.Name())
			assert.Equal(t, tc.want, got.String())
		})
	}
}

func TestFullDiff_Source_Flags(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		wantFlags        map[int]line.Flag
		before           string
		after            string
		wantFlaggedCount int
	}{
		"insertion gets flag": {
			before:           "a: 1\n",
			after:            "a: 1\nb: 2\n",
			wantFlaggedCount: 1,
			wantFlags: map[int]line.Flag{
				1: line.FlagInserted,
			},
		},
		"deletion gets flag": {
			before:           "a: 1\nb: 2\n",
			after:            "a: 1\n",
			wantFlaggedCount: 1,
			wantFlags: map[int]line.Flag{
				1: line.FlagDeleted,
			},
		},
		"modification has delete and insert flags": {
			before:           "key: old\n",
			after:            "key: new\n",
			wantFlaggedCount: 2,
			wantFlags: map[int]line.Flag{
				0: line.FlagDeleted,
				1: line.FlagInserted,
			},
		},
		"only changed lines get flags": {
			before: yamltest.Input(`
				first: 1
				second: 2
				third: 3
			`),
			after: yamltest.Input(`
				first: 1
				second: changed
				third: 3
			`),
			wantFlaggedCount: 2,
			wantFlags: map[int]line.Flag{
				0: line.FlagDefault,
				1: line.FlagDeleted,
				2: line.FlagInserted,
				3: line.FlagDefault,
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			beforeTokens := niceyaml.NewSourceFromString(tc.before, niceyaml.WithName("a"))
			afterTokens := niceyaml.NewSourceFromString(tc.after, niceyaml.WithName("b"))

			revA := niceyaml.NewRevision(beforeTokens)
			revB := niceyaml.NewRevision(afterTokens)
			diff := niceyaml.NewFullDiff(revA, revB)

			got := diff.Build()

			flaggedCount := 0
			for _, ln := range got.AllLines() {
				if ln.Flag != line.FlagDefault {
					flaggedCount++
				}
			}

			assert.Equal(t, tc.wantFlaggedCount, flaggedCount)

			for lineIdx, wantFlag := range tc.wantFlags {
				assert.Equal(t, wantFlag, got.Line(lineIdx).Flag)
			}
		})
	}
}

func TestSummaryDiff_Source(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before      string
		after       string
		context     int
		wantEmpty   bool              // Expect IsEmpty() and Tokens() == nil.
		wantRanges  int               // Expected number of ranges (hunks).
		flags       map[int]line.Flag // Optional: check specific line flags (0-indexed in full source).
		annotations map[int]string    // Optional: check annotations (0-indexed in full source).
	}{
		"context limits output": {
			before: yamltest.Input(`
				line1: 1
				line2: 2
				line3: 3
				line4: 4
				line5: old
				line6: 6
				line7: 7
				line8: 8
				line9: 9
			`),
			after: yamltest.Input(`
				line1: 1
				line2: 2
				line3: 3
				line4: 4
				line5: new
				line6: 6
				line7: 7
				line8: 8
				line9: 9
			`),
			context:    1,
			wantRanges: 1,
			flags: map[int]line.Flag{
				0: line.FlagDefault,  // Line1.
				3: line.FlagDefault,  // Line4 (context).
				4: line.FlagDeleted,  // Line5: old.
				5: line.FlagInserted, // Line5: new.
				6: line.FlagDefault,  // Line6 (context).
			},
			annotations: map[int]string{3: "@@ -4,3 +4,3 @@"},
		},
		"context 0 shows only changes": {
			before: yamltest.Input(`
				line1: 1
				line2: 2
				line3: old
				line4: 4
				line5: 5
			`),
			after: yamltest.Input(`
				line1: 1
				line2: 2
				line3: new
				line4: 4
				line5: 5
			`),
			context:    0,
			wantRanges: 1,
			flags: map[int]line.Flag{
				2: line.FlagDeleted,
				3: line.FlagInserted,
			},
			annotations: map[int]string{2: "@@ -3 +3 @@"},
		},
		"hunk header in annotation": {
			before: yamltest.Input(`
				first: 1
				second: 2
				third: 3
			`),
			after: yamltest.Input(`
				first: 1
				second: changed
				third: 3
			`),
			context:     1,
			wantRanges:  1,
			annotations: map[int]string{0: "@@ -1,3 +1,3 @@"},
		},
		"multiple hunks have separate annotations": {
			before: yamltest.Input(`
				line1: old1
				line2: 2
				line3: 3
				line4: 4
				line5: 5
				line6: 6
				line7: 7
				line8: 8
				line9: old9
			`),
			after: yamltest.Input(`
				line1: new1
				line2: 2
				line3: 3
				line4: 4
				line5: 5
				line6: 6
				line7: 7
				line8: 8
				line9: new9
			`),
			context:    1,
			wantRanges: 2,
			// Full source has 11 lines (9 equal + 2 changes = delete+insert each).
			// First hunk: indices 0-2 (delete, insert, context line2).
			// Second hunk: indices 8-10 (context line8, delete, insert).
			annotations: map[int]string{
				0: "@@ -1,2 +1,2 @@",
				8: "@@ -8,2 +8,2 @@",
			},
		},
		"no changes returns empty tokens": {
			before:     "key: value\n",
			after:      "key: value\n",
			context:    3,
			wantEmpty:  true,
			wantRanges: 0,
		},
		"empty before": {
			before:     "",
			after:      "key: value\n",
			context:    3,
			wantRanges: 1,
			flags:      map[int]line.Flag{0: line.FlagInserted},
		},
		"empty after": {
			before:     "key: value\n",
			after:      "",
			context:    3,
			wantRanges: 1,
			flags:      map[int]line.Flag{0: line.FlagDeleted},
		},
		"negative context treated as zero": {
			before: yamltest.Input(`
				line1: 1
				line2: old
				line3: 3
			`),
			after: yamltest.Input(`
				line1: 1
				line2: new
				line3: 3
			`),
			context:    -5,
			wantRanges: 1,
			flags: map[int]line.Flag{
				1: line.FlagDeleted,
				2: line.FlagInserted,
			},
			annotations: map[int]string{1: "@@ -2 +2 @@"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			beforeTokens := niceyaml.NewSourceFromString(tc.before, niceyaml.WithName("a"))
			afterTokens := niceyaml.NewSourceFromString(tc.after, niceyaml.WithName("b"))

			revA := niceyaml.NewRevision(beforeTokens)
			revB := niceyaml.NewRevision(afterTokens)

			got, ranges := niceyaml.NewSummaryDiff(revA, revB, tc.context).Build()

			assert.Equal(t, "a..b", got.Name())
			assert.Len(t, ranges, tc.wantRanges)

			if tc.wantEmpty {
				assert.True(t, got.IsEmpty())
				assert.Nil(t, got.Tokens())

				return
			}

			for lineIdx, wantFlag := range tc.flags {
				assert.Equal(t, wantFlag, got.Line(lineIdx).Flag, "flag mismatch at line %d", lineIdx)
			}

			if tc.annotations != nil {
				for lineIdx, wantAnnotation := range tc.annotations {
					anns := got.Line(lineIdx).Annotations
					require.NotEmpty(t, anns, "expected annotation at line %d", lineIdx)
					assert.Equal(t, wantAnnotation, anns[0].Content)
				}
			}
		})
	}
}
