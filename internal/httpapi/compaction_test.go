package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

type compactReply struct {
	Compacted  bool                     `json:"compacted"`
	Before     int                      `json:"before_tokens"`
	Estimated  int                      `json:"estimated_tokens"`
	Reason     string                   `json:"reason"`
	Checkpoint *store.ContextCheckpoint `json:"context_checkpoint"`
}

func compactionDatabase(t *testing.T, mode, model string) (*store.Store, store.Conversation) {
	t.Helper()
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	window := 32768
	conversation, err := database.CreateConversation(context.Background(), store.CreateConversationParams{Model: model, Mode: mode, ContextWindow: &window, SystemPrompt: "CUSTOM_SYSTEM"})
	if err != nil {
		t.Fatal(err)
	}
	for index, content := range []string{strings.Repeat("OLD_USER", 300), strings.Repeat("OLD_ASSISTANT", 300), "recent user one", "recent answer one", "recent user two", "recent answer two"} {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		var attachments []store.NewAttachment
		if index == 0 {
			attachments = []store.NewAttachment{{FileName: "old.txt", MediaType: "text/plain", Data: []byte("OLD_FILE_CONTENT")}, {FileName: "old.png", MediaType: "image/png", Data: []byte{1, 2}}}
		}
		if index == 2 {
			attachments = []store.NewAttachment{{FileName: "recent.txt", MediaType: "text/plain", Data: []byte("RECENT_FILE_CONTENT")}, {FileName: "recent.png", MediaType: "image/png", Data: []byte{3, 4}}}
		}
		if _, err := database.AddMessageWithAttachments(context.Background(), conversation.ID, role, content, "complete", attachments); err != nil {
			t.Fatal(err)
		}
	}
	return database, conversation
}

