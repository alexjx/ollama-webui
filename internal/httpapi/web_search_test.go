package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
	"ollama-webui/internal/tavily"
)

func searchTestAPI(t *testing.T, providerURL string) (*store.Store, *Server, *bytes.Buffer) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	client := tavily.NewClient(func(ctx context.Context) (tavily.Config, error) {
		settings, err := db.GetWebSearchSettings(ctx)
		return tavily.Config{Enabled: settings.Enabled, Token: settings.Token}, err
	})
	if providerURL != "" {
		client.Endpoint = providerURL + "/search"
	}
	logs := new(bytes.Buffer)
	api := New(db, nil, agent.Runner{WebSearch: client, Steps: db}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(api.Close)
	return db, api, logs
}

func searchAPIRequest(api *Server, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func TestWebSearchSettingsManageBearerTokenWithoutReturningIt(t *testing.T) {
	db, api, logs := searchTestAPI(t, "")
	for _, testCase := range []struct {
		body                string
		status              int
		enabled, configured bool
		token               string
	}{
		{`{"enabled":true}`, 422, false, false, ""},
		{`{"enabled":true,"api_token":" tvly-first-token "}`, 200, true, true, "tvly-first-token"},
		{`{"enabled":false}`, 200, false, true, "tvly-first-token"},
		{`{"enabled":true,"api_token":""}`, 200, true, true, "tvly-first-token"},
		{`{"api_token":"tvly-second-token"}`, 200, true, true, "tvly-second-token"},
		{`{"enabled":true,"remove_token":true}`, 200, false, false, ""},
	} {
		response := searchAPIRequest(api, http.MethodPut, "/api/settings/web-search", testCase.body)
		if response.Code != testCase.status {
			t.Fatalf("settings status %d want %d: %s", response.Code, testCase.status, response.Body.String())
		}
		settings, err := db.GetWebSearchSettings(context.Background())
		if err != nil || settings.Enabled != testCase.enabled || settings.Token != testCase.token {
			t.Fatalf("settings update failed: enabled=%v configured=%v %v", settings.Enabled, settings.Token != "", err)
		}
		if response.Code == 200 {
			var public struct {
				Provider   string `json:"provider"`
				Enabled    bool   `json:"enabled"`
				Configured bool   `json:"configured"`
			}
			if err = json.Unmarshal(response.Body.Bytes(), &public); err != nil || public.Provider != "tavily" || public.Enabled != testCase.enabled || public.Configured != testCase.configured {
				t.Fatalf("unexpected public status: %s %v", response.Body.String(), err)
			}
		}
		full := searchAPIRequest(api, http.MethodGet, "/api/settings", "")
		if full.Code != 200 || !strings.Contains(full.Body.String(), `"web_search"`) {
			t.Fatalf("missing search settings: %s", full.Body.String())
		}
		for _, text := range []string{response.Body.String(), full.Body.String(), logs.String()} {
			if strings.Contains(text, "tvly-first-token") || strings.Contains(text, "tvly-second-token") {
				t.Fatal("API token leaked into public response or request log")
			}
		}
	}
}

func TestDefaultSearchProviderAndChildrenObserveSavedConfiguration(t *testing.T) {
	db, _, _ := searchTestAPI(t, "")
	children := &agent.SubagentOrchestrator{}
	api := New(db, nil, agent.Runner{Subagents: children}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer api.Close()
	if api.agent.WebSearch == nil || children.WebSearch != api.agent.WebSearch || api.agent.WebSearch.Enabled(context.Background()) {
		t.Fatal("default and child provider configuration was not wired")
	}
	if err := db.SetWebSearchSettings(context.Background(), store.WebSearchSettings{Enabled: true, Token: "tvly-runtime-token"}); err != nil {
		t.Fatal(err)
	}
	if !api.agent.WebSearch.Enabled(context.Background()) || !children.WebSearch.Enabled(context.Background()) {
		t.Fatal("saved token did not enable default and child search")
	}
	if err := db.SetWebSearchSettings(context.Background(), store.WebSearchSettings{}); err != nil {
		t.Fatal(err)
	}
	if api.agent.WebSearch.Enabled(context.Background()) || children.WebSearch.Enabled(context.Background()) {
		t.Fatal("token removal did not disable default and child search")
	}
}

func TestWebSearchSettingsValidateWithoutLosingSavedToken(t *testing.T) {
	db, api, _ := searchTestAPI(t, "")
	if response := searchAPIRequest(api, http.MethodPut, "/api/settings/web-search", `{"enabled":true,"api_token":"tvly-saved-token"}`); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	for _, testCase := range []struct {
		body string
		code int
	}{
		{`{"enabled":"true"}`, 400}, {`{"password":"secret"}`, 400}, {`{"endpoint":"https://other.example"}`, 400},
		{`{"api_token":"token\r\nheader"}`, 422}, {`{"api_token":"two tokens"}`, 422},
		{`{"api_token":"new","remove_token":true}`, 422}, {`{"api_token":"` + strings.Repeat("x", 513) + `"}`, 422},
	} {
		response := searchAPIRequest(api, http.MethodPut, "/api/settings/web-search", testCase.body)
		if response.Code != testCase.code {
			t.Fatalf("invalid settings returned %d: %s", response.Code, response.Body.String())
		}
		saved, err := db.GetWebSearchSettings(context.Background())
		if err != nil || !saved.Enabled || saved.Token != "tvly-saved-token" {
			t.Fatal("failed update changed saved settings")
		}
	}
	if _, err := db.CreateConversation(context.Background(), store.CreateConversationParams{Title: "History", Model: "model", Mode: "chat"}); err != nil {
		t.Fatal(err)
	}
	cleared := searchAPIRequest(api, http.MethodDelete, "/api/conversations", `{"confirmation":"DELETE"}`)
	if cleared.Code != 200 {
		t.Fatalf("clear status %d: %s", cleared.Code, cleared.Body.String())
	}
	saved, err := db.GetWebSearchSettings(context.Background())
	if err != nil || !saved.Enabled || saved.Token != "tvly-saved-token" {
		t.Fatal("clearing conversation history removed global search settings")
	}
}

func TestWebSearchConnectivityUsesSavedTokenForOneSearchAndSanitizesFailure(t *testing.T) {
	calls, status := 0, http.StatusOK
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		if request.Method != http.MethodPost || request.URL.Path != "/search" || request.Header.Get("Authorization") != "Bearer tvly-connect-token" {
			t.Error("search did not use expected Bearer token and /search endpoint")
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["query"] != "Tavily" || body["max_results"] != float64(1) || body["search_depth"] != "basic" || body["include_raw_content"] != false || body["include_answer"] != false {
			t.Error("connectivity check used unsupported search features")
		}
		response.WriteHeader(status)
		if status != 200 {
			_, _ = io.WriteString(response, `{"error":"tvly-connect-token was rejected"}`)
			return
		}
		_, _ = io.WriteString(response, `{"results":[{"title":"Source","url":"https://example.com/source","content":"Snippet","score":0.9}]}`)
	}))
	defer provider.Close()
	_, api, logs := searchTestAPI(t, provider.URL)
	response := searchAPIRequest(api, http.MethodPost, "/api/settings/web-search/test", `{}`)
	if response.Code != 422 || calls != 0 {
		t.Fatal("disabled provider was called")
	}
	if response = searchAPIRequest(api, http.MethodPut, "/api/settings/web-search", `{"enabled":true,"api_token":"tvly-connect-token"}`); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response = searchAPIRequest(api, http.MethodPost, "/api/settings/web-search/test", "")
	if response.Code != 400 || calls != 0 {
		t.Fatal("test search accepted a simple cross-site form request")
	}
	response = searchAPIRequest(api, http.MethodPost, "/api/settings/web-search/test", `{}`)
	if response.Code != 200 || calls != 1 || !strings.Contains(response.Body.String(), `"result_count":1`) {
		t.Fatalf("test search failed: %s calls %d", response.Body.String(), calls)
	}
	status = 401
	response = searchAPIRequest(api, http.MethodPost, "/api/settings/web-search/test", `{}`)
	if response.Code != 502 || strings.Contains(response.Body.String(), "tvly-connect-token") || strings.Contains(logs.String(), "tvly-connect-token") || !strings.Contains(response.Body.String(), "rejected the token") {
		t.Fatalf("unsafe failure response: %s", response.Body.String())
	}
}

