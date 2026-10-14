package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

type recordingWebSearch struct {
	enabled bool
	query   string
	limit   int
	calls   int
	err     error
	result  WebSearchResponse
}

func (search *recordingWebSearch) Enabled(context.Context) bool { return search.enabled }

func (search *recordingWebSearch) Search(_ context.Context, query string, limit int) (WebSearchResponse, error) {
	search.query, search.limit = query, limit
	search.calls++
	return search.result, search.err
}

func searchCall(raw string) ollama.ToolCall {
	return ollama.ToolCall{ID: "search-call", Function: ollama.ToolCallFunction{Name: "web_search", Arguments: json.RawMessage(raw)}}
}

func TestWebSearchToolAvailableOnlyToEnabledAgents(t *testing.T) {
	for _, test := range []struct {
		mode     string
		enabled  bool
		disabled bool
		want     bool
	}{{"agent", true, false, true}, {"agent", true, true, false}, {"agent", false, false, false}, {"chat", true, false, false}, {"chat", false, false, false}} {
		chat := &scriptedChat{turns: [][]ollama.ChatChunk{{{Message: ollama.Message{Content: "done"}, Done: true}}}}
		runner := Runner{Chat: chat, MaxTurns: 2, WebSearch: &recordingWebSearch{enabled: test.enabled}}
		if _, err := runner.Run(context.Background(), RunInput{Mode: test.mode, DisableWebSearch: test.disabled}, func(Event) error { return nil }); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, tool := range chat.requests[0].Tools {
			if tool.Function.Name == "web_search" {
				found = true
				if !strings.Contains(tool.Function.Description, "untrusted") || !strings.Contains(tool.Function.Description, "source URLs") {
					t.Fatal("tool lacks source handling instructions")
				}
			}
		}
		if found != test.want {
			t.Fatalf("mode=%s enabled=%v disabled=%v advertised=%v", test.mode, test.enabled, test.disabled, found)
		}
	}
	for _, prompt := range []string{SystemPrompt, delegatedSystemPrompt} {
		if !strings.Contains(prompt, "untrusted") || !strings.Contains(prompt, "Markdown links") {
			t.Fatal("agent instructions omit trust or citation rules")
		}
	}
}

func TestRunnerSearchPersistsActivityAndFeedsCitableSources(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(ctx, store.CreateConversationParams{Title: "Search", Model: "tools"})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := database.AddMessage(ctx, conversation.ID, "assistant", "", "streaming")
	if err != nil {
		t.Fatal(err)
	}
	search := &recordingWebSearch{enabled: true, result: WebSearchResponse{Query: "weather", Results: []WebSearchResult{
		{Title: "Forecast", URL: "https://example.com/forecast", Content: "Rain tomorrow", Score: 0.8},
	}}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{searchCall(`{"query":"  weather  ","max_results":3}`)}}, Done: true}},
		{{Message: ollama.Message{Content: "Rain tomorrow. [Forecast](https://example.com/forecast)"}, Done: true}},
	}}
	var events []Event
	result, err := (Runner{Chat: chat, Steps: database, WebSearch: search, MaxTurns: 3}).Run(ctx, RunInput{
		Model: "tools", Mode: "agent", ConversationID: conversation.ID, AssistantMessageID: assistant.ID,
	}, func(event Event) error { events = append(events, event); return nil })
	if err != nil || !strings.Contains(result.Content, "https://example.com/forecast") || search.calls != 1 || search.query != "weather" || search.limit != 3 {
		t.Fatalf("search run failed: %#v %v %#v", result, err, search)
	}
	feedback := chat.requests[1].Messages[len(chat.requests[1].Messages)-1]
	if feedback.Role != "tool" || feedback.ToolName != "web_search" || feedback.ToolCallID != "search-call" || !strings.Contains(feedback.Content, "https://example.com/forecast") {
		t.Fatalf("source feedback missing: %#v", feedback)
	}
	started, completed := false, false
	for _, event := range events {
		if event.Type == "tool.started" && event.Step.ToolName == "web_search" {
			started = true
		}
		if event.Type == "tool.done" && event.Step.Status == "complete" {
			completed = true
		}
	}
	messages, err := database.Messages(ctx, conversation.ID)
	if err != nil || !started || !completed || len(messages) != 1 || len(messages[0].AgentSteps) != 1 || messages[0].AgentSteps[0].Status != "complete" || !strings.Contains(messages[0].AgentSteps[0].Output, "https://example.com/forecast") {
		t.Fatalf("search activity did not survive reload: %#v, %v", messages, err)
	}
}

