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
	"testing"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/store"
)

func TestPagedHistoryLoadsInspectionContextOnDemand(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "context.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := db.CreateConversation(ctx, store.CreateConversationParams{Title: "History", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateConversation(ctx, store.CreateConversationParams{Title: "Other", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := db.AddMessage(ctx, c.ID, "assistant", "Visible reply", "complete")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := db.AddMessage(ctx, c.ID, "assistant", "Older reply without capture", "complete")
	if err != nil {
		t.Fatal(err)
	}
	capture := map[string]any{"request": map[string]any{"model": "test", "messages": []any{map[string]any{"role": "user", "content": strings.Repeat("large model context ", 100000)}}}, "status": "complete"}
	if err := db.UpdateMessage(ctx, m.ID, m.Content, "", "complete", map[string]any{"model_exchange": capture, "eval_count": 42, "activity": map[string]any{"version": 1}}); err != nil {
		t.Fatal(err)
	}
	api := New(db, nil, agent.Runner{}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	get := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		return response
	}
	page := get(fmt.Sprintf("/api/conversations/%d?limit=5", c.ID))
	if page.Code != 200 || page.Body.Len() > 4096 {
		t.Fatalf("page status=%d bytes=%d", page.Code, page.Body.Len())
	}
	var payload struct {
		Messages []store.Message `json:"messages"`
	}
	if err := json.Unmarshal(page.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Messages) != 2 {
		t.Fatal("missing messages")
	}
	metadata := payload.Messages[0].Metadata
	if _, exists := metadata["model_exchange"]; exists {
		t.Fatal("inspection capture was included in page")
	}
	if metadata["eval_count"] != float64(42) || metadata["activity"].(map[string]any)["version"] != float64(1) {
		t.Fatalf("other metadata changed: %#v", metadata)
	}
	full := get(fmt.Sprintf("/api/conversations/%d", c.ID))
	if full.Code != 200 || full.Body.Len() < 1000000 {
		t.Fatalf("full history lost capture: status=%d bytes=%d", full.Code, full.Body.Len())
	}
	if err := json.Unmarshal(full.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Messages[0].Metadata["model_exchange"] == nil {
		t.Fatal("legacy API lost model exchange")
	}
	inspection := get(fmt.Sprintf("/api/conversations/%d/messages/%d/context", c.ID, m.ID))
	if inspection.Code != 200 || inspection.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("inspection status=%d", inspection.Code)
	}
	var actual struct {
		Exchange json.RawMessage `json:"model_exchange"`
	}
	if err := json.Unmarshal(inspection.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(capture)
	if string(actual.Exchange) != string(expected) {
		t.Fatal("inspection did not return exact saved capture")
	}
	for _, tc := range []struct {
		path   string
		status int
	}{
		{fmt.Sprintf("/api/conversations/%d/messages/%d/context", other.ID, m.ID), 404},
		{fmt.Sprintf("/api/conversations/%d/messages/999999/context", c.ID), 404},
		{fmt.Sprintf("/api/conversations/999999/messages/%d/context", m.ID), 404},
		{fmt.Sprintf("/api/conversations/%d/messages/0/context", c.ID), 400},
		{fmt.Sprintf("/api/conversations/%d/messages/nope/context", c.ID), 400},
		{fmt.Sprintf("/api/conversations/0/messages/%d/context", m.ID), 400},
	} {
		if response := get(tc.path); response.Code != tc.status {
			t.Errorf("%s status=%d want=%d", tc.path, response.Code, tc.status)
		}
	}
	missing := get(fmt.Sprintf("/api/conversations/%d/messages/%d/context", c.ID, empty.ID))
	if missing.Code != 200 || strings.TrimSpace(missing.Body.String()) != "{\"model_exchange\":null}" {
		t.Fatalf("missing capture: %d %s", missing.Code, missing.Body.String())
	}
}
