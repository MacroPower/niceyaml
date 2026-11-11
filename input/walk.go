package input

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
	"go.jacobcolvin.com/niceyaml/internal/inputhook"
	"go.jacobcolvin.com/niceyaml/internal/rootpath"
)

var (
	// The patterns that select the files of a directory without
	// [WithInclude].
	defaultInclude = []string{"*.yaml", "*.yml"}

	// The reason of an entry that changed during each try of a directory
	// walk to read it.
	errChangedInWalk = errors.New("changed during the read")

	// The reason of a path that runs through a link at a step of a
	// directory walk that reads no link.
	errLinkInWalk = errors.New("runs through a link")
)

// pattern is one pattern of [WithInclude] or [WithExclude], ready to
// match the path of an entry below the input directory.
type pattern struct {
	glob    filepaths.Pattern
	dirOnly bool
}

// compilePatterns returns the patterns of globs. An include pattern picks
// only files, so an include pattern that ends with "/" fails. Any pattern
// with a ".." element fails too. A walk without [WithHidden] skips each
// entry whose name starts with ".". So an include pattern in which
// [filepaths.Pattern.DotElement] finds an element fails unless hidden is
// set. That pattern fails even when it matches the file that a link leads
// to.
func compilePatterns(globs []string, include, hidden bool) ([]pattern, error) {
	patterns := make([]pattern, 0, len(globs))

	for _, g := range globs {
		p, err := compilePattern(g, include, hidden)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", g, err)
		}

		patterns = append(patterns, p)
	}

	return patterns, nil
}

// compilePattern returns the pattern of glob, which reads as a .gitignore
// line without negation.
func compilePattern(glob string, include, hidden bool) (pattern, error) {
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

	if include && !hidden {
		if elem, ok := p.DotElement(); ok {
			return pattern{}, fmt.Errorf(
				"%w: a walk skips entries whose names start with \".\", and %q matches only those names",
				filepaths.ErrInvalidPattern, elem,
			)
		}
	}

	return pattern{glob: p, dirOnly: dirOnly}, nil
}

// matchAny reports whether one of patterns matches rel, the slash path of
// an entry below the input directory. The entry is a directory when dir is
// set.
func matchAny(patterns []pattern, rel string, dir bool) bool {
	return slices.ContainsFunc(patterns, func(p pattern) bool {
		return (dir || !p.dirOnly) && p.glob.Match(rel)
	})
}

// prepared is one input that is not a list, checked before the read reads
// a file.
type prepared struct {
	def     *inputDef
	info    fs.FileInfo
	include []pattern
	exclude []pattern
	globs   []string
}

// prepare checks every input of in before the read reads a file, and
// returns them in order. It fails for a pattern that the walk rejects and
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
		return nil, err //nolint:wrapcheck // The read ends with the error of its context.
	}

	p := &prepared{def: d, globs: d.cfg.include}
	if len(p.globs) == 0 {
		p.globs = defaultInclude
	}

	p.include, err = compilePatterns(p.globs, true, d.cfg.hidden)
	if err != nil {
		return nil, err
	}

	p.exclude, err = compilePatterns(d.cfg.exclude, false, d.cfg.hidden)
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

// inputError returns the error of an input path that the read cannot
// stat. The error names the path as the user gave it, cleaned as
// [filepaths.Clean] cleans it.
func inputError(d *inputDef, err error) error {
	name := filepaths.Clean(d.path)
	if d.kind == inputFS {
		name = cleanFSPath(d.path)
	}

	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		err = pe.Err
	}

	return fmt.Errorf("%s: %w", escape.Line(name), err)
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

