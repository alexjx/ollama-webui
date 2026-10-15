package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/store"
)

func TestForkConversationAPI(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	original, err := database.CreateConversation(ctx, store.CreateConversationParams{Title: "Original", Model: "offline-model", Mode: "chat", SystemPrompt: "Remember this"})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "artifacts")
	artifacts, err := agent.NewArtifactManager(root, database)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := artifacts.Write(ctx, store.CreateContextArtifactParams{ConversationID: original.ID}, []byte("full context"))
	if err != nil {
		t.Fatal(err)
	}
	api := New(database, nil, agent.Runner{Artifacts: artifacts}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	forkPath := fmt.Sprintf("/api/conversations/%d/fork", original.ID)
	for _, path := range []string{"/api/conversations/no/fork", "/api/conversations/0/fork"} {
		response := searchAPIRequest(api, http.MethodPost, path, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid id: %d", response.Code)
		}
	}
	if response := searchAPIRequest(api, http.MethodPost, "/api/conversations/999/fork", ""); response.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", response.Code)
	}
	api.active[original.ID] = struct{}{}
	if response := searchAPIRequest(api, http.MethodPost, forkPath, ""); response.Code != http.StatusConflict {
		t.Fatalf("active: %d", response.Code)
	}
	delete(api.active, original.ID)
	response := searchAPIRequest(api, http.MethodPost, forkPath, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("fork: %d %s", response.Code, response.Body.String())
	}
	var fork store.Conversation
	if err := json.Unmarshal(response.Body.Bytes(), &fork); err != nil {
		t.Fatal(err)
	}
	if fork.ID == original.ID || fork.Title != "Fork of Original" || fork.Model != original.Model || fork.SystemPrompt != original.SystemPrompt {
		t.Fatalf("fork: %#v", fork)
	}
	copied, err := artifacts.List(ctx, fork.ID)
	if err != nil || len(copied) != 1 {
		t.Fatalf("artifacts: %#v %v", copied, err)
	}
	if copied[0].StorageKey == artifact.StorageKey {
		t.Fatal("artifact key was not remapped")
	}
	deleted := httptest.NewRecorder()
	api.ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/conversations/%d", original.ID), nil))
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", deleted.Code)
	}
	content, err := artifacts.Read(ctx, fork.ID, copied[0].StorageKey, 0, 100)
	if err != nil || string(content.Data) != "full context" {
		t.Fatalf("fork lost context after source deletion: %q %v", content.Data, err)
	}

	// A missing source file fails the transaction without leaving a new session.
	contextRow, err := database.GetConversationContext(ctx, fork.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, contextRow.StorageKey, copied[0].StorageKey)); err != nil {
		t.Fatal(err)
	}
	failed := searchAPIRequest(api, http.MethodPost, fmt.Sprintf("/api/conversations/%d/fork", fork.ID), "")
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("missing artifact: %d %s", failed.Code, failed.Body.String())
	}
	sessions, err := database.ListConversations(ctx, "")
	if err != nil || len(sessions) != 1 {
		t.Fatalf("failed fork left session: %#v %v", sessions, err)
	}
	directories, err := os.ReadDir(root)
	if err != nil || len(directories) != 1 {
		t.Fatalf("failed fork left files: %v %v", directories, err)
	}
}
