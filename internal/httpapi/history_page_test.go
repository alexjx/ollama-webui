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
	"testing"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/store"
)

func TestConversationHistoryPagination(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := db.CreateConversation(ctx, store.CreateConversationParams{Title: "History", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := 0; i < 5; i++ {
		m, err := db.AddMessage(ctx, c.ID, "user", "hello", "complete")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	api := New(db, nil, agent.Runner{}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tc := range []struct {
		query         string
		status, count int
		more          bool
		before        int64
	}{
		{"", 200, 5, false, 0},
		{"?limit=2", 200, 2, true, ids[3]},
		{fmt.Sprintf("?limit=2&before=%d", ids[3]), 200, 2, true, ids[1]},
		{fmt.Sprintf("?limit=2&before=%d", ids[1]), 200, 1, false, 0},
		{fmt.Sprintf("?limit=2&before=%d", ids[0]), 200, 0, false, 0},
		{"?limit=0", 400, 0, false, 0}, {"?limit=101", 400, 0, false, 0}, {"?limit=x", 400, 0, false, 0},
		{"?limit=2&before=-1", 400, 0, false, 0}, {"?limit=2&before=x", 400, 0, false, 0}, {"?before=1", 400, 0, false, 0},
		{"?limit=2&before=99999", 404, 0, false, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			response := httptest.NewRecorder()
			api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/conversations/%d%s", c.ID, tc.query), nil))
			if response.Code != tc.status {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			if tc.status != 200 {
				return
			}
			var payload struct {
				Messages []store.Message `json:"messages"`
				Page     *struct {
					More   bool   `json:"has_more"`
					Before *int64 `json:"before"`
				} `json:"history_page"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Messages == nil || len(payload.Messages) != tc.count {
				t.Fatalf("messages: %#v", payload.Messages)
			}
			if tc.query == "" {
				if payload.Page != nil {
					t.Fatal("default response changed")
				}
				return
			}
			if payload.Page == nil || payload.Page.More != tc.more {
				t.Fatalf("page: %#v", payload.Page)
			}
			if tc.before == 0 {
				if payload.Page.Before != nil {
					t.Fatal("unexpected next cursor")
				}
			} else if payload.Page.Before == nil || *payload.Page.Before != tc.before {
				t.Fatalf("wrong cursor: %#v", payload.Page)
			}
		})
	}
}
