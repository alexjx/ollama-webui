package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

const defaultImageBatchLimit = 1000

type ImageBatchRequest struct {
	ConversationID      int64
	RequestingMessageID int64
	Model               string
	SupportsVision      bool
	Directory           string
	Instructions        string
	OutputPath          string
	Recursive           bool
	MaxImages           int
}

type ImageBatchExecutor interface {
	LabelImageDirectory(context.Context, ImageBatchRequest, func(Event) error) (SubagentResult, error)
}

type imageLabel struct {
	Path        string   `json:"path"`
	Labels      []string `json:"labels"`
	Confidence  float64  `json:"confidence"`
	NeedsReview bool     `json:"needs_review"`
	Notes       string   `json:"notes"`
	Error       string   `json:"error,omitempty"`
}

type modelImageLabel struct {
	Labels      []string `json:"labels"`
	Confidence  *float64 `json:"confidence"`
	NeedsReview *bool    `json:"needs_review"`
	Notes       string   `json:"notes"`
}

func (orchestrator *SubagentOrchestrator) LabelImageDirectory(ctx context.Context, request ImageBatchRequest, emit func(Event) error) (SubagentResult, error) {
	started := time.Now()
	request.Directory = strings.TrimSpace(request.Directory)
	request.Instructions = strings.TrimSpace(request.Instructions)
	request.OutputPath = strings.TrimSpace(request.OutputPath)
	if request.Directory == "" || request.Instructions == "" || request.OutputPath == "" {
		return SubagentResult{ExitCode: -1}, errors.New("directory, instructions, and output_path are required")
	}
	if !request.SupportsVision {
		return SubagentResult{ExitCode: -1}, errors.New("selected model does not support image input")
	}
	if len(request.Instructions) > maxDelegatedTaskBytes {
		return SubagentResult{ExitCode: -1}, errors.New("image labeling instructions exceed 16 KiB")
	}
	if request.MaxImages == 0 {
		request.MaxImages = defaultImageBatchLimit
	}
	if request.MaxImages < 1 || request.MaxImages > 10000 {
		return SubagentResult{ExitCode: -1}, errors.New("max_images must be between 1 and 10000")
	}
	model := strings.TrimSpace(request.Model)
	if model == "" {
		model = orchestrator.Model
	}
	if model == "" {
		return SubagentResult{ExitCode: -1}, errors.New("subagent model is required")
	}
	directory := request.Directory
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(orchestrator.Workspace, directory)
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return SubagentResult{ExitCode: -1}, fmt.Errorf("resolve image directory: %w", err)
	}
	paths, err := listImagePaths(directory, request.Recursive, request.MaxImages)
	if err != nil {
		return SubagentResult{ExitCode: -1}, err
	}
	outputPath, err := prepareWorkspaceOutput(orchestrator.Workspace, request.OutputPath)
	if err != nil {
		return SubagentResult{ExitCode: -1}, err
	}

	task := fmt.Sprintf("Label %d images from %s", len(paths), directory)
	job, err := orchestrator.Store.CreateAgentJob(ctx, store.CreateAgentJobParams{
		ConversationID: request.ConversationID, RequestingMessageID: request.RequestingMessageID, Task: task,
	})
	if err != nil {
		return SubagentResult{ExitCode: -1}, err
	}
	run, err := orchestrator.Store.CreateAgentRun(ctx, store.CreateAgentRunParams{
		JobID: job.ID, Task: task, Model: model, ContextWindow: orchestrator.ContextTokens, Depth: 0,
	})
	if err != nil {
		_, _ = orchestrator.Store.FailAgentJob(context.Background(), job.ID, err.Error())
		return SubagentResult{ExitCode: -1}, err
	}
	select {
	case orchestrator.semaphore <- struct{}{}:
		defer func() { <-orchestrator.semaphore }()
	case <-ctx.Done():
		orchestrator.cancel(job.ID, run.ID, ctx.Err())
		return SubagentResult{ExitCode: -1, Duration: time.Since(started)}, ctx.Err()
	}
	if _, err = orchestrator.Store.StartAgentJob(ctx, job.ID); err != nil {
		orchestrator.cancel(job.ID, run.ID, err)
		return SubagentResult{ExitCode: -1, Duration: time.Since(started)}, err
	}
	run, err = orchestrator.Store.StartAgentRun(ctx, run.ID)
	if err != nil {
		_, _ = orchestrator.Store.FailAgentJob(context.Background(), job.ID, err.Error())
		return SubagentResult{ExitCode: -1, Duration: time.Since(started)}, err
	}
	if err := emit(Event{Type: "subagent.started", Run: &run}); err != nil {
		orchestrator.cancel(job.ID, run.ID, err)
		return SubagentResult{ExitCode: -1, Duration: time.Since(started)}, err
	}

	temporary, err := os.CreateTemp(filepath.Dir(outputPath), ".labels-*.jsonl")
	if err != nil {
		return orchestrator.failSubagent(job.ID, run.ID, started, err, emit)
	}
	temporaryPath := temporary.Name()
	keepTemporary := false
	defer func() {
		_ = temporary.Close()
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	writer := bufio.NewWriter(temporary)
	failed, review := 0, 0
	for index, path := range paths {
		if err := ctx.Err(); err != nil {
			cancelled := orchestrator.cancel(job.ID, run.ID, err)
			_ = emit(Event{Type: "subagent.cancelled", Run: &cancelled})
			return SubagentResult{Feedback: err.Error(), ExitCode: -1, Duration: time.Since(started)}, err
		}
		label := orchestrator.labelOneImage(ctx, model, directory, path, request.Instructions)
		if label.Error != "" {
			failed++
		}
		if label.NeedsReview {
			review++
		}
		encoded, _ := json.Marshal(label)
		if _, err := writer.Write(append(encoded, '\n')); err != nil {
			return orchestrator.failSubagent(job.ID, run.ID, started, err, emit)
		}
		if (index+1)%10 == 0 || index+1 == len(paths) {
			progress, _ := json.Marshal(map[string]int{"processed": index + 1, "total": len(paths), "failed": failed, "needs_review": review})
			if err := emit(Event{Type: "subagent.progress", Content: string(progress), Run: &run}); err != nil {
				return orchestrator.failSubagent(job.ID, run.ID, started, err, emit)
			}
			_ = orchestrator.Store.TouchAgentRun(ctx, run.ID)
			_ = orchestrator.Store.TouchAgentJob(ctx, job.ID)
		}
	}
	if err := writer.Flush(); err != nil {
		return orchestrator.failSubagent(job.ID, run.ID, started, err, emit)
	}
	if err := temporary.Sync(); err != nil {
		return orchestrator.failSubagent(job.ID, run.ID, started, err, emit)
	}
	if err := temporary.Close(); err != nil {
		return orchestrator.failSubagent(job.ID, run.ID, started, err, emit)
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		return orchestrator.failSubagent(job.ID, run.ID, started, err, emit)
	}
	keepTemporary = true
	relativeOutput, _ := filepath.Rel(orchestrator.Workspace, outputPath)
	outputs := []string{filepath.ToSlash(relativeOutput)}
	summary := fmt.Sprintf("Labeled %d images; %d failed validation and %d need review.", len(paths), failed, review)
	persistCtx, cancelPersist := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelPersist()
	run, err = orchestrator.Store.CompleteAgentRun(persistCtx, run.ID, summary, outputs)
	if err != nil {
		return orchestrator.failSubagent(job.ID, run.ID, started, err, emit)
	}
	if _, err = orchestrator.Store.CompleteAgentJob(persistCtx, job.ID, summary, outputs); err != nil {
		_ = emit(Event{Type: "subagent.done", Run: &run})
		return SubagentResult{Feedback: err.Error(), ExitCode: -1, Duration: time.Since(started)}, err
	}
	if err := emit(Event{Type: "subagent.done", Run: &run}); err != nil {
		return SubagentResult{Feedback: err.Error(), ExitCode: -1, Duration: time.Since(started)}, err
	}
	payload, _ := json.Marshal(map[string]any{
		"status": "complete", "processed": len(paths), "failed": failed,
		"needs_review": review, "output_path": outputs[0], "run_id": run.ID,
	})
	return SubagentResult{Feedback: string(payload), ExitCode: 0, Duration: time.Since(started)}, nil
}

