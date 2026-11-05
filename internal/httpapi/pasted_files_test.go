package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func TestPastedFilesUseUniqueNamesAndPreserveExactBytes(t *testing.T) {
	workspace := t.TempDir()
	s := &Server{runtime: RuntimeSettings{Workspace: workspace}, logger: slog.Default()}
	paths := map[string]bool{}
	for _, conversationID := range []int64{12, 12, 13} {
		files := []store.NewAttachment{
			{FileName: "pasted.txt", MediaType: "text/plain", Data: []byte("  世界\r\n\tend\n  ")},
			{FileName: "pasted.txt", MediaType: "text/plain", Data: []byte("different")},
		}
		if err := s.persistPastedFiles(conversationID, files); err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if paths[file.WorkspacePath] || !strings.HasPrefix(file.WorkspacePath, fmt.Sprintf("pasted/conversation-%d-", conversationID)) {
				t.Fatalf("nonunique or unscoped path: %q", file.WorkspacePath)
			}
			paths[file.WorkspacePath] = true
			data, err := os.ReadFile(filepath.Join(workspace, file.WorkspacePath))
			if err != nil || string(data) != string(file.Data) {
				t.Fatalf("pasted bytes changed: %q, %v", data, err)
			}
		}
	}
}

func TestPastedFilesRejectSymlinkDirectories(t *testing.T) {
	for _, outside := range []bool{false, true} {
		t.Run(fmt.Sprintf("outside=%v", outside), func(t *testing.T) {
			workspace := t.TempDir()
			target := t.TempDir()
			if !outside {
				target = filepath.Join(workspace, "other")
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, filepath.Join(workspace, "pasted")); err != nil {
				t.Fatal(err)
			}
			s := &Server{runtime: RuntimeSettings{Workspace: workspace}, logger: slog.Default()}
			if err := s.persistPastedFiles(1, []store.NewAttachment{{MediaType: "text/plain", Data: []byte("private")}}); err == nil {
				t.Fatal("pasted file followed a symlink")
			}
			entries, err := os.ReadDir(target)
			if err != nil || len(entries) != 0 {
				t.Fatalf("symlink target modified: %v, %v", entries, err)
			}
		})
	}
}

func TestChatPastedFileReadsOnlyRequestedRange(t *testing.T) {
	ctx := context.Background()
	content := "secret answer\r\n" + strings.Repeat("unneeded context\n", 2000)
	var requests []ollama.ChatRequest
	idPattern := regexp.MustCompile(`attachment_id=(\d+)`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			io.WriteString(w, `{"models":[{"name":"text","capabilities":["completion","tools"]}]}`)
			return
		}
		var modelRequest ollama.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&modelRequest); err != nil {
			t.Error(err)
			return
		}
		requests = append(requests, modelRequest)
		if len(requests) == 1 {
			user := modelRequest.Messages[len(modelRequest.Messages)-1]
			match := idPattern.FindStringSubmatch(user.Content)
			if len(match) != 2 || strings.Contains(user.Content, "secret answer") || !strings.Contains(user.Content, "pasted/conversation-") {
				t.Errorf("initial request did not contain only a saved reference: %q", user.Content)
				return
			}
			id, _ := strconv.ParseInt(match[1], 10, 64)
			json.NewEncoder(w).Encode(ollama.ChatChunk{Message: ollama.Message{Role: "assistant", ToolCalls: []ollama.ToolCall{{ID: "read", Function: ollama.ToolCallFunction{Name: "attachment_read", Arguments: json.RawMessage(fmt.Sprintf(`{"attachment_id":%d,"limit":6}`, id))}}}}, Done: true})
			return
		}
		feedback := modelRequest.Messages[len(modelRequest.Messages)-1]
		if feedback.Role != "tool" || !strings.Contains(feedback.Content, `\"content\":\"secret\"`) || strings.Contains(feedback.Content, "unneeded context") {
			t.Errorf("file read did not limit model context: %q", feedback.Content)
		}
		io.WriteString(w, "{\"message\":{\"role\":\"assistant\",\"content\":\"The file starts with secret.\"},\"done\":true}\n")
	}))
	defer upstream.Close()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(ctx, store.CreateConversationParams{Model: "text", Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	client := ollama.NewClient(upstream.URL)
	api := New(database, client, agent.Runner{Chat: client, Steps: database, MaxTurns: 3}, RuntimeSettings{Workspace: workspace}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	body, _ := json.Marshal(map[string]any{"content": "Read the first six bytes", "files": []imageInput{{Name: "pasted.txt", MediaType: "text/plain", Data: base64.StdEncoding.EncodeToString([]byte(content))}}})
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/conversations/%d/messages", conversation.ID), strings.NewReader(string(body))))
	if response.Code != 200 || strings.Contains(response.Body.String(), `"type":"error"`) || len(requests) != 2 {
		t.Fatalf("lazy Chat failed: calls=%d status=%d body=%s", len(requests), response.Code, response.Body.String())
	}
	history, err := database.Messages(ctx, conversation.ID)
	if err != nil || len(history) != 2 {
		t.Fatalf("saved history: %v, %v", history, err)
	}
	attachment := history[0].Attachments[0]
	saved, err := os.ReadFile(filepath.Join(workspace, attachment.WorkspacePath))
	if err != nil || string(saved) != content || len(history[1].AgentSteps) != 1 {
		t.Fatalf("workspace file or read trace lost: %v", err)
	}
	for _, modelRequest := range requests {
		if len(modelRequest.Tools) != 2 {
			t.Fatalf("unexpected Chat tools: %#v", modelRequest.Tools)
		}
		for _, tool := range modelRequest.Tools {
			if tool.Function.Name != "attachment_read" && tool.Function.Name != "attachment_list" {
				t.Fatalf("Chat exposed %q", tool.Function.Name)
			}
		}
	}
}
