package schema

import (
	"container/list"
	"strings"
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
// Without a limit, the registry keeps each schema it compiles until
// [Registry.Forget], [Registry.Invalidate], or [Registry.Purge] drops
// it. It holds the file system the schema comes from for that long.
// Each distinct [Ref.Key] adds an entry. A URL that differs from another
// only in its query string or fragment is a Key of its own. Documents
// that name endless distinct Keys therefore grow the memory of a
// long-lived registry without bound. A registry that serves documents
// from another trust domain for its whole lifetime sets a limit, as the
// Untrusted Documents section of the package documentation describes.
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

// A cache holds what a [Registry] keeps between requests: its compiled
// schemas, the documents $refs name, and an index of the documents each
// schema reached. One lock guards all of it, so a release such as
// [Registry.Invalidate] drops a document and every schema that reached
// it in one step. A cache is safe for concurrent use.
//
// The cache counts its generations. [cache.invalidate] and [cache.purge]
// each start a new one. A load keeps its outcome only in the generation
// it started in. A load that ran across a release thus keeps nothing,
// whether or not the release meant to drop what the load returns.
type cache struct {
	schemas *lru[cacheKey, cacheEntry]
	docs    *lru[cacheKey, refDoc]
	// The reach of each schema the cache keeps, by each document it
	// reached.
	dependents map[docID]map[*reach]struct{}
	// The keys of the documents a $ref names, by document. Several keys
	// name one document when they spell its URL with different userinfo,
	// or with none, or when they spell one file in different ways, as
	// [docOf] describes.
	docKeys map[docID]map[cacheKey]struct{}
	gen     uint64
	mu      sync.Mutex
}

// newCache creates a new [cache] that keeps at most limit schemas and at
// most limit documents, or any number for a limit of 0. The cache drops
// a failure retryAfter after it happened.
func newCache(limit int, retryAfter time.Duration) *cache {
	c := &cache{
		dependents: make(map[docID]map[*reach]struct{}),
		docKeys:    make(map[docID]map[cacheKey]struct{}),
	}

	c.schemas = newLRU(limit, retryAfter, c.releaseSchema)
	c.docs = newLRU(limit, retryAfter, c.releaseDoc)

	return c
}

// schema returns the entry c keeps under key, and the generation a load
// for key that misses starts in.
func (c *cache) schema(key cacheKey) (cacheEntry, uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.schemas.get(key)

	return e, c.gen, ok
}

// keepSchema keeps e, with rc as its reach, under key for a compile that
// started in generation gen. It returns the entry c then holds under
// key. A compile from an earlier generation keeps nothing, and
// keepSchema returns e for it.
func (c *cache) keepSchema(key cacheKey, e cacheEntry, failedAt time.Time, gen uint64, rc *reach) cacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()

	if gen != c.gen {
		rc.state = reachDropped

		return e
	}

	e.reach = rc

	kept := c.schemas.keep(key, e, failedAt)
	if kept.reach != rc {
		rc.state = reachDropped

		return kept
	}

	rc.state = reachKept

	for id := range rc.docs {
		link(c.dependents, id, rc)
	}

	return kept
}

// doc records that the schema of rc reached the document that key names.
// It returns what c keeps under key, with the generation a load for key
// that misses starts in. The record comes before the read, so a release
// of the document that runs after doc returns finds the schema of rc.
func (c *cache) doc(key cacheKey, rc *reach) (refDoc, uint64, bool) {
	id := docOf(key)

	c.mu.Lock()
	defer c.mu.Unlock()

	if rc.state != reachDropped {
		if _, ok := rc.docs[id]; !ok {
			rc.docs[id] = struct{}{}

			if rc.state == reachKept {
				link(c.dependents, id, rc)
			}
		}
	}

	d, ok := c.docs.get(key)

	return d, c.gen, ok
}

// keepDoc keeps d under key for a load that started in generation gen,
// and returns the document c then holds under key. A load from an
// earlier generation keeps nothing, and keepDoc returns d for it.
func (c *cache) keepDoc(key cacheKey, d refDoc, failedAt time.Time, gen uint64) refDoc {
	id := docOf(key)

	c.mu.Lock()
	defer c.mu.Unlock()

	if gen != c.gen {
		return d
	}

	kept := c.docs.keep(key, d, failedAt)
	link(c.docKeys, id, key)

	return kept
}

// invalidate starts a new generation and drops each document of ids, and
// each schema that reached one of them.
func (c *cache) invalidate(ids map[docID]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.gen++

	// Each drop changes the indexes, so collect the keys first.
	var schemas, docs []cacheKey

	for id := range ids {
		for rc := range c.dependents[id] {
			schemas = append(schemas, rc.key)
		}

		for key := range c.docKeys[id] {
			docs = append(docs, key)
		}
	}

	for _, key := range schemas {
		c.schemas.discard(key)
	}

	for _, key := range docs {
		c.docs.discard(key)
	}
}

// forget drops each schema and document from the file system id names.
// It does not start a new generation, so a load from that file system in
// flight might keep its outcome after forget returns.
func (c *cache) forget(id fsID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	inFS := func(key cacheKey) bool { return key.fs == id }

	c.schemas.drop(inFS)
	c.docs.drop(inFS)
}

