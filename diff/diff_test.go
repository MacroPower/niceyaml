package diff_test

import (
	"cmp"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/diff/lcs"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

func TestDiffer_Views(t *testing.T) {
	t.Parallel()

	before := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	after := niceyaml.NewSourceFromString("a: 1\nb: 3\n")

	// The decoration of an input view stays on that view.
	decorated := before.View()
	decorated.AddOverlay(kind.GenericHighlight, yamltest.LineRange(1, 0, 1))
	decorated.Annotate(1, line.Annotation{Content: "note", Placement: line.Below})
	decorated.SetFlag(0, line.FlagInserted)

	result := diff.Diff(decorated, after.View())

	got := result.Unified()
	require.Equal(t, 3, got.Count())
	assert.Equal(t, map[int]line.Flag{1: line.FlagDeleted, 2: line.FlagInserted}, changedFlags(got))

	// A diff reads the lines of its inputs alone, so the result carries the
	// flags of the diff and no overlays or annotations.
	for _, view := range []*line.View{got, result.Before(), result.After()} {
		for i := range view.All() {
			assert.Empty(t, view.Overlays(i))
			assert.Empty(t, view.Annotations(i))
		}
	}

	assert.Equal(t, line.FlagDefault, result.Before().Flag(0))
	assert.Len(t, decorated.Overlays(1), 1)
	assert.Len(t, decorated.Annotations(1), 1)
	assert.Equal(t, line.FlagInserted, decorated.Flag(0))

	// The diff of a diff is a diff of the lines the view holds, and it
	// ignores the flags of the first diff.
	again := diff.Diff(got, got)
	assert.Equal(t, 3, again.Unified().Count())
	assert.False(t, again.Stats().Changed())
	assert.Equal(t, line.FlagDefault, again.Unified().Flag(1))
}

func TestDiffer_Views_LineNumbers(t *testing.T) {
	t.Parallel()

	// An unchanged line keeps the number it has in each revision, so an
	// insertion at the top of the after revision leaves the before view's
	// numbers alone.
	result := yamltest.Diff("a: 1\nb: 2\n", stringtest.LinesLF(
		"x: 0",
		"a: 1",
		"b: 2",
	))

	assert.Equal(t, []int{0, 1, 2}, yamltest.LineNumbers(result.Before()))
	assert.Equal(t, []int{1, 2, 3}, yamltest.LineNumbers(result.After()))
}

// sequenceFunc adapts an iterator to [line.Sequence], as an implementation
// from outside the line package.
type sequenceFunc iter.Seq2[int, *line.Line]

func (f sequenceFunc) All() iter.Seq2[int, *line.Line] {
	return iter.Seq2[int, *line.Line](f)
}

func TestDiffer_Diff_Sequences(t *testing.T) {
	t.Parallel()

	before := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"b: 2",
		"c: 3",
	))
	after := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"b: 9",
		"c: 3",
		"d: 4",
	))

	whole := stringtest.JoinLF(
		"   1 | a: 1",
		"   2 | b: 2",
		"   2 | b: 9",
		"   3 | c: 3",
		"   4 | d: 4",
	)
	afterAlone := stringtest.JoinLF(
		"   1 | a: 1",
		"   2 | b: 9",
		"   3 | c: 3",
		"   4 | d: 4",
	)

	tcs := map[string]struct {
		before    line.Sequence
		after     line.Sequence
		want      string
		wantStats diff.Stats
	}{
		"lines": {
			before:    before.Lines(),
			after:     after.Lines(),
			want:      whole,
			wantStats: diff.Stats{Added: 2, Removed: 1},
		},
		"views of every line": {
			before:    before.View(),
			after:     after.View(),
			want:      whole,
			wantStats: diff.Stats{Added: 2, Removed: 1},
		},
		"lines against a view": {
			before:    before.Lines(),
			after:     after.View(),
			want:      whole,
			wantStats: diff.Stats{Added: 2, Removed: 1},
		},
		"views of some lines": {
			before: before.View().Slice(position.NewSpan(1, 3)),
			after:  after.View().Slice(position.NewSpan(1, 3)),
			want: stringtest.JoinLF(
				"   2 | b: 2",
				"   2 | b: 9",
				"   3 | c: 3",
			),
			wantStats: diff.Stats{Added: 1, Removed: 1},
		},
		"views with a gap": {
			before: before.View().Slice(position.NewSpan(0, 1), position.NewSpan(2, 3)),
			after:  after.View().Slice(position.NewSpan(0, 1), position.NewSpan(2, 4)),
			want: stringtest.JoinLF(
				"   1 | a: 1",
				"   3 | c: 3",
				"   4 | d: 4",
			),
			wantStats: diff.Stats{Added: 1},
		},
		"another implementation": {
			before: sequenceFunc(func(yield func(int, *line.Line) bool) {
				_ = yield(0, before.Lines().Line(0)) && yield(2, before.Lines().Line(2))
			}),
			after:     after.Lines(),
			want:      afterAlone,
			wantStats: diff.Stats{Added: 2},
		},
		"nil sequences": {},
		"nil sequence against lines": {
			after:     after.Lines(),
			want:      afterAlone,
			wantStats: diff.Stats{Added: 4},
		},
		"nil view against a view": {
			before:    (*line.View)(nil),
			after:     after.View(),
			want:      afterAlone,
			wantStats: diff.Stats{Added: 4},
		},
		"nil pointer to lines": {
			before:    (*line.Lines)(nil),
			after:     after.Lines(),
			want:      afterAlone,
			wantStats: diff.Stats{Added: 4},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := diff.Diff(tc.before, tc.after)

			got := result.Unified()
			assert.Equal(t, tc.want, got.String())
			assert.Equal(t, tc.wantStats, result.Stats())
			assert.Equal(t, tc.want == "", result.IsEmpty())

			// The result shares its lines with the inputs.
			for _, l := range got.All() {
				_, inBefore := before.View().Index(l)
				_, inAfter := after.View().Index(l)

				assert.True(t, inBefore || inAfter, "line %d", l.Number())
			}
		})
	}
}

