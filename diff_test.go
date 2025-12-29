package niceyaml_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macropower/niceyaml"
)

func TestFullDiff_Lines(t *testing.T) {
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
			want: `   1 | a: 1
   2 | b: 2`,
		},
		"simple deletion": {
			before: "a: 1\nb: 2\n",
			after:  "a: 1\n",
			want: `   1 | a: 1
   2 | b: 2`,
		},
		"modification": {
			before: "key: old\n",
			after:  "key: new\n",
			want: `   1 | key: old
   1 | key: new`,
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
			want: `   1 | first: 1
   2 | second: 2
   2 | second: changed
   3 | third: 3`,
		},
		"change with surrounding context": {
			before: `header: value
unchanged1: foo
unchanged2: bar
middle: old
unchanged3: baz
unchanged4: qux
footer: end
`,
			after: `header: value
unchanged1: foo
unchanged2: bar
middle: new
unchanged3: baz
unchanged4: qux
footer: end
`,
			want: `   1 | header: value
   2 | unchanged1: foo
   3 | unchanged2: bar
   4 | middle: old
   4 | middle: new
   5 | unchanged3: baz
   6 | unchanged4: qux
   7 | footer: end`,
		},
		"multiple scattered changes": {
			before: `first: 1
second: 2
third: 3
fourth: 4
fifth: 5
`,
			after: `first: changed1
second: 2
third: changed3
fourth: 4
fifth: changed5
`,
			want: `   1 | first: 1
   1 | first: changed1
   2 | second: 2
   3 | third: 3
   3 | third: changed3
   4 | fourth: 4
   5 | fifth: 5
   5 | fifth: changed5`,
		},
		"consecutive insertions": {
			before: `before: 1
after: 2
`,
			after: `before: 1
new1: a
new2: b
new3: c
after: 2
`,
			want: `   1 | before: 1
   2 | new1: a
   3 | new2: b
   4 | new3: c
   5 | after: 2`,
		},
		"consecutive deletions": {
			before: `before: 1
old1: a
old2: b
old3: c
after: 2
`,
			after: `before: 1
after: 2
`,
			want: `   1 | before: 1
   2 | old1: a
   3 | old2: b
   4 | old3: c
   2 | after: 2`,
		},
		"nested yaml structure": {
			before: `metadata:
  name: myapp
  namespace: default
spec:
  replicas: 3
  template:
    image: nginx:1.19
`,
			after: `metadata:
  name: myapp
  namespace: production
spec:
  replicas: 5
  template:
    image: nginx:1.21
`,
			want: `   1 | metadata:
   2 |   name: myapp
   3 |   namespace: default
   3 |   namespace: production
   4 | spec:
   5 |   replicas: 3
   5 |   replicas: 5
   6 |   template:
   7 |     image: nginx:1.19
   7 |     image: nginx:1.21`,
		},
		"change at beginning": {
			before: `first: old
second: 2
third: 3
fourth: 4
`,
			after: `first: new
second: 2
third: 3
fourth: 4
`,
			want: `   1 | first: old
   1 | first: new
   2 | second: 2
   3 | third: 3
   4 | fourth: 4`,
		},
		"change at end": {
			before: `first: 1
second: 2
third: 3
fourth: old
`,
			after: `first: 1
second: 2
third: 3
fourth: new
`,
			want: `   1 | first: 1
   2 | second: 2
   3 | third: 3
   4 | fourth: old
   4 | fourth: new`,
		},
		"yaml with list items": {
			before: `items:
  - name: item1
    value: 100
  - name: item2
    value: 200
`,
			after: `items:
  - name: item1
    value: 150
  - name: item2
    value: 200
`,
			want: `   1 | items:
   2 |   - name: item1
   3 |     value: 100
   3 |     value: 150
   4 |   - name: item2
   5 |     value: 200`,
		},
		"insert and delete in same region": {
			before: `keep1: a
delete1: x
delete2: y
keep2: b
`,
			after: `keep1: a
insert1: p
insert2: q
keep2: b
`,
			want: `   1 | keep1: a
   2 | delete1: x
   3 | delete2: y
   2 | insert1: p
   3 | insert2: q
   4 | keep2: b`,
		},
		"large context around small change": {
			before: `line1: 1
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
`,
			after: `line1: 1
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
`,
			want: `   1 | line1: 1
   2 | line2: 2
   3 | line3: 3
   4 | line4: 4
   5 | line5: 5
   6 | line6: 6
   7 | line7: 7
   8 | line8: 8
   9 | line9: 9
  10 | line10: old
  10 | line10: new
  11 | line11: 11
  12 | line12: 12
  13 | line13: 13
  14 | line14: 14
  15 | line15: 15`,
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

			got := diff.Lines()

			assert.Equal(t, "a..b", got.Name)
			assert.Equal(t, tc.want, got.String())
		})
	}
}

