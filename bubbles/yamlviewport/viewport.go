package yamlviewport

import (
	"cmp"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

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
)

const defaultHorizontalStep = 6

// Searcher builds an [Index] over the lines on display. The viewport loads
// the lines once per change of content and runs every search term through
// the Index it gets back. A nil Index finds no match for any term.
//
// [WithFinder] adapts a [finder.Finder] to this interface.
type Searcher interface {
	Load(lines line.Lines) Index
}

// Index finds each [position.Range] that matches a search string in the lines
// it was built from. Find may return the ranges in any order, because the
// viewport sorts them into document order.
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
func (s finderSearcher) Load(lines line.Lines) Index {
	return s.finder.Load(lines)
}

// Revision is one version of a document in the viewport's history: a name
// for the revision picker and a view of its content. A [*niceyaml.Source]
// is a Revision, so a source goes straight to [Model.AddRevision] or
// [Model.SetRevision]. A view that carries decoration, such as the marks
// [niceyaml.SourceError.Annotate] adds, goes in through [NewRevision]:
//
//	view := source.View()
//	for bound := range niceyaml.AllBindings(err) {
//		bound.Annotate(view)
//	}
//
//	m.SetRevision(yamlviewport.NewRevision(source.Name(), view))
//
// The viewport reads the view of a revision it shows without a diff when
// the revision changes, or when [Model.SetDiffMode] turns the diff off, and
// never decorates it. It computes the diff between two revisions from their
// views and keeps it until it compares another pair or the history changes,
// so a change of view mode or hunk context reuses the diff without reading
// the views again. Search highlights go on a clone, so the marks a caller
// adds stay, and the caller's view stays as the caller left it. Marks added
// after the viewport read the view show once the revision is set again. A
// diff between two revisions interleaves their lines in a view of its own,
// so decoration shows only while the viewport displays a revision without a
// diff.
//
// The viewport renders the lines the view holds in the order
// [line.View.All] yields them, which is content order, and windows them
// by index. A search covers the content of the view, and the viewport
// keeps the matches on lines the view holds.
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
	// preceding revision.
	// This is the default mode.
	DiffModeAdjacent DiffMode = iota
	// DiffModeOrigin compares the current revision with the first (origin)
	// revision, so the diff shows cumulative changes.
	DiffModeOrigin
	// DiffModeNone displays the current revision without any diff markers.
	DiffModeNone
)

// ViewMode specifies how the viewport renders diff content.
//
// Use [Model.SetViewMode] to change the mode, or [Model.ToggleViewMode] to
// cycle through modes.
type ViewMode int

const (
	// ViewModeFull displays all lines including unchanged content.
	// This is the default mode.
	ViewModeFull ViewMode = iota
	// ViewModeHunks displays only changed lines with surrounding context.
	ViewModeHunks
	// ViewModeSideBySide displays before and after content in separate panes.
	ViewModeSideBySide
)

// Option configures a [Model].
//
// Available options:
//   - [WithPrinter]
//   - [WithContainerStyle]
//   - [WithSearcher]
//   - [WithFinder]
type Option func(*Model)

// WithPrinter is an [Option] that sets the [*printer.Printer] used for
// rendering. Without it, the viewport creates a default [printer.Printer].
// A nil p selects that same default.
//
// The viewport never modifies the printer. Each render derives a copy with
// [printer.Printer.With] and the viewport's wrap width, so other renderers
// can share the same printer. The viewport sizes that copy's container to
// the content area, so it ignores any Width, Height, MaxWidth, or MaxHeight
// on the printer's container style. To fix the size of the viewport, set
// them on [WithContainerStyle] instead.
func WithPrinter(p *printer.Printer) Option {
	return func(m *Model) {
		m.printer = p
	}
}

// WithContainerStyle is an [Option] that sets the [lipgloss.Style] wrapped
// around the viewport, as [printer.WithContainerStyle] does for a printer.
// See [Model.SetContainerStyle].
//
//nolint:gocritic // hugeParam: Copying.
func WithContainerStyle(s lipgloss.Style) Option {
	return func(m *Model) {
		m.style = s
	}
}

// WithSearcher is an [Option] that sets the [Searcher] that search terms run
// through. Without it, and without [WithFinder], the viewport creates a
// [finder.Finder] with [finder.New], which folds case and ignores
// diacritics. A nil s selects that same default.
func WithSearcher(s Searcher) Option {
	return func(m *Model) {
		m.searcher = s
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
		opt(&m)
	}

	m.setInitialValues()

	return m
}

