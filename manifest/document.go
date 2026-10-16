package manifest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/schemahook"
	"go.jacobcolvin.com/niceyaml/manifest/internal/edit"
	"go.jacobcolvin.com/niceyaml/paths"
)

// docState is one document of a load, as its checks leave it.
type docState struct {
	// The decoded value, once the decode passed.
	value any
	// The warning of the deprecated version that the document declares, or
	// nil.
	versionWarning error
	l              *loader
	f              *file
	// The document as the user wrote it.
	doc *niceyaml.Node
	// The working copy of the document, in the layout of the current
	// version after the steps ran, or nil. The checks of the document read
	// it, and a schema check that hides a type field reads a blanked copy
	// instead.
	work *niceyaml.Node
	// The document or migrated copy that work copies.
	workFrom *niceyaml.Node
	// Moves a location of work into the document, or nil for the document
	// itself.
	workLocate func(hook.Located) hook.Place
	// The copy of the document that the steps edit, or nil.
	edit *edit.Doc
	// The name, when the document reached the layout of the current version
	// with a name that is a non-empty string.
	name string
	// The text of the document at the written version, for a document that
	// declares an older version and passed the steps up to it.
	after    string
	route    route
	nameRead fieldRead
	// The values that the steps up to the written version removed, which a
	// rewrite deletes, and the warnings of the values.
	removedValues []edit.Removal
	removed       []error
	// The warnings that strict mode leaves warnings.
	lenient []error
	// The warnings of the checks, in check order.
	warned []error
	// The problems of the checks, bound, in check order.
	problems []error
	// The index of the document within its file, and of the file.
	index int
	file  int
	// The number of warnings in warned that the steps up to the written
	// version handed over, which a rewrite clears.
	earlyWarned int
	// A document of the file writes a type field.
	typedFile bool
	// The load skips the document, which holds no content or which
	// [WithIgnore] accepts.
	skipped bool
	// The document routed to a kind and a version.
	routed bool
	// The document reached the layout of the current version with a name
	// that is a non-empty string.
	hasName bool
	// The document passed every check.
	passed bool
	// A plan rewrites the document to the written version of its kind.
	rewritten bool
}

// check runs the checks of the document in order, and stops at the first
// that fails.
func (d *docState) check(ctx context.Context) {
	doc := d.doc

	err := doc.Err()
	if err != nil {
		d.fail(err)

		return
	}

	if doc.IsEmpty() {
		d.skipped = true

		return
	}

	reg := d.l.reg

	if reg.cfg.ignore != nil {
		ok, err := reg.cfg.ignore.Match(ctx, doc)
		if err != nil {
			d.fail(doc.Bind(err))

			return
		}

		if ok {
			d.skipped = true

			return
		}
	}

	if !d.routeDoc(ctx) {
		return
	}

	d.routed = true

	d.deprecations()

	if !d.prepareWork(d.doc, nil) || !d.checkSchema(ctx, d.route.version, nil) ||
		!d.migrate(ctx) {
		return
	}

	d.readName(ctx)

	if !d.decode(ctx) || !d.checkName() {
		return
	}

	d.passed = true
}

// fail adds err, a problem of a check, bound in the document.
func (d *docState) fail(err error) {
	d.problems = append(d.problems, d.doc.Bind(err))
}

// warn adds w, the warnings that a check collected, to those of the
// document. A nil w adds nothing.
func (d *docState) warn(w error) {
	if w != nil {
		d.warned = append(d.warned, w)
	}
}

// report adds the problems of the document to the report of the load. The
// first error of a document that the load reads as a kind it does not
// name carries the details of that choice.
//
// A document that a plan rewrites leaves out the warnings that the
// rewrite clears: the deprecation of its version, the warnings of the
// steps up to the written version, and its removals, which the plan lists
// with the file. Its migrated text stays a migrated text, so the warnings
// of the steps after the written version stay.
func (d *docState) report(migrated bool) {
	adoption := d.route.adoption
	problems := slices.Concat(d.problems, d.removed, d.warned)

	if migrated {
		problems = slices.Concat(
			slices.DeleteFunc(slices.Clone(d.problems), func(e error) bool {
				return e == d.versionWarning //nolint:errorlint // The binding itself, not a chain search.
			}),
			d.warned[d.earlyWarned:],
		)
	}

	for _, err := range problems {
		for problem := range niceyaml.Problems(err) {
			e := problem.Err

			if len(adoption) > 0 && niceyaml.HasErrors(e) {
				e = d.doc.Bind(niceyaml.Locate(e, niceyaml.WithDetails(adoption...)))
				adoption = nil
			}

			d.l.add(&row{err: e, doc: d, file: d.file, index: d.index})
		}
	}

	for _, w := range d.lenient {
		d.l.add(&row{err: w, doc: d, file: d.file, index: d.index, lenient: true})
	}
}