func TestFullDiff_Lines_Flags(t *testing.T) {
	t.Parallel()

	t.Run("insertion gets flag", func(t *testing.T) {
		t.Parallel()

		before := "a: 1\n"
		after := "a: 1\nb: 2\n"

		beforeTokens := niceyaml.NewSourceFromString(before, niceyaml.WithName("a"))
		afterTokens := niceyaml.NewSourceFromString(after, niceyaml.WithName("b"))

		revA := niceyaml.NewRevision(beforeTokens)
		revB := niceyaml.NewRevision(afterTokens)
		diff := niceyaml.NewFullDiff(revA, revB)

		got := diff.Lines()

		// Count lines with non-default flags.
		flaggedCount := 0
		for _, line := range got.Lines() {
			if line.Flag != niceyaml.FlagDefault {
				flaggedCount++
			}
		}

		assert.Equal(t, 1, flaggedCount)
		assert.Equal(t, niceyaml.FlagInserted, got.Line(1).Flag)
	})

	t.Run("deletion gets flag", func(t *testing.T) {
		t.Parallel()

		before := "a: 1\nb: 2\n"
		after := "a: 1\n"

		beforeTokens := niceyaml.NewSourceFromString(before, niceyaml.WithName("a"))
		afterTokens := niceyaml.NewSourceFromString(after, niceyaml.WithName("b"))

		revA := niceyaml.NewRevision(beforeTokens)
		revB := niceyaml.NewRevision(afterTokens)
		diff := niceyaml.NewFullDiff(revA, revB)

		got := diff.Lines()

		// Count lines with non-default flags.
		flaggedCount := 0
		for _, line := range got.Lines() {
			if line.Flag != niceyaml.FlagDefault {
				flaggedCount++
			}
		}

		assert.Equal(t, 1, flaggedCount)
		assert.Equal(t, niceyaml.FlagDeleted, got.Line(1).Flag)
	})

	t.Run("modification has delete and insert flags", func(t *testing.T) {
		t.Parallel()

		before := "key: old\n"
		after := "key: new\n"

		beforeTokens := niceyaml.NewSourceFromString(before, niceyaml.WithName("a"))
		afterTokens := niceyaml.NewSourceFromString(after, niceyaml.WithName("b"))

		revA := niceyaml.NewRevision(beforeTokens)
		revB := niceyaml.NewRevision(afterTokens)
		diff := niceyaml.NewFullDiff(revA, revB)

		got := diff.Lines()

		// Count lines with non-default flags.
		flaggedCount := 0
		for _, line := range got.Lines() {
			if line.Flag != niceyaml.FlagDefault {
				flaggedCount++
			}
		}

		assert.Equal(t, 2, flaggedCount)

		// First line is delete.
		assert.Equal(t, niceyaml.FlagDeleted, got.Line(0).Flag)

		// Second line is insert.
		assert.Equal(t, niceyaml.FlagInserted, got.Line(1).Flag)
	})

	t.Run("only changed lines get flags", func(t *testing.T) {
		t.Parallel()

		before := `first: 1
second: 2
third: 3
`
		after := `first: 1
second: changed
third: 3
`

		beforeTokens := niceyaml.NewSourceFromString(before, niceyaml.WithName("a"))
		afterTokens := niceyaml.NewSourceFromString(after, niceyaml.WithName("b"))

		revA := niceyaml.NewRevision(beforeTokens)
		revB := niceyaml.NewRevision(afterTokens)
		diff := niceyaml.NewFullDiff(revA, revB)

		got := diff.Lines()

		// Only the changed lines (delete old second, insert new second) should have flags.
		flaggedCount := 0
		for _, line := range got.Lines() {
			if line.Flag != niceyaml.FlagDefault {
				flaggedCount++
			}
		}

		assert.Equal(t, 2, flaggedCount)

		// Line 1 is unchanged.
		assert.Equal(t, niceyaml.FlagDefault, got.Line(0).Flag)

		// Line 2 is delete.
		assert.Equal(t, niceyaml.FlagDeleted, got.Line(1).Flag)

		// Line 3 is insert.
		assert.Equal(t, niceyaml.FlagInserted, got.Line(2).Flag)

		// Line 4 is unchanged.
		assert.Equal(t, niceyaml.FlagDefault, got.Line(3).Flag)
	})
}

