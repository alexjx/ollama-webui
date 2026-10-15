package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestForkConversationPreservesContextAndIsolatesDeletion(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	window := 8192
	temperature := 0.3
	thinking := "high"
	search := false
	source, err := db.CreateConversation(ctx, CreateConversationParams{Title: "Research", AutoTitle: true, Model: "model", Mode: "agent", SystemPrompt: "Remember everything", ContextWindow: &window, Temperature: &temperature, ThinkingMode: &thinking, WebSearchEnabled: &search})
	check(err)
	user, err := db.AddMessageWithAttachments(ctx, source.ID, "user", "question", "complete", []NewAttachment{{FileName: "image.png", MediaType: "image/png", Data: []byte("original bytes")}})
	check(err)
	answer, err := db.AddMessage(ctx, source.ID, "assistant", "answer", "complete")
	check(err)
	step, err := db.BeginAgentStep(ctx, answer.ID, 1, "shell", `{"command":"ls"}`)
	check(err)
	artifact, err := db.CreateContextArtifact(ctx, CreateContextArtifactParams{ConversationID: source.ID, SourceMessageID: &answer.ID, SourceStepID: &step.ID, Kind: "tool_output", SizeBytes: 4, SHA256: "hash", Summary: "artifact summary"})
	check(err)
	_, err = db.CompleteAgentStep(ctx, step.ID, `{"artifact_id":"`+artifact.StorageKey+`"}`, 0, "complete")
	check(err)
	check(db.UpdateMessage(ctx, answer.ID, "answer "+artifact.StorageKey+" "+user.Attachments[0].URL, "reasoning", "complete", map[string]any{"eval_count": 42, "context_through_message_id": user.ID}))
	_, err = db.UpsertCheckpoint(ctx, UpsertCheckpointParams{ConversationID: source.ID, ThroughMessageID: user.ID, Summary: "summary " + artifact.StorageKey, State: map[string]any{"compacted_after_message_id": answer.ID}, EstimatedTokens: 12})
	check(err)
	job, err := db.CreateAgentJob(ctx, CreateAgentJobParams{ConversationID: source.ID, RequestingMessageID: answer.ID, Task: "research"})
	check(err)
	_, err = db.StartAgentJob(ctx, job.ID)
	check(err)
	run, err := db.CreateAgentRun(ctx, CreateAgentRunParams{JobID: job.ID, Task: "parent", Model: "model", ContextWindow: 8192})
	check(err)
	child, err := db.CreateAgentRun(ctx, CreateAgentRunParams{JobID: job.ID, ParentRunID: &run.ID, Task: "child", Model: "model", ContextWindow: 8192, Depth: 1})
	check(err)
	for _, id := range []int64{run.ID, child.ID} {
		_, err = db.StartAgentRun(ctx, id)
		check(err)
		_, err = db.CompleteAgentRun(ctx, id, "done", []string{artifact.StorageKey})
		check(err)
	}
	_, err = db.CompleteAgentJob(ctx, job.ID, "done", []string{artifact.StorageKey})
	check(err)
	before, err := db.MessagesWithAttachmentData(ctx, source.ID)
	check(err)
	var copiedKey string
	fork, err := db.ForkConversation(ctx, source.ID, func(sourceKey, targetKey string, keys map[string]string) error {
		if sourceKey == targetKey {
			t.Fatal("shared context directory")
		}
		copiedKey = keys[artifact.StorageKey]
		if copiedKey == "" || copiedKey == artifact.StorageKey {
			t.Fatal("shared artifact key")
		}
		return nil
	})
	check(err)
	if fork.ID == source.ID || fork.Title != "Fork of Research" || fork.AutoTitlePending || fork.Model != source.Model || fork.Mode != source.Mode || fork.SystemPrompt != source.SystemPrompt || *fork.ContextWindow != window || *fork.Temperature != temperature || *fork.ThinkingMode != thinking || fork.WebSearchEnabled {
		t.Fatalf("settings not preserved: %#v", fork)
	}
	after, err := db.MessagesWithAttachmentData(ctx, source.ID)
	check(err)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("source changed")
	}
	messages, err := db.MessagesWithAttachmentData(ctx, fork.ID)
	check(err)
	if len(messages) != 2 || messages[0].ID == user.ID || messages[1].ID == answer.ID || messages[1].Thinking != "reasoning" || messages[1].Metadata["eval_count"] != float64(42) || messages[1].Metadata["context_through_message_id"] != float64(messages[0].ID) {
		t.Fatalf("messages not preserved: %#v", messages)
	}
	if string(messages[0].Attachments[0].Data) != "original bytes" || messages[0].Attachments[0].ID == user.Attachments[0].ID {
		t.Fatal("attachment not copied")
	}
	if !strings.Contains(messages[1].Content, fmt.Sprintf("/api/attachments/%d", messages[0].Attachments[0].ID)) {
		t.Fatal("embedded attachment URL not remapped")
	}
	if !strings.Contains(messages[1].AgentSteps[0].Output, copiedKey) || messages[1].AgentSteps[0].ID == step.ID {
		t.Fatal("step not remapped")
	}
	runs := messages[1].SubagentRuns
	if len(runs) != 2 || runs[1].ParentRunID == nil || *runs[1].ParentRunID != runs[0].ID || runs[0].JobID == job.ID || runs[0].OutputRefs[0] != copiedKey {
		t.Fatalf("runs not copied: %#v", runs)
	}
	checkpoint, err := db.GetCheckpoint(ctx, fork.ID)
	check(err)
	if checkpoint.ThroughMessageID != messages[0].ID || checkpoint.State["compacted_after_message_id"] != float64(messages[1].ID) || !strings.Contains(checkpoint.Summary, copiedKey) {
		t.Fatalf("checkpoint not remapped: %#v", checkpoint)
	}
	artifacts, err := db.ListContextArtifacts(ctx, fork.ID)
	check(err)
	if len(artifacts) != 1 || *artifacts[0].SourceMessageID != messages[1].ID || *artifacts[0].SourceStepID != messages[1].AgentSteps[0].ID {
		t.Fatal("artifact references not remapped")
	}
	check(db.DeleteConversation(ctx, source.ID))
	remaining, err := db.MessagesWithAttachmentData(ctx, fork.ID)
	check(err)
	if !reflect.DeepEqual(messages, remaining) {
		t.Fatal("deleting source affected fork")
	}
	_, err = db.GetCheckpoint(ctx, fork.ID)
	check(err)
}

