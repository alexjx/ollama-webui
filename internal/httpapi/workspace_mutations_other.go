//go:build !linux

package httpapi

import (
	"context"
	"io"
	"os"
	"syscall"
)

// Do not substitute a check-then-rename implementation: it could overwrite files
// created by an agent between the check and rename.
func createWorkspaceFolder(*os.Root, string) error          { return syscall.ENOSYS }
func moveWorkspaceItem(*os.Root, string, string) error      { return syscall.ENOSYS }
func uploadWorkspaceFile(*os.Root, string, io.Reader) error { return syscall.ENOSYS }
func deleteWorkspaceItem(context.Context, *os.Root, string) (bool, error) {
	return false, syscall.ENOSYS
}
