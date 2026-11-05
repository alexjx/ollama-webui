package store

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestAttachmentWorkspacePathPersistsAcrossReadsAndFork(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "attachments.db")
	db, err := Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := db.CreateConversation(ctx, CreateConversationParams{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("  世界\r\n\ttext\n ")
	const workspacePath = "pasted/thread-1-a1b2c3.txt"
	message, err := db.AddMessageWithAttachments(ctx, conversation.ID, "user", "Read this", "complete", []NewAttachment{{
		FileName: "paste.txt", MediaType: "text/plain", Data: content, WorkspacePath: workspacePath,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if message.Attachments[0].WorkspacePath != workspacePath {
		t.Fatalf("creation lost workspace path: %#v", message.Attachments[0])
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertAttachment := func(attachment Attachment, wantData []byte) {
		t.Helper()
		if attachment.WorkspacePath != workspacePath || attachment.Size != len(content) || !bytes.Equal(attachment.Data, wantData) {
			t.Fatalf("workspace metadata or canonical bytes changed: %#v", attachment)
		}
	}
	for _, includeData := range []bool{false, true} {
		messages, err := db.messages(ctx, conversation.ID, includeData)
		if err != nil || len(messages) != 1 || len(messages[0].Attachments) != 1 {
			t.Fatalf("message read: %#v, %v", messages, err)
		}
		var wantData []byte
		if includeData {
			wantData = content
		}
		assertAttachment(messages[0].Attachments[0], wantData)
	}
	page, _, err := db.MessagesPage(ctx, conversation.ID, 5, 0)
	if err != nil || len(page) != 1 {
		t.Fatalf("page read: %#v, %v", page, err)
	}
	assertAttachment(page[0].Attachments[0], nil)
	listed, err := db.ListTextAttachments(ctx, conversation.ID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("attachment list: %#v, %v", listed, err)
	}
	assertAttachment(listed[0], nil)
	for _, read := range []func() (Attachment, error){
		func() (Attachment, error) { return db.GetAttachment(ctx, message.Attachments[0].ID) },
		func() (Attachment, error) {
			return db.ReadTextAttachment(ctx, conversation.ID, message.Attachments[0].ID, 0, 0)
		},
	} {
		attachment, err := read()
		if err != nil {
			t.Fatal(err)
		}
		assertAttachment(attachment, content)
	}
	fork, err := db.ForkConversation(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	forkedMessages, err := db.MessagesWithAttachmentData(ctx, fork.ID)
	if err != nil || len(forkedMessages) != 1 || len(forkedMessages[0].Attachments) != 1 {
		t.Fatalf("forked attachment read: %#v, %v", forkedMessages, err)
	}
	assertAttachment(forkedMessages[0].Attachments[0], content)
	if forkedMessages[0].Attachments[0].ID == message.Attachments[0].ID {
		t.Fatal("fork did not copy the attachment record")
	}
}

func TestLegacyAttachmentsDefaultToNoWorkspacePath(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := db.CreateConversation(ctx, CreateConversationParams{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := db.AddMessageWithAttachments(ctx, conversation.ID, "user", "Read this", "complete", []NewAttachment{{
		FileName: "old.txt", MediaType: "text/plain", Data: []byte("legacy contents"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(ctx, "ALTER TABLE message_attachments DROP COLUMN workspace_path"); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	attachment, err := db.GetAttachment(ctx, message.Attachments[0].ID)
	if err != nil || attachment.WorkspacePath != "" || string(attachment.Data) != "legacy contents" {
		t.Fatalf("legacy attachment migration changed data: %#v, %v", attachment, err)
	}
}
