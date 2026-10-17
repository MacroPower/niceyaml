package yamlviewport

import (
	"cmp"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

const defaultHorizontalStep = 6

// Searcher builds an [Index] over the content of a view. The viewport calls
// Load the first time it searches for a term after its content changes. It
// runs every term through the Index it gets back until the content changes
// again. A side-by-side diff has two panes, and the viewport calls Load
// once for each of them. Without a diff, side-by-side mode shows the same
// revision in both panes, so Load runs once.
//
// Load receives the view as a [line.Sequence], which yields the lines the
// view holds, each with its index in the content of the view. The ranges
// the Index finds are in the coordinates of that content. A match on a line
// of a hunk therefore reports the index that line has in the unified diff.
// In hunks mode the view skips the lines between the hunks, so a match does
// not start on one of them. A nil Index, or one holding a nil pointer, does
// not match any term.
//
// [WithFinder] adapts a [finder.Finder] to this interface.
type Searcher interface {
	Load(lines line.Sequence) Index
}

// Index finds each [position.Range] that matches a search string in the lines
// it indexes. Find can return the ranges in any order, because the viewport
// sorts them into document order.
//
// See [finder.Index] for an implementation.
type Index interface {
	Find(search string) position.Ranges
}

// finderSearcher adapts a [finder.Finder] to [Searcher].
type finderSearcher struct {
	finder *finder.Finder
}

// Load implements [Searcher].
func (s finderSearcher) Load(lines line.Sequence) Index {
	return s.finder.Load(lines)
}

// Revision is one version of a document in the viewport's history: a name
// for the revision picker and a view of its content. A [*niceyaml.Source]
// is a Revision, so a source goes to [Model.AddRevision] or
// [Model.SetRevision] as it is. A view that carries decoration, such as the
// marks [niceyaml.Annotate] adds, goes in through [NewRevision]:
//
//	view := source.View()
//	niceyaml.Annotate(err, view)
//	m.SetRevision(yamlviewport.NewRevision(source.Name(), view))
//
// The viewport reads the view of a revision it shows without a diff when
// the revision changes, or when [Model.SetDiffMode] turns the diff off. It
// never decorates the view. It computes the diff between two revisions from
// their views and keeps it until it compares another pair or the history
// changes. The marks the views carry then stay with the diff. A change of
// view mode or hunk context reuses the diff and its marks without reading
// the views again. Search highlights go on a clone, so the marks a caller
// adds stay, and the caller's view stays as the caller left it. Marks that a
// caller adds after the viewport read the view show after the caller passes
// the revision in again.
//
// A diff shows lines of two revisions, and each line carries the overlays
// and annotations it has in the view of the revision that holds it. For a
// deleted line, and for every line of the left pane of [ViewModeSideBySide],
// that is the revision the diff compares with. For any other line it is the
// current revision. Outside that pane an unchanged line comes from the
// current revision, so a mark it has in the other revision does not show.
// [ViewModeHunks] shows the lines around each change, and a mark on any
// other line stays hidden with its line. The diff sets the flag of each
// line and shows the line whole. The flags of a view and the clip that
// [line.View.Clip] sets therefore show only without a diff.
//
// The viewport renders the lines the view holds in the order
// [line.View.All] yields them, which is content order, and windows them
// by index. A search reads the lines the view holds, and the viewport
// drops a match that a [Searcher] reports on any other line.
//
// See [ViewRevision] and [niceyaml.Source] for implementations.
type Revision interface {
	Name() string
	View() *line.View
}

var (
	_ Revision = (*niceyaml.Source)(nil)
	_ Revision = ViewRevision{}
)

// ViewRevision is a [Revision] that pairs a name with a view the caller
// built, such as a view with decoration.
//
// Create instances with [NewRevision].
type ViewRevision struct {
	view *line.View
	name string
}

// NewRevision creates a new [ViewRevision] from a name and a view of its
// content.
func NewRevision(name string, view *line.View) ViewRevision {
	return ViewRevision{name: name, view: view}
}

// Name implements [Revision].
func (r ViewRevision) Name() string {
	return r.name
}

// View implements [Revision].
func (r ViewRevision) View() *line.View {
	return r.view
}

// DiffMode specifies how the viewport computes diffs between revisions.
//
// Use [Model.SetDiffMode] to change the mode, or [Model.ToggleDiffMode] to
// cycle through modes.
type DiffMode int

const (
	// DiffModeAdjacent compares the current revision with the immediately
	// preceding revision. It is the default mode.
	DiffModeAdjacent DiffMode = iota
	// DiffModeOrigin compares the current revision with the first (origin)
	// revision, so the diff shows cumulative changes.
	DiffModeOrigin
	// DiffModeNone displays the current revision without any diff markers.
	DiffModeNone
)

// String returns "adjacent" for [DiffModeAdjacent], "origin" for
// [DiffModeOrigin], "none" for [DiffModeNone], and "DiffMode(n)" for any
// other value n.
func (d DiffMode) String() string {
	switch d {
	case DiffModeAdjacent:
		return "adjacent"
	case DiffModeOrigin:
		return "origin"
	case DiffModeNone:
		return "none"
	default:
		return "DiffMode(" + strconv.Itoa(int(d)) + ")"
	}
}

// ViewMode specifies how the viewport renders diff content.
//
// Use [Model.SetViewMode] to change the mode, or [Model.ToggleViewMode] to
// cycle through modes.
type ViewMode int

const (
	// ViewModeFull displays all lines, including unchanged content. It is
	// the default mode.
	ViewModeFull ViewMode = iota
	// ViewModeHunks displays only changed lines with surrounding context.
	ViewModeHunks
	// ViewModeSideBySide displays before and after content in separate panes.
	ViewModeSideBySide
)

// String returns "full" for [ViewModeFull], "hunks" for [ViewModeHunks],
// "side-by-side" for [ViewModeSideBySide], and "ViewMode(n)" for any other
// value n.
func (v ViewMode) String() string {
	switch v {
	case ViewModeFull:
		return "full"
	case ViewModeHunks:
		return "hunks"
	case ViewModeSideBySide:
		return "side-by-side"
	default:
		return "ViewMode(" + strconv.Itoa(int(v)) + ")"
	}
}

// Hit is a place in a revision of the viewport's history. [Model.HitAt]
// returns the Hit that the cell under a pointer shows, and [Model.Reveal]
// scrolls the viewport to a Hit.
type Hit struct {
	// Position is the place in the revision, in the coordinates of the
	// content of the view the revision hands out. For a
	// [*niceyaml.Source] those are the coordinates of
	// [niceyaml.Source.Lines], so the position goes to
	// [niceyaml.Node.PathAt] or [niceyaml.Source.Offset] as it is.
	Position position.Position
	// Revision is the index in the history of the revision that holds
	// the position, as [Model.RevisionIndex] counts.
	Revision int
}

// Option configures a [Model].
//
// Available options:
//   - [WithPrinter]
//   - [WithContainerStyle]
//   - [WithSearcher]
//   - [WithFinder]
type Option func(*config)

// config holds the settings an [Option] configures. An option takes it in
// place of the [Model] that embeds it, so only [New] can apply one.
type config struct {
	// The container style applied to the viewport frame.
	style    lipgloss.Style
	printer  *printer.Printer
	searcher Searcher
}

// WithPrinter is an [Option] that sets the [*printer.Printer] the viewport
// renders with. Without it, the viewport creates a printer that renders with
// [theme.Charm] inside the box of [printer.WithBox]. A nil p selects that
// same default. Pass [printer.WithBox] to a printer of your own to draw the
// same box, with one cell of padding at the right of each row.
//
// The viewport never modifies the printer. Each render derives a copy with
// [printer.Printer.With] and the viewport's wrap width, so other renderers
// can share the same printer. The viewport sizes that copy's container to
// the content area, so it ignores any Width, Height, MaxWidth, or MaxHeight
// on the printer's container style. To fix the size of the viewport, set
// them on [WithContainerStyle] instead.
func WithPrinter(p *printer.Printer) Option {
	return func(c *config) {
		c.printer = p
	}
}

// WithContainerStyle is an [Option] that sets the [lipgloss.Style] the
// viewport renders around its content, as [printer.WithContainerStyle] does
// for a printer. See [Model.SetContainerStyle].
//
//nolint:gocritic // hugeParam: Copying.
func WithContainerStyle(s lipgloss.Style) Option {
	return func(c *config) {
		c.style = s
	}
}

// WithSearcher is an [Option] that sets the [Searcher] that search terms run
// through. Without it, and without [WithFinder], the viewport creates a
// [finder.Finder] with [finder.New], which folds case and ignores
// diacritics. A nil s, or one holding a nil pointer, selects that same
// default.
func WithSearcher(s Searcher) Option {
	return func(c *config) {
		c.searcher = s
	}
}

// WithFinder is an [Option] that sets the [finder.Finder] that builds the
// search index. It is [WithSearcher] with f adapted to [Searcher]:
//
//	yamlviewport.WithFinder(finder.New(finder.WithNormalizer(nil)))
//
// A nil f selects the default searcher, as a nil [WithSearcher] does.
func WithFinder(f *finder.Finder) Option {
	if f == nil {
		return WithSearcher(nil)
	}

	return WithSearcher(finderSearcher{finder: f})
}

// New creates a new [Model] with the given options.
func New(opts ...Option) Model {
	var m Model

	for _, opt := range opts {
		opt(&m.config)
	}

	m.setInitialValues()

	return m
}

// Model is the Bubble Tea model for the YAML viewport.
// Create instances with [New].
//
// A zero Model does not have a printer, keymap, or searcher. It does not
// have rows, so [Model.View] returns "" and both scroll offsets stay at 0.
// Searching needs the searcher that [New] creates, so a zero Model does not
// match any term. Construct every Model with [New] and any [Option] values.
//
// # Rows
//
// The viewport scrolls by rendered row, not by source line. A line that
// wraps to the viewport width or carries an annotation takes several rows.
// The vertical offset that [Model.YOffset], [Model.SetYOffset], and the
// scroll methods use counts those rows, so every row of the view is
// reachable. [printer.WithContainerStyle] gives the printer a container
// style, and the frame of that style adds rows above the first line and
// below the last. The methods named after rows, such as
// [Model.TotalRowCount] and [Model.VisibleRowCount], count rows. The methods
// named after lines, such as [Model.TotalLineCount] and
// [Model.VisibleLineCount], count the lines of the view.
//
// A layout change, such as a new width, style, printer, or wrap setting,
// keeps the same line at the top of the view. The top row stays at the same
// row of that line, or at its last row when the line has fewer rows than
// that. A top row in the container frame stays at the same row of that frame
// in the same way. A view at the top stays at the top, so a frame that
// appears or grows shows its outer row. A change of view mode without a diff
// is a layout change too, because every mode shows the same lines.
//
// A change of content scrolls to the top, or to the first search match when
// the term has one. A new revision changes the content. So do a diff mode
// that changes the diff on display, a view mode while a diff shows, and a
// hunk context while hunks of a diff show.
//
// A search or [Model.Reveal] waits to scroll while the content area does not
// have room for a row, such as before the parent sizes the viewport from the
// first [tea.WindowSizeMsg]. When the area has room, the viewport centers
// the current match or the revealed position in the layout it has then. A
// search without a current match at that point leaves the view in place.
// The scroll sets the offsets, so a layout change before that point does not
// keep the top line. Reveal reports its result at the call, before the
// scroll runs. [Model.SetYOffset], [Model.SetXOffset], and the scroll
// methods drop a scroll that waits, and so does a change of content.
type Model struct {
	config

	// Index over the lines on display. The Model loads it from the searcher
	// on the first search after the lines change. In side-by-side mode,
	// index covers the left pane and indexRight covers the right one.
	index      Index
	indexRight Index
	// Revision history. The revIndex field selects the revision on display.
	revisions []Revision
	// Cached diff between the base and current revisions at the indexes that
	// diffKey holds.
	diffResult *diff.Result
	// Copies of the views of the base and current revisions, by
	// [diff.Side], that the Model took when it computed diffResult from
	// them. They hold every line the diff shows and the marks each line
	// carries into the views of the diff.
	diffViews [2]*line.View
	// The content on display before search highlights, for the left pane or
	// main content and for the right pane of a side-by-side diff.
	// In ViewModeFull: the unified diff, or the view of the revision.
	// In ViewModeHunks with diff: the hunks of the diff with their headers.
	// In ViewModeSideBySide with diff: the Before and After views.
	// In ViewModeSideBySide without diff: the view of the revision on the
	// left and nil on the right.
	//
	// A base is the model's own copy of the view the Revision hands out, or
	// the view of the diff. A base never carries search highlights. A
	// layout or a render slices the lines it needs from a base and adds the
	// highlights of those lines to the slice. Each term or selection
	// therefore starts without the highlights of the last.
	baseLeft, baseRight *line.View
	// The indexes in leftMatches and rightMatches of the matches that cover
	// each line, keyed by the index of the line in the content, in
	// ascending order. A slice of a pane finds the highlights of its lines
	// here.
	leftLines, rightLines map[int][]int
	// Rendered row counts of the view. Copies of the Model share one cache
	// until a layout change gives a copy its own. The counts that the
	// value-receiver View fills therefore stay filled for the Model it
	// copied. The cache locks its fill, so copies can render on separate
	// goroutines.
	rows *rowCache
	// The scroll that scrollTo deferred because the content area did not
	// have room for a row, or nil. The first call to ensureRows with room
	// runs it. Every change that gives the area room calls ensureRows, so a
	// scroll waits only while the area does not have room.
	pending *scrollTarget
	// Current search query.
	searchTerm string
	// KeyMap contains the keybindings for viewport navigation.
	KeyMap        KeyMap
	searchMatches []searchMatch
	// The matches in each pane, or in the content of the unified view on
	// the left. The unified view sorts its matches into document order, so
	// the index of a match here is its index in searchMatches. Each
	// side-by-side pane keeps the order its Index returned.
	leftMatches    position.Ranges
	rightMatches   position.Ranges
	horizontalStep int
	revIndex       int
	diffMode       DiffMode
	diffKey        [2]int
	// MouseWheelDelta is the number of rows to scroll per mouse wheel tick.
	// Default: 3.
	MouseWheelDelta int
	width           int
	searchIndex     int
	yOffset         int
	height          int
	xOffset         int
	viewMode        ViewMode
	hunkContext     int
	// The line at the top of the view and the row within it when a layout
	// change dropped the row counts. A line of -1 stands for the top of the
	// view or the container frame above the first line. A line one past the
	// last stands for the frame below the last line. For either frame, the
	// row counts from the first row of the frame.
	anchorLine int
	anchorRow  int
	// Reports that the base views changed since the searcher last loaded
	// them.
	searcherStale bool
	// MouseWheelEnabled enables mouse wheel scrolling.
	// Default: true.
	MouseWheelEnabled bool
	// Wraps lines to the viewport width when true.
	wrapEnabled bool
	// Reports that anchorLine and anchorRow wait for ensureRows to restore
	// them.
	anchored bool
}

// scrollTarget is a range for scrollTo to center: a search match or a
// revealed position.
type scrollTarget struct {
	// The range, in the content of the left pane, or of the right one when
	// right is true.
	rng   position.Range
	right bool
	// Reports that the range is the current search match. A deferred
	// scroll with match set scrolls to the match that is current when
	// ensureRows runs the scroll, not to this range.
	match bool
}

func (m *Model) setInitialValues() {
	m.KeyMap = DefaultKeyMap()
	m.MouseWheelEnabled = true
	m.MouseWheelDelta = 3
	m.horizontalStep = defaultHorizontalStep
	m.hunkContext = 3 // Default context lines around diff hunks.
	m.wrapEnabled = true
	m.searchIndex = -1

	if m.printer == nil {
		m.printer = defaultPrinter()
	}

	if isNil(m.searcher) {
		m.searcher = finderSearcher{finder: finder.New()}
	}

	m.relayout()
}

// defaultPrinter returns the printer that a [Model] renders with when the
// caller does not set one. It renders with [theme.Charm] inside the box of
// [printer.WithBox].
func defaultPrinter() *printer.Printer {
	return printer.New(printer.WithStyles(theme.Charm), printer.WithBox())
}

// Init returns the command that starts the viewport. The viewport does not
// need one, so Init returns nil. Its signature matches [tea.Model.Init], so
// a parent model can call it from its own Init.
//
//nolint:gocritic // hugeParam: value receivers match the Bubble Tea update loop.
func (m Model) Init() tea.Cmd {
	return nil
}

// Height returns the height of the viewport.
func (m *Model) Height() int {
	return m.height
}

// SetHeight sets the height of the viewport and clamps the scroll offsets to
// the new bounds.
func (m *Model) SetHeight(h int) {
	m.height = h

	// Row counts do not depend on the height, so the cache stays. The
	// offsets clamp now rather than on their next read, since a read
	// between two resizes would otherwise decide where the second one
	// leaves the view.
	m.ensureRows()
}

// Width returns the width of the viewport.
func (m *Model) Width() int {
	return m.width
}

// SetWidth sets the width of the viewport and clamps the scroll offsets to
// the new bounds. A width that leaves the content width as it is leaves the
// view where it is. The current width leaves the content width as it is,
// and so does a width that the container style caps.
func (m *Model) SetWidth(w int) {
	old := m.paneWidth()

	m.width = w

	// Row counts depend on the width only through the pane width.
	if m.paneWidth() == old {
		return
	}

	m.relayout()

	// A width that gives the content area room runs a scroll that waits
	// for it, as SetHeight does.
	if m.pending != nil {
		m.ensureRows()
	}
}

// relayout responds to a layout change, such as a new printer, style, wrap
// setting, or width, without rebuilding the view. It gives the Model an empty
// row count cache and leaves any copy that shares the old cache untouched.
// The next read of the scroll bounds fills the new cache.
//
// Row offsets from the old cache point at other lines after the rows reflow,
// so relayout calls anchorTop to record the top line as the old layout shows
// it. The next call to ensureRows scrolls back to that line. A scroll that
// waits for room replaces the anchor, so anchorTop does not record the top
// line then.
func (m *Model) relayout() {
	m.anchorTop()

	m.rows = &rowCache{}
}

// remeasure responds to a change of decoration confined to the lines that
// ranges cover, such as a move of the selected search match. It lays out
// those lines again, because a highlight style can change the width of the
// text it styles.
//
// When a row count or width differs from the cache, remeasure gives the
// Model a new cache that keeps the counts of every other line. Otherwise
// the Model keeps its cache, which already holds those counts. Like
// relayout, it leaves any copy that shares the old cache untouched and
// records the top line for ensureRows to restore. An empty cache does not
// hold any counts to keep, so remeasure drops it as relayout does. A call
// without ranges does not lay out any line, so the cache stays as it is.
func (m *Model) remeasure(ranges ...position.Range) {
	if m.rows == nil || !m.rows.filled() {
		m.relayout()

		return
	}

	// Without ranges, the slices below hold no line, so laying them out
	// changes nothing.
	if len(ranges) == 0 {
		return
	}

	m.anchorTop()

	spans := make([]position.Span, 0, len(ranges))
	for _, r := range ranges {
		spans = append(spans, position.NewSpan(r.Start.Line, r.LastLine()+1))
	}

	p := m.renderPrinter(m.paneWidth())

	left := m.highlightedWithin(false, spans...)
	leftLayout := p.Layout(left)
	changed := m.rows.differs(leftLayout, left, m.rows.left, m.rows.leftWidths)

	var (
		right       *line.View
		rightLayout printer.Layout
	)

	if m.rows.right != nil && m.baseRight != nil {
		right = m.highlightedWithin(true, spans...)
		rightLayout = p.Layout(right)
		changed = changed || m.rows.differs(rightLayout, right, m.rows.right, m.rows.rightWidths)
	}

	// The cache already holds the counts of a decoration that keeps every
	// row count and width, such as a highlight that changes only color.
	if !changed {
		return
	}

	c := m.rows.clone()
	c.measure(leftLayout, left, c.left, c.leftWidths)

	if right != nil {
		c.measure(rightLayout, right, c.right, c.rightWidths)
	}

	c.sum()

	m.rows = c
}

// anchorTop records the line at the top of the view and the row within it
// for ensureRows to restore after the rows reflow. It reads the top line
// from the vertical offset clamped to the bounds of the current layout.
// An anchor that ensureRows has not restored yet stays, so several changes
// in sequence keep the original top line.
//
// A frame row does not belong to any line, so a top row in the container
// frame anchors to the frame instead. The first row of the view anchors to
// the top frame even when the layout does not have one. A view at the top
// therefore stays there when a frame appears.
//
// A deferred scroll replaces the anchor, so anchorTop does not record an
// anchor while one waits. The caller might have set the new size already.
// The rows have not reflowed yet, so a read of the top line here runs that
// scroll on the old row counts.
func (m *Model) anchorTop() {
	if m.anchored || m.pending != nil || m.rows == nil || !m.rows.filled() || len(m.rows.left) == 0 {
		return
	}

	first, _ := m.rowWindow()
	n := len(m.rows.left)

	switch {
	case m.yOffset < max(1, m.rows.sums[0]):
		m.anchorLine = -1
		m.anchorRow = m.yOffset

	case m.yOffset >= m.rows.sums[n]:
		m.anchorLine = n
		m.anchorRow = m.yOffset - m.rows.sums[n]

	default:
		m.anchorLine = first
		m.anchorRow = m.yOffset - m.rows.sums[first]
	}

	m.anchored = true
}

// renderPrinter returns the printer to render with. It specializes the
// configured printer to the viewport's word wrap setting and to the given
// content width. The printer counts the horizontal frame of its container
// style in that width, so a wrapped line and the frame around it together
// fit the content area.
//
// The printer's container takes the given width, so the box it draws covers
// the same columns for every window. A container without that width shrinks
// to the widest row of the window. A window of short lines then draws its
// border inside the edge of the content area.
//
// The viewport sizes the printer's container itself, so renderPrinter drops
// any Width, Height, MaxWidth, or MaxHeight from its style. With those
// sizes, Print wraps, pads, or cuts the rows after the layout counts them.
// The scroll bounds then differ from the rows on screen.
//
// A render prints a slice of the view, so renderPrinter sizes the gutter for
// the largest line number of the whole view rather than of the window. Every
// window then lines up with the layout. In side-by-side mode both panes get
// the gutter of the longer revision, so the one horizontal offset lands on
// the same content column in both. The row cache holds that number. The
// fillRows method computes it whenever the lines, the printer, or the panes
// change, so the caller fills the cache first.
func (m *Model) renderPrinter(width int) *printer.Printer {
	wrapWidth := 0
	if m.wrapEnabled {
		wrapWidth = width
	}

	container := m.printer.ContainerStyle().
		UnsetWidth().
		UnsetHeight().
		UnsetMaxWidth().
		UnsetMaxHeight()

	return m.printer.With(
		printer.WithWrapWidth(wrapWidth),
		printer.WithMaxNumber(m.rows.maxNumber),
		printer.WithContainerWidth(width),
		printer.WithContainerStyle(container),
	)
}

// SetPrinter sets the [*printer.Printer] the viewport renders with. See
// [WithPrinter]. A nil p selects the default printer, as a nil
// [WithPrinter] does.
//
// The view, its diff, and its search matches stay as they are. A theme
// switch therefore costs one render and does not rebuild the diff or the
// search index.
func (m *Model) SetPrinter(p *printer.Printer) {
	if p == nil {
		p = defaultPrinter()
	}

	// A printer never changes once built, so the printer already set keeps
	// the row counts.
	if p == m.printer {
		return
	}

	m.printer = p
	m.relayout()
}

// SetRevision replaces the revision history with a single revision.
//
// It matches a call to [Model.ClearRevisions] and then a call to
// [Model.AddRevision].
func (m *Model) SetRevision(r Revision) {
	m.ClearRevisions()
	m.AddRevision(r)
}

// AddRevision adds a new revision to the history. The viewport then moves
// to the new revision.
//
// The viewport displays the view r hands out, decoration included. It adds
// its search highlights to a clone of the view, so the view itself stays as
// the caller left it. See [Revision]. AddRevision ignores a nil r, or an r
// that holds a nil pointer or nil func, such as a nil [*niceyaml.Source].
func (m *Model) AddRevision(r Revision) {
	m.AddRevisions(r)
}

// AddRevisions adds revisions to the history in order and moves to the last
// one. It matches a call to [Model.AddRevision] for each revision, except
// that the viewport builds only the view and diff of the last one. Loading
// a history therefore costs one diff rather than one per revision.
// AddRevisions skips a nil revision, or one that holds a nil pointer or nil
// func.
func (m *Model) AddRevisions(rs ...Revision) {
	// Copies of a Model share the backing array of revisions, so the appends
	// must not write into spare capacity that another copy can reach.
	revisions := slices.Clip(m.revisions)
	for _, r := range rs {
		if !isNil(r) {
			revisions = append(revisions, r)
		}
	}

	if len(revisions) == len(m.revisions) {
		return
	}

	m.revisions = revisions
	m.revIndex = len(m.revisions) - 1

	m.rebuildViews()
}

// ClearRevisions removes all revisions from the history.
func (m *Model) ClearRevisions() {
	m.revisions = nil
	m.revIndex = 0
	// The cached diff names its revisions by index, and an index of the
	// next history can hold another revision.
	m.diffResult = nil
	m.rebuildViews()
}

// RevisionIndex returns the current revision index. It returns 0 without
// revisions.
func (m *Model) RevisionIndex() int {
	return m.revIndex
}

// RevisionName returns the name of the current revision. It returns an
// empty string without revisions.
func (m *Model) RevisionName() string {
	if !m.hasRevision() {
		return ""
	}

	return m.currentRevision().Name()
}

// RevisionNames returns all revision names in order, or nil without
// revisions.
func (m *Model) RevisionNames() []string {
	if len(m.revisions) == 0 {
		return nil
	}

	names := make([]string, len(m.revisions))
	for i, s := range m.revisions {
		names[i] = s.Name()
	}

	return names
}

// GotoRevision navigates to the revision at index, clamped to the valid range.
// Index 0 always shows the first revision without diff markers. Indexes 1
// through N-1, inclusive, show a diff based on the current [DiffMode].
func (m *Model) GotoRevision(index int) {
	if !m.hasRevision() {
		return
	}

	index = clamp(index, 0, len(m.revisions)-1)
	if index == m.revIndex {
		return
	}

	m.revIndex = index
	m.rebuildViews()
}

// RevisionCount returns the number of revisions in the history.
func (m *Model) RevisionCount() int {
	return len(m.revisions)
}

// AtFirstRevision reports whether the viewport is at revision index 0.
func (m *Model) AtFirstRevision() bool {
	return m.revIndex == 0
}

// AtLatestRevision reports whether the viewport is at the latest revision.
func (m *Model) AtLatestRevision() bool {
	return m.revIndex >= len(m.revisions)-1
}

// ShowingDiff reports whether the viewport is displaying a diff between
// revisions.
//
// It reports true when the viewport is past the first revision and the
// [DiffMode] is not [DiffModeNone].
func (m *Model) ShowingDiff() bool {
	return m.hasRevision() && m.revIndex > 0 && m.diffMode != DiffModeNone
}

// DiffStats returns the number of added and removed lines in the current
// diff, as [diff.Result.Stats] counts them.
//
// It returns the zero [diff.Stats] when the viewport does not show a diff:
// at the first revision, in [DiffModeNone], or without revisions.
func (m *Model) DiffStats() diff.Stats {
	if !m.ShowingDiff() {
		return diff.Stats{}
	}

	return m.getDiffResult().Stats()
}

// DiffMode returns the current diff display mode.
func (m *Model) DiffMode() DiffMode {
	return m.diffMode
}

// SetDiffMode sets the diff display mode and rebuilds the view when the
// change shows other content. The view stays where it is when the mode is
// already set, or when the viewport does not show a diff before the change
// or after it. It also stays when both modes compare against the same
// revision, as [DiffModeAdjacent] and [DiffModeOrigin] do at revision
// index 1. An undefined mode counts as [DiffModeAdjacent], the default. A
// rebuild moves the view back to the top, and a caller can keep its place
// as [Model.SetViewMode] describes.
func (m *Model) SetDiffMode(mode DiffMode) {
	if mode < DiffModeAdjacent || mode > DiffModeNone {
		mode = DiffModeAdjacent
	}

	if mode == m.diffMode {
		return
	}

	oldBase := m.diffBaseIndex()
	m.diffMode = mode

	if m.diffBaseIndex() != oldBase {
		m.rebuildViews()
	}
}

// ToggleDiffMode cycles between diff modes, in the order [DiffModeAdjacent],
// [DiffModeOrigin], [DiffModeNone]. The view rebuilds as it does for
// [Model.SetDiffMode].
func (m *Model) ToggleDiffMode() {
	switch m.diffMode {
	case DiffModeAdjacent:
		m.SetDiffMode(DiffModeOrigin)
	case DiffModeOrigin:
		m.SetDiffMode(DiffModeNone)
	default:
		m.SetDiffMode(DiffModeAdjacent)
	}
}

// ViewMode returns the current view mode.
func (m *Model) ViewMode() ViewMode {
	return m.viewMode
}

// SetViewMode sets the view mode and rebuilds the view when the viewport
// shows a diff. Without a diff every mode shows the same lines, so the
// change counts as a layout change and keeps the top line. Only
// [ViewModeSideBySide] changes the width lines wrap to. Without a diff, a
// switch between [ViewModeFull] and [ViewModeHunks] therefore leaves the
// view where it is, as does the mode already set. An undefined mode counts
// as [ViewModeFull], the default.
//
// A rebuild moves the view back to the top, because each mode of a diff
// shows other rows. To keep its place, a caller takes a [Hit] from
// [Model.HitAt] for a cell of the content before the change and passes it
// to [Model.Reveal] after the change.
func (m *Model) SetViewMode(mode ViewMode) {
	if mode < ViewModeFull || mode > ViewModeSideBySide {
		mode = ViewModeFull
	}

	if mode == m.viewMode {
		return
	}

	if !m.ShowingDiff() {
		prev := m.viewMode
		old := m.paneWidth()

		m.viewMode = mode

		// Row counts depend on the mode only through the pane width, which
		// side-by-side mode halves, so an unchanged pane width keeps the
		// cache, as in SetWidth.
		if m.paneWidth() == old {
			return
		}

		// The rows reflow as they do for a new width. The old mode set the
		// width the old rows wrap to, so anchorTop reads the top line under
		// the old mode.
		m.viewMode = prev
		m.relayout()

		m.viewMode = mode

		// As in SetWidth, a pane width that gives the content area room
		// runs a scroll that waits for it.
		if m.pending != nil {
			m.ensureRows()
		}

		return
	}

	m.viewMode = mode
	m.rebuildViews()
}

// ToggleViewMode cycles through the view modes in the order [ViewModeFull],
// [ViewModeHunks], [ViewModeSideBySide]. The view rebuilds as it does for
// [Model.SetViewMode].
func (m *Model) ToggleViewMode() {
	switch m.viewMode {
	case ViewModeFull:
		m.SetViewMode(ViewModeHunks)
	case ViewModeHunks:
		m.SetViewMode(ViewModeSideBySide)
	default:
		m.SetViewMode(ViewModeFull)
	}
}

// HunkContext returns the number of context lines shown around diff hunks.
func (m *Model) HunkContext() int {
	return m.hunkContext
}

// SetHunkContext sets the number of context lines shown around diff hunks
// in [ViewModeHunks]. The view rebuilds when that mode shows a diff, and
// otherwise stays where it is. The default is 3.
func (m *Model) SetHunkContext(n int) {
	n = max(0, n)
	if n == m.hunkContext {
		return
	}

	m.hunkContext = n

	if m.viewMode == ViewModeHunks && m.ShowingDiff() {
		m.rebuildViews()
	}
}

// WordWrap reports whether lines wrap to the viewport width.
func (m *Model) WordWrap() bool {
	return m.wrapEnabled
}

// SetWordWrap turns word wrapping on or off. Enabling it returns the view to
// its first column. The setting already in place leaves the view where it
// is. The default is on.
func (m *Model) SetWordWrap(enabled bool) {
	if enabled == m.wrapEnabled {
		return
	}

	m.wrapEnabled = enabled

	if enabled {
		m.xOffset = 0
	}

	m.relayout()
}

// ToggleWordWrap toggles word wrapping on or off. See [Model.SetWordWrap].
func (m *Model) ToggleWordWrap() {
	m.SetWordWrap(!m.wrapEnabled)
}

// ContainerStyle returns the container style applied to the viewport frame.
func (m *Model) ContainerStyle() lipgloss.Style {
	return m.style
}

// SetContainerStyle sets the container style applied to the viewport frame.
// The frame size changes the content area, so the scroll offsets clamp to
// it. A style that changes the content width keeps the same line at the
// top, as far as that clamp allows. A style that leaves the content width
// as it is moves the view only as far as that clamp does. A style that
// changes only a border color or the vertical padding leaves the content
// width as it is.
//
//nolint:gocritic // hugeParam: Copying.
func (m *Model) SetContainerStyle(s lipgloss.Style) {
	prev := m.style
	old := m.paneWidth()

	m.style = s

	// Row counts depend on the style only through the pane width, so an
	// unchanged pane width keeps the cache. The vertical frame still sets
	// the content height, so the offsets clamp now, as SetHeight does.
	if m.paneWidth() == old {
		m.ensureRows()

		return
	}

	// The style sets the content height that bounds the offset, so anchorTop
	// reads the top line under the old style.
	m.style = prev
	m.anchorTop()

	m.style = s
	m.relayout()

	// Then ensureRows restores the top line in the new layout and clamps
	// the offsets to the new content area right away, as the branch above
	// does.
	m.ensureRows()
}

// NextRevision moves to the next revision in history. At the latest
// revision it leaves the viewport as it is.
func (m *Model) NextRevision() { m.GotoRevision(m.revIndex + 1) }

// PreviousRevision moves to the previous revision in history. At the first
// revision, index 0, it leaves the viewport as it is.
func (m *Model) PreviousRevision() { m.GotoRevision(m.revIndex - 1) }

// rebuildViews rebuilds the displayed views from the revision, diff mode, and
// view mode, then refreshes the search state and drops the cached row counts.
// Rendering itself waits for View, which renders only the visible window.
//
// Every change of content goes through rebuildViews. A match index or row
// offset from the old content points at an arbitrary line of the new
// content. The view therefore scrolls to the top, or to the first match in
// the new content when the search term has one.
func (m *Model) rebuildViews() {
	m.baseLeft = nil
	m.baseRight = nil
	m.searcherStale = true
	m.searchIndex = -1

	needsDiff := m.ShowingDiff()

	switch {
	case m.viewMode == ViewModeSideBySide && needsDiff:
		result := m.getDiffResult()
		m.baseLeft = m.carryMarks(result.Before())
		m.baseRight = m.carryMarks(result.After())

	case m.viewMode == ViewModeHunks && needsDiff:
		// Hunks holds no lines when the diff has no changes, which leaves
		// the view empty. The diff picks the hunks from its changes alone,
		// so a marked line outside every hunk stays out of the view.
		m.baseLeft = m.carryMarks(m.getDiffResult().Hunks(m.hunkContext))

	default:
		m.baseLeft = m.getDisplayLines()
	}

	m.refreshSearch()

	// The old row counts describe other lines, so the cache starts empty
	// without anchoring to a line of the old content, as relayout would.
	// The horizontal offset describes them too, and a search match in the
	// new content sets it again below.
	m.anchored = false
	m.yOffset = 0
	m.xOffset = 0
	m.rows = &rowCache{}

	// A deferred scroll targets a range of the old content, so rebuildViews
	// drops it with the offsets. Then scrollToCurrentMatch scrolls to a
	// match in the new content, or defers that scroll.
	m.pending = nil

	m.scrollToCurrentMatch()
}

// refreshSearch recomputes the search matches of the current base views and
// the lines each match covers, without rebuilding the views. A zero Model
// does not have the searcher that [New] creates, so refreshSearch does not
// find a match for any term there.
//
// The caller updates the row counts for the new highlights. They change
// only on the lines of the old and the new matches, so a caller that changes
// the term measures only those lines again.
func (m *Model) refreshSearch() {
	switch {
	case m.baseLeft == nil, m.searcher == nil, m.searchTerm == "":
		m.clearMatches()

	case m.viewMode == ViewModeSideBySide && m.baseRight != nil:
		m.updateSideBySideSearchState()

	default:
		m.updateSearchState()
	}

	// Every caller resets the index before a new term or new content, so
	// the search starts over at the first match. SetSearchTerm returns
	// before this point for the term already set, which keeps its match.
	switch {
	case len(m.searchMatches) == 0:
		m.searchIndex = -1
	case m.searchIndex >= len(m.searchMatches), m.searchIndex < 0:
		m.searchIndex = 0
	}

	m.leftLines = matchLines(m.baseLeft, m.leftMatches)
	m.rightLines = matchLines(m.baseRight, m.rightMatches)
}

// matchRanges returns the range of each search match.
func (m *Model) matchRanges() []position.Range {
	ranges := make([]position.Range, 0, len(m.searchMatches))
	for _, match := range m.searchMatches {
		ranges = append(ranges, match.rng)
	}

	return ranges
}

// clearMatches drops the matches of both panes and the selected match.
func (m *Model) clearMatches() {
	m.searchMatches = nil
	m.leftMatches = nil
	m.rightMatches = nil
	m.searchIndex = -1
}

// matchLines returns the indexes in matches of the matches that cover each
// line of the content of view, keyed by the index of the line, in
// ascending order. A match covers each line from its start line through
// [position.Range.LastLine], which are the lines its highlight marks. A
// match that ends at column 0 of a later line therefore does not cover
// that line.
func matchLines(view *line.View, matches position.Ranges) map[int][]int {
	// The last line the view holds bounds the walk, since a line past it
	// never renders, and an Index of another package may report a range
	// past the content.
	last := -1
	for i := range view.All() {
		last = i
	}

	lines := make(map[int][]int, len(matches))

	for k, r := range matches {
		for l := max(0, r.Start.Line); l <= min(r.LastLine(), last); l++ {
			lines[l] = append(lines[l], k)
		}
	}

	return lines
}

// highlightedWithin returns a slice of the base view of the left pane, or
// of the right one when right is true. The slice holds the lines within
// spans with the search highlights of the matches that cover them.
// Without spans, the slice is empty. The base view does not keep
// highlights. A move of the selection therefore costs the lines a caller
// lays out or renders, not every match of the document.
func (m *Model) highlightedWithin(right bool, spans ...position.Span) *line.View {
	base := m.baseLeft
	if right {
		base = m.baseRight
	}

	return m.highlight(right, base.Slice(spans...))
}

// highlight blends into view the search highlights of the matches that
// cover the lines it holds, and returns view. View must be a clone or a
// slice of the base view of the left pane, or of the right one when right
// is true. The base view does not keep highlights.
//
// It blends the matches in the order of the match list, so each line gets
// its overlays in the same order however the caller slices the view.
func (m *Model) highlight(right bool, view *line.View) *line.View {
	matches, lines := m.leftMatches, m.leftLines
	if right {
		matches, lines = m.rightMatches, m.rightLines
	}

	// A match that runs across a line break covers several lines of the
	// slice, so its index repeats.
	var hits []int

	for i := range view.All() {
		hits = append(hits, lines[i]...)
	}

	slices.Sort(hits)

	selected := m.selectedMatch(right)

	for _, k := range slices.Compact(hits) {
		if selected(k) {
			view.BlendOverlay(kind.GenericHighlight, matches[k])
		} else {
			view.BlendOverlay(kind.GenericHighlightDim, matches[k])
		}
	}

	return view
}

// selectedMatch returns a function that reports whether the left pane, or
// the right one when right is true, shows its k-th match as the selected
// match. In side-by-side mode a match on an equal line shows as selected
// in both panes, because the search counts it once.
func (m *Model) selectedMatch(right bool) func(k int) bool {
	if m.searchIndex < 0 || m.searchIndex >= len(m.searchMatches) {
		return func(int) bool { return false }
	}

	if m.viewMode != ViewModeSideBySide || m.baseRight == nil {
		index := m.searchIndex

		return func(k int) bool { return k == index }
	}

	selected := m.searchMatches[m.searchIndex]
	pos := selected.rng.Start

	show := m.equalLine(pos.Line) || selected.inLeft != right

	matches := m.leftMatches
	if right {
		matches = m.rightMatches
	}

	return func(k int) bool { return show && matches[k].Start == pos }
}

// equalLine reports whether both side-by-side panes hold line l unchanged.
// The padding a diff puts opposite an inserted or deleted line is empty and
// carries the default flag too. One pane alone therefore cannot distinguish
// the padding from an unchanged line.
func (m *Model) equalLine(l int) bool {
	return m.baseLeft.Contains(l) && m.baseRight.Contains(l) &&
		m.baseLeft.Flag(l) == line.FlagDefault && m.baseRight.Flag(l) == line.FlagDefault
}

// searchMatch pairs a match range with its source.
type searchMatch struct {
	rng    position.Range
	inLeft bool
}

// updateSideBySideSearchState gathers the matches of a non-empty search term
// in side-by-side mode. It needs a searcher, and it leaves the match index
// to refreshSearch.
//
// It combines the matches from both panes and deduplicates them. A match on
// an equal line counts once, but each match on a deleted or inserted line
// counts on its own.
func (m *Model) updateSideBySideSearchState() {
	// Load both panes once per change of content, as the unified path
	// does, so typing a term does not rebuild the indexes on every
	// keystroke. A nil Index counts as loaded.
	if m.searcherStale {
		m.index = m.searcher.Load(m.baseLeft)
		m.indexRight = m.searcher.Load(m.baseRight)

		m.searcherStale = false
	}

	// Search both panes and cache the results for overlay application.
	m.leftMatches = heldMatches(m.baseLeft, find(m.index, m.searchTerm))
	m.rightMatches = heldMatches(m.baseRight, find(m.indexRight, m.searchTerm))

	// Build combined match list. For equal lines, a match appears in both
	// panes at the same position, so we deduplicate by (row, startCol).
	// For deleted/inserted lines, the match only appears in one pane.
	//
	// Track equal-line match positions from the left pane for deduplication.
	equalLinePositions := make(map[position.Position]bool)

	combined := make([]searchMatch, 0, len(m.leftMatches)+len(m.rightMatches))

	for _, match := range m.leftMatches {
		combined = append(combined, searchMatch{rng: match, inLeft: true})

		// Track equal-line matches for deduplication.
		if m.equalLine(match.Start.Line) {
			equalLinePositions[match.Start] = true
		}
	}

	// Add matches from the right pane and skip duplicates on equal lines.
	for _, match := range m.rightMatches {
		if equalLinePositions[match.Start] {
			continue
		}

		combined = append(combined, searchMatch{rng: match, inLeft: false})
	}

	// Sort by position for consistent navigation order. A deleted line and
	// the inserted line that replaced it share a position, and the left
	// pane's match comes first there.
	slices.SortFunc(combined, func(a, b searchMatch) int {
		if c := comparePos(a.rng.Start, b.rng.Start); c != 0 {
			return c
		}

		switch {
		case a.inLeft && !b.inLeft:
			return -1
		case !a.inLeft && b.inLeft:
			return 1
		default:
			return 0
		}
	})

	m.searchMatches = combined
}

// updateSearchState gathers the matches of a non-empty search term in the
// unified view, which shows every match in the left base view. It needs a
// searcher, and it leaves the match index to refreshSearch.
//
// It reloads the searcher only when the content changed since the last load,
// so typing a search term does not rebuild the index on every keystroke. A
// nil Index counts as loaded.
func (m *Model) updateSearchState() {
	if m.searcherStale {
		m.index = m.searcher.Load(m.baseLeft)

		m.searcherStale = false
	}

	// The unified view shows every match on the left, and its matches do
	// not use inLeft. An Index may return matches in any order, so sort
	// them into document order as the side-by-side path does.
	m.leftMatches = heldMatches(m.baseLeft, find(m.index, m.searchTerm))
	slices.SortStableFunc(m.leftMatches, func(a, b position.Range) int {
		return comparePos(a.Start, b.Start)
	})

	m.rightMatches = nil
	m.searchMatches = make([]searchMatch, 0, len(m.leftMatches))

	for _, rng := range m.leftMatches {
		m.searchMatches = append(m.searchMatches, searchMatch{rng: rng})
	}
}

// comparePos orders positions by line and then by column, the document
// order that both search paths sort their matches into.
func comparePos(a, b position.Position) int {
	return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col))
}

