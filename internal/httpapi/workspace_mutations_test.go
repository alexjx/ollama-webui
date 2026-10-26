//go:build linux

package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func workspaceMutationRequest(s *Server, operation string, input any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(input)
	r := httptest.NewRequest(http.MethodPost, "/api/workspace/"+operation, bytes.NewReader(body))
	w := httptest.NewRecorder()
	if operation == "folder" {
		s.workspaceCreateFolder(w, r)
	} else {
		s.workspaceMove(w, r)
	}
	return w
}

func workspaceUploadRequest(s *Server, name string, body io.Reader) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/workspace/upload?path="+url.QueryEscape(name), body)
	w := httptest.NewRecorder()
	s.workspaceUpload(w, r)
	return w
}

func TestWorkspaceMutationsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &Server{runtime: RuntimeSettings{Workspace: dir}}
	if w := workspaceMutationRequest(s, "folder", map[string]string{"path": "资料"}); w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	content := []byte("hello 世界\x00\xff\n")
	if w := workspaceUploadRequest(s, "résumé.txt", bytes.NewReader(content)); w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	for _, pair := range [][2]string{{"résumé.txt", "renamed.txt"}, {"renamed.txt", "资料/renamed.txt"}, {"资料/renamed.txt", "back.txt"}, {"资料", "folder"}} {
		w := workspaceMutationRequest(s, "move", map[string]string{"path": pair[0], "destination": pair[1]})
		if w.Code != 200 {
			t.Fatalf("move %v: %d %s", pair, w.Code, w.Body.String())
		}
		if _, err := os.Stat(filepath.Join(dir, pair[0])); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("source remains: %v", err)
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, "back.txt"))
	if err != nil || !bytes.Equal(content, got) {
		t.Fatalf("content changed: %q %v", got, err)
	}
	if w := workspaceUploadRequest(s, "folder/empty", strings.NewReader("")); w.Code != 201 {
		t.Fatalf("empty upload: %d", w.Code)
	}
}

func TestWorkspaceMutationsRejectInvalidPathsAndLinks(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	s := &Server{runtime: RuntimeSettings{Workspace: dir}}
	os.WriteFile(filepath.Join(dir, "file"), []byte("original"), 0600)
	os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(dir, "escape"))
	os.Symlink("file", filepath.Join(dir, "link"))
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "..", "../outside", "/tmp/file", "a/../file", "a\\b", "a//b", "a/", "a\x00b", "escape/secret", "link/child", "missing/child"} {
		t.Run(name, func(t *testing.T) {
			if w := workspaceMutationRequest(s, "folder", map[string]string{"path": name}); w.Code < 400 {
				t.Errorf("folder accepted: %s", w.Body.String())
			}
			if w := workspaceUploadRequest(s, name, strings.NewReader("replacement")); w.Code < 400 {
				t.Errorf("upload accepted: %s", w.Body.String())
			}
			for _, pair := range [][2]string{{name, "new"}, {"file", name}} {
				if w := workspaceMutationRequest(s, "move", map[string]string{"path": pair[0], "destination": pair[1]}); w.Code < 400 {
					t.Errorf("move accepted: %s", w.Body.String())
				}
			}
		})
	}
	for _, name := range []string{"link", "fifo"} {
		if w := workspaceMutationRequest(s, "move", map[string]string{"path": name, "destination": "new"}); w.Code < 400 {
			t.Errorf("special source accepted: %s", name)
		}
	}
	got, _ := os.ReadFile(filepath.Join(outside, "secret"))
	if string(got) != "secret" {
		t.Fatal("outside file changed")
	}
	got, _ = os.ReadFile(filepath.Join(dir, "file"))
	if string(got) != "original" {
		t.Fatal("source changed")
	}
}

func TestWorkspaceMutationsNeverOverwrite(t *testing.T) {
	dir := t.TempDir()
	s := &Server{runtime: RuntimeSettings{Workspace: dir}}
	os.WriteFile(filepath.Join(dir, "source"), []byte("source"), 0600)
	os.WriteFile(filepath.Join(dir, "target"), []byte("target"), 0600)
	os.Mkdir(filepath.Join(dir, "folder"), 0700)
	os.Mkdir(filepath.Join(dir, "other"), 0700)
	os.Symlink("target", filepath.Join(dir, "link"))
	for _, destination := range []string{"target", "folder", "link"} {
		if w := workspaceUploadRequest(s, destination, strings.NewReader("overwrite")); w.Code != 409 {
			t.Errorf("upload conflict %s: %d", destination, w.Code)
		}
		if w := workspaceMutationRequest(s, "folder", map[string]string{"path": destination}); w.Code != 409 {
			t.Errorf("folder conflict %s: %d", destination, w.Code)
		}
		if w := workspaceMutationRequest(s, "move", map[string]string{"path": "source", "destination": destination}); w.Code != 409 {
			t.Errorf("move conflict %s: %d", destination, w.Code)
		}
	}
	if w := workspaceMutationRequest(s, "move", map[string]string{"path": "other", "destination": "folder"}); w.Code != 409 {
		t.Errorf("directory conflict: %d", w.Code)
	}
	for _, destination := range []string{"folder", "folder/child"} {
		if w := workspaceMutationRequest(s, "move", map[string]string{"path": "folder", "destination": destination}); w.Code != 400 {
			t.Errorf("self move: %d", w.Code)
		}
	}
	got, _ := os.ReadFile(filepath.Join(dir, "target"))
	if string(got) != "target" {
		t.Fatal("target overwritten")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 5 {
		t.Fatalf("temporary files leaked: %v", entries)
	}
}

type failingUploadReader struct{ sent bool }

func (r *failingUploadReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, errors.New("client disconnected")
	}
	r.sent = true
	return copy(p, "partial"), nil
}

func TestWorkspaceUploadBoundsAndCleanup(t *testing.T) {
	dir := t.TempDir()
	s := &Server{runtime: RuntimeSettings{Workspace: dir}}
	// Hide Content-Length to exercise the streamed limit rather than the early check.
	body := io.LimitReader(infiniteZeroReader{}, workspaceUploadLimit+1)
	if w := workspaceUploadRequest(s, "large", body); w.Code != 413 {
		t.Fatalf("stream bound: %d %s", w.Code, w.Body.String())
	}
	if w := workspaceUploadRequest(s, "partial", &failingUploadReader{}); w.Code < 400 {
		t.Fatalf("failed upload accepted: %d", w.Code)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("partial files left behind: %v", entries)
	}
	if w := workspaceUploadRequest(s, "exact", io.LimitReader(infiniteZeroReader{}, workspaceUploadLimit)); w.Code != 201 {
		t.Fatalf("exact limit: %d %s", w.Code, w.Body.String())
	}
	info, err := os.Stat(filepath.Join(dir, "exact"))
	if err != nil || info.Size() != workspaceUploadLimit {
		t.Fatalf("exact size: %v %v", info, err)
	}
}

type infiniteZeroReader struct{}

func (infiniteZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestWorkspaceConcurrentUploadsHaveOneWinner(t *testing.T) {
	dir := t.TempDir()
	s := &Server{runtime: RuntimeSettings{Workspace: dir}}
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for i := 0; i < cap(codes); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- workspaceUploadRequest(s, "winner", strings.NewReader("complete")).Code
		}()
	}
	wg.Wait()
	close(codes)
	winners := 0
	for code := range codes {
		if code == 201 {
			winners++
		} else if code != 409 {
			t.Errorf("unexpected status: %d", code)
		}
	}
	if winners != 1 {
		t.Fatalf("winners: %d", winners)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files remain: %v", entries)
	}
}