func TestSearchFailuresContinueModelAndCancellationPersists(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status string
	}{{"provider error", errors.New("Tavily rejected the token; update it in Settings"), "error"},
		{"timeout", context.DeadlineExceeded, "error"}, {"cancel", context.Canceled, "cancelled"}} {
		t.Run(test.name, func(t *testing.T) {
			chat := &scriptedChat{turns: [][]ollama.ChatChunk{
				{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{searchCall(`{"query":"query"}`)}}, Done: true}},
				{{Message: ollama.Message{Content: "Search failed; try again later."}, Done: true}},
			}}
			steps := &memorySteps{}
			search := &recordingWebSearch{enabled: true, err: test.err}
			result, err := (Runner{Chat: chat, Steps: steps, WebSearch: search, MaxTurns: 3}).Run(context.Background(), RunInput{Mode: "agent", AssistantMessageID: 1}, func(Event) error { return nil })
			if len(steps.steps) != 1 || steps.steps[0].Status != test.status {
				t.Fatalf("error activity not terminal: %#v", steps.steps)
			}
			if test.status == "cancelled" {
				if !errors.Is(err, context.Canceled) || len(chat.requests) != 1 {
					t.Fatalf("cancel did not stop agent: %v", err)
				}
			} else if err != nil || result.Content == "" || len(chat.requests) != 2 {
				t.Fatalf("recoverable error stopped agent: %#v %v", result, err)
			}
			if test.name == "timeout" && !strings.Contains(chat.requests[1].Messages[1].Content, `"timed_out":true`) {
				t.Fatal("timeout status missing from tool feedback")
			}
		})
	}
}

func TestWebSearchStrictArgumentsAndRuntimeDisable(t *testing.T) {
	search := &recordingWebSearch{enabled: true}
	runner := Runner{WebSearch: search}
	for _, raw := range []string{`{}`, `null`, `[]`, `{"query":" "}`, `{"query":10}`, `{"query":"ok","max_results":0}`,
		`{"query":"ok","max_results":11}`, `{"query":"ok","max_results":2.5}`, `{"query":"ok","max_results":"2"}`, `{"query":"ok","max_results":null}`,
		`{"query":"ok","api_token":"private"}`, `{"query":"ok","include_raw_content":true}`, `{"query":"ok","endpoint":"https://other"}`,
		`{"query":"ok"} {}`, `{"query":"` + strings.Repeat("x", 4097) + `"}`} {
		result, status := runner.executeWebSearch(context.Background(), json.RawMessage(raw))
		if status != "error" || result.ExitCode != -1 || search.calls != 0 {
			t.Fatalf("invalid search args executed: %s -> %#v", raw, result)
		}
	}
	search.enabled = false
	if _, status := runner.executeWebSearch(context.Background(), json.RawMessage(`{"query":"query"}`)); status != "error" || search.calls != 0 {
		t.Fatal("disabled runtime search called provider")
	}
	search.enabled = true
	if _, status := runner.executeWebSearch(context.Background(), json.RawMessage(`{"query":"query"}`)); status != "complete" || search.limit != 5 {
		t.Fatal("default result count was not used")
	}
}

