package manifest

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/internal/edithook"
	"go.jacobcolvin.com/niceyaml/paths"
)

// StepID names one step of a version, as [Migrate] adds it.
type StepID struct {
	// Kind is the name of the kind, as [NewKind] gives it.
	Kind string
	// Version is the name of the version that holds the step.
	Version string
	// Step counts the steps of the version from 1, in the order of
	// [Migrate].
	Step int
}

// String returns the step as a message names it, such as
// "Hours cafe.jacobcolvin.com/v2 step 3".
func (id StepID) String() string {
	return fmt.Sprintf("%s %s step %d", id.Kind, id.Version, id.Step)
}

// migrate runs the steps of each version after the one the document
// declares, and checks the result against the schema of each. The working
// copy then holds the document in the layout of the current version. It
// reports whether the document reached the current version.
func (d *docState) migrate(ctx context.Context) bool {
	entry := d.route.kind
	from := d.route.version
	current := entry.versions[len(entry.versions)-1]

	if from == current {
		return true
	}

	reg := d.l.reg

	ed, err := edit.New(d.doc,
		edit.WithApp(reg.app()),
		edit.WithProtected(reg.typeFields()...),
		edit.WithUnmarshalers(reg.cfg.unmarshalers...),
	)
	if err != nil {
		d.fail(err)

		return false
	}

	for _, v := range entry.versions[from.index+1:] {
		seen := len(ed.Removals())
		ok := d.runSteps(ctx, ed, v) && d.setTypeFields(ctx, ed, v)

		d.removals(ed.Removals()[seen:], v)

		if !ok {
			return false
		}

		// The schema of the written version reads the text that a rewrite
		// writes, as the next load of the file does.
		if v == entry.written {
			d.after = ed.Text()
			d.earlyWarned = len(d.warned)
		}

		if !d.prepareWork(ed.Node(), edithook.Locator(ed)) {
			return false
		}

		var detail error

		if v != current {
			detail = fmt.Errorf("%s rejects this value on the way to %s", reg.label(v), reg.label(current))
		}

		if !d.checkSchema(ctx, v, detail) {
			return false
		}
	}

	return true
}

// runSteps runs the steps of v on ed, each as one [edit.Doc.Apply], and
// reports whether each passed.
func (d *docState) runSteps(ctx context.Context, ed *edit.Doc, v *versionEntry) bool {
	label := edit.WithLabel(d.l.reg.label(v))

	for i, s := range v.cfg.steps {
		id := StepID{Kind: v.kind.kind.name, Version: v.cfg.name, Step: i + 1}

		err := ed.Apply(ctx, s.path, func(ctx context.Context, n *edit.Node) error {
			d.l.countCall(id)

			warnings := niceyaml.NewWarnings()

			err := callStep(warnings.Context(ctx), id, s.fn, n)

			d.warn(warnings.Err())

			return err
		}, label)
		if err != nil {
			d.fail(err)

			return false
		}
	}

	return true
}

// countCall counts one call of the step id.
func (l *loader) countCall(id StepID) {
	if l.calls == nil {
		l.calls = make(map[StepID]int)
	}

	l.calls[id]++
}

// callStep calls fn with n, and returns its error, or the
// [niceyaml.PanicError] of a panic in it.
func callStep(ctx context.Context, id StepID, fn edit.Func, n *edit.Node) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &panicError{in: id.String(), p: &niceyaml.PanicError{Value: v, Stack: debug.Stack()}}
		}
	}()

	return fn(ctx, n)
}

// setTypeFields sets the type fields of ed to those of v: the kind field
// first, so a version field that the document lacks goes above it.
func (d *docState) setTypeFields(ctx context.Context, ed *edit.Doc, v *versionEntry) bool {
	cfg := &d.l.reg.cfg
	label := d.l.reg.label(v)

	if !cfg.versionOnly {
		err := edithook.SetField(ctx, ed, label, cfg.kindField, v.kindName)
		if err != nil {
			d.fail(err)

			return false
		}
	}

	err := edithook.SetField(ctx, ed, label, cfg.versionField, v.cfg.name)
	if err != nil {
		d.fail(err)

		return false
	}

	return true
}

// removals adds the warning of each value that a step of v removed. A
// value that a rewrite deletes carries the migrate hint. A step newer than
// the written version deletes nothing in a rewrite, so its warning stays a
// warning in strict mode.
func (d *docState) removals(removed []edit.Removal, v *versionEntry) {
	written := v.index <= d.route.kind.written.index

	for _, rm := range removed {
		msg := fmt.Sprintf("%s ignores field %q", rm.Label, rm.Key)
		if rm.Item {
			msg = rm.Label + " ignores this item"
		}

		var details []error

		if rm.Reason != "" {
			details = append(details, errors.New(rm.Reason))
		}

		if written {
			details = append(details, d.hint()...)
		}

		w := d.doc.Bind(newWarning(ErrRemoved, msg, niceyaml.AtPath(rm.Path), niceyaml.AtRange(rm.Range),
			niceyaml.WithDetails(details...)))

		if written {
			d.removed = append(d.removed, w)
			d.removedValues = append(d.removedValues, rm)
		} else {
			d.lenient = append(d.lenient, w)
		}
	}
}

// typeFields returns the paths of the type fields of the registry.
func (r *Registry) typeFields() []paths.Path {
	fields := []paths.Path{r.cfg.versionField}
	if !r.cfg.versionOnly {
		fields = append(fields, r.cfg.kindField)
	}

	return fields
}
