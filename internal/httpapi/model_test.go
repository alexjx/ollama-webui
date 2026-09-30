package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func TestChangeModelRecoversRemovedModelAndPreservesHistory(t *testing.T) {
	for _, mode := range []string{"chat", "agent"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			var captured struct {
				Model    string           `json:"model"`
				Messages []ollama.Message `json:"messages"`
				Think    any              `json:"think"`
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/tags" {
					io.WriteString(w, `{"models":[{"name":"replacement","capabilities":["completion","tools"]}]}`)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
					t.Error(err)
				}
				io.WriteString(w, "{\"message\":{\"role\":\"assistant\",\"content\":\"continued\"},\"done\":true}\n")
			}))
			defer upstream.Close()
			path := filepath.Join(t.TempDir(), "test.db")
			db, err := store.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { db.Close() }()
			thinking := "high"
			temperature := 0.4
			window := 8192
			before, err := db.CreateConversation(ctx, store.CreateConversationParams{Title: "Keep title", Model: "removed", Mode: mode, SystemPrompt: "Keep instructions", ThinkingMode: &thinking, Temperature: &temperature, ContextWindow: &window})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.AddMessage(ctx, before.ID, "user", "old question", "complete"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.AddMessage(ctx, before.ID, "assistant", "old answer", "complete"); err != nil {
				t.Fatal(err)
			}
			history, _ := db.Messages(ctx, before.ID)
			client := ollama.NewClient(upstream.URL)
			api := New(db, client, agent.Runner{Chat: client, MaxTurns: 3}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			result := httptest.NewRecorder()
			api.ServeHTTP(result, httptest.NewRequest(http.MethodPut, "/api/conversations/1/model", strings.NewReader(`{"model":"replacement"}`)))
			if result.Code != 200 {
				t.Fatalf("switch: %d %s", result.Code, result.Body.String())
			}
			after, _ := db.GetConversation(ctx, before.ID)
			if after.Model != "replacement" || after.Mode != mode || after.Title != before.Title || after.SystemPrompt != before.SystemPrompt || after.ThinkingMode != nil || after.ThinkingEnabled != nil || *after.ContextWindow != window || *after.Temperature != temperature {
				t.Fatalf("settings changed: %#v", after)
			}
			saved, _ := db.Messages(ctx, before.ID)
			if !reflect.DeepEqual(history, saved) {
				t.Fatal("switch modified history")
			}
			result = httptest.NewRecorder()
			api.ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/api/conversations/1/messages", strings.NewReader(`{"content":"continue"}`)))
			if result.Code != 200 || strings.Contains(result.Body.String(), `"type":"error"`) {
				t.Fatalf("generation: %d %s", result.Code, result.Body.String())
			}
			if captured.Model != "replacement" || captured.Think != nil {
				t.Fatalf("wrong request: %#v", captured)
			}
			joined := ""
			for _, msg := range captured.Messages {
				joined += msg.Content + "\n"
			}
			if !strings.Contains(joined, "old question") || !strings.Contains(joined, "old answer") {
				t.Fatalf("lost history: %s", joined)
			}
			db.Close()
			db, err = store.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			after, _ = db.GetConversation(ctx, before.ID)
			if after.ThinkingMode != nil || after.ThinkingEnabled != nil {
				t.Fatal("reopening restored thinking")
			}
		})
	}
}

func TestChangeModelValidation(t *testing.T) {
	for _, test := range []struct {
		name, model, mode   string
		images              int
		active, unavailable bool
		status              int
	}{
		{name: "blank", model: " ", mode: "chat", status: 422},
		{name: "missing model", model: "missing", mode: "chat", status: 422},
		{name: "embedding", model: "embed", mode: "chat", status: 422},
		{name: "agent needs tools", model: "plain", mode: "agent", status: 422},
		{name: "history needs vision", model: "plain", mode: "chat", images: 1, status: 422},
		{name: "image count", model: "single", mode: "chat", images: 2, status: 422},
		{name: "active", model: "replacement", mode: "agent", active: true, status: 409},
		{name: "offline", model: "replacement", mode: "chat", unavailable: true, status: 502},
		{name: "same model", model: "original", mode: "chat", status: 200},
		{name: "unknown capabilities chat", model: "unknown", mode: "chat", status: 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.unavailable {
					w.WriteHeader(503)
					return
				}
				io.WriteString(w, `{"models":[{"name":"replacement","capabilities":["completion","tools"]},{"name":"plain","capabilities":["completion"]},{"name":"original","capabilities":["completion","thinking"]},{"name":"unknown"},{"name":"embed","capabilities":["embedding"]},{"name":"single","capabilities":["completion","vision"],"details":{"family":"mllama"}}]}`)
			}))
			defer upstream.Close()
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			thinking := "on"
			before, err := db.CreateConversation(ctx, store.CreateConversationParams{Model: "original", Mode: test.mode, ThinkingMode: &thinking})
			if err != nil {
				t.Fatal(err)
			}
			attachments := make([]store.NewAttachment, test.images)
			for i := range attachments {
				attachments[i] = store.NewAttachment{FileName: "a.png", MediaType: "image/png", Data: []byte{1}}
			}
			if _, err := db.AddMessageWithAttachments(ctx, before.ID, "user", "history", "complete", attachments); err != nil {
				t.Fatal(err)
			}
			before, _ = db.GetConversation(ctx, before.ID)
			api := New(db, ollama.NewClient(upstream.URL), agent.Runner{}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			if test.active {
				api.active[before.ID] = struct{}{}
			}
			body, _ := json.Marshal(map[string]string{"model": test.model})
			result := httptest.NewRecorder()
			api.ServeHTTP(result, httptest.NewRequest(http.MethodPut, "/api/conversations/1/model", strings.NewReader(string(body))))
			if result.Code != test.status {
				t.Fatalf("got %d want %d: %s", result.Code, test.status, result.Body.String())
			}
			after, _ := db.GetConversation(ctx, before.ID)
			if test.status != 200 || test.model == "original" {
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("unexpected mutation before=%#v after=%#v", before, after)
				}
			}
			result = httptest.NewRecorder()
			api.ServeHTTP(result, httptest.NewRequest(http.MethodPut, "/api/conversations/999/model", strings.NewReader(`{"model":"replacement"}`)))
			if result.Code != 404 {
				t.Fatalf("missing conversation: %d", result.Code)
			}
		})
	}
}
