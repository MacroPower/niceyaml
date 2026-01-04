package styletree

import "charm.land/lipgloss/v2"

// node represents a node in the interval tree.
type node struct {
	style  *lipgloss.Style // Style associated with this interval.
	left   *node
	right  *node
	start  int // Linearized start position.
	end    int // Linearized end position (exclusive).
	seq    int // Insertion sequence for order preservation.
	maxEnd int // Maximum end position in this subtree (for pruning).
	height int
}

// newNode creates a new node with the given interval and style.
func newNode(start, end, seq int, style *lipgloss.Style) *node {
	return &node{
		start:  start,
		end:    end,
		style:  style,
		seq:    seq,
		maxEnd: end,
		height: 1,
	}
}

// updateHeight recalculates the height of a node based on its children.
func (n *node) updateHeight() {
	leftH, rightH := 0, 0

	if n.left != nil {
		leftH = n.left.height
	}

	if n.right != nil {
		rightH = n.right.height
	}

	n.height = 1 + max(leftH, rightH)
}

// updateMaxEnd recalculates the maxEnd of a node as the maximum of its own
// end and its children's maxEnd values.
func (n *node) updateMaxEnd() {
	n.maxEnd = n.end
	if n.left != nil && n.left.maxEnd > n.maxEnd {
		n.maxEnd = n.left.maxEnd
	}
	if n.right != nil && n.right.maxEnd > n.maxEnd {
		n.maxEnd = n.right.maxEnd
	}
}

// balanceFactor returns the balance factor of a node.
func (n *node) balanceFactor() int {
	leftH, rightH := 0, 0

	if n.left != nil {
		leftH = n.left.height
	}

	if n.right != nil {
		rightH = n.right.height
	}

	return leftH - rightH
}

// rotateRight performs a right rotation on the subtree rooted at y.
//
//	    y                x
//	   / \              / \
//	  x   C    ->      A   y
//	 / \                  / \
//	A   B                B   C
func rotateRight(y *node) *node {
	x := y.left
	b := x.right

	x.right = y
	y.left = b

	y.updateHeight()
	y.updateMaxEnd()
	x.updateHeight()
	x.updateMaxEnd()

	return x
}

// rotateLeft performs a left rotation on the subtree rooted at x.
//
//	  x                  y
//	 / \                / \
//	A   y      ->      x   C
//	   / \            / \
//	  B   C          A   B
func rotateLeft(x *node) *node {
	y := x.right
	b := y.left

	y.left = x
	x.right = b

	x.updateHeight()
	x.updateMaxEnd()
	y.updateHeight()
	y.updateMaxEnd()

	return y
}

// rebalance rebalances the subtree rooted at n and returns the new root.
func rebalance(n *node) *node {
	n.updateHeight()
	n.updateMaxEnd()

	balance := n.balanceFactor()

	// Left heavy.
	if balance > 1 {
		// Left-right case: rotate left child left first.
		if n.left.balanceFactor() < 0 {
			n.left = rotateLeft(n.left)
		}
		// Left-left case.
		return rotateRight(n)
	}

	// Right heavy.
	if balance < -1 {
		// Right-left case: rotate right child right first.
		if n.right.balanceFactor() > 0 {
			n.right = rotateRight(n.right)
		}
		// Right-right case.
		return rotateLeft(n)
	}

	return n
}
