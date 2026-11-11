package manifest_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"
	"golang.org/x/sys/unix"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
)

// The test binary runs [applyUnkept] in place of [TestPlan_Apply_Unkept]
// when unkeptCaseEnv names a case of that test. The directory that holds
// the file of the case is then in unkeptDirEnv.
const (
	unkeptCaseEnv = "NICEYAML_MANIFEST_UNKEPT_CASE"
	unkeptDirEnv  = "NICEYAML_MANIFEST_UNKEPT_DIR"
)

// TestPlan_Apply_Unkept applies each plan in a child process inside a user
// namespace that maps only the user and the group of the test. The system
// refuses every other ID there. The new file thus cannot get an owner, a
// group, or an access control list entry with such an ID.
//
// Each case first gives its file such an ID, and skips when the system
// refuses. The system refuses an ID that the user namespace of the test
// does not map. It refuses another owner to a process without the
// CAP_CHOWN capability, so the owner case runs only as root. It refuses
// another group to a process that is not root and is in no second group.
func TestPlan_Apply_Unkept(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		change func(t *testing.T, path string)
		// The name in the message for the file that st describes. It is
		// nil for a message without a name.
		name func(st *syscall.Stat_t) string
		err  string
	}{
		"owner that the namespace does not map": {
			change: func(t *testing.T, path string) {
				t.Helper()

				// The child process does not own the file after the chown, so
				// the mode lets every user write it. The chmod comes first
				// because the test might not own the file afterward either.
				require.NoError(t, os.Chmod(path, 0o666))

				// The new owner is never the user of the process. The system
				// lets every process give a file the owner that the file
				// already has, so that chown changes nothing and succeeds.
				//
				// The system refuses the chown with EPERM when the process
				// lacks the CAP_CHOWN capability, which every user other
				// than root lacks. It refuses with EINVAL when the user
				// namespace of the test does not map the new owner.
				err := os.Chown(path, os.Geteuid()+1, -1)
				if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EINVAL) {
					t.Skip("the system does not let the process give the file another owner")
				}

				require.NoError(t, err)
			},
			name: func(st *syscall.Stat_t) string { return userName(st.Uid) },
			err: stringtest.JoinLF(
				"hours.yaml: cannot keep owner %q",
				"|-- a rewrite replaces the file, and the new file cannot get this owner",
				"`-- run the command as the owner of the file, or as root",
			),
		},
		"group that the namespace does not map": {
			change: giveOtherGroup,
			name:   func(st *syscall.Stat_t) string { return groupName(st.Gid) },
			err: stringtest.JoinLF(
				"hours.yaml: cannot keep group %q",
				"|-- a rewrite replaces the file, and the new file cannot get this group",
				"`-- add the owner of the file to the group, or run the command as root",
			),
		},
		"access control list with a user that the namespace does not map": {
			change: func(t *testing.T, path string) {
				t.Helper()

				uid := os.Geteuid() + 1
				if !mapsUser(t, uid) {
					t.Skipf("the user namespace of the test does not map user %d", uid)
				}

				//nolint:gosec // A user ID fits a uint32.
				err := unix.Setxattr(path, "system.posix_acl_access", accessList(uint32(uid)), 0)
				if errors.Is(err, unix.ENOTSUP) {
					t.Skip("the file system does not keep access control lists")
				}

				require.NoError(t, err)
			},
			err: stringtest.JoinLF(
				"hours.yaml: cannot keep the access control list",
				"`-- a rewrite replaces the file, and the new file cannot get this access control list",
			),
		},
	}

	if name := os.Getenv(unkeptCaseEnv); name != "" {
		tc := tcs[name]
		applyUnkept(t, os.Getenv(unkeptDirEnv), tc.err, tc.name)

		return
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			// Each case expects every new file to get the group of the
			// process. A directory with the setgid bit gives a new file its
			// own group instead. So does every directory on a file system
			// with the grpid mount option. A temporary directory can be such
			// a directory, so the test gives it the group of the process.
			//
			// The system refuses the chown with EINVAL when the user
			// namespace of the test does not map the group of the process.
			err := os.Chown(dir, -1, os.Getegid())
			if errors.Is(err, syscall.EINVAL) {
				t.Skip("the user namespace of the test does not map the group of the process")
			}

			require.NoError(t, err)

			config := filepath.Join(dir, "config")
			yamltest.WriteFiles(t, config, map[string]string{"hours.yaml": hoursDoc})

			tc.change(t, filepath.Join(config, "hours.yaml"))

			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPlan_Apply_Unkept$")

			cmd.Env = append(os.Environ(), unkeptCaseEnv+"="+name, unkeptDirEnv+"="+config)
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Cloneflags:  syscall.CLONE_NEWUSER,
				UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Geteuid(), Size: 1}},
				GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getegid(), Size: 1}},
			}

			var out bytes.Buffer

			cmd.Stdout = &out
			cmd.Stderr = &out

			err = cmd.Start()
			if err != nil {
				t.Skipf("the system does not start a process in a user namespace: %v", err)
			}

			require.NoError(t, cmd.Wait(), out.String())
		})
	}
}

