package manifest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/inputhook"
	"go.jacobcolvin.com/niceyaml/internal/quantity"
)

// WriteFunc writes the new text of one file of a plan, as
// [PlanFile.After] returns it, or returns the problem that prevents the
// write. [Plan.Apply] calls it for each file of [Plan.Files] that it
// writes. [WriteAtomic] is the WriteFunc that Apply uses by default.
type WriteFunc func(ctx context.Context, f *PlanFile) error

// ApplyOption configures one call of [Plan.Apply].
//
// Available options:
//   - [WithWriter]
type ApplyOption func(*applyConfig)

// applyConfig holds the settings that each [ApplyOption] gives an Apply.
type applyConfig struct {
	write WriteFunc
}

// WithWriter is an [ApplyOption] that sets the [WriteFunc] that writes
// each file. A program that keeps its files elsewhere, such as in a
// database, passes a WriteFunc that stores [PlanFile.After] there. A nil
// fn selects [WriteAtomic], the default.
//
// A WriteFunc can call WriteAtomic first and store the file another way
// when the error of WriteAtomic matches [ErrReadOnly]. The guarantees of
// WriteAtomic hold only for the files that it writes. A WriteFunc that
// stores a file another way should first compare the stored text with the
// text of [PlanFile.Source]. A WriteFunc that skips the compare can
// overwrite a concurrent edit.
func WithWriter(fn WriteFunc) ApplyOption {
	return func(c *applyConfig) {
		c.write = fn
	}
}

// Apply writes the new text of each file of [Plan.Files] and returns a
// [Result] that records what it did. It hands each file to the
// [WriteFunc] of [WithWriter], or to [WriteAtomic] by default, one file at
// a time in load order. WriteAtomic writes only the files that an
// [input.Path] input read from disk, and fails each other file with an
// error that matches [ErrReadOnly].
//
// Apply counts a file as written when the WriteFunc returns nil for it.
// When the WriteFunc returns an error, Apply counts the file as not
// written and continues with the other files. The error becomes the
// problem of the file in [Result.Err], and its first line becomes the
// reason that [Result.Summary] gives.
//
// Apply refuses each file of [input.Sources] whose Source merges layers,
// whatever the WriteFunc. Such a file fails with an error that matches
// [ErrReadOnly], and Apply does not hand it to the WriteFunc.
//
// [Plan.Err] and [Plan.Warnings] report what a load of the new text
// finds. A program that writes [PlanFile.After] without Apply thus still
// gets that report, but it does not get a Result.
//
// Apply returns a Result even when it returns an error, so a program
// prints [Result.Summary] in either case. The error is the one that
// [Result.Err] returns. A file that Apply has not reached when ctx ends
// counts as not written, and its problem matches ctx.Err().
func (p *Plan) Apply(ctx context.Context, opts ...ApplyOption) (*Result, error) {
	cfg := applyConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.write == nil {
		cfg.write = WriteAtomic
	}

	res := &Result{plan: p, failed: make(map[int]failure)}

	for _, f := range p.files {
		err := ctx.Err()
		if err == nil && len(hook.Layers(f.f.src)) > 0 {
			err = f.layersProblem()
		}

		if err == nil {
			err = cfg.write(ctx, f)
		}

		if err != nil {
			res.failed[f.index] = failure{err: namedSource(f.f.name).Bind(err), reason: firstLine(err.Error())}

			continue
		}

		res.files = append(res.files, f)
	}

	// The check reads the files that Apply wrote. They hold their new text
	// whether or not ctx ended, so the end of ctx fails none of them.
	res.check(context.WithoutCancel(ctx))

	return res, res.err
}

// Result records what [Plan.Apply] did: the files that it wrote, and the
// problems that a load of the files finds as Apply left them.
//
// Receive instances from [Plan.Apply].
type Result struct {
	plan     *Plan
	warnings error
	err      error
	// The files that Apply did not write, by their index in the load, with
	// their problems.
	failed map[int]failure
	files  []*PlanFile
	counts Counts
}

