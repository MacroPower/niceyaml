package aliasing

import (
	"sync"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

// ReferenceWeights keeps what the alias limit and the [Budget] weigh for
// the anchors of reference documents, such as those of
// niceyaml.WithReferences. A document resolves an alias against its
// reference documents when it does not define an anchor of that name
// before the alias. [CheckDecode] and [CheckDecodeText] weigh such an
// alias by the weight of its name.
//
// The weight of a name holds what one alias of the name repeats, in nodes
// for each way that the decoder reads it and in text for the Budget. An
// alias under a reference anchor reads the reference documents by name
// too, so the weight of the anchor holds the weight of that name.
//
// The decoder also reads the anchor itself, once for each document whose
// aliases reach it. The weight holds what that read adds, with the anchors
// inside the anchor. The aliases of a document can reach an anchor and an
// anchor inside it. The document then counts only the read of the outer
// anchor, so each node of the reference documents counts once.
//
// The decoder can read one of several anchors for a name, and the
// function that [NewReferenceWeights] takes lists them. The weight takes
// the largest of each measure among them, so it is an upper estimate.
//
// The anchors of reference documents can reach each other by name in a
// loop, as `&x [*y]` and `&y [*x]` do. An anchor that holds an alias of
// its own name, as `&m {k: *m}` does, is a loop of one name. The content
// that an alias of such a name stands for does not end. An alias that
// closes a loop of names therefore weighs [aliaslimit.CountCap], and a
// document whose aliases reach the loop is past the limit.
//
// What a read does at a loop of one name depends on the reader. A decode
// into any that reads the anchor on its own takes the alias inside it as
// null and ends. A decode into a Go type follows that alias once for each
// level of the type, and a `<<` merge of the anchor follows it too. A
// decode into a recursive Go type or into a type that reads text might
// not end. The weights hold one verdict for the loop, so a document that
// reaches it is past the limit for every reader.
//
// What a decode does at a loop of more names depends on where the
// anchors of the loop lie. The decoder rejects the alias that closes a
// loop of anchors that lie apart, as x and y do. With
// `&a {b: &b [*a], c: *b}`, the anchor b of the loop lies inside the
// anchor a. The decoder rejects a document whose aliases reach that loop
// first at a. A decode into any ends for a document whose aliases reach
// the loop first at b. A decode of that document into a recursive Go
// type might not end.
//
// A loop counts when it closes through any anchor that the function lists
// for a name. With `&o {x: &x [*y]}`, `&y [*x]`, and a last `&x 1`, the
// loop closes only through the anchor x inside o. A decode that reads o
// registers that anchor, and its read of an alias *y might then not end.
// A decode that does not read o reads `&x 1` for the name and ends. The
// weights depend on the reference documents alone, so they hold one
// verdict for both decodes. The document of each decode is past the
// limit.
//
// The weights depend on the reference documents alone, so every document
// that reads those documents shares them, and a ReferenceWeights weighs
// each name once. It also keeps what it found under each anchor. The
// weight of a name whose anchor lies around that anchor takes what it
// kept. It does not read the nodes under the inner anchor again. A
// ReferenceWeights thus reads each node of the reference documents a
// fixed number of times, however deep the anchors nest and however the
// anchors reach each other.
//
// A ReferenceWeights is safe for concurrent use. A nil ReferenceWeights
// does not hold any anchors.
//
// Create instances with [NewReferenceWeights].
type ReferenceWeights struct {
	// Returns the anchors that an alias of a name can read. The anchor
	// that the reference documents hold for the name comes first.
	anchors func(name string) []*ast.AnchorNode
	// The weight of each name that a call has weighed, or nil for a name
	// without an anchor.
	weights map[string]*refWeight
	// The names whose anchors a call is weighing.
	open map[string]bool
	// What one walk found under each anchor that a call has read.
	reads map[*ast.AnchorNode]*refAnchor
	// How many items of each anchor a call has listed, as
	// [ReferenceWeights.nextRead] lists them. The count is one more than
	// the items of an anchor whose aliases a call has listed at every
	// depth.
	listed map[*refAnchor]int
	// What the counters of the calls found for each anchor.
	kept anchorCounts
	mu   sync.Mutex
}

// NewReferenceWeights creates new [*ReferenceWeights] for the reference
// documents whose anchors the function anchors finds. The function
// returns the anchors that an alias of name can read, or nil when the
// reference documents do not define the name. The first of them is the
// anchor that the decoder reads before it decodes a document whose aliases
// reach the name. The function must return the same anchors for a name
// at every call.
func NewReferenceWeights(anchors func(name string) []*ast.AnchorNode) *ReferenceWeights {
	return &ReferenceWeights{
		anchors: anchors,
		weights: map[string]*refWeight{},
		open:    map[string]bool{},
		reads:   map[*ast.AnchorNode]*refAnchor{},
		listed:  map[*refAnchor]int{},
		kept: anchorCounts{
			counts:  map[anchorCount]counted{},
			extents: map[*ast.AnchorNode]extent{},
		},
	}
}

// refAnchor is an anchor of a reference document with what one walk of
// its value found. The walk stops at each anchor inside the value,
// because the refAnchor of that anchor holds what lies under it. Each
// node under an anchor thus belongs to the walk of one anchor, however
// deep the anchors nest. A refAnchor does not change after
// [ReferenceWeights.read] returns it.
type refAnchor struct {
	anchor *ast.AnchorNode
	// The first of the tokens of anchor with the lowest offset and the
	// first of those with the highest, as [astnode.Bounds] orders them.
	first, last *token.Token
	// The aliases and the anchors under anchor and outside every anchor
	// inside it, in document order. An alias appears at the first alias
	// of its name among them.
	items []refItem
	// The text of the value of anchor as the document spells it.
	text spelling
}

// refItem is an alias or an anchor under a [refAnchor].
type refItem struct {
	// The anchor, or nil for an alias.
	inner *refAnchor
	// The name that the alias reads.
	name string
}

// content returns the anchor whose value is the content that an alias to
// a reads. That anchor is a itself, or the innermost of the anchors that
// lie on each other under a, as in `&a &b x`.
func (a *refAnchor) content() *refAnchor {
	for {
		if _, ok := a.anchor.Value.(*ast.AnchorNode); !ok {
			return a
		}

		a = a.items[0].inner
	}
}

// appendInner appends the anchors directly inside a to anchors.
func (a *refAnchor) appendInner(anchors []*refAnchor) []*refAnchor {
	for _, item := range a.items {
		if item.inner != nil {
			anchors = append(anchors, item.inner)
		}
	}

	return anchors
}

// read returns the [*refAnchor] of anchor. The first call for an anchor
// walks its value, and reads each anchor inside the value the same way.
func (r *ReferenceWeights) read(anchor *ast.AnchorNode) *refAnchor {
	if a, ok := r.reads[anchor]; ok {
		return a
	}

	var (
		a     = &refAnchor{anchor: anchor}
		value astnode.BoundsFinder
		named = map[string]bool{}
		// The text of the value of each anchor directly inside anchor,
		// by its first token.
		inner = map[*token.Token]spelling{}
	)

	astnode.Inspect(anchor.Value, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AnchorNode:
			child := r.read(n)
			a.items = append(a.items, refItem{inner: child})

			value.Add(child.first)
			value.Add(child.last)

			if child.text.start != nil {
				inner[child.text.start] = child.text
			}

			return false

		case *ast.AliasNode:
			if name, ok := astnode.Name(n.Value); ok && !named[name] {
				named[name] = true
				a.items = append(a.items, refItem{name: name})
			}
		}

		return value.Visit(node)
	})

	first, last := value.Bounds()
	a.text = spellingOf(first, last, inner)

	// The walk of anchor visits its own token, its comment, and its name
	// before its value.
	var whole astnode.BoundsFinder

	astnode.Inspect(anchor, func(node ast.Node) bool {
		return node != anchor.Value && whole.Visit(node)
	})

	if len(first) > 0 && len(last) > 0 {
		whole.Add(first[0])
		whole.Add(last[0])
	}

	if first, last := whole.Bounds(); len(first) > 0 && len(last) > 0 {
		a.first, a.last = first[0], last[0]
	}

	r.reads[anchor] = a

	return a
}

