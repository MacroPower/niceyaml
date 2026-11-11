package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/escape"
)

// maxLinkHops is the number of links that a walk of an [fs.FS] follows
// from one entry before it stops, as an operating system stops at a loop
// of links.
const maxLinkHops = 40

// walkFS adds the files that the walk of the directory input p, an [FS]
// input, selects.
func (r *reader) walkFS(p *prepared) {
	root := cleanFSPath(p.def.path)
	w := &fsWalk{p: p, fsys: p.def.fsys, root: root}
	w.list(".")

	slices.SortFunc(w.entries, func(a, b walkEntry) int { return strings.Compare(a.rel, b.rel) })

	for _, e := range w.entries {
		if r.ctx.Err() != nil {
			return
		}

		name := path.Join(root, e.rel)
		if e.err != nil {
			r.fail(name, e.err, target{kind: targetFS})

			continue
		}

		before := len(r.files)
		r.readFS(p.def.fsys, name, e.from, p.def.cfg.sourceOptions)

		for _, f := range r.files[before:] {
			f.dir = root
		}
	}

	if w.selected == 0 {
		r.empty = append(r.empty, fmt.Sprintf("%s holds no file that matches %s", escape.Line(root),
			joinOr(p.globs)))
	}
}

// fsWalk is the walk of one directory input in an [fs.FS].
type fsWalk struct {
	fsys fs.FS
	p    *prepared
	root string
	// The input directory with its own links resolved. The walk resolves
	// it when the first link needs it.
	realRoot string
	entries  []walkEntry
	selected int
}

// list adds the entries of the directory at rel, a slash path below the
// input directory, and of its subdirectories under [WithRecursive]. It
// skips each entry whose name starts with "." unless [WithHidden] is set.
func (w *fsWalk) list(rel string) {
	dir := path.Join(w.root, rel)

	entries, err := fs.ReadDir(w.fsys, dir)
	if err != nil {
		w.entries = append(w.entries, walkEntry{rel: rel, err: readError(err)})

		return
	}

	for _, e := range entries {
		name := e.Name()
		if !w.p.def.cfg.hidden && strings.HasPrefix(name, ".") {
			continue
		}

		sub := path.Join(rel, name)

		switch typ := e.Type(); {
		case typ.IsDir():
			if w.p.def.cfg.recursive && !matchAny(w.p.exclude, sub, true) {
				w.list(sub)
			}

		case typ&fs.ModeSymlink != 0:
			if w.selects(sub) {
				w.link(sub)
			}

		case typ.IsRegular():
			if w.selects(sub) {
				w.selected++
				w.entries = append(w.entries, walkEntry{rel: sub, from: path.Join(w.root, sub)})
			}
		}
	}
}

// selects reports whether the walk selects the file at rel by its path.
func (w *fsWalk) selects(rel string) bool {
	return !matchAny(w.p.exclude, rel, false) && matchAny(w.p.include, rel, false)
}

// link adds the entry of the link at rel when it leads to a regular file,
// or the problem of a link that the walk cannot follow. The walk resolves
// a link that the file system reports one element at a time. A target
// that is absolute or climbs out of the input directory leads outside.
// [fsWalk.unreportedLink] adds a link that the file system does not
// report.
func (w *fsWalk) link(rel string) {
	full := path.Join(w.root, rel)

	rl, ok, err := w.reported(full)

	switch {
	case err != nil:
		w.selected++
		w.entries = append(w.entries, walkEntry{rel: rel, err: readError(err)})

		return

	case !ok:
		w.unreportedLink(rel, full)

		return
	}

	written, _ := rl.ReadLink(full) //nolint:errcheck // The resolve below reports the same error.

	resolved, inside, err := w.resolve(rl, full)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		w.selected++
		w.entries = append(w.entries, walkEntry{rel: rel, err: danglingProblem(written, err)})

		return

	case err != nil:
		w.selected++
		w.entries = append(w.entries, walkEntry{rel: rel, err: readError(err)})

		return

	case !inside && !w.p.def.cfg.outsideLinks:
		w.selected++
		w.entries = append(w.entries, walkEntry{rel: rel, err: outsideProblem(written, w.root)})

		return

	case resolved == "":
		// The file system reads no file outside its root.
		w.selected++
		w.entries = append(w.entries, walkEntry{rel: rel, err: newProblem(ErrOutsideLink,
			fmt.Sprintf("links to %s, which leads outside the file system", escape.Line(written)))})

		return
	}

	info, err := fs.Stat(w.fsys, resolved)
	if err != nil {
		w.selected++
		w.entries = append(w.entries, walkEntry{rel: rel, err: readError(err)})

		return
	}

	if !info.Mode().IsRegular() {
		return
	}

	w.selected++

	below := relTo(w.realRoot, resolved)
	if inside && !matchAny(w.p.include, below, false) {
		w.entries = append(w.entries, walkEntry{rel: rel, err: unselectedProblem(written, w.p.globs)})

		return
	}

	w.entries = append(w.entries, walkEntry{rel: rel, from: resolved})
}

