//go:build linux

package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

// Resolve the parent in one kernel operation: no symlinks, no escapes, including
// when agent activity swaps a path component concurrently. The directory handle
// then anchors creation/rename and cleanup to the same directory.
func workspaceParent(root *os.Root, name string) (int, error) {
	base, err := root.Open(".")
	if err != nil {
		return -1, err
	}
	defer base.Close()
	return unix.Openat2(int(base.Fd()), path.Dir(name), &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS,
	})
}

func createWorkspaceFolder(root *os.Root, name string) error {
	parent, err := workspaceParent(root, name)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	return unix.Mkdirat(parent, path.Base(name), 0755)
}

func moveWorkspaceItem(root *os.Root, source, destination string) error {
	from, err := workspaceParent(root, source)
	if err != nil {
		return err
	}
	defer unix.Close(from)
	to, err := workspaceParent(root, destination)
	if err != nil {
		return err
	}
	defer unix.Close(to)
	var info unix.Stat_t
	if err := unix.Fstatat(from, path.Base(source), &info, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	switch info.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		return unix.ELOOP
	case unix.S_IFREG, unix.S_IFDIR:
	default:
		return unix.EACCES
	}
	// RENAME_NOREPLACE makes the conflict check atomic, including external writers.
	return unix.Renameat2(from, path.Base(source), to, path.Base(destination), unix.RENAME_NOREPLACE)
}

func uploadWorkspaceFile(root *os.Root, name string, body io.Reader) error {
	parent, err := workspaceParent(root, name)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	temporary := ".workspace-upload-" + hex.EncodeToString(random)
	fd, err := unix.Openat(parent, temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), temporary)
	defer unix.Unlinkat(parent, temporary, 0)
	_, copyErr := io.Copy(file, body)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return unix.Renameat2(parent, temporary, parent, path.Base(name), unix.RENAME_NOREPLACE)
}
