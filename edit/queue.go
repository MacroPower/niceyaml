package edit

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// queue holds the Sets of the Apply running whose edits wait for one parse
// and one check of the text. [Doc.queues] tells which Sets wait. Each Set
// of the queue read the text that the first one found. It wrote its edits
// inside the bytes of one scalar on one line of that text. So no Set of the
// queue moves the bytes or the places that another one reads. The edits of
// all of them thus make the text that the Sets make one after the other.
// [Doc.flush] makes the edits and checks the text once, as the end of a
// call checks it. When that check fails, the Sets run again one at a time,
// as [Doc.replay] runs them, so each problem names its call.
type queue struct {
	// The record of the last edit before the first Set, and the merges and
	// the texts of the protected values of the text that it found.
	before   *editRecord
	mergesAt mergeState
	// Each node that a Set of the queue changed, as the queue found it.
	found  map[*Node]Node
	guards []string
	// The edits of the Sets that wait. They apply to the text of start,
	// which stays the current text until [Doc.flush] makes them.
	edits []edit
	sets  []queuedSet
	// The text that the first Set of the queue found.
	start state
	// Whether the check of the queue changed the model.
	modeled bool
}

// queuedSet is one Set of a [queue].
type queuedSet struct {
	ctx context.Context //nolint:containedctx // The Set runs again with the context that it took.
	v   any
	n   *Node
	// The change of the Set in the model, which waits for the check of the
	// queue.
	change func() bool
	// What the Set does, as [modelCall] names it, and the path of its
	// place.
	named string
	path  paths.Path
	// The record of the Set, as [Doc.endModel] adds it to the model.
	rec editRecord
	// The bytes of the text of the queue that the Set reads and writes.
	touched span
}

// errQueueModel is the problem of a queue whose text differs from the
// model. The Sets of the queue then run again, and the Set that makes the
// difference fails with the problem that names it.
var errQueueModel = errors.New("the text of the queued edits differs from the model")

// touches reports whether s overlaps the bytes that a Set of q reads or
// writes.
func (q *queue) touches(s span) bool {
	for _, set := range q.sets {
		if s.start < set.touched.end && set.touched.start < s.end {
			return true
		}
	}

	return false
}

// queues reports whether a Set of v through n, with opts, waits in the
// queue of the Apply running. It returns the bytes of the scalar that the
// Set replaces. Such a Set replaces a scalar on one line of a block
// collection, without an anchor or a tag, that does not read null. Its
// value is a Go value of a basic type, such as a string or an int. No
// marshaler writes the value, and the Set takes no option. The text holds
// no anchor, no alias, and no `<<` merge key, so no other place reads the
// scalar. The Set reaches no byte that a Set of the queue reaches.
func (d *Doc) queues(n *Node, v any, opts []SetOption) (span, bool) {
	switch {
	case !d.applying, d.replaying, d.queueing, n.ended, n.err != nil, n.gone, len(n.hidden) > 0,
		len(opts) > 0, len(d.cfg.marshalers) > 0, !basicValue(v):
		return span{}, false
	}

	if d.displaced(n) || !d.simpleText() {
		return span{}, false
	}

	p, err := d.nodePlace(n)
	if err != nil || !p.exists || p.root || p.bare || p.flow || p.shared || p.merged || p.anchored ||
		len(p.via) > 0 || p.end != 0 {
		return span{}, false
	}

	switch p.value.(type) {
	case *ast.StringNode, *ast.IntegerNode, *ast.FloatNode, *ast.BoolNode, *ast.InfinityNode, *ast.NanNode:
	default:
		return span{}, false
	}

	s, ok := d.nodeSpan(p.value)
	if !ok || s.start >= s.end || s.end > len(d.cur) || strings.ContainsAny(d.cur[s.start:s.end], "\r\n") {
		return span{}, false
	}

	if d.queue != nil && d.queue.touches(s) {
		return span{}, false
	}

	return s, true
}

// simpleText reports whether the current text holds no anchor, no alias,
// and no `<<` merge key, so each value has one place that reads it.
func (d *Doc) simpleText() bool {
	t := d.readRefs()

	return len(t.anchors) == 0 && len(t.aliases) == 0 && len(d.mergeMappings()) == 0
}

