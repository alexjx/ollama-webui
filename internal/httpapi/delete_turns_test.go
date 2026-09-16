package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/store"
)

func TestDeleteTurnsEndpoint(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conversation, err := db.CreateConversation(ctx, store.CreateConversationParams{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.AddMessage(ctx, conversation.ID, "user", "wrong input", "complete")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := db.AddMessage(ctx, conversation.ID, "assistant", "partial response", "cancelled")
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	manager, err := agent.NewArtifactManager(artifactRoot, db)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := manager.Write(ctx, store.CreateContextArtifactParams{ConversationID: conversation.ID, SourceMessageID: &assistant.ID}, []byte("removed artifact"))
	if err != nil {
		t.Fatal(err)
	}
	retained, err := manager.Write(ctx, store.CreateContextArtifactParams{ConversationID: conversation.ID}, []byte("independent artifact"))
	if err != nil {
		t.Fatal(err)
	}
	conversationContext, err := db.GetConversationContext(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	api := New(db, nil, agent.Runner{Artifacts: manager}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	remove := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		api.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, path, nil))
		return response
	}
	path := fmt.Sprintf("/api/conversations/%d/messages/%d", conversation.ID, user.ID)
	api.active[conversation.ID] = struct{}{}
	response := remove(path)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "generation_active") {
		t.Fatalf("active: %d %s", response.Code, response.Body)
	}
	messages, err := db.Messages(ctx, conversation.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("active history changed: %v %v", messages, err)
	}
	delete(api.active, conversation.ID)
	for _, test := range []struct {
		path   string
		status int
	}{
		{"/api/conversations/1/messages/0", http.StatusBadRequest},
		{"/api/conversations/1/messages/no", http.StatusBadRequest},
		{"/api/conversations/no/messages/1", http.StatusBadRequest},
		{fmt.Sprintf("/api/conversations/1/messages/%d", assistant.ID), http.StatusBadRequest},
		{"/api/conversations/2/messages/1", http.StatusNotFound},
		{"/api/conversations/1/messages/999", http.StatusNotFound},
	} {
		response := remove(test.path)
		if response.Code != test.status {
			t.Fatalf("%s: %d %s", test.path, response.Code, response.Body)
		}
	}
	response = remove(path)
	var payload struct {
		Conversation store.Conversation `json:"conversation"`
		Messages     []store.Message    `json:"messages"`
	}
	if response.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", response.Code, response.Body)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Conversation.ID != conversation.ID || payload.Conversation.MessageCount != 0 || payload.Messages == nil || len(payload.Messages) != 0 {
		t.Fatalf("payload=%+v", payload)
	}
	if _, err := os.Stat(filepath.Join(artifactRoot, conversationContext.StorageKey, removed.StorageKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted artifact file survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(artifactRoot, conversationContext.StorageKey, retained.StorageKey)); err != nil {
		t.Fatalf("independent artifact changed: %v", err)
	}
	if response := remove(path); response.Code != http.StatusNotFound {
		t.Fatalf("repeat: %d %s", response.Code, response.Body)
	}
}
