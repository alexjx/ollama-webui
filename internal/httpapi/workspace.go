package httpapi

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const workspacePreviewLimit = 256 * 1024
const workspaceDirectoryLimit = 10000
const workspacePageSize = 500

type workspaceEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Type       string    `json:"type"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

// Root confines every lookup even if an agent renames a directory concurrently.
// Nonblocking opens ensure a file replaced with a FIFO cannot hang a request.
func (s *Server) openWorkspace(w http.ResponseWriter, r *http.Request) (*os.File, string, os.FileInfo) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	name := r.URL.Query().Get("path")
	if name == "" {
		name = "."
	}
	if strings.ContainsRune(name, 0) || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		writeError(w, 400, "invalid_path", "Use a relative workspace path.")
		return nil, "", nil
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			writeError(w, 400, "invalid_path", "Parent paths are not allowed.")
			return nil, "", nil
		}
	}
	name = path.Clean(name)
	if s.runtime.Workspace == "" {
		writeError(w, 503, "workspace_unavailable", "Workspace is not configured.")
		return nil, "", nil
	}
	root, err := os.OpenRoot(s.runtime.Workspace)
	if err != nil {
		writeError(w, 503, "workspace_unavailable", "Workspace is unavailable.")
		return nil, "", nil
	}
	defer root.Close()
	// Reject symbolic links in every component; Root also prevents escapes in races.
	prefix := ""
	for _, part := range strings.Split(name, "/") {
		prefix = path.Join(prefix, part)
		info, err := root.Lstat(prefix)
		if err != nil {
			workspaceError(w, err)
			return nil, "", nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			writeError(w, 403, "symlink_not_supported", "Symbolic links cannot be opened.")
			return nil, "", nil
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			writeError(w, 400, "unsupported_file", "Only regular files and directories can be opened.")
			return nil, "", nil
		}
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		workspaceError(w, err)
		return nil, "", nil
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		workspaceError(w, err)
		return nil, "", nil
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		file.Close()
		writeError(w, 400, "unsupported_file", "Only regular files and directories can be opened.")
		return nil, "", nil
	}
	if name == "." {
		name = ""
	}
	return file, name, info
}

func workspaceError(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, 404, "workspace_not_found", "Workspace item no longer exists.")
		return
	}
	writeError(w, 403, "workspace_unreadable", "Workspace item cannot be read.")
}

func (s *Server) workspaceList(w http.ResponseWriter, r *http.Request) {
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			writeError(w, 400, "invalid_offset", "Invalid directory offset.")
			return
		}
		offset = value
	}
	file, name, info := s.openWorkspace(w, r)
	if file == nil {
		return
	}
	defer file.Close()
	if !info.IsDir() {
		writeError(w, 400, "not_directory", "Select a directory.")
		return
	}
	items, err := file.ReadDir(workspaceDirectoryLimit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		workspaceError(w, err)
		return
	}
	if len(items) > workspaceDirectoryLimit {
		writeError(w, 413, "directory_too_large", "This directory has more than 10,000 items and cannot be listed.")
		return
	}
	entries := make([]workspaceEntry, 0, len(items))
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			continue
		}
		kind := "other"
		if info.IsDir() {
			kind = "directory"
		} else if info.Mode().IsRegular() {
			kind = "file"
		} else if info.Mode()&os.ModeSymlink != 0 {
			kind = "symlink"
		}
		entries = append(entries, workspaceEntry{item.Name(), path.Join(name, item.Name()), kind, info.Size(), info.ModTime()})
	}
	sort.Slice(entries, func(i, j int) bool {
		if (entries[i].Type == "directory") != (entries[j].Type == "directory") {
			return entries[i].Type == "directory"
		}
		return entries[i].Name < entries[j].Name
	})
	if offset > len(entries) {
		offset = len(entries)
	}
	end := min(offset+workspacePageSize, len(entries))
	var next *int
	if end < len(entries) {
		next = &end
	}
	writeJSON(w, 200, struct {
		Path       string           `json:"path"`
		Entries    []workspaceEntry `json:"entries"`
		NextOffset *int             `json:"nextOffset"`
	}{name, entries[offset:end], next})
}

func workspaceImageType(data []byte) string {
	kind := http.DetectContentType(data)
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return kind
	}
	return ""
}

func (s *Server) workspacePreview(w http.ResponseWriter, r *http.Request) {
	file, name, info := s.openWorkspace(w, r)
	if file == nil {
		return
	}
	defer file.Close()
	if info.IsDir() {
		writeError(w, 400, "not_file", "Select a regular file.")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, workspacePreviewLimit+1))
	if err != nil {
		workspaceError(w, err)
		return
	}
	result := map[string]any{"path": name, "name": path.Base(name), "size": info.Size(), "kind": "unsupported"}
	if kind := workspaceImageType(data); kind != "" {
		result["kind"] = "image"
		result["mimeType"] = kind
	} else {
		truncated := len(data) > workspacePreviewLimit
		if truncated {
			data = data[:workspacePreviewLimit]
			for len(data) > 0 && !utf8.Valid(data) && len(data) > workspacePreviewLimit-utf8.UTFMax {
				data = data[:len(data)-1]
			}
		}
		if utf8.Valid(data) && !bytes.ContainsRune(data, 0) {
			result["kind"] = "text"
			result["text"] = string(data)
			result["truncated"] = truncated
		}
	}
	writeJSON(w, 200, result)
}

func (s *Server) workspaceFile(w http.ResponseWriter, r *http.Request) {
	file, _, info := s.openWorkspace(w, r)
	if file == nil {
		return
	}
	defer file.Close()
	if info.IsDir() {
		writeError(w, 400, "not_file", "Select a regular file.")
		return
	}
	sample := make([]byte, 512)
	n, err := file.Read(sample)
	if err != nil && !errors.Is(err, io.EOF) {
		workspaceError(w, err)
		return
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		workspaceError(w, err)
		return
	}
	disposition := "attachment"
	contentType := "application/octet-stream"
	if r.URL.Query().Get("download") == "0" {
		if kind := workspaceImageType(sample[:n]); kind != "" {
			disposition = "inline"
			contentType = kind
		} else {
			writeError(w, 415, "unsupported_preview", "Only raster images can be displayed inline.")
			return
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": info.Name()}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}
