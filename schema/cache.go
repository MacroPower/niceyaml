package schema

import (
	"cmp"
	"container/list"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	slashpath "path"

	"go.jacobcolvin.com/niceyaml/internal/capture"
)

// WithCacheLimit is a [RegistryOption] that bounds the number of schemas
// that the registry keeps at n. When the cache holds n entries, the
// registry drops the entry that requests used least recently before it
// keeps a new one. The next request for a schema that the registry
// dropped loads and compiles it again:
//
//	reg := schema.NewRegistry(
//	    schema.WithAccess(access),
//	    schema.WithCacheLimit(1024),
//	    schema.WithResolvers(schema.Directive()),
//	)
//
// A failure that the registry remembers counts toward n until the
// interval of [WithRetryAfter] has passed, and the registry then drops it
// whatever the limit.
//
// The registry keeps the documents that $refs name apart from the
// schemas, and n bounds their number too. A compile or a validation that
// reaches a document that the registry dropped loads it again.
//
// The registry holds each file system for as long as it keeps an entry
// from that file system, as [FileFS] describes. The limit thus bounds
// the file systems that the registry holds too. A program can open each
// upload in a file system of its own, such as an [fs.Sub] or an
// [*archive/zip.Reader]. When the cache is full, the registry drops the
// entries used least recently and stops holding the uploads that they
// came from. [Registry.Forget] drops the entries of one file system without
// waiting for the limit.
//
// Without a limit, the registry keeps each schema that it compiles until
// [Registry.Forget], [Registry.Invalidate], or [Registry.Purge] drops
// it, or until a refresh from [WithRefreshAfter] replaces it. It holds
// the file system that the schema comes from for that long.
//
// Each distinct [Ref.Key] adds an entry. A URL that differs from another
// only in its query string or fragment is a Key of its own. In a
// long-lived registry without a limit, documents that name endless
// distinct Keys therefore grow the memory without bound. A registry that
// serves documents from another trust domain for its whole lifetime sets
// a limit, as the Untrusted Documents section of the package
// documentation describes.
//
// By default the registry keeps every entry, as [niceyaml.NoLimit]
// sets. A limit of zero or less keeps the limit as it is. Given more than
// once, the last option with a positive limit wins.
func WithCacheLimit(n int) RegistryOption {
	return func(c *registryConfig) {
		if n > 0 {
			c.cacheLimit = n
		}
	}
}

// A cache holds what a [Registry] keeps between requests: its compiled
// schemas, the documents that $refs name, and an index of the documents
// that each schema and each compile in flight reached. One lock guards
// all of it, so a release such as [Registry.Invalidate] drops a document
// and every schema that reached it in one step. A cache is safe for
// concurrent use.
//
// The cache dates each schema and document that a URL served, when
// [WithRefreshAfter] gave the registry an interval. A request for such
// a schema after its due time gets [hitDue], and a request for such a
// document after its due time is a miss. The registry then loads the
// entry again. When that load fails, the cache keeps the entry that it
// holds and dates it again for the retry interval, as [lru.keep]
// describes. A schema falls due when the first URL document that it
// reached falls due, so a refresh of the document reaches each schema
// that reached it.
//
// The cache counts its generations. [cache.purge] starts a new one, and
// so does [cache.invalidate] when it names a document that the cache
// knows. A load keeps its outcome only in the generation that it started
// in. A load that ran across such a release thus does not keep its
// outcome, whether or not the release meant to drop what the load
// returns. A release of a document that the cache does not know leaves
// the generation as it is, because no load in flight has read that
// document.
type cache struct {
	schemas *lru[cacheKey, cacheEntry]
	docs    *lru[cacheKey, refDoc]
	// The reach of each schema that the cache keeps, and of each compile
	// that runs, by each document that it reached.
	reaches map[docID]map[*reach]struct{}
	// The keys of the documents that a $ref names, by document. Several
	// keys name one document when they spell its URL with different
	// userinfo, or without userinfo, or when they spell one file in
	// different ways, as [docOf] describes.
	docKeys map[docID]map[cacheKey]struct{}
	// The number of loads of each document that run for a $ref.
	loading map[docID]int
	gen     uint64
	// How long after its load an entry from a URL falls due, or zero for
	// never.
	refreshAfter time.Duration
	mu           sync.Mutex
}

