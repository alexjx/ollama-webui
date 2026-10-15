package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func workspaceRequest(s *Server, endpoint, name string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, endpoint+"path="+url.QueryEscape(name), nil)
	switch {
	case strings.Contains(endpoint, "/preview"):
		s.workspacePreview(w, r)
	case strings.Contains(endpoint, "/file"):
		s.workspaceFile(w, r)
	default:
		s.workspaceList(w, r)
	}
	return w
}
func TestWorkspaceBrowseAndPreview(t *testing.T) {
	dir := t.TempDir()
	s := &Server{runtime: RuntimeSettings{Workspace: dir}}
	os.Mkdir(filepath.Join(dir, "folder"), 0700)
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("hidden"), 0600)
	os.WriteFile(filepath.Join(dir, "text.txt"), []byte("hello 世界"), 0600)
	w := workspaceRequest(s, "/api/workspace?", "")
	var listing struct {
		Path       string
		Entries    []workspaceEntry
		NextOffset *int
	}
	json.Unmarshal(w.Body.Bytes(), &listing)
	if w.Code != 200 || len(listing.Entries) != 3 || listing.Entries[0].Type != "directory" || listing.Entries[1].Name != ".hidden" || listing.NextOffset != nil {
		t.Fatalf("listing: %d %s", w.Code, w.Body.String())
	}
	w = workspaceRequest(s, "/api/workspace/preview?", "text.txt")
	var preview map[string]any
	json.Unmarshal(w.Body.Bytes(), &preview)
	if w.Code != 200 || preview["text"] != "hello 世界" || preview["kind"] != "text" || preview["truncated"] != false {
		t.Fatalf("preview: %s", w.Body.String())
	}
	content := []byte(strings.Repeat("界", workspacePreviewLimit))
	os.WriteFile(filepath.Join(dir, "large.txt"), content, 0600)
	w = workspaceRequest(s, "/api/workspace/preview?", "large.txt")
	json.Unmarshal(w.Body.Bytes(), &preview)
	if preview["truncated"] != true || len(preview["text"].(string)) > workspacePreviewLimit {
		t.Fatalf("unbounded text")
	}
	w = workspaceRequest(s, "/api/workspace/file?", "large.txt")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), content) || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("download mismatch: %d", w.Code)
	}
	for _, tt := range []struct {
		endpoint, path string
		code           int
	}{{"/api/workspace?", "text.txt", 400}, {"/api/workspace/preview?", "folder", 400}, {"/api/workspace/file?", "folder", 400}, {"/api/workspace?", "absent", 404}, {"/api/workspace?offset=-1&", "", 400}} {
		w := workspaceRequest(s, tt.endpoint, tt.path)
		if w.Code != tt.code {
			t.Errorf("%s %s: %d", tt.endpoint, tt.path, w.Code)
		}
	}
}
func TestWorkspaceRejectsEscapesAndSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600)
	os.WriteFile(filepath.Join(dir, "inside"), []byte("inside"), 0600)
	os.Symlink(outside, filepath.Join(dir, "escape"))
	os.Symlink("inside", filepath.Join(dir, "link"))
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{runtime: RuntimeSettings{Workspace: dir}}
	for _, name := range []string{"../secret", "nested/../../secret", filepath.Join(outside, "secret"), "escape/secret", "link", "fifo", "a\\b"} {
		w := workspaceRequest(s, "/api/workspace/preview?", name)
		if w.Code < 400 {
			t.Errorf("allowed %q: %d %s", name, w.Code, w.Body.String())
		}
	}
	w := workspaceRequest(s, "/api/workspace?", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"type":"symlink"`) {
		t.Fatalf("symlink not listed: %s", w.Body.String())
	}
	s.runtime.Workspace = ""
	if w := workspaceRequest(s, "/api/workspace?", ""); w.Code != 503 {
		t.Fatalf("unconfigured workspace: %d", w.Code)
	}
}
func TestWorkspaceSafePreviewTypes(t *testing.T) {
	dir := t.TempDir()
	s := &Server{runtime: RuntimeSettings{Workspace: dir}}
	for _, tt := range []struct {
		name   string
		data   []byte
		kind   string
		inline int
	}{
		{"page.html", []byte("<script>alert(1)</script>"), "text", 415},
		{"vector.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), "text", 415},
		{"binary", []byte{0, 1, 2, 255}, "unsupported", 415},
		{"image.png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...), "image", 200},
	} {
		os.WriteFile(filepath.Join(dir, tt.name), tt.data, 0600)
		w := workspaceRequest(s, "/api/workspace/preview?", tt.name)
		var preview map[string]any
		json.Unmarshal(w.Body.Bytes(), &preview)
		if preview["kind"] != tt.kind {
			t.Errorf("%s: %s", tt.name, w.Body.String())
		}
		w = workspaceRequest(s, "/api/workspace/file?download=0&", tt.name)
		if w.Code != tt.inline {
			t.Errorf("inline %s: %d", tt.name, w.Code)
		}
		w = workspaceRequest(s, "/api/workspace/file?download=1&", tt.name)
		if w.Code != 200 || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Type") != "application/octet-stream" || !bytes.Equal(w.Body.Bytes(), tt.data) {
			t.Errorf("download %s unsafe", tt.name)
		}
	}
}
func TestWorkspacePagination(t *testing.T) {
	dir := t.TempDir()
	s := &Server{runtime: RuntimeSettings{Workspace: dir}}
	for i := 0; i < workspacePageSize+1; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%04d", i)), nil, 0600)
	}
	w := workspaceRequest(s, "/api/workspace?", "")
	var listing struct {
		Entries    []workspaceEntry
		NextOffset *int
	}
	json.Unmarshal(w.Body.Bytes(), &listing)
	if len(listing.Entries) != workspacePageSize || listing.NextOffset == nil || *listing.NextOffset != workspacePageSize {
		t.Fatalf("page: %s", w.Body.String())
	}
	w = workspaceRequest(s, "/api/workspace?offset=500&", "")
	json.Unmarshal(w.Body.Bytes(), &listing)
	if len(listing.Entries) != 1 || listing.NextOffset != nil || listing.Entries[0].Name != "file-0500" {
		t.Fatalf("last page: %s", w.Body.String())
	}
}

func TestWorkspaceConcurrentSymlinkSwapCannotEscape(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "secret"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	stopped, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stopped:
				return
			default:
			}
			if os.Rename(nested, filepath.Join(dir, "parked")) == nil {
				os.Symlink(outside, nested)
				os.Remove(nested)
				os.Rename(filepath.Join(dir, "parked"), nested)
			}
		}
	}()
	defer func() { close(stopped); <-done }()
	s := &Server{runtime: RuntimeSettings{Workspace: dir}}
	for i := 0; i < 150; i++ {
		w := workspaceRequest(s, "/api/workspace/file?", "nested/secret")
		if strings.Contains(w.Body.String(), "outside-secret") {
			t.Fatal("concurrent rename escaped workspace")
		}
		if w.Code == 200 && w.Body.String() != "inside" {
			t.Fatalf("unexpected contents: %q", w.Body.String())
		}
	}
}