// find returns the matches of search in idx. A [Searcher] can return a nil
// idx or one holding a nil pointer, and find returns nil for either.
func find(idx Index, search string) position.Ranges {
	if isNil(idx) {
		return nil
	}

	return idx.Find(search)
}

// heldMatches returns the matches that start on a line view holds. A
// [Searcher] loads only those lines. An Index of another package might
// still report a range on a line the view skips, such as one between two
// hunks or outside the document of a [niceyaml.Node.View].
func heldMatches(view *line.View, matches position.Ranges) position.Ranges {
	held := make(position.Ranges, 0, len(matches))

	for _, match := range matches {
		if view.Contains(match.Start.Line) {
			held = append(held, match)
		}
	}

	return held
}

// diffBaseIndex returns the index of the revision that the diff compares
// the current one against under the current [DiffMode], or -1 when the
// viewport does not show a diff.
func (m *Model) diffBaseIndex() int {
	if !m.ShowingDiff() {
		return -1
	}

	if m.diffMode == DiffModeOrigin {
		return 0
	}

	return m.revIndex - 1
}

// currentRevision returns the revision on display, or nil without revisions.
func (m *Model) currentRevision() Revision {
	return m.revision(m.revIndex)
}

// revision returns the revision at index, or nil when index is outside the
// history.
func (m *Model) revision(index int) Revision {
	if index < 0 || index >= len(m.revisions) {
		return nil
	}

	return m.revisions[index]
}

