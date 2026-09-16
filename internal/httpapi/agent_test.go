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

func TestGenerateStreamsAndPersistsSuccessfulSubagentRun(t *testing.T) {
	var mu sync.Mutex
	chatRequests := make([]map[string]any, 0, 3)
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/tags":
			_, _ = response.Write([]byte(`{"models":[{"name":"agent","modified_at":"2026-01-01T00:00:00Z","capabilities":["completion","tools"]}]}`))
		case "/api/chat":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			messages, ok := payload["messages"].([]any)
			if !ok || len(messages) == 0 {
				t.Fatalf("chat request omitted messages: %#v", payload)
			}
			first, _ := messages[0].(map[string]any)
			firstContent, _ := first["content"].(string)

			mu.Lock()
			chatRequests = append(chatRequests, payload)
			mu.Unlock()
			response.Header().Set("Content-Type", "application/x-ndjson")
			switch {
			case strings.Contains(firstContent, "focused child agent"):
				_, _ = response.Write([]byte(`{"message":{"role":"assistant","content":"Child task completed."},"done":false}` + "\n" + `{"done":true,"done_reason":"stop"}` + "\n"))
			case len(messages) == 2:
				_, _ = response.Write([]byte(`{"message":{"role":"assistant","tool_calls":[{"id":"call_delegate","function":{"index":0,"name":"delegate_task","arguments":{"task":"Inspect the named input and report the result."}}}]},"done":false}` + "\n" + `{"done":true,"done_reason":"stop"}` + "\n"))
			default:
				_, _ = response.Write([]byte(`{"message":{"role":"assistant","content":"Parent received the child result."},"done":false}` + "\n" + `{"done":true,"done_reason":"stop"}` + "\n"))
			}
		default:
			http.NotFound(response, request)
		}
	}))
	defer ollamaServer.Close()

	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(ctx, store.CreateConversationParams{Title: "Delegation", Model: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	client := ollama.NewClient(ollamaServer.URL)
	workspace := t.TempDir()
	shell := agent.ShellExecutor{Workspace: workspace, Timeout: time.Second, MaxOutput: 4096}
	subagents, err := agent.NewSubagentOrchestrator(database, client, shell, nil, workspace, "agent", 4096, 4, 1, 4096, 4096)
	if err != nil {
		t.Fatal(err)
	}
	runner := agent.Runner{
		Chat: client, Steps: database, Shell: shell, Subagents: subagents, MaxTurns: 4, ToolFeedbackLimit: 4096,
	}
	api := New(database, client, runner, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodPost, "/api/conversations/1/messages", strings.NewReader(`{"content":"delegate this task"}`))
	request.SetPathValue("id", "1")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
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
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	wantEvents := "started,turn.started,tool.started,subagent.started,subagent.done,tool.done,turn.started,delta,done"
	if joined := strings.Join(eventTypes, ","); joined != wantEvents {
		t.Fatalf("unexpected subagent event flow: %s\n%s", joined, response.Body.String())
	}

	messages, err := database.Messages(ctx, conversation.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("reload messages: %#v, %v", messages, err)
	}
	if len(messages[0].SubagentRuns) != 0 || len(messages[1].SubagentRuns) != 1 {
		t.Fatalf("subagent run attached to wrong message: %#v", messages)
	}
	run := messages[1].SubagentRuns[0]
	if run.Status != store.AgentStatusComplete || run.Task != "Inspect the named input and report the result." || run.ResultSummary != "Child task completed." {
		t.Fatalf("unexpected persisted subagent run: %#v", run)
	}
	getRequest := httptest.NewRequest(http.MethodGet, "/api/conversations/1", nil)
	getRequest.SetPathValue("id", "1")
	getResponse := httptest.NewRecorder()
	api.ServeHTTP(getResponse, getRequest)
	var reloaded struct {
		Messages []store.Message `json:"messages"`
	}
	if getResponse.Code != http.StatusOK {
		t.Fatalf("conversation API returned %d: %s", getResponse.Code, getResponse.Body.String())
	}
	if err := json.NewDecoder(getResponse.Body).Decode(&reloaded); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Messages) != 2 || len(reloaded.Messages[1].SubagentRuns) != 1 || reloaded.Messages[1].SubagentRuns[0].ID != run.ID {
		t.Fatalf("conversation API omitted persisted subagent run: %#v", reloaded.Messages)
	}

	mu.Lock()
	requests := append([]map[string]any(nil), chatRequests...)
	mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("expected parent, child, parent calls; got %d: %#v", len(requests), requests)
	}
	var childMessages []any
	for _, payload := range requests {
		candidate, _ := payload["messages"].([]any)
		if len(candidate) == 0 {
			continue
		}
		first, _ := candidate[0].(map[string]any)
		if content, _ := first["content"].(string); strings.Contains(content, "focused child agent") {
			childMessages = candidate
			break
		}
	}
	if len(childMessages) != 2 {
		t.Fatalf("child request should contain only system and task messages: %#v", childMessages)
	}
	if childMessages[0].(map[string]any)["role"] != "system" || childMessages[1].(map[string]any)["role"] != "user" {
		t.Fatalf("unexpected child message roles: %#v", childMessages)
	}
	childTask, _ := childMessages[1].(map[string]any)["content"].(string)
	if !strings.Contains(childTask, "Inspect the named input and report the result.") || strings.Contains(childTask, "delegate this task") {
		t.Fatalf("child request inherited parent conversation instead of its task: %#v", childMessages)
	}
}

