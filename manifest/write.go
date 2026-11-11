package manifest

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"go.jacobcolvin.com/niceyaml"
)

// errOutside indicates a link that leads outside the directory that a
// walk reads or that Apply works in.
var errOutside = errors.New("link leads outside the directory")

// maxLinks is the number of links that the resolve of one path follows at
// most.
const maxLinks = 40

// write writes the new text of f over its file on disk, or returns the
// problem that prevents Apply from writing it.
func (f *PlanFile) write() error {
	err := f.readOnly()
	if err != nil {
		return err
	}

	app := f.reg.app()

	dir, name, err := f.f.target.place()
	if errors.Is(err, fs.ErrNotExist) {
		return changedProblem("removed after " + app + " read it")
	}

	if err != nil {
		return systemProblem(err)
	}

	return writeFile(dir, name, []byte(f.f.src.Text()), f.after, app)
}

// diskPath returns the path of the file on disk, as the input reaches it.
func (t target) diskPath() string {
	if t.root == "" {
		return t.path
	}

	return filepath.Join(t.root, filepath.FromSlash(t.path))
}

// place returns the directory that Apply works in and the path of the file
// below it. For a file that a directory input found, Apply works in the
// directory that the input leads to. For a file that an input names
// directly, Apply works in the directory of the file, with every link
// resolved.
func (t target) place() (string, string, error) {
	if t.root != "" {
		return t.root, filepath.FromSlash(t.path), nil
	}

	resolved, err := filepath.EvalSymlinks(t.path)
	if err != nil {
		return "", "", err //nolint:wrapcheck // The caller names the problem.
	}

	return filepath.Dir(resolved), filepath.Base(resolved), nil
}

// diskReadOnly returns the problem of the file at path when Apply cannot
// write it, or nil. A file that does not exist passes, and the write
// reports it.
func diskReadOnly(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return nil //nolint:nilerr // The write reports a file that is gone.
	}

	mode := info.Mode()

	switch {
	case mode&fs.ModeNamedPipe != 0:
		return readOnlyProblem("cannot rewrite a named pipe")
	case mode&(fs.ModeDevice|fs.ModeCharDevice) != 0:
		return readOnlyProblem("cannot rewrite a device")
	case !mode.IsRegular():
		return readOnlyProblem("cannot rewrite a file that is not a regular file")
	case mode.Perm()&0o222 == 0 || !writable(path):
		return readOnlyProblem("cannot rewrite a read-only file")
	}

	// The rewrite creates the new file in the directory of the file that
	// path leads to, and renames it there.
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil && !writable(filepath.Dir(resolved)) {
		return readOnlyProblem("cannot rewrite a file in a read-only directory")
	}

	if n := linkCount(info); n > 1 {
		return readOnlyProblem(fmt.Sprintf("cannot rewrite a file with %d hard links", n),
			errors.New("a rewrite replaces the file, so the other links keep the old text"),
			errors.New("replace the file with a copy to break the links, then run the command again"))
	}

	return nil
}

// writeFile replaces the text of the file at name below dir with text,
// when the file still holds old. It writes text to a new file beside the
// old one. It gives the new file the mode, the owner, the group, and the
// attributes of the old one. Then it renames the new file over the old
// one. The directory never lets a link lead out of it.
func writeFile(dir, name string, old, text []byte, app string) error {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return changedProblem("removed after " + app + " read it")
	}

	if err != nil {
		return systemProblem(err)
	}

	defer root.Close() //nolint:errcheck // A directory has nothing to flush.

	abs, err := filepath.Abs(dir)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}

	if err != nil {
		return systemProblem(err)
	}

	name, err = resolveIn(root, abs, name)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return changedProblem("removed after " + app + " read it")
	case errors.Is(err, errOutside):
		return changedProblem("changed after " + app + " read it")
	case err != nil:
		return systemProblem(err)
	}

	err = sameText(root, name, old, app)
	if err != nil {
		return err
	}

	return replace(root, name, old, text, app)
}