func listImagePaths(directory string, recursive bool, limit int) ([]string, error) {
	info, err := os.Stat(directory)
	if err != nil {
		return nil, fmt.Errorf("inspect image directory: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("image directory must be a directory")
	}
	paths := make([]string, 0)
	err = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != directory && entry.IsDir() && !recursive {
			return filepath.SkipDir
		}
		if entry.Type().IsRegular() && supportedImageExtension(path) {
			paths = append(paths, path)
			if len(paths) > limit {
				return fmt.Errorf("image directory exceeds max_images=%d", limit)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list image directory: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}

func supportedImageExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	default:
		return false
	}
}

func prepareWorkspaceOutput(workspace, output string) (string, error) {
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	resolved := output
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(workspace, resolved)
	}
	resolved = filepath.Clean(resolved)
	relative, err := filepath.Rel(workspace, resolved)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("output_path must name a file beneath AGENT_WORKSPACE")
	}
	current := workspace
	parts := strings.Split(filepath.Dir(relative), string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return "", fmt.Errorf("create output directory: %w", err)
			}
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect output directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", errors.New("output_path parent must contain only real directories")
		}
	}
	if info, err := os.Lstat(resolved); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return "", errors.New("output_path must be a regular file, not a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect output path: %w", err)
	}
	return resolved, nil
}

