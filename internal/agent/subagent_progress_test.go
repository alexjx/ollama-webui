package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func TestSubagentProgressShowsActualBoundedToolsAndPersistsOnReload(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	file, err := database.AddMessageWithAttachments(ctx, conversationID, "user", "", "complete", []store.NewAttachment{{FileName: "原始日志.txt", MediaType: "text/plain", Data: []byte("the relevant bytes")}})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := database.AddMessage(ctx, conversationID, "assistant", "", "streaming")
	if err != nil {
		t.Fatal(err)
	}
	read := ollama.ToolCall{ID: "read", Function: ollama.ToolCallFunction{Name: "attachment_read", Arguments: json.RawMessage(fmt.Sprintf(`{"attachment_id":%d,"offset":4,"limit":8}`, file.Attachments[0].ID))}}
	command := "inspect " + strings.Repeat("世界", 200)
	commandJSON, _ := json.Marshal(map[string]string{"command": command})
	shell := ollama.ToolCall{ID: "shell", Function: ollama.ToolCallFunction{Name: "shell", Arguments: commandJSON}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{Thinking: "PRIVATE_REASONING", ToolCalls: []ollama.ToolCall{read, shell}}, Done: true}},
		{{Message: ollama.Message{Content: "Short final findings."}, Done: true}},
	}}
	orchestrator, err := NewSubagentOrchestrator(database, chat, &recordingShell{result: ShellResult{Output: strings.Repeat("输出", 1000), TimedOut: true}}, nil, t.TempDir(), 2048, 2, 1024, 8192)
	if err != nil {
		t.Fatal(err)
	}
	var sawRunningRead, sawRunningShell bool
	result, err := orchestrator.Execute(ctx, SubagentRequest{ConversationID: conversationID, RequestingMessageID: assistant.ID, Model: "parent", Task: "Read the file and inspect"}, func(event Event) error {
		if strings.Contains(event.Content, "PRIVATE_REASONING") {
			t.Error("thinking text leaked into UI progress")
		}
		if event.Type == "subagent.progress" && event.Run.Progress != nil {
			for _, activity := range event.Run.Progress.Activities {
				if activity.Status == "running" && activity.Title == "Read 原始日志.txt" {
					sawRunningRead = true
				}
				if activity.Status == "running" && activity.Title == "Run command" {
					sawRunningShell = true
				}
			}
			var payload store.AgentRunProgress
			if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
				t.Error(err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawRunningRead || !sawRunningShell {
		t.Fatal("actual running tool activities were never emitted")
	}
	runs, err := database.ListAgentRunsByRequestingMessage(ctx, assistant.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("reload failed: %v", err)
	}
	progress := runs[0].Progress
	if progress == nil || progress.Phase != "complete" || len(progress.Activities) != 2 {
		t.Fatalf("tool activity did not survive reload: %#v", progress)
	}
	readActivity, shellActivity := progress.Activities[0], progress.Activities[1]
	if readActivity.Title != "Read 原始日志.txt" || !strings.Contains(readActivity.Detail, "offset 4") || !strings.Contains(readActivity.Detail, "8 bytes") || readActivity.Output != "relevant" || readActivity.Status != "complete" {
		t.Fatalf("attachment activity is not meaningful: %#v", readActivity)
	}
	if shellActivity.Title != "Run command" || shellActivity.Status != "failed" || len(shellActivity.Detail) > 500 || len(shellActivity.Output) > 2000 || !utf8.ValidString(shellActivity.Output) || shellActivity.StartedAt.IsZero() || shellActivity.CompletedAt == nil {
		t.Fatalf("command activity missing bounds/status/timing: %#v", shellActivity)
	}
	if strings.Contains(result.Feedback, "输出") || strings.Contains(result.Feedback, "原始日志") || !strings.Contains(result.Feedback, "Short final findings.") {
		t.Fatalf("UI detail polluted parent feedback: %s", result.Feedback)
	}
	messages, err := database.Messages(ctx, conversationID)
	if err != nil || messages[len(messages)-1].SubagentRuns[0].Progress == nil {
		t.Fatalf("message reload lost progress: %v", err)
	}
}

func TestSubagentProgressRetainsOnlyRecentTools(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	assistant, err := database.AddMessage(ctx, conversationID, "assistant", "", "streaming")
	if err != nil {
		t.Fatal(err)
	}
	calls := make([]ollama.ToolCall, 20)
	for i := range calls {
		calls[i] = ollama.ToolCall{ID: fmt.Sprint(i), Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"inspect"}`)}}
	}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{{{Message: ollama.Message{ToolCalls: calls}, Done: true}}, {{Message: ollama.Message{Content: "done"}, Done: true}}}}
	orchestrator, err := NewSubagentOrchestrator(database, chat, &recordingShell{result: ShellResult{Output: "observed output"}}, nil, t.TempDir(), 2048, 2, 1024, 8192)
	if err != nil {
		t.Fatal(err)
	}
	_, err = orchestrator.Execute(ctx, SubagentRequest{ConversationID: conversationID, RequestingMessageID: assistant.ID, Model: "parent", Task: "focused"}, func(event Event) error {
		if event.Run.Progress != nil && len(event.Run.Progress.Activities) > store.AgentRunActivityLimit {
			t.Error("unbounded progress emitted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	runs, err := database.ListAgentRunsByRequestingMessage(ctx, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	activities := runs[0].Progress.Activities
	if len(activities) != 12 || activities[0].ID != 9 || activities[11].ID != 20 {
		t.Fatalf("wrong retained tool window: %#v", activities)
	}
}

func TestRunnerDoesNotSendChildUIActivityToParentModel(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	assistant, err := database.AddMessage(ctx, conversationID, "assistant", "", "streaming")
	if err != nil {
		t.Fatal(err)
	}
	delegate := ollama.ToolCall{ID: "delegate", Function: ollama.ToolCallFunction{Name: "delegate_task", Arguments: json.RawMessage(`{"task":"Inspect the target and return a brief finding."}`)}}
	shell := ollama.ToolCall{ID: "child-shell", Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"UI_ONLY_COMMAND"}`)}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{delegate}}, Done: true}},
		{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{shell}}, Done: true}},
		{{Message: ollama.Message{Content: "Child final finding."}, Done: true}},
		{{Message: ollama.Message{Content: "Parent final answer."}, Done: true}},
	}}
	orchestrator, err := NewSubagentOrchestrator(database, chat, &recordingShell{result: ShellResult{Output: "UI_ONLY_RAW_OUTPUT"}}, nil, t.TempDir(), 2048, 3, 1024, 8192)
	if err != nil {
		t.Fatal(err)
	}
	runner := Runner{Chat: chat, Steps: database, Subagents: orchestrator, MaxTurns: 3}
	_, err = runner.Run(ctx, RunInput{ConversationID: conversationID, AssistantMessageID: assistant.ID, Model: "parent", Mode: "agent", Messages: []ollama.Message{{Role: "user", Content: "PARENT_HISTORY_MARKER"}}}, func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(chat.requests[3].Messages)
	if strings.Contains(string(encoded), "UI_ONLY_") || !strings.Contains(string(encoded), "Child final finding.") {
		t.Fatalf("UI trace reached parent model: %s", encoded)
	}
	childInput, _ := json.Marshal(chat.requests[1].Messages)
	if strings.Contains(string(childInput), "PARENT_HISTORY_MARKER") {
		t.Fatal("child inherited parent transcript")
	}
}

func TestChildSearchProgressUsesReadableQueryAndSources(t *testing.T) {
	step := &store.AgentStep{ToolName: "web_search", Input: `{"query":"local model context"}`, Output: `{"results":[{"title":"Source title","url":"https://example.com/source","content":"Readable snippet"}]}`}
	title, detail := childToolDescription(step, func(int64) string { return "" })
	if title != "Search: local model context" || detail != "local model context" {
		t.Fatalf("unhelpful search title: %q / %q", title, detail)
	}
	output := childToolOutput(step)
	if !strings.Contains(output, "Source title\nhttps://example.com/source\nReadable snippet") || strings.Contains(output, `"results"`) {
		t.Fatalf("search progress exposed unreadable JSON: %s", output)
	}
}
