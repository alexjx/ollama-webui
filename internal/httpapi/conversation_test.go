package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/store"
)

func TestCreateConversationPersistsAndValidatesMode(t *testing.T) {
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	api := New(database, nil, agent.Runner{}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	request := httptest.NewRequest(http.MethodPost, "/api/conversations", strings.NewReader(`{"model":"plain","mode":"chat","thinking_mode":"medium"}`))
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var conversation store.Conversation
	if err := json.NewDecoder(response.Body).Decode(&conversation); err != nil || conversation.Mode != "chat" || conversation.ThinkingMode == nil || *conversation.ThinkingMode != "medium" {
		t.Fatalf("chat mode was not returned: %#v, %v", conversation, err)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/conversations", strings.NewReader(`{"model":"plain","mode":"automatic"}`))
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "mode must be agent or chat") {
		t.Fatalf("invalid mode returned %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/conversations", strings.NewReader(`{"model":"plain","mode":"chat","thinking_mode":"extreme"}`))
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "thinking_mode must be") {
		t.Fatalf("invalid thinking mode returned %d: %s", response.Code, response.Body.String())
	}
}

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
	api := New(database, nil, agent.Runner{}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))

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
	contextRoot := filepath.Join(t.TempDir(), "context")
	artifacts, err := agent.NewArtifactManager(contextRoot, database)
	if err != nil {
		t.Fatal(err)
	}
	deletedArtifact, err := artifacts.Write(context.Background(), store.CreateContextArtifactParams{ConversationID: deleted.ID}, []byte("deleted context"))
	if err != nil {
		t.Fatal(err)
	}
	keptArtifact, err := artifacts.Write(context.Background(), store.CreateContextArtifactParams{ConversationID: kept.ID}, []byte("kept context"))
	if err != nil {
		t.Fatal(err)
	}
	deletedContext, _ := database.GetConversationContext(context.Background(), deleted.ID)
	keptContext, _ := database.GetConversationContext(context.Background(), kept.ID)
	api := New(database, nil, agent.Runner{Artifacts: artifacts}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))

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
	if _, err := os.Stat(filepath.Join(contextRoot, deletedContext.StorageKey, deletedArtifact.StorageKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted conversation artifact survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(contextRoot, keptContext.StorageKey, keptArtifact.StorageKey)); err != nil {
		t.Fatalf("unrelated conversation artifact changed: %v", err)
	}
}

func TestSystemSettingsAndBulkClear(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(ctx, store.CreateConversationParams{Title: "Clear me", Model: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddMessage(ctx, conversation.ID, "user", "hello", "complete"); err != nil {
		t.Fatal(err)
	}
	contextRoot := filepath.Join(t.TempDir(), "context")
	artifacts, err := agent.NewArtifactManager(contextRoot, database)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := artifacts.Write(ctx, store.CreateContextArtifactParams{ConversationID: conversation.ID}, []byte("clear context"))
	if err != nil {
		t.Fatal(err)
	}
	conversationContext, _ := database.GetConversationContext(ctx, conversation.ID)
	runner := agent.Runner{
		Shell:  agent.ShellExecutor{Workspace: "/workspace", Timeout: 10 * time.Minute, MaxOutput: 65536},
		Stager: agent.InputStager{MaxInlineBytes: 16384}, MaxTurns: 200, ToolFeedbackLimit: 8192,
		Artifacts: artifacts,
	}
	api := New(database, nil, runner, RuntimeSettings{
		Workspace: "/workspace", MaxTurns: 200, ShellTimeout: 10 * time.Minute,
		ShellMaxOutput: 65536, InlineInputMax: 16384, ToolFeedbackLimit: 8192,
		ContextDirectory: contextRoot, ContextTokens: 32768,
	}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	request := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected settings status %d: %s", response.Code, response.Body.String())
	}
	var settings struct {
		Agent struct {
			Workspace     string `json:"workspace"`
			MaxTurns      int    `json:"max_turns"`
			ContextPath   string `json:"context_path"`
			ContextBudget int    `json:"context_budget_tokens"`
		} `json:"agent"`
		Storage store.Stats `json:"storage"`
	}
	if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if settings.Agent.Workspace != "/workspace" || settings.Agent.MaxTurns != 200 || settings.Agent.ContextPath != contextRoot || settings.Agent.ContextBudget != 32768 || settings.Storage.Conversations != 1 || settings.Storage.Messages != 1 || settings.Storage.ContextArtifacts != 1 {
		t.Fatalf("unexpected settings payload: %#v", settings)
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/conversations", strings.NewReader(`{}`))
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("bulk clear without confirmation returned %d: %s", response.Code, response.Body.String())
	}

	api.active[conversation.ID] = struct{}{}
	request = httptest.NewRequest(http.MethodDelete, "/api/conversations", strings.NewReader(`{"confirmation":"DELETE"}`))
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("bulk clear during generation returned %d: %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodDelete, "/api/conversations/1", nil)
	request.SetPathValue("id", "1")
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("single delete during generation returned %d: %s", response.Code, response.Body.String())
	}
	delete(api.active, conversation.ID)

	request = httptest.NewRequest(http.MethodDelete, "/api/conversations", strings.NewReader(`{"confirmation":"DELETE"}`))
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"deleted":1`) {
		t.Fatalf("unexpected clear response %d: %s", response.Code, response.Body.String())
	}
	if _, err := database.GetConversation(ctx, conversation.ID); err != store.ErrNotFound {
		t.Fatalf("bulk clear preserved conversation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(contextRoot, conversationContext.StorageKey, artifact.StorageKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bulk clear preserved context artifact: %v", err)
	}
}
