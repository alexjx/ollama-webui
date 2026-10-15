package agent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CopyConversation copies indexed artifacts into an independent directory.
// It deliberately performs no store calls, so it can run inside a transaction.
func (manager *ArtifactManager) CopyConversation(sourceKey, destinationKey string, keys map[string]string) (err error) {
	if sourceKey == destinationKey {
		return errors.New("fork artifact destination must differ from source")
	}
	if err := validateStorageKey(destinationKey); err != nil {
		return err
	}
	source, err := manager.conversationDirectory(sourceKey, false)
	if err != nil {
		return err
	}
	destination := filepath.Join(manager.root, destinationKey)
	// Never merge into an existing directory or follow a destination symlink.
	if err := os.Mkdir(destination, 0o700); err != nil {
		return fmt.Errorf("create fork artifact directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = manager.RemoveConversation(destinationKey)
		}
	}()
	for sourceArtifact, destinationArtifact := range keys {
		if err := validateStorageKey(sourceArtifact); err != nil {
			return err
		}
		if err := validateStorageKey(destinationArtifact); err != nil {
			return err
		}
		if err := copyArtifact(filepath.Join(source, sourceArtifact), filepath.Join(destination, destinationArtifact)); err != nil {
			return err
		}
	}
	if err := syncDirectory(destination); err != nil {
		return err
	}
	return syncDirectory(manager.root)
}

func copyArtifact(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect fork source artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("fork source artifact must be a regular file")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("copy fork artifact: %w", err)
	}
	if err := output.Sync(); err != nil {
		return err
	}
	return output.Close()
}