// reader reads the files of the inputs of a read, each once.
type reader struct {
	ctx context.Context
	// The details of each directory input that did not select a file.
	empty []string
	files []*inputhook.File
	// The unmarshalers that every Source that the reader builds takes ahead
	// of the options of its input.
	unmarshalers []niceyaml.Unmarshaler
	// The files, Sources, and streams that the read has taken, which a
	// later input that names one of them skips.
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

// readInputs reads the files of in, in read order. Each Source that it
// builds takes the unmarshalers in us.
func readInputs(ctx context.Context, in Input, us []niceyaml.Unmarshaler) (*reader, error) {
	inputs, err := prepare(ctx, in, nil)
	if err != nil {
		return nil, err
	}

	r := &reader{ctx: ctx, unmarshalers: us}

	for _, p := range inputs {
		err := ctx.Err()
		if err != nil {
			return nil, err //nolint:wrapcheck // The read ends with the error of its context.
		}

		switch {
		case p.def.kind == inputSources:
			r.readSources(p.def.srcs)
		case p.def.kind == inputStream:
			r.readStream(p.def.stream, p.def.cfg.sourceOptions)
		case p.def.kind == inputPath && p.info.IsDir():
			r.walkDisk(p)
		case p.def.kind == inputPath:
			name := filepaths.Clean(p.def.path)
			t := inputhook.Target{Kind: inputhook.Disk, Path: p.def.path}
			r.readDisk(name, p.def.path, p.def.cfg.sourceOptions, t)

		case p.info.IsDir():
			r.walkFS(p)
		default:
			name := cleanFSPath(p.def.path)
			r.readFS(p.def.fsys, name, name, p.def.cfg.sourceOptions)
		}
	}

	err = ctx.Err()
	if err != nil {
		return nil, err //nolint:wrapcheck // The read ends with the error of its context.
	}

	return r, nil
}

// fail adds the file name, whose read failed, with its problem err.
func (r *reader) fail(name string, err error, t inputhook.Target) {
	r.files = append(r.files, &inputhook.File{Err: namedSource(name).Bind(err), Target: t, Name: name})
}

// add adds src, the Source of a file that the read took in.
func (r *reader) add(src *niceyaml.Source, t inputhook.Target) {
	r.files = append(r.files, &inputhook.File{Source: src, Target: t, Name: src.Name()})
}

// readSources adds each of srcs that the read has not taken.
func (r *reader) readSources(srcs []*niceyaml.Source) {
	for _, src := range srcs {
		if src == nil || slices.Contains(r.sources, src) {
			continue
		}

		r.sources = append(r.sources, src)
		r.add(src, inputhook.Target{Kind: inputhook.Source})
	}
}

// readStream adds the Source of the text of s, built with opts, unless the
// read has taken s.
func (r *reader) readStream(s *stream, opts []niceyaml.SourceOption) {
	if slices.Contains(r.streams, s) {
		return
	}

	r.streams = append(r.streams, s)

	t := inputhook.Target{Kind: s.kind}

	text, err := s.read()
	if err != nil {
		r.fail(s.name, readError(err), t)

		return
	}

	src := niceyaml.NewSourceFromString(text, r.sourceOptions(opts, niceyaml.WithName(s.name))...)
	r.add(src, t)
}

// readDisk adds the file at full, which messages name name, unless the
// read has taken it under another path.
func (r *reader) readDisk(name, full string, opts []niceyaml.SourceOption, t inputhook.Target) {
	f, err := os.Open(full) //nolint:gosec // The user names the files to read.
	if err != nil {
		r.fail(name, readError(err), t)

		return
	}

	r.readFile(f, name, full, opts, t)
}

// readFile adds the open file f unless the read has taken it under another
// path, and closes f. Messages name the file name, and full is its path
// on disk.
func (r *reader) readFile(f *os.File, name, full string, opts []niceyaml.SourceOption, t inputhook.Target) {
	defer f.Close() //nolint:errcheck // A read-only file has nothing to flush.

	info, err := f.Stat()
	if err == nil {
		if slices.ContainsFunc(r.disk, func(seen os.FileInfo) bool { return os.SameFile(seen, info) }) {
			return
		}

		r.disk = append(r.disk, info)
	}

	abs, err := filepaths.Abs(full)
	if err != nil {
		abs = full
	}

	src, err := niceyaml.NewSourceFromReader(f,
		r.sourceOptions(opts, niceyaml.WithName(name), niceyaml.WithFilePath(abs))...)
	if err != nil {
		r.fail(name, readError(err), t)

		return
	}

	r.add(src, t)
}

// readFS adds the file at p in fsys, which messages name name, unless the
// read has taken it.
func (r *reader) readFS(fsys fs.FS, name, p string, opts []niceyaml.SourceOption) {
	key := fsKey{fsys: fsys, path: p}
	if slices.ContainsFunc(r.fsFiles, key.same) {
		return
	}

	r.fsFiles = append(r.fsFiles, key)

	t := inputhook.Target{Kind: inputhook.FS}

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

	r.add(src, t)
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

// sourceOptions returns the options of a Source that the reader builds:
// the unmarshalers of the reader, then the options of the user, then
// own. Each later option wins.
func (r *reader) sourceOptions(user []niceyaml.SourceOption, own ...niceyaml.SourceOption) []niceyaml.SourceOption {
	return slices.Concat([]niceyaml.SourceOption{niceyaml.WithUnmarshalers(r.unmarshalers...)}, user, own)
}

// readError returns the problem of a file whose read failed. The problem
// is the reason that the system gives, such as "permission denied", or the
// error of a Source constructor, which the constructor bound to the name
// and the file path of the file already.
func readError(err error) error {
	bound, ok := errors.AsType[*niceyaml.SourceError](err)
	if ok && bound != nil {
		return err
	}

	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		err = pe.Err
	}

	return niceyaml.Unchecked(err)
}

// namedSource returns an empty Source that messages name name, where a
// problem without a document of its own binds.
func namedSource(name string) *niceyaml.Source {
	return niceyaml.NewSourceFromString("", niceyaml.WithName(name))
}

// walkEntry is one entry that a directory walk selected.
type walkEntry struct {
	// The problem of the entry, unbound, or nil.
	err error
	// The slash path of the entry below the input directory.
	rel string
	// The path in the [fs.FS] that the file reads from, which a link
	// resolves. The walk of a directory on disk resolves the path of each
	// entry when it opens the file, so it leaves the field empty.
	from string
}

// walkTries is the number of times that a directory walk resolves and
// opens one entry at most. The walk tries again when the entry changes
// between the resolve and the open.
const walkTries = 4

// walkDisk adds the files that the walk of the directory input p selects.
// The walk lists each directory and opens each file through an [os.Root]
// of the input directory, or through a Root of a directory below it that
// the walk opens from that Root. Only [WithOutsideLinks] thus lets an
// entry lead the walk outside the input directory, even when the entry
// changes during the walk.
func (r *reader) walkDisk(p *prepared) {
	// The walk reads the directory that the stat of prepare found, so a
	// ".." after a link steps up from the target of the link.
	dir := filepaths.Clean(p.def.path)

	w, err := newDiskWalk(p, dir)
	if err != nil {
		r.fail(dir, readError(err), inputhook.Target{Kind: inputhook.Disk})

		return
	}

	defer w.close()

	w.list(w.root, "")

	slices.SortFunc(w.entries, func(a, b walkEntry) int { return strings.Compare(a.rel, b.rel) })

	for _, e := range w.entries {
		if r.ctx.Err() != nil {
			return
		}

		name := filepath.Join(dir, filepath.FromSlash(e.rel))
		t := inputhook.Target{Kind: inputhook.Disk, Root: dir, Path: e.rel}

		if e.err != nil {
			r.fail(name, e.err, t)

			continue
		}

		o := w.open(e.rel)
		t.Outside = o.outside

		switch {
		case o.err != nil:
			w.selected++

			r.fail(name, o.err, t)

		case o.f != nil:
			w.selected++

			before := len(r.files)
			r.readFile(o.f, name, name, p.def.cfg.sourceOptions, t)

			for _, f := range r.files[before:] {
				f.Dir = dir
			}
		}
	}

	if w.selected == 0 {
		r.empty = append(r.empty, fmt.Sprintf("%s holds no file that matches %s", escape.Line(dir),
			joinOr(p.globs)))
	}
}

// diskWalk is the walk of one directory input on disk.
type diskWalk struct {
	p *prepared
	// The input directory, which every listing and every read inside the
	// directory goes through.
	root *os.Root
	// The reason that the Root gives when it refuses a path that leads
	// outside the input directory.
	escapes error
	// The path of the input directory, as messages show it.
	dir string
	// The absolute path of the input directory with every link resolved.
	abs     string
	entries []walkEntry
	// The directories on the path that [diskWalk.reach] reached last, from
	// the input directory down.
	chain []chainDir
	// The number of entries that led to a regular file or to a problem.
	selected int
}

// chainDir is one directory of [diskWalk.chain].
type chainDir struct {
	// The Root of the directory, which [diskWalk.enter] opened.
	root *os.Root
	// The name of the directory in the directory above it.
	name string
}

// newDiskWalk returns the walk of the directory input p, whose cleaned
// path is dir. The caller closes the root of the walk.
func newDiskWalk(p *prepared, dir string) (*diskWalk, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err //nolint:wrapcheck // The caller names the problem.
	}

	w := &diskWalk{p: p, root: root, dir: dir, escapes: escapeReason(root)}

	// A leading ".." in dir climbs from the directory that a symlinked
	// working directory leads to, as the Root that the walk reads climbs.
	w.abs, err = filepaths.Abs(dir)
	if err == nil {
		w.abs, err = filepath.EvalSymlinks(w.abs)
	}

	if err != nil {
		_ = root.Close() //nolint:errcheck // The problem of the path wins.

		return nil, err //nolint:wrapcheck // The caller names the problem.
	}

	return w, nil
}

