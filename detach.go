package niceyaml

import (
	"errors"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/excerpt"
	"go.jacobcolvin.com/niceyaml/internal/multierr"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
)

// Detach returns err with each [*SourceError] in its tree replaced by a
// detached copy. A binding keeps its [Source] alive, with every line,
// token, and parse of it. A detached copy keeps what renders the error
// and no Source. A program that keeps errors past the files they came
// from detaches each error first, as a check that reports every file at
// the end does:
//
//	for _, path := range files {
//		if err := check(path); err != nil {
//			errs = append(errs, niceyaml.Detach(err))
//		}
//	}
//
// A detached binding keeps its message, its position, its range, its
// path, and its nearest path. It keeps the reason its location did not
// resolve and the index of its document. It keeps the name and the file
// path of its source, which [SourceError.Name] and [SourceError.FilePath]
// report. [SourceError.Source] returns nil for it, and [SourceError.Node]
// and [SourceError.Document] report false, as they do for a binding in no
// document.
//
// Detach detaches each binding below a binding too. The copy keeps the
// errors the binding wraps, so [errors.Is], [errors.As], [IsInvalid], and
// the severity of each problem answer as they did for err. A detached
// result of [BindValue] stands in no document, so a document can place
// it.
//
// Detach keeps a copy of each line that the excerpts of its bindings show
// with the context lines opts set. It keeps the width opts set too, as
// [ExcerptOption] describes. Every renderer uses those options for a
// detached error, whatever options the renderer receives, so a detached
// error renders as err renders with opts. Those options hold for
// [FormatError], [NewErrorReport], [SourceError.Excerpt], and the %+v
// verb. [Annotate] marks nothing for a detached binding, whose lines
// belong to no view of a source. Detach keeps no line of a source with
// excerpts off, as [WithExcerpts] sets it.
//
// The bindings of one source share the copy that one call of Detach
// keeps, so they render together as they did in err. A program might join
// the results of separate calls, or a result and a live binding. Each
// copy of a source then renders as a source of its own, with an excerpt
// of its own. A program thus detaches the error it keeps once, after
// [Warnings.Report] adds the warnings of a collector to it. The collector
// keeps the Source of each warning it holds alive until the program drops
// the collector.
//
// Detach rebuilds the errors that this package builds around bindings,
// the joins of [errors.Join], and the wrappers of [fmt.Errorf], each with
// the message it had. Any other error that wraps a binding, such as a
// [go.jacobcolvin.com/niceyaml/manifest.LoadError], keeps the Source of
// that binding. Detach keeps such an error as it is, so [errors.Is] and
// [errors.As] find it in the result as they find it in err. An error
// of go-yaml in the chain, such as the one behind [ErrSyntax], holds a
// token of the parse. That token links to the other tokens of the parse
// and keeps them alive.
//
// Detach returns err itself when it detaches no binding of err, such as
// when err holds no binding or only detached ones. It returns nil for a
// nil err, and for a nil [*Error] or [*SourceError] pointer.
func Detach(err error, opts ...ExcerptOption) error {
	if isNothing(err) {
		return nil
	}

	d := &detacher{
		cfg:      newExcerptConfig(opts),
		bindings: make(map[*SourceError]*SourceError),
		rebuilt:  make(map[error]error),
		sources:  make(map[*Source]*keptSource),
		docs:     make(map[*Node]*keptDocument),
	}

	detached, _ := d.walk(err)
	d.keepLines()

	return detached
}

// detacher detaches the bindings of one error, as [Detach] describes.
type detacher struct {
	// The detached copy of each binding the walk met, or the binding itself
	// when it does not need a copy.
	bindings map[*SourceError]*SourceError
	// The copy of each error that the walk rebuilt and that a pointer
	// stands for. An error the tree reaches twice thus has one copy.
	rebuilt map[error]error
	// What the detached bindings keep of each Source, one per Source.
	sources map[*Source]*keptSource
	// What the detached bindings keep of each document, one per root.
	docs map[*Node]*keptDocument
	// The bindings the walk detached, whose marks decide the lines that
	// each source keeps.
	met []*SourceError
	cfg excerptConfig
}

// walk returns err with each binding in its tree detached, and whether
// that changed err. An error the walk cannot rebuild comes back as it is.
func (d *detacher) walk(err error) (error, bool) {
	if isNothing(err) {
		return err, false
	}

	// Only a pointer keys the map, because a key of a type that does not
	// compare panics.
	keyed := reflect.TypeOf(err).Kind() == reflect.Pointer
	if keyed {
		if done, ok := d.rebuilt[err]; ok {
			return done, true
		}
	}

	out, changed := d.rebuild(err)
	if changed && keyed {
		d.rebuilt[err] = out
	}

	return out, changed
}