// Model is the Bubble Tea model for the YAML viewport.
// Create instances with [New].
//
// A zero Model has no printer, keymap, or searcher. It counts no rows, so
// [Model.View] returns "" and both scroll offsets stay at 0. Searching needs
// the searcher that [New] creates, so a zero Model finds no match for any
// term. Construct every Model with [New] and any [Option] values.
//
// # Rows
//
// The viewport scrolls by rendered row, not by source line. A line that
// wraps to the viewport width or carries an annotation takes several rows,
// and the vertical offset ([Model.YOffset], [Model.SetYOffset], and the
// scroll methods) counts those rows, so every row of the view is reachable.
// The frame of the printer's container style ([printer.WithContainerStyle])
// adds rows above the first line and below the last. Methods named after rows
// ([Model.TotalRowCount], [Model.VisibleRowCount]) count rows; methods named
// after lines ([Model.TotalLineCount], [Model.VisibleLineCount]) count the
// lines of the view.
//
// A layout change, such as a new width, style, printer, or wrap setting,
// keeps the same line at the top of the view. The top row stays at the same
// row of that line, or at its last row if the line no longer has that many.
// A top row in the container frame stays at the same row of that frame in
// the same way, and a view at the top stays at the top, so a frame that
// appears or grows shows its outer row. A change of view mode without a diff
// is a layout change too, since every mode shows the same lines.
//
// A change of content scrolls to the top, or to the first search match when
// the term has one. A new revision changes the content, as does a diff mode
// that changes the diff on display or a view mode while a diff shows.
type Model struct {
	// The container style applied to the viewport frame.
	style    lipgloss.Style
	printer  *printer.Printer
	searcher Searcher
	// Index over the lines on display, built when they change. In
	// side-by-side mode index covers the left pane and indexRight the
	// right one.
	index      Index
	indexRight Index
	// Revision history; revIndex below selects the revision on display.
	revisions []Revision
	// Cached diff between the base and current revisions at the indexes in
	// diffKey below.
	diffResult *diff.Result
	// The content on display before search highlights, for the left pane or
	// main content and for the right pane of a side-by-side diff.
	// In ViewModeFull: the unified diff, or the view of the revision.
	// In ViewModeHunks with diff: the hunks of the diff with their headers.
	// In ViewModeSideBySide with diff: the Before and After views.
	// In ViewModeSideBySide without diff: the view of the revision on the
	// left and nil on the right.
	//
	// A base is the model's own copy of the view the Revision hands out, or
	// the view of the diff, and it never carries search highlights. A
	// layout or a render slices the lines it needs from a base and adds the
	// highlights of those lines to the slice, so each term or selection
	// starts without the highlights of the last.
	baseLeft, baseRight *line.View
	// The indexes in leftMatches and rightMatches of the matches that cover
	// each line, keyed by the index of the line in the content, in
	// ascending order. A slice of a pane finds the highlights of its lines
	// here.
	leftLines, rightLines map[int][]int
	// Rendered row counts of the view. Copies of the Model share one cache
	// until a layout change gives a copy its own, so the counts that the
	// value-receiver View fills in stay filled for the Model it copied.
	rows *rowCache
	// Current search query.
	searchTerm string
	// KeyMap contains the keybindings for viewport navigation.
	KeyMap        KeyMap
	searchMatches []searchMatch
	// The matches in each pane, or in the content of the unified view on
	// the left, in the order the search found them.
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
	// view or the container frame above the first line, and a line one past
	// the last stands for the frame below the last line. The row then counts
	// from the first row of that frame.
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

func (m *Model) setInitialValues() {
	m.KeyMap = DefaultKeyMap()
	m.MouseWheelEnabled = true
	m.MouseWheelDelta = 3
	m.horizontalStep = defaultHorizontalStep
	m.hunkContext = 3 // Default context lines around diff hunks.
	m.wrapEnabled = true
	m.searchIndex = -1

	if m.printer == nil {
		m.printer = printer.New()
	}

	if m.searcher == nil {
		m.searcher = finderSearcher{finder: finder.New()}
	}

	m.relayout()
}

// Init returns the command that starts the viewport, which is nil because
// the viewport requires no initialization commands. It follows the shape of
// [tea.Model.Init], so a parent model can call it from its own Init.
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
	// Row counts do not depend on the height, so the cache stays, and
	// ensureRows clamps the offsets on their next read.
	m.height = h
}

// Width returns the width of the viewport.
func (m *Model) Width() int {
	return m.width
}

// SetWidth sets the width of the viewport and clamps the scroll offsets to
// the new bounds. A width that leaves the content width as it is, such as
// the width already set or one the container style caps, leaves the view
// where it is.
func (m *Model) SetWidth(w int) {
	old := m.paneWidth()

	m.width = w

	// Row counts depend on the width only through the pane width.
	if m.paneWidth() != old {
		m.relayout()
	}
}

// relayout responds to a layout change, such as a new printer, style, wrap
// setting, or width, without rebuilding the view. It gives the Model an empty
// row count cache and leaves any copy that shares the old cache untouched.
// The next read of the scroll bounds fills the new cache.
//
// Row offsets from the old cache point at other lines once the rows reflow,
// so relayout calls anchorTop to record the top line as the old layout shows
// it, and ensureRows scrolls back to that line.
func (m *Model) relayout() {
	m.anchorTop()

	m.rows = &rowCache{}
}