// refWeight is what the anchors of one name of the reference documents
// weigh, as [ReferenceWeights] describes.
type refWeight struct {
	// The anchor that the reference documents hold for the name. The
	// decoder reads the anchors that its aliases name before it reads
	// that anchor.
	read *refAnchor
	// The extent of what one alias of the name writes out.
	extent extent
	// The nodes that one alias of the name repeats in each read mode.
	sizes [readModes]int
	// The nodes that one read of that anchor adds in each read mode, with
	// the nodes of the anchors inside it. A counter adds the same nodes
	// for the definition of an anchor.
	distinct [readModes]int
	// The nodes that the aliases under that anchor repeat in that read.
	aliased [readModes]int
	// Whether what an alias of the name reads has each [trait].
	traits [traits]bool
	// Whether an alias of the name reads more than a shared scalar where
	// the decoder reads the alias into a value.
	repeats bool
}

// loopWeight is the weight of an alias that closes a loop of names.
//
//nolint:gochecknoglobals // Every counter reads it, and none writes to it.
var loopWeight = &refWeight{
	sizes: [readModes]int{aliaslimit.CountCap, aliaslimit.CountCap},
	extent: extent{
		depth:        aliaslimit.CountCap,
		units:        aliaslimit.CountCap,
		lines:        aliaslimit.CountCap,
		levels:       aliaslimit.CountCap,
		spelled:      aliaslimit.CountCap,
		spelledLines: aliaslimit.CountCap,
	},
	traits:  [traits]bool{true, true, true},
	repeats: true,
}

