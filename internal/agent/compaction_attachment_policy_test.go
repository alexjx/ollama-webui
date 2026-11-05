package agent

import (
	"context"
	"strings"
	"testing"

	"ollama-webui/internal/store"
)

func TestReferenceOnlyCheckpointRebuildsForChatWithoutFileTools(t *testing.T) {
	history := compactTestHistory(strings.Repeat("old input", 300), strings.Repeat("old answer", 300))
	history[0].Attachments = []store.Attachment{{ID: 10, FileName: "pasted.txt", MediaType: "text/plain", Data: []byte("ORIGINAL_PASTED_CONTENT")}}
	memory := &checkpointMemory{}
	preparer := ContextPreparer{Chat: &summaryChat{summary: "Preserved attachment reference."}, Store: memory}
	_, err := preparer.Prepare(context.Background(), ContextPrepareInput{
		ConversationID: 7, Model: "tools", History: history, ContextWindow: 32768, Force: true,
	})
	if err != nil || len(memory.upserts) != 1 {
		t.Fatalf("could not compact attachment references: writes=%#v err=%v", memory.upserts, err)
	}
	saved := memory.upserts[0]
	if saved.State["text_attachments_inline"] != false || saved.State["compacted_after_message_id"] != int64(7) {
		t.Fatalf("checkpoint lost attachment policy or manual-compaction boundary: %#v", saved.State)
	}
	memory.checkpoint = store.ContextCheckpoint{ConversationID: 7, ThroughMessageID: saved.ThroughMessageID, Summary: saved.Summary, State: saved.State}
	memory.found = true
	result, err := preparer.Prepare(context.Background(), ContextPrepareInput{
		ConversationID: 7, Model: "no-tools", History: history, ContextWindow: 32768,
		IncludeTextAttachments: true, SkipAutomaticCompaction: true,
	})
	if err != nil || result.Compacted || len(result.Messages) != len(history) || !strings.Contains(result.Messages[0].Content, "ORIGINAL_PASTED_CONTENT") {
		t.Fatalf("non-tool Chat could not recover original text: %#v err=%v", result, err)
	}
	if len(memory.upserts) != 1 {
		t.Fatal("rebuilding the view unexpectedly replaced the saved checkpoint")
	}
}

func TestInlineChatKeepsLegacyAndInlineCheckpoints(t *testing.T) {
	for _, state := range []map[string]any{nil, {"text_attachments_inline": true}} {
		history := compactTestHistory("old input", "old answer")
		history[0].Attachments = []store.Attachment{{ID: 10, FileName: "pasted.txt", MediaType: "text/plain", Data: []byte("OLD_PASTE")}}
		memory := &checkpointMemory{found: true, checkpoint: store.ContextCheckpoint{
			ConversationID: 7, ThroughMessageID: 2, Summary: "Existing memory", State: state,
		}}
		result, err := (ContextPreparer{Store: memory}).Prepare(context.Background(), ContextPrepareInput{
			ConversationID: 7, History: history, IncludeTextAttachments: true, SkipAutomaticCompaction: true,
		})
		if err != nil || !result.Compacted || !strings.Contains(result.Messages[0].Content, "Existing memory") || len(result.Messages) != len(history)-1 {
			t.Fatalf("compatible checkpoint was discarded: state=%#v result=%#v err=%v", state, result, err)
		}
	}
}

func TestIncrementalCompactionCarriesAttachmentPolicyWithoutFlaggingFilelessHistory(t *testing.T) {
	for _, oldState := range []map[string]any{nil, {"text_attachments_inline": false}} {
		history := compactTestHistory(strings.Repeat("old input", 300), strings.Repeat("old answer", 300))
		history[2].Content = strings.Repeat("next input", 300)
		history[3].Content = strings.Repeat("next answer", 300)
		history = append(history, store.Message{ID: 8, Role: "assistant", Content: "recent answer", Status: "complete"}, store.Message{ID: 9, Role: "user", Content: "new request", Status: "complete"})
		memory := &checkpointMemory{found: true, checkpoint: store.ContextCheckpoint{
			ConversationID: 7, ThroughMessageID: 2, Summary: "Earlier memory", State: oldState,
		}}
		_, err := (ContextPreparer{Chat: &summaryChat{summary: "Updated memory"}, Store: memory}).Prepare(context.Background(), ContextPrepareInput{
			ConversationID: 7, History: history, ContextWindow: 32768, Force: true,
		})
		if err != nil || len(memory.upserts) != 1 {
			t.Fatalf("incremental compaction failed: %#v err=%v", memory.upserts, err)
		}
		inline, recorded := memory.upserts[0].State["text_attachments_inline"]
		if recorded != (oldState != nil) || (recorded && inline != false) {
			t.Fatalf("incorrect inherited policy: old=%#v saved=%#v", oldState, memory.upserts[0].State)
		}
	}
}