// close closes the Roots of the walk.
func (w *diskWalk) close() {
	w.trim(0)

	_ = w.root.Close() //nolint:errcheck // A directory has nothing to flush.
}

// escapeReason returns the reason that root gives when it refuses a path
// that leads outside it. No exported error matches that reason, so
// escapeReason asks root for the directory above it, which root refuses
// for the same reason.
func escapeReason(root *os.Root) error {
	_, err := root.Lstat("..")
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return pe.Err
	}

	return err //nolint:wrapcheck // The walk compares the reason and shows it to no one.
}

// list adds the entries of the directory at rel, a slash path below the
// input directory, and of its subdirectories under [WithRecursive]. The
// directory at rel is the last element of rel in parent, or parent itself
// when rel is "". The walk lists each directory once, so list holds a Root
// for each directory above the one that it lists, and no more.
//
// It skips each entry whose name starts with "." unless [WithHidden] is
// set. It adds each link and each regular file that the patterns select,
// and [diskWalk.open] later decides what the entry leads to.
func (w *diskWalk) list(parent *os.Root, rel string) {
	dir, entries, err := w.readDir(parent, path.Base(rel))
	if err != nil {
		w.entries = append(w.entries, walkEntry{rel: rel, err: readError(err)})

		return
	}

	if dir == nil {
		return
	}

	defer dir.Close() //nolint:errcheck // A directory has nothing to flush.

	for _, e := range entries {
		name := e.Name()
		if !w.p.def.cfg.hidden && strings.HasPrefix(name, ".") {
			continue
		}

		sub := path.Join(rel, name)

		switch typ := e.Type(); {
		case typ.IsDir():
			if w.p.def.cfg.recursive && !matchAny(w.p.exclude, sub, true) {
				w.list(dir, sub)
			}

		case typ&fs.ModeSymlink != 0, typ.IsRegular():
			if w.selects(sub) {
				w.entries = append(w.entries, walkEntry{rel: sub})
			}
		}
	}
}

