package edit

import (
	"cmp"
	"errors"
	"fmt"
	"iter"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/lineend"
	"go.jacobcolvin.com/niceyaml/internal/pathshook"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// note is where the text that an edit wrote binds its errors in the user's
// text, and what it says about the edit.
type note struct {
	// The header that an edit wrote for the block scalar whose header the
	// text is, when [Doc.keepStrings] wrote the text in its place, or nil.
	header *run
	// The text of a block scalar that [Doc.writeString] replaced with the
	// text, or nil.
	text *run
	// The subject of the Apply that wrote a value, as messages name it. It
	// is "" for text that does not hold a value, and for text that stands
	// for text of the user, such as a renamed key.
	detail string
	// What the call that wrote the value does, with the paths that the call
	// was given, as in "sets $.a" or "moves $.a to $.b", which a detail that
	// names the call names, for a note with a detail.
	call string
	// The string that the block scalar whose header the text is keeps, as
	// [Doc.keepStrings] keeps it, and whether the note holds one.
	str  string
	path paths.Path
	at   position.Range
	// The origin of the note of the key or the "-" that held the value
	// before a move put it in a flow sequence, where an item has neither,
	// or 0 for none. A move out of the flow sequence writes a key or a "-"
	// that stands for that note again.
	held int
	// Whether the note names a value that the file holds. An error then
	// binds there.
	located bool
	hasPath bool
	// Whether a move wrote the text for the key or the "-" that held its
	// value. The check of merges counts such a key as an entry of its own.
	moved bool
	// Whether the text replaced a value. The detail then says that the
	// Apply set the value from the one that the file holds.
	replaced bool
	// Whether the text lies in a value that replaced a value, outside its
	// token. The text there holds the values that the Apply created inside
	// the new value, and the text around them.
	inside bool
	// Whether the note stands for an item of a flow sequence of the user's
	// text. Such an item has no key or "-", so the note binds at the item.
	flowItem bool
	hasStr   bool
}

// lineStarts returns the offset of each line of text. A line ends where
// the parser ends one, at a "\n", a "\r\n", or a "\r" alone, as
// [lineend.Lines] cuts it.
func lineStarts(text string) []int {
	starts := []int{0}
	off := 0

	for ln := range lineend.Lines(text) {
		off += len(ln)
		if endsLine(ln) {
			starts = append(starts, off)
		}
	}

	return starts
}

// endsLine reports whether text ends with a line break.
func endsLine(text string) bool {
	return strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\r")
}

// isBreak reports whether the byte at off of text ends a line: a "\n", or
// a "\r" that no "\n" follows.
func isBreak(text string, off int) bool {
	switch text[off] {
	case '\n':
		return true
	case '\r':
		return off+1 == len(text) || text[off+1] != '\n'
	default:
		return false
	}
}

// parse parses the current text with its tail, as the parser reads the
// document in its file, and records its lines. The problem of a text that
// go-yaml's parser refuses names the parser and gives its message and its
// place. The engine wrote that text, so the problem does not keep the
// fault of the document that the parse declares. A refusal at a byte that
// an edit created keeps the path of the site where it binds, as
// [Doc.locate] gives it. The caller binds the problem as a problem of the
// engine, which adds [ErrEngine].
func (d *Doc) parse() error {
	// A refusal of the parser is a problem of the edit, so it shows the
	// path of its site. The caller binds it again, and the site of the
	// refusal comes with it, as [placedError] holds it.
	own, held := d.own, d.capture
	d.own, d.capture = true, &capture{}

	defer func() { d.own, d.capture = own, held }()

	//nolint:errcheck // The hook gives a *niceyaml.Source.
	src, _ := hook.WorkingSource(d.cur+d.tail, d.user, d.cfg.unmarshalers, d.locate).(*niceyaml.Source)

	d.src = src
	d.curLines = lineStarts(d.cur)
	d.node = nil

	for _, n := range src.AllDocuments() {
		err := n.Err()
		if err != nil {
			if holdsPanic(err) {
				return &placedError{err: fmt.Errorf("%w: %w", errParserPanicked, err), at: *d.capture}
			}

			return &placedError{err: fmt.Errorf("%w: %w", errParserRefuses, hook.Rewrap(err, func(refusal error) error {
				plain := errors.New(refusal.Error())

				if e, ok := errors.AsType[*niceyaml.Error](refusal); ok {
					if path, ok := e.Path(); ok {
						return niceyaml.Locate(plain, niceyaml.AtPath(path))
					}
				}

				return plain
			})), at: *d.capture}
		}

		if !n.IsEmpty() || d.node == nil {
			d.node = n
		}

		if !n.IsEmpty() {
			break
		}
	}

	if d.node == nil {
		return errNoDocument
	}

	return nil
}

// offset returns the offset in the current text of pos, as [offsetIn]
// gives it. Each byte of a line in ASCII is one rune, so its offset is the
// start of the line plus the column, up to the line break. The Doc keeps
// the lines of the current text that hold a byte outside ASCII, and the
// offsets of the runes of each such line that it read. The offsets of the
// tokens of a long line thus cost one walk of the line.
func (d *Doc) offset(pos position.Position) int {
	if pos.Line >= len(d.curLines) {
		return len(d.cur)
	}

	m := &d.runes
	if len(m.starts) != len(d.curLines) || &m.starts[0] != &d.curLines[0] {
		*m = runeOffsets{starts: d.curLines, wide: wideLines(d.cur, d.curLines)}
	}

	start := d.curLines[pos.Line]

	if _, wide := slices.BinarySearch(m.wide, pos.Line); !wide {
		// The line ends at the byte that ends it, its line break.
		end := len(d.cur)
		if pos.Line+1 < len(d.curLines) {
			end = d.curLines[pos.Line+1] - 1
		}

		return min(start+pos.Col, end)
	}

	if m.lines == nil {
		m.lines = make(map[int][]int)
	}

	offs, ok := m.lines[pos.Line]
	if !ok {
		offs = runeStarts(d.cur, start)
		m.lines[pos.Line] = offs
	}

	return offs[min(pos.Col, len(offs)-1)]
}

// runeOffsets holds, for one slice of the starts of the lines of the
// current text, the lines that hold a byte outside ASCII, in order, and
// the offsets of the runes of each such line that [Doc.offset] read. A
// parse makes a new slice of starts, so the slice names the text.
type runeOffsets struct {
	lines  map[int][]int
	starts []int
	wide   []int
}

// wideLines returns each line of text that holds a byte outside ASCII, in
// order. The lines of text start at starts.
func wideLines(text string, starts []int) []int {
	var out []int

	line := 0

	for i := range len(text) {
		if text[i] < utf8.RuneSelf {
			continue
		}

		for line+1 < len(starts) && starts[line+1] <= i {
			line++
		}

		if len(out) == 0 || out[len(out)-1] != line {
			out = append(out, line)
		}
	}

	return out
}

// runeStarts returns the offset in text of each rune of the line that
// starts at off, then the offset of the end of the line: its line break, or
// the end of text. The runes of a line end where [offsetIn] stops.
func runeStarts(text string, off int) []int {
	var offs []int

	for off < len(text) && !isBreak(text, off) {
		offs = append(offs, off)

		_, size := utf8.DecodeRuneInString(text[off:])
		off += size
	}

	return append(offs, off)
}

// offsetIn returns the offset of pos in text. The lines of text start at
// starts.
func offsetIn(text string, starts []int, pos position.Position) int {
	if pos.Line >= len(starts) {
		return len(text)
	}

	off := starts[pos.Line]

	for range pos.Col {
		if off >= len(text) || isBreak(text, off) {
			break
		}

		_, size := utf8.DecodeRuneInString(text[off:])
		off += size
	}

	return off
}

// positionIn returns the position of the byte at off in text. The lines of
// text start at starts. A column counts runes, so a byte inside a rune has
// the position of that rune. The end of a range thus lies one column past
// the position of its last byte.
func positionIn(text string, starts []int, off int) position.Position {
	line, _ := slices.BinarySearch(starts, off+1)
	line--

	return position.New(line, utf8.RuneCountInString(text[starts[line]:runeStart(text, starts[line], off)]))
}

// runeStart returns the offset of the first byte of the rune of text that
// holds the byte at off, no earlier than from. A byte that no rune holds,
// such as a byte of text that is not UTF-8, counts as a rune of its own.
func runeStart(text string, from, off int) int {
	if off >= len(text) || utf8.RuneStart(text[off]) {
		return off
	}

	for i := off - 1; i >= max(from, off-utf8.UTFMax+1); i-- {
		if !utf8.RuneStart(text[i]) {
			continue
		}

		if _, size := utf8.DecodeRuneInString(text[i:]); i+size > off {
			return i
		}

		break
	}

	return off
}

// locate gives the place in the user's text of a location that an error
// resolved in the current text, or in a copy of it that keeps every line
// and column, as [Doc.sitePlace] gives it. A problem that the edit package
// found shows the path of its site. An error that a caller binds by a path,
// such as an error that a [Func] returns or one that Doc.Node binds, keeps
// the path that the caller gave.
func (d *Doc) locate(at hook.Located) hook.Place {
	place := d.sitePlace(at)
	if !d.own {
		place.Path, place.HasPath = at.Path, at.HasPath
	}

	return place
}

// sitePlace gives the place of at as [Doc.locate] describes, with the path
// of its site where the error binds at a site. The details that name calls
// come with it, as [Doc.callDetails] gives them for that site. An error that
// the edit package binds again takes them where it binds again, so while
// the Doc captures sites, the first site goes to the capture instead.
func (d *Doc) sitePlace(at hook.Located) hook.Place {
	place, w := d.placeOf(at)

	s := site{writer: w, path: place.Path, at: noRange}
	if place.HasRange {
		s.at = place.Range
	}

	if c := d.capture; c != nil {
		if !c.placed {
			c.site, c.placed = s, true
		}

		return place
	}

	place.Details = d.callDetails(s)

	return place
}

// capture holds the first site that [Doc.sitePlace] gave while the Doc
// captured sites, and whether it gave one.
type capture struct {
	site

	placed bool
}

// writerAt returns the writer of the site that c holds when that site lies
// at rng, as the site of an error that the edit package binds again there.
func (c capture) writerAt(rng position.Range) *note {
	if !c.placed || c.at != rng {
		return nil
	}

	return c.writer
}

// placedError is an error of a parse of the current text, with the site of
// the error that the parse bound, as a [capture] holds it.
type placedError struct {
	err error
	at  capture
}

// Error returns the message of the error.
func (e *placedError) Error() string {
	return e.err.Error()
}

// Unwrap returns the error.
func (e *placedError) Unwrap() error {
	return e.err
}

// noRange is the range of a site of a problem without a place in the
// user's text, which lies at no value.
var noRange = position.NewRange(position.New(-1, 0), position.New(-1, 0))

// placeOf gives the place of at as [Doc.sitePlace] describes, without its
// details, and the note of the call that wrote the value there, or nil.
func (d *Doc) placeOf(at hook.Located) (hook.Place, *note) {
	place := hook.Place{Path: at.Path, HasPath: at.HasPath}

	// A place that the current text does not hold has no token, such as a
	// missing field, and so has a place that an edit created. The places of
	// the edit decide which places the text holds, as [Doc.engineOpts] reads
	// them. So a path that names a `<<` merge key binds as such a place. The
	// root package resolves a path through one. A path that
	// stops at an alias binds at the alias, as the root package binds it.
	// The edit does not read the names that a reference document brings in,
	// so a path that such an alias stops binds at the alias whatever the
	// reference document holds, as the problem of a read of the path does.
	// The node at the path gives the path that the user's text spells too.
	var held ast.Node

	if at.HasPath {
		n, err := d.node.Resolver().Node(at.Path)
		if err == nil && !at.Path.IsRoot() && !d.holdsPath(at.Path) {
			err = paths.ErrNotFound
		}

		switch {
		case errors.Is(err, paths.ErrNotFound):
			return d.tokenlessPlace(at.Path, place)
		case errors.Is(err, paths.ErrReferenceAlias):
			if s, ok := d.referenceSite(err); ok {
				place.Range, place.HasRange = s.at, true
				place.Path = s.path

				return place, s.writer
			}

		case err == nil:
			held = n
		}
	}

	if !at.HasRange {
		return place, nil
	}

	// The root has no key or "-", so an error at the start of the root binds
	// as [Doc.rootSite] gives its site.
	if at.HasPath && at.Path.IsRoot() {
		if rng, ok := rootRange(d.node); ok && d.offset(at.Range.Start) <= d.rootStartEnd(rng) {
			if s, ok := d.rootSite(); ok {
				place.Range, place.HasRange = s.at, true

				return place, nil
			}
		}
	}

	start := d.offset(at.Range.Start)
	end := max(start, d.offset(at.Range.End))

	// An empty value has no text, so an error there lands past the ":" of
	// its key or past its "-", and past the blanks after it. When the user
	// wrote that ":" or "-", the error binds past it in the user's text.
	// The text after it does not count, so the error binds there even
	// when a move wrote the lines of the value again.
	before, empty := d.emptyAfter(at, start, end)
	if empty && d.origin[before] >= 0 {
		return d.emptyPlace(place, at, before), nil
	}

	// When an edit wrote that ":" or "-" with a note, as a move does for the
	// key or the "-" of the value, the error binds where the key or the "-"
	// binds. A move into a mapping that the edit creates writes the key
	// with its own note but the ":" with the note of that mapping, so the
	// key gives the place.
	if empty {
		start = before

		if s, ok := d.keyBytes(at.Path); ok && d.origin[s.start] < 0 {
			start = s.start
		}
	}

	if d.madeAt(start) && at.HasPath {
		return d.tokenlessPlace(at.Path, place)
	}

	if start < len(d.origin) && d.origin[start] < 0 {
		n := d.notes[-1-d.origin[start]]
		place = n.place(place, at)

		// The site of such a note shows the path of the value that the edit
		// replaced. An error that a caller binds shows its own path, as
		// [Doc.locate] gives it.
		if n.detail != "" && n.hasPath {
			place.Path = d.notePath(n)
		}

		return place, d.tokenWriter(start)
	}

	rng := d.userRange(start, end)
	place.Range, place.HasRange = rng, true

	if at.HasPath && d.own {
		place.Path = d.userPathOf(at.Path, held, rng.Start)
	}

	return place, nil
}

// holdsPath reports whether the current text holds a value at path, as the
// places of the edit read it. It reports true for a path whose lookup
// fails, which [Doc.placeOf] binds as the root package binds it.
func (d *Doc) holdsPath(path paths.Path) bool {
	p, err := d.resolve(path)

	return err != nil || p.exists
}

// referenceSite returns the site of the alias of a reference document that
// err, the error of a lookup of the current text, names, as
// [Doc.aliasSite] gives it. It reports false for an alias outside the
// current text.
func (d *Doc) referenceSite(err error) (site, bool) {
	alias, ok := pathshook.UnresolvedAlias(err)
	if !ok || !d.inText(alias) {
		return site{}, false
	}

	return d.aliasSite(alias)
}

// tokenlessPlace returns place moved to where an error at path binds, a
// place for which the user's text holds no token, as [Doc.tokenlessSite]
// gives its site, under the path of that site, and the writer of that
// site.
func (d *Doc) tokenlessPlace(path paths.Path, place hook.Place) (hook.Place, *note) {
	s, ok := d.tokenlessSite(path)
	if !ok {
		return place, nil
	}

	place.Range, place.HasRange = s.at, true
	place.Path, place.HasPath = s.path, true

	return place, s.writer
}

// tokenlessSite returns the site of the place at path, a place for which
// the user's text holds no token, as [Doc.placeSite] gives it. An edit can
// have written the deepest value on the path that the text holds, and the
// site then holds the note of that edit as its writer, as [Doc.heldWriter]
// gives it. It reports false when the user's text has no root.
func (d *Doc) tokenlessSite(path paths.Path) (site, bool) {
	s, ok := d.placeSite(path)
	if ok {
		s.writer = d.heldWriter(path)
	}

	return s, ok
}

// heldWriter returns the note of the edit that wrote the token of the
// deepest value on path that the current text holds, path itself included,
// as [Doc.tokenWriter] gives it. It returns nil when the user wrote that
// token or the text holds no such value.
func (d *Doc) heldWriter(path paths.Path) *note {
	for at, ok := path, true; ok && !at.IsRoot(); at, ok = at.Parent() {
		p, err := d.resolve(at)
		if err != nil || !p.exists {
			continue
		}

		off, ok := d.tokenOf(p)
		if !ok {
			return nil
		}

		return d.tokenWriter(off)
	}

	return nil
}

// placeSite returns the site of the place at path, a place for which the
// user's text holds no token. That is a place that the current text does
// not hold, such as a missing field, a place below a scalar, or one past
// the end of a sequence, and a value whose token an edit created. An empty
// value binds past its ":" or "-". Any other place binds at the site of
// the nearest value above it whose token the user wrote and the edits kept,
// as [Doc.valueSite] gives it. At the root, that is the site that
// [Doc.rootSite] gives. It reports false when the user's text has no root.
func (d *Doc) placeSite(path paths.Path) (site, bool) {
	for at, ok := path, true; ok && !at.IsRoot(); at, ok = at.Parent() {
		p, err := d.resolve(at)
		if err != nil || !p.exists {
			continue
		}

		if s, ok := d.valueSite(p); ok {
			return s, true
		}
	}

	return d.rootSite()
}

// valueSite returns the site of the value at p, a place of the current
// text, as [Doc.site] gives the site of its token. That token is where an
// error at the value binds: the key or the "-" of a collection, as
// [Doc.holderSite] gives it, and the first token of a scalar or an alias
// under its anchors and tags. An item of a flow sequence has no key or
// "-", so a collection there binds at its first token under its anchors
// and tags too, unless it is a mapping without braces. An empty value has
// no token, so it binds past its ":" or "-", as an error at the value
// binds. The site of the root is the one that [Doc.rootSite] gives. It
// reports false for a value whose token an edit created.
func (d *Doc) valueSite(p *place) (site, bool) {
	if p.root {
		return d.rootSite()
	}

	off, ok := d.tokenOf(p)
	if !ok || d.madeAt(off) {
		return site{}, false
	}

	switch {
	case holdsAtKey(p):
		return d.holderSite(p), true

	case isImplicitNull(p.value):
		n, ok := d.emptyNote(p.path, off)
		if !ok {
			return site{}, false
		}

		return site{path: n.path, at: n.at, origin: d.originAt(off)}, true

	default:
		return d.site(span{start: off, end: max(off+1, d.valueEnd(p))}, false), true
	}
}

// tokenOf returns the offset of the token where the value of p binds, as
// [Doc.valueSite] reads it: the key or the "-" of a collection, as
// [Doc.holderToken] gives it, the ":" or the "-" before an empty value, and
// the first token of any other value under its anchors and tags. A move
// into a mapping that the edit creates writes the key of an empty value
// with its own note but the ":" with the note of that mapping. The key
// stands for the value then, so it is the token of the value, as
// [Doc.emptyNote] reads it.
func (d *Doc) tokenOf(p *place) (int, bool) {
	switch {
	case holdsAtKey(p):
		start := d.holderToken(p)

		return start, start < len(d.origin)

	case isImplicitNull(p.value):
		vs := d.valueStart(p)
		if vs <= 0 || vs > len(d.origin) || d.origin[vs-1] >= 0 {
			return vs - 1, vs > 0
		}

		if s, ok := d.keyBytes(p.path); ok && d.origin[s.start] < 0 {
			return s.start, true
		}

		return vs - 1, true

	default:
		cs := d.contentStart(p)

		return cs, cs < len(d.origin)
	}
}

// holdsAtKey reports whether the value of p, a place of the current text,
// is a collection that binds at its key or its "-", as [Doc.valueSite]
// binds it. A collection that is an item of a flow sequence binds at its
// first token instead, unless it is a mapping without braces, which starts
// at its first key.
func holdsAtKey(p *place) bool {
	if !isCollection(p.value) {
		return false
	}

	return p.seq == nil || !p.seq.IsFlowStyle || isBareMapping(astnode.Content(p.value))
}

// madeAt reports whether an edit created the byte at off of the current
// text, so that the user's text holds no token for it. A byte that stands
// for text of the user, such as a renamed key, is not made. Neither is a
// byte that stands for a value of the user that a Set replaced, which
// binds at the site of that value. The first byte of the token of a new
// scalar, under its tags, is such a byte. So is that of a new item of a
// flow sequence. A collection elsewhere binds at its key or its "-", so
// every byte that a Set writes for it is made. A byte past the end of the
// text is made.
func (d *Doc) madeAt(off int) bool {
	o := d.originAt(off)

	switch {
	case o == math.MinInt:
		return true
	case o >= 0:
		return false
	}

	n := d.notes[-1-o]

	return !n.located || n.detail != "" && (!n.replaced || n.inside)
}

// rootSite returns the site of the root of the current text. It is the
// site of the first key or item of the root that the user wrote and the
// edits kept, as [Doc.site] gives it, under the path $. An item of a block
// sequence binds at its "-" where its content belongs to a value that an
// edit created, as a collection that a Set wrote does. Where the edits kept
// no key or item, it is the first byte of the root in the user's text, as
// [Doc.userRootSite] gives it. It reports false when the user's text has
// no root.
func (d *Doc) rootSite() (site, bool) {
	for s := range d.rootSpans() {
		if d.madeAt(s.start) {
			continue
		}

		at := d.site(s, false)
		at.path, at.writer, at.inKey = paths.Doc(), nil, false

		return at, true
	}

	return d.userRootSite()
}

// rootSpans returns the bytes of the token where each key or item of the
// root of the current text binds, in order. For a mapping, that is the
// key of each entry. For a flow sequence, it is the first byte of each
// item. For a block sequence, it is the first token of the content of each
// item under its anchors and tags, then the "-" of the item, or only the
// "-" of an empty item. An item that holds a tag alone, as in "- !!null",
// starts at its tag, the token that the user wrote.
func (d *Doc) rootSpans() iter.Seq[span] {
	return func(yield func(span) bool) {
		next := func(tk *token.Token) bool {
			s, ok := d.tokenSpan(tk)

			return !ok || tk == nil || yield(s)
		}

		switch x := astnode.Content(d.node.AST()).(type) {
		case *ast.MappingNode:
			for _, e := range x.Values {
				if !next(astnode.KeyToken(e)) {
					return
				}
			}

		case *ast.SequenceNode:
			for i, v := range x.Values {
				var tk, dash *token.Token

				if !x.IsFlowStyle && i < len(x.Entries) {
					dash = x.Entries[i].Start
				}

				switch content := astnode.Content(v); {
				case x.IsFlowStyle:
					tk = astnode.PathToken(v, astnode.Parent{Sequence: x, Index: i})
				case isImplicitNull(content) && dash != nil && content == v:
					tk, dash = dash, nil
				case isImplicitNull(content):
					tk = v.GetToken()
				default:
					tk = astnode.StartToken(content)
				}

				if !next(tk) || dash != nil && !next(dash) {
					return
				}
			}
		}
	}
}

// userRootSite returns the site of the first byte of the content of the
// root of the user's text, under its anchors and tags, under the path $.
// That byte is the first "-" of a block sequence, and the first key or item
// of any other root, as [rootRange] gives it. A flow root binds past its
// "{" or "[". It reports false when the user's text has no root.
func (d *Doc) userRootSite() (site, bool) {
	var start *token.Token

	past := 0

	switch x := astnode.Content(d.user.AST()).(type) {
	case *ast.MappingNode:
		if x.IsFlowStyle {
			start, past = x.Start, 1
		}

	case *ast.SequenceNode:
		start = x.Start
		if x.IsFlowStyle {
			past = 1
		}
	}

	if start != nil && start.Position != nil {
		pos := position.NewFromToken(start)
		pos.Col += past

		end := pos
		if past == 0 {
			end.Col++
		}

		return site{path: paths.Doc(), at: position.NewRange(pos, end)}, true
	}

	rng, ok := rootRange(d.user)

	return site{path: paths.Doc(), at: rng}, ok
}

// rootStartEnd returns the offset in the current text where the start of
// the root ends: past rng, the range of [rootRange], or past the first token
// of the content of the root. The root package binds an error at the root at
// that token, which can lie inside the first item.
func (d *Doc) rootStartEnd(rng position.Range) int {
	end := d.offset(rng.End)

	rs, err := d.node.Ranges(paths.Doc())
	if err == nil && len(rs) > 0 {
		end = max(end, d.offset(rs[0].End))
	}

	return end
}

// emptyAfter returns the offset of the byte before the empty value where
// the error of at binds, such as the ":" of its key or its "-". The error
// lies at the bytes [start, end) of the current text. An empty value has
// no text, so the error lands past that byte and the blanks after it, as
// a "\r" before a line break. It reports false for an error at any other
// value, and when an edit wrote that byte with the origin of another
// byte.
func (d *Doc) emptyAfter(at hook.Located, start, end int) (int, bool) {
	if start != end || !at.HasPath {
		return 0, false
	}

	n, err := d.node.Resolver().Node(at.Path)
	if err != nil || n == nil || !isImplicitNull(n) {
		return 0, false
	}

	i := min(start, len(d.cur)) - 1
	for i >= 0 && isBlank(d.cur[i]) && !isBreak(d.cur, i) {
		i--
	}

	if i < 0 || isBreak(d.cur, i) || d.origin[i] >= 0 && !d.ownByte(i) {
		return 0, false
	}

	return i, true
}

// emptyPlace returns place moved to where the error of at binds, an error
// at an empty value, as [Doc.emptyAt] finds it. The byte at before of the
// current text is the ":" or "-" of the value.
func (d *Doc) emptyPlace(place hook.Place, at hook.Located, before int) hook.Place {
	rng, path := d.emptyAt(at.Path, before)

	place.Range, place.HasRange = rng, true
	place.Path = path

	return place
}

// emptyAt returns where an error at the empty value at path binds in the
// user's text, and the path there. The byte at before of the current text
// is the ":" or "-" of the value, a byte of the user that an edit kept.
// The error binds past that byte in the user's text, and past the blanks
// after it that come before the place where the user's text holds the
// value. The parser puts an empty value at the end of a text past more
// blanks than one in the middle. The error thus binds as an error at the
// value binds in the user's text, even when an edit moved the value to the
// end of the text or away from it. The path is that of the entry or the
// item that the ":" or "-" belongs to.
func (d *Doc) emptyAt(path paths.Path, before int) (position.Range, paths.Path) {
	user := d.userPath(path, d.userPos(before))

	u := d.origin[before] + 1

	end := d.userEmpty(user, u)
	for u < end && isBlank(d.orig[u]) {
		u++
	}

	pos := positionIn(d.orig, d.origLines, u)
	pos.Line += d.base

	return position.NewRange(pos, pos), user
}

// emptyNote returns the note of the empty value at path, whose ":" or "-"
// is the byte at before of the current text. The note binds where
// [Doc.locate] binds an error at the value. When the user wrote that
// byte, it binds past it, as [Doc.emptyAt] finds the place. When an edit
// wrote the byte with a note, as a move does for the key or the "-" of the
// value, the note is that of the key or the "-". It reports false when an
// edit wrote the byte with the origin of another byte.
func (d *Doc) emptyNote(path paths.Path, before int) (note, bool) {
	o := d.origin[before]

	switch {
	case o >= 0 && d.ownByte(before):
		rng, user := d.emptyAt(path, before)

		return note{at: rng, located: true, path: user, hasPath: true}, true

	case o < 0:
		// A move into a mapping that the edit creates writes the key with
		// its own note but the ":" with the note of that mapping.
		if s, ok := d.keyBytes(path); ok && d.origin[s.start] < 0 {
			o = d.origin[s.start]
		}

		n := d.notes[-1-o]
		n.moved = false

		if n.hasPath {
			n.path = spelled(n.path, path)
		}

		return n, true

	default:
		return note{}, false
	}
}

// userEmpty returns the offset in the user's text of the place where the
// parser put the empty value at path, a path of the user's text. It
// returns after, the offset past the ":" or "-" of the value, when the
// user's text does not hold an empty value at path.
func (d *Doc) userEmpty(path paths.Path, after int) int {
	n, err := d.user.Resolver().Node(path)
	if err != nil || n == nil || !isImplicitNull(n) {
		return after
	}

	pos := position.NewFromToken(n.GetToken())
	pos.Line -= d.base

	if pos.Line < 0 {
		return after
	}

	return offsetIn(d.orig, d.origLines, pos)
}

// isBlank reports whether b is a space, a tab, or a "\r".
func isBlank(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r'
}

// place returns place moved to where n binds, without the details of n.
// The argument place is the place of at, a location in text that an edit
// wrote with n.
func (n note) place(place hook.Place, at hook.Located) hook.Place {
	if n.located {
		place.Range, place.HasRange = n.at, true
	}

	// A note without a detail stands for text of the user, such as a
	// renamed key. An error at the value that such a key holds shows the
	// path of the value. An error without a path takes the path of the note
	// only where an edit created the text, for which the user's text holds
	// no token.
	if n.hasPath && (at.HasPath || n.detail != "") {
		place.Path, place.HasPath = n.path, true

		if n.detail == "" && at.HasPath {
			place.Path = spelled(n.path, at.Path)
		}
	}

	return place
}

// written returns n as the writer of a site, as [site] holds it: the note
// of a value that a call wrote. It returns nil for a note without a
// detail, which stands for text of the user, such as a renamed key.
func (n note) written() *note {
	if n.detail == "" {
		return nil
	}

	return &n
}

// callDetails returns the details that name calls for a problem bound at
// s, by the rule of the Errors section of the package documentation. Each
// names a call by the label of its Apply, what the call does, and the
// paths that the call was given, as in "v1 moves $.a to $.b". The first
// names the call that wrote the value at s, as the writer of s holds it.
// The next names the call that runs, unless the first names it already. A
// detail says "from this value" where s is the site of a value that its
// call replaced. For the call that wrote the value at s, that is where s
// shows the path of the note of the call. For the call that runs, s starts
// where the site that [Doc.startCall] holds starts. The problems of the
// check after the last call name no call.
func (d *Doc) callDetails(s site) []error {
	out := d.writerDetails(s)

	w := s.writer
	if d.running == "" || d.checking || w != nil && w.detail == d.subject() && w.call == d.running {
		return out
	}

	from := d.replacing && s.path.Equal(d.replaced.path) && s.at.Start == d.replaced.at.Start

	return append(out, callDetail(d.subject(), d.running, from))
}

// writerDetails returns the detail that names the call that wrote the value
// at s, as [Doc.callDetails] gives it, without the detail of the call that
// runs. A related site of a problem takes it.
func (d *Doc) writerDetails(s site) []error {
	w := s.writer
	if w == nil || d.checking {
		return nil
	}

	return []error{callDetail(w.detail, w.call, w.replaced && s.path.Equal(d.notePath(*w)))}
}

// callDetail returns the detail that names a call of the Apply that
// subject names, which does what call says. With from, the detail says
// that the call wrote its value from the value at the site.
func callDetail(subject, call string, from bool) error {
	what := subject + " " + call
	if from {
		what += " from this value"
	}

	return errors.New(what)
}

// tokenWriter returns the note of the call that wrote the token at off of
// the current text, as the writer of a [site] holds it. It returns nil
// where the user wrote the token. A token that stands for text of the user
// has no writer, as a renamed key does. A key or a "-" that a move wrote
// for an item of a flow sequence keeps the writer of the token of the
// item, as [Doc.setOver] gives it.
func (d *Doc) tokenWriter(off int) *note {
	o := d.originAt(off)
	if o >= 0 || o == math.MinInt {
		return nil
	}

	n := d.notes[-1-o]

	if n.detail == "" && n.flowItem {
		if value, ok := d.heldValue(off); ok {
			if set, ok := d.setOver(value, n); ok {
				return set.written()
			}
		}
	}

	return n.written()
}

// heldValue returns the value of the current text that the key or the "-"
// at off holds. It reports false where no key and no "-" of a block
// sequence starts at off.
func (d *Doc) heldValue(off int) (ast.Node, bool) {
	var held ast.Node

	d.inspect(func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.MappingValueNode:
			if s, ok := d.nodeSpan(keyContent(x)); ok && s.start == off {
				held = x.Value
			}

		case *ast.SequenceNode:
			for i, e := range x.Entries {
				if s, ok := d.tokenSpan(e.Start); ok && s.start == off && i < len(x.Values) && !x.IsFlowStyle {
					held = x.Values[i]
				}
			}
		}

		return held == nil
	})

	return held, held != nil
}