func TestForkConversationRollbackAndMissingSource(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ForkConversation(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing source: %v", err)
	}
	source, err := db.CreateConversation(ctx, CreateConversationParams{Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ForkConversation(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	_, err = db.CreateContextArtifact(ctx, CreateContextArtifactParams{ConversationID: source.ID, Kind: "output", SizeBytes: 0})
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("disk full")
	for _, copyFn := range []func(string, string, map[string]string) error{nil, func(string, string, map[string]string) error { return failure }} {
		_, err = db.ForkConversation(ctx, source.ID, copyFn)
		if err == nil {
			t.Fatal("expected fork failure")
		}
		if copyFn != nil && !errors.Is(err, failure) {
			t.Fatal(err)
		}
		after, err := db.Stats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("failed fork left records: before %#v after %#v", before, after)
		}
	}
}

func TestForkConversationCancelsStaleWorkAndRemapsToolReferences(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	source, err := db.CreateConversation(ctx, CreateConversationParams{Model: "model"})
	check(err)
	user, err := db.AddMessageWithAttachments(ctx, source.ID, "user", "file", "complete", []NewAttachment{{FileName: "a.txt", MediaType: "text/plain", Data: []byte("file")}})
	check(err)
	answer, err := db.AddMessage(ctx, source.ID, "assistant", fmt.Sprintf("attachment_id=%d", user.Attachments[0].ID), "streaming")
	check(err)
	_, err = db.BeginAgentStep(ctx, answer.ID, 1, "attachment_read", fmt.Sprintf(`{"attachment_id":%d,"message_id":%d}`, user.Attachments[0].ID, user.ID))
	check(err)
	job, err := db.CreateAgentJob(ctx, CreateAgentJobParams{ConversationID: source.ID, RequestingMessageID: answer.ID, Task: "work"})
	check(err)
	_, err = db.CreateAgentRun(ctx, CreateAgentRunParams{JobID: job.ID, Task: "work", Model: "model", ContextWindow: 1024})
	check(err)
	fork, err := db.ForkConversation(ctx, source.ID)
	check(err)
	messages, err := db.Messages(ctx, fork.ID)
	check(err)
	if messages[1].Status != "cancelled" || messages[1].AgentSteps[0].Status != "cancelled" || messages[1].SubagentRuns[0].Status != "cancelled" {
		t.Fatalf("fork retained stale work: %#v", messages[1])
	}
	jobs, err := db.ListAgentJobsByRequestingMessage(ctx, messages[1].ID)
	check(err)
	if jobs[0].Status != "cancelled" {
		t.Fatal("fork retained queued job")
	}
	if messages[1].Content != fmt.Sprintf("attachment_id=%d", messages[0].Attachments[0].ID) || messages[1].AgentSteps[0].Input != fmt.Sprintf(`{"attachment_id":%d,"message_id":%d}`, messages[0].Attachments[0].ID, messages[0].ID) {
		t.Fatal("attachment tool references were not remapped")
	}
	original, err := db.Messages(ctx, source.ID)
	check(err)
	if original[1].Status != "streaming" {
		t.Fatal("source work changed")
	}
}
