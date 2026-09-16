package agent

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func TestImageBatchUsesOneImagePerContextAndWritesAtomicJSONL(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	conversation, err := database.CreateConversation(ctx, store.CreateConversationParams{Title: "Labels", Model: "vision-tools"})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := database.AddMessage(ctx, conversation.ID, "assistant", "", "streaming")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	inputDirectory := filepath.Join(t.TempDir(), "images")
	if err := os.Mkdir(inputDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	png, err := base64.StdEncoding.DecodeString(onePixelPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b.png", "a.png"} {
		if err := os.WriteFile(filepath.Join(inputDirectory, name), png, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	chat := &scriptedChat{turns: [][]ollama.ChatChunk{
		{{Message: ollama.Message{Content: `{"labels":["cat"],"confidence":0.9,"needs_review":false,"notes":"clear"}`}, Done: true}},
		{{Message: ollama.Message{Content: `not-json`}, Done: true}},
	}}
	orchestrator, err := NewSubagentOrchestrator(database, chat, &recordingShell{}, nil,
		workspace, "", 2048, 2, 1, 1024, 1024)
	if err != nil {
		t.Fatal(err)
	}
	events := make([]Event, 0)
	result, err := orchestrator.LabelImageDirectory(ctx, ImageBatchRequest{
		ConversationID: conversation.ID, RequestingMessageID: assistant.ID, Model: "vision-tools", SupportsVision: true,
		Directory: inputDirectory, Instructions: "Use only the label cat.", OutputPath: "labels/all.jsonl",
	}, func(event Event) error { events = append(events, event); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || len(chat.requests) != 2 {
		t.Fatalf("unexpected batch result: %#v requests=%d", result, len(chat.requests))
	}
	for _, request := range chat.requests {
		if len(request.Messages) != 2 || len(request.Messages[1].Images) != 1 {
			t.Fatalf("image calls accumulated context: %#v", request.Messages)
		}
	}
	file, err := os.Open(filepath.Join(workspace, "labels", "all.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows := make([]imageLabel, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var row imageLabel
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Path != "a.png" || rows[0].Labels[0] != "cat" || rows[1].Path != "b.png" || rows[1].Error == "" || !rows[1].NeedsReview {
		t.Fatalf("unexpected label rows: %#v", rows)
	}
	if len(events) != 3 || events[0].Type != "subagent.started" || events[1].Type != "subagent.progress" || events[2].Type != "subagent.done" {
		t.Fatalf("unexpected batch events: %#v", events)
	}
	runs, err := database.ListAgentRunsByRequestingMessage(ctx, assistant.ID)
	if err != nil || len(runs) != 1 || runs[0].Status != store.AgentStatusComplete || len(runs[0].OutputRefs) != 1 || runs[0].OutputRefs[0] != "labels/all.jsonl" {
		t.Fatalf("batch run was not persisted: %#v, %v", runs, err)
	}
}

func TestPrepareWorkspaceOutputRejectsEscapeAndSymlinkParent(t *testing.T) {
	workspace := t.TempDir()
	if _, err := prepareWorkspaceOutput(workspace, "../escape.jsonl"); err == nil {
		t.Fatal("expected workspace escape to fail")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareWorkspaceOutput(workspace, "linked/output.jsonl"); err == nil {
		t.Fatal("expected symlink parent to fail")
	}
}
