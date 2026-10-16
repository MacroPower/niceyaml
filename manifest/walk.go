package manifest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
	"go.jacobcolvin.com/niceyaml/internal/hook"
)

// defaultInclude holds the patterns that select the files of a directory
// without [WithInclude].
var defaultInclude = []string{"*.yaml", "*.yml"}

// file is one file that a load reads.
type file struct {
	// The problem of the file as a whole, bound, or nil.
	err error
	// The Source of the file, or nil when it did not read.
	src *niceyaml.Source
	// The change of the file that a plan writes, or nil.
	plan *PlanFile
	// The name of the directory input whose walk found the file, or "".
	dir string
	// The name of the file, as messages show it.
	name string
	// The documents of the file that the load checked, in order.
	docs []*docState
	// Where Apply writes the file.
	target target
	// The documents of the file that a plan does not migrate, because they
	// fail to route.
	notMigrated int
	// A problem blocks the rewrite of the file.
	blocked bool
}

// targetKind tells apart where a file came from, which decides whether
// Apply rewrites it.
type targetKind int

// The places a file comes from.
const (
	targetDisk targetKind = iota + 1
	targetStdin
	targetReader
	targetFS
	targetSource
)

// target is where a file came from, and where Apply writes it.
type target struct {
	// The directory input whose walk found the file, or "" for a file that
	// an input names directly.
	root string
	// The slash path of the file below root, or the path of a file that an
	// input names directly.
	path string
	kind targetKind
	// A walk reached the file through a link that leads outside the input
	// directory.
	outside bool
}

// pattern is one pattern of [WithInclude] or [WithExclude], ready to
// match the path of an entry below the input directory.
type pattern struct {
	glob    filepaths.Pattern
	dirOnly bool
}

// compilePatterns returns the patterns of globs. An include pattern picks
// files alone, so one that ends with "/" fails, as does any pattern with a
// ".." element.
func compilePatterns(globs []string, include bool) ([]pattern, error) {
	patterns := make([]pattern, 0, len(globs))

	for _, g := range globs {
		p, err := compilePattern(g, include)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", g, err)
		}

		patterns = append(patterns, p)
	}

	return patterns, nil
}

// compilePattern returns the pattern of glob, which reads as a .gitignore
// line without negation.
func compilePattern(glob string, include bool) (pattern, error) {
	dirOnly := strings.HasSuffix(glob, "/")
	if dirOnly && include {
		return pattern{}, fmt.Errorf("%w: an include pattern picks files, so it cannot end with /",
			filepaths.ErrInvalidPattern)
	}

	g := strings.TrimSuffix(glob, "/")
	anchored := strings.HasPrefix(g, "/")
	g = strings.TrimPrefix(g, "/")
	anchored = anchored || strings.Contains(g, "/")

	if slices.Contains(strings.Split(g, "/"), "..") {
		return pattern{}, fmt.Errorf("%w: a pattern matches below the input directory, so it holds no \"..\" element",
			filepaths.ErrInvalidPattern)
	}

	if !anchored {
		g = "**/" + g
	}

	p, err := filepaths.NewPattern(g)
	if err != nil {
		return pattern{}, err //nolint:wrapcheck // The caller names the pattern.
	}

	return pattern{glob: p, dirOnly: dirOnly}, nil
}

// matchAny reports whether one of patterns matches rel, the slash path of
// an entry below the input directory, which is a directory when dir is
// set.
func matchAny(patterns []pattern, rel string, dir bool) bool {
	return slices.ContainsFunc(patterns, func(p pattern) bool {
		return (dir || !p.dirOnly) && p.glob.Match(rel)
	})
}

// prepared is one input that is not a list, checked before the load reads
// a file.
type prepared struct {
	def     *inputDef
	info    fs.FileInfo
	include []pattern
	exclude []pattern
	globs   []string
}

