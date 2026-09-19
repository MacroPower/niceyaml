package diff_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/diff/lcs"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

func TestDiffer_Views(t *testing.T) {
	t.Parallel()

	before := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	after := niceyaml.NewSourceFromString("a: 1\nb: 3\n")

	result := diff.Diff(before.Lines(), after.Lines())

	got := result.Unified()
	require.Equal(t, 3, got.Len())
	assert.Equal(t, line.FlagDefault, got.Flag(0))
	assert.Equal(t, line.FlagDeleted, got.Flag(1))
	assert.Equal(t, line.FlagInserted, got.Flag(2))

	// Inputs are content only, so the result carries the flags of the diff
	// and nothing else.
	for i := range got.Len() {
		assert.Empty(t, got.Overlays(i))
	}

	// Each result view owns its decoration, and none of it reaches the
	// sources the diff was computed from.
	got.AddOverlay(kind.GenericHighlight, position.NewRange(position.New(2, 0), position.New(2, 1)))
	assert.Len(t, got.Overlays(2), 1)
	assert.Empty(t, result.Unified().Overlays(2))
	assert.Empty(t, after.View().Overlays(1))
	assert.Empty(t, before.View().Overlays(1))

	// The diff of a diff is a diff of the view's content.
	again := diff.Diff(got.Lines(), got.Lines())
	assert.Equal(t, 3, again.Unified().Len())
}

func TestDiffer_Views_LineNumbers(t *testing.T) {
	t.Parallel()

	// An unchanged line keeps the number it has in each revision, so an
	// insertion at the top of the after revision leaves the before view's
	// numbers alone.
	before := niceyaml.NewSourceFromString("a: 1\nb: 2\n").Lines()
	after := niceyaml.NewSourceFromString("x: 0\na: 1\nb: 2\n").Lines()

	result := diff.New().Diff(before, after)

	var beforeNumbers, afterNumbers []int

	for _, l := range result.Before().Lines().AllLines() {
		beforeNumbers = append(beforeNumbers, l.Number())
	}

	for _, l := range result.After().Lines().AllLines() {
		afterNumbers = append(afterNumbers, l.Number())
	}

	assert.Equal(t, []int{0, 1, 2}, beforeNumbers)
	assert.Equal(t, []int{1, 2, 3}, afterNumbers)
}