// basicValue reports whether v is a value of a predeclared Go type of a
// bool, a number, or a string. No such type has a method, so go-yaml's
// encoder writes it as a scalar, and no code of the program runs.
func basicValue(v any) bool {
	t := reflect.TypeOf(v)
	if t == nil || t.PkgPath() != "" || t.Name() == "" {
		return false
	}

	switch t.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

// queueSet runs a Set of v through n that [Doc.queues] lets wait, whose
// scalar holds the bytes s. The Set reads and checks the text as a Set
// does before it writes, and it returns the problems that it finds there.
// Its edits then wait in the queue. A Set that writes a value of another
// kind than a scalar, or text on more than one line, flushes the queue at
// once, as [Doc.flush] does.
func (d *Doc) queueSet(ctx context.Context, n *Node, v any, s span) error {
	q := d.queue
	if q == nil {
		q = &queue{
			start:    d.save(),
			before:   d.lastEdit,
			mergesAt: d.mergeState(),
			guards:   d.guardTexts(),
			found:    make(map[*Node]Node),
		}
		d.queue = q
	}

	d.queueing, d.deferring = true, true

	c := d.modelStart(n, "sets "+n.path.String(), true)
	mv, where, err := n.set(ctx, v, false, setConfig{}, "sets")
	mine := d.deferred

	d.queueing, d.deferring, d.deferred = false, false, nil

	if err != nil {
		// The call leaves the text that it found, which is the text of the
		// queue, so the edits of the queue wait again.
		if len(q.sets) == 0 {
			d.queue = nil
		}

		return d.modelEnd(c, err, nil)
	}

	for m, was := range d.found {
		if _, ok := q.found[m]; !ok {
			q.found[m] = was
		}
	}

	d.calling, d.found, d.callNamed = false, nil, ""
	d.endRunning()

	rec := editRecord{named: c.named}
	if d.lastEdit != nil && d.lastEdit != c.before {
		rec = *d.lastEdit
	}

	set := queuedSet{
		ctx:    ctx,
		n:      n,
		v:      v,
		change: func() bool { return c.set(mv, where) },
		rec:    rec,
		named:  c.named,
		path:   c.path,
	}

	fits := mv != nil && mv.val != nil && mv.val.kind == vScalar

	set.touched = s
	for _, e := range mine {
		if strings.ContainsAny(e.text, "\r\n") || e.end > len(d.cur) ||
			strings.ContainsAny(d.cur[e.start:e.end], "\r\n") {
			fits = false
		}

		set.touched.start, set.touched.end = min(set.touched.start, e.start), max(set.touched.end, e.end)
	}

	q.sets = append(q.sets, set)

	// A Set that writes the bytes that it replaces leaves the text, so
	// its bytes keep their origins, as [Doc.keepOrigins] keeps them. When
	// the edits of two Sets overlap, [Doc.apply] refuses them at the flush,
	// so the Sets run again one at a time.
	if !sameText(d.cur, mine) {
		q.edits = append(q.edits, mine...)
	}

	if !fits {
		d.flush()
	}

	return nil
}

// sameText reports whether each of edits writes the bytes of text that it
// replaces.
func sameText(text string, edits []edit) bool {
	for _, e := range edits {
		if e.end > len(text) || text[e.start:e.end] != e.text {
			return false
		}
	}

	return true
}

// settle flushes the queue of the Apply running, as [Doc.flush] does,
// unless a Set of the queue runs.
func (d *Doc) settle() {
	if !d.queueing {
		d.flush()
	}
}

// settleRead flushes the queue of the Apply running before a read through
// n. It does so when a Set of the queue reaches the value of n, and when
// the path of n meets a problem. A place without a value flushes the queue
// when a Set of the queue reaches the last value on the path of n, and
// that value is a scalar. The problem of the read binds at that scalar,
// and a scalar that a Set wrote adds a note that names the Set. A read of
// any other place reads the same value in the text that the queue found.
// So a Func that reads a key that a mapping might lack keeps the queue.
func (d *Doc) settleRead(n *Node) {
	q := d.queue
	if q == nil || d.queueing || n.ended || n.err != nil || n.gone {
		return
	}

	var last ast.Node

	p, err := d.walk(n.path, n.hidden, func(_ *place, cur ast.Node) bool {
		last = cur

		return true
	})
	if err == nil {
		if !p.exists {
			switch astnode.Content(last).(type) {
			case nil, *ast.MappingNode, *ast.SequenceNode:
				return
			}
		}

		if s, ok := d.nodeSpan(last); ok && !q.touches(s) {
			return
		}
	}

	d.flush()
}

// flush ends the queue of the Apply running. It makes the edits of the
// queue, parses the text once, and checks it as [Doc.endModel] checks the
// text after a call. When the check passes, the model takes the change of
// each Set. When the check fails, the Doc goes back to the text that the
// queue found. Each Set then runs again on its own, as [Doc.replay] runs
// it. The text and the problems then come out as though no Set waited.
func (d *Doc) flush() {
	q := d.queue
	if q == nil || d.queueing {
		return
	}

	d.queue = nil

	if len(q.sets) == 0 {
		return
	}

	callErr := d.callErr

	if d.checkQueue(q) == nil {
		return
	}

	d.callErr = callErr
	d.rollback(q)
	d.replay(q)
}

// checkQueue makes the edits of q and checks the text, as [Doc.endModel]
// checks the text after one call. It returns a problem when a check fails,
// and nil when the text holds the model of the Sets of q.
//
//nolint:contextcheck // The checks after a call read no state that a context carries.
func (d *Doc) checkQueue(q *queue) error {
	last := q.sets[len(q.sets)-1]

	d.callStart, d.calling, d.found, d.callNamed = q.start, true, nil, last.named
	d.mergesAt = q.mergesAt

	defer func() {
		for m, was := range d.found {
			if _, ok := q.found[m]; !ok {
				q.found[m] = was
			}
		}

		d.calling, d.found, d.callNamed = false, nil, ""
		d.endRunning()
	}()

	if len(q.edits) > 0 {
		err := d.apply(q.edits...)
		if err != nil {
			return err
		}
	}

	h := &Node{d: d, path: last.path}

	err := d.printableProblem(q.start.cur)
	if err == nil {
		err = d.guardChanged(last.path, q.guards)
	}

	if err == nil {
		err = d.keepStrings(context.Background(), h)
	}

	if err == nil {
		d.keepOrigins()

		err = d.madeMisread(last.named)
	}

	if err == nil {
		err = d.scalarProblem(h, last.named)
	}

	if err == nil {
		err = d.commentProblem(h, last.named)
	}

	if err == nil {
		err = d.mergeKept(h, last.named)
	}

	if err == nil {
		err = d.madeProblem(h, last.named)
	}

	if err != nil {
		return err
	}

	x := d.model
	if x == nil {
		return nil
	}

	q.modeled = true
	x.wrote = x.wrote[:0]

	for _, set := range q.sets {
		x.calls = append(x.calls, set.rec)

		if !set.change() {
			return errQueueModel
		}
	}

	if x.synced == d.node {
		return nil
	}

	d.alignModel(x)

	for i, w := range x.wrote {
		if w.e.seen == x.epoch {
			x.wrote[i].te = w.e.mate
		}
	}

	if _, ok := x.repeat(x.wrote); ok || len(x.diffs) > 0 {
		return errQueueModel
	}

	return nil
}

// rollback leaves the Doc as the queue q found it, after the check of the
// queue failed. The text, the removals, the last edit, and the nodes that
// the Sets changed go back to what the first Set found. When the check
// changed the model, the model starts again from that text, as after a
// call that fails.
func (d *Doc) rollback(q *queue) {
	d.show(q.start)

	d.removals = d.removals[:q.start.removals]
	d.lastEdit = q.before
	d.deferred = nil

	for m, was := range q.found {
		*m = was
	}

	if q.modeled {
		d.model = nil
	}
}

// replay runs each Set of q again on its own, in order, as a Set that does
// not wait runs. Each Set parses and checks the text that it leaves. So the
// problem of the first Set that fails names that Set, and the Apply fails
// with it. A Set that fails before it writes leaves the Doc as it found it
// too. The [Func] saw no problem from such a Set, so the Apply fails with
// that problem as well. The Apply fails whatever the Sets after the first
// failure do, so replay stops there.
//
// Each Set that runs again costs a parse and a check of the whole text. So
// replay checks the context of the Apply before each Set after the first.
// When that context has ended, the Apply fails with its error, as an Apply
// does when its context ends between two calls.
//
//nolint:contextcheck // Each Set runs again with the context that it took.
func (d *Doc) replay(q *queue) {
	d.replaying = true

	defer func() { d.replaying = false }()

	for i, set := range q.sets {
		if d.callErr != nil {
			return
		}

		if i > 0 && d.applyCtx != nil {
			err := d.applyCtx.Err()
			if err != nil {
				d.failApply(d.user.Bind(err))

				return
			}
		}

		err := set.n.Set(context.WithoutCancel(set.ctx), set.v)
		if err != nil {
			d.failApply(err)
		}
	}
}