// getDisplayLines returns the base view to display for the current
// revision and [DiffMode]. With a diff, that is a new unified diff with the
// marks of its revisions. Without one, it is a copy of the view the revision
// hands out. The copy holds the marks the view carried when the content
// changed, so marks the caller adds later stay off the display. It returns
// nil without a revision.
func (m *Model) getDisplayLines() *line.View {
	if m.ShowingDiff() {
		return m.carryMarks(m.getDiffResult().Unified())
	}

	if rev := m.currentRevision(); rev != nil {
		return rev.View().Clone()
	}

	return nil
}

// getDiffResult returns the [diff.Result] between the base revision for the
// current [DiffMode] and the current revision. It caches the result by the
// pair of revision indexes, and computes the diff again when the pair
// changes or [Model.ClearRevisions] drops the cache.
//
// Callers must check [Model.ShowingDiff] first. Without a diff, the Model
// does not have a base revision.
func (m *Model) getDiffResult() *diff.Result {
	base := m.diffBaseIndex()
	pair := [2]int{base, m.revIndex}
	if m.diffResult == nil || m.diffKey != pair {
		// A diff compares the lines each view holds, as the other view
		// modes render them, rather than every line of the content the
		// view is over. The copies keep the marks the views carry now, so
		// every view of the cached diff shows the same marks.
		m.diffViews = [2]*line.View{m.revision(base).View().Clone(), m.currentRevision().View().Clone()}
		m.diffResult = diff.Diff(m.diffViews[diff.Before], m.diffViews[diff.After])
		m.diffKey = pair
	}

	return m.diffResult
}

