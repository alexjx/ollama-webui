package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebSearchSettingsSurviveRestartAndConversationClear(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := db.GetWebSearchSettings(ctx)
	if err != nil || initial.Enabled || initial.Token != "" {
		t.Fatalf("unexpected initial settings: %#v %v", initial, err)
	}
	wanted := WebSearchSettings{Enabled: true, Token: "tvly-test-token"}
	if err = db.SetWebSearchSettings(ctx, wanted); err != nil {
		t.Fatal(err)
	}
	if _, err = db.CreateConversation(ctx, CreateConversationParams{Title: "Clear me", Model: "model", Mode: "chat"}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClearConversations(ctx); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	saved, err := db.GetWebSearchSettings(ctx)
	if err != nil || saved != wanted {
		t.Fatalf("settings lost across clear/restart: %#v %v", saved, err)
	}
	public, err := json.Marshal(saved)
	if err != nil || strings.Contains(string(public), wanted.Token) {
		t.Fatalf("token serialized: %s %v", public, err)
	}
	if err = db.SetWebSearchSettings(ctx, WebSearchSettings{}); err != nil {
		t.Fatal(err)
	}
	saved, err = db.GetWebSearchSettings(ctx)
	if err != nil || saved.Enabled || saved.Token != "" {
		t.Fatalf("token removal failed: %#v %v", saved, err)
	}
}

func TestWebSearchSettingsRejectInvalidTokenAndEnabledWithoutToken(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, settings := range []WebSearchSettings{{Enabled: true}, {Token: "tvly-token\r\nheader"}, {Token: "two tokens"}, {Token: strings.Repeat("x", 513)}} {
		if err = db.SetWebSearchSettings(context.Background(), settings); err == nil {
			t.Fatal("invalid token settings accepted")
		}
	}
}
