package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

type checkpointMemory struct {
	checkpoint store.ContextCheckpoint
	found      bool
	upserts    []store.UpsertCheckpointParams
	getErr     error
	upsertErr  error
}

func (memory *checkpointMemory) GetCheckpoint(context.Context, int64) (store.ContextCheckpoint, error) {
	if memory.getErr != nil {
		return store.ContextCheckpoint{}, memory.getErr
	}
	if !memory.found {
		return store.ContextCheckpoint{}, store.ErrNotFound
	}
	return memory.checkpoint, nil
}

func (memory *checkpointMemory) UpsertCheckpoint(_ context.Context, params store.UpsertCheckpointParams) (store.ContextCheckpoint, error) {
	memory.upserts = append(memory.upserts, params)
	if memory.upsertErr != nil {
		return store.ContextCheckpoint{}, memory.upsertErr
	}
	return store.ContextCheckpoint{ConversationID: params.ConversationID, ThroughMessageID: params.ThroughMessageID, Summary: params.Summary}, nil
}

type summaryChat struct {
	requests []ollama.ChatRequest
	summary  string
	err      error
}

func (chat *summaryChat) Chat(_ context.Context, request ollama.ChatRequest, onChunk func(ollama.ChatChunk) error) error {
	chat.requests = append(chat.requests, request)
	if chat.err != nil {
		return chat.err
	}
	return onChunk(ollama.ChatChunk{Message: ollama.Message{Content: chat.summary}, Done: true})
}