func TestSearchFeedbackPreservesJSONAndSourceURLsWithinBudget(t *testing.T) {
	response := WebSearchResponse{Query: "query", Results: []WebSearchResult{
		{Title: "One", URL: "https://example.com/1", Content: strings.Repeat("中文<", 1000)},
		{Title: "Two", URL: "https://example.com/2", Content: strings.Repeat("large snippet ", 1000)},
	}}
	encoded, _ := json.Marshal(response)
	feedback, truncated := boundedWebSearchFeedback(string(encoded), 1000)
	var decoded WebSearchResponse
	if !truncated || len(feedback) > 1000 || !utf8.ValidString(feedback) || json.Unmarshal([]byte(feedback), &decoded) != nil || len(decoded.Results) != 2 {
		t.Fatalf("invalid bounded search feedback: %s", feedback)
	}
	if decoded.Results[0].URL != response.Results[0].URL || decoded.Results[1].URL != response.Results[1].URL {
		t.Fatal("source URLs were lost")
	}
}

func TestChatNeverExecutesUnexpectedSearchToolCall(t *testing.T) {
	search := &recordingWebSearch{enabled: true}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{{{Message: ollama.Message{Content: "Chat answer", ToolCalls: []ollama.ToolCall{searchCall(`{"query":"query"}`)}}, Done: true}}}}
	result, err := (Runner{Chat: chat, WebSearch: search}).Run(context.Background(), RunInput{Mode: "chat"}, func(Event) error { return nil })
	if err != nil || result.Content != "Chat answer" || search.calls != 0 || len(chat.requests[0].Tools) != 0 {
		t.Fatalf("Chat invoked search: %#v %v", result, err)
	}
}

func TestSubagentInheritsSearchWithoutParentHistory(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(ctx, store.CreateConversationParams{Title: "Search child", Model: "tools"})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := database.AddMessage(ctx, conversation.ID, "assistant", "", "streaming")
	if err != nil {
		t.Fatal(err)
	}
	search := &recordingWebSearch{enabled: true, result: WebSearchResponse{Query: "current facts", Results: []WebSearchResult{{URL: "https://example.com/source", Content: "Facts"}}}}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{searchCall(`{"query":"current facts"}`)}}, Done: true}},
		{{Message: ollama.Message{Content: "Web search is disabled for this session."}, Done: true}},
		{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{searchCall(`{"query":"current facts"}`)}}, Done: true}},
		{{Message: ollama.Message{Content: "[Source](https://example.com/source)"}, Done: true}},
	}}
	orchestrator, err := NewSubagentOrchestrator(database, chat, &recordingShell{}, nil, t.TempDir(), "tools", 4096, 3, 1, 1024, 4096)
	if err != nil {
		t.Fatal(err)
	}
	orchestrator.WebSearch = search
	offResult, err := orchestrator.Execute(ctx, SubagentRequest{ConversationID: conversation.ID, RequestingMessageID: assistant.ID, Task: "Search current facts", DisableWebSearch: true}, func(Event) error { return nil })
	if err != nil || search.calls != 0 || !strings.Contains(offResult.Feedback, "disabled") || len(chat.requests) != 2 {
		t.Fatalf("disabled child executed a search: %#v, %v", offResult, err)
	}
	for _, tool := range chat.requests[0].Tools {
		if tool.Function.Name == "web_search" {
			t.Fatal("disabled child was offered web_search")
		}
	}
	result, err := orchestrator.Execute(ctx, SubagentRequest{ConversationID: conversation.ID, RequestingMessageID: assistant.ID, Task: "Search current facts"}, func(Event) error { return nil })
	if err != nil || search.calls != 1 || !strings.Contains(result.Feedback, "https://example.com/source") || len(chat.requests[2].Messages) != 2 {
		t.Fatalf("child search failed: %#v, %v", result, err)
	}
	for _, tool := range chat.requests[2].Tools {
		if tool.Function.Name == "web_search" {
			return
		}
	}
	t.Fatal("child not offered web_search")
}