func TestDiffer_Full(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		after  string
		want   string
		// Holds the flag of each changed line by its index in the view.
		wantFlags map[int]line.Flag
		wantStats diff.Stats
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
			wantFlags: map[int]line.Flag{1: line.FlagInserted},
			wantStats: diff.Stats{Added: 1},
		},
		"simple deletion": {
			before: "a: 1\nb: 2\n",
			after:  "a: 1\n",
			want: stringtest.JoinLF(
				"   1 | a: 1",
				"   2 | b: 2",
			),
			wantFlags: map[int]line.Flag{1: line.FlagDeleted},
			wantStats: diff.Stats{Removed: 1},
		},
		"modification": {
			before: "key: old\n",
			after:  "key: new\n",
			want: stringtest.JoinLF(
				"   1 | key: old",
				"   1 | key: new",
			),
			wantFlags: map[int]line.Flag{0: line.FlagDeleted, 1: line.FlagInserted},
			wantStats: diff.Stats{Added: 1, Removed: 1},
		},
		"empty before": {
			before:    "",
			after:     "key: value\n",
			want:      "   1 | key: value",
			wantFlags: map[int]line.Flag{0: line.FlagInserted},
			wantStats: diff.Stats{Added: 1},
		},
		"empty after": {
			before:    "key: value\n",
			after:     "",
			want:      "   1 | key: value",
			wantFlags: map[int]line.Flag{0: line.FlagDeleted},
			wantStats: diff.Stats{Removed: 1},
		},
		"both empty": {
			before: "",
			after:  "",
			want:   "",
		},
		"multi-line modification": {
			before: stringtest.LinesLF(
				"first: 1",
				"second: 2",
				"third: 3",
			),
			after: stringtest.LinesLF(
				"first: 1",
				"second: changed",
				"third: 3",
			),
			want: stringtest.JoinLF(
				"   1 | first: 1",
				"   2 | second: 2",
				"   2 | second: changed",
				"   3 | third: 3",
			),
			wantFlags: map[int]line.Flag{1: line.FlagDeleted, 2: line.FlagInserted},
			wantStats: diff.Stats{Added: 1, Removed: 1},
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
			wantFlags: map[int]line.Flag{
				0: line.FlagDeleted,
				1: line.FlagInserted,
				3: line.FlagDeleted,
				4: line.FlagInserted,
				6: line.FlagDeleted,
				7: line.FlagInserted,
			},
			wantStats: diff.Stats{Added: 3, Removed: 3},
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
			wantFlags: map[int]line.Flag{1: line.FlagInserted, 2: line.FlagInserted, 3: line.FlagInserted},
			wantStats: diff.Stats{Added: 3},
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
			wantFlags: map[int]line.Flag{1: line.FlagDeleted, 2: line.FlagDeleted, 3: line.FlagDeleted},
			wantStats: diff.Stats{Removed: 3},
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
			wantFlags: map[int]line.Flag{
				2: line.FlagDeleted,
				3: line.FlagInserted,
				5: line.FlagDeleted,
				6: line.FlagInserted,
				8: line.FlagDeleted,
				9: line.FlagInserted,
			},
			wantStats: diff.Stats{Added: 3, Removed: 3},
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
			wantFlags: map[int]line.Flag{0: line.FlagDeleted, 1: line.FlagInserted},
			wantStats: diff.Stats{Added: 1, Removed: 1},
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
			wantFlags: map[int]line.Flag{3: line.FlagDeleted, 4: line.FlagInserted},
			wantStats: diff.Stats{Added: 1, Removed: 1},
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
			wantFlags: map[int]line.Flag{2: line.FlagDeleted, 3: line.FlagInserted},
			wantStats: diff.Stats{Added: 1, Removed: 1},
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
			wantFlags: map[int]line.Flag{
				1: line.FlagDeleted,
				2: line.FlagDeleted,
				3: line.FlagInserted,
				4: line.FlagInserted,
			},
			wantStats: diff.Stats{Added: 2, Removed: 2},
		},
		"trailing blank line added": {
			before: "a: 1\n",
			after:  "a: 1\n\n",
			want: stringtest.JoinLF(
				"   1 | a: 1",
				"   2 | ",
			),
			wantFlags: map[int]line.Flag{1: line.FlagInserted},
			wantStats: diff.Stats{Added: 1},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := yamltest.Diff(tc.before, tc.after)

			got := result.Unified()
			assert.Equal(t, tc.want, got.String())
			assert.Equal(t, tc.wantFlags, changedFlags(got))
			assert.Equal(t, tc.wantStats, result.Stats())
			assert.Equal(t, tc.wantStats != diff.Stats{}, result.Stats().Changed())
			assert.Equal(t, tc.want == "", result.IsEmpty())
		})
	}
}

func TestDiffer_Hunks(t *testing.T) {
	t.Parallel()

	tenLines := stringtest.Input(`
		l1: 1
		l2: 2
		l3: 3
		l4: 4
		l5: 5
		l6: 6
		l7: 7
		l8: 8
		l9: 9
		l10: 10
	`)
	// Deletes l2, inserts a line after l5, and changes l9.
	threeChanges := stringtest.Input(`
		l1: 1
		l3: 3
		l4: 4
		l5: 5
		new: x
		l6: 6
		l7: 7
		l8: 8
		l9: changed
		l10: 10
	`)

	fiveLines := stringtest.Input(`
		line1: 1
		line2: 2
		line3: old
		line4: 4
		line5: 5
	`)
	// Changes line3.
	oneChange := stringtest.Input(`
		line1: 1
		line2: 2
		line3: new
		line4: 4
		line5: 5
	`)

	tcs := map[string]struct {
		before      string
		after       string
		context     int
		wantLen     int
		flags       map[int]line.Flag
		annotations map[int]string
	}{
		"context 0 shows only changes": {
			before:      fiveLines,
			after:       oneChange,
			context:     0,
			wantLen:     2,
			flags:       map[int]line.Flag{2: line.FlagDeleted, 3: line.FlagInserted},
			annotations: map[int]string{2: "@@ -3 +3 @@"},
		},
		"context 1 adds a line on each side": {
			before:  fiveLines,
			after:   oneChange,
			context: 1,
			wantLen: 4,
			flags: map[int]line.Flag{
				1: line.FlagDefault,
				2: line.FlagDeleted,
				3: line.FlagInserted,
				4: line.FlagDefault,
			},
			annotations: map[int]string{1: "@@ -2,3 +2,3 @@"},
		},
		"context 2 adds two lines on each side": {
			before:  fiveLines,
			after:   oneChange,
			context: 2,
			wantLen: 6,
			flags: map[int]line.Flag{
				0: line.FlagDefault,
				1: line.FlagDefault,
				2: line.FlagDeleted,
				3: line.FlagInserted,
				4: line.FlagDefault,
				5: line.FlagDefault,
			},
			annotations: map[int]string{0: "@@ -1,5 +1,5 @@"},
		},
		"no changes returns empty": {
			before:  "key: value\n",
			after:   "key: value\n",
			context: 3,
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
			context:     -5,
			wantLen:     2,
			flags:       map[int]line.Flag{1: line.FlagDeleted, 2: line.FlagInserted},
			annotations: map[int]string{1: "@@ -2 +2 @@"},
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
			context:     0,
			wantLen:     2,
			flags:       map[int]line.Flag{3: line.FlagInserted, 4: line.FlagInserted},
			annotations: map[int]string{3: "@@ -3,0 +4,2 @@"},
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
			context:     0,
			wantLen:     1,
			flags:       map[int]line.Flag{1: line.FlagDeleted},
			annotations: map[int]string{1: "@@ -2 +1,0 @@"},
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
			context:     0,
			wantLen:     1,
			flags:       map[int]line.Flag{0: line.FlagInserted},
			annotations: map[int]string{0: "@@ -0,0 +1 @@"},
		},
		"insertion into empty file": {
			before:      "",
			after:       "a: 1\n",
			context:     0,
			wantLen:     1,
			flags:       map[int]line.Flag{0: line.FlagInserted},
			annotations: map[int]string{0: "@@ -0,0 +1 @@"},
		},
		"deletion to empty file": {
			before:      "a: 1\nb: 2\n",
			after:       "",
			context:     0,
			wantLen:     2,
			flags:       map[int]line.Flag{0: line.FlagDeleted, 1: line.FlagDeleted},
			annotations: map[int]string{0: "@@ -1,2 +0,0 @@"},
		},
		"delete trailing repeated list item": {
			before: stringtest.Input(`
				containers:
				  - name: app
				    image: nginx
				    ports:
				      - containerPort: 80
				  - name: sidecar
				    image: nginx
				    ports:
				      - containerPort: 80
			`),
			after: stringtest.Input(`
				containers:
				  - name: app
				    image: nginx
				    ports:
				      - containerPort: 80
			`),
			context: 0,
			wantLen: 4,
			flags: map[int]line.Flag{
				5: line.FlagDeleted,
				6: line.FlagDeleted,
				7: line.FlagDeleted,
				8: line.FlagDeleted,
			},
			annotations: map[int]string{5: "@@ -6,4 +5,0 @@"},
		},
		"insert trailing repeated list item": {
			before: stringtest.Input(`
				containers:
				  - name: app
				    image: nginx
				    ports:
				      - containerPort: 80
			`),
			after: stringtest.Input(`
				containers:
				  - name: app
				    image: nginx
				    ports:
				      - containerPort: 80
				  - name: sidecar
				    image: nginx
				    ports:
				      - containerPort: 80
			`),
			context: 0,
			wantLen: 4,
			flags: map[int]line.Flag{
				5: line.FlagInserted,
				6: line.FlagInserted,
				7: line.FlagInserted,
				8: line.FlagInserted,
			},
			annotations: map[int]string{5: "@@ -5,0 +6,4 @@"},
		},
		"three hunks without context": {
			before:  tenLines,
			after:   threeChanges,
			context: 0,
			wantLen: 4,
			flags: map[int]line.Flag{
				1:  line.FlagDeleted,
				5:  line.FlagInserted,
				9:  line.FlagDeleted,
				10: line.FlagInserted,
			},
			annotations: map[int]string{
				1: "@@ -2 +1,0 @@",
				5: "@@ -5,0 +5 @@",
				9: "@@ -9 +9 @@",
			},
		},
		"three hunks with context": {
			before:  tenLines,
			after:   threeChanges,
			context: 1,
			wantLen: 10,
			flags: map[int]line.Flag{
				0:  line.FlagDefault,
				1:  line.FlagDeleted,
				2:  line.FlagDefault,
				4:  line.FlagDefault,
				5:  line.FlagInserted,
				6:  line.FlagDefault,
				8:  line.FlagDefault,
				9:  line.FlagDeleted,
				10: line.FlagInserted,
				11: line.FlagDefault,
			},
			annotations: map[int]string{
				0: "@@ -1,3 +1,2 @@",
				4: "@@ -5,2 +4,3 @@",
				8: "@@ -8,3 +8,3 @@",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := yamltest.Diff(tc.before, tc.after)
			got := result.Hunks(tc.context)

			// The hunks keep the indices of the unified view.
			require.NotNil(t, got)
			assertSameContent(t, result.Unified(), got)

			assert.Equal(t, tc.wantLen, got.Count())

			for lineIdx, wantFlag := range tc.flags {
				assert.True(t, got.Contains(lineIdx), "line %d not held", lineIdx)
				assert.Equal(t, wantFlag, got.Flag(lineIdx), "flag mismatch at line %d", lineIdx)
			}

			for lineIdx, wantAnnotation := range tc.annotations {
				anns := got.Annotations(lineIdx)
				require.NotEmpty(t, anns, "expected annotation at line %d", lineIdx)
				assert.Equal(t, wantAnnotation, anns[0].Content)
			}

			assert.Len(t, hunkHeaders(got), len(tc.annotations), "one header for each hunk")

			if tc.wantLen == 0 {
				// The empty view takes decoration as any other.
				got.AddOverlay(kind.GenericHighlight, yamltest.LineRange(0, 0, 1))
				assert.Empty(t, got.String())
			}
		})
	}
}

