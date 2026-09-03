package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatOmitsInheritedOptionsAndKeepAlive(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		response.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = response.Write([]byte("{\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},\"done\":false}\n{\"done\":true,\"done_reason\":\"stop\"}\n"))
	}))
	defer server.Close()

	err := NewClient(server.URL).Chat(context.Background(), ChatRequest{
		Model: "test:latest", Messages: []Message{{Role: "user", Content: "hello"}},
	}, func(ChatChunk) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"options", "keep_alive", "think"} {
		if _, exists := captured[forbidden]; exists {
			t.Fatalf("default request unexpectedly contains %q: %#v", forbidden, captured)
		}
	}
}

func TestChatIncludesExplicitThinkingWithoutKeepAlive(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			var captured map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				_ = json.NewDecoder(request.Body).Decode(&captured)
				_, _ = response.Write([]byte("{\"done\":true}\n"))
			}))
			defer server.Close()
			if err := NewClient(server.URL).Chat(context.Background(), ChatRequest{Model: "thinking", Think: &enabled}, func(ChatChunk) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if captured["think"] != enabled {
				t.Fatalf("explicit thinking value was not sent: %#v", captured)
			}
			if _, exists := captured["keep_alive"]; exists {
				t.Fatal("thinking requests must not add keep_alive")
			}
		})
	}
}

func TestListModelsExcludesExplicitEmbeddingOnlyModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"models":[
          {"name":"chat","modified_at":"2026-01-01T00:00:00Z","capabilities":["completion","tools"]},
          {"name":"embedding","modified_at":"2026-01-01T00:00:00Z","capabilities":["embedding"]},
          {"name":"legacy","modified_at":"2026-01-01T00:00:00Z"}
        ]}`))
	}))
	defer server.Close()

	models, err := NewClient(server.URL).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Name != "chat" || models[1].Name != "legacy" {
		t.Fatalf("unexpected chat model list: %#v", models)
	}
}

func TestChatIncludesOnlyExplicitOptions(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_ = json.NewDecoder(request.Body).Decode(&captured)
		_, _ = response.Write([]byte("{\"done\":true}\n"))
	}))
	defer server.Close()

	numCtx, temperature := 32768, 0.0
	err := NewClient(server.URL).Chat(context.Background(), ChatRequest{
		Model: "test", Options: &ChatOptions{NumCtx: &numCtx, Temperature: &temperature},
	}, func(ChatChunk) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	options, ok := captured["options"].(map[string]any)
	if !ok || options["num_ctx"] != float64(32768) || options["temperature"] != float64(0) {
		t.Fatalf("unexpected options: %#v", captured["options"])
	}
	if _, exists := captured["keep_alive"]; exists {
		t.Fatal("keep_alive must remain omitted")
	}
}

func TestChatEncodesImageBytesForOllama(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_ = json.NewDecoder(request.Body).Decode(&captured)
		_, _ = response.Write([]byte("{\"done\":true}\n"))
	}))
	defer server.Close()

	err := NewClient(server.URL).Chat(context.Background(), ChatRequest{
		Model: "vision", Messages: []Message{{Role: "user", Content: "describe", Images: [][]byte{{1, 2, 3}}}},
	}, func(ChatChunk) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	messages := captured["messages"].([]any)
	message := messages[0].(map[string]any)
	images := message["images"].([]any)
	if len(images) != 1 || images[0] != "AQID" {
		t.Fatalf("unexpected encoded images: %#v", images)
	}
	if _, exists := captured["keep_alive"]; exists {
		t.Fatal("image requests must not add keep_alive")
	}
}

func TestChatEncodesAndDecodesNativeToolCalls(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_, _ = response.Write([]byte(`{"message":{"role":"assistant","tool_calls":[{"id":"call_1","function":{"index":0,"name":"shell","arguments":{"command":"pwd"}}}]},"done":false}` + "\n" + `{"done":true}` + "\n"))
	}))
	defer server.Close()

	var calls []ToolCall
	err := NewClient(server.URL).Chat(context.Background(), ChatRequest{
		Model:    "tools",
		Messages: []Message{{Role: "user", Content: "inspect"}},
		Tools: []Tool{{Type: "function", Function: ToolFunction{
			Name: "shell", Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
		}}},
	}, func(chunk ChatChunk) error {
		calls = append(calls, chunk.Message.ToolCalls...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, ok := captured["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tool definition missing: %#v", captured)
	}
	parameters := tools[0].(map[string]any)["function"].(map[string]any)["parameters"]
	if _, ok := parameters.(map[string]any); !ok {
		t.Fatalf("parameters must be an object, got %#v", parameters)
	}
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Function.Name != "shell" || string(calls[0].Function.Arguments) != `{"command":"pwd"}` {
		t.Fatalf("tool call did not decode: %#v", calls)
	}
	if _, exists := captured["keep_alive"]; exists {
		t.Fatal("tool requests must not add keep_alive")
	}
}

func TestChatRejectsEarlyEOF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte("{\"message\":{\"content\":\"partial\"},\"done\":false}\n"))
	}))
	defer server.Close()

	err := NewClient(server.URL).Chat(context.Background(), ChatRequest{Model: "test"}, func(ChatChunk) error { return nil })
	if err == nil {
		t.Fatal("expected incomplete stream error")
	}
}

func TestChatPropagatesCancellation(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		response.WriteHeader(http.StatusOK)
		response.(http.Flusher).Flush()
		<-request.Context().Done()
		close(requestCancelled)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- NewClient(server.URL).Chat(ctx, ChatRequest{Model: "test"}, func(ChatChunk) error { return nil })
	}()
	<-requestStarted
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	<-requestCancelled
}
