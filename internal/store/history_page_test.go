package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestMessagesPageStableOrderingAndBoundedHydration(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := db.CreateConversation(ctx, CreateConversationParams{Title: "History", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := 0; i < 5; i++ {
		m, err := db.AddMessageWithAttachments(ctx, c.ID, "assistant", "reply", "complete", []NewAttachment{{FileName: "note.txt", MediaType: "text/plain", Data: []byte("contents")}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
		if _, err := db.BeginAgentStep(ctx, m.ID, 1, "shell", "{}"); err != nil {
			t.Fatal(err)
		}
	}
	// Equal timestamps must paginate by ID without losing messages.
	if _, err := db.db.ExecContext(ctx, "UPDATE messages SET created_at = ? WHERE conversation_id = ?", "2026-01-01T00:00:00Z", c.ID); err != nil {
		t.Fatal(err)
	}
	// An unread older message must not be decoded while opening the latest page.
	if _, err := db.db.ExecContext(ctx, "UPDATE messages SET metadata_json = 'invalid' WHERE id = ?", ids[0]); err != nil {
		t.Fatal(err)
	}
	page, more, err := db.MessagesPage(ctx, c.ID, 2, 0)
	if err != nil || !more || len(page) != 2 || page[0].ID != ids[3] || page[1].ID != ids[4] {
		t.Fatalf("latest page: %#v %v %v", page, more, err)
	}
	for _, m := range page {
		if len(m.Attachments) != 1 || len(m.AgentSteps) != 1 || len(m.Attachments[0].Data) != 0 {
			t.Fatalf("page hydration: %#v", m)
		}
	}
	if _, err := db.db.ExecContext(ctx, "UPDATE messages SET metadata_json = '{}' WHERE id = ?", ids[0]); err != nil {
		t.Fatal(err)
	}
	// New replies do not shift an existing backward cursor.
	if _, err := db.AddMessage(ctx, c.ID, "user", "new reply", "complete"); err != nil {
		t.Fatal(err)
	}
	page, more, err = db.MessagesPage(ctx, c.ID, 2, page[0].ID)
	if err != nil || !more || len(page) != 2 || page[0].ID != ids[1] || page[1].ID != ids[2] {
		t.Fatalf("middle page: %#v %v %v", page, more, err)
	}
	page, more, err = db.MessagesPage(ctx, c.ID, 2, page[0].ID)
	if err != nil || more || len(page) != 1 || page[0].ID != ids[0] {
		t.Fatalf("oldest page: %#v %v %v", page, more, err)
	}
	page, more, err = db.MessagesPage(ctx, c.ID, 2, page[0].ID)
	if err != nil || more || page == nil || len(page) != 0 {
		t.Fatalf("empty page: %#v %v %v", page, more, err)
	}
	other, err := db.CreateConversation(ctx, CreateConversationParams{Title: "Other", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.MessagesPage(ctx, other.ID, 2, ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign cursor: %v", err)
	}
	full, err := db.Messages(ctx, c.ID)
	if err != nil || len(full) != 6 {
		t.Fatalf("full history changed: %d %v", len(full), err)
	}
}