func TestDiffResult_HunksOf(t *testing.T) {
	t.Parallel()

	// The diff changes l2 of fourteen lines, so the unified view shows the
	// before and after l2 on rows 1 and 2, and l12 on row 12.
	lines := make([]string, 0, 14)
	for n := 1; n <= 14; n++ {
		lines = append(lines, fmt.Sprintf("l%d: %d", n, n))
	}

	before := stringtest.LinesLF(lines...)

	lines[1] = "l2: changed"
	after := stringtest.LinesLF(lines...)

	// The func errorAt marks the columns cols of line i of the after
	// revision with an error, as a validation of that revision does.
	errorAt := func(i int, cols position.Span) func(result *diff.Result, beforeView, afterView *line.View) *line.View {
		return func(result *diff.Result, beforeView, afterView *line.View) *line.View {
			afterView.Annotate(i, yamltest.ErrorAnnotation(cols, "bad"))

			view := result.Unified()
			result.Carry(view, beforeView, afterView)

			return view
		}
	}

	tcs := map[string]struct {
		view    func(result *diff.Result, beforeView, afterView *line.View) *line.View
		want    string
		context int
	}{
		"unified view gives the hunks of the changes": {
			view: func(result *diff.Result, _, _ *line.View) *line.View { return result.Unified() },
			want: stringtest.JoinLF(
				"     | @@ -1,3 +1,3 @@",
				"   1 | l1: 1",
				"   2 | l2: 2",
				"   2 | l2: changed",
				"   3 | l3: 3",
			),
			context: 1,
		},
		"error on an unchanged row gets a hunk of its own": {
			view: errorAt(11, position.NewSpan(5, 7)),
			want: stringtest.JoinLF(
				"     | @@ -1,3 +1,3 @@",
				"   1 | l1: 1",
				"   2 | l2: 2",
				"   2 | l2: changed",
				"   3 | l3: 3",
				"     | @@ -11,3 +11,3 @@",
				"  11 | l11: 11",
				"  12 | l12: 12",
				"     |      ^^ bad",
				"  13 | l13: 13",
			),
			context: 1,
		},
		"error with no context": {
			view: errorAt(11, position.NewSpan(5, 7)),
			want: stringtest.JoinLF(
				"     | @@ -2 +2 @@",
				"   2 | l2: 2",
				"   2 | l2: changed",
				"     | @@ -12 +12 @@",
				"  12 | l12: 12",
				"     |      ^^ bad",
			),
		},
		"error near a change joins its hunk": {
			view: errorAt(3, position.NewSpan(4, 5)),
			want: stringtest.JoinLF(
				"     | @@ -1,5 +1,5 @@",
				"   1 | l1: 1",
				"   2 | l2: 2",
				"   2 | l2: changed",
				"   3 | l3: 3",
				"   4 | l4: 4",
				"     |     ^ bad",
				"   5 | l5: 5",
			),
			context: 1,
		},
		"header stays above a carried note": {
			view: func(result *diff.Result, beforeView, afterView *line.View) *line.View {
				afterView.Annotate(9, line.Annotation{Content: "note", Placement: line.Above})

				view := result.Unified()
				result.Carry(view, beforeView, afterView)

				return view
			},
			want: stringtest.JoinLF(
				"     | @@ -2 +2 @@",
				"   2 | l2: 2",
				"   2 | l2: changed",
				"     | @@ -10 +10 @@",
				"     | note",
				"  10 | l10: 10",
			),
		},
		"hunks of hunks match the hunks": {
			view: func(result *diff.Result, beforeView, afterView *line.View) *line.View {
				return result.HunksOf(errorAt(11, position.NewSpan(5, 7))(result, beforeView, afterView), 1)
			},
			want: stringtest.JoinLF(
				"     | @@ -1,3 +1,3 @@",
				"   1 | l1: 1",
				"   2 | l2: 2",
				"   2 | l2: changed",
				"   3 | l3: 3",
				"     | @@ -11,3 +11,3 @@",
				"  11 | l11: 11",
				"  12 | l12: 12",
				"     |      ^^ bad",
				"  13 | l13: 13",
			),
			context: 1,
		},
		"context stops at the rows the view leaves out": {
			view: func(result *diff.Result, _, _ *line.View) *line.View {
				return result.Hunks(1)
			},
			want: stringtest.JoinLF(
				"     | @@ -1,3 +1,3 @@",
				"   1 | l1: 1",
				"   2 | l2: 2",
				"   2 | l2: changed",
				"   3 | l3: 3",
			),
			context: 3,
		},
		// The slice leaves out row 1, the deleted l2, so the context of the
		// inserted l2 stops there and leaves l1 out. Each header counts
		// the lines that its hunk holds.
		"a slice of two spans stops at the gap": {
			view: func(result *diff.Result, _, _ *line.View) *line.View {
				return result.Unified().Slice(position.NewSpan(0, 1), position.NewSpan(2, 6))
			},
			want: stringtest.JoinLF(
				"     | @@ -3,3 +2,4 @@",
				"   2 | l2: changed",
				"   3 | l3: 3",
				"   4 | l4: 4",
				"   5 | l5: 5",
			),
			context: 3,
		},
		"a slice without the inserted row": {
			view: func(result *diff.Result, _, _ *line.View) *line.View {
				return result.Unified().Slice(position.NewSpan(0, 2), position.NewSpan(3, 6))
			},
			want: stringtest.JoinLF(
				"     | @@ -1,2 +1 @@",
				"   1 | l1: 1",
				"   2 | l2: 2",
			),
			context: 3,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			beforeView := niceyaml.NewSourceFromString(before).View()
			afterView := niceyaml.NewSourceFromString(after).View()
			result := diff.Diff(beforeView, afterView)

			view := tc.view(result, beforeView, afterView)
			assert.Equal(t, tc.want, result.HunksOf(view, tc.context).String())
		})
	}
}