// purge starts a new generation and drops every schema and document.
func (c *cache) purge() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.gen++

	all := func(cacheKey) bool { return true }

	c.schemas.drop(all)
	c.docs.drop(all)
}

// releaseSchema removes e, an entry that c.schemas dropped, from the
// index of dependents. After the release, the reach of e records no more
// documents.
func (c *cache) releaseSchema(_ cacheKey, e cacheEntry) {
	rc := e.reach

	for id := range rc.docs {
		unlink(c.dependents, id, rc)
	}

	rc.state = reachDropped
}

// releaseDoc removes key, the key of a document that c.docs dropped,
// from the index of document keys.
func (c *cache) releaseDoc(key cacheKey, _ refDoc) {
	unlink(c.docKeys, docOf(key), key)
}

// A reach records the documents that one compile of a schema reached:
// the document of the schema itself, and each document a $ref names. A
// validation with the compiled schema that loads a $ref document adds
// that document. A [cache] guards it.
type reach struct {
	docs  map[docID]struct{}
	key   cacheKey // the key the registry keeps the schema under
	state reachState
}

// newReach creates a new [reach] for a compile of the schema the
// registry keeps under key. The reach starts with the document of the
// schema itself.
func newReach(key cacheKey) *reach {
	return &reach{key: key, docs: map[docID]struct{}{docOf(key): {}}, state: reachPending}
}

// A reachState says whether a [cache] keeps the outcome of the compile
// that a [reach] belongs to.
type reachState int

const (
	// The compile runs, and the cache does not keep its outcome yet.
	reachPending reachState = iota
	// The cache keeps the outcome, and the index of dependents holds the
	// reach under each of its documents.
	reachKept
	// The cache does not keep the outcome, and never will.
	reachDropped
)

// A docID names a document the registry loads, whatever key a request
// spells it with. It holds the file system the document lives in, as a
// [cacheKey] does, and a URI without userinfo or fragment. The URI of a
// file is the URL [fileURL] gives for the path the registry reads. Any
// other URI is in the normal form [canonicalURL] gives.
//
//nolint:unused // The fields tell the documents of the registry apart.
type docID struct {
	fs  fsID
	uri string
}

// docOf returns the [docID] of the document that key names. The registry
// reads a file URL by the path [fileURLPath] finds in it. That path
// leaves out a query and a localhost authority, so every URL that reads
// one file gets one docID. Windows reads a drive letter in either case, so
// docOf spells it in upper case.
func docOf(key cacheKey) docID {
	if isFileURL(key.uri) {
		if path, ok := fileURLPath(key.uri); ok {
			if hasDriveLetter(path) {
				path = strings.ToUpper(path[:1]) + path[1:]
			}

			return docID{fs: key.fs, uri: fileURL(path)}
		}
	}

	uri, _ := splitUserinfo(key.uri)

	return docID{fs: key.fs, uri: canonicalURL(uri)}
}

// link adds v to the set m holds under k.
func link[K, V comparable](m map[K]map[V]struct{}, k K, v V) {
	set, ok := m[k]
	if !ok {
		set = make(map[V]struct{})
		m[k] = set
	}

	set[v] = struct{}{}
}

// unlink removes v from the set m holds under k, and removes the set
// when it holds nothing more.
func unlink[K, V comparable](m map[K]map[V]struct{}, k K, v V) {
	set := m[k]
	delete(set, v)

	if len(set) == 0 {
		delete(m, k)
	}
}

// An lru keeps values by key in the order of their last use. It drops
// the value used least recently when it holds more than its limit. It
// drops a value that records a failure when the retry interval has
// passed since the failure. It calls its release function with each
// value it drops. It is not safe for concurrent use, and a [cache]
// guards it.
type lru[K comparable, V any] struct {
	items      map[K]*lruItem[K, V]
	release    func(K, V) // called with each value the lru drops
	recent     list.List  // the item of each key, most recently used first
	failures   list.List  // the items that record a failure, oldest first
	retryAfter time.Duration
	limit      int // the most items the lru keeps, or 0 for no limit
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
// number for a limit of 0. The lru drops a failure retryAfter after it
// happened, and it calls release with each value it drops.
func newLRU[K comparable, V any](limit int, retryAfter time.Duration, release func(K, V)) *lru[K, V] {
	return &lru[K, V]{
		items:      make(map[K]*lruItem[K, V]),
		release:    release,
		limit:      limit,
		retryAfter: retryAfter,
	}
}

// get returns the value c keeps under key and marks it used. A failure
// whose retry interval has passed counts as no value, and get drops it.
func (c *lru[K, V]) get(key K) (V, bool) {
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

// discard drops the value c keeps under key, if any.
func (c *lru[K, V]) discard(key K) {
	if item, ok := c.items[key]; ok {
		c.remove(item)
	}
}

// drop drops the value of each key that match reports true for.
func (c *lru[K, V]) drop(match func(K) bool) {
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

// remove drops item from c and calls the release function with it.
func (c *lru[K, V]) remove(item *lruItem[K, V]) {
	delete(c.items, item.key)
	c.recent.Remove(item.use)

	if item.failure != nil {
		c.failures.Remove(item.failure)
	}

	c.release(item.key, item.value)
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