func TestGenerateCompactsLongAgentHistoryBeforeRunning(t *testing.T) {
	var mu sync.Mutex
	chatRequests := make([]map[string]any, 0)
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/tags":
			_, _ = response.Write([]byte(`{"models":[{"name":"agent","capabilities":["completion","tools"]}]}`))
		case "/api/chat":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			chatRequests = append(chatRequests, payload)
			call := len(chatRequests)
			mu.Unlock()
			if call == 1 {
				_, _ = response.Write([]byte("{\"message\":{\"role\":\"assistant\",\"content\":\"## Goal\\nPreserve the verified decision.\"},\"done\":true}\n"))
			} else {
				_, _ = response.Write([]byte("{\"message\":{\"role\":\"assistant\",\"content\":\"continued\"},\"done\":true}\n"))
			}
		default:
			http.NotFound(response, request)
		}
	}))
	defer ollamaServer.Close()

	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	window := 1000
	conversation, err := database.CreateConversation(ctx, store.CreateConversationParams{
		Title: "Long agent", Model: "agent", ContextWindow: &window,
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, content := range []string{
		strings.Repeat("old-user-", 180), strings.Repeat("old-assistant-", 150),
		"recent user one", "recent answer one", "recent user two", "recent answer two",
	} {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		if _, err := database.AddMessage(ctx, conversation.ID, role, content, "complete"); err != nil {
			t.Fatal(err)
		}
	}
	client := ollama.NewClient(ollamaServer.URL)
	runner := agent.Runner{
		Chat: client, Steps: database, Shell: agent.ShellExecutor{Workspace: t.TempDir(), Timeout: time.Second, MaxOutput: 4096},
		Stager: agent.InputStager{Workspace: t.TempDir(), MaxInlineBytes: 16384}, MaxTurns: 4,
		Context: &agent.ContextPreparer{Chat: client, Store: database},
	}
	api := New(database, client, runner, RuntimeSettings{ContextTokens: 32768}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodPost, "/api/conversations/1/messages", strings.NewReader(`{"content":"current request"}`))
	request.SetPathValue("id", "1")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"type":"done"`) {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	if len(chatRequests) != 2 {
		t.Fatalf("expected summary and agent calls, got %d", len(chatRequests))
	}
	actualMessages := chatRequests[1]["messages"].([]any)
	encoded, _ := json.Marshal(actualMessages)
	if !strings.Contains(string(encoded), "Preserve the verified decision") || strings.Contains(string(encoded), "old-user-") || !strings.Contains(string(encoded), "current request") {
		t.Fatalf("agent received wrong compacted history: %s", encoded)
	}
	checkpoint, err := database.GetCheckpoint(ctx, conversation.ID)
	if err != nil || checkpoint.ThroughMessageID != 2 || !strings.Contains(checkpoint.Summary, "verified decision") {
		t.Fatalf("checkpoint was not persisted: %#v, %v", checkpoint, err)
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
