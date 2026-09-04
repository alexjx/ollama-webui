package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"ollama-webui/internal/store"
)

const maxArtifactReadBytes int64 = 8 << 10

var artifactStorageKeyPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ArtifactStore is the persistence surface required by ArtifactManager.
// Keeping it small also makes filesystem failure handling independently testable.
type ArtifactStore interface {
	EnsureConversationContext(context.Context, int64) (store.ConversationContext, error)
	ListConversationContexts(context.Context) ([]store.ConversationContext, error)
	CreateContextArtifact(context.Context, store.CreateContextArtifactParams) (store.ContextArtifact, error)
	GetContextArtifactByStorageKey(context.Context, int64, string) (store.ContextArtifact, error)
	ListContextArtifacts(context.Context, int64) ([]store.ContextArtifact, error)
	DeleteContextArtifact(context.Context, int64, int64) error
}

// ArtifactManager stores large agent context outside the database while keeping
// ownership and metadata in the database.
type ArtifactManager struct {
	root  string
	store ArtifactStore
}

// ArtifactRead is one bounded page of an artifact.
type ArtifactRead struct {
	Artifact   store.ContextArtifact `json:"artifact"`
	Data       []byte                `json:"data"`
	Offset     int64                 `json:"offset"`
	NextOffset int64                 `json:"next_offset"`
	EOF        bool                  `json:"eof"`
}

func NewArtifactManager(root string, artifactStore ArtifactStore) (*ArtifactManager, error) {
	if artifactStore == nil {
		return nil, errors.New("artifact store is required")
	}
	if root == "" || !filepath.IsAbs(root) {
		return nil, errors.New("artifact root must be an absolute path")
	}
	root = filepath.Clean(root)
	if root == string(filepath.Separator) {
		return nil, errors.New("artifact root must not be the filesystem root")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create artifact root: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect artifact root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("artifact root must be a directory, not a symbolic link")
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("secure artifact root: %w", err)
	}
	return &ArtifactManager{root: root, store: artifactStore}, nil
}