// selects reports whether the walk selects the file at rel by its path.
func (w *diskWalk) selects(rel string) bool {
	return !matchAny(w.p.exclude, rel, false) && matchAny(w.p.include, rel, false)
}

// readDir returns a Root of the directory at name in parent, and the
// entries of that directory. The caller closes the Root.
//
// A directory can become a link after the listing that named it. The walk
// does not enter a link to a directory. So readDir returns no Root and no
// entry when name is a link.
//
// Another directory can also replace the directory between two steps of
// the system. A path that changes so can fail with a reason that names
// no link, so readDir tries again after any other problem. It returns
// the problem of the last try. A directory that changed in every try
// gets [errChangedInWalk].
func (w *diskWalk) readDir(parent *os.Root, name string) (*os.Root, []fs.DirEntry, error) {
	var err error

	for range walkTries {
		var (
			dir     *os.Root
			entries []fs.DirEntry
		)

		dir, entries, err = w.listDir(parent, name)

		switch {
		case err == nil:
			return dir, entries, nil
		case errors.Is(err, w.escapes), errors.Is(err, errLinkInWalk):
			return nil, nil, nil
		}
	}

	return nil, nil, err
}

// listDir returns a Root of the directory at name in parent, and the
// entries of that directory, in one try of [diskWalk.readDir]. It fails as
// [diskWalk.enter] does.
func (w *diskWalk) listDir(parent *os.Root, name string) (*os.Root, []fs.DirEntry, error) {
	dir, err := w.enter(parent, name)
	if err != nil {
		return nil, nil, err
	}

	entries, err := readRoot(dir)
	if err != nil {
		_ = dir.Close() //nolint:errcheck // The problem of the listing wins.

		return nil, nil, err
	}

	return dir, entries, nil
}