// setOver returns the note of the Set that wrote value, a collection of
// the current text, over an item of a flow sequence. It does so when key,
// the note of the key or the "-" that holds value, stands for that item.
// The writer of that key or "-" is then the Set, as at the token of the
// item before a move took the value out of the sequence. An item has no
// key or "-". So the note of the token of the value holds the origin of
// the note that stood for the item. It reports false for any other key,
// such as a key that stands for a key of the user, and for a note that
// names an edit.
func (d *Doc) setOver(value ast.Node, key note) (note, bool) {
	if key.detail != "" || !key.flowItem || astnode.IsNil(value) {
		return note{}, false
	}

	s, ok := d.nodeSpan(astnode.Content(value))
	if !ok || s.start >= len(d.origin) || d.origin[s.start] >= 0 {
		return note{}, false
	}

	n := d.notes[-1-d.origin[s.start]]
	if n.held >= 0 || n.detail == "" {
		return note{}, false
	}

	held := d.notes[-1-n.held]

	return n, key.located && held.located && key.at == held.at
}

// userRange returns the range in the user's text of the bytes [start, end)
// of the current text, which an edit kept.
func (d *Doc) userRange(start, end int) position.Range {
	from := d.userPos(start)
	to := from

	if end > start {
		last := d.userPos(end - 1)
		to = position.New(last.Line, last.Col+1)
	}

	if to.Line < from.Line || to.Line == from.Line && to.Col < from.Col {
		to = from
	}

	return position.NewRange(from, to)
}