// widen grows what one alias weighs in w to hold what one weighs in o as
// well.
func (w *refWeight) widen(o *refWeight) {
	for mode := range w.sizes {
		w.sizes[mode] = max(w.sizes[mode], o.sizes[mode])
	}

	for t := range w.traits {
		w.traits[t] = w.traits[t] || o.traits[t]
	}

	w.repeats = w.repeats || o.repeats

	w.extent.widen(o.extent)
}

// weight returns the weight of name, or nil when the reference documents
// do not hold an anchor for name. The first call for a name weighs it,
// with every name that its anchors reach.
//
// A stack holds the names on the way from name to the name that the call
// weighs. A long chain of anchors thus stays out of the call stack. The
// call weighs a name after the names that the aliases under its anchors
// read, as [ReferenceWeights.nextRead] lists them. A name on the stack is
// open. An alias that reads an open name closes a loop of names, which
// [ReferenceWeights.weighed] reports.
func (r *ReferenceWeights) weight(name string) *refWeight {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if w, ok := r.weights[name]; ok {
		return w
	}

	for stack := []*weighing{r.begin(name)}; len(stack) > 0; {
		top := stack[len(stack)-1]

		if read, ok := r.nextRead(top); ok {
			stack = append(stack, r.begin(read))

			continue
		}

		r.weights[top.name] = r.finish(top)
		delete(r.open, top.name)

		stack = stack[:len(stack)-1]
	}

	return r.weights[name]
}

// lookup returns a function that returns the weight of a name, as
// [ReferenceWeights.weight] does. The check of a document calls the
// function at every alias that reads the reference documents. The
// function keeps each weight that it has returned, so it takes the lock
// of r one time for each name. The checks of many documents can read one
// ReferenceWeights at the same time. A lock at every alias makes each of
// those checks wait for the others. Only one goroutine at a time can
// call the function.
func (r *ReferenceWeights) lookup() func(name string) *refWeight {
	known := map[string]*refWeight{}

	return func(name string) *refWeight {
		w, ok := known[name]
		if !ok {
			w = r.weight(name)
			known[name] = w
		}

		return w
	}
}

// weighing is a name on the stack of [ReferenceWeights.weight].
type weighing struct {
	name string
	// The anchors that an alias of the name can read.
	candidates []*refAnchor
	// The anchors whose aliases the call is listing for the name. The
	// first is a candidate, and each of the others lies directly inside
	// the one before it.
	path []*refAnchor
	// The index of the next candidate.
	next int
}

// begin opens name and returns its place on the stack.
func (r *ReferenceWeights) begin(name string) *weighing {
	r.open[name] = true

	w := &weighing{name: name}
	for _, anchor := range r.anchors(name) {
		w.candidates = append(w.candidates, r.read(anchor))
	}

	return w
}

// nextRead returns the next name that an alias under the candidates of w
// reads and that a call has neither weighed nor opened. It reports false
// after the last such name. A call has then weighed or opened every name
// that an alias under the candidates reads. Every alias under a candidate
// reads the anchors of its name, an alias inside an anchor of its own
// name too. The name of such an alias is open while a call weighs its
// anchors, so the alias closes a loop of one name.
//
// The calls list the items of each anchor one time, whatever name reaches
// the anchor. The listed count of an anchor holds how many of its items a
// call has listed. A call has weighed or opened the name of each listed
// alias. So the lists read each alias of the reference documents once,
// however many anchors lie around the alias.
//
// Another name on the stack can be in the middle of the list of an
// anchor under a candidate. The last listed item of such an anchor is the
// alias whose name is open, or an anchor inside that is in the middle of
// its list. The call goes on with those lists from where they stand.
func (r *ReferenceWeights) nextRead(w *weighing) (string, bool) {
	for {
		if len(w.path) == 0 {
			if w.next == len(w.candidates) {
				return "", false
			}

			w.path = append(w.path, w.candidates[w.next])
			w.next++

			continue
		}

		at := w.path[len(w.path)-1]
		listed := r.listed[at]

		// The last listed item can be an anchor whose own list stands at
		// its start or in its middle. The call ends that list first.
		if listed > 0 && listed <= len(at.items) {
			inner := at.items[listed-1].inner
			if inner != nil && r.listed[inner] <= len(inner.items) {
				w.path = append(w.path, inner)

				continue
			}
		}

		if listed >= len(at.items) {
			r.listed[at] = len(at.items) + 1
			w.path = w.path[:len(w.path)-1]

			continue
		}

		r.listed[at] = listed + 1

		item := at.items[listed]
		if item.inner != nil {
			continue
		}

		if _, ok := r.weights[item.name]; !ok && !r.open[item.name] {
			return item.name, true
		}
	}
}