// carryMarks copies the marks of the two revisions of the diff on display
// onto view with [diff.Result.Carry], and returns view.
func (m *Model) carryMarks(view *line.View) *line.View {
	m.getDiffResult().Carry(view, m.diffViews[diff.Before], m.diffViews[diff.After])

	return view
}

// paneWidth returns the width lines wrap to: the content width, or in
// side-by-side mode the width of one pane.
func (m *Model) paneWidth() int {
	if m.viewMode == ViewModeSideBySide {
		return max(0, (m.maxWidth()-ansi.StringWidth(sideBySideSeparator))/2)
	}

	return m.maxWidth()
}

// rowCache holds the layout of a view: the row count and width the
// printer reports for each line of each pane, and the prefix sums that
// place every line.
//
// The Models that share a cache can fill it from separate goroutines, so
// fillRows and filled hold the lock while they write or test the counts. A
// filled cache never changes, so a read after either call does not need the
// lock.
type rowCache struct {
	// Row counts of each line the view renders, in content order, for the
	// left and right panes. The right counts are nil outside side-by-side
	// diffs.
	left, right []int
	// Widths in cells of the widest row of each line the view renders, in
	// the order of the row counts, for the left and right panes. The right
	// widths are nil outside side-by-side diffs.
	leftWidths, rightWidths []int
	// The index in the content of the k-th rendered line, ascending.
	indices []int
	// Prefix sums of the taller pane after the top frame. The entry sums[k]
	// is the first row of the k-th rendered line, and the last entry is the
	// first row of the bottom frame. The slice stays nil until fillRows
	// fills the cache.
	sums []int
	// Width in cells of the widest row of either pane.
	width int
	// Largest line number of either pane, which sizes the gutter.
	maxNumber int
	// Rows of the printer's container frame above the first line and below
	// the last. A view without lines does not have frame rows.
	top, bottom int
	// Guards the fill of an empty cache.
	mu sync.Mutex
}

