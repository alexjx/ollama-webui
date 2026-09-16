package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

type blockingChat struct {
	started chan struct{}
}

func (chat blockingChat) Chat(ctx context.Context, _ ollama.ChatRequest, _ func(ollama.ChatChunk) error) error {
	close(chat.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestSubagentOrchestratorUsesPrivateContextAndPersistsBoundedResult(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(ctx, store.CreateConversationParams{Title: "Delegation", Model: "vision-tools"})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := database.AddMessage(ctx, conversation.ID, "assistant", "", "streaming")
	if err != nil {
		t.Fatal(err)
	}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{{ID: "child-shell", Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"printf child-only"}`)}}}}, Done: true}},
		{{Message: ollama.Message{Content: "这是一个很长的子任务总结" + strings.Repeat("x", 100)}, Done: true}},
	}}
	orchestrator, err := NewSubagentOrchestrator(database, chat, &recordingShell{}, nil,
		t.TempDir(), "", 2048, 2, 1, 48, 1024)
	if err != nil {
		t.Fatal(err)
	}
	events := make([]Event, 0)
	result, err := orchestrator.Execute(ctx, SubagentRequest{
		ConversationID: conversation.ID, RequestingMessageID: assistant.ID,
		Model: "vision-tools", Task: "Inspect only the supplied task context.",
	}, func(event Event) error { events = append(events, event); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || len(result.Feedback) == 0 {
		t.Fatalf("unexpected delegation result: %#v", result)
	}
	if len(chat.requests) != 2 || len(chat.requests[0].Messages) != 2 {
		t.Fatalf("child inherited unexpected parent history: %#v", chat.requests)
	}
	if chat.requests[0].Messages[0].Role != "system" || !strings.Contains(chat.requests[0].Messages[1].Content, "Inspect only") {
		t.Fatalf("child context envelope is wrong: %#v", chat.requests[0].Messages)
	}
	if len(events) != 2 || events[0].Type != "subagent.started" || events[1].Type != "subagent.done" {
		t.Fatalf("unexpected lifecycle events: %#v", events)
	}
	runs, err := database.ListAgentRunsByRequestingMessage(ctx, assistant.ID)
	if err != nil || len(runs) != 1 || runs[0].Status != store.AgentStatusComplete {
		t.Fatalf("run was not persisted: %#v, %v", runs, err)
	}
	if len(runs[0].ResultSummary) > 48 || !strings.Contains(runs[0].ResultSummary, "truncated") {
		t.Fatalf("child result was not bounded: %q", runs[0].ResultSummary)
	}
	messages, err := database.Messages(ctx, conversation.ID)
	if err != nil || len(messages) != 1 || len(messages[0].SubagentRuns) != 1 || messages[0].SubagentRuns[0].ID != runs[0].ID {
		t.Fatalf("message reload omitted subagent run: %#v, %v", messages, err)
	}
	if len(messages[0].AgentSteps) != 0 {
		t.Fatalf("private child tool trace leaked into the parent message: %#v", messages[0].AgentSteps)
	}
}

func TestVerifiedOutputPathsRejectsOutsideAndSymlinkOutputs(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if _, err := verifiedOutputPaths(workspace, []string{outside}); err == nil {
		t.Fatal("expected outside output path to fail")
	}
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "linked.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := verifiedOutputPaths(workspace, []string{"linked.txt"}); err == nil {
		t.Fatal("expected symlink output to fail")
	}
}

func TestSubagentOrchestratorPersistsFailureCancellationAndMissingOutput(t *testing.T) {
	tests := []struct {
		name       string
		chat       func() ChatClient
		outputPath string
		cancel     bool
		wantStatus string
		wantEvent  string
	}{
		{name: "model failure", chat: func() ChatClient { return errorChat{err: errors.New("model unavailable")} }, wantStatus: store.AgentStatusFailed, wantEvent: "subagent.failed"},
		{name: "missing output", chat: func() ChatClient {
			return &scriptedChat{turns: [][]ollama.ChatChunk{{{Message: ollama.Message{Content: "done"}, Done: true}}}}
		}, outputPath: "missing.jsonl", wantStatus: store.AgentStatusFailed, wantEvent: "subagent.failed"},
		{name: "cancellation", chat: func() ChatClient { return blockingChat{started: make(chan struct{})} }, cancel: true, wantStatus: store.AgentStatusCancelled, wantEvent: "subagent.cancelled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			database, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			conversation, err := database.CreateConversation(ctx, store.CreateConversationParams{Title: "Delegation", Model: "tools"})
			if err != nil {
				t.Fatal(err)
			}
			assistant, err := database.AddMessage(ctx, conversation.ID, "assistant", "", "streaming")
			if err != nil {
				t.Fatal(err)
			}
			chat := test.chat()
			orchestrator, err := NewSubagentOrchestrator(database, chat, &recordingShell{}, nil, t.TempDir(), "", 2048, 2, 1, 256, 1024)
			if err != nil {
				t.Fatal(err)
			}
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			events := make([]Event, 0)
			done := make(chan error, 1)
			go func() {
				_, executeErr := orchestrator.Execute(runCtx, SubagentRequest{
					ConversationID: conversation.ID, RequestingMessageID: assistant.ID, Model: "tools",
					Task: "focused", OutputPaths: nonEmptyStrings(test.outputPath),
				}, func(event Event) error { events = append(events, event); return nil })
				done <- executeErr
			}()
			if test.cancel {
				blocking := chat.(blockingChat)
				select {
				case <-blocking.started:
				case <-time.After(time.Second):
					t.Fatal("child model call did not start")
				}
				cancel()
			}
			if err := <-done; err == nil {
				t.Fatal("expected delegated task to fail")
			}
			runs, err := database.ListAgentRunsByRequestingMessage(ctx, assistant.ID)
			if err != nil || len(runs) != 1 || runs[0].Status != test.wantStatus {
				t.Fatalf("unexpected persisted run: %#v, %v", runs, err)
			}
			if len(events) != 2 || events[0].Type != "subagent.started" || events[1].Type != test.wantEvent {
				t.Fatalf("unexpected terminal event flow: %#v", events)
			}
		})
	}
}

func nonEmptyStrings(value string) []string {
	if value == "" {
		return nil
	}
	return []string{value}
}

type fakeSubagentExecutor struct {
	request SubagentRequest
}

func (executor *fakeSubagentExecutor) Execute(_ context.Context, request SubagentRequest, _ func(Event) error) (SubagentResult, error) {
	executor.request = request
	return SubagentResult{Feedback: `{"status":"complete"}`, ExitCode: 0}, nil
}

func TestRunnerDelegateToolPassesOnlyExplicitTaskInputs(t *testing.T) {
	call := ollama.ToolCall{ID: "delegate", Function: ollama.ToolCallFunction{Name: "delegate_task", Arguments: json.RawMessage(`{"task":"focused","image_paths":["/tmp/a.png"],"output_paths":["result.jsonl"]}`)}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{call}}, Done: true}},
		{{Message: ollama.Message{Content: "done"}, Done: true}},
	}}
	executor := &fakeSubagentExecutor{}
	runner := Runner{Chat: chat, Steps: &memorySteps{}, Shell: &recordingShell{}, Subagents: executor, MaxTurns: 2}
	_, err := runner.Run(context.Background(), RunInput{
		ConversationID: 7, AssistantMessageID: 9, Model: "tools", Messages: []ollama.Message{{Role: "user", Content: "parent context"}},
	}, func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if executor.request.ConversationID != 7 || executor.request.RequestingMessageID != 9 || executor.request.Model != "tools" || executor.request.Task != "focused" {
		t.Fatalf("delegation request mismatch: %#v", executor.request)
	}
	if len(chat.requests[0].Tools) != 2 || chat.requests[0].Tools[1].Function.Name != "delegate_task" {
		t.Fatalf("delegate tool was not advertised: %#v", chat.requests[0].Tools)
	}
}
