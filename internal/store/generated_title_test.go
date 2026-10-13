package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestExplicitGeneratedTitlePreservesConcurrentManualEdits(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "titles.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, manual := range []bool{false, true} {
		conversation, err := database.CreateConversation(ctx, CreateConversationParams{Title: "Original", Model: "test", AutoTitle: true})
		if err != nil {
			t.Fatal(err)
		}
		if manual {
			if err := database.UpdateTitle(ctx, conversation.ID, "Original"); err != nil {
				t.Fatal(err)
			}
		}
		changed, err := database.UpdateTitleIfUnchanged(ctx, conversation.ID, conversation.Title, conversation.UpdatedAt, "Latest topic")
		if err != nil || changed == manual {
			t.Fatalf("conditional naming: changed=%v manual=%v error=%v", changed, manual, err)
		}
		got, err := database.GetConversation(ctx, conversation.ID)
		if err != nil || got.AutoTitlePending {
			t.Fatalf("invalid title state: %#v %v", got, err)
		}
		if manual && got.Title != "Original" || !manual && got.Title != "Latest topic" {
			t.Fatalf("unexpected saved title: %q", got.Title)
		}
	}
}
