package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

const maxDelegatedTaskBytes = 16 << 10

type SubagentRequest struct {
	ConversationID      int64
	RequestingMessageID int64
	Model               string
	SupportsVision      bool
	Task                string
	ImagePaths          []string
	OutputPaths         []string
}

type SubagentResult struct {
	Feedback string
	ExitCode int
	Duration time.Duration
}

type SubagentExecutor interface {
	Execute(context.Context, SubagentRequest, func(Event) error) (SubagentResult, error)
}

type SubagentStore interface {
	CreateAgentJob(context.Context, store.CreateAgentJobParams) (store.AgentJob, error)
	StartAgentJob(context.Context, int64) (store.AgentJob, error)
	CompleteAgentJob(context.Context, int64, string, []string) (store.AgentJob, error)
	FailAgentJob(context.Context, int64, string) (store.AgentJob, error)
	CancelAgentJob(context.Context, int64, string) (store.AgentJob, error)
	CreateAgentRun(context.Context, store.CreateAgentRunParams) (store.AgentRun, error)
	StartAgentRun(context.Context, int64) (store.AgentRun, error)
	CompleteAgentRun(context.Context, int64, string, []string) (store.AgentRun, error)
	FailAgentRun(context.Context, int64, string) (store.AgentRun, error)
	CancelAgentRun(context.Context, int64, string) (store.AgentRun, error)
	TouchAgentJob(context.Context, int64) error
	TouchAgentRun(context.Context, int64) error
}

type SubagentOrchestrator struct {
	Store             SubagentStore
	Chat              ChatClient
	Shell             CommandExecutor
	Artifacts         *ArtifactManager
	Workspace         string
	Model             string
	ContextTokens     int
	MaxTurns          int
	ToolFeedbackLimit int
	ResultBytes       int
	semaphore         chan struct{}
}

func NewSubagentOrchestrator(store SubagentStore, chat ChatClient, shell CommandExecutor, artifacts *ArtifactManager,
	workspace, model string, contextTokens, maxTurns, concurrency, resultBytes, toolFeedbackLimit int) (*SubagentOrchestrator, error) {
	if store == nil || chat == nil || shell == nil {
		return nil, errors.New("subagent store, chat client, and shell executor are required")
	}
	if contextTokens <= 0 || maxTurns <= 0 || concurrency <= 0 || resultBytes <= 0 {
		return nil, errors.New("subagent limits must be positive")
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve subagent workspace: %w", err)
	}
	return &SubagentOrchestrator{
		Store: store, Chat: chat, Shell: shell, Artifacts: artifacts, Workspace: workspace, Model: model,
		ContextTokens: contextTokens, MaxTurns: maxTurns, ResultBytes: resultBytes,
		ToolFeedbackLimit: toolFeedbackLimit, semaphore: make(chan struct{}, concurrency),
	}, nil
}

