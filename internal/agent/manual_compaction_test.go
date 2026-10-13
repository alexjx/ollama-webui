package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func TestForcedCompactionBelowThresholdPreservesRecentTail(t *testing.T) {
	history := compactTestHistory(strings.Repeat("a", 1500), strings.Repeat("b", 1500))
	history = append(history, store.Message{ID: 8, Role: "assistant", Status: "cancelled"})
	memory := &checkpointMemory{}
	chat := &summaryChat{summary: "Preserved goal and decisions."}
	result, err := (ContextPreparer{Chat: chat, Store: memory}).Prepare(context.Background(), ContextPrepareInput{
		ConversationID: 7, Model: "model", History: history, ContextWindow: 32768, Force: true,
	})
	if err != nil || !result.Changed || result.BeforeTokens >= 32768*contextTriggerPercent/100 || result.EstimatedTokens >= result.BeforeTokens {
		t.Fatalf("forced compaction did not reduce small context: %#v, %v", result, err)
	}
	if len(memory.upserts) != 1 || memory.upserts[0].ThroughMessageID != 2 || memory.upserts[0].State["compacted_after_message_id"] != int64(8) {
		t.Fatalf("wrong persisted checkpoint: %#v", memory.upserts)
	}
	for index, message := range history[2:7] {
		if result.Messages[index+1].Content != message.Content {
			t.Fatalf("recent message %d changed: %#v", message.ID, result.Messages)
		}
	}
}

func TestForcedCompactionNoOpsDoNotOverwriteCheckpoint(t *testing.T) {
	for _, test := range []struct {
		name, summary, reason string
		history               []store.Message
		checkpoint            store.ContextCheckpoint
	}{
		{name: "short", history: compactTestHistory("short", "answer")[:4], reason: "not_enough_history"},
		{name: "not reduced", history: compactTestHistory("a", "b"), summary: strings.Repeat("verbose", 100), reason: "not_reduced"},
		{name: "repeated", history: compactTestHistory("a", "b")[:6], checkpoint: store.ContextCheckpoint{ConversationID: 7, ThroughMessageID: 2, Summary: "existing memory"}, reason: "not_enough_history"},
	} {
		t.Run(test.name, func(t *testing.T) {
			memory := &checkpointMemory{checkpoint: test.checkpoint, found: test.checkpoint.ThroughMessageID != 0}
			result, err := (ContextPreparer{Chat: &summaryChat{summary: test.summary}, Store: memory}).Prepare(context.Background(), ContextPrepareInput{
				ConversationID: 7, Model: "model", History: test.history, ContextWindow: 32768, Force: true,
			})
			if err != nil || result.Changed || result.Reason != test.reason || result.EstimatedTokens != result.BeforeTokens || len(memory.upserts) != 0 {
				t.Fatalf("incorrect no-op: %#v, %v, writes=%#v", result, err, memory.upserts)
			}
		})
	}
}

func TestForcedCompactionErrorsDoNotReplaceMemory(t *testing.T) {
	for _, test := range []struct {
		name                        string
		getErr, summaryErr, saveErr error
		summary                     string
	}{
		{name: "read", getErr: errors.New("read failed")},
		{name: "summary", summaryErr: errors.New("offline")},
		{name: "empty", summary: " "},
		{name: "save", saveErr: errors.New("read-only storage"), summary: "good summary"},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := store.ContextCheckpoint{ConversationID: 7, ThroughMessageID: 2, Summary: "previous stable memory"}
			memory := &checkpointMemory{getErr: test.getErr, upsertErr: test.saveErr, found: true, checkpoint: previous}
			history := compactTestHistory(strings.Repeat("a", 2400), strings.Repeat("b", 2400))
			history[2].Content = strings.Repeat("next older input", 600)
			history = append(history, store.Message{ID: 8, Role: "assistant", Content: "recent answer", Status: "complete"}, store.Message{ID: 9, Role: "user", Content: "latest request", Status: "complete"})
			result, err := (ContextPreparer{Chat: &summaryChat{summary: test.summary, err: test.summaryErr}, Store: memory}).Prepare(context.Background(), ContextPrepareInput{
				ConversationID: 7, Model: "model", History: history, ContextWindow: 1000, Force: true,
			})
			if err == nil || result.Changed || memory.checkpoint.Summary != previous.Summary || memory.checkpoint.ThroughMessageID != previous.ThroughMessageID {
				t.Fatalf("manual error was hidden: %#v, %v", result, err)
			}
			if test.saveErr == nil && len(memory.upserts) != 0 {
				t.Fatal("failure attempted to overwrite checkpoint")
			}
		})
	}
}

type cancelSummaryChat struct{ cancel context.CancelFunc }

func (chat cancelSummaryChat) Chat(_ context.Context, _ ollama.ChatRequest, emit func(ollama.ChatChunk) error) error {
	chat.cancel()
	return emit(ollama.ChatChunk{Message: ollama.Message{Content: "summary"}})
}

func TestForcedCompactionCancellationAfterSummaryDoesNotPersist(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	memory := &checkpointMemory{}
	_, err := (ContextPreparer{Chat: cancelSummaryChat{cancel}, Store: memory}).Prepare(ctx, ContextPrepareInput{
		ConversationID: 7, Model: "model", History: compactTestHistory(strings.Repeat("a", 2400), strings.Repeat("b", 2400)), ContextWindow: 1000, Force: true,
	})
	if !errors.Is(err, context.Canceled) || len(memory.upserts) != 0 {
		t.Fatalf("cancelled request persisted: %v, %#v", err, memory.upserts)
	}
}

func TestChatCheckpointReusesTextAttachmentsWithoutAutomaticCompaction(t *testing.T) {
	history := compactTestHistory("old input", "old output")
	history[2].Attachments = []store.Attachment{{ID: 20, FileName: "recent.txt", MediaType: "text/plain", Data: []byte("RECENT_FILE_CONTENT")}}
	history[2].Attachments = append(history[2].Attachments, store.Attachment{MediaType: "image/png", Data: []byte{1, 2}})
	memory := &checkpointMemory{found: true, checkpoint: store.ContextCheckpoint{ConversationID: 7, ThroughMessageID: 2, Summary: "old memory"}}
	chat := &summaryChat{}
	result, err := (ContextPreparer{Chat: chat, Store: memory}).Prepare(context.Background(), ContextPrepareInput{
		ConversationID: 7, Model: "chat", History: history, ContextWindow: 1, IncludeTextAttachments: true, SkipAutomaticCompaction: true,
	})
	if err != nil || len(chat.requests) != 0 || len(memory.upserts) != 0 || !strings.Contains(result.Messages[0].Content, "old memory") || !strings.Contains(result.Messages[1].Content, "RECENT_FILE_CONTENT") || len(result.Messages[1].Images) != 1 {
		t.Fatalf("chat checkpoint lost data or compacted automatically: %#v, %v", result, err)
	}
}