func (orchestrator *SubagentOrchestrator) labelOneImage(ctx context.Context, model, root, path, instructions string) imageLabel {
	relative, _ := filepath.Rel(root, path)
	label := imageLabel{Path: filepath.ToSlash(relative), Labels: []string{}, NeedsReview: true}
	images, err := loadImages(orchestrator.Workspace, []string{path})
	if err != nil {
		label.Error = err.Error()
		return label
	}
	var content strings.Builder
	err = orchestrator.Chat.Chat(ctx, ollama.ChatRequest{
		Model: model,
		Messages: []ollama.Message{
			{Role: "system", Content: "Label exactly one image. Follow the supplied taxonomy literally. Return only one JSON object with labels (array of strings), confidence (0 to 1), needs_review (boolean), and notes (short string). Do not use Markdown."},
			{Role: "user", Content: instructions + "\n\nImage path: " + label.Path, Images: [][]byte{images[0].Data}},
		},
		Options: &ollama.ChatOptions{NumCtx: &orchestrator.ContextTokens},
	}, func(chunk ollama.ChatChunk) error {
		content.WriteString(chunk.Message.Content)
		return nil
	})
	if err != nil {
		label.Error = err.Error()
		return label
	}
	var modelLabel modelImageLabel
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(content.String())))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&modelLabel); err != nil {
		label.Error = "invalid model JSON: " + err.Error()
		label.Notes = boundedSubagentResult(strings.TrimSpace(content.String()), 512)
		return label
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		label.Error = "invalid model JSON: response must contain exactly one object"
		return label
	}
	if modelLabel.Labels == nil || modelLabel.Confidence == nil || modelLabel.NeedsReview == nil || *modelLabel.Confidence < 0 || *modelLabel.Confidence > 1 {
		label.Error = "model JSON is missing required fields or has invalid confidence"
		return label
	}
	for _, item := range modelLabel.Labels {
		if strings.TrimSpace(item) == "" {
			label.Error = "model JSON contains an empty label"
			return label
		}
	}
	label.Labels = modelLabel.Labels
	label.Confidence = *modelLabel.Confidence
	label.NeedsReview = *modelLabel.NeedsReview
	label.Notes = boundedSubagentResult(modelLabel.Notes, 512)
	return label
}