func TestManualCompactionPersistsAndContinuesBothModes(t *testing.T) {
	for _, test := range []struct {
		mode               string
		configuredPreparer bool
	}{{"agent", true}, {"chat", true}, {"agent", false}, {"chat", false}} {
		t.Run(fmt.Sprintf("%s/preparer=%t", test.mode, test.configuredPreparer), func(t *testing.T) {
			mode := test.mode
			var mu sync.Mutex
			var calls []ollama.ChatRequest
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/tags" {
					io.WriteString(w, `{"models":[{"name":"model","capabilities":["completion","tools","vision"]}]}`)
					return
				}
				var request ollama.ChatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				mu.Lock()
				calls = append(calls, request)
				call := len(calls)
				mu.Unlock()
				if call == 1 {
					io.WriteString(w, "{\"message\":{\"content\":\"Preserved decisions and essential image facts.\"},\"done\":true}\n")
				} else {
					io.WriteString(w, "{\"message\":{\"content\":\"continued\"},\"done\":true,\"prompt_eval_count\":150,\"eval_count\":10}\n")
				}
			}))
			defer upstream.Close()
			database, conversation := compactionDatabase(t, mode, "model")
			client := ollama.NewClient(upstream.URL)
			runner := agent.Runner{Chat: client, Steps: database, MaxTurns: 2}
			if test.configuredPreparer {
				runner.Context = &agent.ContextPreparer{Chat: client, Store: database}
			}
			api := New(database, client, runner, RuntimeSettings{ContextTokens: 32768}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			url := fmt.Sprintf("/api/conversations/%d", conversation.ID)
			response := httptest.NewRecorder()
			api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, url+"/compact", nil))
			var compacted compactReply
			if err := json.Unmarshal(response.Body.Bytes(), &compacted); err != nil {
				t.Fatal(err)
			}
			if response.Code != 200 || !compacted.Compacted || compacted.Estimated >= compacted.Before || compacted.Checkpoint == nil || compacted.Checkpoint.ThroughMessageID != 2 || compacted.Checkpoint.State["compacted_after_message_id"] != float64(6) {
				t.Fatalf("wrong compaction result: %d %s", response.Code, response.Body.String())
			}
			mu.Lock()
			summaryCall := calls[0]
			mu.Unlock()
			summaryJSON, _ := json.Marshal(summaryCall)
			if strings.Contains(string(summaryJSON), "OLD_FILE_CONTENT") != (mode == "chat") || len(summaryCall.Messages[2].Images) != 1 || len(summaryCall.Tools) != 0 {
				t.Fatalf("summary attachment policy changed for %s: %s", mode, summaryJSON)
			}
			history, err := database.MessagesWithAttachmentData(context.Background(), conversation.ID)
			if err != nil || len(history) != 6 || !strings.Contains(history[0].Content, "OLD_USER") || string(history[0].Attachments[0].Data) != "OLD_FILE_CONTENT" {
				t.Fatalf("original history changed: %#v, %v", history, err)
			}
			response = httptest.NewRecorder()
			api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, url, nil))
			var detail struct {
				Checkpoint *store.ContextCheckpoint `json:"context_checkpoint"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil || detail.Checkpoint == nil || detail.Checkpoint.EstimatedTokens != compacted.Estimated {
				t.Fatalf("reload lost checkpoint: %s %v", response.Body.String(), err)
			}
			response = httptest.NewRecorder()
			api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, url+"/compact", nil))
			var repeated compactReply
			json.Unmarshal(response.Body.Bytes(), &repeated)
			if response.Code != 200 || repeated.Compacted || repeated.Reason != "not_enough_history" || repeated.Checkpoint == nil || repeated.Checkpoint.Summary != compacted.Checkpoint.Summary {
				t.Fatalf("repeat did not preserve checkpoint: %s", response.Body.String())
			}
			response = httptest.NewRecorder()
			api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, url+"/messages", strings.NewReader(`{"content":"continue now"}`)))
			if response.Code != 200 || !strings.Contains(response.Body.String(), `"type":"done"`) {
				t.Fatalf("continued generation failed: %d %s", response.Code, response.Body.String())
			}
			mu.Lock()
			defer mu.Unlock()
			if len(calls) != 2 {
				t.Fatalf("unexpected summary/generation calls: %d", len(calls))
			}
			continuedJSON, _ := json.Marshal(calls[1])
			if !strings.Contains(string(continuedJSON), "Preserved decisions") || !strings.Contains(string(continuedJSON), "CUSTOM_SYSTEM") || strings.Contains(string(continuedJSON), "OLD_USER") || !strings.Contains(string(continuedJSON), "continue now") || strings.Contains(string(continuedJSON), "RECENT_FILE_CONTENT") != (mode == "chat") {
				t.Fatalf("continued model got wrong context: %s", continuedJSON)
			}
			if len(calls[1].Messages[1].Images) != 1 {
				t.Fatal("recent image was dropped")
			}
		})
	}
}

type blockingCompactionChat struct {
	started chan struct{}
	release chan struct{}
}

func (chat blockingCompactionChat) Chat(ctx context.Context, request ollama.ChatRequest, emit func(ollama.ChatChunk) error) error {
	if request.Model == "blocked" {
		close(chat.started)
		select {
		case <-chat.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return emit(ollama.ChatChunk{Message: ollama.Message{Content: "Preserved memory."}, Done: true})
}

func TestManualCompactionReservesConversationAndCancellationReleasesIt(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelRun), func(t *testing.T) {
			database, conversation := compactionDatabase(t, "agent", "blocked")
			previous, err := database.UpsertCheckpoint(context.Background(), store.UpsertCheckpointParams{ConversationID: conversation.ID, ThroughMessageID: 2, Summary: "Previous stable summary"})
			if err != nil {
				t.Fatal(err)
			}
			// Add another completed turn so advancing this checkpoint remains possible.
			database.AddMessage(context.Background(), conversation.ID, "user", strings.Repeat("next", 1000), "complete")
			database.AddMessage(context.Background(), conversation.ID, "assistant", "next answer", "complete")
			// The prefix after the old checkpoint now contains messages 3 and 4.
			// Enlarge it enough that a faithful short summary reduces its estimate.
			if err := database.UpdateMessage(context.Background(), 3, strings.Repeat("relevant", 1000), "", "complete", nil); err != nil {
				t.Fatal(err)
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"models":[{"name":"blocked","capabilities":["completion","tools","vision"]}]}`)
			}))
			defer upstream.Close()
			client := ollama.NewClient(upstream.URL)
			chat := blockingCompactionChat{started: make(chan struct{}), release: make(chan struct{})}
			api := New(database, client, agent.Runner{Context: &agent.ContextPreparer{Chat: chat, Store: database}}, RuntimeSettings{ContextTokens: 32768}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			url := fmt.Sprintf("/api/conversations/%d", conversation.ID)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, url+"/compact", nil).WithContext(ctx))
			}()
			select {
			case <-chat.started:
			case <-time.After(5 * time.Second):
				t.Fatal("summarizer did not start")
			}
			for _, operation := range []struct{ method, path, body string }{
				{http.MethodPost, url + "/compact", ""},
				{http.MethodPost, url + "/messages", `{"content":"overlap"}`},
				{http.MethodDelete, url, ""},
				{http.MethodDelete, url + "/messages/3", ""},
				{http.MethodDelete, "/api/conversations", `{"confirmation":"DELETE"}`},
				{http.MethodPut, url + "/model", `{"model":"blocked"}`},
			} {
				conflict := httptest.NewRecorder()
				api.ServeHTTP(conflict, httptest.NewRequest(operation.method, operation.path, strings.NewReader(operation.body)))
				if conflict.Code != http.StatusConflict {
					t.Fatalf("overlapping %s %s allowed: %d %s", operation.method, operation.path, conflict.Code, conflict.Body.String())
				}
			}
			// A separate conversation can still compact while this model call blocks.
			other, err := database.CreateConversation(context.Background(), store.CreateConversationParams{Model: "other"})
			if err != nil {
				t.Fatal(err)
			}
			for index := 0; index < 6; index++ {
				role := "user"
				if index%2 == 1 {
					role = "assistant"
				}
				if _, err := database.AddMessage(context.Background(), other.ID, role, strings.Repeat("independent", 200), "complete"); err != nil {
					t.Fatal(err)
				}
			}
			independent := httptest.NewRecorder()
			api.ServeHTTP(independent, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/conversations/%d/compact", other.ID), nil))
			if independent.Code != http.StatusOK || !strings.Contains(independent.Body.String(), `"compacted":true`) {
				t.Fatalf("other conversation blocked: %d %s", independent.Code, independent.Body.String())
			}
			if cancelRun {
				cancel()
			} else {
				close(chat.release)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("compaction did not stop")
			}
			stored, err := database.GetCheckpoint(context.Background(), conversation.ID)
			if err != nil {
				t.Fatal(err)
			}
			if cancelRun && (response.Code != http.StatusBadGateway || stored.Summary != previous.Summary || stored.ThroughMessageID != previous.ThroughMessageID) {
				t.Fatalf("cancellation overwrote checkpoint: %d %s %#v", response.Code, response.Body.String(), stored)
			}
			if !cancelRun && (response.Code != http.StatusOK || stored.ThroughMessageID != 4) {
				t.Fatalf("checkpoint did not advance: %d %s %#v", response.Code, response.Body.String(), stored)
			}
			api.activeMu.Lock()
			_, active := api.active[conversation.ID]
			api.activeMu.Unlock()
			if active {
				t.Fatal("compaction leaked active reservation")
			}
		})
	}
}