// newCache creates a new [cache] that keeps at most limit schemas and at
// most limit documents, or any number for [niceyaml.NoLimit]. The cache drops
// a failure retryAfter after it happened. An entry from a URL falls due
// refreshAfter after its load started, or never for a refreshAfter of
// zero.
func newCache(limit int, retryAfter, refreshAfter time.Duration) *cache {
	c := &cache{
		reaches:      make(map[docID]map[*reach]struct{}),
		docKeys:      make(map[docID]map[cacheKey]struct{}),
		loading:      make(map[docID]int),
		refreshAfter: refreshAfter,
	}

	c.schemas = newLRU(limit, retryAfter, c.releaseSchema)
	c.docs = newLRU(limit, retryAfter, c.releaseDoc)

	return c
}

// schema returns the entry that c keeps under key, and the generation
// that a load for key starts in after a miss. It also returns the [hit]
// that says what the entry answers. An entry that has fallen due answers
// with [hitDue], and c keeps it until a load takes its place.
func (c *cache) schema(key cacheKey) (cacheEntry, uint64, hit) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, due, ok := c.schemas.get(key)

	switch {
	case !ok:
		return e, c.gen, hitNone
	case isDue(due):
		return e, c.gen, hitDue
	default:
		return e, c.gen, hitFresh
	}
}

// A hit says what an entry that a [cache] holds answers with.
type hit int

const (
	// The cache does not hold an entry, and a load must produce one.
	hitNone hit = iota
	// The entry answers every request.
	hitFresh
	// The entry holds a schema that has fallen due. It answers a request
	// only while a load that takes its place has not finished.
	hitDue
)

// newReach creates a new [reach] for a compile, which starts now, of the
// schema that ref names and that the registry keeps under key. The reach
// starts with the document of the schema itself, which falls due as
// [cache.dueAfter] says. The index of reaches holds it from the start,
// so a release of the document during the compile finds it. The caller
// passes the reach to [cache.settle] when the compile ends.
func (c *cache) newReach(key cacheKey, ref Ref) *reach {
	start := time.Now()
	id := docOf(key)

	rc := &reach{
		cache: c,
		key:   key,
		fsys:  ref.fsys,
		start: start,
		due:   c.dueAfter(key, start),
		docs:  map[docID]struct{}{id: {}},
		state: reachPending,
	}

	if key.kind == keyNamed {
		rc.named = Ref{key: ref.key, load: ref.load}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	link(c.reaches, id, rc)

	return rc
}

// settle drops rc, the reach of a compile that has ended, from the index
// of reaches when the cache did not keep the outcome of the compile.
func (c *cache) settle(rc *reach) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if rc.state == reachPending {
		c.dropReach(rc)
	}
}

// dropReach removes rc from the index of reaches. After the drop, a
// release does not find rc, but rc still records the documents that its
// schema reaches. The caller holds the lock of c.
func (c *cache) dropReach(rc *reach) {
	for id := range rc.docs {
		unlink(c.reaches, id, rc)
	}

	rc.state = reachDropped
}

// dueAfter returns the time when an entry under key that loaded at
// start falls due. It returns the zero time, for never, when key does
// not name a URL or when c does not refresh anything.
func (c *cache) dueAfter(key cacheKey, start time.Time) time.Time {
	if c.refreshAfter <= 0 || key.kind != keyURL {
		return time.Time{}
	}

	return start.Add(c.refreshAfter)
}

// reached records in rc that its schema used a document that falls due
// at due. The schema falls due no later than the document. For a schema
// that c keeps, reached moves the due time of its entry too.
func (c *cache) reached(rc *reach, due time.Time) {
	if due.IsZero() || (!rc.due.IsZero() && !due.Before(rc.due)) {
		return
	}

	rc.due = due

	if rc.state == reachKept {
		c.schemas.advance(rc.key, due)
	}
}