// reported returns the file system as an [fs.ReadLinkFS] when it reports
// the link at full, an entry that the listing of a directory names as a
// link. The file system reports the link when its Lstat describes a link
// too. An Lstat that fails returns its error.
//
// A file system without [fs.ReadLinkFS] reports no link. [fs.Sub] of that
// file system implements the interface and reports no link either, because
// its Lstat follows the link. The walk thus asks Lstat about the link, and
// does not rely on the type alone. ReadLink does not decide the answer.
// The walk thus fails a link that Lstat describes and ReadLink cannot
// read with the error of ReadLink, whatever [WithOutsideLinks] says.
func (w *fsWalk) reported(full string) (fs.ReadLinkFS, bool, error) {
	rl, ok := w.fsys.(fs.ReadLinkFS)
	if !ok {
		return nil, false, nil
	}

	info, err := rl.Lstat(full)
	if err != nil {
		return nil, false, err //nolint:wrapcheck // The caller reads the error of the file system.
	}

	if info.Mode()&fs.ModeSymlink == 0 {
		return nil, false, nil
	}

	return rl, true, nil
}

// unreportedLink adds the entry of the link at rel, whose path in the file
// system is full, when the file system does not report the link. The walk
// cannot tell whether the link stays inside the input directory. A link to
// a regular file is thus a problem unless [WithOutsideLinks] is set. With
// that option, the walk reads what Open returns for the link.
func (w *fsWalk) unreportedLink(rel, full string) {
	info, err := fs.Stat(w.fsys, full)

	switch {
	case err != nil:
		w.selected++
		w.entries = append(w.entries, walkEntry{rel: rel, err: readError(err)})

	case !info.Mode().IsRegular():
		// The walk skips a link to anything but a regular file.

	case !w.p.def.cfg.outsideLinks:
		w.selected++
		w.entries = append(w.entries, walkEntry{rel: rel, err: unreportedProblem()})

	default:
		w.selected++
		w.entries = append(w.entries, walkEntry{rel: rel, from: full})
	}
}

// unreportedProblem returns the problem of a link that the file system
// does not report. A file system whose type implements [fs.ReadLinkFS]
// still reports no link when it forwards to one that does not, so the
// detail names both.
func unreportedProblem() error {
	detail := errors.New("pass WithOutsideLinks(true) to read it, or implement fs.ReadLinkFS in the file system " +
		"and in each file system that it wraps")

	return newProblem(ErrOutsideLink, "is a link, and the file system does not report where it leads",
		niceyaml.WithDetails(detail))
}

// resolve follows the links of name, a path in the file system, one
// element at a time, as an operating system does. It returns the path that
// name leads to, and whether that path lies inside the input directory
// with the links of the directory resolved too. A link with an absolute
// target, or one that climbs above the root of the file system, leads
// outside.
func (w *fsWalk) resolve(rl fs.ReadLinkFS, name string) (string, bool, error) {
	resolved, ok, err := evalFS(rl, name)
	if err != nil || !ok {
		return "", false, err
	}

	if w.realRoot == "" {
		root, ok, err := evalFS(rl, w.root)
		if err != nil {
			return "", false, err
		}

		w.realRoot = root
		if !ok {
			w.realRoot = w.root
		}
	}

	return resolved, within(w.realRoot, resolved), nil
}

// within reports whether name, a cleaned path in the file system, lies
// inside root.
func within(root, name string) bool {
	return root == "." || name == root || strings.HasPrefix(name, root+"/")
}

// evalFS returns name, a path in the file system of rl, with the links of
// each element resolved. It also reports whether the path stays inside
// the file system. A link with an absolute target, or a path that climbs
// above the root, leaves it.
func evalFS(rl fs.ReadLinkFS, name string) (string, bool, error) {
	dir := "."
	rest := strings.Split(name, "/")
	hops := 0

	for len(rest) > 0 {
		elem := rest[0]
		rest = rest[1:]

		switch elem {
		case "", ".":
			continue

		case "..":
			if dir == "." {
				return "", false, nil
			}

			dir = path.Dir(dir)

			continue
		}

		cur := path.Join(dir, elem)

		info, err := rl.Lstat(cur)
		if err != nil {
			return "", true, err //nolint:wrapcheck // The caller reads the error of the file system.
		}

		if info.Mode()&fs.ModeSymlink == 0 {
			dir = cur

			continue
		}

		hops++
		if hops > maxLinkHops {
			return "", true, fmt.Errorf("%s: too many links", name)
		}

		target, err := rl.ReadLink(cur)
		if err != nil {
			return "", true, err //nolint:wrapcheck // The caller reads the error of the file system.
		}

		if path.IsAbs(target) {
			return "", false, nil
		}

		rest = append(strings.Split(target, "/"), rest...)
	}

	return dir, true, nil
}

// relTo returns name, a cleaned path in the file system inside root, as a
// slash path below root.
func relTo(root, name string) string {
	if root == "." {
		return name
	}

	return strings.TrimPrefix(strings.TrimPrefix(name, root), "/")
}