func TestContextPreparerLeavesSmallHistoryUnchanged(t *testing.T) {
	history := compactTestHistory("short", "answer")
	memory := &checkpointMemory{}
	chat := &summaryChat{summary: "unused"}
	result, err := (ContextPreparer{Chat: chat, Store: memory}).Prepare(context.Background(), ContextPrepareInput{
		ConversationID: 7, Model: "model", SystemPrompt: "system", History: history, ContextWindow: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Compacted || len(chat.requests) != 0 || len(memory.upserts) != 0 {
		t.Fatalf("small history was compacted: %#v", result)
	}
	if len(result.Messages) != len(history)+1 || result.Messages[len(result.Messages)-1].Content != history[len(history)-1].Content {
		t.Fatalf("history changed: %#v", result.Messages)
	}
}

func TestContextPreparerSummarizesPrefixAndKeepsRecentTail(t *testing.T) {
	history := compactTestHistory(strings.Repeat("a", 2400), strings.Repeat("b", 2400))
	memory := &checkpointMemory{}
	chat := &summaryChat{summary: "- preserved memory"}
	result, err := (ContextPreparer{Chat: chat, Store: memory}).Prepare(context.Background(), ContextPrepareInput{
		ConversationID: 7, Model: "model", SystemPrompt: "system", History: history, ContextWindow: 2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted || result.Fallback || result.ThroughMessageID != 2 {
		t.Fatalf("unexpected compaction result: %#v", result)
	}
	if len(result.Messages) != 6 || result.Messages[1].Content != history[2].Content || result.Messages[len(result.Messages)-1].Content != history[6].Content {
		t.Fatalf("recent tail was not preserved: %#v", result.Messages)
	}
	if !strings.Contains(result.Messages[0].Content, "preserved memory") || len(memory.upserts) != 1 || memory.upserts[0].ThroughMessageID != 2 {
		t.Fatalf("summary checkpoint was not used and persisted: result=%#v upserts=%#v", result, memory.upserts)
	}
	if history[0].Content != strings.Repeat("a", 2400) {
		t.Fatal("input history was mutated")
	}
}

func TestContextPreparerUpdatesCheckpointIncrementally(t *testing.T) {
	history := compactTestHistory(strings.Repeat("a", 1200), strings.Repeat("b", 1200))
	history[2].Content = strings.Repeat("c", 600)
	history[3].Content = strings.Repeat("d", 600)
	history = append(history,
		store.Message{ID: 8, Role: "assistant", Content: "recent answer", Status: "complete"},
		store.Message{ID: 9, Role: "user", Content: "current request", Status: "complete"},
	)
	memory := &checkpointMemory{found: true, checkpoint: store.ContextCheckpoint{ConversationID: 7, ThroughMessageID: 2, Summary: "old memory"}}
	chat := &summaryChat{summary: "updated memory"}
	result, err := (ContextPreparer{Chat: chat, Store: memory}).Prepare(context.Background(), ContextPrepareInput{
		ConversationID: 7, Model: "model", History: history, ContextWindow: 350,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ThroughMessageID != 4 || len(chat.requests) != 1 || len(memory.upserts) != 1 {
		t.Fatalf("checkpoint did not advance incrementally: result=%#v upserts=%#v", result, memory.upserts)
	}
	var prompt strings.Builder
	for _, message := range chat.requests[0].Messages {
		prompt.WriteString(message.Content)
	}
	promptText := prompt.String()
	if !strings.Contains(promptText, "old memory") || !strings.Contains(promptText, "message 3") || !strings.Contains(promptText, "message 4") || strings.Contains(promptText, "message 1") {
		t.Fatalf("wrong incremental summary input: %s", promptText)
	}
}

func TestContextPreparerSummaryFailureBelowHardLimitUsesOriginalView(t *testing.T) {
	history := compactTestHistory(strings.Repeat("a", 1050), strings.Repeat("b", 1050))
	memory := &checkpointMemory{}
	result, err := (ContextPreparer{Chat: &summaryChat{err: errors.New("offline")}, Store: memory}).Prepare(context.Background(), ContextPrepareInput{
		ConversationID: 7, Model: "model", History: history, ContextWindow: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Compacted || result.Fallback || result.CompactionError != "offline" || len(result.Messages) != len(history) {
		t.Fatalf("unexpected soft failure behavior: %#v", result)
	}
	if len(memory.upserts) != 0 {
		t.Fatal("failed summary overwrote the checkpoint")
	}
}

func TestContextPreparerSummaryFailureAboveHardLimitUsesBoundedFallback(t *testing.T) {
	history := compactTestHistory(strings.Repeat("界", 700), strings.Repeat("文", 700))
	memory := &checkpointMemory{}
	result, err := (ContextPreparer{Chat: &summaryChat{err: errors.New("offline")}, Store: memory}).Prepare(context.Background(), ContextPrepareInput{
		ConversationID: 7, Model: "model", History: history, ContextWindow: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted || !result.Fallback || result.ThroughMessageID != 2 || len(memory.upserts) != 0 {
		t.Fatalf("unexpected hard failure behavior: %#v", result)
	}
	if estimateTextTokens(strings.TrimPrefix(result.Messages[0].Content, "Conversation memory (a compact summary of older messages):\n")) > 500 {
		t.Fatalf("deterministic fallback was not bounded: %q", result.Messages[0].Content)
	}
	if result.Messages[len(result.Messages)-1].Content != "current request" {
		t.Fatal("fallback lost the current user message")
	}
}

func TestContextPreparerCancellationDoesNotPersist(t *testing.T) {
	history := compactTestHistory(strings.Repeat("a", 2400), strings.Repeat("b", 2400))
	memory := &checkpointMemory{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (ContextPreparer{Chat: &summaryChat{err: context.Canceled}, Store: memory}).Prepare(ctx, ContextPrepareInput{
		ConversationID: 7, Model: "model", History: history, ContextWindow: 1000,
	})
	if !errors.Is(err, context.Canceled) || len(memory.upserts) != 0 {
		t.Fatalf("cancellation was not preserved: err=%v upserts=%#v", err, memory.upserts)
	}
}

func TestContextPreparerSummarizesAttachmentsWithoutDroppingImageInput(t *testing.T) {
	history := compactTestHistory(strings.Repeat("a", 2400), strings.Repeat("b", 2400))
	history[1].Attachments = []store.Attachment{{ID: 1, Data: []byte{1, 2, 3}}}
	chat := &summaryChat{summary: "image facts preserved"}
	result, err := (ContextPreparer{Chat: chat, Store: &checkpointMemory{}}).Prepare(context.Background(), ContextPrepareInput{
		ConversationID: 7, Model: "model", History: history, ContextWindow: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted || len(chat.requests) != 1 {
		t.Fatalf("history containing an attachment was not compacted: %#v", result)
	}
	if len(chat.requests[0].Messages) < 4 || len(chat.requests[0].Messages[3].Images) != 1 {
		t.Fatalf("summarizer did not receive attachment data: %#v", chat.requests[0].Messages)
	}
	if !strings.Contains(result.Messages[0].Content, "image facts preserved") || result.Messages[len(result.Messages)-1].Content != "current request" {
		t.Fatalf("summary or current request was lost: %#v", result.Messages)
	}
}

func TestContextPreparerSummaryRequestOmitsThinkingOverride(t *testing.T) {
	history := compactTestHistory(strings.Repeat("a", 2400), strings.Repeat("b", 2400))
	chat := &summaryChat{summary: "memory"}
	_, err := (ContextPreparer{Chat: chat, Store: &checkpointMemory{}}).Prepare(context.Background(), ContextPrepareInput{
		ConversationID: 7, Model: "model", History: history, ContextWindow: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.requests) != 1 || chat.requests[0].Think != nil || len(chat.requests[0].Tools) != 0 {
		t.Fatalf("summary request used optional model capabilities: %#v", chat.requests)
	}
}

func TestEstimateTextTokensIsConservativeForNonASCII(t *testing.T) {
	if got := estimateTextTokens("abcdef世界"); got != 4 {
		t.Fatalf("unexpected token estimate: %d", got)
	}
}

func compactTestHistory(first, second string) []store.Message {
	return []store.Message{
		{ID: 1, Role: "user", Content: first, Status: "complete"},
		{ID: 2, Role: "assistant", Content: second, Status: "complete"},
		{ID: 3, Role: "user", Content: "follow-up one", Status: "complete"},
		{ID: 4, Role: "assistant", Content: "answer one", Status: "complete"},
		{ID: 5, Role: "user", Content: "follow-up two", Status: "complete"},
		{ID: 6, Role: "assistant", Content: "answer two", Status: "complete"},
		{ID: 7, Role: "user", Content: "current request", Status: "complete"},
	}
}