// rewritable reports whether a rewrite can write the file of the
// document, so a problem that a rewrite clears carries the migrate hint.
func (d *docState) rewritable() bool {
	return d.f.target.kind == targetDisk && !d.f.target.outside
}

// hint returns the migrate hint as the last detail of a problem that a
// rewrite clears, or nil when the registry has none or a rewrite cannot
// write the file of the document.
func (d *docState) hint() []error {
	h := d.l.reg.errHint()
	if h == nil || !d.rewritable() {
		return nil
	}

	return []error{h}
}

// skipDetail returns the detail that says how to skip the file of the
// document, which starts with what. It returns the detail when a directory
// walk found the file and no document of the file writes a type field, and
// nil otherwise.
func (d *docState) skipDetail(what string) error {
	if d.f.dir == "" || d.typedFile {
		return nil
	}

	return fmt.Errorf("%s, or move the file out of %s", what, d.f.dir)
}

// routeDoc reads the type fields of the document and picks its kind and
// its version. It reports whether the document routed.
func (d *docState) routeDoc(ctx context.Context) bool {
	reg := d.l.reg
	cfg := &reg.cfg
	doc := d.doc
	rt := &d.route

	err := d.checkLayers()
	if err != nil {
		d.fail(err)

		return false
	}

	rt.versionTxt = readField(doc, cfg.versionField)
	if rt.versionTxt.err != nil {
		d.fail(rt.versionTxt.err)

		return false
	}

	if !cfg.versionOnly {
		rt.kindTxt = readField(doc, cfg.kindField)
		if rt.kindTxt.err != nil {
			d.fail(rt.kindTxt.err)

			return false
		}
	}

	switch {
	case cfg.versionOnly:
		if len(reg.kinds) == 0 {
			d.fail(reg.missingTypeFields(doc, false, false, nil))

			return false
		}

		rt.counted = reg.kinds[0]

		return d.routeVersion(reg.kinds[0], "")

	case rt.kindTxt.found:
		name, version := rt.kindTxt.text, rt.versionTxt.text
		rt.wroteKind = name

		entry := reg.names[name]
		rt.counted = entry

		if rt.versionTxt.found {
			if rm := reg.findRemoval(version, name); rm != nil && !listed(entry, rm) {
				d.fail(reg.removedProblem(doc, rm, false))

				return false
			}
		}

		if entry == nil {
			d.fail(reg.unknownKind(name, version))

			return false
		}

		if !rt.versionTxt.found {
			d.fail(reg.missingTypeFields(doc, false, true, nil))

			return false
		}

		return d.routeVersion(entry, name)

	default:
		return d.adopt(ctx)
	}
}

// listed reports whether rm names a version that the kind of entry still
// lists, which a plan still migrates.
func listed(entry *kindEntry, rm *removal) bool {
	if entry == nil {
		return false
	}

	for _, v := range entry.versions {
		if v.removal == rm {
			return true
		}
	}

	return false
}

// routeVersion picks the version of entry that the document declares, for
// a document that writes the kind name wrote, or "" without a kind field.
// It reports whether the document routed.
func (d *docState) routeVersion(entry *kindEntry, wrote string) bool {
	reg := d.l.reg
	rt := &d.route
	rt.kind = entry

	if !rt.versionTxt.found {
		first := entry.versions[0]

		switch {
		case first.cfg.unversioned && first.removal != nil && !d.l.cfg.plan:
			d.fail(reg.removedProblem(d.doc, first.removal, d.rewritable()))

			return false

		case first.cfg.unversioned:
			rt.version = first

			return true

		case entry.removedUnversioned != nil:
			d.fail(reg.removedProblem(d.doc, entry.removedUnversioned, false))

			return false

		default:
			d.fail(reg.missingTypeFields(d.doc, false, true, nil))

			return false
		}
	}

	version := rt.versionTxt.text

	v := entry.byName[version]
	if v == nil {
		name := wrote
		if name == "" {
			name = entry.kind.name
		}

		if rm := reg.findRemoval(version, name); rm != nil {
			d.fail(reg.removedProblem(d.doc, rm, false))

			return false
		}

		d.fail(reg.unknownVersion(entry, version, wrote))

		return false
	}

	if wrote != "" && v.kindName != wrote {
		d.fail(reg.kindMismatch(v, wrote))

		return false
	}

	rt.version = v

	if v.removal != nil && !d.l.cfg.plan {
		d.fail(reg.removedProblem(d.doc, v.removal, d.rewritable()))

		return false
	}

	return true
}