func TestDiffResult_HunksOf_KeepsClip(t *testing.T) {
	t.Parallel()

	long := "k: " + strings.Repeat("x", 60)

	tcs := map[string]struct {
		before, after string
		// The row of the unified view that shows the long line. An error
		// marks a column near its end.
		row int
	}{
		"long row inside a hunk": {
			before: stringtest.LinesLF("a: 1", long),
			after:  stringtest.LinesLF("a: 2", long),
			row:    2,
		},
		// The hunk header sits above the long row, and the clip does not
		// center a window on it.
		"long row that starts a hunk": {
			before: stringtest.LinesLF("a: 1", "b: 2", "c: 3", long),
			after:  stringtest.LinesLF("a: 2", "b: 2", "c: 3", long),
			row:    4,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := yamltest.Diff(tc.before, tc.after)

			view := result.Unified()
			view.Annotate(tc.row, yamltest.ErrorAnnotation(position.NewSpan(50, 51), "bad"))

			clipped := view.Clip(20)
			hunks := result.HunksOf(clipped, 0)

			assert.Equal(t, 20, hunks.ClipCols())
			assert.Equal(t, clipped.Windows(tc.row), hunks.Windows(tc.row))
			assert.Equal(t, result.HunksOf(view, 0).Clip(20).String(), hunks.String())
		})
	}
}

func TestDiffResult_HunksOf_Panics(t *testing.T) {
	t.Parallel()

	result := yamltest.Diff("a: 1\n", "a: 2\n")
	other := niceyaml.NewSourceFromString("a: 1\n").View()

	require.Panics(t, func() { result.HunksOf(other, 1) })
}

func TestDiffResult_HunksKeepUnifiedIndices(t *testing.T) {
	t.Parallel()

	before := stringtest.LinesLF(
		"a: 1",
		"b: 2",
		"c: 3",
		"d: 4",
		"e: 5",
	)

	tcs := map[string]struct {
		after    string
		mark     int
		wantHeld []int
	}{
		"changes": {
			after: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: 6",
			),
			mark:     5,
			wantHeld: []int{3, 4, 5},
		},
		"no changes": {
			after: before,
			mark:  4,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := yamltest.Diff(before, tc.after)

			unified := result.Unified()
			got := result.Hunks(1)
			assertSameContent(t, unified, got)

			var held []int

			for i, l := range got.All() {
				held = append(held, i)

				assert.Same(t, unified.Line(i), l, "line %d", i)
			}

			assert.Equal(t, tc.wantHeld, held)

			// A unified index marks the same line in the hunks.
			got.AddOverlay(kind.GenericHighlight, yamltest.LineRange(tc.mark, 0, 1))

			for i := range unified.All() {
				if i == tc.mark {
					assert.NotEmpty(t, got.Overlays(i), "line %d", i)
				} else {
					assert.Empty(t, got.Overlays(i), "line %d", i)
				}
			}
		})
	}
}

func TestDiffer_HunksOfPartOfASource(t *testing.T) {
	t.Parallel()

	// The second document starts after the separator on line 4, so its
	// body lines are 5, 6, and 7 in the file.
	head := stringtest.LinesLF(
		"x: 1",
		"y: 2",
		"z: 3",
	)
	body := stringtest.LinesLF(
		"a: 1",
		"b: 2",
		"c: 3",
	)
	changed := stringtest.LinesLF(
		"a: 1",
		"b: 9",
		"c: 3",
	)

	tcs := map[string]struct {
		// Holds the second document of the before side, when it is not body.
		before string
		// Holds the first document of the after side, when it is not head.
		afterHead string
		after     string
		context   int
		want      []string
	}{
		"modify one line": {
			after: changed,
			want:  []string{"@@ -6 +6 @@"},
		},
		"insert after the separator": {
			after: stringtest.LinesLF(
				"w: 0",
				"a: 1",
				"b: 2",
				"c: 3",
			),
			want: []string{"@@ -4,0 +5 @@"},
		},
		"append at end": {
			after: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
			),
			want: []string{"@@ -7,0 +8 @@"},
		},
		"delete the last line": {
			after: stringtest.LinesLF(
				"a: 1",
				"b: 2",
			),
			want: []string{"@@ -7 +6,0 @@"},
		},
		"two separate hunks": {
			before: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: 5",
			),
			after: stringtest.LinesLF(
				"a: 0",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: 0",
			),
			want: []string{"@@ -5 +5 @@", "@@ -9 +9 @@"},
		},
		"context 1": {
			after:   changed,
			context: 1,
			want:    []string{"@@ -5,3 +5,3 @@"},
		},
		"earlier document grew": {
			afterHead: head + stringtest.LinesLF("v: 4", "u: 5"),
			after:     changed,
			want:      []string{"@@ -6 +8 @@"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before := niceyaml.NewSourceFromString(head + "---\n" + cmp.Or(tc.before, body))
			after := niceyaml.NewSourceFromString(cmp.Or(tc.afterHead, head) + "---\n" + tc.after)

			beforeDocs := yamltest.Documents(t, before, 2)
			afterDocs := yamltest.Documents(t, after, 2)

			got := diff.Diff(beforeDocs[1].View(), afterDocs[1].View()).Hunks(tc.context)
			assert.Equal(t, tc.want, hunkHeaders(got))

			// With the same first document, a diff of the whole file
			// names the same lines.
			if tc.afterHead == "" {
				whole := diff.Diff(before.Lines(), after.Lines()).Hunks(tc.context)
				assert.Equal(t, tc.want, hunkHeaders(whole))
			}
		})
	}
}

func TestDiffer_HunksOfHeldLines(t *testing.T) {
	t.Parallel()

	// One before side holds lines 4-6 of a six-line file.
	sixLines := stringtest.LinesLF(
		"x: 1",
		"y: 2",
		"z: 3",
		"a: 1",
		"b: 2",
		"c: 3",
	)
	lastThree := []position.Span{position.NewSpan(3, 6)}
	// The other holds lines 1-2 and 6-7 of a seven-line file.
	sevenLines := stringtest.LinesLF(
		"a: 1",
		"b: 2",
		"c: 3",
		"d: 4",
		"e: 5",
		"f: 6",
		"g: 7",
	)
	gapped := []position.Span{position.NewSpan(0, 2), position.NewSpan(5, 7)}

	tcs := map[string]struct {
		before      string
		beforeSpans []position.Span
		after       string
		afterSpans  []position.Span
		context     int
		want        []string
		// Each change falls within the spans, so a diff of the whole file
		// names the same lines.
		whole bool
	}{
		"insert before the first held line": {
			before:      sixLines,
			beforeSpans: lastThree,
			after: stringtest.LinesLF(
				"x: 1",
				"y: 2",
				"z: 3",
				"w: 0",
				"a: 1",
				"b: 2",
				"c: 3",
			),
			afterSpans: []position.Span{position.NewSpan(3, 7)},
			want:       []string{"@@ -3,0 +4 @@"},
			whole:      true,
		},
		"delete the first held line": {
			before:      sixLines,
			beforeSpans: lastThree,
			after: stringtest.LinesLF(
				"x: 1",
				"y: 2",
				"z: 3",
				"b: 2",
				"c: 3",
			),
			afterSpans: []position.Span{position.NewSpan(3, 5)},
			want:       []string{"@@ -4 +3,0 @@"},
			whole:      true,
		},
		"context stops at the gap": {
			before:      sevenLines,
			beforeSpans: gapped,
			after: stringtest.LinesLF(
				"a: 1",
				"b: 9",
				"c: 3",
				"d: 4",
				"e: 5",
				"f: 6",
				"g: 8",
			),
			afterSpans: gapped,
			context:    1,
			want:       []string{"@@ -1,2 +1,2 @@", "@@ -6,2 +6,2 @@"},
		},
		"no context past the gap": {
			before:      sevenLines,
			beforeSpans: gapped,
			after: stringtest.LinesLF(
				"a: 1",
				"b: 9",
				"c: 3",
				"d: 4",
				"e: 5",
				"f: 6",
				"g: 7",
			),
			afterSpans: gapped,
			context:    1,
			want:       []string{"@@ -1,2 +1,2 @@"},
		},
		"changes on both sides of the gap": {
			// The run of changes deletes lines 2 and 6 before it
			// inserts them, so it splits wherever a side skips lines.
			before:      sevenLines,
			beforeSpans: gapped,
			after: stringtest.LinesLF(
				"a: 1",
				"b: 9",
				"c: 3",
				"d: 4",
				"e: 5",
				"f: 9",
				"g: 7",
			),
			afterSpans: gapped,
			want:       []string{"@@ -2 +1,0 @@", "@@ -6 +2 @@", "@@ -6,0 +6 @@"},
		},
		"insert after the gap": {
			// Line 6 of the after side starts its part, so the insert
			// comes before line 6 of the before side, not after line 2.
			before:      sevenLines,
			beforeSpans: gapped,
			after: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: 5",
				"x: 0",
				"f: 6",
				"g: 7",
			),
			afterSpans: []position.Span{position.NewSpan(0, 2), position.NewSpan(5, 8)},
			want:       []string{"@@ -5,0 +6 @@"},
			whole:      true,
		},
		"delete after the gap": {
			// Line 6 of the before side starts its part, so the delete
			// comes before line 6 of the after side, not after line 2.
			before:      sevenLines,
			beforeSpans: gapped,
			after: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: 5",
				"g: 7",
			),
			afterSpans: []position.Span{position.NewSpan(0, 2), position.NewSpan(5, 6)},
			want:       []string{"@@ -6 +5,0 @@"},
			whole:      true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before := niceyaml.NewSourceFromString(tc.before)
			after := niceyaml.NewSourceFromString(tc.after)
			beforeView := before.View().Slice(tc.beforeSpans...)
			afterView := after.View().Slice(tc.afterSpans...)

			got := diff.Diff(beforeView, afterView).Hunks(tc.context)
			assert.Equal(t, tc.want, hunkHeaders(got))

			// The lines a view holds diff as the view does.
			held := diff.Diff(beforeView.Held(), afterView.Held()).Hunks(tc.context)
			assert.Equal(t, tc.want, hunkHeaders(held))

			if tc.whole {
				whole := diff.Diff(before.Lines(), after.Lines()).Hunks(tc.context)
				assert.Equal(t, tc.want, hunkHeaders(whole))
			}
		})
	}
}

