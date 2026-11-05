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
	DisableWebSearch    bool
	Options             *ollama.ChatOptions
	Think               *ollama.ThinkValue
	SystemPrompt        string
	MaxTurns            int
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
	WebSearch         WebSearcher
	Artifacts         *ArtifactManager
	Workspace         string
	ContextTokens     int
	MaxTurns          int
	ToolFeedbackLimit int
	ResultBytes       int
	semaphore         chan struct{}
}

func NewSubagentOrchestrator(store SubagentStore, chat ChatClient, shell CommandExecutor, artifacts *ArtifactManager,
	workspace string, contextTokens, maxTurns, resultBytes, toolFeedbackLimit int) (*SubagentOrchestrator, error) {
	if store == nil || chat == nil || shell == nil {
		return nil, errors.New("subagent store, chat client, and shell executor are required")
	}
	if contextTokens <= 0 || maxTurns <= 0 || resultBytes <= 0 {
		return nil, errors.New("subagent limits must be positive")
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve subagent workspace: %w", err)
	}
	return &SubagentOrchestrator{
		Store: store, Chat: chat, Shell: shell, Artifacts: artifacts, Workspace: workspace,
		ContextTokens: contextTokens, MaxTurns: maxTurns, ResultBytes: resultBytes,
		ToolFeedbackLimit: toolFeedbackLimit, semaphore: make(chan struct{}, 1),
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
		ContextWindow: orchestrator.contextWindow(request.Options), Depth: 0,
	})
	if err != nil {
		_, _ = orchestrator.Store.FailAgentJob(context.Background(), job.ID, err.Error())
		return SubagentResult{ExitCode: -1}, err
	}
	if err := emit(Event{Type: "subagent.queued", Run: &run}); err != nil {
		orchestrator.cancel(job.ID, run.ID, err)
		return SubagentResult{ExitCode: -1, Duration: time.Since(started)}, err
	}

	select {
	case orchestrator.semaphore <- struct{}{}:
		defer func() { <-orchestrator.semaphore }()
	case <-ctx.Done():
		cancelled := orchestrator.cancel(job.ID, run.ID, ctx.Err())
		_ = emit(Event{Type: "subagent.cancelled", Run: &cancelled})
		return SubagentResult{ExitCode: -1, Duration: time.Since(started)}, ctx.Err()
	}
	if _, err = orchestrator.Store.StartAgentJob(ctx, job.ID); err != nil {
		return orchestrator.failSubagent(job.ID, run.ID, started, err, emit)
	}
	run, err = orchestrator.Store.StartAgentRun(ctx, run.ID)
	if err != nil {
		return orchestrator.failSubagent(job.ID, run.ID, started, err, emit)
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
	maxTurns := request.MaxTurns
	if maxTurns <= 0 {
		maxTurns = orchestrator.MaxTurns
	}
	childSteps := &transientStepStore{}
	attachments, _ := orchestrator.Store.(AttachmentStore)
	childRunner := Runner{
		Chat: orchestrator.Chat, Steps: childSteps, Shell: orchestrator.Shell,
		MaxTurns: maxTurns, ToolFeedbackLimit: orchestrator.ToolFeedbackLimit,
		Artifacts:   orchestrator.Artifacts,
		Attachments: attachments,
		WebSearch:   orchestrator.WebSearch,
	}
	childResult, runErr := childRunner.Run(ctx, RunInput{
		ConversationID: request.ConversationID, Model: model, Mode: "agent", DisableWebSearch: request.DisableWebSearch,
		Messages: []ollama.Message{
			{Role: "system", Content: childSystemPrompt(request.SystemPrompt, orchestrator.Workspace)},
			{Role: "user", Content: childPrompt, Images: imageBytes},
		},
		Options: cloneChatOptions(request.Options), Think: cloneThink(request.Think),
	}, childProgressEmitter(&run, emit))
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

	finalContent := childResult.Content
	if start, ok := childResult.Metadata["final_answer_start"].(int); ok && start >= 0 && start <= len(finalContent) {
		finalContent = finalContent[start:]
	}
	summary := boundedSubagentResult(strings.TrimSpace(finalContent), orchestrator.ResultBytes)
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

const delegatedSystemPrompt = `You are a focused child agent with a private working context. Inherit the parent's settings and instructions; work only on the delegated task. Read referenced files or attachment IDs in relevant ranges. Keep large intermediate results in workspace files. Do not delegate further. Your final answer is the concise summary returned to the parent: include findings, verified output paths, failures and unresolved questions, without raw logs or intermediate narration.`

func childSystemPrompt(parent, workspace string) string {
	if parent == "" {
		return delegatedSystemPrompt + WorkspaceInstructions(workspace) + webSearchInstructions
	}
	return parent + "\n\n" + delegatedSystemPrompt
}

func (orchestrator *SubagentOrchestrator) contextWindow(options *ollama.ChatOptions) int {
	if options != nil && options.NumCtx != nil && *options.NumCtx > 0 {
		return *options.NumCtx
	}
	return orchestrator.ContextTokens
}

func cloneChatOptions(options *ollama.ChatOptions) *ollama.ChatOptions {
	if options == nil {
		return nil
	}
	copy := *options
	if options.NumCtx != nil {
		value := *options.NumCtx
		copy.NumCtx = &value
	}
	if options.NumPredict != nil {
		value := *options.NumPredict
		copy.NumPredict = &value
	}
	if options.Temperature != nil {
		value := *options.Temperature
		copy.Temperature = &value
	}
	return &copy
}

func cloneThink(think *ollama.ThinkValue) *ollama.ThinkValue {
	if think == nil {
		return nil
	}
	copy := *think
	return &copy
}

// Progress describes observable phases without sending private child content or
// tool arguments back into the parent's transcript or model context.
func childProgressEmitter(run *store.AgentRun, emit func(Event) error) func(Event) error {
	lastPhase, lastTurn := "", 0
	return func(event Event) error {
		phase := ""
		switch event.Type {
		case "turn.started":
			phase = "starting"
		case "thinking.delta":
			phase = "thinking"
		case "delta":
			phase = "responding"
		case "tool.started":
			phase = "tool"
			if event.Step != nil {
				switch event.Step.ToolName {
				case "shell":
					phase = "shell"
				case "web_search":
					phase = "web_search"
				case "attachment_read", "attachment_list", "context_read", "context_list":
					phase = "reading"
				}
			}
		case "tool.done":
			phase = "reviewing"
		}
		if phase == "" {
			return nil
		}
		turn := event.Turn
		if turn == 0 {
			turn = lastTurn
		}
		if phase == lastPhase && turn == lastTurn {
			return nil
		}
		lastPhase, lastTurn = phase, turn
		progress, _ := json.Marshal(map[string]any{"phase": phase, "turn": turn})
		return emit(Event{Type: "subagent.progress", Run: run, Content: string(progress)})
	}
}

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