// keepSchema keeps e under key for a compile that started in generation
// gen. The reach of e records what the compile reached. It returns the
// entry that c then holds under key. For a compile from an earlier
// generation, keepSchema does not keep e. It returns e as it is, or for
// a failure, the schema that c holds under key. A release that started a
// new generation without dropping that schema thus leaves the schema to
// answer a failed refresh.
func (c *cache) keepSchema(key cacheKey, e cacheEntry, failedAt time.Time, gen uint64) cacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()

	rc := e.reach

	if gen != c.gen {
		c.dropReach(rc)

		if !failedAt.IsZero() {
			held, _, ok := c.schemas.get(key)
			if ok && held.failed == nil {
				return held
			}
		}

		return e
	}

	var due time.Time

	if failedAt.IsZero() {
		due = rc.due
	}

	kept, _ := c.schemas.keep(key, e, failedAt, due)
	if kept.reach != rc {
		c.dropReach(rc)

		return kept
	}

	rc.state = reachKept

	return kept
}

// doc records that the schema of rc reached the document that key names.
// It returns what c keeps under key, with the generation that a load for
// key starts in after a miss. The record comes before the read, so a release
// of the document that runs after doc returns finds the schema of rc. A
// document that has fallen due is a miss, as it is in [cache.schema].
//
// After a miss, c counts the load of the document as one in flight, so a
// release of the document finds it even when c has dropped rc. The
// caller passes key to [cache.loaded] when the load ends.
func (c *cache) doc(key cacheKey, rc *reach) (refDoc, uint64, bool) {
	id := docOf(key)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.add(rc, id)

	d, due, ok := c.docs.get(key)
	if !ok || isDue(due) {
		c.loading[id]++

		return d, c.gen, false
	}

	if rc.state != reachDropped {
		c.reached(rc, due)
	}

	return d, c.gen, true
}

// record records that the schema of rc reached the document that key
// names, for a document that the registry refuses to read. The
// dependencies of rc then name the document, as they name one that the
// registry read.
func (c *cache) record(key cacheKey, rc *reach) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.add(rc, docOf(key))
}

// add adds id to the documents of rc, and adds rc to the index of
// reaches under id unless c dropped rc. The caller holds the lock of c.
func (c *cache) add(rc *reach, id docID) {
	if _, ok := rc.docs[id]; ok {
		return
	}

	rc.docs[id] = struct{}{}

	if rc.state != reachDropped {
		link(c.reaches, id, rc)
	}
}

// loaded ends the count of a load of the document that key names, which
// [cache.doc] started.
func (c *cache) loaded(key cacheKey) {
	id := docOf(key)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.loading[id]--
	if c.loading[id] <= 0 {
		delete(c.loading, id)
	}
}

// heldDoc returns the document that c holds under key, for a load of the
// document that ended with its context. It takes the lock of c and
// returns what [cache.held] returns.
func (c *cache) heldDoc(key cacheKey, rc *reach) (refDoc, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.held(key, rc)
}

// held returns the document that c holds under key, for a load of the
// document that cannot serve the schema of rc. The caller holds the lock
// of c. It returns a document that loaded, whether or not it has fallen
// due, and records its due time in rc as [cache.doc] does. A schema
// that uses a document that has fallen due falls due with it, so the
// next request for the schema loads it again. When c does not hold a
// document that loaded, it reports false.
func (c *cache) held(key cacheKey, rc *reach) (refDoc, bool) {
	d, due, ok := c.docs.get(key)
	if !ok || d.err != nil {
		return refDoc{}, false
	}

	if rc.state != reachDropped {
		c.reached(rc, due)
	}

	return d, true
}

// keepDoc keeps d under key for a load that started at loaded in
// generation gen, for the schema whose reach is rc. It returns the
// document that c then holds under key. For a load from an earlier
// generation, keepDoc does not keep d. It returns d as it is, or for a
// failure, the document that c holds under key, as [cache.held] does.
//
// A document from a URL falls due when the compile of rc started, plus
// the refresh interval, so it falls due with the schema that the compile
// fetched. A load for a schema that c keeps already, as a validation
// runs, starts the interval at loaded.
func (c *cache) keepDoc(key cacheKey, d refDoc, failedAt, loaded time.Time, gen uint64, rc *reach) refDoc {
	id := docOf(key)

	c.mu.Lock()
	defer c.mu.Unlock()

	start := rc.start
	if rc.state != reachPending {
		start = loaded
	}

	var due time.Time

	if failedAt.IsZero() {
		due = c.dueAfter(key, start)
	}

	if gen != c.gen {
		if !failedAt.IsZero() {
			if held, ok := c.held(key, rc); ok {
				return held
			}
		}

		return d
	}

	kept, due := c.docs.keep(key, d, failedAt, due)
	link(c.docKeys, id, key)

	if rc.state != reachDropped {
		c.reached(rc, due)
	}

	return kept
}