// userPos returns the position in the user's text of the byte at off of
// the current text, or of the nearest byte before it that an edit kept.
func (d *Doc) userPos(off int) position.Position {
	for i := min(off, len(d.origin)-1); i >= 0; i-- {
		if o := d.origin[i]; o >= 0 {
			pos := positionIn(d.orig, d.origLines, o)
			pos.Line += d.base

			return pos
		}
	}

	return position.New(d.base, 0)
}

// userPath returns the path of the value that the user's text holds at
// pos, for an error at path in the current text, as [Doc.site] spells it.
// A path that the current text does not hold, such as a field that the
// document omits, stays as it is, in the terms of the current text.
func (d *Doc) userPath(path paths.Path, pos position.Position) paths.Path {
	// The resolver binds no error. A bound error locates itself through d,
	// which then calls userPath again.
	n, err := d.node.Resolver().Node(path)
	if err != nil {
		return path
	}

	return d.userPathOf(path, n, pos)
}

// userPathOf returns the path that [Doc.userPath] gives for an error at
// path. The node n is the node of the current text at path, or nil where
// the current text does not hold path.
func (d *Doc) userPathOf(path paths.Path, n ast.Node, pos position.Position) paths.Path {
	if n == nil {
		return path
	}

	got, ok := d.userPathAt(pos)
	if !ok {
		// An empty value has no token of its own, so its key gives the path.
		got, ok = d.keyPath(path)
	}

	// A mapping without braces in a flow sequence starts at its first key,
	// which gives the path of that key.
	if ok {
		if item := d.bareItem(got); !item.Equal(got) && isBareMapping(n) {
			got = item
		}
	}

	if !ok {
		return path
	}

	// The site path of a token in a key ends in `~` where it names the entry
	// of that key. No path names a key inside a << merge entry, so an error
	// at such a key shows the path of the mapping that holds the entry.
	if last, _ := path.Last(); last.Kind == paths.SelectorKey && path.Len() > 1 {
		if gotLast, _ := got.Last(); gotLast.Kind != paths.SelectorKey {
			return got
		}
	}

	return spelled(got, path)
}

