package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func TestConversationWebSearchSettingValidationAndBusyGuard(t *testing.T) {
	db, api, _ := searchTestAPI(t, "")
	created := searchAPIRequest(api, "POST", "/api/conversations", `{"model":"fixture","web_search_enabled":false}`)
	var conversation store.Conversation
	if err := json.Unmarshal(created.Body.Bytes(), &conversation); err != nil || created.Code != 201 || conversation.WebSearchEnabled {
		t.Fatalf("create off: %s %v", created.Body.String(), err)
	}
	path := fmt.Sprintf("/api/conversations/%d/web-search", conversation.ID)
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"false"}`, `{"enabled":true,"api_token":"not-allowed"}`} {
		if response := searchAPIRequest(api, "PUT", path, body); response.Code != 400 {
			t.Fatalf("accepted %s: %s", body, response.Body.String())
		}
	}
	api.active[conversation.ID] = struct{}{}
	response := searchAPIRequest(api, "PUT", path, `{"enabled":true}`)
	if response.Code != 409 {
		t.Fatalf("busy guard: %s", response.Body.String())
	}
	delete(api.active, conversation.ID)
	response = searchAPIRequest(api, "PUT", path, `{"enabled":true}`)
	if err := json.Unmarshal(response.Body.Bytes(), &conversation); err != nil || response.Code != 200 || !conversation.WebSearchEnabled {
		t.Fatalf("enable: %s %v", response.Body.String(), err)
	}
	settings, err := db.GetWebSearchSettings(context.Background())
	if err != nil || settings.Enabled || settings.Token != "" {
		t.Fatal("session switch modified global provider settings")
	}
	if response = searchAPIRequest(api, "PUT", "/api/conversations/9999/web-search", `{"enabled":false}`); response.Code != 404 {
		t.Fatalf("missing session: %s", response.Body.String())
	}
}

func TestSessionWebSearchPermissionControlsActualGeneration(t *testing.T) {
	searches := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { searches++; io.WriteString(w, `{"results":[]}`) }))
	defer provider.Close()
	available := false
	expected := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			io.WriteString(w, `{"models":[{"name":"fixture","capabilities":["completion","tools"]}]}`)
			return
		}
		var body ollama.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		available = false
		for _, tool := range body.Tools {
			if tool.Function.Name == "web_search" {
				available = true
			}
		}
		if available != expected {
			t.Errorf("session tool advertised=%v want=%v", available, expected)
		}
		if body.Messages[len(body.Messages)-1].Role == "tool" {
			io.WriteString(w, `{"message":{"role":"assistant","content":"Finished"},"done":true}`+"\n")
			return
		}
		// A model can request a hidden tool; execution must still enforce permission.
		io.WriteString(w, `{"message":{"role":"assistant","tool_calls":[{"id":"search","function":{"name":"web_search","arguments":{"query":"fixture"}}}]},"done":true}`+"\n")
	}))
	defer upstream.Close()
	db, api, _ := searchTestAPI(t, provider.URL)
	api.ollama = ollama.NewClient(upstream.URL)
	api.agent.Chat = api.ollama
	api.agent.MaxTurns = 2
	if err := db.SetWebSearchSettings(context.Background(), store.WebSearchSettings{Enabled: true, Token: "tvly-fixture"}); err != nil {
		t.Fatal(err)
	}
	off := false
	conversation, err := db.CreateConversation(context.Background(), store.CreateConversationParams{Model: "fixture", WebSearchEnabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/conversations/%d", conversation.ID)
	for _, enabled := range []bool{false, true, false} {
		expected = enabled
		before := searches
		if response := searchAPIRequest(api, "PUT", path+"/web-search", fmt.Sprintf(`{"enabled":%t}`, enabled)); response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		response := searchAPIRequest(api, "POST", path+"/messages", `{"content":"Find fixture"}`)
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		wanted := before
		if enabled {
			wanted++
		}
		if searches != wanted {
			t.Fatalf("permission=%v search calls=%d want=%d", enabled, searches, wanted)
		}
	}
	messages, err := db.Messages(context.Background(), conversation.ID)
	if err != nil || len(messages) != 6 {
		t.Fatalf("history: %d %v", len(messages), err)
	}
}
