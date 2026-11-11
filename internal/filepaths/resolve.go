package filepaths

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Abs returns an absolute form of path that names the file the OS opens
// for it. It joins a relative path to the working directory, as
// [filepath.Abs] does, and then cleans the result as [Clean] does. A ".."
// element thus steps up from the directory that a symbolic link before it
// leads to, as the OS steps. That includes a ".." that climbs out of a
// symlinked working directory.
//
// Abs returns an error only when [filepath.Abs] does, which is when the
// working directory has no name.
func Abs(path string) (string, error) {
	if runtime.GOOS == "windows" || !hasDotDot(path) {
		return filepath.Abs(path) //nolint:wrapcheck // Abs returns the error of filepath.Abs.
	}

	// Join the parts as text, because filepath.Join drops the ".."
	// elements that Clean resolves.
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err //nolint:wrapcheck // filepath.Abs returns this error too.
		}

		path = wd + string(filepath.Separator) + path
	}

	return Clean(path), nil
}

// Clean returns a clean path that names the file the OS opens for path.
// It cleans path as [filepath.Clean] does, except at a ".." that follows
// a symbolic link. The OS steps up from the directory that the link
// leads to. Clean therefore replaces the path up to that link with its
// target, all of its links resolved by [filepath.EvalSymlinks], and steps
// up from there. A relative path keeps its leading ".." elements, which
// the OS resolves against the working directory.
//
// Clean calls [os.Lstat] once for each ".." that follows a name, and
// leaves a path without one as [filepath.Clean] leaves it. A path whose
// ".." elements follow no link thus keeps every element as written.
// Clean returns the [filepath.Clean] form when a call fails, and for
// every path on Windows, because Windows drops each ".." element as text
// before it follows a link.
func Clean(path string) string {
	lexical := filepath.Clean(path)
	if runtime.GOOS == "windows" || !hasDotDot(path) {
		return lexical
	}

	cur := ""
	if filepath.IsAbs(path) {
		cur = string(filepath.Separator)
	}

	for elem := range strings.SplitSeq(path, string(filepath.Separator)) {
		switch elem {
		case "", ".":
			continue

		case "..":
			if !namesEntry(cur) {
				cur = parent(cur)

				continue
			}

			info, err := os.Lstat(cur)
			if err != nil {
				return lexical
			}

			if info.Mode()&fs.ModeSymlink != 0 {
				// A relative link target gives a relative result, which
				// might be "." or end in "..", so parent steps up from it
				// as text.
				cur, err = filepath.EvalSymlinks(cur)
				if err != nil {
					return lexical
				}
			}

			cur = parent(cur)

		default:
			cur = filepath.Join(cur, elem)
		}
	}

	if cur == "" {
		return "."
	}

	return cur
}

// namesEntry reports whether the clean path cur ends in a name that
// [os.Lstat] can check. The working directory, a path that ends in "..",
// and the root do not.
func namesEntry(cur string) bool {
	return cur != "" && cur != "." && filepath.Base(cur) != ".." && filepath.Dir(cur) != cur
}

// parent returns the parent of the clean path cur as text. The parent of
// the working directory is "..", the parent of a path that ends in ".."
// climbs one more level, and the parent of the root is the root.
func parent(cur string) string {
	switch {
	case cur == "" || cur == ".":
		return ".."
	case filepath.Base(cur) == "..":
		return filepath.Join(cur, "..")
	default:
		return filepath.Dir(cur)
	}
}

// hasDotDot reports whether path holds a ".." element.
func hasDotDot(path string) bool {
	for elem := range strings.SplitSeq(path, string(filepath.Separator)) {
		if elem == ".." {
			return true
		}
	}

	return false
}
