package manifest

import (
	"cmp"
	"context"
	"errors"
	"runtime/debug"
	"slices"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// LoadOption configures one load or plan.
//
// Available options:
//   - [WithWarningsAsErrors]
//   - [WithWarningFilter]
//   - [WithSetChecks]
type LoadOption func(*loadConfig)

// loadConfig holds the settings that each [LoadOption] configures.
type loadConfig struct {
	filter      func(*Warning) bool
	strict      bool
	noSetChecks bool
	// Whether the checks run for a plan. A plan migrates a document at a
	// version that a removal names. The load reports each file after it
	// knows whether the plan rewrites the file.
	plan bool
}

// newLoadConfig returns the settings of opts.
func newLoadConfig(opts []LoadOption) loadConfig {
	var c loadConfig

	for _, opt := range opts {
		opt(&c)
	}

	return c
}

// WithWarningsAsErrors is a [LoadOption] that turns each warning that the
// filter of [WithWarningFilter] keeps into an error, as
// [niceyaml.AsErrors] does. The load then fails on a deprecation, and each
// message loses its "warning:" label. An app sets it from a flag, so a CI
// pipeline catches a deprecated file before a release removes support
// for it.
func WithWarningsAsErrors(strict bool) LoadOption {
	return func(c *loadConfig) {
		c.strict = strict
	}
}

// WithWarningFilter is a [LoadOption] that gives the load a function that
// decides which warnings it keeps. The function gets each warning as a
// [*Warning], one at a time in load order, and returns false to drop it.
// It runs before [WithWarningsAsErrors] applies, so a dropped warning
// never fails a strict load.
func WithWarningFilter(keep func(*Warning) bool) LoadOption {
	return func(c *loadConfig) {
		c.filter = keep
	}
}

// WithSetChecks is a [LoadOption] that sets whether a load runs the checks
// across the set: duplicate names, [Required], [Singleton], and
// [WithCheck]. An app turns them off when its input holds part of the
// set, such as the files that a commit changes.
func WithSetChecks(enabled bool) LoadOption {
	return func(c *loadConfig) {
		c.noSetChecks = !enabled
	}
}

// Load reads the documents of in and returns the [Set] of their
// manifests. It checks every file and every document before it returns,
// and a problem in one does not stop the others. A load that fails returns
// a nil set and a [*LoadError] that holds every error and every warning in
// load order. A load that passes returns its warnings from
// [Set.Warnings]. [Registry.Check] runs the same checks and returns the
// manifests that pass beside the problems.
//
// The heading of the error starts with the name of the input. The name is
// the path of a lone [Path] or [FS] input, or the name of a lone [Stdin]
// or [Reader] input. A lone [Sources] input that holds one Source gives
// the name of that Source. Any other load leaves the name out.
//
// A pattern that does not parse fails Load before it reads a file, with an
// error that matches
// [go.jacobcolvin.com/niceyaml/schema/matcher.ErrInvalidPattern]. An input
// path that does not exist also fails Load before it reads a file, with an
// error that matches [fs.ErrNotExist]. A load whose ctx ends fails with an
// error that matches ctx.Err().
func (r *Registry) Load(ctx context.Context, in Input, opts ...LoadOption) (*Set, error) {
	c, err := r.Check(ctx, in, opts...)
	if err != nil {
		return nil, err
	}

	if c.err != nil {
		return nil, c.err
	}

	return c.set, nil
}

// Check reads the documents of in and runs the checks of [Registry.Load].
// It returns the manifests that pass beside the problems of the load. An
// editor can thus show the problems of each file and still use the
// manifests that pass. [Checked.Err] returns the error that Load returns
// for the same input.
//
// Check returns a nil result and an error for a pattern that does not
// parse, for an input path that does not exist, and for a ctx that ends.
// Load fails with the same errors.
func (r *Registry) Check(ctx context.Context, in Input, opts ...LoadOption) (*Checked, error) {
	cfg := newLoadConfig(opts)

	l, err := r.check(ctx, in, cfg)
	if err != nil {
		return nil, err
	}

	if !cfg.noSetChecks {
		l.checkNames()
		l.checkCounts()
	}

	set := l.newSet()

	if !cfg.noSetChecks && !l.failed() {
		err := l.runChecks(ctx, set)
		if err != nil {
			return nil, err
		}
	}

	rows := l.finishRows(l.rows)

	c := &Checked{}
	c.err, c.warnings = l.outcome(rows)

	// Build a new set rather than filter the one that the functions of
	// WithCheck saw, because a function might keep that set. It holds each
	// document that passed its own checks, even one that a later error
	// binds in.
	if c.err != nil {
		l.reject(rows)

		set = l.newSet()
	}

	set.warnings = c.warnings
	c.set = set

	return c, nil
}

// Checked is the outcome of [Registry.Check]: the manifests that pass,
// beside the problems of the load.
//
// The methods of a Checked are safe for concurrent use.
//
// Receive instances from [Registry.Check].
type Checked struct {
	set      *Set
	err      error
	warnings error
}

// Set returns the manifests of the documents that do not hold an error, in
// load order. A document holds an error when one of its own checks fails, or
// when a later error binds in it.
//
// The checks across the set bind an error in each manifest of a
// [Singleton] kind after the first. They also bind one in each manifest
// that repeats the name of an earlier manifest of its kind. An error that
// a function of [WithCheck] binds with [Object.Bind] binds in the document
// of the Object. [WithWarningsAsErrors] binds each error that it makes
// from a warning in the document of the warning. An error of a whole file
// or of the whole set does not bind in a document and does not remove a
// manifest. A [Required] kind that the set lacks causes such an error.
//
// When [Checked.Err] returns nil, the set is the one that [Registry.Load]
// returns. When Err returns an error, the set might lack a Required kind.
// It might also hold a manifest with a problem that only a function of
// WithCheck finds, such as a reference that dangles. Those functions run
// only when the documents and the other checks across the set do not find
// an error. They run before WithWarningsAsErrors turns warnings into
// errors.
func (c *Checked) Set() *Set {
	return c.set
}

// Err returns the problems of the load as a [*LoadError] that holds the
// warnings too, or nil when the load did not find an error. It is the
// error that [Registry.Load] returns.
func (c *Checked) Err() error {
	return c.err
}

// Warnings returns the warnings of the load as a [*LoadError], or nil when
// the load did not find a warning or [Checked.Err] returns an error. The
// Warnings method of the set from [Checked.Set] returns the same.
func (c *Checked) Warnings() error {
	return c.warnings
}

// loader holds the state of one load.
type loader struct {
	reg *Registry
	// The empty Source that a problem of the whole set binds to, named as
	// the heading of the load.
	named *niceyaml.Source
	// The number of calls of each step that ran.
	calls map[StepID]int
	cfg   loadConfig
	// The details of each directory input that did not select a file.
	empty []string
	files []*file
	docs  []*docState
	// The documents of docs, by the index of the file and of the
	// document.
	byPlace map[docPlace]*docState
	rows    []*row
}

// docPlace names a document of a load: the index of its file and its
// index within the file.
//
//nolint:unused // The fields tell the keys of a map apart.
type docPlace struct {
	file, index int
}

// row is one problem of a load, with the key that sorts it in load order.
type row struct {
	err error
	doc *docState
	// The index of the file of the problem, or the number of files for a
	// problem of the whole set.
	file int
	// The index of the document within its file, or -1 for a problem of
	// the whole file.
	index int
	pos   position.Position
	// Whether the problem lacks a position. Such a problem sorts first
	// within its document.
	unplaced bool
	// Whether the problem is a warning that strict mode leaves a warning.
	lenient bool
}

// check reads the files of in and runs the checks of each document.
func (r *Registry) check(ctx context.Context, in Input, cfg loadConfig) (*loader, error) {
	rd, err := readInputs(ctx, in, r.cfg.unmarshalers)
	if err != nil {
		return nil, err
	}

	l := &loader{
		reg:   r,
		cfg:   cfg,
		named: namedSource(in.heading()),
		empty: rd.empty,
		files: rd.files,
	}

	for i, f := range l.files {
		err := ctx.Err()
		if err != nil {
			return nil, err //nolint:wrapcheck // The load ends with the error of its context.
		}

		l.checkFile(ctx, i, f)
	}

	err = ctx.Err()
	if err != nil {
		return nil, err //nolint:wrapcheck // The load ends with the error of its context.
	}

	return l, nil
}

// checkFile runs the checks of each document of f, the file at index i.
func (l *loader) checkFile(ctx context.Context, i int, f *file) {
	if f.err != nil {
		l.add(&row{err: f.err, file: i, index: -1, unplaced: true})

		f.blocked = true

		return
	}

	docs := f.src.Documents()
	typed := slices.ContainsFunc(docs, l.writesTypeField)

	// Each document takes its index among every document of the file, as
	// a binding reports it, which counts the empty documents that docs
	// leaves out.
	for _, doc := range docs {
		d := &docState{l: l, f: f, file: i, index: doc.DocumentIndex(), doc: doc, typedFile: typed}
		d.check(ctx)

		if d.skipped {
			continue
		}

		l.addDoc(d)

		f.docs = append(f.docs, d)

		if !l.cfg.plan {
			d.report(false)
		}
	}

	if l.cfg.plan {
		l.planFile(i, f)
	}
}

// writesTypeField reports whether doc writes a type field.
func (l *loader) writesTypeField(doc *niceyaml.Node) bool {
	if doc.Err() != nil || doc.IsEmpty() {
		return false
	}

	v := readField(doc, l.reg.cfg.versionField)
	if v.found || v.err != nil {
		return true
	}

	if l.reg.cfg.versionOnly {
		return false
	}

	k := readField(doc, l.reg.cfg.kindField)

	return k.found || k.err != nil
}

// add adds the problems of tmpl.err to the report, each as a row with the
// sort key of tmpl.
func (l *loader) add(tmpl *row) {
	for problem := range niceyaml.Problems(tmpl.err) {
		r := *tmpl
		r.err = problem.Err
		r.pos, r.unplaced = problem.Position()
		r.unplaced = !r.unplaced

		if r.doc == nil && r.file == len(l.files) && problem.Source() != nil {
			r.file, r.index = l.locate(problem)
		}

		l.rows = append(l.rows, &r)
	}
}

// locate returns the index of the file and of the document of problem, a
// row bound in a file of the load. For a problem that does not bind in a
// file of the load, it returns the number of files and -1.
func (l *loader) locate(problem niceyaml.ErrorTree) (int, int) {
	src := problem.Source()

	for i, f := range l.files {
		if f.src != nil && f.src == src {
			idx, ok := problem.DocumentIndex()
			if !ok {
				return i, -1
			}

			return i, idx
		}
	}

	return len(l.files), -1
}

// failed reports whether a row of the load is an error.
func (l *loader) failed() bool {
	return slices.ContainsFunc(l.rows, func(r *row) bool { return niceyaml.HasErrors(r.err) })
}

// reject marks each document that an error of rows binds in, so newSet
// leaves it out. The rows must be finished, so that strict mode has turned
// its warnings into errors.
func (l *loader) reject(rows []*row) {
	for _, r := range rows {
		if d := l.docOf(r); d != nil && niceyaml.HasErrors(r.err) {
			d.rejected = true
		}
	}
}

// docOf returns the document of the row r, or nil for a row of a whole
// file or of the whole set.
func (l *loader) docOf(r *row) *docState {
	if r.doc == nil && r.file < len(l.files) && r.index >= 0 {
		return l.docAt(r.file, r.index)
	}

	return r.doc
}

// finishRows returns rows in load order, and wraps each warning in a
// [*Warning]. It applies the filter of [WithWarningFilter] and the strict
// mode of [WithWarningsAsErrors] to the warnings.
func (l *loader) finishRows(rows []*row) []*row {
	slices.SortStableFunc(rows, compareRows)

	out := make([]*row, 0, len(rows))

	for _, r := range rows {
		if !niceyaml.HasErrors(r.err) {
			w := l.warningOf(r)
			wrapped := wrapIn(r.err, w)

			if l.cfg.filter != nil && !l.cfg.filter(w) {
				continue
			}

			r.err = wrapped

			if l.cfg.strict && !r.lenient {
				r.err = niceyaml.AsErrors(r.err)
			}
		}

		out = append(out, r)
	}

	return out
}

// wrapWarning returns the warning of r wrapped in a [*Warning]. It does
// not apply the filter or the strict mode.
func (l *loader) wrapWarning(r *row) error {
	return wrapIn(r.err, l.warningOf(r))
}

// wrapIn returns err, a bound warning, with w around the error that it
// binds.
func wrapIn(err error, w *Warning) error {
	//nolint:wrapcheck // The wrapper is the warning of the row.
	return hook.Rewrap(err, func(inner error) error {
		w.Err = inner

		return w
	})
}

// outcome returns a [*LoadError] that holds the finished rows. It returns
// the LoadError as the error when a row holds an error, and as the
// warnings otherwise. Both results are nil when rows is empty.
func (l *loader) outcome(rows []*row) (error, error) {
	errs := make([]error, 0, len(rows))
	for _, r := range rows {
		errs = append(errs, r.err)
	}

	if slices.ContainsFunc(errs, niceyaml.HasErrors) {
		return newLoadError(l.named, errs), nil
	}

	return nil, newLoadError(l.named, errs)
}

// compareRows orders two rows in load order: by file, then by document,
// then by position, with the rows that lack a position first.
func compareRows(a, b *row) int {
	return cmp.Or(
		cmp.Compare(a.file, b.file),
		cmp.Compare(a.index, b.index),
		compareBool(b.unplaced, a.unplaced),
		cmp.Compare(a.pos.Line, b.pos.Line),
		cmp.Compare(a.pos.Col, b.pos.Col),
	)
}

// compareBool orders false before true.
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}

