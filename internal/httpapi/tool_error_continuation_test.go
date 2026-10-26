package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func TestConversationContinuesAfterRepeatedMalformedToolArguments(t *testing.T) {
	var continuationHistory []ollama.Message
	failedAttempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			io.WriteString(w, `{"models":[{"name":"fixture","capabilities":["completion","tools"]}]}`)
			return
		}
		var request ollama.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, message := range request.Messages {
			if message.Role == "user" && message.Content == "Continue this session" {
				continuationHistory = request.Messages
				io.WriteString(w, `{"message":{"role":"assistant","content":"Session continued."},"done":true}`+"\n")
				return
			}
		}
		failedAttempts++
		io.WriteString(w, `{"message":{"role":"assistant","thinking":"Preparing a command."},"done":false}`+"\n")
		io.WriteString(w, `{"error":"llama-server returned invalid tool call arguments for \"shell\": unexpected end of JSON input"}`+"\n")
	}))
	defer upstream.Close()

	database, api, _ := searchTestAPI(t, "")
	api.ollama = ollama.NewClient(upstream.URL)
	api.agent.Chat = api.ollama
	api.agent.MaxTurns = 5
	conversation, err := database.CreateConversation(context.Background(), store.CreateConversationParams{Title: "Recovery test", Model: "fixture", Mode: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/conversations/%d/messages", conversation.ID)
	first := searchAPIRequest(api, http.MethodPost, path, `{"content":"Run a command"}`)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"type":"error"`) {
		t.Fatalf("expected exhausted generation to report its failure: %d %s", first.Code, first.Body.String())
	}
	if failedAttempts != 3 {
		t.Fatalf("expected initial attempt plus two recovery attempts, got %d", failedAttempts)
	}
	if !strings.Contains(first.Body.String(), "You can send another message to continue this conversation") {
		t.Fatalf("missing continuation guidance: %s", first.Body.String())
	}
	messages, err := database.Messages(context.Background(), conversation.ID)
	if err != nil || len(messages) != 2 || messages[1].Status != "error" {
		t.Fatalf("failed turn was not persisted: %#v, %v", messages, err)
	}

	next := searchAPIRequest(api, http.MethodPost, path, `{"content":"Continue this session"}`)
	if next.Code != http.StatusOK || strings.Contains(next.Body.String(), `"type":"error"`) || !strings.Contains(next.Body.String(), `"type":"done"`) {
		t.Fatalf("same conversation cannot continue: %d %s", next.Code, next.Body.String())
	}
	messages, err = database.Messages(context.Background(), conversation.ID)
	if err != nil || len(messages) != 4 || messages[3].Status != "complete" || messages[3].Content != "Session continued." {
		t.Fatalf("continuation was not persisted: %#v, %v", messages, err)
	}
	foundOriginal := false
	for _, message := range continuationHistory {
		if message.Role == "user" && message.Content == "Run a command" {
			foundOriginal = true
		}
	}
	if !foundOriginal {
		t.Fatalf("continuation lost original conversation history: %#v", continuationHistory)
	}
}