// rebuild returns a copy of err with each binding below it detached, as
// [detacher.walk] describes.
func (d *detacher) rebuild(err error) (error, bool) {
	switch x := err.(type) { //nolint:errorlint // Walks the tree one node at a time.
	case *SourceError:
		return d.binding(x)

	case *Error:
		return d.error(x)

	case *markedError:
		inner, changed := d.walk(x.err)
		if !changed {
			return err, false
		}

		c := *x
		c.err = inner

		return &c, true
	}

	joined, several := err.(interface{ Unwrap() []error }) //nolint:errorlint // The error itself, not a chain search.

	switch {
	case multierr.IsBuilt(err):
		// The copy of a stand-in holds what the walk makes of the error it
		// stands in for. It keeps that error of the program only when the
		// walk cannot rebuild it.
		return multierr.Map(err, d.walk)

	case several && multierr.IsJoin(err):
		branches, changed := d.walkAll(joined.Unwrap())
		if !changed {
			return err, false
		}

		return errors.Join(branches...), true

	case several && multierr.IsWrapErrors(err):
		branches, changed := d.walkAll(joined.Unwrap())
		if !changed {
			return err, false
		}

		return multierr.WrapErrors(branches, err.Error()), true

	case multierr.IsWrap(err):
		inner, changed := d.walk(errors.Unwrap(err))
		if !changed {
			return err, false
		}

		return multierr.Wrap(inner, err.Error()), true

	default:
		return err, false
	}
}

// walkAll returns errs with each walked as [detacher.walk] walks it, and
// whether that changed any of them. It returns errs itself when it
// changed none.
func (d *detacher) walkAll(errs []error) ([]error, bool) {
	var out []error

	for i, err := range errs {
		walked, changed := d.walk(err)
		if changed && out == nil {
			out = slices.Clone(errs)
		}

		if out != nil {
			out[i] = walked
		}
	}

	if out == nil {
		return errs, false
	}

	return out, true
}

// error returns a copy of x with the errors it wraps, the problems it
// heads, and its details walked.
func (d *detacher) error(x *Error) (error, bool) {
	inner, wraps := d.walk(x.err)
	members, heads := d.walkAll(x.errors)
	details, explains := d.walkAll(x.details)

	if !wraps && !heads && !explains {
		return x, false
	}

	c := *x
	c.err, c.errors, c.details = inner, members, details

	return &c, true
}

// binding returns the detached copy of x, and whether x needed one. The
// copy keeps the location x resolved and drops its source, its node, and
// the token its path resolved to. A detached x comes back as it is when
// every binding below it is detached too.
func (d *detacher) binding(x *SourceError) (*SourceError, bool) {
	if c, ok := d.bindings[x]; ok {
		return c, c != x
	}

	inner, wraps := d.walk(x.err)
	free, frees := d.walk(x.free)
	locErr, reasons := d.walk(x.locErr)
	unfollowed, aliases := d.walk(x.loc.unfollowed)
	below, heads := d.children(x.below)

	var (
		lead  *SourceError
		leads bool
	)

	found := x.passedLead()
	if found != nil {
		lead, leads = d.binding(found)
	}

	if x.kept != nil && !wraps && !frees && !reasons && !aliases && !heads && !leads {
		d.bindings[x] = x

		return x, false
	}

	c := &SourceError{
		err:     inner,
		free:    free,
		locErr:  locErr,
		lead:    lead,
		kept:    x.kept,
		keptDoc: x.keptDoc,
		loc:     x.loc,
		end:     x.end,
		ranges:  x.ranges,
		below:   below,
		rng:     x.rng,
		adopted: x.adopted,
	}

	c.loc.tk = nil
	c.loc.unfollowed = unfollowed
	c.end.err = d.mapped(x.end.err)

	// The lead of x stands for the lead of the copy, so the copy finds none
	// of its own.
	c.leadOnce.Do(func() {})

	if c.kept == nil {
		c.kept, c.keptDoc = d.keep(x)
		d.met = append(d.met, x)
	}

	d.bindings[x] = c

	return c, true
}

// children returns below, the children of a binding, with each bound
// child detached, and whether that changed any of them.
func (d *detacher) children(below []boundChild) ([]boundChild, bool) {
	var out []boundChild

	for i, child := range below {
		bound, changed := d.binding(child.bound)
		if changed && out == nil {
			out = slices.Clone(below)
		}

		if out != nil {
			out[i].bound = bound
		}
	}

	if out == nil {
		return below, false
	}

	return out, true
}

