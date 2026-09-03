package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

type scriptedChat struct {
	turns    [][]ollama.ChatChunk
	requests []ollama.ChatRequest
}

type errorChat struct{ err error }

func (chat errorChat) Chat(context.Context, ollama.ChatRequest, func(ollama.ChatChunk) error) error {
	return chat.err
}

func (chat *scriptedChat) Chat(ctx context.Context, request ollama.ChatRequest, onChunk func(ollama.ChatChunk) error) error {
	chat.requests = append(chat.requests, request)
	turn := len(chat.requests) - 1
	if turn >= len(chat.turns) {
		return errors.New("unexpected chat turn")
	}
	for _, chunk := range chat.turns[turn] {
		if err := onChunk(chunk); err != nil {
			return err
		}
	}
	return nil
}

type memorySteps struct {
	steps []store.AgentStep
}

func (steps *memorySteps) BeginAgentStep(_ context.Context, messageID int64, turn int, name, input string) (store.AgentStep, error) {
	step := store.AgentStep{ID: int64(len(steps.steps) + 1), MessageID: messageID, Turn: turn, ToolName: name, Input: input, Status: "running", StartedAt: time.Now()}
	steps.steps = append(steps.steps, step)
	return step, nil
}

func (steps *memorySteps) CompleteAgentStep(_ context.Context, id int64, output string, exitCode int, status string) (store.AgentStep, error) {
	for index := range steps.steps {
		if steps.steps[index].ID == id {
			steps.steps[index].Output = output
			steps.steps[index].ExitCode = &exitCode
			steps.steps[index].Status = status
			return steps.steps[index], nil
		}
	}
	return store.AgentStep{}, errors.New("step not found")
}

type recordingShell struct {
	commands []string
	result   ShellResult
}

func (shell *recordingShell) Run(_ context.Context, command string, _ time.Duration) ShellResult {
	shell.commands = append(shell.commands, command)
	return shell.result
}

