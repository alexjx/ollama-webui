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
		fmt.Sprintf(`{"attachment_id":%d,"limit":0}`, id),
		fmt.Sprintf(`{"attachment_id":%d,"limit":-1}`, id),
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
		if status != "complete" {
			t.Fatalf("read failed: %#v", result)
		}
		var page struct {
			Content    string `json:"content"`
			NextOffset int64  `json:"next_offset"`
			EOF        bool   `json:"eof"`
		}
		if err := json.Unmarshal([]byte(result.Output), &page); err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(page.Content) || len(page.Content) > 8192 || !page.EOF && page.NextOffset <= offset {
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

func TestAttachmentReadUsesAgentChosenRangeWithoutFeedbackCap(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	content := strings.Repeat("世界\"<&\r\n ", 10000)
	message, err := database.AddMessageWithAttachments(ctx, conversationID, "user", "", "complete", []store.NewAttachment{{FileName: "large.txt", MediaType: "text/plain", Data: []byte(content)}})
	if err != nil {
		t.Fatal(err)
	}
	id := message.Attachments[0].ID
	runner := Runner{Attachments: database, ToolFeedbackLimit: 256}
	for _, test := range []struct {
		name, arguments, want string
		offset                int64
	}{
		{"large range", fmt.Sprintf(`{"attachment_id":%d,"limit":24000}`, id), content[:24000], 0},
		{"all remaining", fmt.Sprintf(`{"attachment_id":%d,"offset":12}`, id), content[12:], 12},
		{"past end limit", fmt.Sprintf(`{"attachment_id":%d,"limit":9223372036854775807}`, id), content, 0},
		{"UTF-8 boundary", fmt.Sprintf(`{"attachment_id":%d,"limit":24001}`, id), content[:24000], 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, status := runner.executeAttachmentTool(ctx, conversationID, "attachment_read", json.RawMessage(test.arguments))
			if status != "complete" {
				t.Fatalf("read failed: %s", result.Output)
			}
			var page struct {
				Content    string `json:"content"`
				Offset     int64  `json:"offset"`
				NextOffset int64  `json:"next_offset"`
				Bytes      int    `json:"bytes"`
				EOF        bool   `json:"eof"`
			}
			if err := json.Unmarshal([]byte(result.Output), &page); err != nil {
				t.Fatal(err)
			}
			if page.Content != test.want || page.Offset != test.offset || page.Bytes != len(test.want) || page.NextOffset != test.offset+int64(len(test.want)) || page.EOF != (page.NextOffset == int64(len(content))) {
				t.Fatalf("wrong range: offset=%d next=%d bytes=%d eof=%v", page.Offset, page.NextOffset, page.Bytes, page.EOF)
			}
		})
	}
}

