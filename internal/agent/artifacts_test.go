package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ollama-webui/internal/store"
)

func TestArtifactManagerWriteReadListAndDelete(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	root := filepath.Join(t.TempDir(), "artifacts")
	manager, err := NewArtifactManager(root, database)
	if err != nil {
		t.Fatal(err)
	}

	content := []byte(strings.Repeat("0123456789", 1000))
	artifact, err := manager.Write(ctx, store.CreateContextArtifactParams{
		ConversationID: conversationID,
		Kind:           "tool_output", DisplayName: "command.txt", MediaType: "text/plain", Summary: "bounded output",
	}, content)
	if err != nil {
		t.Fatal(err)
	}
	if !artifactStorageKeyPattern.MatchString(artifact.StorageKey) {
		t.Fatalf("unexpected artifact storage key %q", artifact.StorageKey)
	}
	if artifact.SizeBytes != int64(len(content)) || len(artifact.SHA256) != 64 {
		t.Fatalf("incorrect artifact metadata: %#v", artifact)
	}

	conversationContext, err := database.EnsureConversationContext(ctx, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, conversationContext.StorageKey)
	assertPermissions(t, root, 0o700)
	assertPermissions(t, directory, 0o700)
	assertPermissions(t, filepath.Join(directory, artifact.StorageKey), 0o600)

	first, err := manager.Read(ctx, conversationID, artifact.StorageKey, 0, maxArtifactReadBytes)
	if err != nil {
		t.Fatal(err)
	}
	if first.EOF || first.NextOffset != maxArtifactReadBytes || string(first.Data) != string(content[:maxArtifactReadBytes]) {
		t.Fatalf("unexpected first page: offset=%d bytes=%d eof=%v", first.NextOffset, len(first.Data), first.EOF)
	}
	second, err := manager.Read(ctx, conversationID, artifact.StorageKey, first.NextOffset, maxArtifactReadBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !second.EOF || second.NextOffset != int64(len(content)) || string(second.Data) != string(content[maxArtifactReadBytes:]) {
		t.Fatalf("unexpected second page: offset=%d bytes=%d eof=%v", second.NextOffset, len(second.Data), second.EOF)
	}
	beyondEnd, err := manager.Read(ctx, conversationID, artifact.StorageKey, int64(len(content))+10, 1)
	if err != nil || !beyondEnd.EOF || len(beyondEnd.Data) != 0 {
		t.Fatalf("read beyond end = %#v, %v", beyondEnd, err)
	}
	if _, err := manager.Read(ctx, conversationID, artifact.StorageKey, 0, maxArtifactReadBytes+1); err == nil {
		t.Fatal("oversized read limit was accepted")
	}
	if _, err := manager.Read(ctx, conversationID, artifact.StorageKey, -1, 1); err == nil {
		t.Fatal("negative offset was accepted")
	}

	items, err := manager.List(ctx, conversationID)
	if err != nil || len(items) != 1 || items[0].StorageKey != artifact.StorageKey {
		t.Fatalf("listed artifacts = %#v, %v", items, err)
	}
	if err := manager.Delete(ctx, conversationID, artifact.StorageKey); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, artifact.StorageKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted artifact file still exists: %v", err)
	}
	items, err = manager.List(ctx, conversationID)
	if err != nil || len(items) != 0 {
		t.Fatalf("metadata was not deleted: %#v, %v", items, err)
	}
}

