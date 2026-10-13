package agent

import (
	"context"
	"encoding/json"
	"testing"

	"ollama-webui/internal/ollama"
)

func TestFinalAnswerBoundaryExcludesEarlierToolTurnNarration(t *testing.T) {
	call := ollama.ToolCall{Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"pwd"}`)}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{Content: "检查中🙂", ToolCalls: []ollama.ToolCall{call}}}, {Done: true}},
		{{Message: ollama.Message{Content: "Final answer.\n\n"}}, {Message: ollama.Message{Content: "Second paragraph 世界"}}, {Done: true}},
	}}
	runner := Runner{Chat: chat, Steps: &memorySteps{}, Shell: &recordingShell{}, MaxTurns: 3}
	result, err := runner.Run(context.Background(), RunInput{Model: "original-model", Mode: "agent"}, func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	start, ok := result.Metadata["final_answer_start"].(int)
	if !ok || result.Content[start:] != "Final answer.\n\nSecond paragraph 世界" {
		t.Fatalf("final reply boundary incorrect: %q %#v", result.Content, result.Metadata)
	}
	if result.Metadata["response_model"] != "original-model" {
		t.Fatalf("response model missing: %#v", result.Metadata)
	}
}

func TestChatFinalAnswerStartsAtZero(t *testing.T) {
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{{{Message: ollama.Message{Content: "Chat answer"}}, {Done: true}}}}
	result, err := (Runner{Chat: chat}).Run(context.Background(), RunInput{Model: "chat-model", Mode: "chat"}, func(Event) error { return nil })
	if err != nil || result.Metadata["final_answer_start"] != 0 || result.Metadata["response_model"] != "chat-model" {
		t.Fatalf("chat reply boundary incorrect: %#v, %v", result, err)
	}
}
