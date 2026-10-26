package httpapi

import (
	"errors"
	"net/http"
	"os"
	"path"
	"strings"
	"syscall"
)

const workspaceUploadLimit int64 = 32 << 20

// Mutations accept canonical, relative paths only. In particular, no operation
// can rename the workspace root or hide a parent traversal through cleaning.
func validWorkspaceMutationPath(name string) bool {
	if name == "" || name == "." || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") || path.Clean(name) != name {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func (s *Server) mutationWorkspace(w http.ResponseWriter) *os.Root {
	w.Header().Set("Cache-Control", "no-store")
	if s.runtime.Workspace == "" {
		writeError(w, 503, "workspace_unavailable", "Workspace is not configured.")
		return nil
	}
	root, err := os.OpenRoot(s.runtime.Workspace)
	if err != nil {
		writeError(w, 503, "workspace_unavailable", "Workspace is unavailable.")
		return nil
	}
	return root
}

func workspaceMutationError(w http.ResponseWriter, err error) {
	var sizeErr *http.MaxBytesError
	switch {
	case errors.As(err, &sizeErr):
		writeError(w, 413, "upload_too_large", "Files must be 32 MiB or smaller.")
	case errors.Is(err, os.ErrExist), errors.Is(err, syscall.ENOTEMPTY):
		writeError(w, 409, "workspace_conflict", "An item with this name already exists. Choose another name.")
	case errors.Is(err, os.ErrNotExist):
		writeError(w, 404, "workspace_not_found", "Workspace item or destination folder no longer exists.")
	case errors.Is(err, syscall.ELOOP):
		writeError(w, 403, "symlink_not_supported", "Symbolic links cannot be modified or used as folders.")
	case errors.Is(err, syscall.EINVAL), errors.Is(err, syscall.ENOTDIR):
		writeError(w, 400, "invalid_destination", "Choose an existing folder outside the item being moved.")
	case errors.Is(err, syscall.ENOSYS), errors.Is(err, syscall.ENOTSUP):
		writeError(w, 501, "workspace_operation_unsupported", "Safe workspace modifications are not supported on this system.")
	default:
		writeError(w, 403, "workspace_write_failed", "Workspace item could not be modified. Check its permissions and try again.")
	}
}

func (s *Server) workspaceCreateFolder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if decodeJSON(r, &input) != nil || !validWorkspaceMutationPath(input.Path) {
		writeError(w, 400, "invalid_path", "Use a relative workspace path with a folder name.")
		return
	}
	root := s.mutationWorkspace(w)
	if root == nil {
		return
	}
	defer root.Close()
	if err := createWorkspaceFolder(root, input.Path); err != nil {
		workspaceMutationError(w, err)
		return
	}
	writeJSON(w, 201, map[string]string{"path": input.Path})
}

func (s *Server) workspaceMove(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path        string `json:"path"`
		Destination string `json:"destination"`
	}
	if decodeJSON(r, &input) != nil || !validWorkspaceMutationPath(input.Path) || !validWorkspaceMutationPath(input.Destination) {
		writeError(w, 400, "invalid_path", "Use relative workspace paths with item names.")
		return
	}
	if input.Path == input.Destination || strings.HasPrefix(input.Destination, input.Path+"/") {
		writeError(w, 400, "invalid_destination", "Choose a different name or a folder outside the item being moved.")
		return
	}
	root := s.mutationWorkspace(w)
	if root == nil {
		return
	}
	defer root.Close()
	if err := moveWorkspaceItem(root, input.Path, input.Destination); err != nil {
		workspaceMutationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"path": input.Destination})
}

func (s *Server) workspaceUpload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	if !validWorkspaceMutationPath(name) {
		writeError(w, 400, "invalid_path", "Use a relative workspace path with a file name.")
		return
	}
	if r.ContentLength > workspaceUploadLimit {
		workspaceMutationError(w, &http.MaxBytesError{Limit: workspaceUploadLimit})
		return
	}
	root := s.mutationWorkspace(w)
	if root == nil {
		return
	}
	defer root.Close()
	body := http.MaxBytesReader(w, r.Body, workspaceUploadLimit)
	defer body.Close()
	if err := uploadWorkspaceFile(root, name, body); err != nil {
		workspaceMutationError(w, err)
		return
	}
	writeJSON(w, 201, map[string]string{"path": name})
}