func TestDiffer_HunksFallBackToPositions(t *testing.T) {
	t.Parallel()

	src := niceyaml.NewSourceFromString("a: 1\n").Lines()

	tcs := map[string]struct {
		before line.Lines
		after  line.Lines
		// Holds the algorithm of the diff, when it is not the default.
		algo lcs.Algorithm
		want []string
	}{
		"line with no number": {
			before: line.Collect(&line.Line{}),
			after:  line.Collect(),
			want:   []string{"@@ -1 +0,0 @@"},
		},
		"algorithm that repeats a line": {
			before: src,
			after:  src,
			algo: opsAlgorithm{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpDelete, Before: 0, After: -1},
			},
			want: []string{"@@ -3 +2,0 @@"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := diff.New(diff.WithAlgorithm(tc.algo)).Diff(tc.before, tc.after).Hunks(0)
			assert.Equal(t, tc.want, hunkHeaders(got))
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

	before, after := "a: 1\nb: 2\n", "a: 1\nc: 3\n"

	// The fixed ops and the default algorithm pair the lines alike.
	want := stringtest.JoinLF(
		"   1 | a: 1",
		"   2 | b: 2",
		"   2 | c: 3",
	)
	wantFlags := map[int]line.Flag{1: line.FlagDeleted, 2: line.FlagInserted}

	tcs := map[string]struct {
		algo      lcs.Algorithm
		wantPanic string
	}{
		"known kinds render in order": {
			algo: opsAlgorithm{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 1},
			},
		},
		"unknown kind panics instead of dropping the line": {
			algo: opsAlgorithm{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpKind(3), Before: -1, After: 1},
			},
			wantPanic: "diff: op 1 has unknown lcs.OpKind 3",
		},
		// A nil algorithm falls back to the default.
		"untyped nil":         {algo: nil},
		"nil *lcs.Hirschberg": {algo: (*lcs.Hirschberg)(nil)},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opt := diff.WithAlgorithm(tc.algo)

			if tc.wantPanic != "" {
				assert.PanicsWithValue(t, tc.wantPanic, func() {
					yamltest.Diff(before, after, opt)
				})

				return
			}

			got := yamltest.Diff(before, after, opt).Unified()
			assert.Equal(t, want, got.String())
			assert.Equal(t, wantFlags, changedFlags(got))
		})
	}
}

func TestDiff_Concurrent(t *testing.T) {
	t.Parallel()

	before := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"b: 2",
		"c: 3",
		"d: 4",
	)).Lines()
	after := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"b: 5",
		"c: 3",
		"e: 6",
		"d: 4",
	)).Lines()
	want := diff.New().Diff(before, after).Unified()
	wantFlags := changedFlags(want)

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			for range 50 {
				got := diff.Diff(before, after).Unified()
				if !assert.Equal(t, want.Count(), got.Count()) {
					return
				}

				assert.Equal(t, want.String(), got.String())
				assert.Equal(t, wantFlags, changedFlags(got))
			}
		})
	}

	wg.Wait()
}