// Write atomically persists data before publishing its metadata. If the
// database write fails, the file is removed so no unindexed artifact remains.
func (manager *ArtifactManager) Write(ctx context.Context, params store.CreateContextArtifactParams, data []byte) (store.ContextArtifact, error) {
	if params.MediaType == "" {
		params.MediaType = "text/plain"
	}
	if strings.HasPrefix(params.MediaType, "text/") && !utf8.Valid(data) {
		return store.ContextArtifact{}, errors.New("text artifact must contain valid UTF-8")
	}
	conversationContext, err := manager.store.EnsureConversationContext(ctx, params.ConversationID)
	if err != nil {
		return store.ContextArtifact{}, err
	}
	if conversationContext.ConversationID != params.ConversationID {
		return store.ContextArtifact{}, errors.New("conversation context does not match request")
	}
	directory, err := manager.conversationDirectory(conversationContext.StorageKey, true)
	if err != nil {
		return store.ContextArtifact{}, err
	}
	storageKey, err := newArtifactStorageKey()
	if err != nil {
		return store.ContextArtifact{}, err
	}
	finalPath := filepath.Join(directory, storageKey)
	temporary, err := os.CreateTemp(directory, ".artifact-*")
	if err != nil {
		return store.ContextArtifact{}, fmt.Errorf("create temporary artifact: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanupTemporary := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		cleanupTemporary()
		return store.ContextArtifact{}, fmt.Errorf("secure temporary artifact: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		cleanupTemporary()
		return store.ContextArtifact{}, fmt.Errorf("write artifact: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		cleanupTemporary()
		return store.ContextArtifact{}, fmt.Errorf("sync artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return store.ContextArtifact{}, fmt.Errorf("close artifact: %w", err)
	}
	if _, err := os.Lstat(finalPath); err == nil {
		_ = os.Remove(temporaryPath)
		return store.ContextArtifact{}, errors.New("generated artifact key already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(temporaryPath)
		return store.ContextArtifact{}, fmt.Errorf("inspect artifact destination: %w", err)
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		_ = os.Remove(temporaryPath)
		return store.ContextArtifact{}, fmt.Errorf("publish artifact: %w", err)
	}
	if err := syncDirectory(directory); err != nil {
		_ = os.Remove(finalPath)
		return store.ContextArtifact{}, err
	}

	digest := sha256.Sum256(data)
	params.StorageKey = storageKey
	params.SizeBytes = int64(len(data))
	params.SHA256 = hex.EncodeToString(digest[:])
	artifact, err := manager.store.CreateContextArtifact(ctx, params)
	if err != nil {
		_ = os.Remove(finalPath)
		_ = syncDirectory(directory)
		return store.ContextArtifact{}, err
	}
	if artifact.ConversationID != params.ConversationID || artifact.StorageKey != storageKey {
		_ = manager.store.DeleteContextArtifact(ctx, params.ConversationID, artifact.ID)
		_ = os.Remove(finalPath)
		return store.ContextArtifact{}, errors.New("artifact metadata does not match stored file")
	}
	return artifact, nil
}

// Read returns at most 8192 bytes and always resolves metadata through both the
// conversation ID and artifact key, preventing cross-conversation access.
func (manager *ArtifactManager) Read(ctx context.Context, conversationID int64, storageKey string, offset, limit int64) (ArtifactRead, error) {
	if offset < 0 {
		return ArtifactRead{}, errors.New("artifact offset must not be negative")
	}
	if limit <= 0 || limit > maxArtifactReadBytes {
		return ArtifactRead{}, fmt.Errorf("artifact limit must be between 1 and %d bytes", maxArtifactReadBytes)
	}
	if err := validateStorageKey(storageKey); err != nil {
		return ArtifactRead{}, err
	}
	artifact, err := manager.store.GetContextArtifactByStorageKey(ctx, conversationID, storageKey)
	if err != nil {
		return ArtifactRead{}, err
	}
	if artifact.ConversationID != conversationID || artifact.StorageKey != storageKey {
		return ArtifactRead{}, errors.New("artifact metadata does not match request")
	}
	conversationContext, err := manager.store.EnsureConversationContext(ctx, conversationID)
	if err != nil {
		return ArtifactRead{}, err
	}
	if conversationContext.ConversationID != conversationID {
		return ArtifactRead{}, errors.New("conversation context does not match request")
	}
	directory, err := manager.conversationDirectory(conversationContext.StorageKey, false)
	if err != nil {
		return ArtifactRead{}, err
	}
	path := filepath.Join(directory, storageKey)
	info, err := os.Lstat(path)
	if err != nil {
		return ArtifactRead{}, fmt.Errorf("inspect artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ArtifactRead{}, errors.New("artifact must be a regular file")
	}
	if info.Size() != artifact.SizeBytes {
		return ArtifactRead{}, fmt.Errorf("artifact size mismatch: file has %d bytes, metadata records %d", info.Size(), artifact.SizeBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return ArtifactRead{}, fmt.Errorf("open artifact: %w", err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return ArtifactRead{}, fmt.Errorf("verify artifact: %w", err)
	}
	if actual := hex.EncodeToString(hasher.Sum(nil)); actual != artifact.SHA256 {
		return ArtifactRead{}, errors.New("artifact checksum mismatch")
	}
	data := make([]byte, limit)
	read, readErr := file.ReadAt(data, offset)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return ArtifactRead{}, fmt.Errorf("read artifact: %w", readErr)
	}
	data = data[:read]
	if len(data) > 0 && offset > 0 && data[0]&0xc0 == 0x80 {
		return ArtifactRead{}, errors.New("artifact offset must be on a UTF-8 character boundary")
	}
	if readErr == nil {
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
		if len(data) == 0 && read > 0 {
			return ArtifactRead{}, errors.New("artifact limit is too small for the next UTF-8 character")
		}
	} else if !utf8.Valid(data) {
		return ArtifactRead{}, errors.New("artifact contains invalid UTF-8")
	}
	nextOffset := offset + int64(read)
	if len(data) != read {
		nextOffset = offset + int64(len(data))
	}
	return ArtifactRead{
		Artifact: artifact, Data: data, Offset: offset, NextOffset: nextOffset, EOF: nextOffset >= info.Size(),
	}, nil
}

func (manager *ArtifactManager) List(ctx context.Context, conversationID int64) ([]store.ContextArtifact, error) {
	return manager.store.ListContextArtifacts(ctx, conversationID)
}

func (manager *ArtifactManager) Delete(ctx context.Context, conversationID int64, storageKey string) error {
	if err := validateStorageKey(storageKey); err != nil {
		return err
	}
	artifact, err := manager.store.GetContextArtifactByStorageKey(ctx, conversationID, storageKey)
	if err != nil {
		return err
	}
	conversationContext, err := manager.store.EnsureConversationContext(ctx, conversationID)
	if err != nil {
		return err
	}
	if conversationContext.ConversationID != conversationID {
		return errors.New("conversation context does not match request")
	}
	directory, err := manager.conversationDirectory(conversationContext.StorageKey, false)
	if err != nil {
		return err
	}
	if err := manager.store.DeleteContextArtifact(ctx, conversationID, artifact.ID); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(directory, storageKey)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove artifact: %w", err)
	}
	return syncDirectory(directory)
}

// RemoveConversation removes only a valid, direct child directory of the root.
func (manager *ArtifactManager) RemoveConversation(storageKey string) error {
	directory, err := manager.conversationDirectory(storageKey, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("remove conversation artifacts: %w", err)
	}
	return syncDirectory(manager.root)
}

// Reconcile removes only orphaned managed directories and interrupted temporary
// files. Unknown names and symbolic links are left untouched for inspection.
func (manager *ArtifactManager) Reconcile(ctx context.Context) error {
	contexts, err := manager.store.ListConversationContexts(ctx)
	if err != nil {
		return err
	}
	live := make(map[string]store.ConversationContext, len(contexts))
	for _, item := range contexts {
		live[item.StorageKey] = item
	}
	entries, err := os.ReadDir(manager.root)
	if err != nil {
		return fmt.Errorf("list artifact root: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !artifactStorageKeyPattern.MatchString(name) || entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		conversationContext, ok := live[name]
		if !ok {
			if err := manager.RemoveConversation(name); err != nil {
				return err
			}
			continue
		}
		directory, err := manager.conversationDirectory(name, false)
		if err != nil {
			return err
		}
		children, err := os.ReadDir(directory)
		if err != nil {
			return fmt.Errorf("list conversation artifacts: %w", err)
		}
		artifacts, err := manager.store.ListContextArtifacts(ctx, conversationContext.ConversationID)
		if err != nil {
			return err
		}
		expected := make(map[string]store.ContextArtifact, len(artifacts))
		for _, artifact := range artifacts {
			expected[artifact.StorageKey] = artifact
		}
		seen := make(map[string]struct{}, len(artifacts))
		for _, child := range children {
			if child.Type().IsRegular() && strings.HasPrefix(child.Name(), ".artifact-") {
				if err := os.Remove(filepath.Join(directory, child.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("remove interrupted artifact: %w", err)
				}
				continue
			}
			artifact, indexed := expected[child.Name()]
			if !artifactStorageKeyPattern.MatchString(child.Name()) || !child.Type().IsRegular() {
				continue
			}
			path := filepath.Join(directory, child.Name())
			if !indexed {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("remove unindexed artifact: %w", err)
				}
				continue
			}
			info, err := child.Info()
			if err != nil {
				return fmt.Errorf("inspect indexed artifact: %w", err)
			}
			if info.Size() != artifact.SizeBytes {
				if err := manager.store.DeleteContextArtifact(ctx, conversationContext.ConversationID, artifact.ID); err != nil {
					return err
				}
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("remove corrupt artifact: %w", err)
				}
				continue
			}
			seen[child.Name()] = struct{}{}
		}
		for key, artifact := range expected {
			if _, ok := seen[key]; !ok {
				if err := manager.store.DeleteContextArtifact(ctx, conversationContext.ConversationID, artifact.ID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (manager *ArtifactManager) conversationDirectory(storageKey string, create bool) (string, error) {
	if err := validateStorageKey(storageKey); err != nil {
		return "", fmt.Errorf("invalid conversation storage key: %w", err)
	}
	directory := filepath.Join(manager.root, storageKey)
	if filepath.Dir(directory) != manager.root {
		return "", errors.New("conversation artifact path escapes root")
	}
	if create {
		created := false
		if err := os.Mkdir(directory, 0o700); err == nil {
			created = true
		} else if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create conversation artifact directory: %w", err)
		}
		if created {
			if err := syncDirectory(manager.root); err != nil {
				return "", err
			}
		}
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", fmt.Errorf("inspect conversation artifact directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("conversation artifact path must be a directory, not a symbolic link")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", fmt.Errorf("secure conversation artifact directory: %w", err)
	}
	return directory, nil
}

func validateStorageKey(storageKey string) error {
	if !artifactStorageKeyPattern.MatchString(storageKey) {
		return errors.New("storage key must be exactly 32 lowercase hexadecimal characters")
	}
	return nil
}

func newArtifactStorageKey() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate artifact storage key: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open artifact directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync artifact directory: %w", err)
	}
	return nil
}
