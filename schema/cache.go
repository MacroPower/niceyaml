package schema

import (
	"container/list"
	"sync"
	"time"

	"go.jacobcolvin.com/niceyaml/internal/capture"
)

// WithCacheLimit is a [RegistryOption] that bounds the number of schemas
// the registry keeps at n. Once the cache holds n entries, the registry
// drops the entry no request has used for the longest time before it
// keeps a new one. The next request for a schema the registry dropped
// loads and compiles it again:
//
//	reg := schema.NewRegistry(
//	    schema.WithCacheLimit(1024),
//	    schema.WithResolvers(schema.Directive()),
//	)
//
// A failure the registry remembers counts toward n until the interval of
// [WithRetryAfter] has passed, and the registry then drops it whatever
// the limit.
//
// The registry keeps the documents that $refs name apart from the
// schemas, and n bounds their number too. A compile or a validation that
// reaches a document the registry dropped loads it again.
//
// The registry holds each file system for as long as it keeps an entry
// from that file system, as [FileFS] describes. The limit thus bounds
// the file systems the registry holds too. A program can open each
// upload in a file system of its own, such as an [fs.Sub] or an
// [*archive/zip.Reader]. When the cache is full, the registry drops the
// entries used least recently and stops holding the uploads they came
// from. [Registry.Forget] drops the entries of one file system without
// waiting for the limit.
//
// Without a limit, the registry keeps each schema it compiles, and holds
// the file system the schema comes from, until [Registry.Forget] drops
// the entries of that file system. Each distinct [Ref.Key] adds an
// entry. A URL that differs from another only in its query string or
// fragment is a Key of its own. Documents that name endless distinct
// Keys therefore grow the memory of a long-lived registry without bound.
// A registry that serves documents from another trust domain for its
// whole lifetime sets a limit, as the Untrusted Documents section of the
// package documentation describes.
//
// A limit of zero or less leaves the registry as it is, and by default
// the registry keeps every entry. Given more than once, the last option
// with a positive limit wins.
func WithCacheLimit(n int) RegistryOption {
	return func(c *registryConfig) {
		if n > 0 {
			c.cacheLimit = n
		}
	}
}

// An lru keeps values by key in the order of their last use. It drops
// the value used least recently once it holds more than its limit, and
// it drops a value that records a failure once the retry interval has
// passed since the failure. It is safe for concurrent use.
type lru[K comparable, V any] struct {
	items      map[K]*lruItem[K, V]
	recent     list.List // the item of each key, most recently used first
	failures   list.List // the items that record a failure, oldest first
	retryAfter time.Duration
	limit      int // the most items the lru keeps, or 0 for no limit
	mu         sync.Mutex
}

// An lruItem is a value an [lru] keeps, with its key and its place in
// each list of the lru.
type lruItem[K comparable, V any] struct {
	// When the failure the value records happened, or the zero time for
	// a value that records none.
	failedAt time.Time
	key      K
	value    V
	use      *list.Element // the element of the item in recent
	failure  *list.Element // the element of the item in failures, or nil
}

// newLRU creates a new [lru] that keeps at most limit values, or any
// number for a limit of 0, and drops a failure retryAfter after it
// happened.
func newLRU[K comparable, V any](limit int, retryAfter time.Duration) *lru[K, V] {
	return &lru[K, V]{
		items:      make(map[K]*lruItem[K, V]),
		limit:      limit,
		retryAfter: retryAfter,
	}
}

// get returns the value c keeps under key and marks it used. A failure
// whose retry interval has passed counts as no value, and get drops it.
func (c *lru[K, V]) get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, ok := c.items[key]
	if !ok {
		var zero V

		return zero, false
	}

	if c.expired(item) {
		c.remove(item)

		var zero V

		return zero, false
	}

	c.recent.MoveToFront(item.use)

	return item.value, true
}

