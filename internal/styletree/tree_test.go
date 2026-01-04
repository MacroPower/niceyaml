package styletree_test

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml/internal/styletree"
)

func TestTree_Empty(t *testing.T) {
	t.Parallel()

	tree := styletree.New()

	assert.Equal(t, 0, tree.Len())
	assert.Nil(t, tree.Query(0))
	assert.Nil(t, tree.Query(100))
}

func TestTree_SingleInterval(t *testing.T) {
	t.Parallel()

	tree := styletree.New()
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("red"))
	tree.Insert(10, 20, &style)

	assert.Equal(t, 1, tree.Len())

	tests := map[string]struct {
		point int
		want  int
	}{
		"before interval":    {point: 5, want: 0},
		"at start":           {point: 10, want: 1},
		"inside":             {point: 15, want: 1},
		"at end (exclusive)": {point: 20, want: 0},
		"after interval":     {point: 25, want: 0},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := tree.Query(tc.point)
			assert.Len(t, result, tc.want)
		})
	}
}

func TestTree_OverlappingIntervals(t *testing.T) {
	t.Parallel()

	tree := styletree.New()
	style1 := lipgloss.NewStyle().Foreground(lipgloss.Color("red"))
	style2 := lipgloss.NewStyle().Foreground(lipgloss.Color("blue"))
	style3 := lipgloss.NewStyle().Foreground(lipgloss.Color("green"))

	// Insert overlapping intervals.
	tree.Insert(0, 30, &style1)  // [0, 30).
	tree.Insert(10, 20, &style2) // [10, 20).
	tree.Insert(15, 25, &style3) // [15, 25).

	assert.Equal(t, 3, tree.Len())

	tests := map[string]struct {
		point int
		want  int
	}{
		"point 5 (only style1)":        {point: 5, want: 1},
		"point 10 (style1, &style2)":   {point: 10, want: 2},
		"point 15 (all three)":         {point: 15, want: 3},
		"point 19 (all three)":         {point: 19, want: 3},
		"point 20 (style1, &style3)":   {point: 20, want: 2},
		"point 24 (style1, &style3)":   {point: 24, want: 2},
		"point 25 (only style1)":       {point: 25, want: 1},
		"point 29 (only style1)":       {point: 29, want: 1},
		"point 30 (none, end is excl)": {point: 30, want: 0},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := tree.Query(tc.point)
			assert.Len(t, result, tc.want)
		})
	}
}

func TestTree_InsertionOrder(t *testing.T) {
	t.Parallel()

	tree := styletree.New()

	// Insert styles in a specific order.
	style1 := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	style2 := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	style3 := lipgloss.NewStyle().Foreground(lipgloss.Color("3"))

	tree.Insert(0, 100, &style1)
	tree.Insert(0, 100, &style2)
	tree.Insert(0, 100, &style3)

	result := tree.Query(50)
	require.Len(t, result, 3)

	// Verify insertion order is preserved.
	assert.Equal(t, style1.GetForeground(), result[0].GetForeground())
	assert.Equal(t, style2.GetForeground(), result[1].GetForeground())
	assert.Equal(t, style3.GetForeground(), result[2].GetForeground())
}

func TestTree_Clear(t *testing.T) {
	t.Parallel()

	tree := styletree.New()
	style := lipgloss.NewStyle()

	tree.Insert(0, 10, &style)
	tree.Insert(5, 15, &style)
	tree.Insert(10, 20, &style)

	assert.Equal(t, 3, tree.Len())

	tree.Clear()

	assert.Equal(t, 0, tree.Len())
	assert.Nil(t, tree.Query(5))
}

func TestTree_DisjointIntervals(t *testing.T) {
	t.Parallel()

	tree := styletree.New()
	style := lipgloss.NewStyle()

	// Insert disjoint intervals.
	tree.Insert(0, 10, &style)
	tree.Insert(20, 30, &style)
	tree.Insert(40, 50, &style)

	tests := map[string]struct {
		point int
		want  int
	}{
		"in first":         {point: 5, want: 1},
		"between 1 and 2":  {point: 15, want: 0},
		"in second":        {point: 25, want: 1},
		"between 2 and 3":  {point: 35, want: 0},
		"in third":         {point: 45, want: 1},
		"after all":        {point: 55, want: 0},
		"at gap boundary":  {point: 10, want: 0},
		"at interval edge": {point: 20, want: 1},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := tree.Query(tc.point)
			assert.Len(t, result, tc.want)
		})
	}
}