func TestDiffResult_BeforeAfter(t *testing.T) {
	t.Parallel()

	oneChange := struct{ before, after string }{"a: 1\nb: 2\n", "a: 1\nb: 3\n"}
	twoChanges := struct{ before, after string }{
		stringtest.LinesLF(
			"a: 1",
			"b: 2",
			"c: 3",
		),
		stringtest.LinesLF(
			"a: 1",
			"b: 3",
			"c: 4",
		),
	}

	tcs := map[string]struct {
		before string
		after  string
		// Holds the algorithm of the diff, when it is not the default.
		algo       lcs.Algorithm
		wantBefore []wantLine
		wantAfter  []wantLine
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
		},
		"simple insertion": {
			before: "a: 1\n",
			after:  "a: 1\nb: 2\n",
			wantBefore: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				placeholder,
			},
			wantAfter: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "b: 2", flag: line.FlagInserted},
			},
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
				placeholder,
			},
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
				placeholder,
				placeholder,
				{content: "after: 2", flag: line.FlagDefault},
			},
			wantAfter: []wantLine{
				{content: "before: 1", flag: line.FlagDefault},
				{content: "new1: a", flag: line.FlagInserted},
				{content: "new2: b", flag: line.FlagInserted},
				{content: "after: 2", flag: line.FlagDefault},
			},
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
				placeholder,
				placeholder,
				{content: "after: 2", flag: line.FlagDefault},
			},
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
		},
		"unbalanced delete insert": {
			// More deletes than inserts, so the extra rows are placeholders.
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
				placeholder,
				placeholder,
				{content: "end: 2", flag: line.FlagDefault},
			},
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
				placeholder,
				placeholder,
				{content: "end: 2", flag: line.FlagDefault},
			},
			wantAfter: []wantLine{
				{content: "keep: 1", flag: line.FlagDefault},
				{content: "ins1: x", flag: line.FlagInserted},
				{content: "ins2: y", flag: line.FlagInserted},
				{content: "ins3: z", flag: line.FlagInserted},
				{content: "end: 2", flag: line.FlagDefault},
			},
		},
		"both empty": {
			before:     "",
			after:      "",
			wantBefore: nil,
			wantAfter:  nil,
		},
		// An algorithm may return the deletes and inserts of one change in
		// any order, and the views still pair them row by row.
		"delete then insert": {
			before: oneChange.before,
			after:  oneChange.after,
			algo: opsAlgorithm{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 1},
			},
			wantBefore: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "b: 2", flag: line.FlagDeleted},
			},
			wantAfter: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "b: 3", flag: line.FlagInserted},
			},
		},
		"insert then delete": {
			before: oneChange.before,
			after:  oneChange.after,
			algo: opsAlgorithm{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpInsert, Before: -1, After: 1},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
			},
			wantBefore: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "b: 2", flag: line.FlagDeleted},
			},
			wantAfter: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "b: 3", flag: line.FlagInserted},
			},
		},
		"interleaved deletes and inserts": {
			before: twoChanges.before,
			after:  twoChanges.after,
			algo: opsAlgorithm{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 1},
				{Kind: lcs.OpDelete, Before: 2, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 2},
			},
			wantBefore: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "b: 2", flag: line.FlagDeleted},
				{content: "c: 3", flag: line.FlagDeleted},
			},
			wantAfter: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "b: 3", flag: line.FlagInserted},
				{content: "c: 4", flag: line.FlagInserted},
			},
		},
		"inserts ahead of deletes": {
			before: twoChanges.before,
			after:  twoChanges.after,
			algo: opsAlgorithm{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpInsert, Before: -1, After: 1},
				{Kind: lcs.OpInsert, Before: -1, After: 2},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
				{Kind: lcs.OpDelete, Before: 2, After: -1},
			},
			wantBefore: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "b: 2", flag: line.FlagDeleted},
				{content: "c: 3", flag: line.FlagDeleted},
			},
			wantAfter: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "b: 3", flag: line.FlagInserted},
				{content: "c: 4", flag: line.FlagInserted},
			},
		},
		"more inserts than deletes": {
			before: oneChange.before,
			after:  twoChanges.after,
			algo: opsAlgorithm{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpInsert, Before: -1, After: 1},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 2},
			},
			wantBefore: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "b: 2", flag: line.FlagDeleted},
				placeholder,
			},
			wantAfter: []wantLine{
				{content: "a: 1", flag: line.FlagDefault},
				{content: "b: 3", flag: line.FlagInserted},
				{content: "c: 4", flag: line.FlagInserted},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := yamltest.Diff(tc.before, tc.after, diff.WithAlgorithm(tc.algo))

			beforeView := result.Before()
			afterView := result.After()

			assert.Equal(t, beforeView.Count(), afterView.Count())
			assert.Equal(t, len(tc.wantBefore) == 0, beforeView.Held().IsEmpty(), "Before IsEmpty()")
			assert.Equal(t, len(tc.wantAfter) == 0, afterView.Held().IsEmpty(), "After IsEmpty()")

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

// placeholder is the line a side holds opposite a line that only the other
// side has.
var placeholder = wantLine{empty: true}

// verifyLines checks that the lines of a view match expected lines.
func verifyLines(t *testing.T, side string, actual *line.View, want []wantLine) {
	t.Helper()

	require.Equal(t, len(want), actual.Count(), "%s: line count mismatch", side)

	for i, actualLn := range actual.All() {
		wantLn := want[i]

		if wantLn.empty {
			// An empty placeholder has no tokens.
			assert.Empty(t, actualLn.Tokens(), "%s line %d: expected empty placeholder", side, i)
		} else {
			assert.Equal(t, wantLn.content, actualLn.Content(), "%s line %d content", side, i)
		}

		assert.Equal(t, wantLn.flag, actual.Flag(i), "%s line %d flag", side, i)
	}
}

func TestDiffResult_PlaceholdersAreDistinct(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		after  string
	}{
		"opposite standalone inserts": {
			before: "a: 1\n",
			after: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
			),
		},
		"opposite inserts paired with a delete": {
			before: "a: 1\n",
			after: stringtest.LinesLF(
				"x: 1",
				"b: 2",
				"c: 3",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			beforeSrc := niceyaml.NewSourceFromString(tc.before)
			afterSrc := niceyaml.NewSourceFromString(tc.after)

			left := diff.Diff(beforeSrc.Lines(), afterSrc.Lines()).Before()
			require.Equal(t, 3, left.Count())

			// Rows 1 and 2 are the placeholders opposite the two lines the
			// other pane added. Each names the one row it fills, so a
			// decorator that holds one marks that row alone.
			i, ok := left.Index(left.Line(1))
			require.True(t, ok)
			assert.Equal(t, 1, i)

			i, ok = left.Index(left.Line(2))
			require.True(t, ok)
			assert.Equal(t, 2, i)

			// A placeholder of one diff belongs to no view of another.
			other := diff.Diff(beforeSrc.Lines(), afterSrc.Lines()).Before()
			_, ok = other.Index(left.Line(1))
			assert.False(t, ok)
		})
	}
}

func TestDiffResult_ViewsAreIndependent(t *testing.T) {
	t.Parallel()

	before := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("a"))
	after := niceyaml.NewSourceFromString("a: 1\nb: 3\n", niceyaml.WithName("b"))
	result := diff.Diff(before.Lines(), after.Lines())

	highlight := yamltest.LineRange(0, 0, 1)

	t.Run("Unified", func(t *testing.T) {
		t.Parallel()

		first := result.Unified()
		text := first.String()
		first.AddOverlay(kind.GenericHighlight, highlight)
		assert.Len(t, first.Overlays(0), 1)

		second := result.Unified()
		assert.Empty(t, second.Overlays(0))
		assert.Equal(t, text, second.String())
	})

	t.Run("Before and After", func(t *testing.T) {
		t.Parallel()

		left := result.Before()
		right := result.After()

		left.AddOverlay(kind.GenericHighlight, highlight)

		assert.Empty(t, right.Overlays(0))
		assert.Empty(t, result.Before().Overlays(0))
	})
}

// originViews returns the two revisions the origin tests compare. Each
// view skips the lines above the change, two in the before revision and
// one in the after revision. The index of a line in the content of its
// input thus differs from its position among the lines the view holds.
//
// The unified diff and the panes show these lines:
//
//	row  unified    before     after
//	0    a: 1 (A1)  a: 1 (B2)  a: 1 (A1)
//	1    b: 2 (B3)  b: 2 (B3)  b: 9 (A2)
//	2    b: 9 (A2)  c: 3 (B4)  c: 3 (A3)
//	3    c: 3 (A3)  d: 4 (B5)  e: 5 (A4)
//	4    d: 4 (B5)  (filler)   f: 6 (A5)
//	5    e: 5 (A4)
//	6    f: 6 (A5)
func originViews() (*line.View, *line.View) {
	before := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"x: 0",
		"y: 0",
		"a: 1",
		"b: 2",
		"c: 3",
		"d: 4",
	)).View().Slice(position.NewSpan(2, 6))
	after := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"x: 0",
		"a: 1",
		"b: 9",
		"c: 3",
		"e: 5",
		"f: 6",
	)).View().Slice(position.NewSpan(1, 6))

	return before, after
}

func TestDiffResult_UnifiedOrigin(t *testing.T) {
	t.Parallel()

	before, after := originViews()
	result := diff.Diff(before, after)
	unified := result.Unified()
	hunks := result.Hunks(0)

	tcs := map[string]struct {
		want   diff.Origin
		row    int
		wantOK bool
	}{
		"unchanged line shows the after line": {row: 0, want: diff.Origin{Side: diff.After, Index: 1}, wantOK: true},
		"deleted line":                        {row: 1, want: diff.Origin{Side: diff.Before, Index: 3}, wantOK: true},
		"inserted line":                       {row: 2, want: diff.Origin{Side: diff.After, Index: 2}, wantOK: true},
		"line after a change":                 {row: 3, want: diff.Origin{Side: diff.After, Index: 3}, wantOK: true},
		"deleted line of a second change":     {row: 4, want: diff.Origin{Side: diff.Before, Index: 5}, wantOK: true},
		"last row":                            {row: 6, want: diff.Origin{Side: diff.After, Index: 5}, wantOK: true},
		"row before the view":                 {row: -1},
		"row past the view":                   {row: 7},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := result.UnifiedOrigin(tc.row)
			require.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)

			if !ok {
				return
			}

			// The index reaches the line the row shows through the input
			// view, and the row of the hunks shows the same line.
			input := before
			if got.Side == diff.After {
				input = after
			}

			assert.Same(t, input.Line(got.Index), unified.Line(tc.row))

			if hunks.Contains(tc.row) {
				assert.Same(t, unified.Line(tc.row), hunks.Line(tc.row))
			}

			// The row maps back to itself.
			row, ok := result.UnifiedRow(got)
			require.True(t, ok)
			assert.Equal(t, tc.row, row)
		})
	}
}