// total returns the number of rows in a filled cache.
func (c *rowCache) total() int {
	return c.sums[len(c.sums)-1] + c.bottom
}

// filled reports whether the cache holds the counts of a view.
func (c *rowCache) filled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.sums != nil
}

// clone returns a copy of a filled cache with row counts and widths of its
// own. A change to the copy therefore leaves every Model that shares c as it
// was. It copies each field rather than the whole struct, so the copy starts
// with a lock of its own.
func (c *rowCache) clone() *rowCache {
	return &rowCache{
		left:        slices.Clone(c.left),
		right:       slices.Clone(c.right),
		leftWidths:  slices.Clone(c.leftWidths),
		rightWidths: slices.Clone(c.rightWidths),
		indices:     c.indices,
		sums:        c.sums,
		width:       c.width,
		maxNumber:   c.maxNumber,
		top:         c.top,
		bottom:      c.bottom,
	}
}

// sum computes the prefix sums and the widest row from the row counts and
// widths of the lines.
func (c *rowCache) sum() {
	sums := make([]int, len(c.left)+1)
	sums[0] = c.top
	c.width = 0

	for k, rows := range c.left {
		c.width = max(c.width, c.leftWidths[k])

		if c.right != nil {
			rows = max(rows, c.right[k])
			c.width = max(c.width, c.rightWidths[k])
		}

		sums[k+1] = sums[k] + rows
	}

	c.sums = sums
}

// measure copies the row count and width of each line of view from layout,
// the layout of view, into rows and widths. The rows and widths slices hold
// an entry for each rendered line of the cache.
func (c *rowCache) measure(layout printer.Layout, view *line.View, rows, widths []int) {
	for i := range view.All() {
		if k, ok := slices.BinarySearch(c.indices, i); ok {
			rows[k] = layout.LineRows(i)
			widths[k] = layout.LineWidth(i)
		}
	}
}

// differs reports whether layout, the layout of view, gives any line of
// view a row count or width other than the one in rows and widths. The rows
// and widths slices hold an entry for each rendered line of the cache.
func (c *rowCache) differs(layout printer.Layout, view *line.View, rows, widths []int) bool {
	for i := range view.All() {
		k, ok := slices.BinarySearch(c.indices, i)
		if ok && (rows[k] != layout.LineRows(i) || widths[k] != layout.LineWidth(i)) {
			return true
		}
	}

	return false
}

// ensureRows fills the row count cache when it is empty, scrolls back to the
// row that anchorTop recorded, and clamps both scroll offsets to the cache's
// bounds. When the content area has room for a row, it then runs the scroll
// that scrollTo deferred, which replaces the restored row. It checks on
// every call, because a copy of the Model can fill a shared cache without
// restoring or clamping this Model's offsets.
func (m *Model) ensureRows() {
	m.fillRows()

	if m.anchored {
		m.anchored = false

		// The anchored row keeps its place in the line or frame, up to the
		// new last row of that line or frame. A frame that is gone leaves the
		// row at the edge of the lines it stood beside.
		if n := len(m.rows.left); n > 0 {
			switch k := m.anchorLine; {
			case k < 0:
				m.yOffset = min(m.anchorRow, max(0, m.rows.sums[0]-1))
			case k >= n:
				m.yOffset = m.rows.sums[n] + min(m.anchorRow, max(0, m.rows.bottom-1))
			default:
				m.yOffset = m.rows.sums[k] + min(m.anchorRow, max(0, m.lineRows(k)-1))
			}
		}
	}

	m.yOffset = clamp(m.yOffset, 0, m.rowOffsetLimit())
	m.xOffset = clamp(m.xOffset, 0, m.maxXOffset())

	// The anchor keeps the top line of an earlier layout. A deferred scroll
	// waited for the first layout with room, so it wins. Clearing it first
	// keeps the ensureRows calls inside scrollTo from running it again.
	if p := m.pending; p != nil && m.canRender() {
		m.pending = nil

		if p.match {
			m.scrollToCurrentMatch()
		} else {
			m.scrollTo(*p)
		}
	}
}

// fillRows computes the row counts of the view into the cache when it is
// empty. A Model without a printer, such as a zero Model, does not have
// rows.
//
// Copies of the Model that share the cache can fill it at the same time,
// so fillRows holds the lock of the cache. It leaves a filled cache as it
// is. A layout change gives a Model a cache of its own, so every Model that
// shares a cache has the same layout. The first fill therefore holds the
// counts of them all.
func (m *Model) fillRows() {
	if m.rows == nil {
		m.rows = &rowCache{}
	}

	c := m.rows

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sums != nil {
		return
	}

	c.left, c.right = nil, nil
	c.leftWidths, c.rightWidths = nil, nil
	c.top, c.bottom = 0, 0
	c.maxNumber = 0

	if m.baseLeft != nil && m.printer != nil {
		c.maxNumber = m.printer.MaxNumber(m.baseLeft)
		if m.viewMode == ViewModeSideBySide && m.baseRight != nil {
			c.maxNumber = max(c.maxNumber, m.printer.MaxNumber(m.baseRight))
		}

		p := m.renderPrinter(m.paneWidth())

		left := m.highlight(false, m.baseLeft.Clone())
		c.indices, c.left, c.leftWidths = layoutRows(p.Layout(left), left)

		if m.viewMode == ViewModeSideBySide && m.baseRight != nil {
			right := m.highlight(true, m.baseRight.Clone())
			_, c.right, c.rightWidths = layoutRows(p.Layout(right), right)
		}

		// A layout counts the rows before the container style applies. Print
		// adds the container's frame above and below the lines it renders.
		if len(c.left) > 0 {
			frame := p.ContainerStyle()
			c.top = frame.GetMarginTop() + frame.GetBorderTopSize() + frame.GetPaddingTop()
			c.bottom = frame.GetPaddingBottom() + frame.GetBorderBottomSize() + frame.GetMarginBottom()
		}
	}

	c.sum()
}

// lineRows returns the number of rows the k-th rendered line takes, which is
// the taller of its two panes in side-by-side mode.
func (m *Model) lineRows(k int) int {
	return m.rows.sums[k+1] - m.rows.sums[k]
}

// rowWindow returns the rendered lines [first, last) that own the rows of the
// visible window. The window starts at the vertical offset and is one
// content height tall.
//
// The range holds at least one line whenever the view has lines. A frame
// row does not belong to any line. A window that lies entirely inside the
// top or the bottom frame therefore still needs a line to render the frame
// around.
func (m *Model) rowWindow() (int, int) {
	m.ensureRows()

	n := len(m.rows.sums) - 1
	if n == 0 {
		return 0, 0
	}

	top := m.yOffset
	bottom := top + m.maxHeight()

	// The last line starting at or above the top row, and the first line
	// starting at or below the bottom row.
	first := clamp(sort.SearchInts(m.rows.sums, top+1)-1, 0, n-1)
	last := clamp(sort.SearchInts(m.rows.sums, bottom), first+1, n)

	return first, last
}

// window returns the span of content indices that selects the rendered
// lines [first, last) from the view. The view holds its lines in ascending
// order, so a slice of the view by the span renders only those lines.
func (m *Model) window(first, last int) position.Span {
	if first >= last {
		return position.Span{}
	}

	indices := m.rows.indices

	return position.NewSpan(indices[first], indices[last-1]+1)
}

// trimWindow drops the rows above and below the visible window from rows,
// which hold the rendered lines starting at the first line of the window. For
// the first line of the view, rows start with the top frame.
func (m *Model) trimWindow(rows []string, first int) []string {
	start := m.rows.sums[first]
	if first == 0 {
		start = 0
	}

	skip := min(m.yOffset-start, len(rows))
	rows = rows[skip:]

	return rows[:min(len(rows), m.maxHeight())]
}

// trimFrame drops from rows the container frame rows that Print adds around
// the lines [first, last). It keeps the top frame above the first line of
// the view and the bottom frame below its last line.
func (m *Model) trimFrame(rows []string, first, last int) []string {
	if first > 0 {
		rows = rows[min(m.rows.top, len(rows)):]
	}

	if last < len(m.rows.left) {
		rows = rows[:max(0, len(rows)-m.rows.bottom)]
	}

	return rows
}

// AtTop reports whether the viewport is scrolled to the top.
func (m *Model) AtTop() bool {
	return !m.hasContent() || m.YOffset() <= 0
}

// AtBottom reports whether the viewport is scrolled to or past the bottom.
func (m *Model) AtBottom() bool {
	return !m.hasContent() || m.YOffset() >= m.maxYOffset()
}

// ScrollPercent returns the vertical scroll position as a float between 0 and
// 1, inclusive. It is 1 at the bottom, where [Model.AtBottom] reports true.
func (m *Model) ScrollPercent() float64 {
	if m.baseLeft == nil {
		return 1.0
	}

	return scrollPercent(m.YOffset(), m.maxYOffset())
}

// HorizontalScrollPercent returns the horizontal scroll position as a float
// between 0 and 1, inclusive. It is 1 when every row fits the content width.
// Wrapped rows fit unless an annotation column past the end of its line lies
// past the wrap width, or a style transform widens a row.
func (m *Model) HorizontalScrollPercent() float64 {
	if m.baseLeft == nil || m.printer == nil {
		return 1.0
	}

	return scrollPercent(m.XOffset(), m.maxXOffset())
}

// scrollPercent returns offset as a fraction of limit, the largest offset
// the view can reach, clamped between 0 and 1, inclusive. It is 1 when limit
// is 0 or less.
func scrollPercent(offset, limit int) float64 {
	if limit <= 0 {
		return 1.0
	}

	return clamp(float64(offset)/float64(limit), 0, 1)
}

// maxYOffset returns the maximum Y offset, in rows.
func (m *Model) maxYOffset() int {
	m.ensureRows()

	return m.rowOffsetLimit()
}

