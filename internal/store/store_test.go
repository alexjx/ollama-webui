package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
	"time"
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

func TestAgentJobsAndRunsHierarchyLifecycleReloadAndCascade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agent-work.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := database.CreateConversation(ctx, CreateConversationParams{Title: "Delegate", Model: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := database.AddMessage(ctx, conversation.ID, "user", "label images", "complete")
	if err != nil {
		t.Fatal(err)
	}
	job, err := database.CreateAgentJob(ctx, CreateAgentJobParams{
		ConversationID: conversation.ID, RequestingMessageID: request.ID, Task: "label a directory",
	})
	if err != nil || job.Status != AgentStatusQueued || job.OutputRefs == nil {
		t.Fatalf("agent job was not created in a usable queued state: %#v, %v", job, err)
	}
	root, err := database.CreateAgentRun(ctx, CreateAgentRunParams{
		JobID: job.ID, Task: "create manifest", Model: "qwen3-vl", ContextWindow: 8192, Depth: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := database.CreateAgentRun(ctx, CreateAgentRunParams{
		JobID: job.ID, ParentRunID: &root.ID, Task: "label shard 1", Model: "qwen3-vl", ContextWindow: 4096, Depth: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentRunID == nil || *child.ParentRunID != root.ID || child.ConversationID != conversation.ID || child.RequestingMessageID != request.ID {
		t.Fatalf("agent run hierarchy or ownership did not round trip: %#v", child)
	}
	if _, err := database.CreateAgentRun(ctx, CreateAgentRunParams{
		JobID: job.ID, ParentRunID: &root.ID, Task: "bad depth", Model: "test", ContextWindow: 1024, Depth: 3,
	}); !errors.Is(err, ErrInvalidAgentState) {
		t.Fatalf("invalid child depth was accepted: %v", err)
	}
	otherJob, err := database.CreateAgentJob(ctx, CreateAgentJobParams{
		ConversationID: conversation.ID, RequestingMessageID: request.ID, Task: "other",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAgentRun(ctx, CreateAgentRunParams{
		JobID: otherJob.ID, ParentRunID: &root.ID, Task: "cross-job child", Model: "test", ContextWindow: 1024, Depth: 1,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-job parent was accepted: %v", err)
	}
	if _, err := database.StartAgentJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.StartAgentRun(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.StartAgentRun(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	child, err = database.CompleteAgentRun(ctx, child.ID, "12 images labeled", []string{"labels/part-1.jsonl"})
	if err != nil || child.Status != AgentStatusComplete || child.CompletedAt == nil || child.OutputRefs[0] != "labels/part-1.jsonl" {
		t.Fatalf("agent run completion did not round trip: %#v, %v", child, err)
	}
	if _, err := database.FailAgentRun(ctx, root.ID, "manifest invalid"); err != nil {
		t.Fatal(err)
	}
	job, err = database.CompleteAgentJob(ctx, job.ID, "partial output retained", []string{"labels/part-1.jsonl"})
	if err != nil || job.StartedAt == nil || job.CompletedAt == nil || job.Status != AgentStatusComplete {
		t.Fatalf("agent job lifecycle did not round trip: %#v, %v", job, err)
	}
	if _, err := database.StartAgentJob(ctx, job.ID); !errors.Is(err, ErrInvalidAgentState) {
		t.Fatalf("terminal job was restarted: %v", err)
	}
	cancelledRun, err := database.CreateAgentRun(ctx, CreateAgentRunParams{
		JobID: otherJob.ID, Task: "cancel me", Model: "test", ContextWindow: 1024, Depth: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancelledRun, err = database.CancelAgentRun(ctx, cancelledRun.ID, "no longer needed"); err != nil || cancelledRun.Status != AgentStatusCancelled {
		t.Fatalf("queued run was not cancelled: %#v, %v", cancelledRun, err)
	}
	if _, err := database.CancelAgentJob(ctx, otherJob.ID, "no longer needed"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	jobs, err := database.ListAgentJobsByRequestingMessage(ctx, request.ID)
	if err != nil || len(jobs) != 2 || jobs[0].ID != job.ID {
		t.Fatalf("agent jobs did not survive reload: %#v, %v", jobs, err)
	}
	runs, err := database.ListAgentRunsByRequestingMessage(ctx, request.ID)
	if err != nil || len(runs) != 3 || runs[0].ID != root.ID || runs[1].ID != child.ID {
		t.Fatalf("agent runs did not survive reload in creation order: %#v, %v", runs, err)
	}
	if err := database.DeleteConversation(ctx, conversation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetAgentJob(ctx, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("agent job did not cascade with conversation: %v", err)
	}
	if _, err := database.GetAgentRun(ctx, child.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("agent run did not cascade with conversation: %v", err)
	}
}

func TestAgentWorkOwnershipAndStaleRecovery(t *testing.T) {
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
	request, err := database.AddMessage(ctx, first.ID, "user", "delegate", "complete")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAgentJob(ctx, CreateAgentJobParams{
		ConversationID: second.ID, RequestingMessageID: request.ID, Task: "cross boundary",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-conversation requesting message was accepted: %v", err)
	}

	staleJob, err := database.CreateAgentJob(ctx, CreateAgentJobParams{
		ConversationID: first.ID, RequestingMessageID: request.ID, Task: "stale job",
	})
	if err != nil {
		t.Fatal(err)
	}
	staleRun, err := database.CreateAgentRun(ctx, CreateAgentRunParams{
		JobID: staleJob.ID, Task: "stale run", Model: "test", ContextWindow: 2048, Depth: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.StartAgentJob(ctx, staleJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.StartAgentRun(ctx, staleRun.ID); err != nil {
		t.Fatal(err)
	}
	freshJob, err := database.CreateAgentJob(ctx, CreateAgentJobParams{
		ConversationID: first.ID, RequestingMessageID: request.ID, Task: "fresh job",
	})
	if err != nil {
		t.Fatal(err)
	}
	freshRun, err := database.CreateAgentRun(ctx, CreateAgentRunParams{
		JobID: freshJob.ID, Task: "fresh run", Model: "test", ContextWindow: 2048, Depth: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.StartAgentJob(ctx, freshJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.StartAgentRun(ctx, freshRun.ID); err != nil {
		t.Fatal(err)
	}
	old := formatTime(time.Now().UTC().Add(-2 * time.Hour))
	if _, err := database.db.ExecContext(ctx, `UPDATE agent_jobs SET updated_at = ? WHERE id = ?`, old, staleJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE agent_runs SET updated_at = ? WHERE id = ?`, old, staleRun.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE agent_jobs SET updated_at = ? WHERE id = ?`, old, freshJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE agent_runs SET updated_at = ? WHERE id = ?`, old, freshRun.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.TouchAgentJob(ctx, freshJob.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.TouchAgentRun(ctx, freshRun.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := database.RecoverStaleAgentWork(ctx, time.Now().UTC().Add(-time.Hour))
	if err != nil || recovered.Jobs != 1 || recovered.Runs != 1 {
		t.Fatalf("unexpected stale recovery result: %#v, %v", recovered, err)
	}
	staleJob, err = database.GetAgentJob(ctx, staleJob.ID)
	if err != nil || staleJob.Status != AgentStatusFailed || staleJob.CompletedAt == nil || staleJob.Error == "" {
		t.Fatalf("stale job was not failed durably: %#v, %v", staleJob, err)
	}
	staleRun, err = database.GetAgentRun(ctx, staleRun.ID)
	if err != nil || staleRun.Status != AgentStatusFailed || staleRun.CompletedAt == nil || staleRun.Error == "" {
		t.Fatalf("stale run was not failed durably: %#v, %v", staleRun, err)
	}
	freshJob, err = database.GetAgentJob(ctx, freshJob.ID)
	if err != nil || freshJob.Status != AgentStatusRunning {
		t.Fatalf("fresh running job was recovered incorrectly: %#v, %v", freshJob, err)
	}
	freshRun, err = database.GetAgentRun(ctx, freshRun.ID)
	if err != nil || freshRun.Status != AgentStatusRunning {
		t.Fatalf("fresh running run was recovered incorrectly: %#v, %v", freshRun, err)
	}
}

func TestRecoverStaleAgentWorkReloadsQueuedAndRunningParents(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(ctx, CreateConversationParams{Title: "Recovery", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}

	type work struct {
		message Message
		job     AgentJob
		run     AgentRun
	}
	createWork := func(task string, start bool) work {
		t.Helper()
		message, err := database.AddMessage(ctx, conversation.ID, "assistant", "", "streaming")
		if err != nil {
			t.Fatal(err)
		}
		job, err := database.CreateAgentJob(ctx, CreateAgentJobParams{
			ConversationID: conversation.ID, RequestingMessageID: message.ID, Task: task,
		})
		if err != nil {
			t.Fatal(err)
		}
		run, err := database.CreateAgentRun(ctx, CreateAgentRunParams{
			JobID: job.ID, Task: task, Model: "test", ContextWindow: 2048, Depth: 0,
		})
		if err != nil {
			t.Fatal(err)
		}
		if start {
			job, err = database.StartAgentJob(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			run, err = database.StartAgentRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		return work{message: message, job: job, run: run}
	}

	queued := createWork("queued before crash", false)
	running := createWork("running before crash", true)
	fresh := createWork("still active", true)
	old := formatTime(time.Now().UTC().Add(-2 * time.Hour))
	for _, item := range []work{queued, running} {
		if _, err := database.db.ExecContext(ctx, `UPDATE agent_jobs SET updated_at = ? WHERE id = ?`, old, item.job.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(ctx, `UPDATE agent_runs SET updated_at = ? WHERE id = ?`, old, item.run.ID); err != nil {
			t.Fatal(err)
		}
	}

	recovered, err := database.RecoverStaleAgentWork(ctx, time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Jobs != 2 || recovered.Runs != 2 || recovered.Messages != 2 {
		t.Fatalf("unexpected recovery counts: %#v", recovered)
	}
	messages, err := database.Messages(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[int64]Message, len(messages))
	for _, message := range messages {
		byID[message.ID] = message
	}
	for _, item := range []work{queued, running} {
		message := byID[item.message.ID]
		if message.Status != "error" || len(message.SubagentRuns) != 1 {
			t.Fatalf("recovered parent did not reload with one child: %#v", message)
		}
		run := message.SubagentRuns[0]
		if run.ID != item.run.ID || run.Status != AgentStatusFailed || run.CompletedAt == nil || run.Error != "interrupted before completion" {
			t.Fatalf("recovered child did not reload as failed: %#v", run)
		}
	}
	freshMessage := byID[fresh.message.ID]
	if freshMessage.Status != "streaming" || len(freshMessage.SubagentRuns) != 1 || freshMessage.SubagentRuns[0].Status != AgentStatusRunning {
		t.Fatalf("fresh parent or child was recovered incorrectly: %#v", freshMessage)
	}
	freshJob, err := database.GetAgentJob(ctx, fresh.job.ID)
	if err != nil || freshJob.Status != AgentStatusRunning {
		t.Fatalf("fresh job was recovered incorrectly: %#v, %v", freshJob, err)
	}
}
