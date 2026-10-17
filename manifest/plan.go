package manifest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/quantity"
	"go.jacobcolvin.com/niceyaml/line"
)

// PlanMigration reads the documents of in and runs the checks of a load on
// each. It computes the new text of each file whose documents declare a
// version older than the written version of their kind. It does not write
// a file, and [Plan.Apply] writes the files. The options apply as they do
// to a load, and the plan skips the checks across the set.
//
// A document blocks its file when it fails to parse, or when it routes and
// then fails a check. The plan leaves a blocked file as it is. A document
// that fails to route keeps its text, and the other documents of its file
// still migrate. The plan reports each problem in [Plan.Err], so the other
// files still migrate.
//
// PlanMigration returns a nil plan and an error only in two cases. One is
// a ctx that ends. The other is a load of the same input that fails before
// it reads a file. A pattern that does not parse and an input path that
// does not exist each fail a load that way.
func (r *Registry) PlanMigration(ctx context.Context, in Input, opts ...LoadOption) (*Plan, error) {
	cfg := newLoadConfig(opts)
	cfg.plan = true
	cfg.noSetChecks = true

	l, err := r.check(ctx, in, cfg)
	if err != nil {
		return nil, err
	}

	return l.newPlan(), nil
}

// Plan is the rewrite that [Registry.PlanMigration] computes. It holds the
// new text of each file that changes, and the problems that a load of the
// files finds after [Plan.Apply] writes them. Apply is the only method of
// a Plan that writes a file.
//
// The methods of a Plan are safe for concurrent use, but Apply writes each
// file once, so a program should call it once.
//
// Receive instances from [Registry.PlanMigration].
type Plan struct {
	reg *Registry
	// The loader of the plan, whose settings and files a result reuses.
	l        *loader
	warnings error
	err      error
	calls    map[StepID]int
	files    []*PlanFile
	blocked  []string
	sources  []*niceyaml.Source
	// The rows of the plan, finished, in load order.
	rows   []*row
	counts counts
}

// PlanFile is one file that a [Plan] changes: its text as the plan read it
// and its new text.
//
// Receive instances from [Plan.Files] or [Result.Files].
type PlanFile struct {
	reg *Registry
	f   *file
	// The warnings of the values that the rewrite deletes, finished.
	removals []error
	after    []byte
	docs     []migratedDoc
	// The index of the file in the load.
	index int
	// The number of documents of the file that fail to route, so the
	// rewrite leaves them as they are.
	notMigrated int
}

// migratedDoc is one document that a [PlanFile] migrates.
type migratedDoc struct {
	// The document as messages name it, as in `Hours "weekday"`.
	name     string
	from     string
	to       string
	removals []edit.Removal
}

// counts holds the numbers of files and documents that a summary counts.
type counts struct {
	migrated    int
	notMigrated int
	blocked     int
	notWritten  int
	upToDate    int
}

// newPlan returns the plan of the files that l checked.
func (l *loader) newPlan() *Plan {
	p := &Plan{reg: l.reg, l: l, calls: l.reg.stepIDs()}

	maps.Copy(p.calls, l.calls)

	for _, f := range l.files {
		if f.src != nil {
			p.sources = append(p.sources, f.src)
		}
	}

	for _, f := range l.files {
		switch {
		case f.blocked:
			p.blocked = append(p.blocked, f.name)
			p.counts.blocked++

		case f.plan != nil:
			p.files = append(p.files, f.plan)
			p.counts.migrated++
			p.counts.notMigrated += f.plan.notMigrated

		default:
			p.counts.upToDate++
			p.counts.notMigrated += f.notMigrated
		}
	}

	p.rows = l.finishRows(l.rows)
	p.err, p.warnings = l.outcome(p.rows)

	return p
}

// Files returns the files that the plan changes, in load order. It leaves
// out each file that is up to date or that a problem blocks. It also
// returns each file that [Plan.Apply] cannot write, so a dry run shows its
// diff.
func (p *Plan) Files() []*PlanFile {
	return p.files
}

// Blocked returns the names of the files that a problem blocks, as
// messages show them, in load order.
func (p *Plan) Blocked() []string {
	return p.blocked
}

// Sources returns the Source of each file that the plan read, in load
// order, as [Set.Sources] does.
func (p *Plan) Sources() []*niceyaml.Source {
	return p.sources
}

// Summary returns a line that counts the files of the plan, as in
// "2 files to migrate, 1 up to date, nothing written".
func (p *Plan) Summary() string {
	return p.counts.text("to migrate") + ", nothing written"
}