// adopt picks the kind of a document without a kind field: the first kind
// whose [Claim] accepts it, or for a document without type fields, the
// kind that reads the unversioned layout without a claim. It reports
// whether the document routed.
func (d *docState) adopt(ctx context.Context) bool {
	reg := d.l.reg
	rt := &d.route
	doc := d.doc
	hasVersion := rt.versionTxt.found

	var (
		entry   *kindEntry
		claimed bool
	)

	for _, e := range reg.kinds {
		if e.kind.cfg.claim == nil {
			continue
		}

		warnings := niceyaml.NewWarnings()

		ok, err := e.kind.cfg.claim.Match(warnings.Context(ctx), doc)

		d.warn(warnings.Err())

		if err != nil {
			d.fail(err)

			return false
		}

		if ok {
			entry, claimed = e, true

			break
		}
	}

	if entry == nil && !hasVersion {
		for _, e := range reg.kinds {
			if e.kind.cfg.claim == nil && (e.versions[0].cfg.unversioned || e.removedUnversioned != nil) {
				entry = e

				break
			}
		}
	}

	if entry == nil {
		var details []error

		skip := d.skipDetail("add " + reg.typeFieldsText())
		if skip != nil {
			details = append(details, skip)
		}

		d.fail(reg.missingTypeFields(doc, hasVersion, false, details))

		return false
	}

	rt.counted = entry

	if !d.routeVersion(entry, "") {
		return false
	}

	name := entry.kind.name
	kindField, versionField := fieldName(reg.cfg.kindField), fieldName(reg.cfg.versionField)

	if hasVersion && claimed {
		rt.adoption = append(rt.adoption, fmt.Errorf("the document has no %s, so %s reads it as %s",
			kindField, reg.app(), article(name)))
	} else {
		rt.adoption = append(rt.adoption, fmt.Errorf("the document has no %s and no %s, so %s reads it as an "+
			"unversioned %s", versionField, kindField, reg.app(), name))
	}

	if d.f.dir != "" && !d.typedFile {
		rt.adoption = append(rt.adoption, fmt.Errorf("move the file out of %s if it is not %s", d.f.dir,
			article(name)))
	}

	return true
}

// article returns name after "a", or "an" before a vowel.
func article(name string) string {
	if name != "" && strings.ContainsRune("AEIOUaeiou", rune(name[0])) {
		return "an " + name
	}

	return "a " + name
}

// checkLayers checks that the layers of a merged document write the same
// text in each type field.
func (d *docState) checkLayers() error {
	layers := hook.Layers(d.doc.Source())
	if len(layers) < 2 {
		return nil
	}

	reg := d.l.reg

	fields := []paths.Path{reg.cfg.versionField}
	if !reg.cfg.versionOnly {
		fields = append(fields, reg.cfg.kindField)
	}

	type writer struct {
		node *niceyaml.Node
		read fieldRead
	}

	for i, p := range fields {
		var writers []writer

		for _, l := range layers {
			n, ok := l.(*niceyaml.Node)
			if !ok {
				continue
			}

			if r := readField(n, p); r.found {
				writers = append(writers, writer{node: n, read: r})
			}
		}

		if len(writers) < 2 {
			continue
		}

		top := writers[len(writers)-1]

		for _, w := range writers[:len(writers)-1] {
			if w.read.text == top.read.text {
				continue
			}

			what := "versions"
			if i == 1 {
				what = "kinds"
			}

			detail := top.node.Bind(niceyaml.NewError(fmt.Sprintf("%s overrides %s", top.read.text, w.read.text),
				niceyaml.AtPath(p)))

			//nolint:wrapcheck // The binding is the problem of the document.
			return w.node.Bind(newProblem(ErrMixedLayers, "layers declare different "+what,
				niceyaml.AtPath(p), niceyaml.WithDetails(detail)))
		}
	}

	return nil
}