func TestRunnerExecutesToolAndContinuesUntilFinalAnswer(t *testing.T) {
	call := ollama.ToolCall{ID: "call_123", Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"pwd"}`)}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{Thinking: "Need to inspect.", Content: "Checking.", ToolCalls: []ollama.ToolCall{call}}}, {Done: true, DoneReason: "stop", PromptEvalCount: 10, PromptEvalDuration: int64(time.Second), EvalCount: 5, EvalDuration: int64(500 * time.Millisecond), TotalDuration: int64(2 * time.Second), LoadDuration: int64(200 * time.Millisecond)}},
		{{Message: ollama.Message{Thinking: "The result is clear.", Content: "The workspace is ready."}}, {Done: true, DoneReason: "stop", PromptEvalCount: 20, PromptEvalDuration: int64(2 * time.Second), EvalCount: 10, EvalDuration: int64(time.Second), TotalDuration: int64(4 * time.Second), LoadDuration: int64(100 * time.Millisecond)}},
	}}
	steps := &memorySteps{}
	shell := &recordingShell{result: ShellResult{Output: "/workspace\n", ExitCode: 0}}
	events := make([]Event, 0)
	runner := Runner{Chat: chat, Steps: steps, Shell: shell, MaxTurns: 200}
	result, err := runner.Run(context.Background(), RunInput{
		Model: "tools", Messages: []ollama.Message{{Role: "user", Content: "inspect"}}, AssistantMessageID: 9,
	}, func(event Event) error { events = append(events, event); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "Checking.\n\nThe workspace is ready." {
		t.Fatalf("unexpected visible content: %q", result.Content)
	}
	if result.Thinking != "Need to inspect.\n\nThe result is clear." {
		t.Fatalf("unexpected thinking content: %q", result.Thinking)
	}
	if len(shell.commands) != 1 || shell.commands[0] != "pwd" {
		t.Fatalf("unexpected commands: %#v", shell.commands)
	}
	if len(chat.requests) != 2 || len(chat.requests[0].Tools) != 1 {
		t.Fatalf("tools were not sent on every agent turn: %#v", chat.requests)
	}
	history := chat.requests[1].Messages
	if len(history) != 3 || len(history[1].ToolCalls) != 1 || history[2].Role != "tool" || history[2].ToolName != "shell" || history[2].ToolCallID != "call_123" {
		t.Fatalf("tool history contract was not preserved: %#v", history)
	}
	if history[1].Thinking != "" {
		t.Fatalf("thinking leaked into the next model turn: %#v", history[1])
	}
	if len(events) != 8 || events[0].Type != "turn.started" || events[1].Type != "thinking.delta" || events[3].Type != "tool.started" || events[4].Type != "tool.done" || events[5].Type != "turn.started" || events[6].Type != "thinking.delta" {
		t.Fatalf("unexpected events: %#v", events)
	}
	if result.Metadata["prompt_eval_count"] != 20 || result.Metadata["eval_count"] != 10 {
		t.Fatalf("final-turn context metrics changed: %#v", result.Metadata)
	}
	if result.Metadata["agent_prompt_eval_count"] != 30 || result.Metadata["agent_eval_count"] != 15 || result.Metadata["agent_total_duration"] != int64(6*time.Second) {
		t.Fatalf("agent-run metrics were not aggregated: %#v", result.Metadata)
	}
}

func TestRunnerAnswersDirectlyWithoutUsingShell(t *testing.T) {
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{{{Message: ollama.Message{Content: "direct answer"}, Done: true}}}}
	shell := &recordingShell{}
	result, err := (Runner{Chat: chat, Steps: &memorySteps{}, Shell: shell, MaxTurns: 200}).Run(
		context.Background(), RunInput{Model: "tools", Messages: []ollama.Message{{Role: "user", Content: "simple question"}}}, func(Event) error { return nil },
	)
	if err != nil || result.Content != "direct answer" {
		t.Fatalf("direct response failed: %#v, %v", result, err)
	}
	if len(shell.commands) != 0 || len(chat.requests) != 1 {
		t.Fatalf("simple response unexpectedly used a tool: commands=%#v requests=%d", shell.commands, len(chat.requests))
	}
}

func TestRunnerChatModeUsesOneTurnWithoutToolsOrStaging(t *testing.T) {
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{{
		{Message: ollama.Message{Content: "plain chat"}},
		{Done: true, EvalCount: 12, EvalDuration: int64(time.Second)},
	}}}
	result, err := (Runner{Chat: chat}).Run(context.Background(), RunInput{
		Model: "chat", Mode: "chat", Messages: []ollama.Message{{Role: "user", Content: "hello"}},
	}, func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "plain chat" || len(chat.requests) != 1 || len(chat.requests[0].Tools) != 0 {
		t.Fatalf("chat mode did not remain a single tool-free call: result=%#v requests=%#v", result, chat.requests)
	}
	if result.Metadata["run_mode"] != "chat" || result.Metadata["eval_count"] != 12 {
		t.Fatalf("chat metadata was not preserved: %#v", result.Metadata)
	}
	if _, exists := result.Metadata["agent_eval_count"]; exists {
		t.Fatalf("chat metadata was incorrectly marked as an agent aggregate: %#v", result.Metadata)
	}
}

func TestRunnerBoundsToolFeedbackButPersistsFullOutput(t *testing.T) {
	call := ollama.ToolCall{ID: "large", Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"large"}`)}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{call}}, Done: true}},
		{{Message: ollama.Message{Content: "done"}, Done: true}},
	}}
	fullOutput := "HEAD-" + strings.Repeat("x", 80) + "-TAIL"
	steps := &memorySteps{}
	runner := Runner{Chat: chat, Steps: steps, Shell: &recordingShell{result: ShellResult{Output: fullOutput}}, MaxTurns: 2, ToolFeedbackLimit: 24}
	if _, err := runner.Run(context.Background(), RunInput{Model: "tools", AssistantMessageID: 1}, func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(steps.steps) != 1 || steps.steps[0].Output != fullOutput {
		t.Fatalf("full activity output was not persisted: %#v", steps.steps)
	}
	var payload struct {
		Output            string `json:"output"`
		OutputBytes       int    `json:"output_bytes"`
		FeedbackTruncated bool   `json:"feedback_truncated"`
	}
	feedbackMessage := chat.requests[1].Messages[len(chat.requests[1].Messages)-1]
	if err := json.Unmarshal([]byte(feedbackMessage.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.FeedbackTruncated || payload.OutputBytes != len(fullOutput) || !strings.Contains(payload.Output, "bytes omitted") || !strings.HasPrefix(payload.Output, "HEAD-") || !strings.HasSuffix(payload.Output, "-TAIL") {
		t.Fatalf("tool feedback was not bounded head-and-tail output: %#v", payload)
	}
}

func TestRunnerCleansStagedInputAfterFailureOrCancellation(t *testing.T) {
	for name, runErr := range map[string]error{"failure": errors.New("chat failed"), "cancellation": context.Canceled} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			runner := Runner{Chat: errorChat{err: runErr}, Steps: &memorySteps{}, Shell: &recordingShell{}, Stager: InputStager{Workspace: workspace, MaxInlineBytes: 4}, MaxTurns: 1}
			_, err := runner.Run(context.Background(), RunInput{Model: "tools", Messages: []ollama.Message{{Role: "user", Content: "oversized"}}}, func(Event) error { return nil })
			if !errors.Is(err, runErr) {
				t.Fatalf("unexpected run error: %v", err)
			}
			entries, readErr := os.ReadDir(workspace)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("ended run left staged input behind: %#v, %v", entries, readErr)
			}
		})
	}
}