// mapped returns the copy the walk made of err, or err itself when the
// walk made none.
func (d *detacher) mapped(err error) error {
	if isNothing(err) || reflect.TypeOf(err).Kind() != reflect.Pointer {
		return err
	}

	if x, ok := err.(*SourceError); ok { //nolint:errorlint // The error itself, not a chain search.
		if c, done := d.bindings[x]; done {
			return c
		}
	}

	if done, ok := d.rebuilt[err]; ok {
		return done
	}

	return err
}

// keep returns what a detached copy of x keeps of the source and the
// document x is bound in. The copies of the bindings of one source share
// what they keep of it, and so do those of one document.
func (d *detacher) keep(x *SourceError) (*keptSource, *keptDocument) {
	src, ok := d.sources[x.source]
	if !ok {
		src = &keptSource{
			name:     x.source.Name(),
			filePath: x.source.FilePath(),
			shows:    x.source.ShowsExcerpts(),
			cfg:      d.cfg,
		}

		d.sources[x.source] = src
	}

	if x.node == nil {
		return src, nil
	}

	root := x.node.Document()

	doc, ok := d.docs[root]
	if !ok {
		doc = &keptDocument{index: root.DocumentIndex(), label: documentName(x.node)}
		d.docs[root] = doc
	}

	return src, doc
}

// keepLines copies, for each source the walk detached bindings of, the
// lines that the excerpts of those bindings show.
func (d *detacher) keepLines() {
	sources, positions := treePositions(d.met, true, nil, nil)

	for _, origin := range sources {
		src := origin.live()
		if src == nil {
			continue
		}

		if kept := d.sources[src]; kept != nil {
			kept.keep(src, positions[origin])
		}
	}
}

// keptSource is what a detached binding keeps of the [Source] it was bound
// in. It holds the name, the file path, and the [WithExcerpts] setting of
// the Source. It also holds the options [Detach] received and a copy of
// the lines that the excerpts of its bindings show. One call of Detach
// makes one keptSource per Source, which every binding it detaches from
// that Source shares.
type keptSource struct {
	name     string
	filePath string
	// A copy of each line an excerpt shows, in the order of the source.
	// An empty line stands in for the lines between two copies that are
	// not next to each other in the source.
	lines line.Lines
	// The index in the source of each copied line, ascending.
	rows []int
	// The index in lines of the copy of each line of rows.
	at  []int
	cfg excerptConfig
	// The source shows its text in an excerpt, as [WithExcerpts] sets it.
	shows bool
}

// Name returns the name the Source had, as [Source.Name] returned it.
func (k *keptSource) Name() string {
	return k.name
}

// live returns nil, because k holds no Source.
func (*keptSource) live() *Source {
	return nil
}

// keep copies the lines of src that an excerpt with marks shows, with the
// context lines of the options of k.
func (k *keptSource) keep(src *Source, marks []excerpt.Mark) {
	if !k.shows {
		return
	}

	view := src.View()

	if !excerpt.Annotate(view, src.lines, marks) {
		return
	}

	for i := range view.Hunks(k.cfg.context).All() {
		k.rows = append(k.rows, i)
	}

	k.lines, k.at = copyLines(src.lines, k.rows)
}

// excerpt returns a fresh view of the lines k kept, with marks on them,
// cut to hunks and clipped as the options of k set, whatever options the
// caller passes. The marks lie in the lines of the source, which k does
// not hold, so each moves to the lines k kept first.
func (k *keptSource) excerpt(marks []excerpt.Mark, _ excerptConfig) (*line.View, bool) {
	if !k.shows {
		return nil, false
	}

	moved := make([]excerpt.Mark, 0, len(marks))
	for _, m := range marks {
		moved = append(moved, k.move(m))
	}

	view := line.NewView(k.lines)

	if !excerpt.Annotate(view, k.lines, moved) {
		return nil, false
	}

	return view.Hunks(k.cfg.context).Clip(k.cfg.width), true
}