// applyUnkept plans and applies the migration of the file in config. It
// requires the problem want, whose %q verb takes the name that name
// returns for the file.
func applyUnkept(t *testing.T, config, want string, name func(st *syscall.Stat_t) string) {
	t.Helper()

	path := filepath.Join(config, "hours.yaml")

	if name != nil {
		info, err := os.Stat(path)
		require.NoError(t, err)

		st, ok := info.Sys().(*syscall.Stat_t)
		require.True(t, ok)

		want = fmt.Sprintf(want, name(st))
	}

	plan, err := newCafeRegistry(t).PlanMigration(t.Context(), manifest.Path(config))
	require.NoError(t, err)
	require.Len(t, plan.Files(), 1)

	res, err := plan.Apply(t.Context())
	require.ErrorIs(t, err, manifest.ErrReadOnly)
	require.ErrorIs(t, err, syscall.EINVAL)
	assert.Equal(t, res.Err(), err)
	assert.Empty(t, res.Files())
	assert.False(t, niceyaml.IsInvalid(err))
	assert.Equal(t, config+"/"+want, niceyaml.FormatError(err))
	assert.Equal(t, hoursDoc, yamltest.ReadFile(t, path))

	entries, err := os.ReadDir(config)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

// giveOtherGroup gives the file at path a group that is not the group of
// the process. It skips the test when the system refuses every group that
// the process can try.
func giveOtherGroup(t *testing.T, path string) {
	t.Helper()

	groups, err := os.Getgroups()
	require.NoError(t, err)

	// Root can also give a file a group that root is not in.
	if os.Geteuid() == 0 {
		groups = append(groups, os.Getegid()+1)
	}

	for _, gid := range groups {
		if gid == os.Getegid() {
			continue
		}

		// A user namespace that does not map a group of the process
		// reports that group as the overflow group. The system refuses
		// the overflow group with EINVAL when the namespace does not map
		// it. Otherwise the process is not in the group that the ID
		// names, and the system refuses it with EPERM.
		err := os.Chown(path, -1, gid)
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EPERM) {
			continue
		}

		require.NoError(t, err)

		return
	}

	t.Skip("the system does not let the process give the file another group")
}

// mapsUser reports whether the user namespace of the process maps the
// user uid.
func mapsUser(t *testing.T, uid int) bool {
	t.Helper()

	ranges, err := os.ReadFile("/proc/self/uid_map")
	require.NoError(t, err)

	// Each line describes a range of IDs. Its fields are the first ID, the
	// ID that the first one has in the parent namespace, and the length.
	for line := range strings.Lines(string(ranges)) {
		var first, parent, length int64

		_, err := fmt.Sscan(line, &first, &parent, &length)
		require.NoError(t, err)

		if int64(uid) >= first && int64(uid)-first < length {
			return true
		}
	}

	return false
}

// userName returns the name of the user uid, or its number.
func userName(uid uint32) string {
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

// accessList returns an access control list in the format of the
// system.posix_acl_access attribute. It gives the owner and the user uid
// read and write access.
func accessList(uid uint32) []byte {
	const (
		version  = 2
		noID     = 0xffffffff
		ownerTag = 0x01
		userTag  = 0x02
		groupTag = 0x04
		maskTag  = 0x10
		otherTag = 0x20
	)

	entries := []struct {
		tag  uint16
		perm uint16
		id   uint32
	}{
		{tag: ownerTag, perm: 6, id: noID},
		{tag: userTag, perm: 6, id: uid},
		{tag: groupTag, perm: 4, id: noID},
		{tag: maskTag, perm: 6, id: noID},
		{tag: otherTag, perm: 4, id: noID},
	}

	list := binary.LittleEndian.AppendUint32(nil, version)

	for _, e := range entries {
		list = binary.LittleEndian.AppendUint16(list, e.tag)
		list = binary.LittleEndian.AppendUint16(list, e.perm)
		list = binary.LittleEndian.AppendUint32(list, e.id)
	}

	return list
}
