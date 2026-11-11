//go:build unix

package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// writable reports whether the process can write the file at path, as
// faccessat with the effective IDs of the process decides.
func writable(path string) bool {
	return unix.Faccessat(unix.AT_FDCWD, path, unix.W_OK, unix.AT_EACCESS) == nil
}

// linkCount returns the number of hard links of the file that info
// describes.
func linkCount(info fs.FileInfo) int {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 1
	}

	return int(st.Nlink) //nolint:gosec // A link count fits an int.
}

// keepOwner gives tmp the owner and the group of the file that info
// describes. For an owner or a group that it cannot give, it returns a
// problem that matches [ErrReadOnly].
func keepOwner(tmp *os.File, info fs.FileInfo) error {
	want, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}

	tmpInfo, err := tmp.Stat()
	if err != nil {
		return systemProblem(err)
	}

	got, ok := tmpInfo.Sys().(*syscall.Stat_t)
	if !ok || got.Uid == want.Uid && got.Gid == want.Gid {
		return nil
	}

	err = tmp.Chown(int(want.Uid), int(want.Gid))
	if err == nil {
		return nil
	}

	if got.Uid != want.Uid {
		return unkeptProblem(err, fmt.Sprintf("cannot keep owner %q", ownerName(want.Uid)),
			errors.New("a rewrite replaces the file, and the new file cannot get this owner"),
			errors.New("run the command as the owner of the file, or as root"))
	}

	// The process runs as the owner of the file here, because the new file
	// already has the owner of the old one. The owner is not in the group
	// of the file. Another member of that group is not the owner, so the
	// advice does not name such a user.
	return unkeptProblem(err, fmt.Sprintf("cannot keep group %q", groupName(want.Gid)),
		errors.New("a rewrite replaces the file, and the new file cannot get this group"),
		errors.New("add the owner of the file to the group, or run the command as root"))
}

// ownerName returns the name of the user uid, or its number.
func ownerName(uid uint32) string {
	id := strconv.FormatUint(uint64(uid), 10)

	u, err := user.LookupId(id)
	if err != nil {
		return id
	}

	return u.Username
}

// groupName returns the name of the group gid, or its number.
func groupName(gid uint32) string {
	id := strconv.FormatUint(uint64(gid), 10)

	g, err := user.LookupGroupId(id)
	if err != nil {
		return id
	}

	return g.Name
}