// readRoot returns the entries of the directory of root.
func readRoot(root *os.Root) ([]fs.DirEntry, error) {
	d, err := root.Open(".")
	if err != nil {
		return nil, err //nolint:wrapcheck // The caller names the problem.
	}

	defer d.Close() //nolint:errcheck // A directory has nothing to flush.

	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, err //nolint:wrapcheck // The caller names the problem.
	}

	return entries, nil
}

// enter returns a Root of the directory at name in parent, where name is
// one element or ".". The caller closes the Root. It returns
// [errLinkInWalk] when name is a link. It returns [errChangedInWalk] when
// the directory that it opens is not the one that name named a step
// before. The Root thus holds a directory that name named through no
// link.
func (w *diskWalk) enter(parent *os.Root, name string) (*os.Root, error) {
	info, err := parent.Lstat(name)

	switch {
	case err != nil:
		return nil, err //nolint:wrapcheck // The caller names the problem.
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, errLinkInWalk
	}

	dir, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err //nolint:wrapcheck // The caller names the problem.
	}

	opened, err := dir.Stat(".")
	if err == nil && !os.SameFile(info, opened) {
		err = errChangedInWalk
	}

	if err != nil {
		_ = dir.Close() //nolint:errcheck // A directory has nothing to flush.

		return nil, err //nolint:wrapcheck // The caller names the problem.
	}

	return dir, nil
}

// opened is what [diskWalk.open] found for one entry.
type opened struct {
	// The problem of the entry, unbound, or nil.
	err error
	// The open file. It is nil for an entry with a problem and for an entry
	// that the walk skips.
	f *os.File
	// Whether the file lies outside the input directory.
	outside bool
}

// open opens the file that the entry at rel, a slash path below the input
// directory, leads to. It returns no file and no problem for an entry
// that leads to anything but a regular file, which the walk skips.
//
// The walk opens entries sorted by path, so the entries of one directory
// come one after another. The walk thus keeps a Root of each directory on
// the path that it reached last, in [diskWalk.chain]. Each step of open
// reads one element of the path through the Root of its directory. So the
// cost of an entry does not grow with its depth.
//
// The entry and each directory above it can change after the listing that
// named them, and between two steps of open. So open checks each step on
// the Root of its directory. A directory on the path can become a link or
// another file. The entry itself can be a link. In each of those cases,
// open resolves the entry from the input directory, as
// [diskWalk.resolveOpen] describes.
func (w *diskWalk) open(rel string) opened {
	dirRel, base := path.Split(rel)

	dir := w.reach(dirRel)
	if dir != nil {
		o, ok := openIn(dir, base)
		if ok {
			return o
		}
	}

	return w.resolveOpen(rel)
}

// reach returns the Root of the directory at dir, a slash path below the
// input directory that is "" or ends with "/". It moves
// [diskWalk.chain] to that directory. It enters each directory that the
// chain does not hold as [diskWalk.enter] does. It returns nil when it
// cannot enter a directory on the path. The chain then ends at the
// directory above that one.
func (w *diskWalk) reach(dir string) *os.Root {
	var names []string

	if dir != "" {
		names = strings.Split(strings.TrimSuffix(dir, "/"), "/")
	}

	keep := 0
	for keep < len(names) && keep < len(w.chain) && w.chain[keep].name == names[keep] {
		keep++
	}

	w.trim(keep)

	for _, name := range names[keep:] {
		parent := w.root
		if len(w.chain) > 0 {
			parent = w.chain[len(w.chain)-1].root
		}

		root, err := w.enter(parent, name)
		if err != nil {
			return nil
		}

		w.chain = append(w.chain, chainDir{root: root, name: name})
	}

	if len(w.chain) == 0 {
		return w.root
	}

	return w.chain[len(w.chain)-1].root
}

