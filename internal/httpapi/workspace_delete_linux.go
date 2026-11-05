//go:build linux

package httpapi

import (
	"context"
	"errors"
	"io"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

// Parent and directory handles pin traversal to the selected entries. Each
// nested name is a single directory entry; symbolic links are never followed.
func deleteWorkspaceItem(ctx context.Context, root *os.Root, name string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !validWorkspaceMutationPath(name) {
		return false, unix.EINVAL
	}
	parent, err := workspaceParent(root, name)
	if err != nil {
		return false, err
	}
	defer unix.Close(parent)
	return deleteWorkspaceEntry(ctx, parent, path.Base(name), true)
}

func deleteWorkspaceEntry(ctx context.Context, parent int, name string, selected bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var info unix.Stat_t
	if err := unix.Fstatat(parent, name, &info, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if !selected && errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	kind := info.Mode & unix.S_IFMT
	if selected {
		if kind == unix.S_IFLNK {
			return false, unix.ELOOP
		}
		if kind != unix.S_IFREG && kind != unix.S_IFDIR {
			return false, errUnsupportedWorkspaceDelete
		}
	}
	if kind != unix.S_IFDIR {
		// Unlinking a nested symlink or FIFO only removes its entry; its target
		// is never opened, so external files and blocking devices are untouched.
		err := unix.Unlinkat(parent, name, 0)
		if !selected && errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return err == nil, err
	}
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return false, err
	}
	directory := os.NewFile(uintptr(fd), name)
	defer directory.Close()
	changed := false
	for {
		if err := ctx.Err(); err != nil {
			return changed, err
		}
		names, readErr := directory.Readdirnames(64)
		for _, child := range names {
			removed, err := deleteWorkspaceEntry(ctx, fd, child, false)
			changed = changed || removed
			if err != nil {
				return changed, err
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return changed, readErr
			}
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return changed, err
	}
	if err := unix.Unlinkat(parent, name, unix.AT_REMOVEDIR); err != nil {
		return changed, err
	}
	return true, nil
}
