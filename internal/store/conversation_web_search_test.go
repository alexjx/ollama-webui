package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestConversationWebSearchMigrationAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := db.CreateConversation(ctx, CreateConversationParams{Title: "Legacy", Model: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddMessage(ctx, legacy.ID, "user", "Keep history", "complete"); err != nil {
		t.Fatal(err)
	}
	// Simulate the schema deployed before per-session search permissions.
	if _, err = db.db.ExecContext(ctx, "ALTER TABLE conversations DROP COLUMN web_search_enabled"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetConversation(ctx, legacy.ID)
	if err != nil || !got.WebSearchEnabled || got.Title != "Legacy" || got.MessageCount != 1 {
		t.Fatalf("migration changed session: %#v %v", got, err)
	}
	off := false
	other, err := db.CreateConversation(ctx, CreateConversationParams{Title: "Off", Model: "fixture", WebSearchEnabled: &off})
	if err != nil || other.WebSearchEnabled {
		t.Fatalf("explicit off lost: %#v %v", other, err)
	}
	if err = db.UpdateConversationWebSearch(ctx, legacy.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = db.UpdateConversationWebSearch(ctx, other.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = db.UpdateConversationWebSearch(ctx, 9999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
	db.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	items, err := db.ListConversations(ctx, "")
	if err != nil || len(items) != 2 {
		t.Fatalf("list: %#v %v", items, err)
	}
	for _, item := range items {
		if item.WebSearchEnabled != (item.ID == other.ID) {
			t.Fatalf("independent permission lost: %#v", item)
		}
	}
	got, err = db.GetConversation(ctx, legacy.ID)
	if err != nil || got.MessageCount != 1 || got.Title != "Legacy" {
		t.Fatalf("history lost: %#v %v", got, err)
	}
}
