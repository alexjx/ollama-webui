package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
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
		Title: "SQLite persistence", Model: "qwen3:8b", Mode: "chat", ContextWindow: &contextWindow, ThinkingEnabled: &thinkingEnabled,
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
	if conversation.ThinkingMode == nil || *conversation.ThinkingMode != "off" {
		t.Fatalf("legacy thinking off was not normalized: %#v", conversation.ThinkingMode)
	}
	if conversation.Mode != "chat" {
		t.Fatalf("conversation mode was not persisted: %q", conversation.Mode)
	}
	inherited, err := database.CreateConversation(ctx, CreateConversationParams{Title: "Inherited thinking", Model: "qwen3:8b"})
	if err != nil {
		t.Fatal(err)
	}
	if inherited.ThinkingEnabled != nil {
		t.Fatalf("inherited thinking should remain null: %#v", inherited.ThinkingEnabled)
	}
	if inherited.ThinkingMode != nil {
		t.Fatalf("inherited thinking mode should remain null: %#v", inherited.ThinkingMode)
	}
	high := "high"
	leveled, err := database.CreateConversation(ctx, CreateConversationParams{Title: "High effort", Model: "qwen3:8b", ThinkingMode: &high})
	if err != nil || leveled.ThinkingMode == nil || *leveled.ThinkingMode != "high" || leveled.ThinkingEnabled != nil {
		t.Fatalf("reasoning level did not persist independently: %#v, %v", leveled, err)
	}
	if inherited.Mode != "agent" {
		t.Fatalf("the backward-compatible mode default changed: %q", inherited.Mode)
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

func TestMigrationDefaultsExistingConversationsToAgentMode(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.ExecContext(ctx, `CREATE TABLE conversations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL DEFAULT 'New chat', model TEXT NOT NULL,
  system_prompt TEXT NOT NULL DEFAULT '', context_window INTEGER,
  thinking_enabled INTEGER, temperature REAL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO conversations (title, model, thinking_enabled, created_at, updated_at)
VALUES ('Existing agent', 'tools', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.GetConversation(ctx, 1)
	if err != nil || conversation.Mode != "agent" || conversation.ThinkingMode == nil || *conversation.ThinkingMode != "on" {
		t.Fatalf("legacy conversation did not migrate to agent mode: %#v, %v", conversation, err)
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

func TestStatsAndClearConversations(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(ctx, CreateConversationParams{Title: "Clear me", Model: "vision"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.AddMessageWithAttachments(ctx, conversation.ID, "user", "image", "complete", []NewAttachment{{
		FileName: "pixel.png", MediaType: "image/png", Data: []byte{1, 2, 3},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.BeginAgentStep(ctx, message.ID, 1, "shell", `{"command":"pwd"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpsertCheckpoint(ctx, UpsertCheckpointParams{
		ConversationID: conversation.ID, ThroughMessageID: message.ID, Summary: "before clear",
	}); err != nil {
		t.Fatal(err)
	}
	contextArtifact, err := database.CreateContextArtifact(ctx, CreateContextArtifactParams{
		ConversationID: conversation.ID, SourceMessageID: &message.ID, Kind: "tool_output", SizeBytes: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := database.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Conversations != 1 || stats.Messages != 1 || stats.Attachments != 1 || stats.AgentSteps != 1 || stats.AttachmentBytes != 3 || stats.DatabaseBytes <= 0 {
		t.Fatalf("unexpected populated stats: %#v", stats)
	}
	deleted, err := database.ClearConversations(ctx)
	if err != nil || deleted != 1 {
		t.Fatalf("clear returned %d, %v", deleted, err)
	}
	stats, err = database.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Conversations != 0 || stats.Messages != 0 || stats.Attachments != 0 || stats.AgentSteps != 0 || stats.AttachmentBytes != 0 {
		t.Fatalf("clear did not cascade: %#v", stats)
	}
	if _, err := database.GetConversationContext(ctx, conversation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clear preserved conversation context: %v", err)
	}
	if _, err := database.GetContextArtifact(ctx, conversation.ID, contextArtifact.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clear preserved context artifact: %v", err)
	}
}

func TestContextSchemaMigratesAndCreatesOpaqueNamespace(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(ctx, `CREATE TABLE conversations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL DEFAULT 'New chat', model TEXT NOT NULL,
  system_prompt TEXT NOT NULL DEFAULT '', context_window INTEGER,
  thinking_enabled INTEGER, temperature REAL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO conversations (title, model, created_at, updated_at)
VALUES ('Existing conversation', 'test', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	first, err := database.EnsureConversationContext(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.EnsureConversationContext(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.StorageKey != second.StorageKey {
		t.Fatalf("context key changed across ensure calls: %q, %q", first.StorageKey, second.StorageKey)
	}
	decoded, err := hex.DecodeString(first.StorageKey)
	if err != nil || len(decoded) != 16 || len(first.StorageKey) != 32 {
		t.Fatalf("storage key is not opaque 128-bit hex: %q, %v", first.StorageKey, err)
	}
	if _, err := database.EnsureConversationContext(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing conversation created a context: %v", err)
	}
}

func TestCheckpointRoundTripAndConversationBoundary(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	first, err := database.CreateConversation(ctx, CreateConversationParams{Title: "First", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateConversation(ctx, CreateConversationParams{Title: "Second", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	firstMessage, err := database.AddMessage(ctx, first.ID, "user", "one", "complete")
	if err != nil {
		t.Fatal(err)
	}
	secondMessage, err := database.AddMessage(ctx, second.ID, "user", "two", "complete")
	if err != nil {
		t.Fatal(err)
	}

	checkpoint, err := database.UpsertCheckpoint(ctx, UpsertCheckpointParams{
		ConversationID: first.ID, ThroughMessageID: firstMessage.ID, Summary: "initial",
		State: map[string]any{"goal": "ship"}, EstimatedTokens: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Summary != "initial" || checkpoint.State["goal"] != "ship" || checkpoint.EstimatedTokens != 42 {
		t.Fatalf("checkpoint did not round trip: %#v", checkpoint)
	}
	if _, err := database.UpsertCheckpoint(ctx, UpsertCheckpointParams{
		ConversationID: first.ID, ThroughMessageID: secondMessage.ID, Summary: "crossed boundary",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-conversation checkpoint was accepted: %v", err)
	}
	unchanged, err := database.GetCheckpoint(ctx, first.ID)
	if err != nil || unchanged.Summary != "initial" {
		t.Fatalf("rejected checkpoint changed stored state: %#v, %v", unchanged, err)
	}

	if _, err := database.db.ExecContext(ctx, `
UPDATE context_checkpoints SET through_message_id = ? WHERE conversation_id = ?`, secondMessage.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetCheckpoint(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get checkpoint exposed a cross-conversation boundary: %v", err)
	}
}

func TestContextArtifactsAreConversationScopedAndCascade(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	first, err := database.CreateConversation(ctx, CreateConversationParams{Title: "First", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreateConversation(ctx, CreateConversationParams{Title: "Second", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	firstMessage, err := database.AddMessage(ctx, first.ID, "assistant", "result", "complete")
	if err != nil {
		t.Fatal(err)
	}
	secondMessage, err := database.AddMessage(ctx, second.ID, "assistant", "other", "complete")
	if err != nil {
		t.Fatal(err)
	}
	step, err := database.BeginAgentStep(ctx, firstMessage.ID, 1, "shell", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpsertCheckpoint(ctx, UpsertCheckpointParams{
		ConversationID: first.ID, ThroughMessageID: firstMessage.ID, Summary: "saved state",
	}); err != nil {
		t.Fatal(err)
	}
	artifact, err := database.CreateContextArtifact(ctx, CreateContextArtifactParams{
		ConversationID: first.ID, SourceMessageID: &firstMessage.ID, SourceStepID: &step.ID,
		Kind: "tool_output", DisplayName: "result.txt", MediaType: "text/plain", SizeBytes: 7,
		SHA256: "239f59ed55e737c77147cf55ad0c1b030b6d7ee748a7426952f9b852d5a935e5", Summary: "command result",
	})
	if err != nil {
		t.Fatal(err)
	}
	if decoded, err := hex.DecodeString(artifact.StorageKey); err != nil || len(decoded) != 16 {
		t.Fatalf("artifact storage key is not opaque 128-bit hex: %q, %v", artifact.StorageKey, err)
	}
	if artifact.SourceMessageID == nil || *artifact.SourceMessageID != firstMessage.ID || artifact.SourceStepID == nil || *artifact.SourceStepID != step.ID {
		t.Fatalf("artifact sources did not round trip: %#v", artifact)
	}
	if _, err := database.GetContextArtifact(ctx, second.ID, artifact.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("artifact was readable by id across conversations: %v", err)
	}
	if _, err := database.GetContextArtifactByStorageKey(ctx, second.ID, artifact.StorageKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("artifact was readable by key across conversations: %v", err)
	}
	if err := database.DeleteContextArtifact(ctx, second.ID, artifact.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("artifact was deletable across conversations: %v", err)
	}
	if _, err := database.CreateContextArtifact(ctx, CreateContextArtifactParams{
		ConversationID: first.ID, SourceMessageID: &secondMessage.ID, Kind: "input", SizeBytes: 0,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-conversation artifact source was accepted: %v", err)
	}
	explicitKey := "0123456789abcdef0123456789abcdef"
	explicit, err := database.CreateContextArtifact(ctx, CreateContextArtifactParams{
		ConversationID: second.ID, StorageKey: explicitKey, Kind: "tool_output", MediaType: "text/plain",
		SizeBytes: 0, SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	})
	if err != nil || explicit.StorageKey != explicitKey {
		t.Fatalf("explicit artifact storage key was not preserved: %#v, %v", explicit, err)
	}
	for _, invalidKey := range []string{"short", "0123456789ABCDEF0123456789ABCDEF", "../../../../outside0000000000000000"} {
		if _, err := database.CreateContextArtifact(ctx, CreateContextArtifactParams{
			ConversationID: second.ID, StorageKey: invalidKey, Kind: "tool_output", SizeBytes: 0,
		}); !errors.Is(err, ErrInvalidStorageKey) {
			t.Fatalf("invalid artifact storage key %q was accepted: %v", invalidKey, err)
		}
	}
	items, err := database.ListContextArtifacts(ctx, first.ID)
	if err != nil || len(items) != 1 || items[0].ID != artifact.ID {
		t.Fatalf("unexpected artifact list: %#v, %v", items, err)
	}
	if err := database.DeleteConversation(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetConversationContext(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("conversation context did not cascade: %v", err)
	}
	if _, err := database.GetContextArtifact(ctx, first.ID, artifact.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("context artifact did not cascade: %v", err)
	}
	if _, err := database.GetCheckpoint(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("context checkpoint did not cascade: %v", err)
	}
	if _, err := database.GetConversation(ctx, second.ID); err != nil {
		t.Fatalf("unrelated conversation was changed: %v", err)
	}
}