// prepare checks every input of in before the load reads a file, and
// returns them in order. It fails for a pattern that does not parse and
// for an input path that does not exist.
func prepare(ctx context.Context, in Input, out []*prepared) ([]*prepared, error) {
	d := in.in
	if d == nil {
		return out, nil
	}

	switch d.kind {
	case inputList:
		var err error

		for _, sub := range d.ins {
			out, err = prepare(ctx, sub, out)
			if err != nil {
				return nil, err
			}
		}

		return out, nil

	case inputSources:
		return append(out, &prepared{def: d}), nil

	case inputPath, inputFS, inputStream:
	}

	err := ctx.Err()
	if err != nil {
		return nil, err //nolint:wrapcheck // The load ends with the error of its context.
	}

	p := &prepared{def: d, globs: d.cfg.include}
	if len(p.globs) == 0 {
		p.globs = defaultInclude
	}

	p.include, err = compilePatterns(p.globs, true)
	if err != nil {
		return nil, err
	}

	p.exclude, err = compilePatterns(d.cfg.exclude, false)
	if err != nil {
		return nil, err
	}

	switch d.kind {
	case inputPath:
		p.info, err = os.Stat(d.path)
	case inputFS:
		p.info, err = fs.Stat(d.fsys, cleanFSPath(d.path))
	case inputStream, inputSources, inputList:
	}

	if err != nil {
		return nil, inputError(d, err)
	}

	return append(out, p), nil
}

// inputError returns the error of an input path that the load cannot
// stat, which names the path as the user gave it.
func inputError(d *inputDef, err error) error {
	name := filepath.Clean(d.path)
	if d.kind == inputFS {
		name = cleanFSPath(d.path)
	}

	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		err = pe.Err
	}

	return fmt.Errorf("%s: %w", escape.Control(name), err)
}

// cleanFSPath returns p cleaned as a path of an [fs.FS], where "." names
// the root.
func cleanFSPath(p string) string {
	p = path.Clean(strings.TrimPrefix(p, "/"))
	if p == "" {
		return "."
	}

	return p
}

// reader reads the files of the inputs of a load, each once.
type reader struct {
	ctx context.Context
	// The details of each directory input that selected no file.
	empty []string
	files []*file
	// The unmarshalers of the registry, which every Source the reader
	// builds takes ahead of the options of its input.
	unmarshalers []niceyaml.Unmarshaler
	// The files, Sources, and streams that the load has read, which a later
	// input that names one of them skips.
	disk    []os.FileInfo
	fsFiles []fsKey
	sources []*niceyaml.Source
	streams []*stream
}

// fsKey names one file of an [fs.FS].
type fsKey struct {
	fsys fs.FS
	path string
}

// readInputs reads the files of in, in load order. Each Source it builds
// takes the unmarshalers us.
func readInputs(ctx context.Context, in Input, us []niceyaml.Unmarshaler) (*reader, error) {
	inputs, err := prepare(ctx, in, nil)
	if err != nil {
		return nil, err
	}

	r := &reader{ctx: ctx, unmarshalers: us}

	for _, p := range inputs {
		err := ctx.Err()
		if err != nil {
			return nil, err //nolint:wrapcheck // The load ends with the error of its context.
		}

		switch {
		case p.def.kind == inputSources:
			r.readSources(p.def.srcs)
		case p.def.kind == inputStream:
			r.readStream(p.def.stream, p.def.cfg.sourceOptions)
		case p.def.kind == inputPath && p.info.IsDir():
			r.walkDisk(p)
		case p.def.kind == inputPath:
			name := filepath.Clean(p.def.path)
			r.readDisk(name, p.def.path, p.def.cfg.sourceOptions, target{kind: targetDisk, path: p.def.path})

		case p.info.IsDir():
			r.walkFS(p)
		default:
			name := cleanFSPath(p.def.path)
			r.readFS(p.def.fsys, name, name, p.def.cfg.sourceOptions)
		}
	}

	err = ctx.Err()
	if err != nil {
		return nil, err //nolint:wrapcheck // The load ends with the error of its context.
	}

	return r, nil
}