func TestDiffResult_PaneOrigin(t *testing.T) {
	t.Parallel()

	before, after := originViews()
	result := diff.Diff(before, after)

	tcs := map[string]struct {
		want   diff.Origin
		side   diff.Side
		row    int
		wantOK bool
	}{
		"unchanged line on the left": {
			side:   diff.Before,
			row:    0,
			want:   diff.Origin{Side: diff.Before, Index: 2},
			wantOK: true,
		},
		"unchanged line on the right": {
			side:   diff.After,
			row:    0,
			want:   diff.Origin{Side: diff.After, Index: 1},
			wantOK: true,
		},
		"deleted line": {
			side:   diff.Before,
			row:    1,
			want:   diff.Origin{Side: diff.Before, Index: 3},
			wantOK: true,
		},
		"inserted line": {
			side:   diff.After,
			row:    1,
			want:   diff.Origin{Side: diff.After, Index: 2},
			wantOK: true,
		},
		"line beside a placeholder": {
			side:   diff.After,
			row:    4,
			want:   diff.Origin{Side: diff.After, Index: 5},
			wantOK: true,
		},
		"placeholder":              {side: diff.Before, row: 4},
		"row past the pane":        {side: diff.After, row: 5},
		"row before the pane":      {side: diff.Before, row: -1},
		"side of neither revision": {side: diff.After + 1, row: 0},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := result.PaneOrigin(tc.side, tc.row)
			require.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)

			if !ok {
				return
			}

			input, pane := before, result.Before()
			if tc.side == diff.After {
				input, pane = after, result.After()
			}

			assert.Same(t, input.Line(got.Index), pane.Line(tc.row))

			row, ok := result.PaneRow(got)
			require.True(t, ok)
			assert.Equal(t, tc.row, row)
		})
	}
}

