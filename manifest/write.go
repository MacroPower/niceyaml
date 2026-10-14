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
	"strings"

	"go.jacobcolvin.com/niceyaml"
)

// errOutside indicates a link that leads outside the directory that Apply
// works in.
var errOutside = errors.New("link leads outside the directory")

// The most links that the resolve of one path follows.
const maxLinks = 40

// write writes the new text of f over its file on disk, or returns the
// problem that keeps Apply from writing it.
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
// below it. A directory input works in the directory that it leads to, and
// a file that an input names directly works in the directory of the file
// with every link resolved.
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

// writeFile replaces the file at name below dir, whose text must still be
// old, with text. It writes text to a new file beside the old one, gives
// the new file the mode, the owner, the group, and the attributes of the
// old one, and renames it over the old one. The directory never lets a
// link lead out of it.
func writeFile(dir, name string, old, text []byte, app string) error {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return changedProblem("removed after " + app + " read it")
	}

	if err != nil {
		return systemProblem(err)
	}

	defer root.Close() //nolint:errcheck // A directory has nothing to flush.

	abs, err := filepath.EvalSymlinks(dir)
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

// replace writes text to a new file beside the file at name in root, and
// renames it over that file once its text is still old.
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
			_ = root.Remove(tmpName) //nolint:errcheck // A stray file starts with "." and a load skips it.
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
// the attributes of old, whose details info holds, then syncs it.
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
// is no longer old, or when it is gone.
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
// crash. A file counts as written once its rename succeeds, so a sync that
// does not succeed changes nothing.
func syncDir(root *os.Root, dir string) {
	d, err := root.Open(dir)
	if err != nil {
		return
	}

	_ = d.Sync()  //nolint:errcheck // The rename already succeeded.
	_ = d.Close() //nolint:errcheck // A directory has nothing to flush.
}

// resolveIn returns the path of the file at name in root with every link
// resolved. Each link must lead to a path below root, whose path with
// every link resolved is abs, or resolveIn returns [errOutside].
func resolveIn(root *os.Root, abs, name string) (string, error) {
	parts := strings.Split(filepath.ToSlash(name), "/")

	var done []string

	for links := 0; len(parts) > 0; {
		part := parts[0]
		parts = parts[1:]

		switch part {
		case "", ".":
			continue

		case "..":
			if len(done) > 0 {
				done = done[:len(done)-1]

				continue
			}

			// A link can climb out of root and come back in, as
			// "../config/x.yaml" does in a directory named config.
			rel, ok := absBelow(abs, filepath.Join(append([]string{filepath.Dir(abs)}, parts...)...))
			if !ok {
				return "", errOutside
			}

			parts = strings.Split(filepath.ToSlash(rel), "/")

			continue
		}

		cur := filepath.Join(append(done, part)...)

		info, err := root.Lstat(cur)
		if err != nil {
			return "", err //nolint:wrapcheck // The caller names the problem.
		}

		if info.Mode()&fs.ModeSymlink == 0 {
			done = append(done, part)

			continue
		}

		links++
		if links > maxLinks {
			return "", errOutside
		}

		link, err := root.Readlink(cur)
		if err != nil {
			return "", err //nolint:wrapcheck // The caller names the problem.
		}

		if filepath.IsAbs(link) {
			rel, ok := absBelow(abs, link)
			if !ok {
				return "", errOutside
			}

			done, link = nil, rel
		}

		parts = append(strings.Split(filepath.ToSlash(link), "/"), parts...)
	}

	return filepath.Join(done...), nil
}

// absBelow returns the path of link, an absolute link target, below abs,
// a directory with every link resolved, and whether link lies below abs.
// A target whose directory runs through links, such as /tmp on macOS,
// lies below abs when it does after those links resolve.
func absBelow(abs, link string) (string, bool) {
	if rel, ok := relBelow(abs, link); ok {
		return rel, true
	}

	dir, err := filepath.EvalSymlinks(filepath.Dir(link))
	if err != nil {
		return "", false
	}

	return relBelow(abs, filepath.Join(dir, filepath.Base(link)))
}

// relBelow returns the path of target below dir, and whether target lies
// below dir as text.
func relBelow(dir, target string) (string, bool) {
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	return rel, true
}

// systemProblem returns the problem of a file that the system refuses to
// replace, with the reason that the system gives.
func systemProblem(err error) error {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		err = pe.Err
	}

	if le, ok := errors.AsType[*os.LinkError](err); ok {
		err = le.Err
	}

	return niceyaml.Place(fmt.Errorf("cannot replace the file: %w", err))
}