// failure is a file that [Plan.Apply] did not write.
type failure struct {
	// The problem of the file, bound to its name.
	err error
	// The headline of the problem, as the summary names it.
	reason string
}

// Files returns the files that Apply wrote, in load order.
//
// Every call returns the same files in a new slice, so a caller that
// reorders or filters the slice does not change the result.
func (r *Result) Files() []*PlanFile {
	return slices.Clone(r.files)
}

// Summary returns a line for each document that Apply migrated and for
// each file that a problem blocked or that Apply did not write. A row
// below the line of a document names each value that Apply deleted. When
// a file that Apply wrote holds documents that fail to route, a line
// counts them as not migrated. The last line counts the files, as in
// "2 files migrated, 1 up to date", and the documents that fail to route.
//
// Each line takes one row of a terminal. Summary passes the name of each
// file and the reason of each file that Apply did not write through
// [escape.Line], as an error of the load draws them.
func (r *Result) Summary() string {
	var b strings.Builder

	written := make(map[int]*PlanFile, len(r.files))
	for _, f := range r.files {
		written[f.index] = f
	}

	p := r.plan

	for i, f := range p.l.files {
		switch {
		case f.blocked:
			errs := 0

			for _, row := range p.rows {
				if row.file == i && niceyaml.HasErrors(row.err) {
					errs++
				}
			}

			fmt.Fprintf(&b, "%s: not migrated, %s\n", f.label(), quantity.Plural(errs, "error"))

		case r.failed[i].err != nil:
			fmt.Fprintf(&b, "%s: not written, %s\n", f.label(), escape.Line(r.failed[i].reason))

		case written[i] != nil:
			wf := written[i]

			for _, d := range wf.docs {
				fmt.Fprintf(&b, "%s: migrated %s to %s\n", f.label(), wf.docLabel(d), p.reg.layoutLabel(d.Kind, d.To))

				for _, rm := range d.Removals {
					b.WriteString("  removed " + removalPath(rm) + "\n")
				}
			}

			if wf.notMigrated > 0 {
				fmt.Fprintf(&b, "%s: %s not migrated\n", f.label(), quantity.Plural(wf.notMigrated, "document"))
			}
		}
	}

	b.WriteString(r.counts.text("migrated"))

	return b.String()
}

// Counts returns the numbers of files and documents that the last line of
// [Result.Summary] counts.
func (r *Result) Counts() Counts {
	return r.counts
}

// Warnings returns the warnings that remain in the files as Apply left
// them, as a [*LoadError]. It returns nil when the files do not hold a
// warning, or when [Result.Err] returns an error.
func (r *Result) Warnings() error {
	return r.warnings
}

// Err returns the error that [Plan.Apply] returned: the problems that fail
// the rewrite as a [*LoadError] that holds the warnings too, or nil. The
// error holds the reason of each file that Apply did not write and each
// problem that a load finds in the files that Apply wrote. For each other
// file, it holds the problems of the plan. [Plan.Err] keeps the problems
// of the plan in each file that Apply did not write.
func (r *Result) Err() error {
	return r.err
}