// rowOffsetLimit returns the last row the view can start at, from a filled
// cache. That row brings the last row of the view to the bottom of the
// content area. The content area has a height of 0 when the height is 0 or
// the container frame is as tall as the height. For such an area, that row
// lies one row past the end of the view, so rowOffsetLimit returns the last
// row of the view instead.
func (m *Model) rowOffsetLimit() int {
	total := m.rows.total()

	return max(0, min(total-m.maxHeight(), total-1))
}

// lineCount returns the number of lines the view renders.
func (m *Model) lineCount() int {
	return m.baseLeft.Count()
}

// maxXOffset returns the maximum X offset, which brings the last column of
// the widest rendered row into view. It is 0 when every row fits the content
// width. Wrapped rows fit unless an annotation column past the end of its
// line lies past the wrap width, or a style transform widens a row.
func (m *Model) maxXOffset() int {
	if m.baseLeft == nil || m.printer == nil {
		return 0
	}

	return max(0, m.rowWidth()-m.scrollWidth())
}

// rowWidth returns the width in cells of the widest row the printer renders
// for the view before the container frame applies, across both panes in
// side-by-side mode. It reads the line widths the layouts report, so
// annotation rows and wide characters count toward the horizontal scroll
// bound.
func (m *Model) rowWidth() int {
	// Fill the cache without ensureRows, which clamps the horizontal offset
	// through this method.
	m.fillRows()

	return m.rows.width
}

// layoutRows returns the index in the content of each line view holds, the
// number of rows each takes in layout, and the width of its widest row, in
// content order.
func layoutRows(layout printer.Layout, view *line.View) ([]int, []int, []int) {
	indices := make([]int, 0, view.Count())
	rows := make([]int, 0, view.Count())
	widths := make([]int, 0, view.Count())

	for i := range view.All() {
		indices = append(indices, i)
		rows = append(rows, layout.LineRows(i))
		widths = append(widths, layout.LineWidth(i))
	}

	return indices, rows, widths
}

// scrollWidth returns the number of row columns a pane shows at a time: the
// pane width minus the horizontal frame of the printer's container. The
// cutRow method keeps that frame in place.
func (m *Model) scrollWidth() int {
	return max(0, m.paneWidth()-m.printer.ContainerStyle().GetHorizontalFrameSize())
}

// outerSize returns the width and height of the viewport frame: the set
// dimensions, capped by any fixed or maximum size on the container style.
// As in lipgloss, a fixed size excludes the style's margins, so that cap
// adds them back. Lipgloss cuts to MaxWidth and MaxHeight after it adds
// the margins, so those caps apply as they are.
func (m *Model) outerSize() (int, int) {
	w, h := m.Width(), m.Height()
	if sw := m.style.GetWidth(); sw != 0 {
		w = min(w, sw+m.style.GetHorizontalMargins())
	}

	if sh := m.style.GetHeight(); sh != 0 {
		h = min(h, sh+m.style.GetVerticalMargins())
	}

	if mw := m.style.GetMaxWidth(); mw > 0 {
		w = min(w, mw)
	}

	if mh := m.style.GetMaxHeight(); mh > 0 {
		h = min(h, mh)
	}

	return w, h
}

// maxWidth returns the content width accounting for frame size.
func (m *Model) maxWidth() int {
	w, _ := m.outerSize()

	return max(0, w-m.style.GetHorizontalFrameSize())
}

// maxHeight returns the content height accounting for frame size.
func (m *Model) maxHeight() int {
	_, h := m.outerSize()

	return max(0, h-m.style.GetVerticalFrameSize())
}

// hasContent reports whether the view has lines to display.
func (m *Model) hasContent() bool {
	return m.lineCount() > 0
}

// hasRevision reports whether the history holds a revision.
func (m *Model) hasRevision() bool {
	return len(m.revisions) > 0
}

// canRender reports whether the content area has room for any rows. In
// side-by-side mode each pane needs a column beside the separator.
func (m *Model) canRender() bool {
	return m.maxHeight() > 0 && m.paneWidth() > 0
}

// visibleRows renders the rows of the visible window, trimmed to the content
// height and without the padding renderContent adds.
func (m *Model) visibleRows() []string {
	if !m.canRender() || !m.hasContent() {
		return nil
	}

	first, last := m.rowWindow()
	if first >= last {
		return nil
	}

	p := m.renderPrinter(m.maxWidth())
	rows := splitLines(p.Print(m.highlightedWithin(false, m.window(first, last))))
	rows = m.trimWindow(m.trimFrame(rows, first, last), first)

	// Rows may run past the content width, with wrap off or when the
	// printer renders a row wider than the wrap width (see
	// [printer.WithContainerWidth]). Cutting every row to the horizontal
	// window keeps the container frame in its columns.
	maxWidth := m.maxWidth()

	for i := range rows {
		rows[i] = m.cutRow(rows[i], m.rowOffset(m.yOffset+i), maxWidth)
	}

	return rows
}

// rowOffset returns the horizontal offset of the rendered row r. A row of
// the container frame above the first line or below the last does not hold
// content, so its offset is 0. A row of a line takes the horizontal scroll
// offset.
func (m *Model) rowOffset(r int) int {
	if r < m.rows.top || r >= m.rows.total()-m.rows.bottom {
		return 0
	}

	return m.xOffset
}

// cutRow fits a rendered row to width by cutting its content columns to the
// horizontal window that starts at offset. The columns of the printer's
// container frame on either side stay in place, so a border keeps its edges
// and the content between them scrolls. A wide character that an edge of
// the window cuts shows as spaces in its style.
func (m *Model) cutRow(row string, offset, width int) string {
	left, right := m.printerFrame()

	inner := ansi.StringWidth(row) - left - right
	if inner < 0 {
		return printer.Cut(row, 0, width)
	}

	visible := max(0, width-left-right)
	body := printer.Cut(row, left, left+inner)

	// The side-by-side view pads a pane to its own widest row. The offset
	// runs to the widest row of either pane, so the window can reach past
	// the content of this row. Filling the window keeps the right frame in
	// its column.
	content := m.printer.Pad(printer.Cut(body, offset, offset+visible), visible)

	return printer.Cut(row, 0, left) + content + printer.Cut(row, left+inner, left+inner+right)
}

// printerFrame returns the cells the frame of the printer's container style
// takes on the left and on the right of every row.
func (m *Model) printerFrame() (int, int) {
	frame := m.printer.ContainerStyle()
	left := frame.GetMarginLeft() + frame.GetBorderLeftSize() + frame.GetPaddingLeft()

	return left, frame.GetHorizontalFrameSize() - left
}

// SetYOffset sets the vertical offset, in rows, clamped to the scrollable
// range. It drops a search or [Model.Reveal] scroll that waits for the
// content area to have room for a row, as the [Model] doc describes.
func (m *Model) SetYOffset(n int) {
	m.pending = nil
	m.yOffset = clamp(n, 0, m.maxYOffset())
}

// YOffset returns the vertical offset, the index of the rendered row at the
// top of the viewport.
func (m *Model) YOffset() int {
	m.ensureRows()

	return m.yOffset
}

// SetXOffset sets the X offset. Like [Model.SetYOffset], it drops a scroll
// that waits for the content area to have room for a row.
func (m *Model) SetXOffset(n int) {
	m.pending = nil
	m.xOffset = clamp(n, 0, m.maxXOffset())
}

// XOffset returns the current X offset.
func (m *Model) XOffset() int {
	m.ensureRows()

	return m.xOffset
}

// ScrollDown moves the view down by n rows. A negative n moves it up.
func (m *Model) ScrollDown(n int) {
	// Clamping the step to the rows on either side keeps y+n from
	// overflowing for a huge n.
	y := m.YOffset()
	m.SetYOffset(y + clamp(n, -y, m.maxYOffset()-y))
}

// ScrollUp moves the view up by n rows. A negative n moves it down.
func (m *Model) ScrollUp(n int) {
	y := m.YOffset()
	m.SetYOffset(y - clamp(n, y-m.maxYOffset(), y))
}

// PageDown moves the view down by one page.
func (m *Model) PageDown() {
	m.ScrollDown(m.maxHeight())
}

// PageUp moves the view up by one page.
func (m *Model) PageUp() {
	m.ScrollUp(m.maxHeight())
}

// HalfPageDown moves the view down by half a page, and by one row when half
// a page rounds down to 0 rows.
func (m *Model) HalfPageDown() {
	if h := m.maxHeight(); h > 0 {
		m.ScrollDown(max(1, h/2))
	}
}

// HalfPageUp moves the view up by half a page, and by one row when half a
// page rounds down to 0 rows.
func (m *Model) HalfPageUp() {
	if h := m.maxHeight(); h > 0 {
		m.ScrollUp(max(1, h/2))
	}
}

// ScrollLeft moves the viewport left by n columns.
func (m *Model) ScrollLeft(n int) {
	x := m.XOffset()
	m.SetXOffset(x - clamp(n, x-m.maxXOffset(), x))
}

// ScrollRight moves the viewport right by n columns.
func (m *Model) ScrollRight(n int) {
	x := m.XOffset()
	m.SetXOffset(x + clamp(n, -x, m.maxXOffset()-x))
}

// SetHorizontalStep sets the horizontal scroll step size.
func (m *Model) SetHorizontalStep(n int) {
	m.horizontalStep = max(0, n)
}

// GotoTop scrolls to the top.
func (m *Model) GotoTop() {
	m.SetYOffset(0)
}

// GotoBottom scrolls to the bottom.
func (m *Model) GotoBottom() {
	m.SetYOffset(m.maxYOffset())
}

// TotalLineCount returns the number of lines in the view. When
// [ViewModeHunks] shows a diff, it counts only the lines inside the hunks,
// not the whole diff.
func (m *Model) TotalLineCount() int {
	return m.lineCount()
}

// TopLine returns the index of the line that owns the top row of the view.
// It counts lines as [Model.TotalLineCount] does, not rows, so a line that
// wraps to several rows counts once. When the top row belongs to the frame
// of the container style, TopLine returns the nearest line. The nearest
// line is the first line for a row in the top frame, and the last line for
// a row in the bottom frame. It returns 0 for a view without lines.
func (m *Model) TopLine() int {
	first, _ := m.rowWindow()

	return first
}

// VisibleLineCount returns the number of lines with at least one row on
// screen. It returns 0 when the content area does not have room for a row,
// such as at a height of 0 or when the container frame fills the width.
// Otherwise it reports at least 1 whenever the view has lines, because a
// window that lies wholly inside the container frame still renders around a
// line.
func (m *Model) VisibleLineCount() int {
	if !m.canRender() || !m.hasContent() {
		return 0
	}

	first, last := m.rowWindow()

	return last - first
}

// TotalRowCount returns the number of rendered rows in the view. It counts
// the rows of every line, wrapped to the viewport width, plus their
// annotations and the frame of the printer's container style.
func (m *Model) TotalRowCount() int {
	m.ensureRows()

	return m.rows.total()
}

// VisibleRowCount returns the number of rendered rows on screen, without the
// padding that fills the rest of the viewport height.
func (m *Model) VisibleRowCount() int {
	if !m.canRender() {
		return 0
	}

	return clamp(m.TotalRowCount()-m.YOffset(), 0, m.maxHeight())
}