type failedCompactionChat struct{}

func (failedCompactionChat) Chat(context.Context, ollama.ChatRequest, func(ollama.ChatChunk) error) error {
	return fmt.Errorf("summary service offline")
}

func TestManualCompactionFailureAndShortHistory(t *testing.T) {
	database, conversation := compactionDatabase(t, "chat", "model")
	client := ollama.NewClient("http://unused.invalid")
	api := New(database, client, agent.Runner{Context: &agent.ContextPreparer{Chat: failedCompactionChat{}, Store: database}}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	url := fmt.Sprintf("/api/conversations/%d/compact", conversation.ID)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, url, nil))
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "summary service offline") {
		t.Fatalf("summary failure was hidden: %d %s", response.Code, response.Body.String())
	}
	if _, err := database.GetCheckpoint(context.Background(), conversation.ID); err != store.ErrNotFound {
		t.Fatalf("failed summary was persisted: %v", err)
	}
	if _, err := database.DeleteMessagesFrom(context.Background(), conversation.ID, 3); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, url, nil))
	var short compactReply
	if err := json.Unmarshal(response.Body.Bytes(), &short); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || short.Compacted || short.Reason != "not_enough_history" || short.Checkpoint != nil || short.Before != short.Estimated {
		t.Fatalf("short history did not return clean no-op: %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/conversations/999/compact", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing conversation: %d %s", response.Code, response.Body.String())
	}
}
