// Package rootpath resolves the links of a path below an [os.Root]. A
// directory walk reads through the Root, and a rewrite writes through it,
// so neither follows a link out of the directory of the Root.
package rootpath

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// ErrOutside indicates a link that leads outside the directory of the
// Root, or a path that follows more links than the resolve allows.
var ErrOutside = errors.New("link leads outside the directory")

// maxLinks is the number of links that the resolve of one path follows at
// most.
const maxLinks = 40

// Resolve returns the path of the file at name in root with every link
// resolved. The path abs is the path of root with every link resolved. It
// returns [ErrOutside] when the file lies outside root.
//
// It follows each element of the path as the system does, so a ".." after
// a link climbs from the directory that the link leads to. It reads each
// element inside root through root. A link can lead outside root and back
// in, so it reads each element outside root by its path. It returns to
// root at the element that names the directory of root.
func Resolve(root *os.Root, abs, name string) (string, error) {
	r := &resolve{root: root, abs: abs, parts: splitPath(name)}

	for len(r.parts) > 0 {
		part := r.parts[0]
		r.parts = r.parts[1:]

		switch part {
		case "", ".":
			continue
		case "..":
			err := r.up()
			if err != nil {
				return "", err
			}

			continue
		}

		err := r.step(part)
		if err != nil {
			return "", err
		}
	}

	if r.out != "" {
		return "", ErrOutside
	}

	return filepath.Join(r.done...), nil
}

// resolve is the state of one call of [Resolve].
type resolve struct {
	root *os.Root
	// The path of root with every link resolved.
	abs string
	// The details of root, which [resolve.reach] reads at its first use.
	info fs.FileInfo
	// The absolute path that the resolve has reached outside root, with
	// every link resolved. It is empty while the resolve stays inside root.
	out string
	// The elements of the path that the resolve has yet to follow.
	parts []string
	// The elements below root that the resolve has followed. None of them
	// is a link.
	done []string
	// The number of links that the resolve has followed.
	links int
}

// splitPath returns the elements of the path p.
func splitPath(p string) []string {
	return strings.Split(filepath.ToSlash(p), "/")
}

// up follows a ".." element from the directory that the resolve has
// reached. Every element that the resolve has followed is a directory and
// not a link, so the parent of that directory is the parent by text.
func (r *resolve) up() error {
	switch {
	case r.out != "":
		return r.reach(filepath.Dir(r.out), nil)
	case len(r.done) > 0:
		r.done = r.done[:len(r.done)-1]

		return nil

	default:
		return r.reach(filepath.Dir(r.abs), nil)
	}
}

// step follows the element part from the directory that the resolve has
// reached. It splices the target of a link into the elements that the
// resolve has yet to follow.
func (r *resolve) step(part string) error {
	dir := r.out
	if dir == "" {
		dir = filepath.Join(r.done...)
	}

	cur := filepath.Join(dir, part)

	info, err := r.lstat(cur)
	if err != nil {
		return err
	}

	if info.Mode()&fs.ModeSymlink == 0 {
		return r.enter(part, cur, info)
	}

	r.links++
	if r.links > maxLinks {
		return ErrOutside
	}

	link, err := r.readlink(cur)
	if err != nil {
		return err
	}

	if filepath.IsAbs(link) {
		vol := filepath.VolumeName(link)

		err := r.reach(vol+string(filepath.Separator), nil)
		if err != nil {
			return err
		}

		link = link[len(vol):]
	}

	r.parts = append(splitPath(link), r.parts...)

	return nil
}

// enter moves the resolve to cur, the element part that info describes,
// which is not a link. A file ends the path, so enter refuses a file that
// more elements follow, as the system does.
func (r *resolve) enter(part, cur string, info fs.FileInfo) error {
	if !info.IsDir() && slices.ContainsFunc(r.parts, func(p string) bool { return p != "" && p != "." }) {
		return &fs.PathError{Op: "lstat", Path: cur, Err: syscall.ENOTDIR}
	}

	if r.out == "" {
		r.done = append(r.done, part)

		return nil
	}

	return r.reach(cur, info)
}

// reach moves the resolve to dir, an absolute path with every link
// resolved, which info describes. When info is nil, reach reads it. The
// resolve returns inside root when dir names the directory of root, as a
// volume root can.
func (r *resolve) reach(dir string, info fs.FileInfo) error {
	if info == nil {
		var err error

		info, err = os.Stat(dir)
		if err != nil {
			return err //nolint:wrapcheck // The caller names the problem.
		}
	}

	if info.IsDir() {
		if r.info == nil {
			var err error

			r.info, err = r.root.Stat(".")
			if err != nil {
				return err //nolint:wrapcheck // The caller names the problem.
			}
		}

		if os.SameFile(info, r.info) {
			r.out, r.done = "", nil

			return nil
		}
	}

	r.out, r.done = dir, nil

	return nil
}

// lstat describes cur, a path below root while the resolve stays inside
// root, and an absolute path outside it.
func (r *resolve) lstat(cur string) (fs.FileInfo, error) {
	if r.out == "" {
		return r.root.Lstat(cur) //nolint:wrapcheck // The caller names the problem.
	}

	return os.Lstat(cur) //nolint:wrapcheck // The caller names the problem.
}

// readlink returns the target of the link at cur, a path below root while
// the resolve stays inside root, and an absolute path outside it.
func (r *resolve) readlink(cur string) (string, error) {
	if r.out == "" {
		return r.root.Readlink(cur) //nolint:wrapcheck // The caller names the problem.
	}

	return os.Readlink(cur) //nolint:wrapcheck // The caller names the problem.
}