func TestSummaryDiff_Lines(t *testing.T) {
	t.Parallel()

	t.Run("context limits output", func(t *testing.T) {
		t.Parallel()

		before := `line1: 1
line2: 2
line3: 3
line4: 4
line5: old
line6: 6
line7: 7
line8: 8
line9: 9
`
		after := `line1: 1
line2: 2
line3: 3
line4: 4
line5: new
line6: 6
line7: 7
line8: 8
line9: 9
`
		want := `   4 | line4: 4
   4 | ^ @@ -4,3 +4,3 @@
   5 | line5: old
   5 | line5: new
   6 | line6: 6`

		beforeTokens := niceyaml.NewSourceFromString(before, niceyaml.WithName("a"))
		afterTokens := niceyaml.NewSourceFromString(after, niceyaml.WithName("b"))

		revA := niceyaml.NewRevision(beforeTokens)
		revB := niceyaml.NewRevision(afterTokens)
		diff := niceyaml.NewSummaryDiff(revA, revB, 1)

		// Context 1 should include: line4, line5 (delete), line5 (insert), line6.
		got := diff.Lines()

		assert.Equal(t, "a..b", got.Name)
		assert.Equal(t, 4, got.Count())
		assert.Equal(t, want, got.String())
	})

	t.Run("context 0 shows only changes", func(t *testing.T) {
		t.Parallel()

		before := `line1: 1
line2: 2
line3: old
line4: 4
line5: 5
`
		after := `line1: 1
line2: 2
line3: new
line4: 4
line5: 5
`
		want := `   3 | line3: old
   3 | ^ @@ -3 +3 @@
   3 | line3: new`

		beforeTokens := niceyaml.NewSourceFromString(before, niceyaml.WithName("a"))
		afterTokens := niceyaml.NewSourceFromString(after, niceyaml.WithName("b"))

		revA := niceyaml.NewRevision(beforeTokens)
		revB := niceyaml.NewRevision(afterTokens)
		diff := niceyaml.NewSummaryDiff(revA, revB, 0)

		got := diff.Lines()

		// Only the deleted and inserted lines.
		assert.Equal(t, 2, got.Count())
		assert.Equal(t, niceyaml.FlagDeleted, got.Line(0).Flag)
		assert.Equal(t, niceyaml.FlagInserted, got.Line(1).Flag)
		assert.Equal(t, want, got.String())
	})

	t.Run("hunk header in annotation", func(t *testing.T) {
		t.Parallel()

		before := `first: 1
second: 2
third: 3
`
		after := `first: 1
second: changed
third: 3
`
		want := `   1 | first: 1
   1 | ^ @@ -1,3 +1,3 @@
   2 | second: 2
   2 | second: changed
   3 | third: 3`

		beforeTokens := niceyaml.NewSourceFromString(before, niceyaml.WithName("a"))
		afterTokens := niceyaml.NewSourceFromString(after, niceyaml.WithName("b"))

		revA := niceyaml.NewRevision(beforeTokens)
		revB := niceyaml.NewRevision(afterTokens)
		diff := niceyaml.NewSummaryDiff(revA, revB, 1)

		got := diff.Lines()

		assert.Equal(t, want, got.String())

		// First line of hunk should have annotation.
		assert.Equal(t, "@@ -1,3 +1,3 @@", got.Line(0).Annotation.Content)

		// Other lines should not have annotation.
		for i := 1; i < got.Count(); i++ {
			assert.Empty(t, got.Line(i).Annotation.Content)
		}
	})

	t.Run("multiple hunks have separate annotations", func(t *testing.T) {
		t.Parallel()

		before := `line1: old1
line2: 2
line3: 3
line4: 4
line5: 5
line6: 6
line7: 7
line8: 8
line9: old9
`
		after := `line1: new1
line2: 2
line3: 3
line4: 4
line5: 5
line6: 6
line7: 7
line8: 8
line9: new9
`
		want := `   1 | line1: old1
   1 | ^ @@ -1,2 +1,2 @@
   1 | line1: new1
   2 | line2: 2
   8 | line8: 8
   8 | ^ @@ -8,2 +8,2 @@
   9 | line9: old9
   9 | line9: new9`

		beforeTokens := niceyaml.NewSourceFromString(before, niceyaml.WithName("a"))
		afterTokens := niceyaml.NewSourceFromString(after, niceyaml.WithName("b"))

		revA := niceyaml.NewRevision(beforeTokens)
		revB := niceyaml.NewRevision(afterTokens)
		diff := niceyaml.NewSummaryDiff(revA, revB, 1)

		// Context 1 should create two separate hunks.
		got := diff.Lines()

		assert.Equal(t, want, got.String())

		// First hunk starts at line 0.
		assert.Equal(t, "@@ -1,2 +1,2 @@", got.Line(0).Annotation.Content)

		// Second hunk starts at line 3 (after first hunk's 3 lines).
		assert.Equal(t, "@@ -8,2 +8,2 @@", got.Line(3).Annotation.Content)

		// Other lines should not have annotations.
		for pos, line := range got.Lines() {
			if pos.Line != 0 && pos.Line != 3 {
				assert.Empty(t, line.Annotation.Content)
			}
		}
	})

	t.Run("no changes returns empty tokens", func(t *testing.T) {
		t.Parallel()

		before := "key: value\n"
		after := "key: value\n"

		beforeTokens := niceyaml.NewSourceFromString(before, niceyaml.WithName("a"))
		afterTokens := niceyaml.NewSourceFromString(after, niceyaml.WithName("b"))

		revA := niceyaml.NewRevision(beforeTokens)
		revB := niceyaml.NewRevision(afterTokens)
		diff := niceyaml.NewSummaryDiff(revA, revB, 3)

		got := diff.Lines()

		assert.Equal(t, "a..b", got.Name)
		assert.True(t, got.IsEmpty())
		assert.Nil(t, got.Tokens())
	})

	t.Run("empty before", func(t *testing.T) {
		t.Parallel()

		before := ""
		after := "key: value\n"
		want := "   1 | key: value\n   1 | ^ @@ -0,0 +1 @@"

		beforeTokens := niceyaml.NewSourceFromString(before, niceyaml.WithName("a"))
		afterTokens := niceyaml.NewSourceFromString(after, niceyaml.WithName("b"))

		revA := niceyaml.NewRevision(beforeTokens)
		revB := niceyaml.NewRevision(afterTokens)
		diff := niceyaml.NewSummaryDiff(revA, revB, 3)

		got := diff.Lines()

		assert.Equal(t, "a..b", got.Name)
		assert.Equal(t, 1, got.Count())
		assert.Equal(t, niceyaml.FlagInserted, got.Line(0).Flag)
		assert.Equal(t, want, got.String())
	})

	t.Run("empty after", func(t *testing.T) {
		t.Parallel()

		before := "key: value\n"
		after := ""
		want := "   1 | key: value\n   1 | ^ @@ -1 +0,0 @@"

		beforeTokens := niceyaml.NewSourceFromString(before, niceyaml.WithName("a"))
		afterTokens := niceyaml.NewSourceFromString(after, niceyaml.WithName("b"))

		revA := niceyaml.NewRevision(beforeTokens)
		revB := niceyaml.NewRevision(afterTokens)
		diff := niceyaml.NewSummaryDiff(revA, revB, 3)

		got := diff.Lines()

		assert.Equal(t, "a..b", got.Name)
		assert.Equal(t, 1, got.Count())
		assert.Equal(t, niceyaml.FlagDeleted, got.Line(0).Flag)
		assert.Equal(t, want, got.String())
	})

	t.Run("negative context treated as zero", func(t *testing.T) {
		t.Parallel()

		before := `line1: 1
line2: old
line3: 3
`
		after := `line1: 1
line2: new
line3: 3
`
		want := `   2 | line2: old
   2 | ^ @@ -2 +2 @@
   2 | line2: new`

		beforeTokens := niceyaml.NewSourceFromString(before, niceyaml.WithName("a"))
		afterTokens := niceyaml.NewSourceFromString(after, niceyaml.WithName("b"))

		revA := niceyaml.NewRevision(beforeTokens)
		revB := niceyaml.NewRevision(afterTokens)
		diff := niceyaml.NewSummaryDiff(revA, revB, -5)

		got := diff.Lines()

		// Same as context 0: only changed lines.
		assert.Equal(t, 2, got.Count())
		assert.Equal(t, want, got.String())
	})
}
