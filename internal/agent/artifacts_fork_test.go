package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"ollama-webui/internal/store"
)

func TestCopyConversationRejectsUnsafeFilesAndKeepsExistingDestination(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := filepath.Join(t.TempDir(), "artifacts")
	manager, err := NewArtifactManager(root, db)
	if err != nil {
		t.Fatal(err)
	}
	source := "11111111111111111111111111111111"
	destination := "22222222222222222222222222222222"
	key := "33333333333333333333333333333333"
	if err := os.Mkdir(filepath.Join(root, source), 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, source, key)); err != nil {
		t.Fatal(err)
	}
	if err := manager.CopyConversation(source, destination, map[string]string{key: key}); err == nil {
		t.Fatal("copied symbolic link")
	}
	if _, err := os.Stat(filepath.Join(root, destination)); !os.IsNotExist(err) {
		t.Fatalf("failed copy left directory: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, destination), 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, destination, "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.CopyConversation(source, destination, map[string]string{key: key}); err == nil {
		t.Fatal("accepted existing destination")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("existing directory damaged: %v", err)
	}
	if err := manager.CopyConversation(source, "../escape", map[string]string{key: key}); err == nil {
		t.Fatal("accepted escaping key")
	}
}
