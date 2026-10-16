package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTemplatesPersistenceAndConversationIndependence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "templates.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	input := TemplateInput{Name: " Review ", Description: "Code review", Message: "  Keep indentation\n\n", SystemPrompt: " Review carefully \n", Settings: TemplateSettings{"temperature": json.RawMessage("null"), "mode": json.RawMessage(`"chat"`)}}
	saved, err := db.CreateTemplate(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Name != "Review" || saved.Message != input.Message || saved.SystemPrompt != input.SystemPrompt {
		t.Fatalf("unexpected normalization: %#v", saved)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.GetTemplate(ctx, saved.ID)
	if err != nil || !reflect.DeepEqual(got, saved) {
		t.Fatalf("reopen: %#v %v", got, err)
	}
	conversation, err := db.CreateConversation(ctx, CreateConversationParams{Model: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteConversation(ctx, conversation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClearConversations(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListTemplates(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("clear history affected templates: %#v %v", items, err)
	}
	input.Name = "Updated"
	input.Settings = nil
	updated, err := db.UpdateTemplate(ctx, saved.ID, input)
	if err != nil || updated.Name != "Updated" || updated.Settings != nil || !updated.CreatedAt.Equal(saved.CreatedAt) {
		t.Fatalf("update: %#v %v", updated, err)
	}
	if err := db.DeleteTemplate(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetTemplate(ctx, saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}
	if _, err := db.UpdateTemplate(ctx, saved.ID, input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
	if err := db.DeleteTemplate(ctx, saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	items, err = db.ListTemplates(ctx)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("empty list: %#v %v", items, err)
	}
}