// move returns m, a mark in the lines of the source, in the lines k kept.
// A position on a line k did not keep moves to line -1, which marks
// nothing. Each range becomes one range per line k kept, with the columns
// [line.Lines.SliceLines] gives the range on that line.
func (k *keptSource) move(m excerpt.Mark) excerpt.Mark {
	moved := excerpt.Mark{Message: m.Message, Warning: m.Warning, Pos: position.New(-1, m.Pos.Col)}

	if n, ok := slices.BinarySearch(k.rows, m.Pos.Line); ok {
		moved.Pos.Line = k.at[n]
	}

	for _, r := range m.Ranges {
		last := r.LastLine()
		from, _ := slices.BinarySearch(k.rows, r.Start.Line)

		for n := from; n < len(k.rows) && k.rows[n] <= last; n++ {
			i, j := k.rows[n], k.at[n]
			start, end := 0, k.lines.Line(j).Width()

			if i == r.Start.Line {
				start = max(0, r.Start.Col)
			}

			if i == r.End.Line {
				end = min(end, r.End.Col)
			}

			if end > start {
				moved.Ranges = append(moved.Ranges, position.NewRange(position.New(j, start), position.New(j, end)))
			}
		}
	}

	return moved
}

// keptDocument is what a detached binding keeps of the document it was
// bound in.
type keptDocument struct {
	// The label that the message of a binding with no position names the
	// document with, as "document 3", or "" in a file of one document.
	label string
	// The index of the document among every document of its file.
	index int
}

// copyLines returns copies of the lines of src at held, which ascend, and
// the index of each copy in the result. An empty line stands in for each
// run of lines between two of held. The hunks of a view of the result
// thus stay apart where those of the source do.
func copyLines(src line.Lines, held []int) (line.Lines, []int) {
	var c lineCopier

	out := make([]*line.Line, 0, len(held))
	at := make([]int, 0, len(held))

	for n, i := range held {
		if n > 0 && i > held[n-1]+1 {
			out = append(out, new(line.Line))
		}

		at = append(at, len(out))
		out = append(out, c.copy(src.Line(i)))
	}

	return line.Collect(out...), at
}

// lineCopier copies lines of a source onto clones of their tokens.
type lineCopier struct {
	// The token that stands in for every neighbor of one type.
	stubs map[token.Type]*token.Token
}

// copy returns a copy of ln that holds clones of its tokens. The tokens of
// ln link to the rest of their source through Next and Prev, and the
// clones do not. Each clone links instead to tokens that carry only the
// types of the neighbors its token has in the source. [line.Line.Kind]
// reads those types, so the copy renders as ln does. A line the clones do
// not rebuild becomes one token of plain text with the text of ln.
func (c *lineCopier) copy(ln *line.Line) *line.Line {
	parts := ln.Tokens()
	sources := ln.SourceTokens()

	tks := make(token.Tokens, 0, len(parts)+1)

	for n, part := range parts {
		clone := part.Clone()
		clone.Prev, clone.Next = nil, nil

		// Each part of a line comes from its own source token, so when the
		// counts match, part n pairs with source token n.
		if len(sources) == len(parts) {
			clone.Prev = c.stub(sources[n].Prev)
			clone.Next = c.stub(significant(sources[n].Next))
		}

		tks = append(tks, clone)
	}

	if copied, ok := rebuildLine(tks, ln); ok {
		return copied
	}

	text := ln.Content()
	plain := &token.Token{
		Type:     token.StringType,
		Value:    text,
		Origin:   text + "\n",
		Position: &token.Position{Line: ln.Number(), Column: 1, Offset: 1},
	}

	copied, _ := rebuildLine(token.Tokens{plain}, ln)

	return copied
}

// stub returns a token of the type of tk that links to nothing, or nil
// for a nil tk.
func (c *lineCopier) stub(tk *token.Token) *token.Token {
	if tk == nil {
		return nil
	}

	if c.stubs == nil {
		c.stubs = make(map[token.Type]*token.Token)
	}

	s, ok := c.stubs[tk.Type]
	if !ok {
		s = &token.Token{Type: tk.Type}
		c.stubs[tk.Type] = s
	}

	return s
}

// significant returns the first token from tk on that is not a comment,
// or nil when the chain ends first.
func significant(tk *token.Token) *token.Token {
	for tk != nil && tk.Type == token.CommentType {
		tk = tk.Next
	}

	return tk
}

// rebuildLine returns the first line that [line.NewLines] cuts from tks,
// the tokens of ln, and whether it holds the number and the text of ln. A
// token of text on the line after ln closes the line. The cut thus always
// holds a line, and a line of whitespace alone keeps its number.
func rebuildLine(tks token.Tokens, ln *line.Line) (*line.Line, bool) {
	closing := &token.Token{
		Type:     token.StringType,
		Value:    "-",
		Origin:   "-",
		Position: &token.Position{Line: ln.Number() + 1, Column: 1, Offset: 1},
	}

	got := line.NewLines(append(tks, closing)).Line(0)

	return got, got.Number() == ln.Number() && got.Content() == ln.Content()
}