func (orchestrator *SubagentOrchestrator) Execute(ctx context.Context, request SubagentRequest, emit func(Event) error) (SubagentResult, error) {
	started := time.Now()
	request.Task = strings.TrimSpace(request.Task)
	if request.Task == "" {
		return SubagentResult{ExitCode: -1}, errors.New("delegated task is required")
	}
	if len(request.Task) > maxDelegatedTaskBytes {
		return SubagentResult{ExitCode: -1}, errors.New("delegated task exceeds 16 KiB")
	}
	if len(request.OutputPaths) > 20 {
		return SubagentResult{ExitCode: -1}, errors.New("delegate at most 20 output paths")
	}
	if len(request.ImagePaths) > 0 && !request.SupportsVision {
		return SubagentResult{ExitCode: -1}, errors.New("selected model does not support image input")
	}
	model := strings.TrimSpace(request.Model)
	if model == "" {
		model = orchestrator.Model
	}
	if model == "" {
		return SubagentResult{ExitCode: -1}, errors.New("subagent model is required")
	}
	images, err := loadImages(orchestrator.Workspace, request.ImagePaths)
	if err != nil {
		return SubagentResult{ExitCode: -1}, err
	}

	job, err := orchestrator.Store.CreateAgentJob(ctx, store.CreateAgentJobParams{
		ConversationID: request.ConversationID, RequestingMessageID: request.RequestingMessageID, Task: request.Task,
	})
	if err != nil {
		return SubagentResult{ExitCode: -1}, err
	}
	run, err := orchestrator.Store.CreateAgentRun(ctx, store.CreateAgentRunParams{
		JobID: job.ID, Task: request.Task, Model: model,
		ContextWindow: orchestrator.ContextTokens, Depth: 0,
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

	imageBytes := make([][]byte, 0, len(images))
	imageNames := make([]string, 0, len(images))
	for _, item := range images {
		imageBytes = append(imageBytes, item.Data)
		imageNames = append(imageNames, item.Path)
	}
	childPrompt := delegatedPrompt(request.Task, imageNames, request.OutputPaths)
	numCtx := orchestrator.ContextTokens
	childSteps := &transientStepStore{}
	childRunner := Runner{
		Chat: orchestrator.Chat, Steps: childSteps, Shell: orchestrator.Shell,
		MaxTurns: orchestrator.MaxTurns, ToolFeedbackLimit: orchestrator.ToolFeedbackLimit,
		Artifacts: orchestrator.Artifacts,
	}
	childResult, runErr := childRunner.Run(ctx, RunInput{
		ConversationID: request.ConversationID, Model: model, Mode: "agent",
		Messages: []ollama.Message{
			{Role: "system", Content: delegatedSystemPrompt},
			{Role: "user", Content: childPrompt, Images: imageBytes},
		},
		Options: &ollama.ChatOptions{NumCtx: &numCtx},
	}, func(Event) error { return nil })
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			cancelled := orchestrator.cancel(job.ID, run.ID, runErr)
			_ = emit(Event{Type: "subagent.cancelled", Run: &cancelled})
		} else {
			failed, _ := orchestrator.Store.FailAgentRun(context.Background(), run.ID, runErr.Error())
			_, _ = orchestrator.Store.FailAgentJob(context.Background(), job.ID, runErr.Error())
			_ = emit(Event{Type: "subagent.failed", Run: &failed})
		}
		return SubagentResult{Feedback: runErr.Error(), ExitCode: -1, Duration: time.Since(started)}, runErr
	}

	summary := boundedSubagentResult(strings.TrimSpace(childResult.Content), orchestrator.ResultBytes)
	outputs, err := verifiedOutputPaths(orchestrator.Workspace, request.OutputPaths)
	if err != nil {
		failed, _ := orchestrator.Store.FailAgentRun(context.Background(), run.ID, err.Error())
		_, _ = orchestrator.Store.FailAgentJob(context.Background(), job.ID, err.Error())
		_ = emit(Event{Type: "subagent.failed", Run: &failed})
		return SubagentResult{Feedback: err.Error(), ExitCode: -1, Duration: time.Since(started)}, err
	}
	persistCtx, cancelPersist := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelPersist()
	run, err = orchestrator.Store.CompleteAgentRun(persistCtx, run.ID, summary, outputs)
	if err != nil {
		return orchestrator.failSubagent(job.ID, run.ID, started, err, emit)
	}
	if _, err = orchestrator.Store.CompleteAgentJob(persistCtx, job.ID, summary, outputs); err != nil {
		// The run is already durably terminal. Close the UI lifecycle even if the
		// redundant job aggregate could not be updated.
		_ = emit(Event{Type: "subagent.done", Run: &run})
		return SubagentResult{Feedback: err.Error(), ExitCode: -1, Duration: time.Since(started)}, err
	}
	if err := emit(Event{Type: "subagent.done", Run: &run}); err != nil {
		return SubagentResult{Feedback: err.Error(), ExitCode: -1, Duration: time.Since(started)}, err
	}
	payload, _ := json.Marshal(map[string]any{
		"status": "complete", "summary": summary, "output_paths": outputs, "run_id": run.ID,
	})
	return SubagentResult{Feedback: string(payload), ExitCode: 0, Duration: time.Since(started)}, nil
}

