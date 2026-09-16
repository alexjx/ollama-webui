package agent

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

const onePixelPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestLoadImagesSupportsWorkspaceRelativeAndExternalAbsolutePaths(t *testing.T) {
	workspace := t.TempDir()
	external := t.TempDir()
	data, err := base64.StdEncoding.DecodeString(onePixelPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(workspace, "inside.png")
	externalPath := filepath.Join(external, "outside.png")
	if err := os.WriteFile(workspacePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(externalPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	images, err := loadImages(workspace, []string{"inside.png", externalPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || images[0].Path != workspacePath || images[1].Path != externalPath {
		t.Fatalf("unexpected loaded images: %#v", images)
	}
}

func TestLoadImagesRejectsInvalidOrOversizedBatches(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "text.png"), []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadImages(workspace, []string{"text.png"}); err == nil {
		t.Fatal("expected invalid image to fail")
	}
	if _, err := loadImages(workspace, []string{"a", "b", "c", "d", "e"}); err == nil {
		t.Fatal("expected oversized image batch to fail")
	}
}
