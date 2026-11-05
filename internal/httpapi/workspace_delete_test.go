//go:build linux

package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"ollama-webui/internal/agent"
)

func workspaceDeleteRequest(s *Server, name string, ctx context.Context) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodDelete, "/api/workspace/file?path="+url.QueryEscape(name), nil).WithContext(ctx)
	w := httptest.NewRecorder()
	s.workspaceDelete(w, r)
	return w
}

func TestWorkspaceDeleteUnicodeFileAndNestedFolderThroughRoute(t *testing.T) {
	workspace := t.TempDir()
	api := New(nil, nil, agent.Runner{}, RuntimeSettings{Workspace: workspace}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer api.Close()
	for _, name := range []string{"日志 世界.txt", "资料/内层/报告.txt"} {
		file := filepath.Join(workspace, name)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("exact original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"日志 世界.txt", "资料"} {
		w := httptest.NewRecorder()
		api.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/workspace/file?path="+url.QueryEscape(name), nil))
		if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
			t.Fatalf("delete %q: %d %s", name, w.Code, w.Body.String())
		}
		if _, err := os.Lstat(filepath.Join(workspace, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("selected item remains: %v", err)
		}
	}
	if _, err := os.Stat(workspace); err != nil {
		t.Fatalf("workspace root was removed: %v", err)
	}
}

func TestWorkspaceDeleteRejectsRootTraversalLinksAndSpecialTargets(t *testing.T) {
	workspace, outside := t.TempDir(), t.TempDir()
	s := &Server{runtime: RuntimeSettings{Workspace: workspace}}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "regular"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("regular", filepath.Join(workspace, "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(workspace, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "..", "../secret", "/", "/tmp/file", "a/../regular", "a//b", "a/", "a\\b", "a\x00b"} {
		if w := workspaceDeleteRequest(s, name, context.Background()); w.Code != 400 {
			t.Errorf("unsafe path %q: %d %s", name, w.Code, w.Body.String())
		}
	}
	for _, name := range []string{"escape", "escape/secret", "link"} {
		if w := workspaceDeleteRequest(s, name, context.Background()); w.Code != 403 {
			t.Errorf("symlink path %q: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := workspaceDeleteRequest(s, "fifo", context.Background()); w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported_file") {
		t.Fatalf("special target accepted: %d %s", w.Code, w.Body.String())
	}
	for _, file := range []string{filepath.Join(workspace, "regular"), filepath.Join(outside, "secret")} {
		data, err := os.ReadFile(file)
		if err != nil || string(data) != "keep" {
			t.Fatalf("protected file changed: %q %v", data, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(workspace, "link")); err != nil {
		t.Fatalf("selected symlink was modified: %v", err)
	}
}

func TestWorkspaceDeleteNestedLinksNeverFollowsTargets(t *testing.T) {
	workspace, outside := t.TempDir(), t.TempDir()
	folder := filepath.Join(workspace, "selected")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("external data"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"external-dir": outside, "external-file": filepath.Join(outside, "keep.txt"), "broken": filepath.Join(outside, "missing")} {
		if err := os.Symlink(target, filepath.Join(folder, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mkfifo(filepath.Join(folder, "nested-fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{runtime: RuntimeSettings{Workspace: workspace}}
	if w := workspaceDeleteRequest(s, "selected", context.Background()); w.Code != 204 {
		t.Fatalf("recursive deletion failed: %d %s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(outside, "keep.txt"))
	if err != nil || string(data) != "external data" {
		t.Fatalf("nested symlink target changed: %q %v", data, err)
	}
	if _, err := os.Stat(folder); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selected directory remains: %v", err)
	}
}

type cancelAfterEntryDeleted struct {
	context.Context
	cancel  context.CancelFunc
	folder  string
	initial int
}

func (ctx cancelAfterEntryDeleted) Err() error {
	if entries, err := os.ReadDir(ctx.folder); err == nil && len(entries) < ctx.initial {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestWorkspaceDeleteCancellationAndPartialFeedback(t *testing.T) {
	workspace := t.TempDir()
	s := &Server{runtime: RuntimeSettings{Workspace: workspace}}
	folder := filepath.Join(workspace, "selected")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two", "three"} {
		if err := os.WriteFile(filepath.Join(folder, name), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if w := workspaceDeleteRequest(s, "selected", ctx); w.Code != 408 {
		t.Fatalf("cancelled request succeeded: %d %s", w.Code, w.Body.String())
	}
	entries, err := os.ReadDir(folder)
	if err != nil || len(entries) != 3 {
		t.Fatalf("pre-cancelled operation deleted data: %v %v", entries, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	partialCtx := cancelAfterEntryDeleted{Context: ctx, cancel: cancel, folder: folder, initial: 3}
	w := workspaceDeleteRequest(s, "selected", partialCtx)
	if w.Code != 408 || !strings.Contains(w.Body.String(), "already deleted") {
		t.Fatalf("partial deletion was not reported: %d %s", w.Code, w.Body.String())
	}
	entries, err = os.ReadDir(folder)
	if err != nil || len(entries) != 2 {
		t.Fatalf("deletion did not stop after cancellation: %v %v", entries, err)
	}
}

func TestWorkspaceDeleteMissingAndPermissionErrors(t *testing.T) {
	workspace := t.TempDir()
	s := &Server{runtime: RuntimeSettings{Workspace: workspace}}
	if w := workspaceDeleteRequest(s, "missing", context.Background()); w.Code != 404 || !strings.Contains(w.Body.String(), "no longer exists") {
		t.Fatalf("missing target response: %d %s", w.Code, w.Body.String())
	}
	if os.Geteuid() == 0 {
		t.Skip("root can override filesystem permissions")
	}
	folder := filepath.Join(workspace, "readonly")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "keep"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(folder, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(folder, 0700)
	if w := workspaceDeleteRequest(s, "readonly", context.Background()); w.Code != 403 || !strings.Contains(w.Body.String(), "permissions") {
		t.Fatalf("permission error response: %d %s", w.Code, w.Body.String())
	}
}