// finish returns the weight of the name of w, or nil when the name does
// not have an anchor. One alias of the name weighs the largest of what it
// weighs for each candidate. The read of the anchor that the reference
// documents hold for the name, the first candidate, counts as the
// definition of an anchor counts in its own document.
func (r *ReferenceWeights) finish(w *weighing) *refWeight {
	var weight *refWeight

	for i, candidate := range w.candidates {
		counter := newTreeCounter(nil)
		counter.reference = r.weighed
		counter.kept = &r.kept

		content := counter.contentWeight(candidate)

		if i > 0 {
			weight.widen(content)

			continue
		}

		weight = content
		weight.read = candidate

		for mode := range readModes {
			counter.distinct, counter.aliased = 0, 0
			counter.count(candidate.anchor, true, mode)

			weight.distinct[mode], weight.aliased[mode] = counter.distinct, counter.aliased
		}
	}

	return weight
}

// weighed returns the weight of name for a counter that reads an anchor
// of a name that [ReferenceWeights.finish] weighs. A call has weighed or
// opened every name that an alias under such an anchor reads. It returns
// nil for a name without an anchor. It returns [loopWeight] for a name
// without a weight, which is open, so its alias closes a loop of names.
func (r *ReferenceWeights) weighed(name string) *refWeight {
	if w, ok := r.weights[name]; ok {
		return w
	}

	return loopWeight
}

// anchorCounts keeps what the counters of a [ReferenceWeights] found for
// each anchor of the reference documents. A counter reads the nodes under
// an anchor one time for each way that it reads the anchor. Where the
// anchor lies inside other anchors, the counters of those anchors take
// what it found. The counts thus read each node of the reference
// documents a fixed number of times, however deep the anchors nest.
//
// A counter can read an alias of an open name under an anchor, and it
// then keeps [loopWeight] as a part of what it found. The open name lies
// on a loop of names, so the weight that it gets counts the same nodes
// and the same text as loopWeight. A later counter that takes the result
// thus gets what it counts for the anchor itself.
type anchorCounts struct {
	counts  map[anchorCount]counted
	extents map[*ast.AnchorNode]extent
}

// anchorCount names one way that a counter reads an anchor. The fields
// are the arguments of [treeCounter.count], and together they form the
// key of the counts of [anchorCounts].
//
//nolint:unused // The fields tell the keys of the map apart.
type anchorCount struct {
	anchor *ast.AnchorNode
	mode   readMode
	top    bool
}

// counted is what [treeCounter.count] found for an [anchorCount].
type counted struct {
	// The number of nodes that count returned.
	size int
	// The nodes that count added to the distinct and the aliased nodes.
	distinct, aliased int
}

// contentWeight returns what one alias weighs when it reads read, an
// anchor of a reference document, without what the read of the anchor
// itself adds. An anchor on an alias, as in `&a *b`, does not define
// content of its own, so its alias weighs what the alias under the anchor
// weighs.
func (c *treeCounter) contentWeight(read *refAnchor) *refWeight {
	read = read.content()
	content := read.anchor.Value

	if alias, ok := content.(*ast.AliasNode); ok {
		_, ref, ok := c.resolve(alias)
		if !ok {
			return &refWeight{sizes: [readModes]int{1, 1}, extent: leafExtent}
		}

		return &refWeight{sizes: ref.sizes, extent: ref.extent, traits: ref.traits, repeats: ref.repeats}
	}

	w := &refWeight{extent: c.measure(read.anchor), repeats: c.repeatsContent(content)}
	read.text.spell(&w.extent)

	for mode := range readModes {
		w.sizes[mode] = c.count(read.anchor, false, mode)
	}

	for t := range traits {
		w.traits[t] = c.reaches(content, t)
	}

	return w
}
