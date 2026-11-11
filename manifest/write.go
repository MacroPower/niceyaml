package manifest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
	"go.jacobcolvin.com/niceyaml/internal/inputhook"
	"go.jacobcolvin.com/niceyaml/internal/rootpath"
)

// WriteAtomic is the [WriteFunc] that [Plan.Apply] uses by default. It
// writes the new text of f over its file on disk, or returns the problem
// that prevents the write. It writes only a file that an [input.Path]
// input read from disk. Each other file fails with an error that matches
// [ErrReadOnly]. WriteAtomic returns the error of ctx when ctx has ended
// before it starts.
//
// WriteAtomic writes the new text to a temporary file beside the old one,
// named ".<name>.<hex>", and renames it over the old one. A crash thus
// leaves the old text or the new text. The process must be able to
// create a file in the directory of the file. A symbolic link keeps
// pointing at its target, which receives the new text. For a file that a
// directory input found, WriteAtomic never follows a link out of the
// input directory.
//
// WriteAtomic reads the file again immediately before the rename. A file
// whose text differs from the text that the plan read, or that no longer
// exists, fails with an error that matches [ErrChanged]. WriteAtomic then
// leaves the file as it is.
//
// The new file gets the mode of the old one. On unix systems, it also
// gets the owner and the group of the old one. On Linux, it also gets the
// access control list and the SELinux label. Platforms other than unix do
// not keep the owner or the group, and platforms other than Linux do not
// keep any extended attribute. When the system refuses to give the new
// file one of them, WriteAtomic leaves the old file as it is and fails
// with an error that matches [ErrReadOnly]. That error also keeps the
// error that the system returned.
//
// These files fail with an error that matches ErrReadOnly:
//
//   - A file that is not a regular file, such as a named pipe or a device.
//   - A file without a write permission bit. On unix systems, the access
//     check of the process also decides, as faccessat does with the
//     effective IDs.
//   - On unix systems, a file in a directory that the process cannot
//     write, even when the file itself is writable.
//   - On unix systems, a file with more than one hard link. A rename
//     replaces the file, so each other link keeps the old text.
//   - A file that a directory input reached through a link that leads
//     outside the input directory, as [input.WithOutsideLinks] allows.
//
// Platforms other than unix skip the access check, the check of the
// directory, and the check of hard links. The write permission bits alone
// decide there. On those platforms, a file whose directory refuses
// the new file fails with the error of the system, which does not match
// ErrReadOnly.
func WriteAtomic(ctx context.Context, f *PlanFile) error {
	err := ctx.Err()
	if err != nil {
		return err //nolint:wrapcheck // Apply binds the end of ctx to the file.
	}

	err = f.readOnly()
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

// target is where a file came from, and where [WriteAtomic] writes it.
type target struct {
	// The directory input whose walk found the file, or "" for a file that
	// an input names directly.
	root string
	// The slash path of the file below root, or the path of a file that an
	// input names directly.
	path string
	// The place that the file came from, which decides whether
	// [WriteAtomic] rewrites the file.
	kind inputhook.Kind
	// Whether a walk reached the file through a link that leads outside
	// the input directory.
	outside bool
}

// diskPath returns the path of the file on disk, as the input reaches it.
func (t target) diskPath() string {
	if t.root == "" {
		return t.path
	}

	return filepath.Join(t.root, filepath.FromSlash(t.path))
}

// place returns the directory that [WriteAtomic] works in and the path of
// the file below it. For a file that a directory input found, WriteAtomic
// works in the directory that the input leads to. For a file that an
// input names directly, WriteAtomic works in the directory of the file,
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

// diskReadOnly returns the problem of the file at path when [WriteAtomic]
// cannot write it, or nil. A file that does not exist passes, and the write
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

	// A ".." in dir climbs from the directory that a link before it leads
	// to, as the climb of OpenRoot does, so abs names the directory of root.
	abs, err := filepaths.Abs(dir)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}

	if err != nil {
		return systemProblem(err)
	}

	name, err = rootpath.Resolve(root, abs, name)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return changedProblem("removed after " + app + " read it")
	case errors.Is(err, rootpath.ErrOutside):
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