// deprecations adds the warnings of a deprecated version and of a
// deprecated kind.
func (d *docState) deprecations() {
	reg := d.l.reg
	rt := &d.route
	v, entry := rt.version, rt.kind
	current := entry.versions[len(entry.versions)-1]

	if v.cfg.deprecated && v != current {
		details := append(detailsOf(v.cfg.details), d.hint()...)

		if v.cfg.unversioned {
			d.fail(newWarning(ErrDeprecated, "deprecated unversioned layout of "+entry.kind.name,
				startOf(d.doc), niceyaml.WithDetails(details...)))
		} else {
			d.fail(newWarning(ErrDeprecated, fmt.Sprintf("deprecated version %q", v.cfg.name),
				niceyaml.AtPath(reg.cfg.versionField), niceyaml.WithDetails(details...)))
		}

		d.versionWarning = d.problems[len(d.problems)-1]
	}

	if current.cfg.deprecated {
		at := startOf(d.doc)

		switch {
		case rt.kindTxt.found:
			at = niceyaml.AtPath(reg.cfg.kindField)
		case rt.versionTxt.found:
			at = niceyaml.AtPath(reg.cfg.versionField)
		}

		d.fail(newWarning(ErrDeprecated, fmt.Sprintf("deprecated kind %q", entry.kind.name),
			at, niceyaml.WithDetails(detailsOf(current.cfg.details)...)))
	}
}

// detailsOf returns texts as errors, for the details of a problem.
func detailsOf(texts []string) []error {
	errs := make([]error, 0, len(texts))
	for _, t := range texts {
		errs = append(errs, errors.New(t))
	}

	return errs
}

// checkSchema checks the working copy against the schema of v, and reports
// whether it passed. A version without a schema passes. The check sees
// each type field that the schema declares, with the names of v, and sees
// every other type field blank. Each problem of the check takes detail as
// its last detail, when detail is not nil. Only the schema of the current
// version reports deprecations.
func (d *docState) checkSchema(ctx context.Context, v *versionEntry, detail error) bool {
	if !v.cfg.hasSchema {
		return true
	}

	if v != v.kind.versions[len(v.kind.versions)-1] {
		ctx = schemahook.DeprecationsOff(ctx)
	}

	reg := d.l.reg

	sch, err := reg.schemas.Schema(ctx, v.cfg.schema)
	if err != nil {
		what := fmt.Sprintf("%s cannot load its schema of %s %s", reg.app(), v.kind.kind.name, v.label())
		d.fail(niceyaml.Unchecked(&problemError{msg: what, marks: []error{err}}, niceyaml.WithDetails(err)))

		return false
	}

	work := d.work

	if hide := reg.hiddenFields(sch); len(hide) > 0 {
		var ok bool

		work, ok = d.workingCopy(d.workFrom, d.workLocate, hide)
		if !ok {
			return false
		}
	}

	warnings := niceyaml.NewWarnings()

	err = work.Validate(warnings.Context(ctx), sch)

	d.warn(warnings.Err())

	if err == nil {
		return true
	}

	if detail == nil {
		d.problems = append(d.problems, err)

		return false
	}

	for problem := range niceyaml.Problems(err) {
		d.problems = append(d.problems, d.doc.Bind(niceyaml.Locate(problem.Err, niceyaml.WithDetails(detail))))
	}

	return false
}

// decode decodes the working copy into a value of the Go type of the kind,
// and reports whether the decode passed. The decode admits the type
// fields, and fills each field of the Go type that reads one, unless a
// value on the way decodes itself. It rejects every other key that the Go
// type does not declare.
func (d *docState) decode(ctx context.Context) bool {
	reg := d.l.reg
	entry := d.route.kind

	known, _ := hook.KnownKeys(reg.typeFields()).(niceyaml.DecodeOption) //nolint:errcheck // The hook returns a DecodeOption.

	opts := append([]niceyaml.DecodeOption{niceyaml.WithDisallowUnknownFields(true), known}, reg.cfg.decodeOptions...)
	opts = append(opts, entry.kind.cfg.decodeOptions...)

	warnings := niceyaml.NewWarnings()

	v, err := entry.kind.decode(warnings.Context(ctx), d.work, opts)

	// A Validate method reports a field that the document leaves out at
	// the mapping that lacks it, with a detail that names the field.
	d.warn(d.leftOut(warnings.Err()))

	if err != nil {
		d.problems = append(d.problems, d.leftOut(err))

		return false
	}

	d.value = v

	return true
}

