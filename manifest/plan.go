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
	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/input"
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
// The error that PlanMigration returns is the one that [Plan.Err] returns.
// When a document holds a problem, PlanMigration returns a non-nil plan
// beside that error, and [Plan.Apply] still writes the files that the
// problem does not block. A program that migrates the other files thus
// tests the plan for nil, not the error. PlanMigration returns a nil plan
// only when it cannot finish. One such case is a ctx that ends. The other
// is a load of the same input that fails before it reads a file. A
// pattern that Load rejects and an input path that does not exist each
// fail a load that way.
func (r *Registry) PlanMigration(ctx context.Context, in input.Input, opts ...LoadOption) (*Plan, error) {
	cfg := newLoadConfig(opts)
	cfg.plan = true
	cfg.noSetChecks = true

	l, err := r.check(ctx, in, cfg)
	if err != nil {
		return nil, err
	}

	p := l.newPlan()

	return p, p.err
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
	counts Counts
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
	after    string
	docs     []PlanDocument
	// The index of the file in the load.
	index int
	// The number of documents of the file that fail to route, so the
	// rewrite leaves them as they are.
	notMigrated int
}

// PlanDocument is one document that a [PlanFile] migrates. It holds the
// facts that [PlanFile.Header] names in its line for the document, so a
// program can render them in a format of its own, such as a JSON report.
//
// Receive instances from [PlanFile.Documents].
type PlanDocument struct {
	// Kind is the registered name of the kind of the document, as
	// [NewKind] gives it, also for a document that writes an old name
	// from [KindName].
	Kind string
	// Name is the name that the document holds in its [NameField], or ""
	// when the kind has no NameField. The name comes from the text of the
	// file, so it can hold control characters.
	Name string
	// From is the name of the version that the document declares, as
	// [Version] gives it, or "" for the unversioned layout of the kind.
	From string
	// To is the name of the version that the rewrite writes, the written
	// version of the kind.
	To string
	// Removals holds each value that the rewrite deletes from the
	// document, or nil when it deletes none. The path and the range of
	// each value are in the text of [PlanFile.Source]. [PlanFile.Removals]
	// returns the same values as warnings bound in that text.
	Removals []edit.Removal
	// Index is the place of the document among the documents of the
	// file, from 0.
	Index int
}

// String returns the document as a line of [PlanFile.Header] names it:
// the kind and the quoted name, as in `Hours "weekday"`, or the kind
// alone when the document has no name. A header puts the place of a
// document without a name before it when the file holds several
// documents, as in `document 2: Hours`.
func (d PlanDocument) String() string {
	if d.Name != "" {
		return fmt.Sprintf("%s %q", d.Kind, d.Name)
	}

	return d.Kind
}

// Counts holds the numbers of files and documents that [Plan.Summary] and
// [Result.Summary] count.
//
// Receive instances from [Plan.Counts] or [Result.Counts].
type Counts struct {
	// Migrate counts the files that migrate. In a [Plan], they are the
	// files of [Plan.Files]. In a [Result], they are the files that
	// [Plan.Apply] wrote.
	Migrate int
	// Unrouted counts the documents that fail to route, which the rewrite
	// leaves as they are. A Result counts only those of the files that
	// Apply wrote and of the files that are up to date.
	Unrouted int
	// Blocked counts the files that a problem blocks, the files of
	// [Plan.Blocked].
	Blocked int
	// NotWritten counts the files of [Plan.Files] that Apply did not
	// write. It is 0 in a Plan.
	NotWritten int
	// UpToDate counts the files that hold no document that migrates and
	// that a problem does not block.
	UpToDate int
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
			p.counts.Blocked++

		case f.plan != nil:
			p.files = append(p.files, f.plan)
			p.counts.Migrate++
			p.counts.Unrouted += f.plan.notMigrated

		default:
			p.counts.UpToDate++
			p.counts.Unrouted += f.notMigrated
		}
	}

	p.rows = l.finishRows(l.rows)
	p.err, p.warnings = l.outcome(p.rows)

	return p
}

// Files returns the files that the plan changes, in load order. It leaves
// out each file that is up to date or that a problem blocks. It also
// returns each file that [WriteAtomic] cannot write, so a dry run shows
// its diff and a [WriteFunc] can write it.
//
// Every call returns the same files in a new slice, so a caller that
// reorders or filters the slice does not change the plan.
func (p *Plan) Files() []*PlanFile {
	return slices.Clone(p.files)
}