func TestDiffer_Full(t *testing.T) {
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
			want: stringtest.JoinLF(
				"   1 | a: 1",
				"   2 | b: 2",
			),
		},
		"simple deletion": {
			before: "a: 1\nb: 2\n",
			after:  "a: 1\n",
			want: stringtest.JoinLF(
				"   1 | a: 1",
				"   2 | b: 2",
			),
		},
		"modification": {
			before: "key: old\n",
			after:  "key: new\n",
			want: stringtest.JoinLF(
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
			want: stringtest.JoinLF(
				"   1 | first: 1",
				"   2 | second: 2",
				"   2 | second: changed",
				"   3 | third: 3",
			),
		},
		"change with surrounding context": {
			before: stringtest.Input(`
				header: value
				unchanged1: foo
				unchanged2: bar
				middle: old
				unchanged3: baz
				unchanged4: qux
				footer: end
			`),
			after: stringtest.Input(`
				header: value
				unchanged1: foo
				unchanged2: bar
				middle: new
				unchanged3: baz
				unchanged4: qux
				footer: end
			`),
			want: stringtest.JoinLF(
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
			before: stringtest.Input(`
				first: 1
				second: 2
				third: 3
				fourth: 4
				fifth: 5
			`),
			after: stringtest.Input(`
				first: changed1
				second: 2
				third: changed3
				fourth: 4
				fifth: changed5
			`),
			want: stringtest.JoinLF(
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
			before: stringtest.Input(`
				before: 1
				after: 2
			`),
			after: stringtest.Input(`
				before: 1
				new1: a
				new2: b
				new3: c
				after: 2
			`),
			want: stringtest.JoinLF(
				"   1 | before: 1",
				"   2 | new1: a",
				"   3 | new2: b",
				"   4 | new3: c",
				"   5 | after: 2",
			),
		},
		"consecutive deletions": {
			before: stringtest.Input(`
				before: 1
				old1: a
				old2: b
				old3: c
				after: 2
			`),
			after: stringtest.Input(`
				before: 1
				after: 2
			`),
			want: stringtest.JoinLF(
				"   1 | before: 1",
				"   2 | old1: a",
				"   3 | old2: b",
				"   4 | old3: c",
				"   2 | after: 2",
			),
		},
		"nested yaml structure": {
			before: stringtest.Input(`
				metadata:
				  name: myapp
				  namespace: default
				spec:
				  replicas: 3
				  template:
				    image: nginx:1.19
			`),
			after: stringtest.Input(`
				metadata:
				  name: myapp
				  namespace: production
				spec:
				  replicas: 5
				  template:
				    image: nginx:1.21
			`),
			want: stringtest.JoinLF(
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
			before: stringtest.Input(`
				first: old
				second: 2
				third: 3
				fourth: 4
			`),
			after: stringtest.Input(`
				first: new
				second: 2
				third: 3
				fourth: 4
			`),
			want: stringtest.JoinLF(
				"   1 | first: old",
				"   1 | first: new",
				"   2 | second: 2",
				"   3 | third: 3",
				"   4 | fourth: 4",
			),
		},
		"change at end": {
			before: stringtest.Input(`
				first: 1
				second: 2
				third: 3
				fourth: old
			`),
			after: stringtest.Input(`
				first: 1
				second: 2
				third: 3
				fourth: new
			`),
			want: stringtest.JoinLF(
				"   1 | first: 1",
				"   2 | second: 2",
				"   3 | third: 3",
				"   4 | fourth: old",
				"   4 | fourth: new",
			),
		},
		"yaml with list items": {
			before: stringtest.Input(`
				items:
				  - name: item1
				    value: 100
				  - name: item2
				    value: 200
			`),
			after: stringtest.Input(`
				items:
				  - name: item1
				    value: 150
				  - name: item2
				    value: 200
			`),
			want: stringtest.JoinLF(
				"   1 | items:",
				"   2 |   - name: item1",
				"   3 |     value: 100",
				"   3 |     value: 150",
				"   4 |   - name: item2",
				"   5 |     value: 200",
			),
		},
		"insert and delete in same region": {
			before: stringtest.Input(`
				keep1: a
				delete1: x
				delete2: y
				keep2: b
			`),
			after: stringtest.Input(`
				keep1: a
				insert1: p
				insert2: q
				keep2: b
			`),
			want: stringtest.JoinLF(
				"   1 | keep1: a",
				"   2 | delete1: x",
				"   3 | delete2: y",
				"   2 | insert1: p",
				"   3 | insert2: q",
				"   4 | keep2: b",
			),
		},
		"large context around small change": {
			before: stringtest.Input(`
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
			after: stringtest.Input(`
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
			want: stringtest.JoinLF(
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

			result := diff.Diff(beforeTokens.Lines(), afterTokens.Lines())

			got := result.Unified()
			assert.Equal(t, tc.want, got.String())
		})
	}
}

func TestDiffer_Full_Flags(t *testing.T) {
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
			before: stringtest.Input(`
				first: 1
				second: 2
				third: 3
			`),
			after: stringtest.Input(`
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

			result := diff.Diff(beforeTokens.Lines(), afterTokens.Lines())

			got := result.Unified()

			flaggedCount := 0
			for i := range got.AllLines() {
				if got.Flag(i) != line.FlagDefault {
					flaggedCount++
				}
			}

			assert.Equal(t, tc.wantFlaggedCount, flaggedCount)

			for lineIdx, wantFlag := range tc.wantFlags {
				assert.Equal(t, wantFlag, got.Flag(lineIdx))
			}
		})
	}
}

func TestDiffer_Hunks(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before      string
		after       string
		context     int
		wantEmpty   bool
		wantLen     int
		flags       map[int]line.Flag
		annotations map[int]string
	}{
		"context limits output": {
			before: stringtest.Input(`
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
			after: stringtest.Input(`
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
			context: 1,
			wantLen: 4,
			flags: map[int]line.Flag{
				0: line.FlagDefault,
				1: line.FlagDeleted,
				2: line.FlagInserted,
				3: line.FlagDefault,
			},
			annotations: map[int]string{0: "@@ -4,3 +4,3 @@"},
		},
		"context 0 shows only changes": {
			before: stringtest.Input(`
				line1: 1
				line2: 2
				line3: old
				line4: 4
				line5: 5
			`),
			after: stringtest.Input(`
				line1: 1
				line2: 2
				line3: new
				line4: 4
				line5: 5
			`),
			context: 0,
			wantLen: 2,
			flags: map[int]line.Flag{
				0: line.FlagDeleted,
				1: line.FlagInserted,
			},
			annotations: map[int]string{0: "@@ -3 +3 @@"},
		},
		"no changes returns empty": {
			before:    "key: value\n",
			after:     "key: value\n",
			context:   3,
			wantEmpty: true,
		},
		"negative context treated as zero": {
			before: stringtest.Input(`
				line1: 1
				line2: old
				line3: 3
			`),
			after: stringtest.Input(`
				line1: 1
				line2: new
				line3: 3
			`),
			context: -5,
			wantLen: 2,
			flags: map[int]line.Flag{
				0: line.FlagDeleted,
				1: line.FlagInserted,
			},
			annotations: map[int]string{0: "@@ -2 +2 @@"},
		},
		"append reports line before insertion with zero count": {
			before: stringtest.Input(`
				a: 1
				b: 2
				c: 3
			`),
			after: stringtest.Input(`
				a: 1
				b: 2
				c: 3
				d: 4
				e: 5
			`),
			context: 0,
			wantLen: 2,
			flags: map[int]line.Flag{
				0: line.FlagInserted,
				1: line.FlagInserted,
			},
			annotations: map[int]string{0: "@@ -3,0 +4,2 @@"},
		},
		"deletion reports line before removal with zero count": {
			before: stringtest.Input(`
				a: 1
				b: 2
				c: 3
			`),
			after: stringtest.Input(`
				a: 1
				c: 3
			`),
			context: 0,
			wantLen: 1,
			flags: map[int]line.Flag{
				0: line.FlagDeleted,
			},
			annotations: map[int]string{0: "@@ -2 +1,0 @@"},
		},
		"insertion at start reports zero line": {
			before: stringtest.Input(`
				a: 1
				b: 2
			`),
			after: stringtest.Input(`
				x: 0
				a: 1
				b: 2
			`),
			context: 0,
			wantLen: 1,
			flags: map[int]line.Flag{
				0: line.FlagInserted,
			},
			annotations: map[int]string{0: "@@ -0,0 +1 @@"},
		},
		"insertion into empty file": {
			before:  "",
			after:   "a: 1\n",
			context: 0,
			wantLen: 1,
			flags: map[int]line.Flag{
				0: line.FlagInserted,
			},
			annotations: map[int]string{0: "@@ -0,0 +1 @@"},
		},
		"deletion to empty file": {
			before:  "a: 1\nb: 2\n",
			after:   "",
			context: 0,
			wantLen: 2,
			flags: map[int]line.Flag{
				0: line.FlagDeleted,
				1: line.FlagDeleted,
			},
			annotations: map[int]string{0: "@@ -1,2 +0,0 @@"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			beforeTokens := niceyaml.NewSourceFromString(tc.before, niceyaml.WithName("a"))
			afterTokens := niceyaml.NewSourceFromString(tc.after, niceyaml.WithName("b"))

			result := diff.Diff(beforeTokens.Lines(), afterTokens.Lines())
			got := result.Hunks(tc.context)

			if tc.wantEmpty {
				assert.Nil(t, got)
				assert.Equal(t, 0, got.Len())
				assert.True(t, got.Lines().IsEmpty())

				return
			}

			assert.Equal(t, tc.wantLen, got.Len())

			for lineIdx, wantFlag := range tc.flags {
				assert.Equal(t, wantFlag, got.Flag(lineIdx), "flag mismatch at line %d", lineIdx)
			}

			if tc.annotations != nil {
				for lineIdx, wantAnnotation := range tc.annotations {
					anns := got.Annotations(lineIdx)
					require.NotEmpty(t, anns, "expected annotation at line %d", lineIdx)
					assert.Equal(t, wantAnnotation, anns[0].Content)
				}
			}
		})
	}
}

// opsAlgorithm is an [lcs.Algorithm] that returns a fixed op sequence.
type opsAlgorithm []lcs.Op

func (a opsAlgorithm) Diff(_, _ []string) []lcs.Op {
	return []lcs.Op(a)
}

func TestDiffer_WithAlgorithm(t *testing.T) {
	t.Parallel()

	before := niceyaml.NewSourceFromString("a: 1\nb: 2\n").Lines()
	after := niceyaml.NewSourceFromString("a: 1\nc: 3\n").Lines()

	tcs := map[string]struct {
		ops       []lcs.Op
		wantFlags []line.Flag
		wantPanic string
	}{
		"known kinds render in order": {
			ops: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 1},
			},
			wantFlags: []line.Flag{line.FlagDefault, line.FlagDeleted, line.FlagInserted},
		},
		"unknown kind panics instead of dropping the line": {
			ops: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpKind(3), Before: -1, After: 1},
			},
			wantPanic: "diff: op 1 has unknown lcs.OpKind 3",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := diff.New(diff.WithAlgorithm(opsAlgorithm(tc.ops)))

			if tc.wantPanic != "" {
				assert.PanicsWithValue(t, tc.wantPanic, func() {
					d.Diff(before, after)
				})

				return
			}

			got := d.Diff(before, after).Unified()
			require.Equal(t, len(tc.wantFlags), got.Len())

			for i, want := range tc.wantFlags {
				assert.Equal(t, want, got.Flag(i), "flag mismatch at line %d", i)
			}
		})
	}
}

func TestDiffer_IsEmpty(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		after  string
		want   bool
	}{
		"identical content": {
			before: "key: value\n",
			after:  "key: value\n",
			want:   false,
		},
		"both empty": {
			before: "",
			after:  "",
			want:   true,
		},
		"has changes": {
			before: "key: old\n",
			after:  "key: new\n",
			want:   false,
		},
		"insertion": {
			before: "",
			after:  "key: value\n",
			want:   false,
		},
		"deletion": {
			before: "key: value\n",
			after:  "",
			want:   false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			beforeTokens := niceyaml.NewSourceFromString(tc.before, niceyaml.WithName("a"))
			afterTokens := niceyaml.NewSourceFromString(tc.after, niceyaml.WithName("b"))

			result := diff.Diff(beforeTokens.Lines(), afterTokens.Lines())

			assert.Equal(t, tc.want, result.IsEmpty())
		})
	}
}

func TestDiffResult_Stats(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		before      string
		after       string
		wantAdded   int
		wantRemoved int
	}{
		"no changes": {
			before:      "a: 1\n",
			after:       "a: 1\n",
			wantAdded:   0,
			wantRemoved: 0,
		},
		"additions only": {
			before:      "a: 1\n",
			after:       "a: 1\nb: 2\n",
			wantAdded:   1,
			wantRemoved: 0,
		},
		"removals only": {
			before:      "a: 1\nb: 2\n",
			after:       "a: 1\n",
			wantAdded:   0,
			wantRemoved: 1,
		},
		"mixed changes": {
			before:      "a: 1\nb: 2\n",
			after:       "a: 1\nc: 3\n",
			wantAdded:   1,
			wantRemoved: 1,
		},
		"trailing blank line added": {
			before:      "a: 1\n",
			after:       "a: 1\n\n",
			wantAdded:   1,
			wantRemoved: 0,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			beforeSrc := niceyaml.NewSourceFromString(tt.before, niceyaml.WithName("a"))
			afterSrc := niceyaml.NewSourceFromString(tt.after, niceyaml.WithName("b"))

			result := diff.Diff(
				beforeSrc.Lines(),
				afterSrc.Lines(),
			)
			added, removed := result.Stats()
			assert.Equal(t, tt.wantAdded, added, "added count")
			assert.Equal(t, tt.wantRemoved, removed, "removed count")
		})
	}
}

func TestDiffResult_BeforeAfter(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before      string
		after       string
		wantBefore  []wantLine
		wantAfter   []wantLine
		wantRowLen  int
		description string
	}{
		"no changes": {
			before: "key: value\n",
			after:  "key: value\n",
			wantBefore: []wantLine{
				{content: "key: value", flag: line.FlagDefault},
			},
			wantAfter: []wantLine{
				{content: "key: value", flag: line.FlagDefault},
			},
			wantRowLen: 1,
		},
		"simple insertion": {
			before: "a: 1\n",
			after:  "a: 1\nb: 2\n",
			wantBefore: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "", flag: line.FlagDefault, empty: true}, // Placeholder.
			},
			wantAfter: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "b: 2", flag: line.FlagInserted},
			},
			wantRowLen: 2,
		},
		"simple deletion": {
			before: "a: 1\nb: 2\n",
			after:  "a: 1\n",
			wantBefore: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "b: 2", flag: line.FlagDeleted},
			},
			wantAfter: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "", flag: line.FlagDefault, empty: true}, // Placeholder.
			},
			wantRowLen: 2,
		},
		"modification": {
			before: "key: old\n",
			after:  "key: new\n",
			wantBefore: []wantLine{
				{content: "key: old", flag: line.FlagDeleted},
			},
			wantAfter: []wantLine{
				{content: "key: new", flag: line.FlagInserted},
			},
			wantRowLen: 1,
		},
		"consecutive insertions": {
			before: stringtest.Input(`
				before: 1
				after: 2
			`),
			after: stringtest.Input(`
				before: 1
				new1: a
				new2: b
				after: 2
			`),
			wantBefore: []wantLine{
				{content: "before: 1", flag: line.FlagDefault},
				{content: "", flag: line.FlagDefault, empty: true}, // Placeholder for new1.
				{content: "", flag: line.FlagDefault, empty: true}, // Placeholder for new2.
				{content: "after: 2", flag: line.FlagDefault},
			},
			wantAfter: []wantLine{
				{content: "before: 1", flag: line.FlagDefault},
				{content: "new1: a", flag: line.FlagInserted},
				{content: "new2: b", flag: line.FlagInserted},
				{content: "after: 2", flag: line.FlagDefault},
			},
			wantRowLen: 4,
		},
		"consecutive deletions": {
			before: stringtest.Input(`
				before: 1
				old1: a
				old2: b
				after: 2
			`),
			after: stringtest.Input(`
				before: 1
				after: 2
			`),
			wantBefore: []wantLine{
				{content: "before: 1", flag: line.FlagDefault},
				{content: "old1: a", flag: line.FlagDeleted},
				{content: "old2: b", flag: line.FlagDeleted},
				{content: "after: 2", flag: line.FlagDefault},
			},
			wantAfter: []wantLine{
				{content: "before: 1", flag: line.FlagDefault},
				{content: "", flag: line.FlagDefault, empty: true}, // Placeholder for old1.
				{content: "", flag: line.FlagDefault, empty: true}, // Placeholder for old2.
				{content: "after: 2", flag: line.FlagDefault},
			},
			wantRowLen: 4,
		},
		"mixed changes": {
			before: stringtest.Input(`
				keep1: a
				delete1: x
				delete2: y
				keep2: b
			`),
			after: stringtest.Input(`
				keep1: a
				insert1: p
				insert2: q
				keep2: b
			`),
			wantBefore: []wantLine{
				{content: "keep1: a", flag: line.FlagDefault},
				{content: "delete1: x", flag: line.FlagDeleted},
				{content: "delete2: y", flag: line.FlagDeleted},
				{content: "keep2: b", flag: line.FlagDefault},
			},
			wantAfter: []wantLine{
				{content: "keep1: a", flag: line.FlagDefault},
				{content: "insert1: p", flag: line.FlagInserted},
				{content: "insert2: q", flag: line.FlagInserted},
				{content: "keep2: b", flag: line.FlagDefault},
			},
			wantRowLen: 4,
		},
		"unbalanced delete insert": {
			// More deletes than inserts: extra rows for placeholders.
			before: stringtest.Input(`
				keep: 1
				del1: a
				del2: b
				del3: c
				end: 2
			`),
			after: stringtest.Input(`
				keep: 1
				ins1: x
				end: 2
			`),
			wantBefore: []wantLine{
				{content: "keep: 1", flag: line.FlagDefault},
				{content: "del1: a", flag: line.FlagDeleted},
				{content: "del2: b", flag: line.FlagDeleted},
				{content: "del3: c", flag: line.FlagDeleted},
				{content: "end: 2", flag: line.FlagDefault},
			},
			wantAfter: []wantLine{
				{content: "keep: 1", flag: line.FlagDefault},
				{content: "ins1: x", flag: line.FlagInserted},
				{content: "", flag: line.FlagDefault, empty: true}, // Placeholder.
				{content: "", flag: line.FlagDefault, empty: true}, // Placeholder.
				{content: "end: 2", flag: line.FlagDefault},
			},
			wantRowLen: 5,
		},
		"unbalanced insert delete": {
			// More inserts than deletes.
			before: stringtest.Input(`
				keep: 1
				del1: a
				end: 2
			`),
			after: stringtest.Input(`
				keep: 1
				ins1: x
				ins2: y
				ins3: z
				end: 2
			`),
			wantBefore: []wantLine{
				{content: "keep: 1", flag: line.FlagDefault},
				{content: "del1: a", flag: line.FlagDeleted},
				{content: "", flag: line.FlagDefault, empty: true}, // Placeholder.
				{content: "", flag: line.FlagDefault, empty: true}, // Placeholder.
				{content: "end: 2", flag: line.FlagDefault},
			},
			wantAfter: []wantLine{
				{content: "keep: 1", flag: line.FlagDefault},
				{content: "ins1: x", flag: line.FlagInserted},
				{content: "ins2: y", flag: line.FlagInserted},
				{content: "ins3: z", flag: line.FlagInserted},
				{content: "end: 2", flag: line.FlagDefault},
			},
			wantRowLen: 5,
		},
		"both empty": {
			before:     "",
			after:      "",
			wantBefore: nil,
			wantAfter:  nil,
			wantRowLen: 0,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			beforeSrc := niceyaml.NewSourceFromString(tc.before, niceyaml.WithName("a"))
			afterSrc := niceyaml.NewSourceFromString(tc.after, niceyaml.WithName("b"))

			result := diff.Diff(
				beforeSrc.Lines(),
				afterSrc.Lines(),
			)

			beforeView := result.Before()
			afterView := result.After()

			// Both views should have the same length.
			assert.Equal(t, tc.wantRowLen, beforeView.Len(), "Before Len()")
			assert.Equal(t, tc.wantRowLen, afterView.Len(), "After Len()")

			// Check IsEmpty.
			assert.Equal(t, tc.wantRowLen == 0, beforeView.Lines().IsEmpty(), "Before IsEmpty()")
			assert.Equal(t, tc.wantRowLen == 0, afterView.Lines().IsEmpty(), "After IsEmpty()")

			verifyLines(t, "Before", beforeView, tc.wantBefore)
			verifyLines(t, "After", afterView, tc.wantAfter)
		})
	}
}

// wantLine specifies expected line content and flag for testing.
type wantLine struct {
	content string
	flag    line.Flag
	empty   bool // True if this should be an empty placeholder (zero tokens).
}

// verifyLines checks that the lines of a view match expected lines.
func verifyLines(t *testing.T, side string, actual *line.View, want []wantLine) {
	t.Helper()

	require.Equal(t, len(want), actual.Len(), "%s: line count mismatch", side)

	for i, actualLn := range actual.AllLines() {
		wantLn := want[i]

		if wantLn.empty {
			// Empty placeholder: should have no tokens.
			assert.Empty(t, actualLn.Tokens(), "%s line %d: expected empty placeholder", side, i)
		} else {
			assert.Equal(t, wantLn.content, actualLn.Content(), "%s line %d content", side, i)
		}

		assert.Equal(t, wantLn.flag, actual.Flag(i), "%s line %d flag", side, i)
	}
}

func TestDiffer_MultipleRenders(t *testing.T) {
	t.Parallel()

	before := stringtest.Input(`
		line1: 1
		line2: 2
		line3: old
		line4: 4
		line5: 5
	`)
	after := stringtest.Input(`
		line1: 1
		line2: 2
		line3: new
		line4: 4
		line5: 5
	`)

	beforeTokens := niceyaml.NewSourceFromString(before, niceyaml.WithName("a"))
	afterTokens := niceyaml.NewSourceFromString(after, niceyaml.WithName("b"))

	result := diff.Diff(beforeTokens.Lines(), afterTokens.Lines())

	// Call Full multiple times.
	full1 := result.Unified()
	full2 := result.Unified()

	assert.Equal(t, full1.String(), full2.String())

	// Call Summary with different contexts.
	summary0 := result.Hunks(0)
	summary1 := result.Hunks(1)
	summary2 := result.Hunks(2)

	// All should have 1 hunk.
	assert.Equal(t, 1, hunkCount(summary0))
	assert.Equal(t, 1, hunkCount(summary1))
	assert.Equal(t, 1, hunkCount(summary2))

	// Different contexts should produce different hunk sizes.
	assert.Less(t, summary0.Len(), summary1.Len())
	assert.Less(t, summary1.Len(), summary2.Len())
}

func TestDiffResult_ViewsAreIndependent(t *testing.T) {
	t.Parallel()

	before := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("a"))
	after := niceyaml.NewSourceFromString("a: 1\nb: 3\n", niceyaml.WithName("b"))
	result := diff.Diff(before.Lines(), after.Lines())

	highlight := position.NewRange(position.New(0, 0), position.New(0, 1))

	t.Run("Unified", func(t *testing.T) {
		t.Parallel()

		first := result.Unified()
		first.AddOverlay(kind.GenericHighlight, highlight)

		second := result.Unified()
		assert.Empty(t, second.Overlays(0))
	})

	t.Run("Before and After", func(t *testing.T) {
		t.Parallel()

		left := result.Before()
		right := result.After()

		left.AddOverlay(kind.GenericHighlight, highlight)

		assert.Empty(t, right.Overlays(0))
		assert.Empty(t, result.Before().Overlays(0))
	})

	t.Run("Hunks without changes", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, diff.Diff(before.Lines(), before.Lines()).Hunks(1))
	})

	t.Run("inputs are untouched", func(t *testing.T) {
		t.Parallel()

		result.Unified().AddOverlay(kind.GenericHighlight, highlight)

		assert.Empty(t, before.View().Overlays(0))
		assert.Empty(t, after.View().Overlays(0))
	})
}

// hunkCount returns the number of hunks in a view from [diff.Result.Hunks],
// which is the number of lines carrying a hunk header above them.
func hunkCount(view *line.View) int {
	count := 0

	for i := range view.AllLines() {
		if len(view.Annotations(i).Filter(line.Above)) > 0 {
			count++
		}
	}

	return count
}
