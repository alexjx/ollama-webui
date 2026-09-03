package agent

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ollama-webui/internal/ollama"
)

const (
	defaultInlineInputBytes = 16 << 10
	minimumInlineInputBytes = 4 << 10
)

// InputStager keeps oversized user input out of the repeated model request while
// leaving the complete text available to the agent through its shell tool.
type InputStager struct {
	Workspace      string
	MaxInlineBytes int
}

func (stager InputStager) Stage(messages []ollama.Message, contextWindow *int) ([]ollama.Message, func(), error) {
	prepared := append([]ollama.Message(nil), messages...)
	limit := stager.inlineLimit(contextWindow)
	if stager.Workspace == "" || limit <= 0 {
		return prepared, func() {}, nil
	}

	var runDirectory string
	staged := 0
	cleanup := func() {
		if runDirectory != "" {
			_ = os.RemoveAll(runDirectory)
		}
	}
	for index := range prepared {
		if prepared[index].Role != "user" || len(prepared[index].Content) <= limit {
			continue
		}
		if runDirectory == "" {
			var err error
			runDirectory, err = os.MkdirTemp(stager.Workspace, ".ollama-webui-run-")
			if err != nil {
				return nil, cleanup, fmt.Errorf("create staged-input directory: %w", err)
			}
			if err := os.Chmod(runDirectory, 0o700); err != nil {
				cleanup()
				return nil, func() {}, fmt.Errorf("secure staged-input directory: %w", err)
			}
		}
		staged++
		fileName := fmt.Sprintf("user-input-%03d.txt", staged)
		absolutePath := filepath.Join(runDirectory, fileName)
		if err := os.WriteFile(absolutePath, []byte(prepared[index].Content), 0o600); err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("stage user input: %w", err)
		}
		relativePath, err := filepath.Rel(stager.Workspace, absolutePath)
		if err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("resolve staged-input path: %w", err)
		}
		prepared[index].Content = stagedInputNotice(filepath.ToSlash(relativePath), prepared[index].Content)
	}
	return prepared, cleanup, nil
}

func (stager InputStager) inlineLimit(contextWindow *int) int {
	limit := stager.MaxInlineBytes
	if limit <= 0 {
		limit = defaultInlineInputBytes
	}
	if contextWindow != nil && *contextWindow > 0 {
		contextLimit := *contextWindow
		if contextLimit < minimumInlineInputBytes {
			contextLimit = minimumInlineInputBytes
		}
		if contextLimit < limit {
			limit = contextLimit
		}
	}
	return limit
}

func stagedInputNotice(path, content string) string {
	lines := 1
	if content != "" {
		lines += strings.Count(content, "\n")
	}
	digest := sha256.Sum256([]byte(content))
	return fmt.Sprintf(`[The complete user input was staged to %q to preserve model context. It contains %d bytes across %d lines (SHA-256 %x). Treat that file as the user's request. Inspect only the portions needed, using bounded line ranges such as sed -n '1,120p' -- %q, and continue in further ranges. Do not print the entire file into one tool result.]`, path, len(content), lines, digest, path)
}
