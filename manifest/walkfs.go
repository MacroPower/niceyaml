package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"go.jacobcolvin.com/niceyaml/escape"
)

// maxLinkHops is the number of links a walk of an [fs.FS] follows from one
// entry before it stops, as a system stops at a loop of links.
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
		r.empty = append(r.empty, fmt.Sprintf("%s holds no file that matches %s", escape.Control(root),
			joinOr(p.globs)))
	}
}

// fsWalk is the walk of one directory input in an [fs.FS].
type fsWalk struct {
	fsys fs.FS
	p    *prepared
	root string
	// The input directory with its own links resolved, once a link needs
	// it.
	realRoot string
	entries  []walkEntry
	selected int
}

// list adds the entries of the directory at rel, a slash path below the
// input directory, and of its subdirectories under [WithRecursive].
func (w *fsWalk) list(rel string) {
	dir := path.Join(w.root, rel)

	entries, err := fs.ReadDir(w.fsys, dir)
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
// or the problem of a link that the walk cannot follow. A file system that
// reports links through [fs.ReadLinkFS] resolves the link one element at
// a time, and a target that is absolute or climbs out of the input
// directory leads outside. Any other file system reads what Open returns.
func (w *fsWalk) link(rel string) {
	full := path.Join(w.root, rel)

	rl, ok := w.fsys.(fs.ReadLinkFS)
	if !ok {
		info, err := fs.Stat(w.fsys, full)

		switch {
		case err != nil:
			w.selected++
			w.entries = append(w.entries, walkEntry{rel: rel, err: readError(err)})

		case info.Mode().IsRegular():
			w.selected++
			w.entries = append(w.entries, walkEntry{rel: rel, from: full})
		}

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
			fmt.Sprintf("links to %s, which leads outside the file system", escape.Control(written)))})

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

	w.entries = append(w.entries, walkEntry{rel: rel, from: resolved, outside: !inside})
}

// resolve follows the links of name, a path in the file system, one
// element at a time, as the system does. It returns the path that name
// leads to, and whether that path lies inside the input directory once
// the links of the directory resolve too. A link with an absolute target,
// or one that climbs above the root of the file system, leads outside.
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
// each element resolved, and reports whether the path stays inside the
// file system. A link with an absolute target, or a path that climbs
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