// keyPath returns the path that the user's text spells for the key of the
// entry at path, an entry of the current text. A renamed or moved key
// gives the path of the key that it stands for. It reports false for a
// key that an edit created and for a path that does not end in a name.
func (d *Doc) keyPath(path paths.Path) (paths.Path, bool) {
	s, ok := d.keyBytes(path)
	if !ok {
		return paths.Path{}, false
	}

	if d.standsFor(s.start) {
		n := d.notes[-1-d.origin[s.start]]

		return n.path, n.hasPath
	}

	if !d.ownByte(s.start) {
		return paths.Path{}, false
	}

	return d.userPathAt(d.userPos(s.start))
}

// keyBytes returns the bytes of the key of the entry at path, an entry of
// the current text. It reports false for a path that does not end in a
// name.
func (d *Doc) keyBytes(path paths.Path) (span, bool) {
	if last, ok := path.Last(); !ok || last.Kind != paths.SelectorChild {
		return span{}, false
	}

	tk, err := d.node.Resolver().Token(path.Key())
	if err != nil || tk == nil {
		return span{}, false
	}

	s, ok := d.tokenSpan(tk)
	if !ok || s.start >= len(d.origin) {
		return span{}, false
	}

	return s, true
}

// spelled returns got, a path of the user's text, in the form of path. For
// the path of a key, it returns the path of the key of got, and for any
// other path, the path of the value. An item of a sequence has no key, so
// got then stays the path of the item. The root of the document binds at
// its first key or item, but every text spells the root as `$` and its key
// as `$~`, so both keep their path.
func spelled(got, path paths.Path) paths.Path {
	last, _ := path.Last()

	if parent, ok := path.Parent(); path.IsRoot() || ok && parent.IsRoot() && last.Kind == paths.SelectorKey {
		return path
	}

	gotLast, _ := got.Last()

	switch {
	case last.Kind == paths.SelectorKey && gotLast.Kind == paths.SelectorChild:
		return got.Key()

	case last.Kind != paths.SelectorKey && gotLast.Kind == paths.SelectorKey:
		parent, _ := got.Parent()

		return parent

	default:
		return got
	}
}

