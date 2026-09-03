package httpapi

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func TestGenerateRunsShellAgentAndPersistsTrace(t *testing.T) {
	var mu sync.Mutex
	chatRequests := make([]map[string]any, 0)
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/tags":
			_, _ = response.Write([]byte(`{"models":[{"name":"agent","modified_at":"2026-01-01T00:00:00Z","capabilities":["completion","tools","vision","thinking"]}]}`))
		case "/api/chat":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			chatRequests = append(chatRequests, payload)
			turn := len(chatRequests)
			mu.Unlock()
			response.Header().Set("Content-Type", "application/x-ndjson")
			if turn == 1 {
				_, _ = response.Write([]byte(`{"message":{"role":"assistant","thinking":"I need the shell.","content":"I will verify it.","tool_calls":[{"id":"call_test","function":{"index":0,"name":"shell","arguments":{"command":"printf AGENT_OK"}}}]},"done":false}` + "\n" + `{"done":true,"done_reason":"stop"}` + "\n"))
			} else {
				_, _ = response.Write([]byte(`{"message":{"role":"assistant","thinking":"The marker is present.","content":"Verified AGENT_OK."},"done":false}` + "\n" + `{"done":true,"done_reason":"stop"}` + "\n"))
			}
		default:
			http.NotFound(response, request)
		}
	}))
	defer ollamaServer.Close()

	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	thinkingMode := "high"
	conversation, err := database.CreateConversation(context.Background(), store.CreateConversationParams{
		Title: "Agent", Model: "agent", ThinkingMode: &thinkingMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := ollama.NewClient(ollamaServer.URL)
	workspace := t.TempDir()
	runner := agent.Runner{
		Chat: client, Steps: database, MaxTurns: 200,
		Shell:  agent.ShellExecutor{Workspace: workspace, Timeout: 2 * time.Second, MaxOutput: 4096},
		Stager: agent.InputStager{Workspace: workspace, MaxInlineBytes: 8},
	}
	api := New(database, client, runner, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	requestBody := `{"content":"  verify long input  ","images":[{"name":"pixel.png","media_type":"image/png","data":"` + base64.StdEncoding.EncodeToString(onePixelPNG) + `"}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/conversations/1/messages", strings.NewReader(requestBody))
	request.SetPathValue("id", "1")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}

	eventTypes := make([]string, 0)
	scanner := bufio.NewScanner(strings.NewReader(response.Body.String()))
	for scanner.Scan() {
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		eventTypes = append(eventTypes, event.Type)
	}
	joined := strings.Join(eventTypes, ",")
	if joined != "started,turn.started,thinking.delta,delta,tool.started,tool.done,turn.started,thinking.delta,delta,done" {
		t.Fatalf("unexpected event flow: %s\n%s", joined, response.Body.String())
	}
	messages, err := database.Messages(context.Background(), conversation.ID)
	if err != nil || len(messages) != 2 || messages[1].Status != "complete" || len(messages[1].AgentSteps) != 1 {
		t.Fatalf("agent result not persisted: %#v, %v", messages, err)
	}
	step := messages[1].AgentSteps[0]
	if step.Output != "AGENT_OK" || step.ExitCode == nil || *step.ExitCode != 0 {
		t.Fatalf("unexpected persisted step: %#v", step)
	}
	if messages[1].Content != "I will verify it.\n\nVerified AGENT_OK." {
		t.Fatalf("unexpected assistant content: %q", messages[1].Content)
	}
	if messages[1].Thinking != "I need the shell.\n\nThe marker is present." {
		t.Fatalf("unexpected persisted thinking: %q", messages[1].Thinking)
	}
	if messages[0].Content != "  verify long input  " {
		t.Fatalf("stored user input lost whitespace: %q", messages[0].Content)
	}
	if len(chatRequests) != 2 || chatRequests[0]["tools"] == nil || chatRequests[0]["think"] != "high" || chatRequests[0]["keep_alive"] != nil {
		t.Fatalf("agent request lost tools or added keep_alive: %#v", chatRequests)
	}
	firstMessages := chatRequests[0]["messages"].([]any)
	userInput := firstMessages[len(firstMessages)-1].(map[string]any)
	if content, _ := userInput["content"].(string); !strings.Contains(content, "staged to") || strings.Contains(content, "verify long input") {
		t.Fatalf("large input was not staged for the model: %#v", userInput)
	}
	images, ok := userInput["images"].([]any)
	if !ok || len(images) != 1 || images[0] != base64.StdEncoding.EncodeToString(onePixelPNG) {
		t.Fatalf("agent request lost image input: %#v", userInput)
	}
	secondMessages := chatRequests[1]["messages"].([]any)
	toolResult := secondMessages[len(secondMessages)-1].(map[string]any)
	if toolResult["role"] != "tool" || toolResult["tool_name"] != "shell" || toolResult["tool_call_id"] != "call_test" {
		t.Fatalf("tool result contract mismatch: %#v", toolResult)
	}
	entries, err := os.ReadDir(workspace)
	if err != nil || len(entries) != 0 {
		t.Fatalf("agent run left staged input behind: %#v, %v", entries, err)
	}
}

func TestGenerateChatModeSupportsNonToolModelWithoutAgentPrompt(t *testing.T) {
	var captured map[string]any
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/tags":
			_, _ = response.Write([]byte(`{"models":[{"name":"chat","modified_at":"2026-01-01T00:00:00Z","capabilities":["completion"]}]}`))
		case "/api/chat":
			if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
				t.Fatal(err)
			}
			_, _ = response.Write([]byte("{\"message\":{\"role\":\"assistant\",\"content\":\"chat reply\"},\"done\":false}\n{\"done\":true,\"eval_count\":2,\"eval_duration\":1000000000}\n"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer ollamaServer.Close()
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(context.Background(), store.CreateConversationParams{
		Title: "Chat", Model: "chat", Mode: "chat", SystemPrompt: "Keep it concise.",
	})
	if err != nil {
		t.Fatal(err)
	}
	client := ollama.NewClient(ollamaServer.URL)
	api := New(database, client, agent.Runner{Chat: client}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodPost, "/api/conversations/1/messages", strings.NewReader(`{"content":"hello"}`))
	request.SetPathValue("id", "1")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	if _, exists := captured["tools"]; exists {
		t.Fatalf("chat request unexpectedly included tools: %#v", captured)
	}
	if _, exists := captured["keep_alive"]; exists {
		t.Fatalf("chat request unexpectedly overrode model lifetime: %#v", captured)
	}
	messages := captured["messages"].([]any)
	if len(messages) != 2 || messages[0].(map[string]any)["content"] != "Keep it concise." || strings.Contains(messages[0].(map[string]any)["content"].(string), "autonomous") {
		t.Fatalf("chat request used the wrong system prompt: %#v", captured)
	}
	stored, err := database.Messages(context.Background(), conversation.ID)
	if err != nil || len(stored) != 2 || stored[1].Content != "chat reply" || stored[1].Metadata["run_mode"] != "chat" {
		t.Fatalf("chat result was not persisted: %#v, %v", stored, err)
	}
}

func TestGenerateRejectsNonToolAgentModeBeforePersistence(t *testing.T) {
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"models":[{"name":"chat","modified_at":"2026-01-01T00:00:00Z","capabilities":["completion"]}]}`))
	}))
	defer ollamaServer.Close()
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(context.Background(), store.CreateConversationParams{Title: "Agent", Model: "chat", Mode: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	client := ollama.NewClient(ollamaServer.URL)
	api := New(database, client, agent.Runner{}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodPost, "/api/conversations/1/messages", strings.NewReader(`{"content":"hello"}`))
	request.SetPathValue("id", "1")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "tools_unsupported") {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	messages, err := database.Messages(context.Background(), conversation.ID)
	if err != nil || len(messages) != 0 {
		t.Fatalf("unsupported request persisted messages: %#v, %v", messages, err)
	}
}

func TestGenerateOmitsInheritedThinking(t *testing.T) {
	var captured map[string]any
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/tags":
			_, _ = response.Write([]byte(`{"models":[{"name":"agent","modified_at":"2026-01-01T00:00:00Z","capabilities":["completion","tools","thinking"]}]}`))
		case "/api/chat":
			if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
				t.Fatal(err)
			}
			_, _ = response.Write([]byte("{\"message\":{\"role\":\"assistant\",\"content\":\"done\"},\"done\":false}\n{\"done\":true}\n"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer ollamaServer.Close()
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.CreateConversation(context.Background(), store.CreateConversationParams{Title: "Inherited", Model: "agent"}); err != nil {
		t.Fatal(err)
	}
	client := ollama.NewClient(ollamaServer.URL)
	runner := agent.Runner{Chat: client, Steps: database, MaxTurns: 200, Shell: agent.ShellExecutor{Workspace: t.TempDir()}}
	api := New(database, client, runner, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodPost, "/api/conversations/1/messages", strings.NewReader(`{"content":"hello"}`))
	request.SetPathValue("id", "1")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	if _, exists := captured["think"]; exists {
		t.Fatalf("inherited thinking unexpectedly sent an override: %#v", captured)
	}
}

func TestGenerateRejectsUnsupportedThinkingOverrideBeforePersistence(t *testing.T) {
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"models":[{"name":"agent","modified_at":"2026-01-01T00:00:00Z","capabilities":["completion","tools"]}]}`))
	}))
	defer ollamaServer.Close()
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	thinkingMode := "on"
	conversation, err := database.CreateConversation(context.Background(), store.CreateConversationParams{
		Title: "No thinking", Model: "agent", ThinkingMode: &thinkingMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := ollama.NewClient(ollamaServer.URL)
	api := New(database, client, agent.Runner{}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodPost, "/api/conversations/1/messages", strings.NewReader(`{"content":"hello"}`))
	request.SetPathValue("id", "1")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "thinking_unsupported") {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	messages, err := database.Messages(context.Background(), conversation.ID)
	if err != nil || len(messages) != 0 {
		t.Fatalf("unsupported thinking request persisted messages: %#v, %v", messages, err)
	}
}
