package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func TestRunnerKeepsLargeInlineInputUnchangedWithoutWritingFiles(t *testing.T) {
	workspace := t.TempDir()
	content := "  " + strings.Repeat("中文\n", 10000) + "  "
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{{{Message: ollama.Message{Content: "done"}, Done: true}}}}
	runner := Runner{Chat: chat, MaxTurns: 1, Shell: ShellExecutor{Workspace: workspace}}
	_, err := runner.Run(context.Background(), RunInput{Messages: []ollama.Message{{Role: "user", Content: content}}}, func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.requests) != 1 || chat.requests[0].Messages[0].Content != content {
		t.Fatal("large inline user text was replaced")
	}
	entries, err := os.ReadDir(workspace)
	if err != nil || len(entries) != 0 {
		t.Fatalf("raw input wrote workspace files: %#v, %v", entries, err)
	}
}

func TestModelMessageDistinguishesImagesAndTextFiles(t *testing.T) {
	message := store.Message{Role: "user", Content: "keep prompt", Attachments: []store.Attachment{
		{ID: 7, FileName: "pasted.txt", MediaType: "text/plain", Size: 15, Data: []byte(" exact\r\n世界\n ")},
		{ID: 8, MediaType: "image/png", Data: []byte{1, 2, 3}},
	}}
	for _, includeText := range []bool{false, true} {
		converted := ModelMessage(message, includeText)
		if len(converted.Images) != 1 || string(converted.Images[0]) != string([]byte{1, 2, 3}) || !strings.HasPrefix(converted.Content, message.Content) || !strings.Contains(converted.Content, "attachment_id=7") {
			t.Fatalf("lost attachment type or prompt: %#v", converted)
		}
		if strings.Contains(converted.Content, string(message.Attachments[0].Data)) != includeText {
			t.Fatalf("wrong file text visibility: %q", converted.Content)
		}
	}
	if MessageImageCount(message) != 1 {
		t.Fatal("text file counted as an image")
	}
}