// Blocked returns the names of the files that a problem blocks, in load
// order. Each name is as the input gives it, as [niceyaml.Source.Name]
// returns it, so a program can match it against the names of
// [Plan.Sources]. A name can hold control characters, so a renderer
// passes each through [escape.Line] before it prints the name.
//
// Every call returns the same names in a new slice, so a caller that
// reorders or filters the slice does not change the plan.
func (p *Plan) Blocked() []string {
	return slices.Clone(p.blocked)
}

// Sources returns the Source of each file that the plan read, in load
// order, as [Set.Sources] does.
//
// Every call returns the same Sources in a new slice, so a caller that
// reorders or filters the slice does not change the plan.
func (p *Plan) Sources() []*niceyaml.Source {
	return slices.Clone(p.sources)
}

// Summary returns a line that counts the files of the plan, as in
// "2 files to migrate, 1 up to date, nothing written". The line also
// counts the documents that fail to route as not migrated. [Plan.Counts]
// returns the same numbers.
func (p *Plan) Summary() string {
	return p.counts.text("to migrate") + ", nothing written"
}

// Counts returns the numbers of files and documents that [Plan.Summary]
// counts.
func (p *Plan) Counts() Counts {
	return p.counts
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
// [WithWarningsAsErrors] makes an error. It is the error that
// [Registry.PlanMigration] returns.
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
func (f *PlanFile) After() string {
	return f.after
}

// Removals returns the values that the rewrite deletes from the file, or
// nil when the rewrite does not delete a value. It returns them as a
// [*LoadError] of the warnings that a load reports for them, bound in the
// text that the plan read. Removals does not apply [WithWarningFilter] or
// [WithWarningsAsErrors], so each value that a rewrite deletes has a row
// that says so.
func (f *PlanFile) Removals() error {
	return newLoadError(namedSource(f.f.name), f.removals)
}

// Documents returns each document of the file that migrates, in the
// order of the file. A document that is up to date or that fails to route
// does not appear.
//
// Every call returns the same documents in a new slice, so a caller that
// changes the slice or the removals of a document does not change the
// plan.
func (f *PlanFile) Documents() []PlanDocument {
	docs := slices.Clone(f.docs)
	for i := range docs {
		docs[i].Removals = slices.Clone(docs[i].Removals)
	}

	return docs
}

// Header returns a line for each document of the file that migrates, as
// in `config/hours.yaml: Hours "weekday" from cafe.jacobcolvin.com/v1alpha1
// to cafe.jacobcolvin.com/v1`. An indented row below the line of a
// document names each value that the rewrite deletes, with the reason of
// its step. When the file holds documents that fail to route, which the
// rewrite leaves as they are, a last line counts them as not migrated. A
// document that is up to date does not get a line.
//
// Each line takes one row of a terminal. Header passes the name of the
// file and the reason of each removal through [escape.Line], as an error
// of the load draws them. [PlanFile.Source] holds the name as the input
// gives it, and [PlanFile.Documents] holds the facts of each line as
// fields.
func (f *PlanFile) Header() string {
	var b strings.Builder

	for _, d := range f.docs {
		fmt.Fprintf(&b, "%s: %s from %s to %s\n", f.f.label(), f.docLabel(d), f.reg.layoutLabel(d.Kind, d.From),
			f.reg.layoutLabel(d.Kind, d.To))

		for _, rm := range d.Removals {
			b.WriteString("  removes " + removalPath(rm))

			if rm.Reason != "" {
				b.WriteString(": " + escape.Line(rm.Reason))
			}

			b.WriteByte('\n')
		}
	}

	if f.notMigrated > 0 {
		fmt.Fprintf(&b, "%s: %s not migrated\n", f.f.label(), quantity.Plural(f.notMigrated, "document"))
	}

	return strings.TrimSuffix(b.String(), "\n")
}

// Diff returns the change of the file as a view of its hunks, with the
// line numbers of the file and lines of context around each hunk. When
// [niceyaml.Source.ShowsExcerpts] reports false, as [niceyaml.WithExcerpts]
// and [niceyaml.WithPositions] set it, the view holds no rows, and
// [PlanFile.Stats] still counts the lines that change.
//
// A printer renders the view with styles. [line.View.String] renders it
// as plain text for a log, with a "-" beside each line the plan removes
// and a "+" beside each line it adds.
//
// Diff passes ctx to [diff.Diff], so a deadline bounds the diff of a
// large file. When ctx ends, Diff returns a nil view with the error of
// ctx, which matches [context.Canceled] or [context.DeadlineExceeded].
func (f *PlanFile) Diff(ctx context.Context, lines int) (*line.View, error) {
	result, err := f.diff(ctx)
	if err != nil {
		return nil, err
	}

	if !f.f.src.ShowsExcerpts() {
		return result.Unified().Slice(), nil
	}

	return result.Hunks(lines), nil
}

// Stats counts the lines that the plan removes from the file and the
// lines that it adds, from the diff that [PlanFile.Diff] shows. It counts
// them whether or not [niceyaml.Source.ShowsExcerpts] reports true, so a
// program that hides the excerpts of the Source can still print a count
// for each file:
//
//	st, err := f.Stats(ctx)
//	if err != nil {
//		return err
//	}
//
//	fmt.Printf("  %d removed, %d added\n", st.Removed, st.Added)
//
// Stats passes ctx to [diff.Diff], as Diff does. When ctx ends, Stats
// returns zero [diff.Stats] with the error of ctx.
func (f *PlanFile) Stats(ctx context.Context) (diff.Stats, error) {
	result, err := f.diff(ctx)
	if err != nil {
		return diff.Stats{}, err
	}

	return result.Stats(), nil
}

// diff returns the diff of the text that the plan read and the new text
// of the file.
func (f *PlanFile) diff(ctx context.Context) (*diff.Result, error) {
	result, err := diff.Diff(ctx, f.f.src.Lines(), f.f.src.WithText(f.after).Lines())
	if err != nil {
		return nil, err //nolint:wrapcheck // The error of ctx, as Diff and Stats document.
	}

	return result, nil
}

// docLabel returns d as a line of a header names it. A document without
// a name follows its place when the file holds several documents.
func (f *PlanFile) docLabel(d PlanDocument) string {
	if d.Name == "" && len(f.f.src.Documents()) > 1 {
		return fmt.Sprintf("document %d: %s", d.Index+1, d)
	}

	return d.String()
}

// removalPath returns the path of the value that rm removed, as a header
// names it, without the `~` of an entry.
func removalPath(rm edit.Removal) string {
	return rm.Path.Value().String()
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

	pf := &PlanFile{reg: l.reg, f: f, index: i, after: after, notMigrated: f.notMigrated}

	for _, d := range f.docs {
		if d.after == "" {
			d.report(false)

			continue
		}

		d.rewritten = true
		d.report(true)

		pf.docs = append(pf.docs, PlanDocument{
			Index:    d.index,
			Kind:     d.route.kind.kind.name,
			Name:     d.name,
			From:     d.route.version.cfg.name,
			To:       d.route.kind.written.cfg.name,
			Removals: d.removedValues,
		})

		for _, w := range d.removed {
			pf.removals = append(pf.removals, l.wrapProblem(&row{err: w, doc: d, file: i, index: d.index}))
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

// newText returns text, the text of a file, with the text of each of docs
// at the written version in place of the text that the document held. The
// documents come in the order of the file. Each text takes the place of
// the bytes that [edit.FileText] gives its document. The comment lines
// between two documents thus stay where they are.
func newText(text string, docs []*docState) string {
	var b strings.Builder

	last := 0

	for _, d := range docs {
		b.WriteString(text[last:d.afterStart])
		b.WriteString(d.after)

		last = d.afterEnd
	}

	b.WriteString(text[last:])

	return b.String()
}

// checkLimits returns the problem of after, the new text of the file of
// src, when it holds more bytes than the byte limit of src. The edits of
// each document keep the limits of src, the document byte limit among
// them. The documents of one file can still each grow within those limits
// and hold more bytes together than the byte limit allows. The check
// keeps every file that Apply writes readable by the next load. The
// problem matches [niceyaml.ErrLimit]. A load of the file passes, so
// [niceyaml.IsInvalid] does not report the problem.
func checkLimits(src *niceyaml.Source, after string) error {
	bytes := src.ByteLimit()
	if len(after) <= bytes {
		return nil
	}

	return niceyaml.Unchecked(&problemError{
		msg:   "cannot rewrite a file whose new text holds more than " + quantity.Bytes(bytes),
		marks: []error{niceyaml.ErrLimit},
	}, niceyaml.WithDetails(errors.New("split its documents across several files")))
}

// text returns the line of c, where verb names what happens to the files
// that migrate, as "to migrate" or "migrated". The first count of files
// names them, as in "2 files migrated, 1 up to date". So does the first
// count of files after the count of documents, as in "1 file migrated, 1
// document not migrated, 1 file not migrated".
func (c Counts) text(verb string) string {
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

	files(c.Migrate, verb)

	if c.Unrouted > 0 {
		parts = append(parts, quantity.Plural(c.Unrouted, "document")+" not migrated")
		named = false
	}

	files(c.Blocked, "not migrated")
	files(c.NotWritten, "not written")
	files(c.UpToDate, "up to date")

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
