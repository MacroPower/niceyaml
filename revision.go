package niceyaml

// Revision represents [Lines] at one or more revisions.
// It may form a linked or doubly-linked list to track changes across revisions.
// It is not required to have multiple revisions; a single revision is valid.
type Revision struct {
	// The previous token collection in the revision sequence.
	// If there is no previous revision, it is nil.
	prev *Revision
	// The next token collection in the revision sequence.
	// If there is no next revision, it is nil.
	next *Revision
	// The collection of [Tokens] at the head.
	head *Lines
}

// NewRevision creates new [Revision]. The provided values form the [Lines] at the head.
// You may use [Revision.Append] or [Revision.Prepend] to add more revisions.
// A builder pattern is supported for values that are known at compile time.
func NewRevision(tokens *Lines) *Revision {
	return &Revision{head: tokens}
}

// Lines returns the [Lines] at the head.
func (t *Revision) Lines() *Lines {
	return t.head
}

// Name returns the name of the [Lines] at the head.
func (t *Revision) Name() string {
	return t.head.Name
}

// Seek moves n revisions forward (n > 0) or backward (n < 0) in the sequence.
// If n exceeds the available revisions, it stops at the end.
func (t *Revision) Seek(n int) *Revision {
	curr := t

	if n > 0 {
		for range n {
			if curr.next == nil {
				break
			}

			curr = curr.next
		}
	}

	if n < 0 {
		for range -n {
			if curr.prev == nil {
				break
			}

			curr = curr.prev
		}
	}

	return curr
}

// Tip goes to the latest revision in the sequence.
func (t *Revision) Tip() *Revision {
	curr := t
	for curr.next != nil {
		curr = curr.next
	}

	return curr
}

// Origin goes to the original revision in the sequence.
func (t *Revision) Origin() *Revision {
	curr := t
	for curr.prev != nil {
		curr = curr.prev
	}

	return curr
}

// At returns the revision at the given zero-based index.
// If index exceeds the available revisions, it stops at the last one.
// This is equivalent to Origin().Seek(index).
func (t *Revision) At(index int) *Revision {
	return t.Origin().Seek(index)
}

// AtTip returns true if this is the latest revision in the sequence.
func (t *Revision) AtTip() bool {
	return t.next == nil
}

// AtOrigin returns true if this is the original revision in the sequence.
func (t *Revision) AtOrigin() bool {
	return t.prev == nil
}

// Names returns the names of all revisions in order from origin to latest.
func (t *Revision) Names() []string {
	var names []string

	// Walk to origin.
	origin := t
	for origin.prev != nil {
		origin = origin.prev
	}

	// Collect names forward.
	curr := origin
	for curr != nil {
		names = append(names, curr.head.Name)
		curr = curr.next
	}

	return names
}

// Index returns the zero-based index of the [Revision] at the head.
func (t *Revision) Index() int {
	index := 0

	// Count previous revisions.
	curr := t
	for curr.prev != nil {
		index++
		curr = curr.prev
	}

	return index
}

// Count returns the total number of revisions in the sequence.
func (t *Revision) Count() int {
	// Count previous revisions.
	count := t.Index()

	// Count next revisions.
	curr := t
	for curr.next != nil {
		count++
		curr = curr.next
	}

	return count + 1
}

// Append adds a new revision after the [Lines] at the head.
// Returns the newly added revision.
func (t *Revision) Append(tokens *Lines) *Revision {
	rev := &Revision{
		prev: t,
		head: tokens,
	}
	t.next = rev

	return rev
}

// Prepend adds a new revision before the [Lines] at the head.
// Returns the newly added revision.
func (t *Revision) Prepend(tokens *Lines) *Revision {
	rev := &Revision{
		next: t,
		head: tokens,
	}
	t.prev = rev

	return rev
}
