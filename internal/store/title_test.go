package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func TestAutomaticTitleEligibilityAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "titles.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := database.CreateConversation(ctx, CreateConversationParams{Title: "Initial question", Model: "test", AutoTitle: true})
	if err != nil || !conversation.AutoTitlePending {
		t.Fatalf("new opted-in conversation not eligible: %#v, %v", conversation, err)
	}
	defaultConversation, err := database.CreateConversation(ctx, CreateConversationParams{Title: "Explicit title", Model: "test"})
	if err != nil || defaultConversation.AutoTitlePending {
		t.Fatalf("automatic title should require opt in: %#v, %v", defaultConversation, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err = database.GetConversation(ctx, conversation.ID)
	if err != nil || !conversation.AutoTitlePending {
		t.Fatalf("eligibility lost after reopen: %#v, %v", conversation, err)
	}
	renamed, err := database.CompleteAutoTitle(ctx, conversation.ID, "  A concise generated title  ")
	if err != nil || !renamed {
		t.Fatalf("automatic rename failed: %v, %v", renamed, err)
	}
	if renamed, err := database.CompleteAutoTitle(ctx, conversation.ID, "Second title"); err != nil || renamed {
		t.Fatalf("automatic title was applied twice: %v, %v", renamed, err)
	}
	conversations, err := database.ListConversations(ctx, "generated title")
	if err != nil || len(conversations) != 1 || conversations[0].Title != "A concise generated title" || conversations[0].AutoTitlePending {
		t.Fatalf("generated title not visible in conversation list: %#v, %v", conversations, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err = database.GetConversation(ctx, conversation.ID)
	if err != nil || conversation.AutoTitlePending || conversation.Title != "A concise generated title" {
		t.Fatalf("generated title state lost after reopen: %#v, %v", conversation, err)
	}
}

func TestManualTitleAndFailedAttemptPreventAutomaticRename(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "titles.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, action := range []string{"same title", "different title", "failed attempt", "deleted"} {
		t.Run(action, func(t *testing.T) {
			conversation, err := database.CreateConversation(ctx, CreateConversationParams{Title: "Initial", Model: "test", AutoTitle: true})
			if err != nil {
				t.Fatal(err)
			}
			wantTitle := "Initial"
			switch action {
			case "same title":
				err = database.UpdateTitle(ctx, conversation.ID, wantTitle)
			case "different title":
				wantTitle = "My custom title"
				err = database.UpdateTitle(ctx, conversation.ID, wantTitle)
			case "failed attempt":
				err = database.DisableAutoTitle(ctx, conversation.ID)
			case "deleted":
				err = database.DeleteConversation(ctx, conversation.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if renamed, err := database.CompleteAutoTitle(ctx, conversation.ID, "Late model title"); err != nil || renamed {
				t.Fatalf("late automatic title applied: %v, %v", renamed, err)
			}
			if action != "deleted" {
				got, err := database.GetConversation(ctx, conversation.ID)
				if err != nil || got.Title != wantTitle || got.AutoTitlePending {
					t.Fatalf("title or eligibility changed incorrectly: %#v, %v", got, err)
				}
			}
		})
	}
}

func TestConcurrentAutomaticTitleCompletionAppliesOnce(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "titles.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(ctx, CreateConversationParams{Model: "test", AutoTitle: true})
	if err != nil {
		t.Fatal(err)
	}
	const count = 8
	results := make(chan bool, count)
	errors := make(chan error, count)
	var group sync.WaitGroup
	for range count {
		group.Add(1)
		go func() {
			defer group.Done()
			renamed, err := database.CompleteAutoTitle(ctx, conversation.ID, "Generated title")
			results <- renamed
			errors <- err
		}()
	}
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	applied := 0
	for renamed := range results {
		if renamed {
			applied++
		}
	}
	if applied != 1 {
		t.Fatalf("automatic title applied %d times, want 1", applied)
	}
}