func TestArtifactManagerIsolatesConversations(t *testing.T) {
	ctx := context.Background()
	database, firstID := artifactTestStore(t, ctx)
	second, err := database.CreateConversation(ctx, store.CreateConversationParams{Title: "Second", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewArtifactManager(filepath.Join(t.TempDir(), "artifacts"), database)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := manager.Write(ctx, store.CreateContextArtifactParams{ConversationID: firstID, Kind: "note"}, []byte("private"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Read(ctx, second.ID, artifact.StorageKey, 0, 8); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-conversation read returned %v, want ErrNotFound", err)
	}
	if err := manager.Delete(ctx, second.ID, artifact.StorageKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-conversation delete returned %v, want ErrNotFound", err)
	}
	read, err := manager.Read(ctx, firstID, artifact.StorageKey, 0, 8)
	if err != nil || string(read.Data) != "private" {
		t.Fatalf("owner could not read artifact after rejected delete: %#v, %v", read, err)
	}
}

func TestArtifactManagerReadsUTF8AtCharacterBoundaries(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	manager, err := NewArtifactManager(filepath.Join(t.TempDir(), "artifacts"), database)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := manager.Write(ctx, store.CreateContextArtifactParams{ConversationID: conversationID}, []byte("甲乙丙"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := manager.Read(ctx, conversationID, artifact.StorageKey, 0, 4)
	if err != nil || string(page.Data) != "甲" || page.NextOffset != 3 || page.EOF {
		t.Fatalf("UTF-8 page was split: %#v, %v", page, err)
	}
	if _, err := manager.Read(ctx, conversationID, artifact.StorageKey, 1, 4); err == nil {
		t.Fatal("mid-character offset was accepted")
	}
	if _, err := manager.Write(ctx, store.CreateContextArtifactParams{ConversationID: conversationID}, []byte{0xff}); err == nil {
		t.Fatal("invalid UTF-8 text artifact was accepted")
	}
}

func TestArtifactManagerCleansFileWhenDatabaseCreateFails(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "artifacts")
	storageKey := strings.Repeat("a", 32)
	manager, err := NewArtifactManager(root, &failingArtifactStore{
		conversationContext: store.ConversationContext{ConversationID: 9, StorageKey: storageKey},
		createErr:           errors.New("database unavailable"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Write(ctx, store.CreateContextArtifactParams{ConversationID: 9}, []byte("must be removed")); err == nil {
		t.Fatal("database failure was not returned")
	}
	entries, err := os.ReadDir(filepath.Join(root, storageKey))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("database failure left files behind: %#v", entries)
	}
}

func TestArtifactManagerRejectsUnsafePaths(t *testing.T) {
	if _, err := NewArtifactManager("relative/path", &failingArtifactStore{}); err == nil {
		t.Fatal("relative artifact root was accepted")
	}
	temporary := t.TempDir()
	realRoot := filepath.Join(temporary, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkRoot := filepath.Join(temporary, "link")
	if err := os.Symlink(realRoot, symlinkRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := NewArtifactManager(symlinkRoot, &failingArtifactStore{}); err == nil {
		t.Fatal("symbolic-link artifact root was accepted")
	}

	root := filepath.Join(temporary, "artifacts")
	manager, err := NewArtifactManager(root, &failingArtifactStore{
		conversationContext: store.ConversationContext{ConversationID: 1, StorageKey: "../../escape"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Write(context.Background(), store.CreateContextArtifactParams{ConversationID: 1}, []byte("unsafe")); err == nil {
		t.Fatal("unsafe conversation key was accepted")
	}
	for _, key := range []string{"../outside", strings.Repeat("A", 32), strings.Repeat("0", 31), strings.Repeat("0", 33)} {
		if err := manager.RemoveConversation(key); err == nil {
			t.Fatalf("unsafe removal key %q was accepted", key)
		}
	}
	outside := filepath.Join(temporary, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	validKey := strings.Repeat("b", 32)
	if err := os.Symlink(outside, filepath.Join(root, validKey)); err != nil {
		t.Fatal(err)
	}
	if err := manager.RemoveConversation(validKey); err == nil {
		t.Fatal("symbolic-link conversation directory was accepted")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory was affected: %v", err)
	}
}

func TestArtifactManagerRejectsSymlinkArtifact(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	root := filepath.Join(t.TempDir(), "artifacts")
	manager, err := NewArtifactManager(root, database)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := manager.Write(ctx, store.CreateContextArtifactParams{ConversationID: conversationID}, []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	conversationContext, _ := database.EnsureConversationContext(ctx, conversationID)
	path := filepath.Join(root, conversationContext.StorageKey, artifact.StorageKey)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Read(ctx, conversationID, artifact.StorageKey, 0, 8); err == nil {
		t.Fatal("symbolic-link artifact was read")
	}
}

func TestArtifactManagerRejectsSameSizeCorruption(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	root := filepath.Join(t.TempDir(), "artifacts")
	manager, err := NewArtifactManager(root, database)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := manager.Write(ctx, store.CreateContextArtifactParams{ConversationID: conversationID}, []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	conversationContext, _ := database.GetConversationContext(ctx, conversationID)
	path := filepath.Join(root, conversationContext.StorageKey, artifact.StorageKey)
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Read(ctx, conversationID, artifact.StorageKey, 0, 8); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("same-size corruption was not detected: %v", err)
	}
}

func TestArtifactManagerRemoveConversationRemovesOnlyDirectDirectory(t *testing.T) {
	key := strings.Repeat("c", 32)
	root := filepath.Join(t.TempDir(), "artifacts")
	manager, err := NewArtifactManager(root, &failingArtifactStore{})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, key)
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "content"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.RemoveConversation(key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("conversation directory still exists: %v", err)
	}
}

func TestArtifactManagerReconcileRemovesOnlyManagedOrphansAndTemporaryFiles(t *testing.T) {
	ctx := context.Background()
	database, conversationID := artifactTestStore(t, ctx)
	root := filepath.Join(t.TempDir(), "artifacts")
	manager, err := NewArtifactManager(root, database)
	if err != nil {
		t.Fatal(err)
	}
	live, err := database.EnsureConversationContext(ctx, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	liveDirectory := filepath.Join(root, live.StorageKey)
	if err := os.Mkdir(liveDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(liveDirectory, ".artifact-interrupted")
	if err := os.WriteFile(temporary, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	unindexed := filepath.Join(liveDirectory, strings.Repeat("e", 32))
	if err := os.WriteFile(unindexed, []byte("not indexed"), 0o600); err != nil {
		t.Fatal(err)
	}
	orphanKey := strings.Repeat("d", 32)
	orphanDirectory := filepath.Join(root, orphanKey)
	if err := os.Mkdir(orphanDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(root, "keep-me")
	if err := os.Mkdir(unknown, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphanDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan directory survived: %v", err)
	}
	if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file survived: %v", err)
	}
	if _, err := os.Stat(unindexed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unindexed artifact survived: %v", err)
	}
	if _, err := os.Stat(liveDirectory); err != nil {
		t.Fatalf("live directory was removed: %v", err)
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatalf("unknown directory was removed: %v", err)
	}
}

func artifactTestStore(t *testing.T, ctx context.Context) (*store.Store, int64) {
	t.Helper()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	conversation, err := database.CreateConversation(ctx, store.CreateConversationParams{Title: "Artifacts", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return database, conversation.ID
}

func assertPermissions(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("permissions for %s = %o, want %o", path, got, want)
	}
}

type failingArtifactStore struct {
	conversationContext store.ConversationContext
	createErr           error
}

func (fake *failingArtifactStore) EnsureConversationContext(context.Context, int64) (store.ConversationContext, error) {
	return fake.conversationContext, nil
}

func (fake *failingArtifactStore) ListConversationContexts(context.Context) ([]store.ConversationContext, error) {
	if fake.conversationContext.StorageKey == "" {
		return nil, nil
	}
	return []store.ConversationContext{fake.conversationContext}, nil
}

func (fake *failingArtifactStore) CreateContextArtifact(context.Context, store.CreateContextArtifactParams) (store.ContextArtifact, error) {
	return store.ContextArtifact{}, fake.createErr
}

func (fake *failingArtifactStore) GetContextArtifactByStorageKey(context.Context, int64, string) (store.ContextArtifact, error) {
	return store.ContextArtifact{}, store.ErrNotFound
}

func (fake *failingArtifactStore) ListContextArtifacts(context.Context, int64) ([]store.ContextArtifact, error) {
	return nil, nil
}

func (fake *failingArtifactStore) DeleteContextArtifact(context.Context, int64, int64) error {
	return nil
}
