package agent

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"

	_ "golang.org/x/image/webp"
)

const (
	maxSubagentImages     = 4
	maxSubagentImageBytes = 10 << 20
	maxSubagentTotalBytes = 20 << 20
)

type loadedImage struct {
	Path string
	Data []byte
}

// loadImages reads image inputs for a delegated task. Relative paths are
// resolved from the agent workspace; absolute paths intentionally remain
// readable so workers can inspect the host/container view exposed to WebUI.
func loadImages(workspace string, paths []string) ([]loadedImage, error) {
	if len(paths) > maxSubagentImages {
		return nil, fmt.Errorf("delegate at most %d images per task", maxSubagentImages)
	}
	result := make([]loadedImage, 0, len(paths))
	total := 0
	for _, inputPath := range paths {
		if inputPath == "" {
			return nil, fmt.Errorf("image path is required")
		}
		resolved := inputPath
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(workspace, resolved)
		}
		resolved, err := filepath.Abs(resolved)
		if err != nil {
			return nil, fmt.Errorf("resolve image %q: %w", inputPath, err)
		}
		file, err := os.Open(resolved)
		if err != nil {
			return nil, fmt.Errorf("open image %q: %w", inputPath, err)
		}
		info, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return nil, fmt.Errorf("inspect image %q: %w", inputPath, statErr)
		}
		if !info.Mode().IsRegular() {
			file.Close()
			return nil, fmt.Errorf("image %q must be a regular file", inputPath)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxSubagentImageBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read image %q: %w", inputPath, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close image %q: %w", inputPath, closeErr)
		}
		if len(data) > maxSubagentImageBytes {
			return nil, fmt.Errorf("image %q exceeds 10 MiB", inputPath)
		}
		total += len(data)
		if total > maxSubagentTotalBytes {
			return nil, fmt.Errorf("delegated images may total at most 20 MiB")
		}
		mediaType := http.DetectContentType(data)
		if mediaType != "image/jpeg" && mediaType != "image/png" && mediaType != "image/webp" {
			return nil, fmt.Errorf("image %q must be JPEG, PNG, or WebP", inputPath)
		}
		if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
			return nil, fmt.Errorf("decode image %q: %w", inputPath, err)
		}
		result = append(result, loadedImage{Path: resolved, Data: data})
	}
	return result, nil
}
