package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func TestChatReadsTextAttachmentsOnDemand(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	file, err := database.AddMessageWithAttachments(ctx, conversationID, "user", "Find the answer", "complete", []store.NewAttachment{{
		FileName: "pasted.txt", MediaType: "text/plain", Data: []byte("first secret answer last"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	list := ollama.ToolCall{ID: "list", Function: ollama.ToolCallFunction{Name: "attachment_list", Arguments: json.RawMessage(`{}`)}}
	read := ollama.ToolCall{ID: "read", Function: ollama.ToolCallFunction{Name: "attachment_read", Arguments: json.RawMessage(fmt.Sprintf(`{"attachment_id":%d,"offset":6,"limit":6}`, file.Attachments[0].ID))}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{Content: "Checking the file.", ToolCalls: []ollama.ToolCall{list}}, Done: true, EvalCount: 2}},
		{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{read}}, Done: true, EvalCount: 3}},
		{{Message: ollama.Message{Content: "The answer is secret."}, Done: true, EvalCount: 4}},
	}}
	steps := &memorySteps{}
	runner := Runner{Chat: chat, Steps: steps, Attachments: database}
	result, err := runner.Run(ctx, RunInput{
		Model: "text", Mode: "chat", ConversationID: conversationID, AssistantMessageID: 10,
		AttachmentTools: true, Messages: []ollama.Message{ModelMessage(file, false)},
	}, func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.requests) != 3 || len(steps.steps) != 2 {
		t.Fatalf("attachment loop did not finish: requests=%d steps=%#v", len(chat.requests), steps.steps)
	}
	for _, request := range chat.requests {
		if len(request.Tools) != 2 || request.Tools[0].Function.Name != "attachment_list" || request.Tools[1].Function.Name != "attachment_read" {
			t.Fatalf("Chat offered tools beyond attachment reads: %#v", request.Tools)
		}
	}
	if strings.Contains(chat.requests[0].Messages[0].Content, "secret") {
		t.Fatal("initial request eagerly included file contents")
	}
	feedback := chat.requests[2].Messages[len(chat.requests[2].Messages)-1]
	if feedback.ToolName != "attachment_read" || feedback.ToolCallID != "read" || !strings.Contains(feedback.Content, `\"content\":\"secret\"`) || strings.Contains(feedback.Content, "first secret answer") {
		t.Fatalf("read did not return only the selected bytes: %#v", feedback)
	}
	start, ok := result.Metadata["final_answer_start"].(int)
	if !ok || result.Content[start:] != "The answer is secret." {
		t.Fatalf("final answer includes file-read narration: %#v", result)
	}
	if result.Metadata["run_mode"] != "chat" || result.Metadata["agent_turns"] != 3 || result.Metadata["agent_eval_count"] != 9 {
		t.Fatalf("multi-turn Chat lost mode or aggregate performance: %#v", result.Metadata)
	}
}

func TestChatAttachmentToolsRejectOtherRequestedTools(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	names := []string{"shell", "web_search", "context_write", "context_read", "context_list", "delegate_task", "label_image_directory", "unknown"}
	calls := make([]ollama.ToolCall, 0, len(names))
	for _, name := range names {
		calls = append(calls, ollama.ToolCall{ID: name, Function: ollama.ToolCallFunction{Name: name, Arguments: json.RawMessage(`{"command":"touch unwanted"}`)}})
	}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{ToolCalls: calls}, Done: true}},
		{{Message: ollama.Message{Content: "I can only read attachments."}, Done: true}},
	}}
	steps := &memorySteps{}
	shell := &recordingShell{}
	_, err := (Runner{Chat: chat, Steps: steps, Shell: shell, Attachments: database, MaxTurns: 2}).Run(ctx, RunInput{
		Mode: "chat", ConversationID: conversationID, AttachmentTools: true,
	}, func(Event) error { return nil })
	if err != nil || len(chat.requests) != 2 {
		t.Fatalf("Chat did not recover from unadvertised calls: requests=%d err=%v", len(chat.requests), err)
	}
	if len(shell.commands) != 0 || len(steps.steps) != 0 {
		t.Fatalf("Chat executed a forbidden tool: commands=%v steps=%#v", shell.commands, steps.steps)
	}
	for index, feedback := range chat.requests[1].Messages[1:] {
		if feedback.Role != "tool" || feedback.ToolName != names[index] || feedback.ToolCallID != names[index] || !strings.Contains(feedback.Content, "Only attachment_list and attachment_read") {
			t.Fatalf("missing rejection feedback: %#v", feedback)
		}
	}
}

func TestChatAttachmentToolsRecoverInvalidArgumentsAndRespectTurnLimit(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	for _, alwaysFail := range []bool{false, true} {
		chat := &recoveryChat{fail: ollama.ErrInvalidToolArguments, alwaysFail: alwaysFail}
		result, err := (Runner{Chat: chat, Steps: &memorySteps{}, Attachments: database, MaxTurns: 3}).Run(ctx, RunInput{
			Mode: "chat", ConversationID: conversationID, AttachmentTools: true,
		}, func(Event) error { return nil })
		if len(chat.requests) != 3 || (err != nil) != alwaysFail {
			t.Fatalf("unexpected recovery: alwaysFail=%v requests=%d err=%v", alwaysFail, len(chat.requests), err)
		}
		if alwaysFail && !errors.Is(err, ollama.ErrInvalidToolArguments) {
			t.Fatalf("lost original parse error: %v", err)
		}
		if !alwaysFail && !strings.HasSuffix(result.Content, "Recovered.") {
			t.Fatalf("recovery lost final answer: %#v", result)
		}
	}
}