// standsFor reports whether an edit wrote the byte at off in place of
// text of the user. A rename writes a key in place of the old key, and a
// move writes a key or a "-" in place of the one that held the value. A
// move also writes an alias in place of an anchored value. The note of
// such a byte binds an error where the user wrote that text.
func (d *Doc) standsFor(off int) bool {
	if off >= len(d.origin) || d.origin[off] >= 0 {
		return false
	}

	n := d.notes[-1-d.origin[off]]

	return n.located && n.detail == ""
}

// ownByte reports whether the byte at off is text of the user's file that
// an edit kept. A byte that an edit wrote with the origin of another byte,
// such as a "-" that takes the origin of its value, is not.
func (d *Doc) ownByte(off int) bool {
	if off >= len(d.origin) {
		return false
	}

	o := d.origin[off]

	return o >= 0 && d.orig[o] == d.cur[off]
}

// addNote returns the origin of text that an edit writes with n.
func (d *Doc) addNote(n note) int {
	d.notes = append(d.notes, n)

	return -len(d.notes)
}

// splice replaces the bytes [start, end) of the current text with text,
// whose bytes came from origins, and parses the result.
func (d *Doc) splice(start, end int, text string, origins []int) error {
	return d.apply(edit{start: start, end: end, text: text, origins: origins})
}

