package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"ollama-webui/internal/ollama"
)

func TestInputStagerLeavesSmallInputInline(t *testing.T) {
	workspace := t.TempDir()
	messages := []ollama.Message{{Role: "user", Content: "12345678"}}
	prepared, cleanup, err := (InputStager{Workspace: workspace, MaxInlineBytes: 8}).Stage(messages, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if prepared[0].Content != messages[0].Content {
		t.Fatalf("boundary input changed: %q", prepared[0].Content)
	}
	entries, err := os.ReadDir(workspace)
	if err != nil || len(entries) != 0 {
		t.Fatalf("small input created staging files: %#v, %v", entries, err)
	}
}

func TestInputStagerPreservesContentAndCleansOnlyRunDirectory(t *testing.T) {
	workspace := t.TempDir()
	sibling := filepath.Join(workspace, "keep.txt")
	if err := os.WriteFile(sibling, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := "  first line\nsecond line\n"
	messages := []ollama.Message{{Role: "user", Content: original, Images: [][]byte{{1, 2, 3}}}}
	prepared, cleanup, err := (InputStager{Workspace: workspace, MaxInlineBytes: 8}).Stage(messages, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prepared[0].Content == original || !strings.Contains(prepared[0].Content, "bounded line ranges") {
		t.Fatalf("large input was not replaced with a useful notice: %q", prepared[0].Content)
	}
	if len(prepared[0].Images) != 1 {
		t.Fatalf("staging lost images: %#v", prepared[0].Images)
	}
	match := regexp.MustCompile(`staged to "([^"]+)"`).FindStringSubmatch(prepared[0].Content)
	if len(match) != 2 {
		t.Fatalf("staged path missing from notice: %q", prepared[0].Content)
	}
	stagedPath := filepath.Join(workspace, filepath.FromSlash(match[1]))
	data, err := os.ReadFile(stagedPath)
	if err != nil || string(data) != original {
		t.Fatalf("staged content changed: %q, %v", data, err)
	}
	info, err := os.Stat(stagedPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("staged file permissions are not private: %#v, %v", info, err)
	}
	cleanup()
	if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
		t.Fatalf("staged file survived cleanup: %v", err)
	}
	if data, err := os.ReadFile(sibling); err != nil || string(data) != "keep" {
		t.Fatalf("cleanup touched a workspace sibling: %q, %v", data, err)
	}
}

func TestInputStagerAdaptsToSmallContextWindow(t *testing.T) {
	workspace := t.TempDir()
	contextWindow := 4096
	content := strings.Repeat("x", contextWindow*estimatedBytesPerToken+1)
	prepared, cleanup, err := (InputStager{Workspace: workspace, MaxInlineBytes: 16 << 10}).Stage(
		[]ollama.Message{{Role: "user", Content: content}}, &contextWindow,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if prepared[0].Content == content {
		t.Fatal("small context window did not lower the staging threshold")
	}
}
