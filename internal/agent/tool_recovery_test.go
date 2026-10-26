package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ollama-webui/internal/ollama"
)

type recoveryChat struct {
	requests   []ollama.ChatRequest
	fail       error
	alwaysFail bool
}

func (chat *recoveryChat) Chat(_ context.Context, request ollama.ChatRequest, emit func(ollama.ChatChunk) error) error {
	chat.requests = append(chat.requests, request)
	call := ollama.ToolCall{ID: "call", Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"pwd"}`)}}
	switch len(chat.requests) {
	case 1:
		return emit(ollama.ChatChunk{Message: ollama.Message{ToolCalls: []ollama.ToolCall{call}}, Done: true})
	case 2:
		if err := emit(ollama.ChatChunk{Message: ollama.Message{Thinking: "Working", ToolCalls: []ollama.ToolCall{call}}}); err != nil {
			return err
		}
		return chat.fail
	default:
		if chat.alwaysFail {
			return chat.fail
		}
		return emit(ollama.ChatChunk{Message: ollama.Message{Content: "Recovered."}, Done: true})
	}
}

func TestToolCallGenerationRecovery(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failure    error
		alwaysFail bool
		maxTurns   int
		wantCalls  int
		wantError  bool
	}{
		{"recover", ollama.ErrInvalidToolArguments, false, 10, 3, false},
		{"bounded", ollama.ErrInvalidToolArguments, true, 10, 4, true},
		{"turn limit", ollama.ErrInvalidToolArguments, true, 2, 2, true},
		{"unrelated error", errors.New("connection lost"), false, 10, 2, true},
		{"cancelled", context.Canceled, false, 10, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chat := &recoveryChat{fail: tc.failure, alwaysFail: tc.alwaysFail}
			shell := &recordingShell{result: ShellResult{Output: "/workspace"}}
			runner := Runner{Chat: chat, Steps: &memorySteps{}, Shell: shell, MaxTurns: tc.maxTurns}
			result, err := runner.Run(context.Background(), RunInput{Messages: []ollama.Message{{Role: "user", Content: "inspect"}}}, func(Event) error { return nil })
			if (err != nil) != tc.wantError || len(chat.requests) != tc.wantCalls {
				t.Fatalf("err=%v requests=%d", err, len(chat.requests))
			}
			if len(shell.commands) != 1 {
				t.Fatalf("executed commands again: %v", shell.commands)
			}
			if tc.wantError && !errors.Is(err, tc.failure) {
				t.Fatalf("lost original error: %v", err)
			}
			if !tc.wantError {
				request := chat.requests[2]
				if len(request.Messages) != 4 || request.Messages[2].Role != "tool" || !strings.Contains(request.Messages[2].Content, "/workspace") || !strings.Contains(request.Messages[3].Content, "No tools") {
					t.Fatalf("bad recovery context: %+v", request.Messages)
				}
				if !strings.Contains(result.Content, "Retrying") || !strings.HasSuffix(result.Content, "Recovered.") || result.Thinking != "Working" {
					t.Fatalf("lost visible output: %+v", result)
				}
				start, ok := result.Metadata["final_answer_start"].(int)
				if !ok || result.Content[start:] != "Recovered." {
					t.Fatalf("bad final answer boundary: %+v", result.Metadata)
				}
			}
		})
	}
}