// HitAt returns the place in a revision that the cell at column x of row y
// of the viewport shows, and true. The cell counts from the top left
// corner of what [Model.View] renders, the frame of the container style
// included. A parent that draws the viewport elsewhere subtracts the cell
// it draws that corner at from the cell of a mouse message:
//
//	case tea.MouseClickMsg:
//		hit, ok := m.viewport.HitAt(msg.X-m.left, msg.Y-m.top)
//		if !ok {
//			break
//		}
//
//		if path, ok := m.docs[hit.Revision].PathAt(hit.Position); ok {
//			m.status = path.String()
//		}
//
// A diff shows lines of two revisions, so the [Hit] names the revision
// that holds the line under the cell. For a deleted line, and for every
// line of the left pane of [ViewModeSideBySide], that is the revision the
// diff compares with. For any other line it is the current revision. The
// position counts in the content of that revision, whatever the diff
// shows around the line. Without a diff both panes of ViewModeSideBySide
// show the current revision.
//
// HitAt reports false for a cell that does not show content of a revision.
// Such a cell lies in the frame of either container style, in the gutter,
// in the separator between two panes, or on an annotation row such as a
// hunk header. The filler past the end of a row, beside a shorter pane, and
// below the last row does not show content either. A wide character that an
// edge of the window cuts shows as spaces in its style, and HitAt reports
// that character for each of them. It reports false for every cell of a
// zero Model.
//
// [line.View.Clip] returns a view that clips its lines. A revision with
// such a view shows part of each long line, with "..." for the columns the
// clip leaves out. A cell of such a line gives the column of the line it
// shows, and HitAt reports false for a cell of a "...".
func (m *Model) HitAt(x, y int) (Hit, bool) {
	if m.printer == nil || !m.canRender() || !m.hasContent() {
		return Hit{}, false
	}

	// The content area lies inside the frame of the viewport's container
	// style.
	x -= m.style.GetMarginLeft() + m.style.GetBorderLeftSize() + m.style.GetPaddingLeft()
	y -= m.style.GetMarginTop() + m.style.GetBorderTopSize() + m.style.GetPaddingTop()

	if x < 0 || x >= m.maxWidth() || y < 0 || y >= m.maxHeight() {
		return Hit{}, false
	}

	m.ensureRows()

	// No line owns a row of the printer's frame, above the first line or
	// below the last, or a row that fills the height below the view.
	row := m.yOffset + y
	if row < m.rows.top || row >= m.rows.total()-m.rows.bottom {
		return Hit{}, false
	}

	// The last line that starts at or above the row.
	k := sort.SearchInts(m.rows.sums, row+1) - 1
	row -= m.rows.sums[k]

	width, right := m.paneWidth(), false

	if m.viewMode == ViewModeSideBySide {
		// The right pane ends at the edge of the content area, and the
		// separator fills the cells between the panes.
		switch second := m.maxWidth() - width; {
		case x >= second:
			x -= second
			right = true

		case x >= width:
			return Hit{}, false
		}
	}

	// A line that wraps taller in the other pane leaves blank rows below
	// its own rows in this one.
	count := m.rows.left[k]
	if right && m.rows.right != nil {
		count = m.rows.right[k]
	}

	if row >= count {
		return Hit{}, false
	}

	// Without a diff, both panes show the left content.
	right = right && m.baseRight != nil

	pos, ok := m.paneHit(right, k, row, x, width)
	if !ok {
		return Hit{}, false
	}

	return m.revisionHit(right, pos)
}

// paneHit returns the position in the content of a pane that a cell of the
// pane shows, and true. The pane is the right one when right is true, and
// the left one otherwise. The cell counts from the left edge of the pane,
// which is width cells wide, on the given row of the k-th rendered line. It
// reports false for a cell that does not show content, as [Model.HitAt]
// lists them.
func (m *Model) paneHit(right bool, k, row, cell, width int) (position.Position, bool) {
	left, rightFrame := m.printerFrame()
	visible := max(0, width-left-rightFrame)

	// The frame of the printer's container stays in place on either side,
	// and the content between its sides scrolls.
	cell -= left
	if cell < 0 || cell >= visible {
		return position.Position{}, false
	}

	// The cut keeps every cell of the row in place, so the cell of the
	// window plus the offset is the cell of the rendered row. A cell that
	// shows a space in place of a cluster an edge of the window cuts gives
	// the column of that cluster.
	i := m.rows.indices[k]
	layout := m.renderPrinter(width).Layout(m.highlightedWithin(right, position.NewSpan(i, i+1)))

	return layout.PositionAt(row, m.xOffset+cell-layout.GutterWidth())
}

// revisionHit returns the [Hit] of pos, and true. The position pos lies in
// the content of the left pane, or of the right one when right is true. A
// diff holds the lines of the two revisions it compares. The hit names the
// revision of the line the row shows and the index of the line there, as
// [diff.Result.UnifiedOrigin] and [diff.Result.PaneOrigin] report them. It
// reports false for a filler row.
func (m *Model) revisionHit(right bool, pos position.Position) (Hit, bool) {
	if !m.ShowingDiff() {
		return Hit{Revision: m.revIndex, Position: pos}, true
	}

	var (
		origin diff.Origin
		ok     bool
	)

	result := m.getDiffResult()

	switch {
	case m.viewMode != ViewModeSideBySide:
		origin, ok = result.UnifiedOrigin(pos.Line)
	case right:
		origin, ok = result.PaneOrigin(diff.After, pos.Line)
	default:
		origin, ok = result.PaneOrigin(diff.Before, pos.Line)
	}

	if !ok {
		return Hit{}, false
	}

	index := m.revIndex
	if origin.Side == diff.Before {
		index = m.diffBaseIndex()
	}

	return Hit{Revision: index, Position: position.New(origin.Index, pos.Col)}, true
}

// Reveal scrolls the viewport to bring hit on screen and reports true. It
// takes a [Hit] that [Model.HitAt] returned, or one the parent builds from
// a position in a revision, such as the start of a line to go to:
//
//	m.viewport.Reveal(yamlviewport.Hit{
//		Revision: m.viewport.RevisionIndex(),
//		Position: position.New(line, 0),
//	})
//
// Reveal scrolls as a search does for its current match. It centers the
// row that shows the position. With wrapping off it scrolls sideways only
// when the position lies past the right edge of the first screen. It then
// centers the position. Before the content area has room for a row, Reveal
// reports its result at the call and scrolls when the area has room, as the
// [Model] doc describes.
//
// Reveal does not change the revision on display, the [ViewMode], or the
// [DiffMode]. It reports false and leaves the viewport as it is when the
// view on display does not show the position. The viewport shows the
// current revision and, with a diff, the revision the diff compares with.
// Reveal reports false for a Hit of any other revision. It also reports
// false for a line outside every hunk of [ViewModeHunks] and for a line
// that the view of the revision does not hold. It reports false for a
// column past the end of its line and for every Hit on a zero Model.
//
// A diff shows an unchanged line of the revision it compares with in the
// left pane of [ViewModeSideBySide]. Every other view of the diff shows
// that line once, as a line of the current revision. Reveal scrolls to it
// there, and [Model.HitAt] names the current revision for its cells.
func (m *Model) Reveal(hit Hit) bool {
	right, pos, ok := m.locate(hit)
	if !ok {
		return false
	}

	// The range covers the column of the position, so a sideways scroll
	// brings the whole of its cell on screen.
	return m.scrollTo(scrollTarget{
		rng:   position.NewRange(pos, position.New(pos.Line, pos.Col+1)),
		right: right,
	})
}

// locate finds hit in the views on display. It returns whether the right
// pane shows hit rather than the left, the position that shows it in the
// content of that pane, and true. The last result is false when the lines
// on display do not show hit, as [Model.Reveal] lists the cases.
func (m *Model) locate(hit Hit) (bool, position.Position, bool) {
	pos := hit.Position

	if !m.ShowingDiff() {
		// Without a diff every pane shows the view of the current
		// revision, which the left base view copies.
		return false, pos, hit.Revision == m.revIndex && holds(m.baseLeft, pos)
	}

	var side diff.Side

	switch hit.Revision {
	case m.diffBaseIndex():
		side = diff.Before
	case m.revIndex:
		side = diff.After
	default:
		return false, position.Position{}, false
	}

	if !holds(m.diffViews[side], pos) {
		return false, position.Position{}, false
	}

	origin := diff.Origin{Side: side, Index: pos.Line}
	result := m.getDiffResult()

	// A side-by-side diff holds every line of the revision it compares
	// with in its left pane and every line of the current one in its right
	// one.
	if m.viewMode == ViewModeSideBySide {
		row, ok := result.PaneRow(origin)

		return side == diff.After, position.New(row, pos.Col), ok
	}

	// Any other view of the diff shows each unchanged line of the revision
	// it compares with as a line of the current one. The hunks hold only
	// the rows a hunk covers.
	row, ok := result.UnifiedRow(origin)

	return false, position.New(row, pos.Col), ok && m.baseLeft.Contains(row)
}

// holds reports whether view holds the line of pos and the line reaches the
// column of pos. That column can be the end of the line.
func holds(view *line.View, pos position.Position) bool {
	return pos.Col >= 0 && view.Contains(pos.Line) && pos.Col <= view.Line(pos.Line).Width()
}

// SetSearchTerm sets the search term and updates highlights. An empty term
// clears all search highlights, as [Model.ClearSearch] does.
//
// The term stays set across content changes. A change of content, as the
// [Model] doc defines it, restarts the search at the first match in the new
// content and scrolls to it. A new term likewise starts at its first
// match, but setting the same term again keeps the current match. Before the
// content area has room for a row, the scroll to the match waits until the
// area has room, as the [Model] doc describes.
func (m *Model) SetSearchTerm(term string) {
	if term == "" {
		m.ClearSearch()

		return
	}

	// The same term keeps the current match, its highlights, and the scroll
	// position, so a parent that pushes the term on every update does not
	// snap the view back to the match or lay out the content again. Every
	// change of content refreshes the search, and the searcher stays the
	// one New set, so the matches of the same term still hold.
	if term == m.searchTerm {
		return
	}

	// Only the lines of the old matches and of the new ones change style,
	// so only they need new row counts.
	changed := m.matchRanges()

	// The match index of another term points at an arbitrary match of this
	// one.
	m.searchIndex = -1
	m.searchTerm = term
	m.refreshSearch()
	m.remeasure(append(changed, m.matchRanges()...)...)
	m.scrollToCurrentMatch()
}

// SearchTerm returns the current search term.
func (m *Model) SearchTerm() string {
	return m.searchTerm
}

// ClearSearch removes all search highlights and clears the search term.
// Without a term, ClearSearch leaves the view and its layout as they are.
func (m *Model) ClearSearch() {
	if m.searchTerm == "" {
		return
	}

	// Only the lines of the old matches lose their highlights.
	changed := m.matchRanges()

	m.searchTerm = ""
	m.refreshSearch()
	m.remeasure(changed...)
}

// SearchNext navigates to the next search match.
func (m *Model) SearchNext() {
	m.navigateSearch(1)
}

// SearchPrevious navigates to the previous search match.
func (m *Model) SearchPrevious() {
	m.navigateSearch(-1)
}

// navigateSearch moves the search index by delta and wraps around at either
// end of the matches.
func (m *Model) navigateSearch(delta int) {
	if len(m.searchMatches) == 0 {
		return
	}

	prev := m.searchMatches[m.searchIndex]
	m.searchIndex = (m.searchIndex + delta + len(m.searchMatches)) % len(m.searchMatches)

	// Only the match that lost the selection and the one that gained it
	// change style, so only their lines need new row counts. Rendering
	// happens lazily in View.
	m.remeasure(prev.rng, m.searchMatches[m.searchIndex].rng)

	m.scrollToCurrentMatch()
}

// SearchIndex returns the index of the current search match, counting from
// 0, or -1 without matches.
func (m *Model) SearchIndex() int {
	// The stored index of a zero Model stays 0 until a search runs, so the
	// match count decides whether a current match exists.
	if len(m.searchMatches) == 0 {
		return -1
	}

	return m.searchIndex
}

// SearchCount returns the total number of search matches.
func (m *Model) SearchCount() int {
	return len(m.searchMatches)
}

// scrollToCurrentMatch scrolls to the current search match, as scrollTo
// does for its range.
func (m *Model) scrollToCurrentMatch() {
	if m.searchIndex < 0 || m.searchIndex >= len(m.searchMatches) {
		return
	}

	match := m.searchMatches[m.searchIndex]

	m.scrollTo(scrollTarget{
		rng:   match.rng,
		right: m.baseRight != nil && !match.inLeft,
		match: true,
	})
}

