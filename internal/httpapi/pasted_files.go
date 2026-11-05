package httpapi

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"

	"ollama-webui/internal/store"
)

// Persist explicit text attachments before accepting the message. Names never
// depend on a user-supplied filename, and publication cannot replace a file.
func (s *Server) persistPastedFiles(conversationID int64, attachments []store.NewAttachment) error {
	root, err := os.OpenRoot(s.runtime.Workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := createWorkspaceFolder(root, "pasted"); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	for i := range attachments {
		attachment := &attachments[i]
		if attachment.MediaType != "text/plain" {
			continue
		}
		name := fmt.Sprintf("pasted/conversation-%d-%s.txt", conversationID, rand.Text())
		if err := uploadWorkspaceFile(root, name, bytes.NewReader(attachment.Data)); err != nil {
			s.removePastedFiles(attachments)
			return err
		}
		attachment.WorkspacePath = name
	}
	return nil
}

// Only used before the message commits; accepted files belong to the shared
// workspace and survive conversation deletion, like other workspace files.
func (s *Server) removePastedFiles(attachments []store.NewAttachment) {
	root, err := os.OpenRoot(s.runtime.Workspace)
	if err != nil {
		s.logger.Warn("open workspace to clean up unaccepted pasted files", "error", err)
		return
	}
	defer root.Close()
	for _, attachment := range attachments {
		if attachment.WorkspacePath != "" {
			if err := root.Remove(attachment.WorkspacePath); err != nil {
				s.logger.Warn("remove unaccepted pasted file", "path", attachment.WorkspacePath, "error", err)
			}
		}
	}
}

func hasTextAttachments(history []store.Message) bool {
	for _, message := range history {
		for _, attachment := range message.Attachments {
			if attachment.MediaType == "text/plain" {
				return true
			}
		}
	}
	return false
}