func TestDiffResult_UnifiedRow(t *testing.T) {
	t.Parallel()

	before, after := originViews()
	result := diff.Diff(before, after)

	tcs := map[string]struct {
		origin diff.Origin
		want   int
		wantOK bool
	}{
		// The unified view shows an unchanged line as the after line, so
		// the before line maps to the row of the after line.
		"unchanged before line":        {origin: diff.Origin{Side: diff.Before, Index: 2}, want: 0, wantOK: true},
		"unchanged after line":         {origin: diff.Origin{Side: diff.After, Index: 1}, want: 0, wantOK: true},
		"unchanged line after changes": {origin: diff.Origin{Side: diff.Before, Index: 4}, want: 3, wantOK: true},
		"deleted line":                 {origin: diff.Origin{Side: diff.Before, Index: 5}, want: 4, wantOK: true},
		"inserted line":                {origin: diff.Origin{Side: diff.After, Index: 4}, want: 5, wantOK: true},
		"line the input skips":         {origin: diff.Origin{Side: diff.Before, Index: 0}},
		"index past the content":       {origin: diff.Origin{Side: diff.After, Index: 6}},
		"side of neither revision":     {origin: diff.Origin{Side: diff.After + 1, Index: 1}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := result.UnifiedRow(tc.origin)
			require.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDiffResult_PaneRow(t *testing.T) {
	t.Parallel()

	before, after := originViews()
	result := diff.Diff(before, after)

	tcs := map[string]struct {
		origin diff.Origin
		want   int
		wantOK bool
	}{
		"unchanged before line":    {origin: diff.Origin{Side: diff.Before, Index: 2}, want: 0, wantOK: true},
		"deleted line":             {origin: diff.Origin{Side: diff.Before, Index: 5}, want: 3, wantOK: true},
		"unchanged after line":     {origin: diff.Origin{Side: diff.After, Index: 3}, want: 2, wantOK: true},
		"inserted line":            {origin: diff.Origin{Side: diff.After, Index: 5}, want: 4, wantOK: true},
		"line the input skips":     {origin: diff.Origin{Side: diff.After, Index: 0}},
		"index before the content": {origin: diff.Origin{Side: diff.Before, Index: -1}},
		"side of neither revision": {origin: diff.Origin{Side: diff.After + 1, Index: 1}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := result.PaneRow(tc.origin)
			require.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDiffResult_Origins_SharedLines(t *testing.T) {
	t.Parallel()

	// Both revisions hold the same lines, and the after revision moves the
	// first line to the end, so the unified diff shows that line twice.
	src := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"b: 2",
		"c: 3",
	)).Lines()
	before := line.Collect(src.Line(0), src.Line(1), src.Line(2))
	after := line.Collect(src.Line(1), src.Line(2), src.Line(0))

	result := diff.Diff(before, after)
	unified := result.Unified()
	require.Equal(t, 4, unified.Count())

	// Each row names a line of one revision and maps back to itself, though
	// two rows show one line pointer.
	seen := map[diff.Origin]bool{}

	for row := range unified.All() {
		origin, ok := result.UnifiedOrigin(row)
		require.True(t, ok)
		assert.False(t, seen[origin], "row %d repeats %v", row, origin)

		seen[origin] = true

		got, ok := result.UnifiedRow(origin)
		require.True(t, ok)
		assert.Equal(t, row, got)
	}

	deleted, ok := result.UnifiedRow(diff.Origin{Side: diff.Before, Index: 0})
	require.True(t, ok)

	inserted, ok := result.UnifiedRow(diff.Origin{Side: diff.After, Index: 2})
	require.True(t, ok)

	assert.Same(t, unified.Line(deleted), unified.Line(inserted))
	assert.NotEqual(t, deleted, inserted)
	assert.Equal(t, line.FlagDeleted, unified.Flag(deleted))
	assert.Equal(t, line.FlagInserted, unified.Flag(inserted))

	// The marks of each revision land on the row of its own copy.
	beforeView, afterView := line.NewView(before), line.NewView(after)
	beforeView.Annotate(0, line.Annotation{Content: "before"})
	afterView.Annotate(2, line.Annotation{Content: "after"})

	result.Carry(unified, beforeView, afterView)
	assert.Equal(t, []string{"before"}, unified.Annotations(deleted).Contents())
	assert.Equal(t, []string{"after"}, unified.Annotations(inserted).Contents())
}

func TestDiffResult_Origins_RepeatedLine(t *testing.T) {
	t.Parallel()

	// An algorithm that lists one line more than once maps the line to the
	// first row that shows it.
	src := niceyaml.NewSourceFromString("a: 1\n").Lines()
	result := diff.New(diff.WithAlgorithm(opsAlgorithm{
		{Kind: lcs.OpEqual, Before: 0, After: 0},
		{Kind: lcs.OpEqual, Before: 0, After: 0},
		{Kind: lcs.OpDelete, Before: 0, After: -1},
	})).Diff(src, src)

	origin, ok := result.UnifiedOrigin(2)
	require.True(t, ok)
	assert.Equal(t, diff.Origin{Side: diff.Before, Index: 0}, origin)

	row, ok := result.UnifiedRow(origin)
	require.True(t, ok)
	assert.Equal(t, 0, row)

	row, ok = result.PaneRow(origin)
	require.True(t, ok)
	assert.Equal(t, 0, row)
}

func TestDiffResult_Carry(t *testing.T) {
	t.Parallel()

	note := func(content string) line.Annotation {
		return line.Annotation{Content: content, Kind: kind.TextError, Placement: line.Below}
	}

	// Marked returns the views of originViews with marks on lines of each
	// revision: the unchanged line, a deleted or an inserted line, and a
	// line the input view does not hold.
	marked := func() (*line.View, *line.View) {
		before, after := originViews()

		before.Annotate(2, note("a before"))
		before.Annotate(3, note("b deleted"))
		before.AddOverlay(kind.GenericHighlight, yamltest.LineRange(3, 0, 1))
		before.Annotate(0, note("x skipped"))

		after.Annotate(1, note("a after"))
		after.Annotate(4, note("e inserted"))
		after.AddOverlay(kind.GenericHighlight, yamltest.LineRange(4, 0, 1))

		return before, after
	}

	before, after := marked()
	result := diff.Diff(before, after)

	tcs := map[string]struct {
		dst          *line.View
		wantNotes    map[int][]string
		wantOverlays map[int]int
	}{
		// The unified view shows the unchanged line as the after line, so
		// it carries the marks of the after revision alone.
		"unified": {
			dst:          result.Unified(),
			wantNotes:    map[int][]string{0: {"a after"}, 1: {"b deleted"}, 5: {"e inserted"}},
			wantOverlays: map[int]int{1: 1, 5: 1},
		},
		// The marks follow the hunk header on the first line of a hunk.
		"hunks": {
			dst: result.Hunks(0),
			wantNotes: map[int][]string{
				1: {"@@ -4 +3 @@", "b deleted"},
				4: {"@@ -6 +5,2 @@"},
				5: {"e inserted"},
			},
			wantOverlays: map[int]int{1: 1, 5: 1},
		},
		"before pane": {
			dst:          result.Before(),
			wantNotes:    map[int][]string{0: {"a before"}, 1: {"b deleted"}},
			wantOverlays: map[int]int{1: 1},
		},
		"after pane": {
			dst:          result.After(),
			wantNotes:    map[int][]string{0: {"a after"}, 3: {"e inserted"}},
			wantOverlays: map[int]int{3: 1},
		},
		"slice of a pane": {
			dst:          result.After().Slice(position.NewSpan(2, 5)),
			wantNotes:    map[int][]string{3: {"e inserted"}},
			wantOverlays: map[int]int{3: 1},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			flags := changedFlags(tc.dst)

			result.Carry(tc.dst, before, after)

			assert.Equal(t, tc.wantNotes, notesOf(tc.dst))
			assert.Equal(t, tc.wantOverlays, overlayCounts(tc.dst))
			assert.Equal(t, flags, changedFlags(tc.dst), "Carry keeps the flags of the diff")
		})
	}

	t.Run("marks added after the diff", func(t *testing.T) {
		t.Parallel()

		// Carry reads the input views when it runs, so it carries a mark
		// added after the diff. Another view over the content of an input
		// gives its marks as the input view does.
		plain, other := originViews()
		result := diff.Diff(plain, other)

		plain.Annotate(3, note("b deleted"))

		clone := other.Clone()
		clone.Annotate(4, note("e inserted"))

		dst := result.Unified()
		result.Carry(dst, plain, clone)

		assert.Equal(t, map[int][]string{1: {"b deleted"}, 5: {"e inserted"}}, notesOf(dst))
	})

	t.Run("carrying again", func(t *testing.T) {
		t.Parallel()

		// A row keeps each mark once, so a second Carry adds none.
		dst := result.Hunks(0)
		result.Carry(dst, before, after)
		result.Carry(dst, before, after)

		assert.Equal(t, map[int][]string{
			1: {"@@ -4 +3 @@", "b deleted"},
			4: {"@@ -6 +5,2 @@"},
			5: {"e inserted"},
		}, notesOf(dst))
		assert.Equal(t, map[int]int{1: 1, 5: 1}, overlayCounts(dst))
	})

	t.Run("source that leaves out a line", func(t *testing.T) {
		t.Parallel()

		// The slice leaves out the deleted line at index 3 of before, so
		// the row of that line takes no marks from it, though the slice
		// carries one there.
		part := before.Slice(position.NewSpan(4, 6))
		part.Annotate(3, note("b left out"))

		dst := result.Unified()
		result.Carry(dst, part, after)

		assert.Equal(t, map[int][]string{0: {"a after"}, 5: {"e inserted"}}, notesOf(dst))
		assert.Equal(t, map[int]int{5: 1}, overlayCounts(dst))
	})

	t.Run("views of the other revision", func(t *testing.T) {
		t.Parallel()

		// The two revisions hold distinct lines, so each view holds a line
		// of its own revision at the index of a line of the other, and
		// gives no marks for it.
		dst := result.Unified()
		result.Carry(dst, after, before)

		assert.Nil(t, notesOf(dst))
		assert.Nil(t, overlayCounts(dst))
	})

	t.Run("inputs that share lines", func(t *testing.T) {
		t.Parallel()

		// Both inputs are views of one source, so the unified view and the
		// before pane show the same line pointers. Each still takes the
		// marks of its own revision.
		src := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2"))
		left, right := src.View(), src.View()
		left.Annotate(0, note("before"))
		right.Annotate(0, note("after"))

		result := diff.Diff(left, right)
		unified, beforePane, afterPane := result.Unified(), result.Before(), result.After()

		for _, dst := range []*line.View{unified, beforePane, afterPane} {
			result.Carry(dst, left, right)
		}

		assert.Equal(t, map[int][]string{0: {"after"}}, notesOf(unified))
		assert.Equal(t, map[int][]string{0: {"before"}}, notesOf(beforePane))
		assert.Equal(t, map[int][]string{0: {"after"}}, notesOf(afterPane))
	})

	t.Run("nil views", func(t *testing.T) {
		t.Parallel()

		dst := result.Unified()
		result.Carry(dst, nil, nil)
		assert.Nil(t, notesOf(dst))

		assert.NotPanics(t, func() { result.Carry(nil, before, after) })
	})

	t.Run("empty diff", func(t *testing.T) {
		t.Parallel()

		empty := diff.Diff(line.Lines{}, line.Lines{})
		dst := empty.Unified()

		assert.NotPanics(t, func() { empty.Carry(dst, before, after) })
		assert.Equal(t, 0, dst.Count())
	})

	t.Run("view of other content", func(t *testing.T) {
		t.Parallel()

		other := diff.Diff(before, after)

		for _, dst := range []*line.View{before, other.Unified(), other.Before()} {
			assert.PanicsWithValue(t, "diff: Carry: dst is not a view of this Result", func() {
				result.Carry(dst, before, after)
			})
		}
	})
}

// notesOf returns the contents of the annotations of each line of view
// that carries some, by the index of the line. It returns nil when no line
// carries an annotation.
func notesOf(view *line.View) map[int][]string {
	var notes map[int][]string

	for i := range view.All() {
		anns := view.Annotations(i)
		if len(anns) == 0 {
			continue
		}

		if notes == nil {
			notes = map[int][]string{}
		}

		notes[i] = anns.Contents()
	}

	return notes
}

// overlayCounts returns the number of overlays on each line of view that
// carries some, by the index of the line. It returns nil when no line
// carries an overlay.
func overlayCounts(view *line.View) map[int]int {
	var counts map[int]int

	for i := range view.All() {
		n := len(view.Overlays(i))
		if n == 0 {
			continue
		}

		if counts == nil {
			counts = map[int]int{}
		}

		counts[i] = n
	}

	return counts
}

// changedFlags returns the flag of each line of view that is not
// [line.FlagDefault], by the index of the line. It returns nil when every
// line has the default flag.
func changedFlags(view *line.View) map[int]line.Flag {
	var flags map[int]line.Flag

	for i := range view.All() {
		flag := view.Flag(i)
		if flag == line.FlagDefault {
			continue
		}

		if flags == nil {
			flags = map[int]line.Flag{}
		}

		flags[i] = flag
	}

	return flags
}

// hunkHeaders returns the hunk headers of a view from [diff.Result.Hunks]
// in the order the view holds them.
func hunkHeaders(view *line.View) []string {
	var headers []string

	for i := range view.All() {
		for _, ann := range view.Annotations(i) {
			if ann.Kind == kind.UIHunkHeader {
				headers = append(headers, ann.Content)
			}
		}
	}

	return headers
}

// assertSameContent asserts that got is a view over the content unified
// holds in full, so every index of unified reaches the same line through
// got, and the content of got ends where unified ends.
func assertSameContent(t *testing.T, unified, got *line.View) {
	t.Helper()

	for i, l := range unified.All() {
		assert.Same(t, l, got.Line(i), "line %d", i)
	}

	assert.Panics(t, func() { got.Line(unified.Count()) }, "the content ends where unified ends")
}

func TestSide_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		side diff.Side
	}{
		"before":       {side: diff.Before, want: "before"},
		"after":        {side: diff.After, want: "after"},
		"out of range": {side: diff.Side(7), want: "Side(7)"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.side.String())
		})
	}
}

func TestOrigin_Print(t *testing.T) {
	t.Parallel()

	// An Origin prints its side by name, as a log or a failed test shows
	// it.
	assert.Equal(t, "{after 3}", fmt.Sprint(diff.Origin{Side: diff.After, Index: 3}))
}