// check reads the files as Apply left them, and gathers the problems of
// the result. A file that Apply wrote reads its new text, in which each of
// its problems binds. A file that Apply did not write reports its reason
// alone, and every other file keeps the problems of the plan.
func (r *Result) check(ctx context.Context) {
	p := r.plan

	l := &loader{reg: p.reg, cfg: p.l.cfg, named: p.l.named, files: make([]*file, len(p.l.files))}
	l.cfg.plan = false

	written := make(map[int]bool, len(r.files))
	for _, f := range r.files {
		written[f.index] = true
	}

	planned := make(map[int]bool, len(p.files))
	for _, f := range p.files {
		planned[f.index] = true
	}

	var kept []*row

	for _, row := range p.rows {
		if !planned[row.file] {
			kept = append(kept, row)
		}
	}

	for i, f := range p.l.files {
		l.files[i] = f

		switch {
		case written[i]:
			nf := &file{src: f.src.WithText(f.plan.after), dir: f.dir, target: f.target, name: f.name}
			l.files[i] = nf
			l.checkFile(ctx, i, nf)

		case r.failed[i].err != nil:
			// A reason that binds in a document, such as the merge of
			// layers, takes the kind and the version of that document.
			reason := &row{err: r.failed[i].err, file: i, index: -1, unplaced: true}
			if idx, ok := niceyaml.NewErrorTree(reason.err).DocumentIndex(); ok {
				reason.doc = p.l.docAt(i, idx)
			}

			l.add(reason)
		}
	}

	rows := slices.Concat(kept, l.finishRows(l.rows))
	slices.SortStableFunc(rows, compareRows)

	r.err, r.warnings = l.outcome(rows)

	r.counts = p.counts
	r.counts.Migrate = len(r.files)
	r.counts.NotWritten = len(r.failed)
	r.counts.Unrouted = 0

	for i, f := range p.l.files {
		switch {
		case f.blocked:
		case f.plan != nil && written[i]:
			r.counts.Unrouted += f.plan.notMigrated
		case f.plan == nil:
			r.counts.Unrouted += f.notMigrated
		}
	}
}

// firstLine returns the first line of s.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")

	return line
}

// readOnly returns the problem of a file that [WriteAtomic] cannot write,
// or nil. It matches [ErrReadOnly].
func (f *PlanFile) readOnly() error {
	app := f.reg.app()
	t := f.f.target

	switch t.kind {
	case inputhook.Stdin:
		return readOnlyProblem("cannot rewrite standard input")

	case inputhook.Reader:
		return readOnlyProblem("cannot rewrite a file that " + app + " read from a stream")

	case inputhook.FS:
		return readOnlyProblem("cannot rewrite a file of a file system that " + app + " only reads")

	case inputhook.Source:
		return readOnlyProblem("cannot rewrite a file that " + app + " built in memory")

	case inputhook.Disk:
		if t.outside {
			return readOnlyProblem("cannot rewrite a file that links outside " + t.root)
		}

		return diskReadOnly(t.diskPath())
	}

	return nil
}

// layersProblem returns the problem of a document that merges layers,
// bound at its version field.
func (f *PlanFile) layersProblem() error {
	problem := readOnlyProblem("cannot rewrite a document that merges layers")

	for _, d := range f.f.docs {
		if d.after == "" {
			continue
		}

		if d.route.versionTxt.found {
			//nolint:wrapcheck // The binding is the problem of the file.
			return d.doc.Bind(niceyaml.Locate(problem, niceyaml.AtPath(f.reg.cfg.versionField)))
		}

		return d.doc.Bind(problem) //nolint:wrapcheck // The binding is the problem of the file.
	}

	return problem
}

// readOnlyProblem returns the problem, with details, of a file that
// [Plan.Apply] or [WriteAtomic] refuses to write. The problem matches
// [ErrReadOnly].
func readOnlyProblem(msg string, details ...error) error {
	return niceyaml.Unchecked(&problemError{msg: msg, marks: []error{ErrReadOnly}}, niceyaml.WithDetails(details...))
}

// unkeptProblem returns the problem, with details, of a file that
// [WriteAtomic] cannot replace whole. The system refuses to give the new file something
// that the old one has, and err is the error of that refusal. The problem
// matches [ErrReadOnly] and keeps the reason that the system gives in err.
func unkeptProblem(err error, msg string, details ...error) error {
	return niceyaml.Unchecked(
		&problemError{err: systemReason(err), msg: msg, marks: []error{ErrReadOnly}},
		niceyaml.WithDetails(details...),
	)
}

// changedProblem returns the problem of a file whose text differs from
// the text that the plan read. The problem matches [ErrChanged].
func changedProblem(msg string) error {
	return niceyaml.Unchecked(&problemError{msg: msg, marks: []error{ErrChanged}},
		niceyaml.WithDetails(errors.New("run the command again")))
}