// readName reads the name of a document that reached the layout of the
// current version, when the registry reads names. The name must be a
// non-empty string, as a decode into an any value reads it.
func (d *docState) readName(ctx context.Context) {
	cfg := &d.l.reg.cfg
	if !cfg.hasNameField {
		return
	}

	d.nameRead = readField(d.work, cfg.nameField)
	if d.nameRead.err != nil || !d.nameRead.found {
		return
	}

	v, err := d.work.DecodeAt[any](ctx, cfg.nameField, niceyaml.WithSelfValidation(false))
	if s, ok := v.(string); err == nil && ok && s != "" {
		d.name, d.hasName = s, true
	}
}

// checkName reports whether the document holds a name, when the registry
// reads names, and adds the problem of one that does not.
func (d *docState) checkName() bool {
	cfg := &d.l.reg.cfg
	if !cfg.hasNameField {
		return true
	}

	switch {
	case d.nameRead.err != nil:
		d.problems = append(d.problems, d.work.Bind(d.nameRead.err))

		return false

	case !d.hasName:
		d.problems = append(d.problems, d.work.Bind(newProblem(ErrMissingName, "name must be a non-empty string",
			niceyaml.AtPath(cfg.nameField))))

		return false
	}

	return true
}

// bindObject binds err in the working copy of the document, as
// [Object.Bind] does.
func (d *docState) bindObject(err error) error {
	return d.leftOut(d.work.Bind(err))
}

// leftOut returns err, an error bound in the document, with a detail on
// each problem at a field that the document leaves out. The detail names
// the first key of the path of the problem that the document lacks, and
// says that the field holds its default, because a check of the decoded
// value reported it. It returns err as it is when no problem needs the
// detail.
func (d *docState) leftOut(err error) error {
	if err == nil {
		return nil
	}

	var problems []error

	changed := false

	for problem := range niceyaml.Problems(err) {
		e := problem.Err

		detail := d.leftOutDetail(problem)
		if detail != nil {
			e = d.doc.Bind(niceyaml.Locate(e, niceyaml.WithDetails(detail)))
			changed = true
		}

		problems = append(problems, e)
	}

	if !changed {
		return err
	}

	return d.doc.Bind(hook.Tally(problems)) //nolint:wrapcheck // The binding is the error of the caller.
}

// leftOutDetail returns the detail of problem when its path names a field
// that the document leaves out, below a mapping that the decode reads
// field by field, or nil. The path names the field as the current version
// does, so the detail names that version for a document that declares an
// older one.
func (d *docState) leftOutDetail(problem niceyaml.ErrorTree) error {
	p, ok := problem.Path()
	if !ok || d.work == nil {
		return nil
	}

	if exists(d.doc, p) || exists(d.work, p) {
		return nil
	}

	res := d.work.Resolver()
	prefix := paths.Doc()

	for sel := range p.Selectors() {
		if sel.Kind != paths.SelectorChild {
			return nil
		}

		next := prefix.Child(sel.Name)
		if exists(d.work, next) {
			prefix = next

			continue
		}

		holder, err := res.Node(prefix)
		if err != nil || !isMapping(holder) {
			return nil
		}

		current := d.route.kind.versions[len(d.route.kind.versions)-1]
		if d.route.version == current {
			return fmt.Errorf("the file leaves out %s, so it holds its default", sel.Name)
		}

		return fmt.Errorf("the file leaves out the field that %s names %s, so it holds its default",
			d.l.reg.label(current), sel.Name)
	}

	return nil
}

// exists reports whether doc holds a value at p.
func exists(doc *niceyaml.Node, p paths.Path) bool {
	n, err := doc.Resolver().Node(p)

	return err == nil && !astnode.IsNil(n)
}

// isMapping reports whether node holds a mapping under its anchors and
// tags.
func isMapping(node ast.Node) bool {
	_, ok := astnode.Entries(astnode.Content(node))

	return ok
}
