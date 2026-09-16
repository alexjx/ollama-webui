package store

import (
	"context"
	"errors"
	"testing"
)

func TestDeleteMessagesFromCascadesAndPreservesEarlierTurns(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conversation, err := db.CreateConversation(ctx, CreateConversationParams{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	add := func(role string) Message {
		t.Helper()
		m, err := db.AddMessageWithAttachments(ctx, conversation.ID, role, "content", "complete", []NewAttachment{{FileName: "image", MediaType: "image/png", Data: []byte("image")}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	first, earlier, target, last := add("user"), add("assistant"), add("user"), add("assistant")
	if _, err := db.EnsureConversationContext(ctx, conversation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertCheckpoint(ctx, UpsertCheckpointParams{ConversationID: conversation.ID, ThroughMessageID: last.ID, Summary: "deleted history"}); err != nil {
		t.Fatal(err)
	}
	step, err := db.BeginAgentStep(ctx, last.ID, 1, "shell", "{}")
	if err != nil {
		t.Fatal(err)
	}
	job, err := db.CreateAgentJob(ctx, CreateAgentJobParams{ConversationID: conversation.ID, RequestingMessageID: last.ID, Task: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAgentRun(ctx, CreateAgentRunParams{JobID: job.ID, Task: "test", Model: "test", ContextWindow: 100}); err != nil {
		t.Fatal(err)
	}
	retained, err := db.CreateContextArtifact(ctx, CreateContextArtifactParams{ConversationID: conversation.ID, SourceMessageID: &earlier.ID, Kind: "text", SHA256: "hash"})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := db.CreateContextArtifact(ctx, CreateContextArtifactParams{ConversationID: conversation.ID, SourceStepID: &step.ID, Kind: "text", SHA256: "hash"})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := db.DeleteMessagesFrom(ctx, conversation.ID, target.ID)
	if err != nil || len(keys) != 1 || keys[0] != removed.StorageKey {
		t.Fatalf("delete: keys=%v err=%v", keys, err)
	}
	messages, err := db.Messages(ctx, conversation.ID)
	if err != nil || len(messages) != 2 || messages[0].ID != first.ID || messages[1].ID != earlier.ID {
		t.Fatalf("remaining=%+v err=%v", messages, err)
	}
	stats, err := db.Stats(ctx)
	if err != nil || stats.Messages != 2 || stats.Attachments != 2 || stats.AgentSteps != 0 || stats.AgentJobs != 0 || stats.AgentRuns != 0 || stats.ContextCheckpoints != 0 || stats.ContextArtifacts != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	artifacts, err := db.ListContextArtifacts(ctx, conversation.ID)
	if err != nil || len(artifacts) != 1 || artifacts[0].ID != retained.ID {
		t.Fatalf("artifacts=%+v err=%v", artifacts, err)
	}
	if _, err := db.GetAttachment(ctx, target.Attachments[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted attachment: %v", err)
	}
	updated, err := db.GetConversation(ctx, conversation.ID)
	if err != nil || updated.MessageCount != 2 || updated.LastMessageAt == nil || !updated.LastMessageAt.Equal(earlier.CreatedAt) {
		t.Fatalf("conversation=%+v err=%v", updated, err)
	}
	if _, err := db.DeleteMessagesFrom(ctx, conversation.ID, earlier.ID); !errors.Is(err, ErrInvalidTurn) {
		t.Fatalf("assistant boundary: %v", err)
	}
	other, err := db.CreateConversation(ctx, CreateConversationParams{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteMessagesFrom(ctx, other.ID, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign boundary: %v", err)
	}
	if _, err := db.DeleteMessagesFrom(ctx, conversation.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	updated, err = db.GetConversation(ctx, conversation.ID)
	if err != nil || updated.MessageCount != 0 || updated.LastMessageAt != nil {
		t.Fatalf("empty conversation=%+v err=%v", updated, err)
	}
	add("user")
	messages, err = db.Messages(ctx, conversation.ID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("reuse conversation=%+v err=%v", messages, err)
	}
}