// warningOf returns the [*Warning] that describes the row r.
func (l *loader) warningOf(r *row) *Warning {
	w := &Warning{Path: paths.Doc()}

	tree := niceyaml.NewErrorTree(r.err)
	if p, ok := tree.Path(); ok {
		w.Path = p
	}

	if src := tree.Source(); src != nil && r.file < len(l.files) {
		w.File = src.Name()
	}

	d := l.docOf(r)
	if d != nil && d.route.kind != nil {
		w.Kind = d.route.kind.kind.name

		v := d.route.version
		if d.rewritten {
			v = d.route.kind.written
		}

		if v != nil && !v.cfg.unversioned {
			w.Version = v.cfg.name
		}
	}

	return w
}

// docAt returns the document at index of the file at file, or nil.
func (l *loader) docAt(file, index int) *docState {
	return l.byPlace[docPlace{file: file, index: index}]
}

// addDoc adds d to the documents of the load.
func (l *loader) addDoc(d *docState) {
	if l.byPlace == nil {
		l.byPlace = make(map[docPlace]*docState)
	}

	l.docs = append(l.docs, d)
	l.byPlace[docPlace{file: d.file, index: d.index}] = d
}

// runChecks runs the checks of [WithCheck] with set, and adds their
// problems to the report. A panic in a check fails the load with a
// [niceyaml.PanicError].
func (l *loader) runChecks(ctx context.Context, set *Set) error {
	warnings := niceyaml.NewWarnings()
	cctx := warnings.Context(ctx)

	for _, check := range l.reg.cfg.checks {
		err := runCheck(cctx, check, set)
		if ctx.Err() != nil {
			return ctx.Err() //nolint:wrapcheck // The load ends with the error of its context.
		}

		l.addChecked(err)
	}

	l.addChecked(warnings.Err())

	return nil
}

// addChecked adds err, the result of a check of [WithCheck], to the
// report. A problem that does not bind in a Source binds to the heading of
// the load.
func (l *loader) addChecked(err error) {
	if err == nil {
		return
	}

	for problem := range niceyaml.Problems(err) {
		bound := problem.Err
		if problem.Source() == nil {
			bound = l.named.Bind(problem.Err)
		}

		l.add(&row{err: bound, file: len(l.files), index: -1, unplaced: true})
	}
}

// runCheck calls check with set and returns its error, or the
// [niceyaml.PanicError] of a panic in it.
func runCheck(ctx context.Context, check func(context.Context, *Set) error, set *Set) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &panicError{in: "check", p: &niceyaml.PanicError{Value: v, Stack: debug.Stack()}}
		}
	}()

	return check(ctx, set)
}

// errHint returns the migrate hint of the registry as an error, or nil
// when the registry does not have one.
func (r *Registry) errHint() error {
	if r.cfg.migrateHint == "" {
		return nil
	}

	return errors.New(r.cfg.migrateHint)
}
