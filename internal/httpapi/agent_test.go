package httpapi

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
				_, _ = response.Write([]byte(`{"message":{"role":"assistant","thinking":"I need the shell.","content":"I will verify it.","tool_calls":[{"id":"call_test","function":{"index":0,"name":"shell","arguments":{"command":"printf AGENT_OK"}}}]},"done":false}` + "\n" + `{"done":true,"done_reason":"stop","prompt_eval_count":10,"eval_count":5}` + "\n"))
			} else {
				_, _ = response.Write([]byte(`{"message":{"role":"assistant","thinking":"The marker is present.","content":"Verified AGENT_OK."},"done":false}` + "\n" + `{"done":true,"done_reason":"stop","prompt_eval_count":20,"eval_count":10}` + "\n"))
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
		Shell: agent.ShellExecutor{Workspace: workspace, Timeout: 2 * time.Second, MaxOutput: 4096},
	}
	api := New(database, client, runner, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	inlineInput := "  verify long input\n" + strings.Repeat("inline 世界\n", 2000) + "  "
	requestBody, err := json.Marshal(map[string]any{"content": inlineInput, "images": []imageInput{{Name: "pixel.png", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(onePixelPNG)}}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/conversations/1/messages", strings.NewReader(string(requestBody)))
	request.SetPathValue("id", "1")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}

	eventTypes := make([]string, 0)
	turnMetadata := make([]map[string]any, 0)
	timingMetadata := make([]map[string]any, 0)
	scanner := bufio.NewScanner(strings.NewReader(response.Body.String()))
	for scanner.Scan() {
		var event struct {
			Type     string         `json:"type"`
			Metadata map[string]any `json:"metadata"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		eventTypes = append(eventTypes, event.Type)
		if event.Type == "turn.done" {
			turnMetadata = append(turnMetadata, event.Metadata)
		}
		if event.Type == "thinking.timing" {
			timingMetadata = append(timingMetadata, event.Metadata)
		}
	}
	joined := strings.Join(eventTypes, ",")
	if joined != "started,activity.started,turn.started,thinking.timing,thinking.delta,thinking.timing,delta,turn.done,tool.started,tool.done,turn.started,thinking.timing,thinking.delta,thinking.timing,delta,turn.done,done" {
		t.Fatalf("unexpected event flow: %s\n%s", joined, response.Body.String())
	}
	if len(turnMetadata) != 2 || turnMetadata[0]["prompt_eval_count"] != float64(10) || turnMetadata[0]["eval_count"] != float64(5) || turnMetadata[1]["prompt_eval_count"] != float64(20) || turnMetadata[1]["eval_count"] != float64(10) || turnMetadata[1]["agent_prompt_eval_count"] != float64(30) {
		t.Fatalf("per-turn context metadata was not streamed: %#v", turnMetadata)
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
	if messages[1].Metadata["prompt_eval_count"] != float64(20) || messages[1].Metadata["eval_count"] != float64(10) {
		t.Fatalf("latest context metadata was not persisted: %#v", messages[1].Metadata)
	}
	if len(timingMetadata) != 4 || timingMetadata[0]["thinking_active"] != true || timingMetadata[1]["thinking_active"] != false || timingMetadata[2]["thinking_active"] != true || timingMetadata[3]["thinking_active"] != false {
		t.Fatalf("thinking phase timing was not streamed: %#v", timingMetadata)
	}
	if messages[1].Metadata["thinking_duration_ms"] != timingMetadata[3]["thinking_duration_ms"] {
		t.Fatalf("final thinking duration was not persisted: %#v", messages[1].Metadata)
	}
	activity := messages[1].Metadata["activity"].(map[string]any)
	blocks := activity["blocks"].([]any)
	if activity["version"] != float64(1) || len(blocks) != 5 || blocks[2].(map[string]any)["step_id"] != float64(step.ID) {
		t.Fatalf("chronology was not persisted: %#v", activity)
	}
	for i, kind := range []string{"thinking", "content", "tool", "thinking", "content"} {
		if blocks[i].(map[string]any)["type"] != kind {
			t.Fatalf("wrong persisted ordering: %#v", blocks)
		}
	}
	if messages[0].Content != inlineInput {
		t.Fatalf("stored user input lost whitespace: %q", messages[0].Content)
	}
	if len(chatRequests) != 2 || chatRequests[0]["tools"] == nil || chatRequests[0]["think"] != "high" || chatRequests[0]["keep_alive"] != nil {
		t.Fatalf("agent request lost tools or added keep_alive: %#v", chatRequests)
	}
	firstMessages := chatRequests[0]["messages"].([]any)
	userInput := firstMessages[len(firstMessages)-1].(map[string]any)
	if content, _ := userInput["content"].(string); content != inlineInput {
		t.Fatalf("inline input changed for the model: %#v", userInput)
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
		t.Fatalf("agent run unexpectedly wrote input files: %#v, %v", entries, err)
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
	wantEvents := "started,activity.started,turn.started,turn.done,tool.started,subagent.started,subagent.done,tool.done,turn.started,delta,turn.done,done"
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
	activity := reloaded.Messages[1].Metadata["activity"].(map[string]any)
	blocks := activity["blocks"].([]any)
	if activity["version"] != float64(1) || len(blocks) != 3 || blocks[1].(map[string]any)["run_id"] != float64(run.ID) || blocks[1].(map[string]any)["turn"] != float64(1) {
		t.Fatalf("conversation API omitted persisted chronology: %#v", activity)
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
		MaxTurns: 4,
		Context:  &agent.ContextPreparer{Chat: client, Store: database},
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
	if _, exists := stored[1].Metadata["thinking_duration_ms"]; exists {
		t.Fatalf("non-thinking reply acquired a fabricated duration: %#v", stored[1].Metadata)
	}
}

type thinkingResultChat struct{ interruption error }

func (chat thinkingResultChat) Chat(_ context.Context, _ ollama.ChatRequest, emit func(ollama.ChatChunk) error) error {
	if err := emit(ollama.ChatChunk{Message: ollama.Message{Thinking: "Reasoning text"}}); err != nil {
		return err
	}
	if chat.interruption != nil {
		return chat.interruption
	}
	return emit(ollama.ChatChunk{Message: ollama.Message{Content: "Answer"}, Done: true})
}

func TestGeneratePersistsThinkingDurationOnCompleteErrorAndCancellation(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		mode         string
		interruption error
		status       string
	}{
		{"chat complete", "chat", nil, "complete"},
		{"chat error", "chat", errors.New("stream failed"), "error"},
		{"chat cancelled", "chat", context.Canceled, "cancelled"},
		{"agent cancelled", "agent", context.Canceled, "cancelled"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				_, _ = response.Write([]byte(`{"models":[{"name":"thinker","capabilities":["completion","tools","thinking"]}]}`))
			}))
			defer upstream.Close()
			database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			conversation, err := database.CreateConversation(context.Background(), store.CreateConversationParams{
				Title: "Thinking", Model: "thinker", Mode: testCase.mode,
			})
			if err != nil {
				t.Fatal(err)
			}
			client := ollama.NewClient(upstream.URL)
			api := New(database, client, agent.Runner{Chat: thinkingResultChat{testCase.interruption}, MaxTurns: 2}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			defer api.Close()
			request := httptest.NewRequest(http.MethodPost, "/api/conversations/1/messages", strings.NewReader(`{"content":"Question"}`))
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
			}
			messages, err := database.Messages(context.Background(), conversation.ID)
			if err != nil || len(messages) != 2 || messages[1].Status != testCase.status || messages[1].Thinking != "Reasoning text" {
				t.Fatalf("thinking response not persisted: %#v, %v", messages, err)
			}
			var stoppedMetadata map[string]any
			scanner := bufio.NewScanner(strings.NewReader(response.Body.String()))
			for scanner.Scan() {
				var event struct {
					Type     string         `json:"type"`
					Metadata map[string]any `json:"metadata"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
					t.Fatal(err)
				}
				if event.Type == "thinking.timing" && event.Metadata["thinking_active"] == false {
					stoppedMetadata = event.Metadata
				}
			}
			if stoppedMetadata == nil || messages[1].Metadata["thinking_duration_ms"] != stoppedMetadata["thinking_duration_ms"] {
				t.Fatalf("stored thinking duration differs from stream: stored=%#v stream=%#v", messages[1].Metadata, stoppedMetadata)
			}
		})
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