// edit is one replacement of the bytes [start, end) of the current text.
type edit struct {
	text    string
	origins []int
	start   int
	end     int
}

// apply applies edits, which must not overlap, and parses the result once.
// An insertion goes in front of an edit that replaces the bytes after it,
// and insertions at one offset keep their order. When the new text does
// not parse, apply leaves the text as it was.
func (d *Doc) apply(edits ...edit) error {
	cur, origin, src, node, lines := d.cur, d.origin, d.src, d.node, d.curLines

	// The edits apply from the end of the text, so no edit moves the
	// offsets of the edits before it. Among the edits that start at one
	// offset, the one that ends later applies first. The reverse keeps the
	// order of insertions at one offset.
	edits = slices.Clone(edits)
	slices.Reverse(edits)
	slices.SortStableFunc(edits, func(a, b edit) int {
		return cmp.Or(cmp.Compare(b.start, a.start), cmp.Compare(b.end, a.end))
	})

	for _, e := range edits {
		d.cur = d.cur[:e.start] + e.text + d.cur[e.end:]
		d.origin = slices.Concat(d.origin[:e.start], e.origins, d.origin[e.end:])
	}

	err := d.parse()
	if err != nil {
		d.cur, d.origin, d.src, d.node, d.curLines = cur, origin, src, node, lines

		return err
	}

	return nil
}