func TestTree_LargeNumberOfIntervals(t *testing.T) {
	t.Parallel()

	tree := styletree.New()
	style := lipgloss.NewStyle()

	// Insert 1000 intervals.
	for i := range 1000 {
		tree.Insert(i*10, i*10+5, &style)
	}

	assert.Equal(t, 1000, tree.Len())

	// Query should still work correctly.
	assert.Empty(t, tree.Query(5005))  // Gap.
	assert.Len(t, tree.Query(5002), 1) // Inside interval 500.
}

func TestTree_NestedIntervals(t *testing.T) {
	t.Parallel()

	tree := styletree.New()
	style := lipgloss.NewStyle()

	// Nested like Russian dolls.
	tree.Insert(0, 100, &style)
	tree.Insert(10, 90, &style)
	tree.Insert(20, 80, &style)
	tree.Insert(30, 70, &style)
	tree.Insert(40, 60, &style)

	tests := map[string]struct {
		point int
		want  int
	}{
		"outermost only":  {point: 5, want: 1},
		"two levels":      {point: 15, want: 2},
		"three levels":    {point: 25, want: 3},
		"four levels":     {point: 35, want: 4},
		"all five levels": {point: 50, want: 5},
		"back to four":    {point: 65, want: 4},
		"back to three":   {point: 75, want: 3},
		"back to two":     {point: 85, want: 2},
		"back to one":     {point: 95, want: 1},
		"outside all":     {point: 100, want: 0},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := tree.Query(tc.point)
			assert.Len(t, result, tc.want)
		})
	}
}

func TestTree_SameStartDifferentEnd(t *testing.T) {
	t.Parallel()

	tree := styletree.New()
	style := lipgloss.NewStyle()

	tree.Insert(0, 10, &style)
	tree.Insert(0, 20, &style)
	tree.Insert(0, 30, &style)

	assert.Len(t, tree.Query(5), 3)
	assert.Len(t, tree.Query(15), 2)
	assert.Len(t, tree.Query(25), 1)
	assert.Empty(t, tree.Query(35))
}

func TestTree_ZeroLengthInterval(t *testing.T) {
	t.Parallel()

	tree := styletree.New()
	style := lipgloss.NewStyle()

	// Zero-length interval [10, 10) should never match anything.
	tree.Insert(10, 10, &style)

	assert.Equal(t, 1, tree.Len())
	assert.Empty(t, tree.Query(9))
	assert.Empty(t, tree.Query(10))
	assert.Empty(t, tree.Query(11))
}

func BenchmarkTree_Insert(b *testing.B) {
	style := lipgloss.NewStyle()

	for b.Loop() {
		tree := styletree.New()
		for j := range 100 {
			tree.Insert(j*100, j*100+50, &style)
		}
	}
}

func BenchmarkTree_Query(b *testing.B) {
	tree := styletree.New()
	style := lipgloss.NewStyle()

	// Insert 100 non-overlapping intervals.
	for i := range 100 {
		tree.Insert(i*100, i*100+50, &style)
	}

	for b.Loop() {
		_ = tree.Query(5025) // Middle of an interval.
	}
}

func BenchmarkTree_Query_Overlapping(b *testing.B) {
	tree := styletree.New()
	style := lipgloss.NewStyle()

	// Insert 100 overlapping intervals all containing point 500.
	for i := range 100 {
		tree.Insert(i*10, 1000, &style)
	}

	for b.Loop() {
		_ = tree.Query(500) // All intervals contain this.
	}
}

func TestTree_QueryRange_Empty(t *testing.T) {
	t.Parallel()

	tree := styletree.New()

	assert.Nil(t, tree.QueryRange(0, 100))
}

func TestTree_QueryRange_NoOverlap(t *testing.T) {
	t.Parallel()

	tree := styletree.New()
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("red"))

	tree.Insert(10, 20, &style)

	tests := map[string]struct {
		start int
		end   int
	}{
		"before interval": {start: 0, end: 5},
		"after interval":  {start: 25, end: 30},
		"adjacent start":  {start: 0, end: 10},  // [0,10) doesn't overlap [10,20).
		"adjacent end":    {start: 20, end: 25}, // [20,25) doesn't overlap [10,20).
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := tree.QueryRange(tc.start, tc.end)
			assert.Nil(t, result)
		})
	}
}

func TestTree_QueryRange_SingleInterval(t *testing.T) {
	t.Parallel()

	tree := styletree.New()
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("red"))

	tree.Insert(10, 20, &style)

	tests := map[string]struct {
		start int
		end   int
		want  int
	}{
		"exact match":        {start: 10, end: 20, want: 1},
		"overlap at start":   {start: 5, end: 15, want: 1},
		"overlap at end":     {start: 15, end: 25, want: 1},
		"contained in query": {start: 5, end: 25, want: 1},
		"query inside":       {start: 12, end: 18, want: 1},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := tree.QueryRange(tc.start, tc.end)
			assert.Len(t, result, tc.want)
		})
	}
}