func TestGenerateAgentWebSearchPersistsSourcesWithoutSendingTokenToModel(t *testing.T) {
	token := "tvly-agent-token"
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/search" || request.Header.Get("Authorization") != "Bearer "+token {
			t.Error("wrong search authentication or endpoint")
		}
		_, _ = io.WriteString(response, `{"results":[{"title":"Fixture source","url":"https://example.com/source","content":"Verified source snippet","score":0.9}]}`)
	}))
	defer provider.Close()
	turns := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/tags" {
			_, _ = io.WriteString(response, `{"models":[{"name":"fixture","capabilities":["completion","tools"]}]}`)
			return
		}
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		if bytes.Contains(raw, []byte(token)) {
			t.Error("token sent to model")
		}
		var body ollama.ChatRequest
		if err = json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
		}
		available := false
		for _, tool := range body.Tools {
			if tool.Function.Name == "web_search" {
				available = true
			}
		}
		if !available {
			t.Error("enabled Agent model did not receive search tool")
		}
		turns++
		if turns == 1 {
			_, _ = io.WriteString(response, `{"message":{"role":"assistant","tool_calls":[{"id":"search_1","function":{"name":"web_search","arguments":{"query":"fixture query","max_results":2}}}]},"done":true}`+"\n")
			return
		}
		if !bytes.Contains(raw, []byte("https://example.com/source")) {
			t.Error("search source URL did not reach model")
		}
		_, _ = io.WriteString(response, `{"message":{"role":"assistant","content":"Verified: [Fixture source](https://example.com/source)."},"done":true}`+"\n")
	}))
	defer upstream.Close()
	db, api, logs := searchTestAPI(t, provider.URL)
	api.ollama = ollama.NewClient(upstream.URL)
	api.agent.Chat = api.ollama
	api.agent.MaxTurns = 2
	conversation, err := db.CreateConversation(context.Background(), store.CreateConversationParams{Title: "Search", Model: "fixture", Mode: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if response := searchAPIRequest(api, http.MethodPut, "/api/settings/web-search", `{"enabled":true,"api_token":"tvly-agent-token"}`); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response := searchAPIRequest(api, http.MethodPost, "/api/conversations/1/messages", `{"content":"Search the web"}`)
	if response.Code != 200 || turns != 2 {
		t.Fatalf("search run failed: %s", response.Body.String())
	}
	messages, err := db.Messages(context.Background(), conversation.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("missing messages: %v", err)
	}
	steps := messages[1].AgentSteps
	if messages[1].Status != "complete" || len(steps) != 1 || steps[0].ToolName != "web_search" || steps[0].Status != "complete" || !strings.Contains(steps[0].Output, "Verified source snippet") {
		t.Fatalf("missing completed search trace: %#v", messages[1])
	}
	if !strings.Contains(messages[1].Content, "https://example.com/source") {
		t.Fatal("final source citation not persisted")
	}
	for _, text := range []string{response.Body.String(), steps[0].Input, steps[0].Output, messages[1].Content, logs.String()} {
		if strings.Contains(text, token) {
			t.Fatal("token leaked into transcript, search trace or logs")
		}
	}
}