func TestRunnerReturnsLargeAttachmentReadIntactToModel(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	content := strings.Repeat("世界\"<&\n", 6000)
	message, err := database.AddMessageWithAttachments(ctx, conversationID, "user", "Read this", "complete", []store.NewAttachment{{FileName: "large.txt", MediaType: "text/plain", Data: []byte(content)}})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := database.AddMessage(ctx, conversationID, "assistant", "", "streaming")
	if err != nil {
		t.Fatal(err)
	}
	call := ollama.ToolCall{ID: "large-read", Function: ollama.ToolCallFunction{Name: "attachment_read", Arguments: json.RawMessage(fmt.Sprintf(`{"attachment_id":%d,"limit":%d}`, message.Attachments[0].ID, len(content)))}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{call}}, Done: true}},
		{{Message: ollama.Message{Content: "done"}, Done: true}},
	}}
	numCtx := 131072
	runner := Runner{Chat: chat, Steps: database, Attachments: database, MaxTurns: 2, ToolFeedbackLimit: 8192}
	_, err = runner.Run(ctx, RunInput{ConversationID: conversationID, AssistantMessageID: assistant.ID, Messages: []ollama.Message{ModelMessage(message, false)}, Options: &ollama.ChatOptions{NumCtx: &numCtx}}, func(Event) error { return nil })
	if err != nil || len(chat.requests) != 2 {
		t.Fatalf("run failed: %v", err)
	}
	for _, tool := range chat.requests[0].Tools {
		if tool.Function.Name == "attachment_read" {
			var schema struct {
				Properties map[string]map[string]any `json:"properties"`
			}
			if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil {
				t.Fatal(err)
			}
			if _, exists := schema.Properties["limit"]["maximum"]; exists {
				t.Fatal("attachment tool still advertises a fixed read maximum")
			}
			if !strings.Contains(tool.Function.Description, "131072 tokens") {
				t.Fatal("attachment tool omits the configured context window")
			}
		}
	}
	toolMessage := chat.requests[1].Messages[len(chat.requests[1].Messages)-1]
	var feedback struct {
		Output    string `json:"output"`
		Truncated bool   `json:"feedback_truncated"`
	}
	if err := json.Unmarshal([]byte(toolMessage.Content), &feedback); err != nil {
		t.Fatal(err)
	}
	var page struct {
		Content    string `json:"content"`
		NextOffset int64  `json:"next_offset"`
		EOF        bool   `json:"eof"`
	}
	if err := json.Unmarshal([]byte(feedback.Output), &page); err != nil {
		t.Fatal(err)
	}
	if toolMessage.ToolCallID != call.ID || feedback.Truncated || page.Content != content || page.NextOffset != int64(len(content)) || !page.EOF {
		t.Fatalf("attachment feedback was clipped: truncated=%v bytes=%d next=%d eof=%v", feedback.Truncated, len(page.Content), page.NextOffset, page.EOF)
	}
	history, err := database.Messages(ctx, conversationID)
	if err != nil || len(history) != 2 || len(history[1].AgentSteps) != 1 || history[1].AgentSteps[0].Output != feedback.Output {
		t.Fatalf("saved read differs from model feedback: %v", err)
	}
}

func TestSubagentReturnsLargeAttachmentReadIntactToModel(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	content := strings.Repeat("attached text\n", 3000)
	message, err := database.AddMessageWithAttachments(ctx, conversationID, "user", "Read this", "complete", []store.NewAttachment{{FileName: "large.txt", MediaType: "text/plain", Data: []byte(content)}})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := database.AddMessage(ctx, conversationID, "assistant", "", "streaming")
	if err != nil {
		t.Fatal(err)
	}
	call := ollama.ToolCall{ID: "child-read", Function: ollama.ToolCallFunction{Name: "attachment_read", Arguments: json.RawMessage(fmt.Sprintf(`{"attachment_id":%d}`, message.Attachments[0].ID))}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{call}}, Done: true}},
		{{Message: ollama.Message{Content: "done"}, Done: true}},
	}}
	orchestrator, err := NewSubagentOrchestrator(database, chat, &recordingShell{}, nil, t.TempDir(), "", 32768, 2, 1, 1024, 256)
	if err != nil {
		t.Fatal(err)
	}
	_, err = orchestrator.Execute(ctx, SubagentRequest{ConversationID: conversationID, RequestingMessageID: assistant.ID, Model: "tools", Task: "Read the attached file."}, func(Event) error { return nil })
	if err != nil || len(chat.requests) != 2 {
		t.Fatalf("child run failed: %v", err)
	}
	var feedback struct {
		Output    string `json:"output"`
		Truncated bool   `json:"feedback_truncated"`
	}
	toolMessage := chat.requests[1].Messages[len(chat.requests[1].Messages)-1]
	if err := json.Unmarshal([]byte(toolMessage.Content), &feedback); err != nil {
		t.Fatal(err)
	}
	var page struct {
		Content string `json:"content"`
		EOF     bool   `json:"eof"`
	}
	if err := json.Unmarshal([]byte(feedback.Output), &page); err != nil {
		t.Fatal(err)
	}
	if feedback.Truncated || page.Content != content || !page.EOF {
		t.Fatalf("child attachment feedback clipped: truncated=%v bytes=%d eof=%v", feedback.Truncated, len(page.Content), page.EOF)
	}
	for _, tool := range chat.requests[0].Tools {
		if tool.Function.Name == "attachment_read" && !strings.Contains(tool.Function.Description, "32768 tokens") {
			t.Fatal("child attachment tool omits its context window")
		}
	}
}