// scrollTo scrolls to center the row that holds the start of the range of
// target. Sideways, it returns to the first screen. When wrap is off and
// the range ends past that screen, it centers the start of the range
// instead. It reports whether the pane renders the line the range starts
// on, and leaves both offsets as they are when it does not.
//
// A content area without room for a row does not have a center row or a
// first screen, so scrollTo defers both offsets. The first call to ensureRows
// with room runs the scroll, and a search target scrolls to the match that
// is current then. The row counts fill at any size, so scrollTo reports its
// result at the call.
func (m *Model) scrollTo(target scrollTarget) bool {
	m.ensureRows()

	rng, right := target.rng, target.right

	view := m.baseLeft
	if right {
		view = m.baseRight
	}

	i := rng.Start.Line

	k, ok := slices.BinarySearch(m.rows.indices, i)
	if !ok || view == nil {
		return false
	}

	// The row of the range within its line comes from a layout of the line
	// alone in the pane it is in, and the line's first row from the sums
	// that place the taller of the two panes.
	layout := m.renderPrinter(m.paneWidth()).Layout(m.highlightedWithin(right, position.NewSpan(i, i+1)))

	lineRow := layout.RowOf(rng.Start)
	if lineRow < 0 {
		return false
	}

	if !m.canRender() {
		m.pending = &target

		return true
	}

	row := m.rows.sums[k] + lineRow

	// (maxHeight-1)/2 puts the range on the visual center row. A height of
	// 22 gives (22-1)/2 = 10, and a height of 21 gives (21-1)/2 = 10, so
	// the range lands on row 10 in both cases.
	m.SetYOffset(row - (m.maxHeight()-1)/2)

	// Wrapped content fits the width, so the range sits in the first
	// screen. An offset left over from reading a wide annotation row could
	// hide it.
	if m.wrapEnabled {
		m.SetXOffset(0)

		return true
	}

	// With wrap off every line is one row that starts at the gutter, so
	// the cells of the range are the gutter plus the cells the layout
	// gives each of its columns. The layout counts what a style's
	// transform adds before them, as the rendered row shows it. A range
	// that ends on a later line runs to the end of this one.
	end := rng.End
	if end.Line != i {
		end = position.New(i, view.Line(i).Width())
	}

	x := layout.GutterWidth() + layout.CellOf(rng.Start)
	xEnd := layout.GutterWidth() + layout.CellOf(end)

	// A range that fits the first screen shows without scrolling, so the
	// view keeps the gutter and the start of the line in sight.
	if xEnd <= m.scrollWidth() {
		m.SetXOffset(0)

		return true
	}

	// Any other range takes the offset that centers its first cell, the
	// way the Y offset centers its row.
	m.SetXOffset(x - (m.scrollWidth()-1)/2)

	return true
}

// Update processes Bubble Tea messages and returns the updated model.
//
//nolint:gocritic // hugeParam: value receivers match the Bubble Tea update loop.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.KeyMap.PageDown):
			m.PageDown()

		case key.Matches(msg, m.KeyMap.PageUp):
			m.PageUp()

		case key.Matches(msg, m.KeyMap.HalfPageDown):
			m.HalfPageDown()

		case key.Matches(msg, m.KeyMap.HalfPageUp):
			m.HalfPageUp()

		case key.Matches(msg, m.KeyMap.Down):
			m.ScrollDown(1)

		case key.Matches(msg, m.KeyMap.Up):
			m.ScrollUp(1)

		case key.Matches(msg, m.KeyMap.Left):
			m.ScrollLeft(m.horizontalStep)

		case key.Matches(msg, m.KeyMap.Right):
			m.ScrollRight(m.horizontalStep)

		case key.Matches(msg, m.KeyMap.NextRevision):
			m.NextRevision()

		case key.Matches(msg, m.KeyMap.PreviousRevision):
			m.PreviousRevision()

		case key.Matches(msg, m.KeyMap.ToggleDiffMode):
			m.ToggleDiffMode()

		case key.Matches(msg, m.KeyMap.ToggleViewMode):
			m.ToggleViewMode()

		case key.Matches(msg, m.KeyMap.ToggleWordWrap):
			m.ToggleWordWrap()
		}

	case tea.MouseWheelMsg:
		if !m.MouseWheelEnabled {
			break
		}

		// Shift turns the vertical wheel into horizontal scrolling. A
		// horizontal wheel scrolls sideways with or without Shift, since
		// some platforms send Shift with it.
		shift := msg.Mod.Contains(tea.ModShift)

		switch msg.Button {
		case tea.MouseWheelDown:
			if shift {
				m.ScrollRight(m.horizontalStep)
			} else {
				m.ScrollDown(m.MouseWheelDelta)
			}

		case tea.MouseWheelUp:
			if shift {
				m.ScrollLeft(m.horizontalStep)
			} else {
				m.ScrollUp(m.MouseWheelDelta)
			}

		case tea.MouseWheelLeft:
			m.ScrollLeft(m.horizontalStep)
		case tea.MouseWheelRight:
			m.ScrollRight(m.horizontalStep)

		default:
			// A MouseWheelMsg carries only wheel buttons.
		}
	}

	return m, nil
}

// getViewDimensions returns (width, height, ok). A false ok means that the
// content area does not have room for rows, and View renders "". The area
// lacks room in these cases:
//   - A dimension is 0 or less.
//   - The frame of the container style takes all of a dimension.
//   - The separator of the side-by-side view leaves its panes without a
//     column.
func (m *Model) getViewDimensions() (int, int, bool) {
	if !m.canRender() {
		return 0, 0, false
	}

	return m.maxWidth(), m.maxHeight(), true
}

// renderContent applies styling and renders lines into final output. It cuts
// each row to contentW and pads it to contentW, so the style fills only the
// rows below them. On a viewport of only a few columns, the printer's gutter
// can force a row past contentW. A style with a width word-wraps every row
// on each render. It also wraps a row wider than contentW onto a second
// screen row that the scroll math does not count. The container style
// therefore renders without its width.
func (m *Model) renderContent(lines []string, contentW, contentH int) string {
	textStyle := m.printer.Style(kind.Text)

	// The style pads the rows it adds to the width of the widest row, so an
	// empty window renders one empty row to carry the width.
	if len(lines) == 0 {
		lines = []string{""}
	}

	for i, row := range lines {
		lines[i] = m.printer.Pad(printer.Cut(row, 0, contentW), contentW)
	}

	contents := textStyle.
		Height(contentH).
		MaxHeight(contentH).
		Render(strings.Join(lines, "\n"))

	return m.style.
		UnsetWidth().UnsetHeight().
		Render(contents)
}

// View renders the viewport. It renders "" for a zero Model, which [New]
// did not create.
//
// The rendering behavior depends on the current [ViewMode]:
//   - [ViewModeFull]: Renders all lines (default behavior).
//   - [ViewModeHunks]: Renders only changed lines, with [Model.HunkContext]
//     lines of context around each change.
//   - [ViewModeSideBySide]: Renders before and after content in separate panes.
//
//nolint:gocritic // hugeParam: value receivers match the Bubble Tea update loop.
func (m Model) View() string {
	if m.printer == nil {
		return ""
	}

	w, h, ok := m.getViewDimensions()
	if !ok {
		return ""
	}

	if m.viewMode == ViewModeSideBySide {
		return m.renderSideBySide(w, h)
	}

	return m.renderContent(m.visibleRows(), w, h)
}

// sideBySideSeparator is the column divider between panes.
const sideBySideSeparator = " │ "

// renderSideBySide renders the side-by-side view with two panes.
func (m *Model) renderSideBySide(contentW, contentH int) string {
	// A model with no content renders an empty viewport.
	if !m.hasContent() {
		return m.renderContent(nil, contentW, contentH)
	}

	paneWidth := m.paneWidth()

	first, last := m.rowWindow()
	if first >= last {
		return m.renderContent(nil, contentW, contentH)
	}

	// Render the lines of the window in both panes.
	window := m.window(first, last)
	p := m.renderPrinter(paneWidth)

	leftRows := m.trimFrame(splitLines(p.Print(m.highlightedWithin(false, window))), first, last)

	// Without a diff, both panes show the left content, so the right pane
	// reuses the rows the left pane rendered.
	rightRows := leftRows
	if m.baseRight != nil {
		rightRows = m.trimFrame(splitLines(p.Print(m.highlightedWithin(true, window))), first, last)
	}

	blank := m.blankPaneRow(p)

	// Get text style for padding empty areas.
	textStyle := m.printer.Style(kind.Text)

	// Build separator with any extra padding from odd width.
	separatorWidth := ansi.StringWidth(sideBySideSeparator)
	extraPadding := (contentW - separatorWidth) % 2
	separator := textStyle.Render(sideBySideSeparator + strings.Repeat(" ", extraPadding))

	combined := make([]string, 0, m.rows.sums[last]-m.rows.sums[first])

	var li, ri int

	// Join the next count rows of the panes. Take at most leftCount rows
	// from the left pane and rightCount from the right, and scroll their
	// content columns to offset.
	appendRows := func(count, leftCount, rightCount, offset int) {
		for i := range count {
			// A pane out of rows shows the blank row, which carries the
			// container's frame. Horizontal scrolling cuts the content
			// columns of a rendered row. The blank row has none, and its
			// frame already sits where it belongs.
			left, right := blank, blank

			if i < leftCount && li < len(leftRows) {
				left = m.cutRow(leftRows[li], offset, paneWidth)
				li++
			}

			if i < rightCount && ri < len(rightRows) {
				right = m.cutRow(rightRows[ri], offset, paneWidth)
				ri++
			}

			// Pad the left pane to a consistent width for alignment. The
			// view cuts both panes to the pane width, which the gutter of
			// a pane row can exceed on its own, so the joined row fits the
			// content width.
			leftPadded := m.printer.Pad(printer.Cut(left, 0, paneWidth), paneWidth)

			combined = append(combined, leftPadded+separator+printer.Cut(right, 0, paneWidth))
		}
	}

	if first == 0 {
		appendRows(m.rows.top, m.rows.top, m.rows.top, 0)
	}

	// Zip the panes line by line. A line that wraps taller in one pane gets
	// blank rows in the other, so the panes stay aligned.
	for k := first; k < last; k++ {
		leftCount := m.rows.left[k]

		rightCount := leftCount
		if m.rows.right != nil {
			rightCount = m.rows.right[k]
		}

		appendRows(m.lineRows(k), leftCount, rightCount, m.xOffset)
	}

	if last == len(m.rows.left) {
		appendRows(m.rows.bottom, m.rows.bottom, m.rows.bottom, 0)
	}

	combined = m.trimWindow(combined, first)

	return m.renderContent(combined, contentW, contentH)
}

// blankPaneRow renders the row that a side-by-side pane shows below its own
// rows when the line wraps taller in the other pane. It is one empty content
// row with the frame of p's container style around it, so the pane keeps
// its border down the whole window. Print pads the row to p's container
// width, and the side-by-side view pins that width to the pane width. A
// container without a horizontal frame therefore gives a row of
// [kind.Text] spaces as wide as the pane.
func (m *Model) blankPaneRow(p *printer.Printer) string {
	// The row holds no content, so an empty view prints it without copying
	// the decoration of a view as long as the document.
	rows := splitLines(p.Print(&line.View{}))

	// Print renders an empty view as one content row below the top frame,
	// so this guard is defensive.
	if m.rows.top >= len(rows) {
		return ""
	}

	return rows[m.rows.top]
}

func clamp[T cmp.Ordered](v, low, high T) T {
	return min(high, max(low, v))
}

// isNil reports whether v is a nil interface or holds a nil pointer or nil
// func. An interface that holds either compares unequal to nil, and a call
// through it panics.
//
// The packages of niceyaml make the same check on their own options. The
// viewport builds on the public API of niceyaml alone, so it keeps its own
// copy.
func isNil(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)

	switch rv.Kind() {
	case reflect.Pointer, reflect.Func:
		return rv.IsNil()
	default:
		return false
	}
}

// splitLines splits content by newlines. Unlike [strings.Split], it does not
// produce an empty trailing element when the content ends with a newline.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}

	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}