// keep keeps v under key and returns it. A failedAt other than the zero
// time says v records a failure that happened then. When c keeps a value
// that records no failure under key already, keep marks that value used
// and returns it in place of v, so every caller sees one value per key.
//
// Before it adds v, keep drops each failure whose retry interval has
// passed. After it, keep drops the values used least recently until c
// holds no more than its limit.
func (c *lru[K, V]) keep(key K, v V, failedAt time.Time) V {
	c.mu.Lock()
	defer c.mu.Unlock()

	if item, ok := c.items[key]; ok {
		if item.failure == nil {
			c.recent.MoveToFront(item.use)

			return item.value
		}

		c.remove(item)
	}

	c.dropExpired()

	item := &lruItem[K, V]{key: key, value: v, failedAt: failedAt}
	item.use = c.recent.PushFront(item)

	if !failedAt.IsZero() {
		item.failure = c.failures.PushBack(item)
	}

	c.items[key] = item

	for c.limit > 0 && len(c.items) > c.limit {
		c.remove(itemOf[K, V](c.recent.Back()))
	}

	return v
}

// drop drops the value of each key that match reports true for.
func (c *lru[K, V]) drop(match func(K) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, item := range c.items {
		if match(key) {
			c.remove(item)
		}
	}
}

// dropExpired drops each failure whose retry interval has passed. The
// failures list holds them oldest first, so dropExpired stops at the
// first failure whose interval has not passed.
func (c *lru[K, V]) dropExpired() {
	for e := c.failures.Front(); e != nil; e = c.failures.Front() {
		item := itemOf[K, V](e)
		if !c.expired(item) {
			return
		}

		c.remove(item)
	}
}

// expired reports whether item records a failure whose retry interval
// has passed.
func (c *lru[K, V]) expired(item *lruItem[K, V]) bool {
	return item.failure != nil && time.Since(item.failedAt) >= c.retryAfter
}

// remove drops item from c.
func (c *lru[K, V]) remove(item *lruItem[K, V]) {
	delete(c.items, item.key)
	c.recent.Remove(item.use)

	if item.failure != nil {
		c.failures.Remove(item.failure)
	}
}

// itemOf returns the item e holds. Every element of the lists of an
// [lru] holds an [*lruItem].
func itemOf[K comparable, V any](e *list.Element) *lruItem[K, V] {
	item, _ := e.Value.(*lruItem[K, V]) //nolint:errcheck // Every element holds an item.

	return item
}

// A flightGroup runs one call at a time for each key, and every caller
// that asks for the key while the call runs shares its result. Its zero
// value is ready to use, and it is safe for concurrent use.
type flightGroup[K comparable, V any] struct {
	calls map[K]*flightCall[V]
	mu    sync.Mutex
}

// A flightCall is one call of a [flightGroup]. Its done channel closes
// when the call ends, and val and err hold its result from then on.
type flightCall[V any] struct {
	err  error
	done chan struct{}
	val  V
}

// do returns the call in flight for key. When none is in flight, do
// starts fn on a goroutine of its own and returns the new call. The
// group forgets the call when it ends, so the next do for key starts a
// new call.
//
// The call runs fn through [capture.Run]. Without it, a panic in fn ends
// the process, and a call to [runtime.Goexit] skips the close of done
// and leaves every caller blocked. With capture.Run, the call keeps
// either as an error in err. Each caller must pass err to
// [capture.Reraise].
func (g *flightGroup[K, V]) do(key K, fn func() (V, error)) *flightCall[V] {
	g.mu.Lock()
	defer g.mu.Unlock()

	if c, ok := g.calls[key]; ok {
		return c
	}

	if g.calls == nil {
		g.calls = make(map[K]*flightCall[V])
	}

	c := &flightCall[V]{done: make(chan struct{})}
	g.calls[key] = c

	go func() {
		c.err = capture.Run(func() error {
			var err error

			c.val, err = fn()

			return err
		})

		g.mu.Lock()
		delete(g.calls, key)
		g.mu.Unlock()

		close(c.done)
	}()

	return c
}