// fail adds the file name, which did not read, with its problem err.
func (r *reader) fail(name string, err error, t target) {
	r.files = append(r.files, &file{err: namedSource(name).Bind(err), target: t, name: name})
}

// add adds f, a file the load read, with the problem of its encoding. A
// Source whose text is past its byte limit holds no lines, so the load
// reports that limit instead.
func (r *reader) add(f *file) {
	f.name = f.src.Name()

	if bytes, _ := hook.Limits(f.src); bytes == 0 || len(f.src.Text()) <= bytes {
		f.err = checkEncoding(f.src)
	}

	r.files = append(r.files, f)
}

// readSources adds each of srcs that the load has not read.
func (r *reader) readSources(srcs []*niceyaml.Source) {
	for _, src := range srcs {
		if src == nil || slices.Contains(r.sources, src) {
			continue
		}

		r.sources = append(r.sources, src)
		r.add(&file{src: src, target: target{kind: targetSource}})
	}
}

// readStream adds the Source of the text of s, built with opts, unless the
// load has read s.
func (r *reader) readStream(s *stream, opts []niceyaml.SourceOption) {
	if slices.Contains(r.streams, s) {
		return
	}

	r.streams = append(r.streams, s)

	t := target{kind: s.kind}

	text, err := s.read()
	if err != nil {
		r.fail(s.name, readError(err), t)

		return
	}

	src := niceyaml.NewSourceFromString(text, r.sourceOptions(opts, niceyaml.WithName(s.name))...)
	r.add(&file{src: src, target: t})
}

// readDisk adds the file at full, which messages name name, unless the
// load has read it under another path.
func (r *reader) readDisk(name, full string, opts []niceyaml.SourceOption, t target) {
	f, err := os.Open(full) //nolint:gosec // The user names the files to read.
	if err != nil {
		r.fail(name, readError(err), t)

		return
	}

	defer f.Close() //nolint:errcheck // A read-only file has nothing to flush.

	info, err := f.Stat()
	if err == nil {
		if slices.ContainsFunc(r.disk, func(seen os.FileInfo) bool { return os.SameFile(seen, info) }) {
			return
		}

		r.disk = append(r.disk, info)
	}

	abs, err := filepath.Abs(full)
	if err != nil {
		abs = full
	}

	src, err := niceyaml.NewSourceFromReader(f,
		r.sourceOptions(opts, niceyaml.WithName(name), niceyaml.WithFilePath(abs))...)
	if err != nil {
		r.fail(name, readError(err), t)

		return
	}

	r.add(&file{src: src, target: t})
}

// readFS adds the file at p in fsys, which messages name name, unless the
// load has read it.
func (r *reader) readFS(fsys fs.FS, name, p string, opts []niceyaml.SourceOption) {
	key := fsKey{fsys: fsys, path: p}
	if slices.ContainsFunc(r.fsFiles, key.same) {
		return
	}

	r.fsFiles = append(r.fsFiles, key)

	t := target{kind: targetFS}

	f, err := fsys.Open(p)
	if err != nil {
		r.fail(name, readError(err), t)

		return
	}

	defer f.Close() //nolint:errcheck // A read-only file has nothing to flush.

	src, err := niceyaml.NewSourceFromReader(f,
		r.sourceOptions(opts, niceyaml.WithName(name), niceyaml.WithFilePath(p), niceyaml.WithFS(fsys))...)
	if err != nil {
		r.fail(name, readError(err), t)

		return
	}

	r.add(&file{src: src, target: t})
}

// same reports whether k and o name one file: one path in file systems
// that == finds equal, or in one map.
func (k fsKey) same(o fsKey) bool {
	if k.path != o.path {
		return false
	}

	a, b := reflect.ValueOf(k.fsys), reflect.ValueOf(o.fsys)
	if a.Type() != b.Type() {
		return false
	}

	switch {
	case a.Type().Comparable():
		return equalFS(a, b)
	case a.Kind() == reflect.Map:
		return a.UnsafePointer() == b.UnsafePointer()
	default:
		return false
	}
}

