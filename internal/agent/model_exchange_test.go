package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ollama-webui/internal/ollama"
)

func TestModelExchangeLatestRound(t *testing.T) {
	call := ollama.ToolCall{ID: "call", Function: ollama.ToolCallFunction{Name: "shell", Arguments: json.RawMessage(`{"command":"pwd"}`)}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{Content: "Checking", ToolCalls: []ollama.ToolCall{call}}, Done: true}},
		{{Message: ollama.Message{Thinking: "Final reasoning", Content: "Final "}}, {Message: ollama.Message{Content: "answer"}, Done: true}},
	}}
	ctxSize := 4096
	think := ollama.ThinkValue("on")
	input := RunInput{Model: "model", Messages: []ollama.Message{{Role: "system", Content: "System prompt"}, {Role: "user", Content: "Check", Images: [][]byte{[]byte("image")}}}, Options: &ollama.ChatOptions{NumCtx: &ctxSize}, Think: &think}
	result, err := (Runner{Chat: chat, Steps: &memorySteps{}, Shell: &recordingShell{result: ShellResult{Output: "/workspace"}}, MaxTurns: 3}).Run(context.Background(), input, func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	exchange := result.Metadata["model_exchange"].(*ModelExchange)
	if exchange.Turn != 2 || exchange.Version != 1 || exchange.Status != "complete" || exchange.Response.Content != "Final answer" || exchange.Response.Thinking != "Final reasoning" {
		t.Fatalf("incorrect exchange: %#v", exchange)
	}
	var request struct {
		Model    string
		Messages []ollama.Message
		Tools    []ollama.Tool
		Options  ollama.ChatOptions
		Think    bool
		Stream   bool
	}
	if err := json.Unmarshal(exchange.Request, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 4 || request.Messages[2].ToolCalls[0].ID != "call" || request.Messages[3].Role != "tool" || !strings.Contains(request.Messages[3].Content, "/workspace") {
		t.Fatalf("missing actual tool context: %#v", request.Messages)
	}
	if len(request.Tools) != 1 || !request.Stream || !request.Think || *request.Options.NumCtx != 4096 {
		t.Fatalf("missing request options: %#v", request)
	}
	before := string(exchange.Request)
	input.Messages[0].Content = "changed"
	input.Messages[1].Images[0][0] = 'X'
	ctxSize = 1
	chat.requests[1].Messages[2].ToolCalls[0].Function.Arguments[0] = 'X'
	if string(exchange.Request) != before {
		t.Fatal("snapshot changed with source data")
	}
	if _, err := json.Marshal(result.Metadata); err != nil {
		t.Fatalf("metadata cannot persist: %v", err)
	}
}

type partialExchangeChat struct{ err error }

func (chat partialExchangeChat) Chat(_ context.Context, _ ollama.ChatRequest, emit func(ollama.ChatChunk) error) error {
	if err := emit(ollama.ChatChunk{Message: ollama.Message{Thinking: "thinking", Content: "partial"}}); err != nil {
		return err
	}
	return chat.err
}

func TestModelExchangeRetainsFailedAndStoppedResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status string
	}{
		{"error", errors.New("connection closed"), "error"},
		{"cancelled", context.Canceled, "stopped"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := (Runner{Chat: partialExchangeChat{err: test.err}}).Run(context.Background(), RunInput{Mode: "chat", Model: "model", Messages: []ollama.Message{{Role: "user", Content: "Hi"}}}, func(Event) error { return nil })
			if !errors.Is(err, test.err) {
				t.Fatalf("got error %v", err)
			}
			exchange := result.Metadata["model_exchange"].(*ModelExchange)
			if exchange.Turn != 1 || exchange.Status != test.status || exchange.Error != test.err.Error() || exchange.Response.Content != "partial" || exchange.Response.Thinking != "thinking" || exchange.Response.Role != "assistant" {
				t.Fatalf("lost partial exchange: %#v", exchange)
			}
		})
	}
}