func TestTree_QueryRange_MultipleIntervals(t *testing.T) {
	t.Parallel()

	tree := styletree.New()
	style1 := lipgloss.NewStyle().Foreground(lipgloss.Color("red"))
	style2 := lipgloss.NewStyle().Foreground(lipgloss.Color("blue"))
	style3 := lipgloss.NewStyle().Foreground(lipgloss.Color("green"))

	tree.Insert(0, 10, &style1)
	tree.Insert(20, 30, &style2)
	tree.Insert(40, 50, &style3)

	tests := map[string]struct {
		start int
		end   int
		want  int
	}{
		"first only":      {start: 0, end: 10, want: 1},
		"second only":     {start: 20, end: 30, want: 1},
		"third only":      {start: 40, end: 50, want: 1},
		"first and gap":   {start: 5, end: 15, want: 1},
		"gap only":        {start: 10, end: 20, want: 0},
		"first two":       {start: 0, end: 25, want: 2},
		"last two":        {start: 25, end: 55, want: 2},
		"all three":       {start: 0, end: 50, want: 3},
		"wide range":      {start: 0, end: 100, want: 3},
		"partial overlap": {start: 5, end: 45, want: 3},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := tree.QueryRange(tc.start, tc.end)
			assert.Len(t, result, tc.want)
		})
	}
}

func TestTree_QueryRange_InsertionOrder(t *testing.T) {
	t.Parallel()

	tree := styletree.New()

	style1 := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	style2 := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	style3 := lipgloss.NewStyle().Foreground(lipgloss.Color("3"))

	tree.Insert(0, 100, &style1)
	tree.Insert(0, 100, &style2)
	tree.Insert(0, 100, &style3)

	result := tree.QueryRange(25, 75)
	require.Len(t, result, 3)

	// Verify insertion order is preserved.
	assert.Equal(t, style1.GetForeground(), result[0].Style.GetForeground())
	assert.Equal(t, style2.GetForeground(), result[1].Style.GetForeground())
	assert.Equal(t, style3.GetForeground(), result[2].Style.GetForeground())
}

func TestTree_QueryRange_IntervalBoundaries(t *testing.T) {
	t.Parallel()

	tree := styletree.New()
	style := lipgloss.NewStyle()

	tree.Insert(10, 20, &style)

	result := tree.QueryRange(10, 20)
	require.Len(t, result, 1)

	// Verify interval boundaries are correct.
	assert.Equal(t, 10, result[0].Start)
	assert.Equal(t, 20, result[0].End)
}

func TestTree_QueryRange_OverlappingIntervals(t *testing.T) {
	t.Parallel()

	tree := styletree.New()
	style1 := lipgloss.NewStyle().Foreground(lipgloss.Color("red"))
	style2 := lipgloss.NewStyle().Foreground(lipgloss.Color("blue"))
	style3 := lipgloss.NewStyle().Foreground(lipgloss.Color("green"))

	// Overlapping intervals.
	tree.Insert(0, 30, &style1)  // [0, 30)
	tree.Insert(10, 20, &style2) // [10, 20)
	tree.Insert(15, 25, &style3) // [15, 25)

	tests := map[string]struct {
		start int
		end   int
		want  int
	}{
		"only style1":  {start: 0, end: 5, want: 1},
		"style1 and 2": {start: 5, end: 15, want: 2},
		"all three":    {start: 15, end: 20, want: 3},
		"style1 and 3": {start: 20, end: 25, want: 2},
		"back to one":  {start: 25, end: 30, want: 1},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := tree.QueryRange(tc.start, tc.end)
			assert.Len(t, result, tc.want)
		})
	}
}

func BenchmarkTree_QueryRange(b *testing.B) {
	tree := styletree.New()
	style := lipgloss.NewStyle()

	// Insert 100 non-overlapping intervals.
	for i := range 100 {
		tree.Insert(i*100, i*100+50, &style)
	}

	for b.Loop() {
		_ = tree.QueryRange(5000, 5100) // Covers about one interval.
	}
}

func BenchmarkTree_QueryRange_Wide(b *testing.B) {
	tree := styletree.New()
	style := lipgloss.NewStyle()

	// Insert 100 non-overlapping intervals.
	for i := range 100 {
		tree.Insert(i*100, i*100+50, &style)
	}

	for b.Loop() {
		_ = tree.QueryRange(0, 10000) // Covers all intervals.
	}
}