// equalFS reports whether == finds the file systems a and b equal. An
// interface inside a comparable type can hold a value that == cannot
// compare, such as a map, and == then panics. Such a pair counts as two
// file systems.
func equalFS(a, b reflect.Value) bool {
	equal := false

	func() {
		defer func() {
			if recover() != nil {
				equal = false
			}
		}()

		equal = a.Equal(b)
	}()

	return equal
}

// sourceOptions returns the options of a Source the reader builds: the
// unmarshalers of the registry, then the options of the user, then own.
// Each later option wins.
func (r *reader) sourceOptions(user []niceyaml.SourceOption, own ...niceyaml.SourceOption) []niceyaml.SourceOption {
	return slices.Concat([]niceyaml.SourceOption{niceyaml.WithUnmarshalers(r.unmarshalers...)}, user, own)
}

// readError returns the problem of a file that did not read: the reason
// that the system gives, such as "permission denied", or the error of a
// file past the byte limit.
func readError(err error) error {
	if errors.Is(err, niceyaml.ErrLimit) {
		return err
	}

	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		err = pe.Err
	}

	return niceyaml.Unchecked(err)
}

// namedSource returns an empty Source that messages name name, where a
// problem with no document of its own binds.
func namedSource(name string) *niceyaml.Source {
	return niceyaml.NewSourceFromString("", niceyaml.WithName(name))
}

// walkEntry is one entry of a directory walk that selected it.
type walkEntry struct {
	// The problem of the entry, unbound, or nil.
	err error
	// The slash path of the entry below the input directory.
	rel string
	// The path the file reads from, which a link resolves.
	from string
	// The entry is a link that leads outside the input directory.
	outside bool
}

// walkDisk adds the files that the walk of the directory input p selects.
func (r *reader) walkDisk(p *prepared) {
	root := filepath.Clean(p.def.path)

	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		r.fail(root, readError(err), target{kind: targetDisk})

		return
	}

	rootInfo, err := os.Stat(resolved)
	if err != nil {
		r.fail(root, readError(err), target{kind: targetDisk})

		return
	}

	w := &diskWalk{p: p, root: root, rootInfo: rootInfo}
	w.list("")

	slices.SortFunc(w.entries, func(a, b walkEntry) int { return strings.Compare(a.rel, b.rel) })

	for _, e := range w.entries {
		if r.ctx.Err() != nil {
			return
		}

		name := filepath.Join(root, filepath.FromSlash(e.rel))
		t := target{kind: targetDisk, outside: e.outside, root: root, path: e.rel}

		if e.err != nil {
			r.fail(name, e.err, t)

			continue
		}

		before := len(r.files)
		r.readDisk(name, e.from, p.def.cfg.sourceOptions, t)

		for _, f := range r.files[before:] {
			f.dir = root
		}
	}

	if w.selected == 0 {
		r.empty = append(r.empty, fmt.Sprintf("%s holds no file that matches %s", escape.Control(root),
			joinOr(p.globs)))
	}
}

// diskWalk is the walk of one directory input on disk.
type diskWalk struct {
	p        *prepared
	rootInfo os.FileInfo
	root     string
	entries  []walkEntry
	selected int
}

