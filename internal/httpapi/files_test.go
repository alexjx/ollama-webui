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
	"strings"
	"testing"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func TestDecodeTextFilesPreservesExactNameAndBytes(t *testing.T) {
	content := "  世界\r\n\n\tend\n  "
	files, err := decodeTextFiles([]imageInput{{Name: " 日志.txt ", MediaType: "text/plain", Data: base64.StdEncoding.EncodeToString([]byte(content))}})
	if err != nil || len(files) != 1 || files[0].FileName != " 日志.txt " || string(files[0].Data) != content || files[0].MediaType != "text/plain" {
		t.Fatalf("file did not retain browser data: %#v, %v", files, err)
	}
}

func TestDecodeTextFilesRejectsInvalidInputAndBudgets(t *testing.T) {
	good := imageInput{Name: "paste.txt", MediaType: "text/plain", Data: base64.StdEncoding.EncodeToString([]byte("text"))}
	cases := []struct {
		name  string
		input imageInput
	}{
		{"wrong MIME", imageInput{Name: good.Name, MediaType: "image/png", Data: good.Data}},
		{"bad base64", imageInput{Name: good.Name, MediaType: good.MediaType, Data: "!"}},
		{"invalid UTF8", imageInput{Name: good.Name, MediaType: good.MediaType, Data: base64.StdEncoding.EncodeToString([]byte{0xff})}},
		{"NUL", imageInput{Name: good.Name, MediaType: good.MediaType, Data: base64.StdEncoding.EncodeToString([]byte("binary\x00text"))}},
		{"empty text", imageInput{Name: good.Name, MediaType: good.MediaType}},
		{"empty name", imageInput{Name: " ", MediaType: good.MediaType, Data: good.Data}},
		{"path", imageInput{Name: "../paste.txt", MediaType: good.MediaType, Data: good.Data}},
		{"windows path", imageInput{Name: `C:\paste.txt`, MediaType: good.MediaType, Data: good.Data}},
		{"header newline", imageInput{Name: "paste\r\n.txt", MediaType: good.MediaType, Data: good.Data}},
		{"long name", imageInput{Name: strings.Repeat("x", 181), MediaType: good.MediaType, Data: good.Data}},
		{"too large", imageInput{Name: good.Name, MediaType: good.MediaType, Data: base64.StdEncoding.EncodeToString(make([]byte, maxImageBytes+1))}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeTextFiles([]imageInput{test.input}); err == nil {
				t.Fatal("invalid file accepted")
			}
		})
	}
	if _, err := decodeTextFiles([]imageInput{good, good, good, good, good}); err == nil {
		t.Fatal("five files accepted")
	}
	if err := validateAttachmentBudget(make([]store.NewAttachment, 5)); err == nil {
		t.Fatal("combined image and file count ignored")
	}
	data := []byte(strings.Repeat("x", maxImageBytes))
	if err := validateAttachmentBudget([]store.NewAttachment{{Data: data}, {Data: data}}); err != nil {
		t.Fatalf("20MiB boundary rejected: %v", err)
	}
	if err := validateAttachmentBudget([]store.NewAttachment{{Data: data}, {Data: data}, {Data: []byte("x")}}); err == nil {
		t.Fatal("combined size limit ignored")
	}
}

func TestGenerateTextFilesWithNonvisionChatAndAgentModels(t *testing.T) {
	for _, mode := range []string{"chat", "agent"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			content := "  file start\r\n" + strings.Repeat("世界\n", 5000) + "  file end\n"
			var captured ollama.ChatRequest
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/tags" {
					io.WriteString(w, `{"models":[{"name":"text","capabilities":["completion","tools"]}]}`)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
					t.Error(err)
				}
				io.WriteString(w, "{\"message\":{\"role\":\"assistant\",\"content\":\"done\"},\"done\":true}\n")
			}))
			defer upstream.Close()
			database, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			conversation, err := database.CreateConversation(ctx, store.CreateConversationParams{Model: "text", Mode: mode})
			if err != nil {
				t.Fatal(err)
			}
			client := ollama.NewClient(upstream.URL)
			workspace := t.TempDir()
			api := New(database, client, agent.Runner{Chat: client, Steps: database, Shell: agent.ShellExecutor{Workspace: workspace}, MaxTurns: 3, Context: &agent.ContextPreparer{Chat: client, Store: database}}, RuntimeSettings{ContextTokens: 4096}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			body, _ := json.Marshal(map[string]any{"content": "", "files": []imageInput{{Name: "pasted.txt", MediaType: "text/plain", Data: base64.StdEncoding.EncodeToString([]byte(content))}}})
			response := httptest.NewRecorder()
			api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/conversations/%d/messages", conversation.ID), strings.NewReader(string(body))))
			if response.Code != 200 || strings.Contains(response.Body.String(), `"type":"error"`) {
				t.Fatalf("file generation: %d %s", response.Code, response.Body.String())
			}
			history, err := database.MessagesWithAttachmentData(ctx, conversation.ID)
			if err != nil || len(history) != 2 || history[0].Content != "" || len(history[0].Attachments) != 1 || string(history[0].Attachments[0].Data) != content || history[0].Attachments[0].FileName != "pasted.txt" {
				t.Fatalf("file did not persist exactly: %#v, %v", history, err)
			}
			user := captured.Messages[len(captured.Messages)-1]
			if len(user.Images) != 0 || !strings.Contains(user.Content, "pasted.txt") || !strings.Contains(user.Content, "attachment_id=") {
				t.Fatalf("file wrongly treated as image or lost: %#v", user)
			}
			if strings.Contains(user.Content, content) != (mode == "chat") {
				t.Fatalf("wrong model file content for %s: %q", mode, user.Content)
			}
			tools := make(map[string]bool)
			for _, tool := range captured.Tools {
				tools[tool.Function.Name] = true
			}
			if mode == "chat" && len(tools) != 0 || mode == "agent" && (!tools["attachment_list"] || !tools["attachment_read"]) {
				t.Fatalf("wrong attachment tool availability: %#v", tools)
			}
			entries, err := os.ReadDir(workspace)
			if err != nil || len(entries) != 0 {
				t.Fatalf("explicit upload was staged to workspace: %#v, %v", entries, err)
			}
			download := httptest.NewRecorder()
			api.ServeHTTP(download, httptest.NewRequest(http.MethodGet, history[0].Attachments[0].URL, nil))
			if download.Code != 200 || download.Body.String() != content || download.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
				t.Fatal("download changed uploaded text")
			}
		})
	}
}

func TestTextAttachmentDoesNotBlockChangingToNonvisionModel(t *testing.T) {
	ctx := context.Background()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"models":[{"name":"text","capabilities":["completion"]}]}`)
	}))
	defer upstream.Close()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(ctx, store.CreateConversationParams{Model: "old", Mode: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddMessageWithAttachments(ctx, conversation.ID, "user", "", "complete", []store.NewAttachment{{FileName: "pasted.txt", MediaType: "text/plain", Data: []byte("text")}}); err != nil {
		t.Fatal(err)
	}
	api := New(database, ollama.NewClient(upstream.URL), agent.Runner{}, RuntimeSettings{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/conversations/%d/model", conversation.ID), strings.NewReader(`{"model":"text"}`)))
	if response.Code != 200 {
		t.Fatalf("text-only history wrongly required vision: %d %s", response.Code, response.Body.String())
	}
}
