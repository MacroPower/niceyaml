package styletree

import "charm.land/lipgloss/v2"

// Tree is an augmented AVL tree for efficient interval stabbing queries.
// It stores half-open intervals [start, end) with associated styles and supports
// O(log n + k) queries to find all styles whose intervals contain a given point.
// Query results are returned in insertion order for predictable style composition.
type Tree struct {
	root *node
	size int
	seq  int // Next insertion sequence number.
}

// New creates a new empty interval tree.
func New() *Tree {
	return &Tree{}
}

// Len returns the number of intervals in the tree.
func (t *Tree) Len() int {
	return t.size
}

// Clear removes all intervals from the tree.
func (t *Tree) Clear() {
	t.root = nil
	t.size = 0
	t.seq = 0
}

// Insert adds an interval [start, end) with the associated style to the tree.
// Intervals are ordered by their start position for BST properties.
func (t *Tree) Insert(start, end int, style *lipgloss.Style) {
	t.root = t.insertNode(t.root, start, end, t.seq, style)
	t.seq++
	t.size++
}

// insertNode recursively inserts a node and rebalances the tree.
func (t *Tree) insertNode(n *node, start, end, seq int, style *lipgloss.Style) *node {
	if n == nil {
		return newNode(start, end, seq, style)
	}

	if start < n.start {
		n.left = t.insertNode(n.left, start, end, seq, style)
	} else {
		n.right = t.insertNode(n.right, start, end, seq, style)
	}

	return rebalance(n)
}

// result is used internally to collect query results with their sequence numbers.
type result struct {
	style *lipgloss.Style
	seq   int
}

// Query returns all styles whose intervals contain the given point,
// sorted by insertion order.
func (t *Tree) Query(point int) []*lipgloss.Style {
	if t.root == nil {
		return nil
	}

	var results []result

	queryNode(t.root, point, &results)

	if len(results) == 0 {
		return nil
	}

	// Results are already sorted by seq via online insertion sort during collection.
	styles := make([]*lipgloss.Style, len(results))
	for i := range results {
		styles[i] = results[i].style
	}

	return styles
}

// queryNode recursively searches for intervals containing the point.
// Results are collected unsorted and sorted after collection in Query().
func queryNode(n *node, point int, results *[]result) {
	if n == nil {
		return
	}

	// Prune: if point is beyond the maximum end in this subtree,
	// no intervals here can contain it.
	if point >= n.maxEnd {
		return
	}

	// Search left subtree.
	queryNode(n.left, point, results)

	// Check this node: interval is [start, end).
	if point >= n.start && point < n.end {
		// Insert maintaining sorted order by seq (online insertion sort).
		// This is efficient because tree traversal often produces nearly-sorted results.
		*results = append(*results, result{style: n.style, seq: n.seq})
		for i := len(*results) - 1; i > 0 && (*results)[i].seq < (*results)[i-1].seq; i-- {
			(*results)[i], (*results)[i-1] = (*results)[i-1], (*results)[i]
		}
	}

	// Search right subtree only if point >= start.
	// (intervals in right subtree have start >= n.start).
	if point >= n.start {
		queryNode(n.right, point, results)
	}
}

// Interval represents an interval with its associated style and insertion order.
type Interval struct {
	Style *lipgloss.Style
	Start int
	End   int
	seq   int // Insertion sequence for ordering.
}

// QueryRange returns all intervals that overlap the range [start, end),
// sorted by insertion order.
// An interval [iStart, iEnd) overlaps [start, end) if iStart < end && iEnd > start.
func (t *Tree) QueryRange(start, end int) []Interval {
	if t.root == nil {
		return nil
	}

	var results []Interval

	queryRangeNode(t.root, start, end, &results)

	if len(results) == 0 {
		return nil
	}

	// Results are already sorted by seq via online insertion sort during collection.
	return results
}

// queryRangeNode recursively searches for intervals overlapping [start, end).
func queryRangeNode(n *node, start, end int, results *[]Interval) {
	if n == nil {
		return
	}

	// Prune: if query start is beyond the maximum end in this subtree,
	// no intervals here can overlap.
	if start >= n.maxEnd {
		return
	}

	// Search left subtree.
	queryRangeNode(n.left, start, end, results)

	// Check this node: interval [n.start, n.end) overlaps [start, end)
	// if n.start < end && n.end > start.
	if n.start < end && n.end > start {
		// Insert maintaining sorted order by seq (online insertion sort).
		// This is efficient because tree traversal often produces nearly-sorted results.
		*results = append(*results, Interval{
			Start: n.start,
			End:   n.end,
			Style: n.style,
			seq:   n.seq,
		})
		for i := len(*results) - 1; i > 0 && (*results)[i].seq < (*results)[i-1].seq; i-- {
			(*results)[i], (*results)[i-1] = (*results)[i-1], (*results)[i]
		}
	}

	// Search right subtree only if query end > n.start.
	// (intervals in right subtree have start >= n.start, so if end <= n.start,
	// they can't overlap with [start, end)).
	if end > n.start {
		queryRangeNode(n.right, start, end, results)
	}
}