// remeasure responds to a change of decoration confined to the lines that
// ranges cover, such as a move of the selected search match. It lays out
// those lines again, since a highlight style may change the width of the
// text it styles, and gives the Model a new cache that carries over the
// counts of every other line. Like relayout, it leaves any copy that
// shares the old cache untouched and records the top line for ensureRows
// to restore. An empty cache has no counts to carry over, so remeasure
// drops it as relayout does. Without ranges no line changed, so the cache
// stays as it is.
func (m *Model) remeasure(ranges ...position.Range) {
	if m.rows == nil || m.rows.sums == nil {
		m.relayout()

		return
	}

	// A slice of a view by no span holds every line, so the slices below
	// would measure the whole view.
	if len(ranges) == 0 {
		return
	}

	m.anchorTop()

	spans := make([]position.Span, 0, len(ranges))
	for _, r := range ranges {
		spans = append(spans, position.NewSpan(r.Start.Line, r.End.Line+1))
	}

	p := m.renderPrinter(m.paneWidth())
	c := m.rows.clone()

	left := m.highlighted(false, spans...)
	c.measure(p.Layout(left), left, c.left, c.leftWidths)

	if c.right != nil && m.baseRight != nil {
		right := m.highlighted(true, spans...)
		c.measure(p.Layout(right), right, c.right, c.rightWidths)
	}

	c.sum()

	m.rows = c
}