// invalidate drops each document of ids, and each schema that reached
// one of them. When c knows one of the documents, invalidate also starts
// a new generation, so the loads in flight do not keep their outcome. A
// document that c does not know leaves c as it is.
func (c *cache) invalidate(ids map[docID]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !slices.ContainsFunc(slices.Collect(maps.Keys(ids)), c.knows) {
		return
	}

	c.gen++

	// Each drop changes the indexes, so collect the keys first.
	var schemas, docs []cacheKey

	for id := range ids {
		for rc := range c.reaches[id] {
			if rc.state == reachKept {
				schemas = append(schemas, rc.key)
			}
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

// knows reports whether c keeps a schema or a document under id, whether
// a schema that c keeps or a compile in flight reached id, and whether a
// load of id runs for a $ref. The caller holds the lock of c.
func (c *cache) knows(id docID) bool {
	return len(c.reaches[id]) > 0 || len(c.docKeys[id]) > 0 || c.loading[id] > 0
}

// forget drops each schema and document from the file system that id
// names. It does not start a new generation, so a load from that file
// system in flight might keep its outcome after forget returns.
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
// index of reaches.
func (c *cache) releaseSchema(_ cacheKey, e cacheEntry) {
	c.dropReach(e.reach)
}

// releaseDoc removes key, the key of a document that c.docs dropped,
// from the index of document keys.
func (c *cache) releaseDoc(key cacheKey, _ refDoc) {
	unlink(c.docKeys, docOf(key), key)
}

// A reach records the documents that one compile of a schema reached:
// the document of the schema itself, and each document that a $ref
// names. A validation with the compiled schema that loads a $ref document
// adds that document, whether or not the cache keeps the schema. The
// [cache] of the reach guards it.
type reach struct {
	// When the compile started.
	start time.Time
	// When the first URL document that the schema used falls due, or the
	// zero time for never.
	due   time.Time
	cache *cache
	docs  map[docID]struct{}
	// The file system that the files of the schema live in, or nil for
	// files on disk.
	fsys fs.FS
	// The Ref of a schema from Loadable, which names its document, or
	// the zero Ref for any other schema.
	named Ref
	key   cacheKey // the key the registry keeps the schema under
	state reachState
}

// dependencies returns a [Ref] for each document that rc records. The
// document of the schema itself comes first, and the rest follow in the
// order of their keys.
func (rc *reach) dependencies() []Ref {
	rc.cache.mu.Lock()

	ids := slices.Collect(maps.Keys(rc.docs))

	rc.cache.mu.Unlock()

	root := docOf(rc.key)

	slices.SortFunc(ids, func(a, b docID) int {
		switch {
		case a == b:
			return 0
		case a == root:
			return -1
		case b == root:
			return 1
		default:
			return cmp.Or(strings.Compare(a.uri, b.uri), cmp.Compare(a.kind, b.kind))
		}
	})

	refs := make([]Ref, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, rc.refOf(id))
	}

	return refs
}

// refOf returns the [Ref] that names the document id, a document that rc
// records. [Registry.Invalidate] reads the Ref as id again. A file on
// disk keeps the path that the registry reads, without any change, so a
// path that a $ref spells with a doubled slash keeps it too. A file in a
// file system takes the cleaned path of id, which [docOf] gives and the
// registry reads.
func (rc *reach) refOf(id docID) Ref {
	switch id.kind {
	case keyNamed:
		return rc.named

	case keyURL:
		return Ref{key: id.uri, url: true}

	default:
		path, _ := fileURLPath(id.uri)
		if rc.fsys == nil {
			return Ref{key: id.uri, file: path, abs: path}
		}

		name := strings.TrimPrefix(filepath.ToSlash(path), "/")
		if name == "" {
			name = "."
		}

		return Ref{key: id.uri, file: name, fsys: rc.fsys}
	}
}

// A reachState says whether a [cache] keeps the outcome of the compile
// that a [reach] belongs to.
type reachState int

const (
	// The compile runs, and the cache does not keep its outcome yet. The
	// index of reaches holds the reach under each of its documents, as it
	// does for a kept reach.
	reachPending reachState = iota
	// The cache keeps the outcome.
	reachKept
	// The cache does not keep the outcome, and never will. The index of
	// reaches does not hold the reach.
	reachDropped
)

// A docID names a document that the registry loads, whatever key a
// request spells it with. It holds the file system that the document
// lives in and the kind of its key, as a [cacheKey] does. It also holds
// a URI without userinfo or fragment. The URI of a file is the URL that
// [fileURL] gives for the path that the registry reads. Any other URI is
// in the normal form that [canonicalURL] gives.
//
// The kind keeps the document of a Ref from [Loadable] apart from the
// file or URL that its key spells. A release of one of the two thus
// leaves the other in the cache.
//
//nolint:unused // The fields tell the documents of the registry apart.
type docID struct {
	fs   fsID
	uri  string
	kind keyKind
}

// docOf returns the [docID] of the document that key names, with the
// kind of key. The registry reads a file URL by the path that
// [fileURLPath] finds in it. That path leaves out a query and a localhost
// authority, so every URL that reads one file gets one docID. Windows
// reads a drive letter in either case, so docOf spells it in upper case.
// The registry cleans the path of a file in a file system before it
// reads the file, as [Registry.readFile] describes, so docOf cleans that
// path too. A $ref that spells sub//defs.json in a file system thus
// names the document of sub/defs.json.
func docOf(key cacheKey) docID {
	if path, ok := docPath(key); ok {
		return docID{kind: key.kind, fs: key.fs, uri: fileURL(path)}
	}

	uri, _ := splitUserinfo(key.uri)

	return docID{kind: key.kind, fs: key.fs, uri: canonicalURL(uri)}
}

// docPath returns the path of the file that key names, in the form that
// [docOf] describes, and false when key does not name a file.
func docPath(key cacheKey) (string, bool) {
	if !isFileURL(key.uri) {
		return "", false
	}

	path, ok := fileURLPath(key.uri)
	if !ok {
		return "", false
	}

	if hasDriveLetter(path) {
		path = strings.ToUpper(path[:1]) + path[1:]
	}

	if key.fs.v != nil {
		path = filepath.FromSlash(slashpath.Clean(filepath.ToSlash(path)))
	}

	return path, true
}

// link adds v to the set that m holds under k.
func link[K, V comparable](m map[K]map[V]struct{}, k K, v V) {
	set, ok := m[k]
	if !ok {
		set = make(map[V]struct{})
		m[k] = set
	}

	set[v] = struct{}{}
}

// unlink removes v from the set that m holds under k, and removes the
// set when it is empty.
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
// passed since the failure. A value that does not record a failure can
// carry a due time, after which a new value takes its place, as
// [lru.keep] describes. It calls its release function with each
// value that it drops. It is not safe for concurrent use, and a [cache]
// guards it.
type lru[K comparable, V any] struct {
	items      map[K]*lruItem[K, V]
	release    func(K, V) // called with each value the lru drops
	recent     list.List  // the item of each key, most recently used first
	failures   list.List  // the items that record a failure, oldest first
	retryAfter time.Duration
	limit      int // the most items the lru keeps, or niceyaml.NoLimit for no limit
}

// An lruItem is a value that an [lru] keeps, with its key and its place
// in each list of the lru.
type lruItem[K comparable, V any] struct {
	// The time of the failure that the value records, or the zero time
	// for a value that does not record a failure.
	failedAt time.Time
	// The time after which a value that does not record a failure falls
	// due, or the zero time for never.
	due     time.Time
	key     K
	value   V
	use     *list.Element // the element of the item in recent
	failure *list.Element // the element of the item in failures, or nil
}

// newLRU creates a new [lru] that keeps at most limit values, or any
// number for [niceyaml.NoLimit]. The lru drops a failure retryAfter after it
// happened, and it calls release with each value that it drops.
func newLRU[K comparable, V any](limit int, retryAfter time.Duration, release func(K, V)) *lru[K, V] {
	return &lru[K, V]{
		items:      make(map[K]*lruItem[K, V]),
		release:    release,
		limit:      limit,
		retryAfter: retryAfter,
	}
}

// get returns the value that c keeps under key, with its due time, and
// marks it used. For a failure whose retry interval has passed, get drops
// the failure and reports false.
func (c *lru[K, V]) get(key K) (V, time.Time, bool) {
	item, ok := c.items[key]
	if !ok {
		var zero V

		return zero, time.Time{}, false
	}

	if c.expired(item) {
		c.remove(item)

		var zero V

		return zero, time.Time{}, false
	}

	c.recent.MoveToFront(item.use)

	return item.value, item.due, true
}

// keep keeps v, which falls due at due, under key, and returns the value
// that c then keeps with its due time. A failedAt other than the zero
// time says that v records a failure that happened then.
//
// When c already keeps a value under key that does not record a failure
// and has not fallen due, keep marks that value used and returns it in
// place of v. Every caller thus sees one value per key. A value that has
// fallen due gives way to a v that does not record a failure. A v that
// records a failure leaves that value in place, and the value then falls
// due again when the retry interval has passed since the failure.
//
// Before it adds v, keep drops each failure whose retry interval has
// passed. After it adds v, keep drops the values used least recently
// until c holds at most its limit.
func (c *lru[K, V]) keep(key K, v V, failedAt, due time.Time) (V, time.Time) {
	if item, ok := c.items[key]; ok {
		if item.failure == nil && (!isDue(item.due) || !failedAt.IsZero()) {
			if isDue(item.due) {
				item.due = failedAt.Add(c.retryAfter)
			}

			c.recent.MoveToFront(item.use)

			return item.value, item.due
		}

		c.remove(item)
	}

	c.dropExpired()

	item := &lruItem[K, V]{key: key, value: v, failedAt: failedAt, due: due}
	item.use = c.recent.PushFront(item)

	if !failedAt.IsZero() {
		item.failure = c.failures.PushBack(item)
	}

	c.items[key] = item

	for len(c.items) > c.limit {
		c.remove(itemOf[K, V](c.recent.Back()))
	}

	return v, due
}

// advance moves the due time of the value that c keeps under key to due,
// when the value does not record a failure and due comes before its own
// due time.
func (c *lru[K, V]) advance(key K, due time.Time) {
	item, ok := c.items[key]
	if !ok || item.failure != nil {
		return
	}

	if item.due.IsZero() || due.Before(item.due) {
		item.due = due
	}
}

// discard drops the value that c keeps under key, if any.
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

// isDue reports whether due, the due time of a value that an [lru]
// keeps, has passed. The zero time never passes.
func isDue(due time.Time) bool {
	return !due.IsZero() && !time.Now().Before(due)
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

// itemOf returns the item that e holds. Every element of the lists of an
// [lru] holds an [*lruItem].
func itemOf[K comparable, V any](e *list.Element) *lruItem[K, V] {
	item, _ := e.Value.(*lruItem[K, V]) //nolint:errcheck // Every element holds an item.

	return item
}

// A flightGroup runs one call at a time for each key. Every caller that
// asks for the key while the call runs shares its result. Its zero value
// is ready to use, and it is safe for concurrent use.
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

// do returns the call in flight for key, and whether do started it. When
// no call is in flight, do starts fn on a goroutine of its own and
// returns the new call. The group forgets the call when it ends, so the
// next do for key starts a new call.
//
// The call runs fn through [capture.Run]. Without it, a panic in fn ends
// the process, and a call to [runtime.Goexit] skips the close of done
// and leaves every caller blocked. With capture.Run, the call keeps
// either as an error in err. Each caller must pass err to
// [capture.Reraise].
func (g *flightGroup[K, V]) do(key K, fn func() (V, error)) (*flightCall[V], bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if c, ok := g.calls[key]; ok {
		return c, false
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

	return c, true
}