// list adds the entries of the directory at rel, a slash path below the
// input directory, and of its subdirectories under [WithRecursive].
func (w *diskWalk) list(rel string) {
	dir := filepath.Join(w.root, filepath.FromSlash(rel))

	entries, err := os.ReadDir(dir)
	if err != nil {
		w.entries = append(w.entries, walkEntry{rel: rel, err: readError(err)})

		return
	}

	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}

		sub := path.Join(rel, name)
		full := filepath.Join(dir, name)

		switch typ := e.Type(); {
		case typ.IsDir():
			if w.p.def.cfg.recursive && !matchAny(w.p.exclude, sub, true) {
				w.list(sub)
			}

		case typ&fs.ModeSymlink != 0:
			if w.selects(sub) {
				w.link(sub, full)
			}

		case typ.IsRegular():
			if w.selects(sub) {
				w.selected++
				w.entries = append(w.entries, walkEntry{rel: sub, from: full})
			}
		}
	}
}

// selects reports whether the walk selects the file at rel by its path.
func (w *diskWalk) selects(rel string) bool {
	return !matchAny(w.p.exclude, rel, false) && matchAny(w.p.include, rel, false)
}

// link adds the entry of the link at rel, whose path on disk is full, when
// it leads to a regular file.
func (w *diskWalk) link(rel, full string) {
	info, err := os.Stat(full)
	if errors.Is(err, fs.ErrNotExist) {
		w.selected++
		w.entries = append(w.entries, walkEntry{rel: rel, err: danglingProblem(linkTarget(full), err)})

		return
	}

	if err != nil {
		w.selected++
		w.entries = append(w.entries, walkEntry{rel: rel, err: readError(err)})

		return
	}

	if !info.Mode().IsRegular() {
		return
	}

	w.selected++

	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		w.entries = append(w.entries, walkEntry{rel: rel, err: readError(err)})

		return
	}

	below, inside := w.below(resolved)

	switch {
	case !inside && !w.p.def.cfg.outsideLinks:
		w.entries = append(w.entries, walkEntry{rel: rel, err: outsideProblem(linkTarget(full), w.root)})
	case inside && !matchAny(w.p.include, below, false):
		w.entries = append(w.entries, walkEntry{rel: rel, err: unselectedProblem(linkTarget(full), w.p.globs)})
	default:
		w.entries = append(w.entries, walkEntry{rel: rel, from: full, outside: !inside})
	}
}

// below returns the slash path of resolved, a file with every link
// resolved, below the directory the input leads to, and whether one of the
// directories above the file is that directory.
func (w *diskWalk) below(resolved string) (string, bool) {
	for dir := filepath.Dir(resolved); ; {
		info, err := os.Stat(dir)
		if err == nil && os.SameFile(info, w.rootInfo) {
			rel, err := filepath.Rel(dir, resolved)
			if err != nil {
				return "", false
			}

			return filepath.ToSlash(rel), true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}

		dir = parent
	}
}

// linkTarget returns the target of the link at full as the link writes it.
func linkTarget(full string) string {
	t, err := os.Readlink(full)
	if err != nil {
		return "?"
	}

	return t
}

// danglingProblem returns the problem of a link whose target, as the
// link writes it, is gone. It matches err, which matches [fs.ErrNotExist].
func danglingProblem(written string, err error) error {
	return niceyaml.Invalid(&problemError{
		msg:   fmt.Sprintf("links to %s, which does not exist", escape.Control(written)),
		marks: []error{err},
	})
}

// outsideProblem returns the problem of a link whose target, as the link
// writes it, leads outside root.
func outsideProblem(written, root string) error {
	root = escape.Control(root)

	return newProblem(ErrOutsideLink,
		fmt.Sprintf("links to %s, which leads outside %s", escape.Control(written), root),
		niceyaml.WithDetails(fmt.Errorf("copy the file into %s, or make %s itself the link", root, root)))
}

// unselectedProblem returns the problem of a link inside the input
// directory whose target, as the link writes it, the include patterns do
// not select.
func unselectedProblem(written string, globs []string) error {
	return newProblem(ErrOutsideLink,
		fmt.Sprintf("links to %s, which does not match %s", escape.Control(written), joinOr(globs)))
}

// joinOr returns items as a list in prose, as in "*.yaml or *.yml".
func joinOr(items []string) string {
	return joinList(items, "or")
}
