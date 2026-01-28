package niceyaml

// Revision represents a [*Source] at one or more revisions.
// It may form a linked or doubly-linked list to track changes across revisions.
// A single revision is valid; multiple revisions are not required.
//
// Create instances with [NewRevision].
type Revision struct {
	// The previous token collection in the revision sequence.
	// If there is no previous revision, it is nil.
	prev *Revision
	// The next token collection in the revision sequence.
	// If there is no next revision, it is nil.
	next *Revision
	// The [*Source] at the head.
	head *Source
}

// NewRevision creates a new [*Revision] with the given [*Source] at the head.
// Use [Revision.Append] or [Revision.Prepend] to add more revisions.
// A builder pattern is supported for values that are known at compile time.
func NewRevision(s *Source) *Revision {
	return &Revision{head: s}
}

// Source returns the [*Source] at the head.
func (t *Revision) Source() *Source {
	return t.head
}

// Name returns the name of the [*Source] at the head.
func (t *Revision) Name() string {
	return t.head.Name()
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

// Tip returns the latest revision in the sequence.
func (t *Revision) Tip() *Revision {
	curr := t
	for curr.next != nil {
		curr = curr.next
	}

	return curr
}

// Origin returns the original revision in the sequence.
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

// AtTip reports whether this is the latest revision in the sequence.
func (t *Revision) AtTip() bool {
	return t.next == nil
}

// AtOrigin reports whether this is the original revision in the sequence.
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
		names = append(names, curr.head.Name())
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

// Len returns the total number of revisions in the sequence.
func (t *Revision) Len() int {
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

// Append adds a new revision after the [*Source] at the head.
// Returns the newly added revision.
func (t *Revision) Append(s *Source) *Revision {
	rev := &Revision{
		prev: t,
		head: s,
	}
	t.next = rev

	return rev
}

// Prepend adds a new revision before the [*Source] at the head.
// Returns the newly added revision.
func (t *Revision) Prepend(s *Source) *Revision {
	rev := &Revision{
		next: t,
		head: s,
	}
	t.prev = rev

	return rev
}