func TestAttachmentToolsReadScopedUTF8PagesAndRediscoverFiles(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	content := "a世界\r\n" + strings.Repeat("\"<&\n", 3000)
	message, err := database.AddMessageWithAttachments(ctx, conversationID, "user", "", "complete", []store.NewAttachment{
		{FileName: "pasted.txt", MediaType: "text/plain", Data: []byte(content)},
		{FileName: "image.png", MediaType: "image/png", Data: []byte{1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateConversation(ctx, store.CreateConversationParams{Model: "tools"})
	if err != nil {
		t.Fatal(err)
	}
	runner := Runner{Attachments: database, ToolFeedbackLimit: 8192}
	id := message.Attachments[0].ID
	listed, status := runner.executeAttachmentTool(ctx, conversationID, "attachment_list", json.RawMessage(`{"limit":1}`))
	if status != "complete" || !strings.Contains(listed.Output, "pasted.txt") || strings.Contains(listed.Output, "image.png") || strings.Contains(listed.Output, "content") {
		t.Fatalf("list did not return text metadata only: %s", listed.Output)
	}
	listed, status = runner.executeAttachmentTool(ctx, other.ID, "attachment_list", json.RawMessage(`{}`))
	if status != "complete" || strings.Contains(listed.Output, "pasted.txt") {
		t.Fatalf("list leaked another conversation: %s", listed.Output)
	}
	for _, raw := range []string{
		fmt.Sprintf(`{"attachment_id":%d,"offset":2}`, id),
		fmt.Sprintf(`{"attachment_id":%d,"offset":-1}`, id),
		fmt.Sprintf(`{"attachment_id":%d,"offset":999999}`, id),
		fmt.Sprintf(`{"attachment_id":%d,"limit":8193}`, id),
		fmt.Sprintf(`{"attachment_id":%d,"offset":1,"limit":1}`, id),
		fmt.Sprintf(`{"attachment_id":%d}`, message.Attachments[1].ID),
	} {
		if result, status := runner.executeAttachmentTool(ctx, conversationID, "attachment_read", json.RawMessage(raw)); status != "error" {
			t.Fatalf("invalid read succeeded: %s -> %#v", raw, result)
		}
	}
	if result, status := runner.executeAttachmentTool(ctx, other.ID, "attachment_read", json.RawMessage(fmt.Sprintf(`{"attachment_id":%d}`, id))); status != "error" {
		t.Fatalf("cross-conversation read succeeded: %#v", result)
	}
	var reconstructed strings.Builder
	offset := int64(0)
	for {
		result, status := runner.executeAttachmentTool(ctx, conversationID, "attachment_read", json.RawMessage(fmt.Sprintf(`{"attachment_id":%d,"offset":%d,"limit":8192}`, id, offset)))
		if status != "complete" || len(result.Output) > runner.ToolFeedbackLimit {
			t.Fatalf("read failed or exceeded feedback budget: %#v", result)
		}
		var page struct {
			Content    string `json:"content"`
			NextOffset int64  `json:"next_offset"`
			EOF        bool   `json:"eof"`
		}
		if err := json.Unmarshal([]byte(result.Output), &page); err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(page.Content) || len(page.Content) > maxAttachmentReadBytes || !page.EOF && page.NextOffset <= offset {
			t.Fatalf("invalid UTF-8 page: %#v", page)
		}
		reconstructed.WriteString(page.Content)
		offset = page.NextOffset
		if page.EOF {
			break
		}
	}
	if reconstructed.String() != content {
		t.Fatal("paging changed original text bytes")
	}
	result, status := runner.executeAttachmentTool(ctx, conversationID, "attachment_read", json.RawMessage(fmt.Sprintf(`{"attachment_id":%d,"offset":%d}`, id, len(content))))
	if status != "complete" || !strings.Contains(result.Output, `"eof":true`) || !strings.Contains(result.Output, `"content":""`) {
		t.Fatalf("EOF read incorrect: %#v", result)
	}
}

func TestAttachmentToolsSurviveCompactionAndDeletedTurns(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	message, err := database.AddMessageWithAttachments(ctx, conversationID, "user", "", "complete", []store.NewAttachment{{FileName: "pasted.txt", MediaType: "text/plain", Data: []byte("original text")}})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := database.AddMessage(ctx, conversationID, "assistant", "done", "complete")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpsertCheckpoint(ctx, store.UpsertCheckpointParams{ConversationID: conversationID, ThroughMessageID: assistant.ID, Summary: "File examined"}); err != nil {
		t.Fatal(err)
	}
	runner := Runner{Attachments: database}
	result, status := runner.executeAttachmentTool(ctx, conversationID, "attachment_list", json.RawMessage(`{}`))
	if status != "complete" || !strings.Contains(result.Output, "pasted.txt") {
		t.Fatal("compaction made attachment undiscoverable")
	}
	if _, err := database.DeleteMessagesFrom(ctx, conversationID, message.ID); err != nil {
		t.Fatal(err)
	}
	result, status = runner.executeAttachmentTool(ctx, conversationID, "attachment_read", json.RawMessage(fmt.Sprintf(`{"attachment_id":%d}`, message.Attachments[0].ID)))
	if status != "error" {
		t.Fatalf("deleted attachment survived: %#v", result)
	}
}

func TestRunnerAdvertisesAndExecutesTextAttachmentTools(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	message, err := database.AddMessageWithAttachments(ctx, conversationID, "user", "Inspect this", "complete", []store.NewAttachment{{FileName: "pasted.txt", MediaType: "text/plain", Data: []byte("text file contents")}})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := database.AddMessage(ctx, conversationID, "assistant", "", "streaming")
	if err != nil {
		t.Fatal(err)
	}
	call := ollama.ToolCall{ID: "read", Function: ollama.ToolCallFunction{Name: "attachment_read", Arguments: json.RawMessage(fmt.Sprintf(`{"attachment_id":%d,"limit":4}`, message.Attachments[0].ID))}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{call}}, Done: true}},
		{{Message: ollama.Message{Content: "read successfully"}, Done: true}},
	}}
	runner := Runner{Chat: chat, Steps: database, Attachments: database, MaxTurns: 2}
	result, err := runner.Run(ctx, RunInput{ConversationID: conversationID, AssistantMessageID: assistant.ID, Messages: []ollama.Message{ModelMessage(message, false)}}, func(Event) error { return nil })
	if err != nil || result.Content != "read successfully" || len(chat.requests) != 2 {
		t.Fatalf("read run failed: %#v, %v", result, err)
	}
	advertised := map[string]bool{}
	for _, tool := range chat.requests[0].Tools {
		advertised[tool.Function.Name] = true
	}
	if !advertised["attachment_list"] || !advertised["attachment_read"] {
		t.Fatalf("attachment tools absent: %#v", advertised)
	}
	toolMessage := chat.requests[1].Messages[len(chat.requests[1].Messages)-1]
	var feedback struct {
		Output    string `json:"output"`
		Truncated bool   `json:"feedback_truncated"`
	}
	if err := json.Unmarshal([]byte(toolMessage.Content), &feedback); err != nil {
		t.Fatal(err)
	}
	if toolMessage.ToolCallID != "read" || feedback.Truncated || !strings.Contains(feedback.Output, `"content":"text"`) || !strings.Contains(feedback.Output, `"next_offset":4`) {
		t.Fatalf("pagination metadata or content lost in runner: %#v", toolMessage)
	}
	history, err := database.Messages(ctx, conversationID)
	if err != nil || len(history[1].AgentSteps) != 1 || history[1].AgentSteps[0].Status != "complete" {
		t.Fatalf("read tool step not persisted: %#v, %v", history, err)
	}
}