// trim closes the Roots of [diskWalk.chain] past the first n.
func (w *diskWalk) trim(n int) {
	for _, d := range w.chain[n:] {
		_ = d.root.Close() //nolint:errcheck // A directory has nothing to flush.
	}

	w.chain = w.chain[:n]
}

// openIn opens the regular file at name, one element, in dir. It reads
// the element and opens it through dir. Then it checks that the open file
// is the file that the element named, so the element was no link. It
// returns no file and no problem for an element that is neither a link
// nor a regular file, which the walk skips.
//
// It reports false for a link, for an element that changed between the
// steps, and for any problem. Then [diskWalk.open] resolves the entry from
// the input directory and words the problem.
func openIn(dir *os.Root, name string) (opened, bool) {
	info, err := dir.Lstat(name)

	switch {
	case err != nil, info.Mode()&fs.ModeSymlink != 0:
		return opened{}, false
	case !info.Mode().IsRegular():
		return opened{}, true
	}

	f, err := dir.Open(name)
	if err != nil {
		return opened{}, false
	}

	got, err := f.Stat()
	if err != nil || !os.SameFile(info, got) {
		_ = f.Close() //nolint:errcheck // A file opened to read has nothing to flush.

		return opened{}, false
	}

	return opened{f: f}, true
}

// resolveOpen opens the file that the entry at rel, a slash path below the
// input directory, leads to. It resolves the entry from the input
// directory, as [diskWalk.try] describes.
//
// An entry can change after the listing that named it, and between two
// steps of resolveOpen. So resolveOpen treats every entry as a possible
// link, whatever the listing said. It tries again when a step finds a
// path that an earlier step did not.
//
// A path that changes in every try gets the problem of the last try. When
// that problem is the refusal of the Root, the path led outside the
// directory, and resolveOpen treats the entry as a link that leads
// outside.
func (w *diskWalk) resolveOpen(rel string) opened {
	name := filepath.FromSlash(rel)

	var o opened

	for range walkTries {
		var again bool

		o, again = w.try(name)
		if !again {
			return o
		}
	}

	if errors.Is(o.err, w.escapes) {
		return w.openOutside(name, filepath.Join(w.dir, name))
	}

	return o
}

// try opens the file that the entry at name, a path below the input
// directory, leads to. It also reports whether the path changed during
// the try, so that [diskWalk.resolveOpen] tries again.
//
// The system follows the entry first. That step reads no file. It words
// the problem of an entry that the system cannot follow, and it skips an
// entry that leads to anything but a regular file.
//
// Then try resolves the links of the entry to a path below the input
// directory, checks that path against the include patterns, and opens it
// through the Root. A chain of links can leave the directory and lead
// back in, and try reads the file of such a chain through the Root too.
// The Root refuses a path that leads outside the directory, so a change
// between the resolve and the open cannot make try read a file outside.
// After the open, try confirms that the resolved path still names the
// open file through no link. The file that try returns is thus the file
// that the include patterns checked. Only [diskWalk.openOutside] reads a
// file outside the directory.
func (w *diskWalk) try(name string) (opened, bool) {
	full := filepath.Join(w.dir, name)

	info, err := os.Stat(full)

	switch {
	case err != nil:
		return opened{err: w.followProblem(name, err)}, false
	case !info.Mode().IsRegular():
		return opened{}, false
	}

	resolved, err := rootpath.Resolve(w.root, w.abs, name)

	switch {
	case errors.Is(err, rootpath.ErrOutside):
		return w.openOutside(name, full), false

	case err != nil:
		// The system followed the entry, so the entry changed after that.
		return opened{err: readError(err)}, true

	case resolved != name && !matchAny(w.p.include, filepath.ToSlash(resolved), false):
		written := w.written(name)

		return opened{err: unselectedProblem(written, w.p.globs)}, false
	}

	f, err := w.root.Open(resolved)
	if err != nil {
		// The system refuses the read, or the path changed after the
		// resolve. The next try names the link of a changed path that the
		// Root refuses.
		return opened{err: readError(err)}, true
	}

	info, err = f.Stat()

	switch {
	case err != nil:
		_ = f.Close() //nolint:errcheck // The problem of the stat wins.

		return opened{err: readError(err)}, true

	case !info.Mode().IsRegular():
		_ = f.Close() //nolint:errcheck // A file opened to read has nothing to flush.

		return opened{}, false

	case !w.identify(resolved, info):
		_ = f.Close() //nolint:errcheck // A file opened to read has nothing to flush.

		return opened{err: readError(errChangedInWalk)}, true
	}

	return opened{f: f}, false
}

