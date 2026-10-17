package manifest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/quantity"
)

// Apply writes the new text of each file of [Plan.Files] and returns a
// [Result] that records what it did. It writes each file atomically, so a
// crash leaves the old text or the new text. Each file keeps its mode, its
// owner, and its group. A symlink keeps pointing at its target, which
// receives the new text.
//
// Apply checks each file immediately before it replaces it, and leaves the
// file as it is when it cannot replace it whole. A file that the plan did
// not read from disk, or that the process cannot write, fails with an
// error that matches [ErrReadOnly]. A file whose text differs from the
// text that the plan read fails with an error that matches [ErrChanged].
// Apply continues with the other files, and [Result.Err] returns an error
// for each file that Apply did not write.
//
// When ctx has ended before the first file, Apply returns an error that
// matches ctx.Err() and does not write a file. When ctx ends partway, each
// file that Apply has not reached counts as not written.
func (p *Plan) Apply(ctx context.Context) (*Result, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err //nolint:wrapcheck // Apply ends with the error of its context.
	}

	res := &Result{plan: p, failed: make(map[int]failure)}

	for _, f := range p.files {
		err := ctx.Err()
		if err == nil {
			err = f.write()
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

	return res, nil
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
	counts counts
}

// failure is a file that [Plan.Apply] did not write.
type failure struct {
	// The problem of the file, bound to its name.
	err error
	// The headline of the problem, as the summary names it.
	reason string
}

// Files returns the files that Apply wrote, in load order.
func (r *Result) Files() []*PlanFile {
	return r.files
}

// Summary returns a line for each document that Apply migrated and for
// each file that a problem blocked or that Apply did not write. A row
// below the line of a document names each value that Apply deleted. The
// last line counts the files, as in "2 files migrated, 1 up to date".
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

			fmt.Fprintf(&b, "%s: not migrated, %s\n", f.name, quantity.Plural(errs, "error"))

		case r.failed[i].err != nil:
			fmt.Fprintf(&b, "%s: not written, %s\n", f.name, r.failed[i].reason)

		case written[i] != nil:
			wf := written[i]

			for _, d := range wf.docs {
				fmt.Fprintf(&b, "%s: migrated %s to %s\n", f.name, d.name, d.to)

				for _, rm := range d.removals {
					b.WriteString("  removed " + removalPath(rm) + "\n")
				}
			}

			if wf.notMigrated > 0 {
				fmt.Fprintf(&b, "%s: %s not migrated\n", f.name, quantity.Plural(wf.notMigrated, "document"))
			}
		}
	}

	b.WriteString(r.counts.text("migrated"))

	return b.String()
}

// Warnings returns the warnings that remain in the files as Apply left
// them, as a [*LoadError]. It returns nil when the files do not hold a
// warning, or when [Result.Err] returns an error.
func (r *Result) Warnings() error {
	return r.warnings
}

// Err returns the problems that fail the rewrite as a [*LoadError] that
// holds the warnings too, or nil. The error holds the problems of the
// plan, but for each file that Apply did not write, it holds the reason
// alone.
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
			src, _ := hook.Reread(f.src, string(f.plan.after)).(*niceyaml.Source) //nolint:errcheck // The hook returns a Source.
			nf := &file{src: src, dir: f.dir, target: f.target, name: f.name}
			l.files[i] = nf
			l.checkFile(ctx, i, nf)

		case r.failed[i].err != nil:
			l.add(&row{err: r.failed[i].err, file: i, index: -1, unplaced: true})
		}
	}

	rows := slices.Concat(kept, l.finishRows(l.rows))
	slices.SortStableFunc(rows, compareRows)

	r.err, r.warnings = l.outcome(rows)

	r.counts = p.counts
	r.counts.migrated = len(r.files)
	r.counts.notWritten = len(r.failed)
	r.counts.notMigrated = 0

	for i, f := range p.l.files {
		switch {
		case f.blocked:
		case f.plan != nil && written[i]:
			r.counts.notMigrated += f.plan.notMigrated
		case f.plan == nil:
			r.counts.notMigrated += f.notMigrated
		}
	}
}

// firstLine returns the first line of s.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")

	return line
}

// readOnly returns the problem of a file that Apply cannot write, or nil.
// It matches [ErrReadOnly].
func (f *PlanFile) readOnly() error {
	app := f.reg.app()
	t := f.f.target

	switch t.kind {
	case targetStdin:
		return readOnlyProblem("cannot rewrite standard input")

	case targetReader:
		return readOnlyProblem("cannot rewrite a file that " + app + " read from a stream")

	case targetFS:
		return readOnlyProblem("cannot rewrite a file that "+app+" provides",
			fmt.Errorf("only a newer release of %s updates this file", app))

	case targetSource:
		if len(hook.Layers(f.f.src)) > 0 {
			return f.layersProblem()
		}

		return readOnlyProblem("cannot rewrite a file that " + app + " built in memory")

	case targetDisk:
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

// readOnlyProblem returns the problem, with details, of a file that Apply
// cannot write. The problem matches [ErrReadOnly].
func readOnlyProblem(msg string, details ...error) error {
	return niceyaml.Unchecked(&problemError{msg: msg, marks: []error{ErrReadOnly}}, niceyaml.WithDetails(details...))
}

// changedProblem returns the problem of a file whose text differs from
// the text that the plan read. The problem matches [ErrChanged].
func changedProblem(msg string) error {
	return niceyaml.Unchecked(&problemError{msg: msg, marks: []error{ErrChanged}},
		niceyaml.WithDetails(errors.New("run the command again")))
}
