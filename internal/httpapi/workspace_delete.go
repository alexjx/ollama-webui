package httpapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"syscall"
)

var errUnsupportedWorkspaceDelete = errors.New("only regular files and directories can be deleted")

func (s *Server) workspaceDelete(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	if !validWorkspaceMutationPath(name) {
		writeError(w, 400, "invalid_path", "Choose a file or folder inside the workspace; the workspace root cannot be deleted.")
		return
	}
	root := s.mutationWorkspace(w)
	if root == nil {
		return
	}
	defer root.Close()
	changed, err := deleteWorkspaceItem(r.Context(), root, name)
	if err != nil {
		workspaceDeleteError(w, err, changed)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func workspaceDeleteError(w http.ResponseWriter, err error, changed bool) {
	partial := ""
	if changed {
		partial = " Some folder contents were already deleted. Refresh the workspace before retrying."
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeError(w, 408, "workspace_delete_cancelled", "Deletion stopped before it finished."+partial)
	case changed:
		writeError(w, 409, "workspace_delete_incomplete", "The folder could not be completely deleted. Check permissions or concurrent file changes."+partial)
	case errors.Is(err, os.ErrNotExist):
		writeError(w, 404, "workspace_not_found", "Workspace item no longer exists.")
	case errors.Is(err, syscall.ELOOP):
		writeError(w, 403, "symlink_not_supported", "Symbolic links cannot be selected for deletion or used as parent folders.")
	case errors.Is(err, errUnsupportedWorkspaceDelete):
		writeError(w, 400, "unsupported_file", "Only regular files and directories can be deleted.")
	case errors.Is(err, syscall.ENOSYS), errors.Is(err, syscall.ENOTSUP):
		writeError(w, 501, "workspace_operation_unsupported", "Safe workspace deletion is not supported on this system.")
	case errors.Is(err, syscall.ENOTEMPTY):
		writeError(w, 409, "workspace_changed", "The folder changed during deletion. Refresh the workspace and try again.")
	case errors.Is(err, syscall.EINVAL), errors.Is(err, syscall.ENOTDIR):
		writeError(w, 400, "invalid_path", "Choose an existing file or folder inside the workspace.")
	default:
		writeError(w, 403, "workspace_delete_failed", "Workspace item could not be deleted. Check its permissions and try again.")
	}
}