const delegatedSystemPrompt = `You are a focused child agent. Work only on the delegated task. You have a private context, so do not assume access to the parent conversation. Read any referenced paths directly. You may read system paths, but the shell enforces that writes are only possible under AGENT_WORKSPACE. Put requested deliverables at the declared workspace paths. Keep your final response concise: state what was completed, verified output paths, failures, and items needing review. Do not include raw logs or per-item reasoning.`

func delegatedPrompt(task string, imagePaths, outputPaths []string) string {
	var prompt strings.Builder
	prompt.WriteString("Delegated task:\n")
	prompt.WriteString(task)
	if len(imagePaths) > 0 {
		prompt.WriteString("\n\nAttached image paths, in the same order as the image inputs:\n")
		for _, path := range imagePaths {
			fmt.Fprintf(&prompt, "- %s\n", path)
		}
	}
	if len(outputPaths) > 0 {
		prompt.WriteString("\nRequired output paths beneath AGENT_WORKSPACE:\n")
		for _, path := range outputPaths {
			fmt.Fprintf(&prompt, "- %s\n", path)
		}
	}
	return prompt.String()
}

func boundedSubagentResult(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	marker := "\n[child summary truncated]"
	if limit <= len(marker) {
		return marker[:limit]
	}
	cut := limit - len(marker)
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut] + marker
}

func verifiedOutputPaths(workspace string, paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		resolved := path
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(workspace, resolved)
		}
		resolved = filepath.Clean(resolved)
		relative, err := filepath.Rel(workspace, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("output path %q must be beneath AGENT_WORKSPACE", path)
		}
		info, err := os.Lstat(resolved)
		if err != nil {
			return nil, fmt.Errorf("required output %q was not created: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("required output %q must be a regular file", path)
		}
		result = append(result, filepath.ToSlash(relative))
	}
	return result, nil
}

func (orchestrator *SubagentOrchestrator) cancel(jobID, runID int64, cause error) store.AgentRun {
	reason := "cancelled"
	if cause != nil {
		reason = cause.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, _ := orchestrator.Store.CancelAgentRun(ctx, runID, reason)
	_, _ = orchestrator.Store.CancelAgentJob(ctx, jobID, reason)
	return run
}

func (orchestrator *SubagentOrchestrator) failSubagent(jobID, runID int64, started time.Time, failure error, emit func(Event) error) (SubagentResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	run, persistErr := orchestrator.Store.FailAgentRun(ctx, runID, failure.Error())
	_, _ = orchestrator.Store.FailAgentJob(ctx, jobID, failure.Error())
	if persistErr != nil {
		run = store.AgentRun{ID: runID, JobID: jobID, Status: store.AgentStatusFailed, Error: failure.Error()}
	}
	_ = emit(Event{Type: "subagent.failed", Run: &run})
	return SubagentResult{Feedback: failure.Error(), ExitCode: -1, Duration: time.Since(started)}, failure
}

type transientStepStore struct {
	next  atomic.Int64
	mu    sync.Mutex
	steps map[int64]store.AgentStep
}

func (steps *transientStepStore) BeginAgentStep(_ context.Context, _ int64, turn int, name, input string) (store.AgentStep, error) {
	id := steps.next.Add(1)
	step := store.AgentStep{ID: id, Turn: turn, ToolName: name, Input: input, Status: "running", StartedAt: time.Now().UTC()}
	steps.mu.Lock()
	defer steps.mu.Unlock()
	if steps.steps == nil {
		steps.steps = make(map[int64]store.AgentStep)
	}
	steps.steps[id] = step
	return step, nil
}

func (steps *transientStepStore) CompleteAgentStep(_ context.Context, id int64, output string, exitCode int, status string) (store.AgentStep, error) {
	steps.mu.Lock()
	defer steps.mu.Unlock()
	step, ok := steps.steps[id]
	if !ok {
		return store.AgentStep{}, store.ErrNotFound
	}
	now := time.Now().UTC()
	step.Output, step.ExitCode, step.Status, step.CompletedAt = output, &exitCode, status, &now
	steps.steps[id] = step
	return step, nil
}