func TestRunnerAccumulatesToolCallsAcrossStreamChunks(t *testing.T) {
	first := ollama.ToolCall{ID: "one", Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"one"}`)}}
	second := ollama.ToolCall{ID: "two", Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"two"}`)}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{first}}}, {Message: ollama.Message{ToolCalls: []ollama.ToolCall{second}}}, {Done: true}},
		{{Message: ollama.Message{Content: "done"}}, {Done: true}},
	}}
	shell := &recordingShell{result: ShellResult{ExitCode: 0}}
	_, err := (Runner{Chat: chat, Steps: &memorySteps{}, Shell: shell, MaxTurns: 5}).Run(context.Background(), RunInput{Model: "tools", AssistantMessageID: 1}, func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(shell.commands) != 2 || shell.commands[0] != "one" || shell.commands[1] != "two" {
		t.Fatalf("streamed calls were lost or reordered: %#v", shell.commands)
	}
}

func TestDecodeShellArgumentsStrictValidation(t *testing.T) {
	tests := []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"command":1}`),
		json.RawMessage(`{"command":"ok","extra":true}`),
		json.RawMessage(`[]`),
		json.RawMessage(`{"command":"ok"} {}`),
		json.RawMessage(`{"command":"ok","timeout_seconds":-1}`),
	}
	for _, input := range tests {
		if _, err := decodeShellArguments(input); err == nil {
			t.Errorf("expected strict validation failure for %s", input)
		}
	}
	valid, err := decodeShellArguments(json.RawMessage(`{"command":"printf ok","timeout_seconds":12}`))
	if err != nil || valid.Command != "printf ok" || valid.TimeoutSeconds != 12 {
		t.Fatalf("valid arguments rejected: %#v, %v", valid, err)
	}
}

func TestRunnerStopsAtEmergencyTurnLimit(t *testing.T) {
	call := ollama.ToolCall{Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"true"}`)}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{call}}, Done: true}}}}
	_, err := (Runner{Chat: chat, Steps: &memorySteps{}, Shell: &recordingShell{}, MaxTurns: 1}).Run(context.Background(), RunInput{Model: "tools", AssistantMessageID: 1}, func(Event) error { return nil })
	if !errors.Is(err, ErrTurnLimit) {
		t.Fatalf("expected turn limit, got %v", err)
	}
}