// Warnings returns the warnings that remain after [Plan.Apply] writes the
// files, as a [*LoadError]. It returns nil when the files do not hold a
// warning after Apply, or when [Plan.Err] returns an error.
func (p *Plan) Warnings() error {
	return p.warnings
}

// Err returns the problems that fail the plan as a [*LoadError] that holds
// the warnings too, or nil. It holds each problem that blocks a file, the
// problem of each document that fails to route, and each warning that
// [WithWarningsAsErrors] makes an error.
func (p *Plan) Err() error {
	return p.err
}

// StepCalls returns the number of calls of each step of the registry that
// the plan made, with 0 for a step that never ran. A test that plans the
// files of each release can thus find a step whose path never selects a
// value.
func (p *Plan) StepCalls() map[StepID]int {
	return maps.Clone(p.calls)
}

// Source returns the Source of the file as the plan read it.
func (f *PlanFile) Source() *niceyaml.Source {
	return f.f.src
}

// After returns the new text of the file.
func (f *PlanFile) After() []byte {
	return f.after
}

// Removals returns the values that the rewrite deletes from the file, or
// nil when the rewrite does not delete a value. It returns them as a
// [*LoadError] of the warnings that a load reports for them, bound in the
// text that the plan read. Removals does not apply the filter of
// [WithWarningFilter] or [WithWarningsAsErrors], so each value that a
// rewrite deletes has a row that says so.
func (f *PlanFile) Removals() error {
	return newLoadError(namedSource(f.f.name), f.removals)
}

// Header returns a line for each document of the file that migrates, as
// in `config/hours.yaml: Hours "weekday" from cafe.jacobcolvin.com/v1alpha1
// to cafe.jacobcolvin.com/v1`. An indented row below the line of a
// document names each value that the rewrite deletes, with the reason of
// its step. When the file holds documents that do not migrate, a last
// line counts them.
func (f *PlanFile) Header() string {
	var b strings.Builder

	for _, d := range f.docs {
		fmt.Fprintf(&b, "%s: %s from %s to %s\n", f.f.name, d.name, d.from, d.to)

		for _, rm := range d.removals {
			b.WriteString("  removes " + removalPath(rm))

			if rm.Reason != "" {
				b.WriteString(": " + rm.Reason)
			}

			b.WriteByte('\n')
		}
	}

	if f.notMigrated > 0 {
		fmt.Fprintf(&b, "%s: %s not migrated\n", f.f.name, quantity.Plural(f.notMigrated, "document"))
	}

	return strings.TrimSuffix(b.String(), "\n")
}

// Diff returns the change of the file as a view of its hunks, with the
// line numbers of the file and lines of context around each hunk. When
// [niceyaml.WithExcerpts] hides the excerpts of the Source, the view does
// not show a line of the file. It holds one row instead, which counts the
// lines that change.
func (f *PlanFile) Diff(lines int) *line.View {
	after, _ := hook.Reread(f.f.src, string(f.after)).(*niceyaml.Source) //nolint:errcheck // The hook returns a Source.
	result := diff.Diff(f.f.src.Lines(), after.Lines())

	if f.f.src.ShowsExcerpts() {
		return result.Hunks(lines)
	}

	stats := result.Stats()

	var parts []string

	if stats.Removed > 0 {
		parts = append(parts, quantity.Plural(stats.Removed, "line")+" removed")
	}

	if stats.Added > 0 {
		word := "added"
		if len(parts) == 0 {
			word = quantity.Plural(stats.Added, "line") + " added"
		} else {
			word = strconv.Itoa(stats.Added) + " " + word
		}

		parts = append(parts, word)
	}

	return line.NewView(niceyaml.NewSourceFromString(strings.Join(parts, ", ")).Lines())
}

// removalPath returns the path of the value that rm removed, as a header
// names it, without the `~` of an entry.
func removalPath(rm edit.Removal) string {
	if rm.Item {
		return rm.Path.String()
	}

	parent, _ := rm.Path.Parent()

	return parent.String()
}

