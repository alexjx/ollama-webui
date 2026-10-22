package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func TestModelExchangePersistsActualRequestForConversationReload(t *testing.T) {
	var sent map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			io.WriteString(w, `{"models":[{"name":"fixture","capabilities":["completion"]}]}`)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Error(err)
			return
		}
		io.WriteString(w, `{"message":{"role":"assistant","thinking":"Reasoning","content":"Answer"},"done":true}`+"\n")
	}))
	defer upstream.Close()
	database, api, _ := searchTestAPI(t, "")
	api.ollama = ollama.NewClient(upstream.URL)
	api.agent.Chat = api.ollama
	conversation, err := database.CreateConversation(context.Background(), store.CreateConversationParams{Title: "Inspector", Model: "fixture", Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/conversations/%d", conversation.ID)
	response := searchAPIRequest(api, http.MethodPost, path+"/messages", `{"content":"Inspect this context"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", response.Code, response.Body.String())
	}
	reloaded := searchAPIRequest(api, http.MethodGet, path, "")
	if reloaded.Code != http.StatusOK {
		t.Fatalf("reload: %d %s", reloaded.Code, reloaded.Body.String())
	}
	var payload struct {
		Messages []struct {
			Metadata struct {
				Exchange struct {
					Request  map[string]any `json:"request"`
					Response ollama.Message `json:"response"`
					Status   string         `json:"status"`
					Turn     int            `json:"turn"`
				} `json:"model_exchange"`
			} `json:"metadata"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(reloaded.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Messages) != 2 {
		t.Fatalf("unexpected messages: %s", reloaded.Body.String())
	}
	exchange := payload.Messages[1].Metadata.Exchange
	if !reflect.DeepEqual(exchange.Request, sent) {
		t.Fatalf("snapshot differs from actual HTTP request: got %#v want %#v", exchange.Request, sent)
	}
	if exchange.Status != "complete" || exchange.Turn != 1 || exchange.Response.Content != "Answer" || exchange.Response.Thinking != "Reasoning" {
		t.Fatalf("exchange lost on reload: %#v", exchange)
	}
}