// replace writes text to a new file beside the file at name in root. It
// renames the new file over that file when that file still holds old.
func replace(root *os.Root, name string, old, text []byte, app string) error {
	oldFile, err := root.Open(name)
	if err != nil {
		return systemProblem(err)
	}

	defer oldFile.Close() //nolint:errcheck // A file opened to read has nothing to flush.

	info, err := oldFile.Stat()
	if err != nil {
		return systemProblem(err)
	}

	tmp, tmpName, err := createTemp(root, name)
	if err != nil {
		return systemProblem(err)
	}

	renamed := false

	defer func() {
		if !renamed {
			_ = root.Remove(tmpName) //nolint:errcheck // A stray file starts with ".", so most loads skip it.
		}
	}()

	err = fill(tmp, oldFile, info, text)
	if err != nil {
		_ = tmp.Close() //nolint:errcheck // The problem of the write wins.

		return err
	}

	err = tmp.Close()
	if err != nil {
		return systemProblem(err)
	}

	err = sameText(root, name, old, app)
	if err != nil {
		return err
	}

	err = root.Rename(tmpName, name)
	if err != nil {
		return systemProblem(err)
	}

	renamed = true

	syncDir(root, filepath.Dir(name))

	return nil
}

// fill writes text to tmp and gives it the mode, the owner, the group, and
// the attributes of old, whose details info holds. Then it syncs tmp. An
// owner, a group, or an attribute that the system refuses to give fails
// with a problem that matches [ErrReadOnly].
func fill(tmp, old *os.File, info fs.FileInfo, text []byte) error {
	_, err := tmp.Write(text)
	if err != nil {
		return systemProblem(err)
	}

	// The mode comes after the owner, because a change of owner or group
	// clears the setuid and setgid bits.
	err = keepOwner(tmp, info)
	if err != nil {
		return err
	}

	err = tmp.Chmod(info.Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky))
	if err != nil {
		return systemProblem(err)
	}

	err = keepAttributes(tmp, old)
	if err != nil {
		return err
	}

	err = tmp.Sync()
	if err != nil {
		return systemProblem(err)
	}

	return nil
}

// createTemp creates a new file beside the file at name in root, under a
// random name that starts with ".", with mode 0600.
func createTemp(root *os.Root, name string) (*os.File, string, error) {
	dir, base := filepath.Dir(name), filepath.Base(name)

	for {
		var b [8]byte

		_, _ = rand.Read(b[:]) //nolint:errcheck // rand.Read never returns an error.

		tmpName := filepath.Join(dir, "."+base+"."+hex.EncodeToString(b[:]))

		f, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}

		if err != nil {
			return nil, "", err //nolint:wrapcheck // The caller names the problem.
		}

		return f, tmpName, nil
	}
}

// sameText returns the problem of the file at name in root when its text
// differs from old, or when the file does not exist.
func sameText(root *os.Root, name string, old []byte, app string) error {
	current, err := root.ReadFile(name)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return changedProblem("removed after " + app + " read it")
	case err != nil:
		return systemProblem(err)
	case !bytes.Equal(current, old):
		return changedProblem("changed after " + app + " read it")
	}

	return nil
}

// syncDir syncs the directory at dir in root, so the rename survives a
// crash. A file counts as written after its rename succeeds, so a failed
// sync does not change the outcome.
func syncDir(root *os.Root, dir string) {
	d, err := root.Open(dir)
	if err != nil {
		return
	}

	_ = d.Sync()  //nolint:errcheck // The rename already succeeded.
	_ = d.Close() //nolint:errcheck // A directory has nothing to flush.
}

// resolveIn returns the path of the file at name in root with every link
// resolved. The path abs is the path of root with every link resolved. It
// returns [errOutside] when the file lies outside root.
//
// It follows each element of the path as the system does, so a ".." after
// a link climbs from the directory that the link leads to. It reads each
// element inside root through root. A link can lead outside root and back
// in, so it reads each element outside root by its path. It returns to
// root at the element that names the directory of root.
func resolveIn(root *os.Root, abs, name string) (string, error) {
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
		return "", errOutside
	}

	return filepath.Join(r.done...), nil
}

// resolve is the state of one call of [resolveIn].
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
		return errOutside
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

// systemProblem returns the problem of a file that the system refuses to
// replace, with the reason that the system gives.
func systemProblem(err error) error {
	return niceyaml.Unchecked(fmt.Errorf("cannot replace the file: %w", systemReason(err)))
}

// systemReason returns the reason that the system gives in err, without
// the operation and the path that an [fs.PathError] or an [os.LinkError]
// adds.
func systemReason(err error) error {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		err = pe.Err
	}

	if le, ok := errors.AsType[*os.LinkError](err); ok {
		err = le.Err
	}

	return err
}
