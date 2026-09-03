package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/store"
)

func TestUpdateConversationTitleTrimsAndPersists(t *testing.T) {
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(context.Background(), store.CreateConversationParams{Title: "Original", Model: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	api := New(database, nil, agent.Runner{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	request := httptest.NewRequest(http.MethodPatch, "/api/conversations/1", strings.NewReader(`{"title":"  Renamed chat  "}`))
	request.SetPathValue("id", "1")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var updated store.Conversation
	if err := json.NewDecoder(response.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	stored, err := database.GetConversation(context.Background(), conversation.ID)
	if err != nil || updated.Title != "Renamed chat" || stored.Title != "Renamed chat" {
		t.Fatalf("renamed title did not persist: response=%#v stored=%#v err=%v", updated, stored, err)
	}

	request = httptest.NewRequest(http.MethodPatch, "/api/conversations/1", strings.NewReader(`{"title":"   "}`))
	request.SetPathValue("id", "1")
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("empty title returned %d: %s", response.Code, response.Body.String())
	}
	stored, err = database.GetConversation(context.Background(), conversation.ID)
	if err != nil || stored.Title != "Renamed chat" {
		t.Fatalf("invalid rename changed the title: %#v, %v", stored, err)
	}
}

func TestDeleteConversationCascadesAndPreservesOthers(t *testing.T) {
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	deleted, err := database.CreateConversation(context.Background(), store.CreateConversationParams{Title: "Delete me", Model: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := database.CreateConversation(context.Background(), store.CreateConversationParams{Title: "Keep me", Model: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.AddMessageWithAttachments(context.Background(), deleted.ID, "user", "private content", "complete", []store.NewAttachment{{
		FileName: "pixel.png", MediaType: "image/png", Data: []byte{1, 2, 3},
	}})
	if err != nil {
		t.Fatal(err)
	}
	api := New(database, nil, agent.Runner{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	request := httptest.NewRequest(http.MethodDelete, "/api/conversations/1", nil)
	request.SetPathValue("id", "1")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	if _, err := database.GetConversation(context.Background(), deleted.ID); err != store.ErrNotFound {
		t.Fatalf("deleted conversation still exists: %v", err)
	}
	if _, err := database.GetAttachment(context.Background(), message.Attachments[0].ID); err != store.ErrNotFound {
		t.Fatalf("dependent attachment still exists: %v", err)
	}
	if conversation, err := database.GetConversation(context.Background(), kept.ID); err != nil || conversation.Title != "Keep me" {
		t.Fatalf("unrelated conversation changed: %#v, %v", conversation, err)
	}
}