// mappingOf returns the mapping of the current text that holds the entry
// e, or nil for a nil e. A block mapping of one entry is the entry itself.
func (d *Doc) mappingOf(e *ast.MappingValueNode) ast.Node {
	if e == nil {
		return nil
	}

	if m, ok := d.tree().parent[e].(*ast.MappingNode); ok && slices.Contains(m.Values, e) {
		return m
	}

	return e
}

// keyPlace returns the site of the key of an entry of the current text,
// whose bytes are s, as [Doc.site] gives it. A key that the user wrote, or
// that an edit wrote for a key of the user, shows the path of that key in
// the user's text, which ends in `~`. No path names a << merge key or a key
// inside a << merge entry, so such a key shows the path of the mapping that
// holds the outermost such entry. Nor does a path name a key without a name
// of its own, such as a collection. A key that an edit created binds as a
// value that an edit created binds, under the path of the value that holds
// it, with a detail that names the edit.
func (d *Doc) keyPlace(s span) site {
	at := d.site(s, false)
	if at.inKey {
		at.path = at.path.Key()
	}

	return at
}

// written returns origins for text that an edit writes with the note
// origin.
func written(text string, origin int) []int {
	origins := make([]int, len(text))
	for i := range origins {
		origins[i] = origin
	}

	return origins
}