func TestRunnerPassesSessionSearchPreferenceToDelegatedTask(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		chat := &scriptedChat{turns: [][]ollama.ChatChunk{
			{{Message: ollama.Message{ToolCalls: []ollama.ToolCall{{Function: ollama.ToolCallFunction{Name: "delegate_task", Arguments: json.RawMessage(`{"task":"Check current facts"}`)}}}}, Done: true}},
			{{Message: ollama.Message{Content: "done"}, Done: true}},
		}}
		child := &fakeSubagentExecutor{}
		_, err := (Runner{Chat: chat, Steps: &memorySteps{}, Subagents: child, MaxTurns: 2}).Run(context.Background(), RunInput{
			Mode: "agent", ConversationID: 1, AssistantMessageID: 2, DisableWebSearch: disabled,
		}, func(Event) error { return nil })
		if err != nil || child.request.DisableWebSearch != disabled {
			t.Fatalf("child received wrong session preference: %#v, %v", child.request, err)
		}
	}
}

type concurrentWebSearch struct{ calls atomic.Int64 }

func (*concurrentWebSearch) Enabled(context.Context) bool { return true }

func (search *concurrentWebSearch) Search(_ context.Context, query string, _ int) (WebSearchResponse, error) {
	search.calls.Add(1)
	return WebSearchResponse{Query: query, Results: []WebSearchResult{{Title: "Source", URL: "https://example.com/source"}}}, nil
}

func TestSharedRunnerKeepsConcurrentSessionSearchPreferencesIndependent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	search := &concurrentWebSearch{}
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	chat := reasoningChatFunc(func(ctx context.Context, request ollama.ChatRequest, emit func(ollama.ChatChunk) error) error {
		if len(request.Messages) == 0 {
			advertised := false
			for _, tool := range request.Tools {
				advertised = advertised || tool.Function.Name == "web_search"
			}
			if advertised != (request.Model == "search-on") {
				return fmt.Errorf("%s advertised search=%v", request.Model, advertised)
			}
			ready <- struct{}{}
			select {
			case <-start:
			case <-ctx.Done():
				return ctx.Err()
			}
			// Both models request search, including a hallucinated call in the
			// disabled session. Only the allowed session may reach the provider.
			return emit(ollama.ChatChunk{Message: ollama.Message{ToolCalls: []ollama.ToolCall{searchCall(`{"query":"current facts"}`)}}, Done: true})
		}
		feedback := request.Messages[len(request.Messages)-1].Content
		if request.Model == "search-off" && !strings.Contains(feedback, "disabled for this session") {
			return errors.New("disabled session did not receive a blocked-search error")
		}
		if request.Model == "search-on" && !strings.Contains(feedback, "https://example.com/source") {
			return errors.New("enabled session lost its search results")
		}
		return emit(ollama.ChatChunk{Message: ollama.Message{Content: "done"}, Done: true})
	})
	steps := &transientStepStore{}
	runner := Runner{Chat: chat, Steps: steps, WebSearch: search, MaxTurns: 2}
	finished := make(chan error, 2)
	for _, disabled := range []bool{true, false} {
		go func(disabled bool) {
			model := "search-on"
			if disabled {
				model = "search-off"
			}
			_, err := runner.Run(ctx, RunInput{Mode: "agent", Model: model, DisableWebSearch: disabled}, func(Event) error { return nil })
			finished <- err
		}(disabled)
	}
	for count := 0; count < 2; count++ {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("independent sessions did not start")
		}
	}
	close(start)
	for count := 0; count < 2; count++ {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	if search.calls.Load() != 1 || runner.WebSearch != search {
		t.Fatal("session toggle changed shared provider or allowed disabled search")
	}
	var complete, blocked int
	for _, step := range steps.steps {
		if step.Status == "complete" {
			complete++
		}
		if step.Status == "error" {
			blocked++
		}
	}
	if complete != 1 || blocked != 1 {
		t.Fatalf("search activity mismatch: complete=%d blocked=%d", complete, blocked)
	}
}