// anchorTop records the line at the top of the view and the row within it,
// with the vertical offset clamped to the bounds of the current layout, for
// ensureRows to restore after the rows reflow. An anchor that ensureRows has
// not restored yet stays, so several changes in a row keep the original top
// line.
//
// No line owns a frame row, so a top row in the container frame anchors to
// the frame instead. The first row of the view anchors to the top frame even
// when the layout has none, so a view at the top stays there when a frame
// appears.
func (m *Model) anchorTop() {
	if m.anchored || m.rows == nil || len(m.rows.left) == 0 {
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
// content width less the horizontal frame of the printer's container style,
// so a wrapped line and the frame around it together fit the content area.
//
// Pinning the printer's container to the given width makes the box it draws
// cover the same columns for every window. Without the pin it would shrink
// to the widest row of the window, and a window of short lines would carry
// its border in from the edge of the content area.
//
// The viewport sizes the printer's container itself, so renderPrinter drops
// any Width, Height, MaxWidth, or MaxHeight from its style. Those would make
// Print wrap, pad, or cut the rows after the layout has counted them, and the
// scroll bounds would no longer match the rows on screen.
//
// A render prints a slice of the view, so renderPrinter sizes the gutter for
// the largest line number of the whole view rather than of the window, and
// every window lines up with the layout. In side-by-side mode both panes
// get the gutter of the longer revision, so the one horizontal offset lands
// on the same content column in both. The row cache holds that number,
// which fillRows computes whenever the lines, the printer, or the panes
// change, so the caller fills the cache first.
func (m *Model) renderPrinter(width int) *printer.Printer {
	wrapWidth := 0
	if m.wrapEnabled {
		wrapWidth = max(0, width-m.printer.ContainerStyle().GetHorizontalFrameSize())
	}

	container := m.printer.ContainerStyle().
		UnsetWidth().
		UnsetHeight().
		UnsetMaxWidth().
		UnsetMaxHeight()

	return m.printer.With(
		printer.WithWrap(wrapWidth),
		printer.WithMaxNumber(m.rows.maxNumber),
		printer.WithContainerWidth(width),
		printer.WithContainerStyle(container),
	)
}

// SetPrinter sets the [*printer.Printer] used for rendering. See
// [WithPrinter]. A nil p selects the default printer, as a nil
// [WithPrinter] does.
//
// The view, its diff, and its search matches stay as they are, so switching
// themes costs one render and no diff or search index rebuild.
func (m *Model) SetPrinter(p *printer.Printer) {
	if p == nil {
		p = printer.New()
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
// This is a convenience method equivalent to [Model.ClearRevisions] followed by
// [Model.AddRevision].
func (m *Model) SetRevision(r Revision) {
	m.ClearRevisions()
	m.AddRevision(r)
}

// AddRevision adds a new revision to the history.
// After adding, the viewport moves to the newly added revision.
//
// The viewport displays the view r hands out, decoration included, and
// adds its search highlights to a clone of it, so the view itself stays as
// the caller left it. See [Revision]. A nil r, or an r that holds a nil
// pointer such as a nil [*niceyaml.Source], adds nothing.
func (m *Model) AddRevision(r Revision) {
	m.AddRevisions(r)
}

// AddRevisions adds revisions to the history in order and moves to the last
// one. It matches a call to [Model.AddRevision] for each revision, except
// that the viewport builds only the view and diff of the last one, so
// loading a history costs one diff rather than one per revision. A nil
// revision, or one that holds a nil pointer, adds nothing.
func (m *Model) AddRevisions(rs ...Revision) {
	// Copies of a Model share the backing array of revisions, so the appends
	// must not write into spare capacity that another copy can reach.
	revisions := slices.Clip(m.revisions)
	for _, r := range rs {
		if !isNilRevision(r) {
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

// isNilRevision reports whether r is nil or holds a nil pointer. A
// [Revision] implemented on a pointer type, such as [*niceyaml.Source],
// reads its fields in Name and View, so a nil one panics there.
func isNilRevision(r Revision) bool {
	if r == nil {
		return true
	}

	v := reflect.ValueOf(r)

	return v.Kind() == reflect.Pointer && v.IsNil()
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

// RevisionIndex returns the current revision index.
// Returns 0 without revisions.
func (m *Model) RevisionIndex() int {
	return m.revIndex
}

// RevisionName returns the name of the current revision.
// Returns an empty string without revisions.
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
// Index 0 always shows the first revision without diff markers.
// Index 1 to N-1 shows a diff based on the current [DiffMode].
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
// This is true when not at the first revision and [DiffMode] is not
// [DiffModeNone].
func (m *Model) ShowingDiff() bool {
	return m.hasRevision() && m.revIndex > 0 && m.diffMode != DiffModeNone
}

// DiffStats returns the number of added and removed lines in the current
// diff, as [diff.Result.Stats] counts them.
//
// Returns the zero [diff.Stats] when the viewport shows no diff (at first
// revision, diff mode is none, or no revisions exist).
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
// already set or when the viewport shows no diff before or after the change.
// It also stays when both modes compare against the same revision, as
// [DiffModeAdjacent] and [DiffModeOrigin] do at revision index 1.
// An undefined mode falls back to [DiffModeAdjacent], the default.
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
// [DiffModeOrigin], [DiffModeNone]. The view rebuilds as for
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
// change counts as a layout change and keeps the top line. The mode already
// set leaves the view where it is. An undefined mode falls back to
// [ViewModeFull], the default.
func (m *Model) SetViewMode(mode ViewMode) {
	if mode < ViewModeFull || mode > ViewModeSideBySide {
		mode = ViewModeFull
	}

	if mode == m.viewMode {
		return
	}

	if !m.ShowingDiff() {
		// Side-by-side mode halves the width lines wrap to, so the rows
		// reflow as they do for a new width.
		m.relayout()

		m.viewMode = mode

		return
	}

	m.viewMode = mode
	m.rebuildViews()
}

// ToggleViewMode cycles through the view modes in the order [ViewModeFull],
// [ViewModeHunks], [ViewModeSideBySide]. The view rebuilds as for
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
// otherwise stays where it is. Default is 3.
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
// it. A style that leaves the content width as it is, such as one that
// changes only a border color, leaves the view where it is.
//
//nolint:gocritic // hugeParam: Copying.
func (m *Model) SetContainerStyle(s lipgloss.Style) {
	prev := m.style
	old := m.paneWidth()

	m.style = s

	// Row counts depend on the style only through the pane width, so an
	// unchanged pane width keeps the cache, as SetHeight does.
	if m.paneWidth() == old {
		return
	}

	// The style sets the content height that bounds the offset, so anchorTop
	// reads the top line under the old style.
	m.style = prev
	m.anchorTop()

	m.style = s
	m.relayout()
}

// NextRevision moves to the next revision in history. At the latest
// revision it does nothing.
func (m *Model) NextRevision() { m.GotoRevision(m.revIndex + 1) }

// PreviousRevision moves to the previous revision in history. At the first
// revision, index 0, it does nothing.
func (m *Model) PreviousRevision() { m.GotoRevision(m.revIndex - 1) }

// rebuildViews rebuilds the displayed views from the revision, diff mode, and
// view mode, then refreshes the search state and drops the cached row counts.
// Rendering itself waits for View, which renders only the visible window.
//
// Every change of content goes through rebuildViews. A match index or row
// offset carried over from the old content points at an arbitrary line, so
// the view scrolls to the top, or to the first match in the new content when
// the search term has one.
func (m *Model) rebuildViews() {
	m.baseLeft = nil
	m.baseRight = nil
	m.searcherStale = true
	m.searchIndex = -1

	needsDiff := m.ShowingDiff()

	switch {
	case m.viewMode == ViewModeSideBySide && needsDiff:
		result := m.getDiffResult()
		m.baseLeft = result.Before()
		m.baseRight = result.After()

	case m.viewMode == ViewModeHunks && needsDiff:
		// Hunks holds no lines when the diff has no changes, which leaves
		// the view empty.
		m.baseLeft = m.getDiffResult().Hunks(m.hunkContext)

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

	m.scrollToCurrentMatch()
}

// refreshSearch recomputes the search matches of the current base views and
// the lines each match covers, without rebuilding the views. A zero Model,
// one not created with [New], has no searcher, so it finds no match for any
// term.
//
// The caller brings the row counts up to date with the new highlights. They
// change only on the lines of the old and the new matches, so a caller that
// changes the term measures only those lines again.
func (m *Model) refreshSearch() {
	switch {
	case m.baseLeft == nil, m.searcher == nil, m.searchTerm == "":
		m.clearMatches()

	case m.viewMode == ViewModeSideBySide && m.baseRight != nil:
		m.updateSideBySideSearchState()

	default:
		m.updateSearchState(m.baseLeft)
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

	m.leftLines = matchLines(m.leftMatches, m.baseLeft.Lines().Len())
	m.rightLines = matchLines(m.rightMatches, m.baseRight.Lines().Len())
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
// line of content n lines long, keyed by the index of the line, in
// ascending order. A match that runs across a line break covers each line
// it touches.
func matchLines(matches position.Ranges, n int) map[int][]int {
	lines := make(map[int][]int, len(matches))

	for k, r := range matches {
		for l := max(0, r.Start.Line); l <= min(r.End.Line, n-1); l++ {
			lines[l] = append(lines[l], k)
		}
	}

	return lines
}

// highlighted returns a slice of the base view of the left pane, or of the
// right one when right is true, with the search highlights of the matches
// that cover its lines. The slice holds the lines within spans, or every
// line when spans is empty. The base view keeps no highlight, so a move of
// the selection costs the lines a caller lays out or renders rather than
// every match of the document.
//
// It blends the matches in the order of the match list, so each line gets
// its overlays in the same order however the caller slices the view.
func (m *Model) highlighted(right bool, spans ...position.Span) *line.View {
	base, matches, lines := m.baseLeft, m.leftMatches, m.leftLines
	if right {
		base, matches, lines = m.baseRight, m.rightMatches, m.rightLines
	}

	view := base.Slice(spans...)

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
// in both panes, since the search counts it once.
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

	// A line is equal when both panes hold it unchanged. The padding a diff
	// puts opposite an inserted or deleted line is empty and carries the
	// default flag too, so one pane alone cannot tell the two apart.
	equal := false
	if l := pos.Line; m.baseLeft.Contains(l) && m.baseRight.Contains(l) {
		equal = m.baseLeft.Flag(l) == line.FlagDefault && m.baseRight.Flag(l) == line.FlagDefault
	}

	show := equal || selected.inLeft != right

	matches := m.leftMatches
	if right {
		matches = m.rightMatches
	}

	return func(k int) bool { return show && matches[k].Start == pos }
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
// It combines the matches from both panes and deduplicates them. Equal lines
// count as a single match, while deleted and inserted lines count as separate
// matches.
func (m *Model) updateSideBySideSearchState() {
	// Load both panes once per change of content, as the unified path
	// does, so typing a term does not rebuild the indexes on every
	// keystroke. A nil Index counts as loaded.
	if m.searcherStale {
		m.index = m.searcher.Load(m.baseLeft.Lines())
		m.indexRight = m.searcher.Load(m.baseRight.Lines())

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
		if m.baseLeft.Contains(match.Start.Line) && m.baseLeft.Flag(match.Start.Line) == line.FlagDefault {
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
		if a.rng.Start.Line != b.rng.Start.Line {
			return cmp.Compare(a.rng.Start.Line, b.rng.Start.Line)
		}

		if a.rng.Start.Col != b.rng.Start.Col {
			return cmp.Compare(a.rng.Start.Col, b.rng.Start.Col)
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
// given lines. It needs a searcher, and it leaves the match index to
// refreshSearch.
//
// It reloads the searcher only when the lines changed since the last load, so
// typing a search term does not rebuild the index on every keystroke. A nil
// Index counts as loaded.
func (m *Model) updateSearchState(lines *line.View) {
	if m.searcherStale {
		m.index = m.searcher.Load(lines.Lines())

		m.searcherStale = false
	}

	// The unified view shows every match on the left, and its matches do
	// not use inLeft. An Index may return matches in any order, so sort
	// them into document order as the side-by-side path does.
	m.leftMatches = heldMatches(lines, find(m.index, m.searchTerm))
	slices.SortStableFunc(m.leftMatches, func(a, b position.Range) int {
		if a.Start.Line != b.Start.Line {
			return cmp.Compare(a.Start.Line, b.Start.Line)
		}

		return cmp.Compare(a.Start.Col, b.Start.Col)
	})

	m.rightMatches = nil
	m.searchMatches = make([]searchMatch, 0, len(m.leftMatches))

	for _, rng := range m.leftMatches {
		m.searchMatches = append(m.searchMatches, searchMatch{rng: rng})
	}
}

// find returns the matches of search in idx. A nil idx, which a [Searcher]
// may return, finds nothing.
func find(idx Index, search string) position.Ranges {
	if idx == nil {
		return nil
	}

	return idx.Find(search)
}

// heldMatches returns the matches that start on a line view holds. A
// search covers the whole content of the view, and a view over part of a
// document, such as one from [niceyaml.Node.View], holds some of it.
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
// viewport shows no diff.
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
// revision and [DiffMode]: a fresh unified diff, or a copy of the view the
// revision hands out. The copy holds the marks the view carried when the
// content changed, so marks the caller adds later stay off the display.
// Returns nil when there is no revision.
func (m *Model) getDisplayLines() *line.View {
	if m.ShowingDiff() {
		return m.getDiffResult().Unified()
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
// Callers must check [Model.ShowingDiff] first. Without a diff there is no
// base revision.
func (m *Model) getDiffResult() *diff.Result {
	base := m.diffBaseIndex()
	pair := [2]int{base, m.revIndex}
	if m.diffResult == nil || m.diffKey != pair {
		// A diff compares the lines each view holds, as the other view
		// modes render them, rather than every line of the content the
		// view is over.
		m.diffResult = diff.Diff(m.revision(base).View().Held(), m.currentRevision().View().Held())
		m.diffKey = pair
	}

	return m.diffResult
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
	// Prefix sums of the taller pane after the top frame, so sums[k] is the
	// first row of the k-th rendered line and the last entry is the first row
	// of the bottom frame. Nil until filled.
	sums []int
	// Width in cells of the widest row of either pane.
	width int
	// Largest line number of either pane, which sizes the gutter.
	maxNumber int
	// Rows of the printer's container frame above the first line and below
	// the last. A view without lines has no frame rows.
	top, bottom int
}

// total returns the number of rows in a filled cache.
func (c *rowCache) total() int {
	return c.sums[len(c.sums)-1] + c.bottom
}

// clone returns a copy of the cache with row counts and widths of its own,
// so a change to the copy leaves every Model that shares c as it was.
func (c *rowCache) clone() *rowCache {
	out := *c
	out.left = slices.Clone(c.left)
	out.right = slices.Clone(c.right)
	out.leftWidths = slices.Clone(c.leftWidths)
	out.rightWidths = slices.Clone(c.rightWidths)

	return &out
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
// the layout of view, into rows and widths, which hold an entry for each
// rendered line of the cache.
func (c *rowCache) measure(layout printer.Layout, view *line.View, rows, widths []int) {
	for i := range view.All() {
		if k, ok := slices.BinarySearch(c.indices, i); ok {
			rows[k] = layout.LineRows(i)
			widths[k] = layout.LineWidth(i)
		}
	}
}

// ensureRows fills the row count cache when it is empty, scrolls back to the
// row that relayout recorded, and clamps both scroll offsets to the cache's
// bounds. It checks on every call, because a copy of the Model can fill a
// shared cache without restoring or clamping this Model's offsets.
func (m *Model) ensureRows() {
	if m.rows == nil {
		m.rows = &rowCache{}
	}

	if m.rows.sums == nil {
		m.fillRows()
	}

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
}

// fillRows computes the row counts of the view into the cache. A Model
// without a printer, such as a zero Model, has no rows.
func (m *Model) fillRows() {
	c := m.rows
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

		left := m.highlighted(false)
		c.indices, c.left, c.leftWidths = layoutRows(p.Layout(left), left)

		if m.viewMode == ViewModeSideBySide && m.baseRight != nil {
			right := m.highlighted(true)
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
// visible window, which starts at the vertical offset and is one content
// height tall.
//
// The range holds at least one line whenever the view has lines. No line owns
// a frame row, so a window that lies entirely inside the top or the bottom
// frame still needs a line to render the frame around.
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
// lines [first, last) from the view, which holds its lines in ascending
// order, so a slice of the view by the span renders those lines alone.
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

// trimFrame drops the container frame rows that Print adds around the lines
// [first, last) in rows, except the top frame above the first line of the
// view and the bottom frame below its last line.
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

// ScrollPercent returns the vertical scroll position as a float between 0 and 1.
func (m *Model) ScrollPercent() float64 {
	if m.baseLeft == nil {
		return 1.0
	}

	return scrollPercent(m.YOffset(), m.maxHeight(), m.TotalRowCount())
}

// HorizontalScrollPercent returns the horizontal scroll position as a float
// between 0 and 1. It is 1 when every row fits the content width, which
// wrapped rows do unless an annotation column past the end of its line lies
// past the wrap width or a style transform widens a row.
func (m *Model) HorizontalScrollPercent() float64 {
	if m.baseLeft == nil || m.printer == nil {
		return 1.0
	}

	return scrollPercent(m.XOffset(), m.scrollWidth(), m.rowWidth())
}

// scrollPercent calculates scroll position as a value between 0 and 1.
func scrollPercent(offset, visible, total int) float64 {
	if visible >= total {
		return 1.0
	}

	v := float64(offset) / float64(total-visible)

	return clamp(v, 0, 1)
}

// maxYOffset returns the maximum Y offset, in rows.
func (m *Model) maxYOffset() int {
	m.ensureRows()

	return m.rowOffsetLimit()
}

// rowOffsetLimit returns the last row the view can start at, from a filled
// cache. That is the row that brings the last row of the view to the bottom
// of the content area. When the content area is shorter than the view, it is
// the last row of the view instead. A content area with no height, which a
// height of 0 or a container frame as tall as the height gives, would
// otherwise put the offset one row past the end of the view.
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
// width, which wrapped rows do unless an annotation column past the end of
// its line lies past the wrap width or a style transform widens a row.
func (m *Model) maxXOffset() int {
	if m.baseLeft == nil || m.printer == nil {
		return 0
	}

	return max(0, m.rowWidth()-m.scrollWidth())
}

// rowWidth returns the width in cells of the widest row the printer renders
// for the view before the container frame applies, over both panes in
// side-by-side mode. It reads the line widths the layouts report, so
// annotation rows and wide characters count toward the horizontal scroll
// bound.
func (m *Model) rowWidth() int {
	// Fill the cache without ensureRows, which clamps the horizontal offset
	// through this method.
	if m.rows == nil {
		m.rows = &rowCache{}
	}

	if m.rows.sums == nil {
		m.fillRows()
	}

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

// scrollWidth returns the number of row columns a pane shows at once: the
// pane width less the horizontal frame of the printer's container, which
// cutRow keeps in place.
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

// hasContent reports whether there is content to display.
func (m *Model) hasContent() bool {
	return m.lineCount() > 0
}

// hasRevision reports whether a revision exists.
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
	rows := splitLines(p.Print(m.highlighted(false, m.window(first, last))))
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

// rowOffset returns the horizontal offset of the rendered row r: 0 for a row
// of the container frame above the first line or below the last, which holds
// no content, and the horizontal scroll offset for a row of a line.
func (m *Model) rowOffset(r int) int {
	if r < m.rows.top || r >= m.rows.total()-m.rows.bottom {
		return 0
	}

	return m.xOffset
}

// cutRow fits a rendered row to width by cutting its content columns to the
// horizontal window that starts at offset. The columns of the printer's
// container frame on either side stay in place, so a border keeps its edges
// while the content between them scrolls. A wide character cut by the left
// edge of the window shows as blank cells.
func (m *Model) cutRow(row string, offset, width int) string {
	frame := m.printer.ContainerStyle()
	left := frame.GetMarginLeft() + frame.GetBorderLeftSize() + frame.GetPaddingLeft()
	right := frame.GetHorizontalFrameSize() - left

	inner := ansi.StringWidth(row) - left - right
	if inner < 0 {
		return ansi.Cut(row, 0, width)
	}

	visible := max(0, width-left-right)
	body := ansi.Cut(row, left, left+inner)

	// A cut keeps the whole of a wide cluster that begins before offset,
	// so the content would run wider than the window. Truncating at a
	// column inside a cluster comes up short of that column, so start
	// moves to the next cluster boundary, and blank cells fill the columns
	// of the dropped cluster.
	start := offset
	for start < min(inner, offset+visible) && ansi.StringWidth(ansi.Truncate(body, start, "")) != start {
		start++
	}

	content := ansi.Cut(body, start, offset+visible)
	if start > offset {
		content = m.printer.Style(kind.Text).Render(strings.Repeat(" ", start-offset)) + content
	}

	// The side-by-side view pads a pane to its own widest row, while the
	// offset runs to the widest row of either pane, so the window can
	// reach past the content of this row. Filling the window keeps the
	// right frame in its column.
	if padding := visible - ansi.StringWidth(content); padding > 0 {
		content += m.printer.Style(kind.Text).Render(strings.Repeat(" ", padding))
	}

	return ansi.Cut(row, 0, left) + content + ansi.Cut(row, left+inner, left+inner+right)
}

// SetYOffset sets the vertical offset, in rows, clamped to the scrollable
// range.
func (m *Model) SetYOffset(n int) {
	m.yOffset = clamp(n, 0, m.maxYOffset())
}

// YOffset returns the vertical offset, the index of the rendered row at the
// top of the viewport.
func (m *Model) YOffset() int {
	m.ensureRows()

	return m.yOffset
}

// SetXOffset sets the X offset.
func (m *Model) SetXOffset(n int) {
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
// a page rounds down to none.
func (m *Model) HalfPageDown() {
	if h := m.maxHeight(); h > 0 {
		m.ScrollDown(max(1, h/2))
	}
}

// HalfPageUp moves the view up by half a page, and by one row when half a
// page rounds down to none.
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

// TotalLineCount returns the number of lines in the view. In [ViewModeHunks]
// that is the lines inside a hunk, not the whole diff.
func (m *Model) TotalLineCount() int {
	return m.lineCount()
}

// TopLine returns the index of the line that owns the top row of the view.
// It counts lines as [Model.TotalLineCount] does, not rows, so a line that
// wraps to several rows counts once. When the top row belongs to the frame
// of the container style, TopLine returns the nearest line: the first line
// for a row in the top frame, and the last line for a row in the bottom
// frame. It returns 0 when the view has no lines.
func (m *Model) TopLine() int {
	first, _ := m.rowWindow()

	return first
}

// VisibleLineCount returns the number of lines with at least one row on
// screen. It reports at least 1 whenever the view has lines, since a window
// that lies wholly inside the container frame still renders around a line.
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

// SetSearchTerm sets the search term and updates highlights.
// If the term is empty, clears all search highlights.
//
// The term stays set across content changes. A change of content, as the
// [Model] doc defines it, starts the search over at the first match in the
// new content and scrolls to it. A new term likewise starts at its first
// match, while setting the same term again keeps the current match.
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
// With no term set, ClearSearch leaves the view and its layout as they are.
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

// navigateSearch moves the search index by delta, wrapping around.
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

// SearchIndex returns the current search match index (0-based), or -1 if no
// matches.
func (m *Model) SearchIndex() int {
	return m.searchIndex
}

// SearchCount returns the total number of search matches.
func (m *Model) SearchCount() int {
	return len(m.searchMatches)
}

// scrollToCurrentMatch scrolls to center the row that holds the start of the
// current search match in the viewport.
func (m *Model) scrollToCurrentMatch() {
	if m.searchIndex < 0 || m.searchIndex >= len(m.searchMatches) {
		return
	}

	match := m.searchMatches[m.searchIndex]
	m.ensureRows()

	right := m.baseRight != nil && !match.inLeft

	view := m.baseLeft
	if right {
		view = m.baseRight
	}

	i := match.rng.Start.Line

	k, ok := slices.BinarySearch(m.rows.indices, i)
	if !ok || view == nil {
		return
	}

	// The row of the match within its line comes from a layout of the line
	// alone in the pane it is in, and the line's first row from the sums
	// that place the taller of the two panes.
	layout := m.renderPrinter(m.paneWidth()).Layout(m.highlighted(right, position.NewSpan(i, i+1)))

	matchRow := layout.RowOf(match.rng.Start)
	if matchRow < 0 {
		return
	}

	row := m.rows.sums[k] + matchRow

	// (maxHeight-1)/2 puts the match on the visual center row. A height of
	// 22 gives (22-1)/2 = 10, and a height of 21 gives (21-1)/2 = 10, so
	// the match lands on row 10 in both cases.
	m.SetYOffset(row - (m.maxHeight()-1)/2)

	// Wrapped content fits the width, so the match sits in the first
	// screen. An offset left over from reading a wide annotation row could
	// hide it.
	if m.wrapEnabled {
		m.SetXOffset(0)

		return
	}

	// With wrap off every line is one row that starts at the gutter, so
	// the cells of the match are the gutter plus the cells the layout
	// gives each of its columns. The layout counts what a style's
	// transform adds before them, as the rendered row shows it. A match
	// that ends on a later line runs to the end of this one.
	end := match.rng.End
	if end.Line != i {
		end = position.New(i, utf8.RuneCountInString(view.Lines().Line(i).Content()))
	}

	x := layout.GutterWidth() + layout.CellOf(match.rng.Start)
	xEnd := layout.GutterWidth() + layout.CellOf(end)

	// A match that fits the first screen shows without scrolling, so the
	// view keeps the gutter and the start of the line in sight.
	if xEnd <= m.scrollWidth() {
		m.SetXOffset(0)

		return
	}

	// Any other match takes the offset that centers its first cell, the
	// way the Y offset centers its row.
	m.SetXOffset(x - (m.scrollWidth()-1)/2)
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
		}
	}

	return m, nil
}

// getViewDimensions returns (width, height, ok). A false ok means the content
// area has no room for rows, and View renders "". That happens when a
// dimension is zero or negative, when the frame of the container style takes
// all of it, or when the separator of the side-by-side view leaves its panes
// no column.
func (m *Model) getViewDimensions() (int, int, bool) {
	if !m.canRender() {
		return 0, 0, false
	}

	return m.maxWidth(), m.maxHeight(), true
}

// renderContent applies styling and renders lines into final output. It cuts
// each row to contentW, a width the printer's gutter can force a row past on
// a viewport of only a few columns, and pads it out to contentW, so the style
// only fills the rows below them. A style with a width would word-wrap every
// row on each render, and wrap a row wider than contentW onto a second
// screen row that the scroll math does not count.
func (m *Model) renderContent(lines []string, contentW, contentH int) string {
	textStyle := m.printer.Style(kind.Text)

	// The style pads the rows it adds to the width of the widest row, so an
	// empty window renders one empty row to carry the width.
	if len(lines) == 0 {
		lines = []string{""}
	}

	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], contentW, "")
		if pad := contentW - ansi.StringWidth(lines[i]); pad > 0 {
			lines[i] += textStyle.Render(strings.Repeat(" ", pad))
		}
	}

	contents := textStyle.
		Height(contentH).
		MaxHeight(contentH).
		Render(strings.Join(lines, "\n"))

	return m.style.
		UnsetWidth().UnsetHeight().
		Render(contents)
}

// View renders the viewport. A zero Model, one not created with [New],
// renders "".
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

	leftRows := m.trimFrame(splitLines(p.Print(m.highlighted(false, window))), first, last)

	// Without a diff, both panes show the left content, so the right pane
	// reuses the rows the left pane rendered.
	rightRows := leftRows
	if m.baseRight != nil {
		rightRows = m.trimFrame(splitLines(p.Print(m.highlighted(true, window))), first, last)
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
			leftPadded := ansi.Truncate(left, paneWidth, "")
			if padding := paneWidth - ansi.StringWidth(leftPadded); padding > 0 {
				leftPadded += textStyle.Render(strings.Repeat(" ", padding))
			}

			combined = append(combined, leftPadded+separator+ansi.Truncate(right, paneWidth, ""))
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

// blankPaneRow renders the row a side-by-side pane shows where it has run
// out of rows, because the line wraps taller in the other pane. It is one
// empty content row with the frame of p's container style around it, so the
// pane keeps its border down the whole window. Print pads the row out to p's
// container width, which the side-by-side view pins to the pane width, so a
// container without a horizontal frame gives a row of [kind.Text] spaces as
// wide as the pane.
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

// splitLines splits content by newlines. Unlike [strings.Split], it does not
// produce an empty trailing element when the content ends with a newline.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}

	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}