// followProblem returns the problem of the entry at name, a path below
// the input directory, that the system cannot follow for the reason err.
func (w *diskWalk) followProblem(name string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		written, linkErr := w.root.Readlink(name)
		if linkErr == nil {
			return danglingProblem(written, err)
		}
	}

	return readError(err)
}

// openOutside opens the regular file at full, which the entry at name
// leads to outside the input directory. The walk reads the file only
// under [WithOutsideLinks]. The Root cannot reach the file, so
// openOutside follows the path as the system does.
func (w *diskWalk) openOutside(name, full string) opened {
	if !w.p.def.cfg.outsideLinks {
		written := w.written(name)

		return opened{err: outsideProblem(written, w.dir)}
	}

	f, err := os.Open(full) //nolint:gosec // WithOutsideLinks lets the links of the directory name the file.
	if err != nil {
		return opened{err: readError(err), outside: true}
	}

	info, err := f.Stat()

	switch {
	case err != nil:
		_ = f.Close() //nolint:errcheck // The problem of the stat wins.

		return opened{err: readError(err), outside: true}

	case !info.Mode().IsRegular():
		_ = f.Close() //nolint:errcheck // A file opened to read has nothing to flush.

		return opened{outside: true}
	}

	return opened{f: f, outside: true}
}

// identify reports whether name, a path below the input directory, names
// the file that info describes through no link. It enters each directory
// on the path again, as [diskWalk.reach] does, so it checks each one
// through the Root of the directory above. A check of the whole path by
// its text would follow a directory that became a link after the check of
// that directory. The Roots that the walk holds can name directories that
// have moved since, so identify does not use them.
func (w *diskWalk) identify(name string, info fs.FileInfo) bool {
	dirRel, base := path.Split(filepath.ToSlash(name))

	w.trim(0)

	dir := w.reach(dirRel)
	if dir == nil {
		return false
	}

	at, err := dir.Lstat(base)

	// A link at base never names the file that info describes.
	return err == nil && os.SameFile(at, info)
}

// written returns the target of the first link on the path name, a path
// below the input directory, as the link writes it. The target reads "?"
// when the path holds no link.
//
// The entry itself is the link in a directory that stays as the walk
// listed it. A directory above the entry is a link only when it changed
// after the walk listed it.
func (w *diskWalk) written(name string) string {
	for i := 1; i <= len(name); i++ {
		if i < len(name) && !os.IsPathSeparator(name[i]) {
			continue
		}

		t, err := w.root.Readlink(name[:i])
		if err == nil {
			return t
		}
	}

	return "?"
}

// danglingProblem returns the problem of a link whose target, as the
// link writes it, does not exist. It matches err, which matches
// [fs.ErrNotExist].
func danglingProblem(written string, err error) error {
	return niceyaml.Invalid(&problemError{
		msg:   fmt.Sprintf("links to %s, which does not exist", escape.Line(written)),
		marks: []error{err},
	})
}

// outsideProblem returns the problem of a link whose target, as the link
// writes it, leads outside root.
func outsideProblem(written, root string) error {
	root = escape.Line(root)

	return newProblem(ErrOutsideLink,
		fmt.Sprintf("links to %s, which leads outside %s", escape.Line(written), root),
		niceyaml.WithDetails(fmt.Errorf("copy the file into %s, or make %s itself the link", root, root)))
}

// unselectedProblem returns the problem of a link inside the input
// directory whose target, as the link writes it, the include patterns do
// not select.
func unselectedProblem(written string, globs []string) error {
	return newProblem(ErrOutsideLink,
		fmt.Sprintf("links to %s, which does not match %s", escape.Line(written), joinOr(globs)))
}