// planFile decides what the plan does with f and adds the problems of its
// documents to the report. A document blocks the file when it fails to
// parse, or when it routes and then fails a check. A file without a
// document that migrates is up to date. Any other file changes, unless its
// new text exceeds a limit of its Source.
func (l *loader) planFile(i int, f *file) {
	var migrating []*docState

	blocked := false

	for _, d := range f.docs {
		switch {
		case d.blocks():
			blocked = true
		case !d.routed && !d.passed:
			f.notMigrated++
		case d.after != "":
			migrating = append(migrating, d)
		}
	}

	if blocked || len(migrating) == 0 {
		f.blocked = blocked

		for _, d := range f.docs {
			d.report(false)
		}

		return
	}

	after := newText(f.src.Text(), migrating)

	err := checkLimits(f.src, after)
	if err != nil {
		f.blocked = true

		for _, d := range f.docs {
			d.report(false)
		}

		l.add(&row{err: namedSource(f.name).Bind(err), file: i, index: -1, unplaced: true})

		return
	}

	pf := &PlanFile{reg: l.reg, f: f, index: i, after: []byte(after), notMigrated: f.notMigrated}

	for _, d := range f.docs {
		if d.after == "" {
			d.report(false)

			continue
		}

		d.rewritten = true
		d.report(true)

		pf.docs = append(pf.docs, migratedDoc{
			name:     d.label(),
			from:     l.reg.label(d.route.version),
			to:       l.reg.label(d.route.kind.written),
			removals: d.removedValues,
		})

		for _, w := range d.removed {
			pf.removals = append(pf.removals, l.wrapWarning(&row{err: w, doc: d, file: i, index: d.index}))
		}
	}

	f.plan = pf
}

// blocks reports whether the document blocks the rewrite of its file. A
// document blocks it when it fails to parse, or when it routes and then
// fails a check.
func (d *docState) blocks() bool {
	if d.doc.Err() != nil {
		return true
	}

	return d.routed && !d.passed && slices.ContainsFunc(d.problems, niceyaml.HasErrors)
}

// label returns the document as a header names it. The label is the kind
// and the name when the registry reads names. Otherwise it is the kind,
// after the place of the document when the file holds several.
func (d *docState) label() string {
	name := d.route.kind.kind.name
	if d.hasName {
		return fmt.Sprintf("%s %q", name, d.name)
	}

	if len(d.f.src.Documents()) > 1 {
		return fmt.Sprintf("document %d: %s", d.index+1, name)
	}

	return name
}

// newText returns text with the text of each of docs at the written
// version in place of the text that the document held.
func newText(text string, docs []*docState) string {
	var b strings.Builder

	last := 0

	for _, d := range docs {
		start, end := textBounds(d.doc)
		b.WriteString(text[last:start])
		b.WriteString(d.after)

		last = end
	}

	b.WriteString(text[last:])

	return b.String()
}

// checkLimits returns the problem of after, the new text of the file of
// src, when it exceeds a limit of src. The check keeps every file that
// Apply writes readable by the next load. The problem matches
// [niceyaml.ErrLimit]. A load of the file passes, so [niceyaml.IsInvalid]
// does not report the problem.
func checkLimits(src *niceyaml.Source, after string) error {
	bytes, depth := hook.Limits(src)

	if bytes > 0 && len(after) > bytes {
		var details []error

		if len(src.Documents()) > 1 {
			details = append(details, errors.New("split its documents across several files"))
		}

		return niceyaml.Unchecked(&problemError{
			msg:   "cannot rewrite a file whose new text holds more than " + quantity.Bytes(bytes),
			marks: []error{niceyaml.ErrLimit},
		}, niceyaml.WithDetails(details...))
	}

	if depth <= 0 {
		return nil
	}

	reread, _ := hook.Reread(src, after).(*niceyaml.Source) //nolint:errcheck // The hook returns a Source.

	for _, doc := range reread.Documents() {
		if errors.Is(doc.Err(), niceyaml.ErrLimit) {
			return &problemError{
				msg:   fmt.Sprintf("cannot rewrite a document whose new text nests more than %d levels", depth),
				marks: []error{niceyaml.ErrLimit},
			}
		}
	}

	return nil
}

// text returns the line of c, where verb names what happens to the files
// that migrate, as "to migrate" or "migrated". The first count of files
// names them, as in "2 files migrated, 1 up to date".
func (c counts) text(verb string) string {
	var parts []string

	named := false

	files := func(n int, what string) {
		if n == 0 {
			return
		}

		count := strconv.Itoa(n)
		if !named {
			count, named = quantity.Plural(n, "file"), true
		}

		parts = append(parts, count+" "+what)
	}

	files(c.migrated, verb)

	if c.notMigrated > 0 {
		parts = append(parts, quantity.Plural(c.notMigrated, "document")+" not migrated")
	}

	files(c.blocked, "not migrated")
	files(c.notWritten, "not written")
	files(c.upToDate, "up to date")

	if len(parts) == 0 {
		return "no files"
	}

	return strings.Join(parts, ", ")
}

// stepIDs returns each step of the registry with a count of 0.
func (r *Registry) stepIDs() map[StepID]int {
	ids := make(map[StepID]int)

	for _, entry := range r.kinds {
		for _, v := range entry.versions {
			for i := range v.cfg.steps {
				ids[StepID{Kind: entry.kind.name, Version: v.cfg.name, Step: i + 1}] = 0
			}
		}
	}

	return ids
}
