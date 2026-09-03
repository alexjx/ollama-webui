package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestConversationPersistenceAndSearch(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	contextWindow := 16384
	thinkingEnabled := false
	conversation, err := database.CreateConversation(ctx, CreateConversationParams{
		Title: "SQLite persistence", Model: "qwen3:8b", ContextWindow: &contextWindow, ThinkingEnabled: &thinkingEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if conversation.ContextWindow == nil || *conversation.ContextWindow != contextWindow {
		t.Fatalf("context window was not persisted: %#v", conversation.ContextWindow)
	}
	if conversation.ThinkingEnabled == nil || *conversation.ThinkingEnabled {
		t.Fatalf("explicit thinking off was not persisted: %#v", conversation.ThinkingEnabled)
	}
	inherited, err := database.CreateConversation(ctx, CreateConversationParams{Title: "Inherited thinking", Model: "qwen3:8b"})
	if err != nil {
		t.Fatal(err)
	}
	if inherited.ThinkingEnabled != nil {
		t.Fatalf("inherited thinking should remain null: %#v", inherited.ThinkingEnabled)
	}
	if _, err := database.AddMessage(ctx, conversation.ID, "user", "searchable needle", "complete"); err != nil {
		t.Fatal(err)
	}
	results, err := database.ListConversations(ctx, "needle")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != conversation.ID {
		t.Fatalf("message search returned %#v", results)
	}
	messages, err := database.MessagesWithAttachmentData(ctx, conversation.ID)
	if err != nil || len(messages) != 1 || messages[0].Content != "searchable needle" {
		t.Fatalf("messages were not persisted: %#v, %v", messages, err)
	}
}

func TestSearchTreatsWildcardsLiterally(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.CreateConversation(ctx, CreateConversationParams{Title: "100% local", Model: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateConversation(ctx, CreateConversationParams{Title: "ordinary", Model: "test"}); err != nil {
		t.Fatal(err)
	}
	results, err := database.ListConversations(ctx, "%")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "100% local" {
		t.Fatalf("wildcard search was not escaped: %#v", results)
	}
}

func TestAttachmentRoundTripAndCascade(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(ctx, CreateConversationParams{Title: "Vision", Model: "vision"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.AddMessageWithAttachments(ctx, conversation.ID, "user", "describe", "complete", []NewAttachment{{
		FileName: "pixel.png", MediaType: "image/png", Data: []byte{1, 2, 3},
	}})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := database.MessagesWithAttachmentData(ctx, conversation.ID)
	if err != nil || len(messages) != 1 || len(messages[0].Attachments) != 1 {
		t.Fatalf("attachment was not loaded: %#v, %v", messages, err)
	}
	attachment := messages[0].Attachments[0]
	if string(attachment.Data) != string([]byte{1, 2, 3}) || attachment.URL == "" {
		t.Fatalf("attachment data did not round trip: %#v", attachment)
	}
	metadataOnly, err := database.Messages(ctx, conversation.ID)
	if err != nil || len(metadataOnly[0].Attachments[0].Data) != 0 || metadataOnly[0].Attachments[0].Size != 3 {
		t.Fatalf("metadata query exposed blob data or lost size: %#v, %v", metadataOnly, err)
	}
	if err := database.DeleteConversation(ctx, conversation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetAttachment(ctx, message.Attachments[0].ID); err != ErrNotFound {
		t.Fatalf("attachment was not cascade-deleted: %v", err)
	}
}

func TestAgentStepsPersistWithAssistantMessage(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(ctx, CreateConversationParams{Title: "Agent", Model: "tools"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.AddMessage(ctx, conversation.ID, "assistant", "", "streaming")
	if err != nil {
		t.Fatal(err)
	}
	step, err := database.BeginAgentStep(ctx, message.ID, 2, "shell", `{"command":"pwd"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CompleteAgentStep(ctx, step.ID, "/workspace\n", 0, "complete"); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateMessage(ctx, message.ID, "Finished", "I should inspect the workspace.", "complete", map[string]any{"agent_turns": 2}); err != nil {
		t.Fatal(err)
	}
	messages, err := database.Messages(ctx, conversation.ID)
	if err != nil || len(messages) != 1 || len(messages[0].AgentSteps) != 1 {
		t.Fatalf("agent step did not load: %#v, %v", messages, err)
	}
	stored := messages[0].AgentSteps[0]
	if stored.Turn != 2 || stored.ToolName != "shell" || stored.ExitCode == nil || *stored.ExitCode != 0 || stored.Output != "/workspace\n" || stored.Status != "complete" {
		t.Fatalf("agent step did not round trip: %#v", stored)
	}
	if messages[0].Thinking != "I should inspect the workspace." || messages[0].Content != "Finished" {
		t.Fatalf("thinking did not round trip: %#v", messages[0])
	}
}
