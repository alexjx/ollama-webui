package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

var ErrTurnLimit = errors.New("agent reached its emergency turn limit")

const maxToolArguments = 128 << 10
const defaultToolFeedbackBytes = 12 << 10

var shellTool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name:        "shell",
		Description: "Run a shell command when it helps complete the task. It can inspect data, calculate, search, or run programs. Commands may read or change files, so use it only when useful and act carefully.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"The shell command to run."},"timeout_seconds":{"type":"integer","minimum":1,"description":"Optional command timeout. The server maximum still applies."}},"required":["command"],"additionalProperties":false}`),
	},
}

var contextListTool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name:        "context_list",
		Description: "List durable notes and large outputs saved for this conversation. Use it to rediscover an artifact before reading it.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"cursor":{"type":"integer","minimum":0,"description":"Zero-based list cursor."},"limit":{"type":"integer","minimum":1,"maximum":20,"description":"Number of artifacts to return."}},"additionalProperties":false}`),
	},
}

var contextReadTool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name:        "context_read",
		Description: "Read a bounded byte range from an artifact owned by this conversation. Continue from next_offset when eof is false.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"artifact_id":{"type":"string","description":"Opaque artifact ID returned by a context tool."},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":8192}},"required":["artifact_id"],"additionalProperties":false}`),
	},
}

var contextWriteTool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name:        "context_write",
		Description: "Save a durable note for this conversation when its details should survive prompt compaction. Prefer concise notes and use the workspace for user-owned deliverables.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Short descriptive name."},"content":{"type":"string","description":"Text to preserve."}},"required":["name","content"],"additionalProperties":false}`),
	},
}

var delegateTaskTool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name:        "delegate_task",
		Description: "Run a focused task in an isolated child-agent context. Use it for substantial independent exploration, analysis, or file work whose intermediate details should not fill the main context. The child receives only the task, named workspace/system image inputs, and the standard agent instructions. Put user deliverables under the workspace and declare their paths. The parent receives only a bounded summary and verified output paths.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"task":{"type":"string","description":"Self-contained objective, constraints, relevant paths, and expected result."},"image_paths":{"type":"array","items":{"type":"string"},"maxItems":4,"description":"Optional workspace-relative or absolute system image paths for a vision-capable model."},"output_paths":{"type":"array","items":{"type":"string"},"maxItems":20,"description":"Files the child must create under the workspace."}},"required":["task"],"additionalProperties":false}`),
	},
}

var labelImageDirectoryTool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name:        "label_image_directory",
		Description: "Label every supported image in a directory using isolated one-image model calls, then atomically write JSONL under AGENT_WORKSPACE. Use this instead of inspecting many images in the main context. Each row contains path, labels, confidence, needs_review, notes, and an optional error.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"directory":{"type":"string","description":"Workspace-relative or absolute system directory to read."},"instructions":{"type":"string","description":"Complete label taxonomy and decision rules."},"output_path":{"type":"string","description":"Workspace-relative JSONL output path."},"recursive":{"type":"boolean","description":"Whether to traverse subdirectories."},"max_images":{"type":"integer","minimum":1,"maximum":10000,"description":"Safety cap; defaults to 1000."}},"required":["directory","instructions","output_path"],"additionalProperties":false}`),
	},
}

const SystemPrompt = `You are a general-purpose autonomous assistant. Continue until the user's task is actually complete, but answer directly when no tool is needed. A shell tool is available as an optional capability for calculation, search, inspection, execution, and verification; do not inspect or edit files unless that helps the user's request. The shell may read system paths allowed by normal OS permissions, while writes are restricted to AGENT_WORKSPACE. Delegate substantial independent work when its intermediate details would crowd the main context. For a directory of images, use the dedicated image-labeling tool so each image is processed in an isolated request and results go to a workspace JSONL file. Context tools can persist important notes or large outputs outside the prompt and retrieve them later within this conversation. When a user message says its full input was staged to a file, use bounded line ranges to inspect only the relevant portions, keep concise intermediate notes, and avoid printing the whole file into the conversation. Prefer narrow commands and small outputs so local model context remains focused. Do not stop after merely describing a plan. When the task is complete, reply with a concise final answer and do not call a tool. The user can interrupt you at any time.`

type ChatClient interface {
	Chat(context.Context, ollama.ChatRequest, func(ollama.ChatChunk) error) error
}

type StepStore interface {
	BeginAgentStep(context.Context, int64, int, string, string) (store.AgentStep, error)
	CompleteAgentStep(context.Context, int64, string, int, string) (store.AgentStep, error)
}

type CommandExecutor interface {
	Run(context.Context, string, time.Duration) ShellResult
}

type Runner struct {
	Chat              ChatClient
	Steps             StepStore
	Shell             CommandExecutor
	Stager            InputStager
	MaxTurns          int
	ToolFeedbackLimit int
	Context           *ContextPreparer
	Artifacts         *ArtifactManager
	Subagents         SubagentExecutor
}

type RunInput struct {
	ConversationID     int64
	Model              string
	Mode               string
	Messages           []ollama.Message
	Options            *ollama.ChatOptions
	Think              *ollama.ThinkValue
	AssistantMessageID int64
	Vision             bool
}

type Event struct {
	Type    string           `json:"type"`
	Content string           `json:"content,omitempty"`
	Turn    int              `json:"turn,omitempty"`
	Step    *store.AgentStep `json:"step,omitempty"`
	Run     *store.AgentRun  `json:"run,omitempty"`
}

type RunResult struct {
	Content  string
	Thinking string
	Metadata map[string]any
}

type runMetrics struct {
	promptEvalCount    int
	promptEvalDuration int64
	evalCount          int
	evalDuration       int64
	totalDuration      int64
	loadDuration       int64
}

func (metrics *runMetrics) add(chunk ollama.ChatChunk) {
	metrics.promptEvalCount += chunk.PromptEvalCount
	metrics.promptEvalDuration += chunk.PromptEvalDuration
	metrics.evalCount += chunk.EvalCount
	metrics.evalDuration += chunk.EvalDuration
	metrics.totalDuration += chunk.TotalDuration
	metrics.loadDuration += chunk.LoadDuration
}

func (runner Runner) Run(ctx context.Context, input RunInput, emit func(Event) error) (RunResult, error) {
	agentMode := input.Mode != "chat"
	if agentMode && runner.MaxTurns <= 0 {
		return RunResult{}, errors.New("agent max turns must be positive")
	}
	var err error
	messages := input.Messages
	cleanup := func() {}
	if agentMode {
		messages, cleanup, err = runner.Stager.Stage(input.Messages, input.OptionsNumCtx())
		if err != nil {
			return RunResult{}, err
		}
	}
	defer cleanup()
	var visible strings.Builder
	var thinking strings.Builder
	var metadata map[string]any
	var metrics runMetrics

	maxTurns := 1
	tools := []ollama.Tool(nil)
	if agentMode {
		maxTurns = runner.MaxTurns
		tools = []ollama.Tool{shellTool}
		if runner.Artifacts != nil && input.ConversationID > 0 {
			tools = append(tools, contextListTool, contextReadTool, contextWriteTool)
		}
		if runner.Subagents != nil && input.ConversationID > 0 && input.AssistantMessageID > 0 {
			tools = append(tools, delegateTaskTool)
			if _, ok := runner.Subagents.(ImageBatchExecutor); ok && input.Vision {
				tools = append(tools, labelImageDirectoryTool)
			}
		}
	}
	for turn := 1; turn <= maxTurns; turn++ {
		if err := emit(Event{Type: "turn.started", Turn: turn}); err != nil {
			return RunResult{Content: visible.String(), Thinking: thinking.String(), Metadata: metadata}, err
		}
		var assistant ollama.Message
		var final ollama.ChatChunk
		turnStartedContent := false
		turnStartedThinking := false
		err = runner.Chat.Chat(ctx, ollama.ChatRequest{
			Model: input.Model, Messages: messages, Tools: tools, Options: input.Options, Think: input.Think,
		}, func(chunk ollama.ChatChunk) error {
			if chunk.Message.Thinking != "" {
				assistant.Thinking += chunk.Message.Thinking
				prefix := ""
				if !turnStartedThinking && thinking.Len() > 0 {
					prefix = "\n\n"
					thinking.WriteString(prefix)
				}
				turnStartedThinking = true
				thinking.WriteString(chunk.Message.Thinking)
				if err := emit(Event{Type: "thinking.delta", Content: prefix + chunk.Message.Thinking, Turn: turn}); err != nil {
					return err
				}
			}
			assistant.ToolCalls = append(assistant.ToolCalls, chunk.Message.ToolCalls...)
			if chunk.Message.Content != "" {
				prefix := ""
				if !turnStartedContent && visible.Len() > 0 {
					prefix = "\n\n"
					visible.WriteString(prefix)
				}
				turnStartedContent = true
				assistant.Content += chunk.Message.Content
				visible.WriteString(chunk.Message.Content)
				if err := emit(Event{Type: "delta", Content: prefix + chunk.Message.Content, Turn: turn}); err != nil {
					return err
				}
			}
			if chunk.Done {
				final = chunk
			}
			return nil
		})
		if err != nil {
			return RunResult{Content: visible.String(), Thinking: thinking.String(), Metadata: metadata}, err
		}
		assistant.Role = "assistant"
		assistant.Thinking = ""
		messages = append(messages, assistant)
		metrics.add(final)
		metadata = chunkMetadata(final, turn, metrics, input.Mode)
		if !agentMode || len(assistant.ToolCalls) == 0 {
			return RunResult{Content: visible.String(), Thinking: thinking.String(), Metadata: metadata}, nil
		}

		for _, call := range assistant.ToolCalls {
			resultContent, err := runner.executeTool(ctx, input.ConversationID, input.AssistantMessageID, input.Model, input.Vision, turn, call, emit)
			if err != nil {
				return RunResult{Content: visible.String(), Thinking: thinking.String(), Metadata: metadata}, err
			}
			messages = append(messages, ollama.Message{
				Role: "tool", Content: resultContent, ToolName: call.Function.Name, ToolCallID: call.ID,
			})
		}
	}
	return RunResult{Content: visible.String(), Thinking: thinking.String(), Metadata: metadata}, ErrTurnLimit
}

type shellArguments struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type contextReadArguments struct {
	ArtifactID string `json:"artifact_id"`
	Offset     int64  `json:"offset,omitempty"`
	Limit      int64  `json:"limit,omitempty"`
}

type contextListArguments struct {
	Cursor int `json:"cursor,omitempty"`
	Limit  int `json:"limit,omitempty"`
}

type contextWriteArguments struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type delegateTaskArguments struct {
	Task        string   `json:"task"`
	ImagePaths  []string `json:"image_paths,omitempty"`
	OutputPaths []string `json:"output_paths,omitempty"`
}

type labelImageDirectoryArguments struct {
	Directory    string `json:"directory"`
	Instructions string `json:"instructions"`
	OutputPath   string `json:"output_path"`
	Recursive    bool   `json:"recursive,omitempty"`
	MaxImages    int    `json:"max_images,omitempty"`
}

func (runner Runner) executeContextTool(ctx context.Context, conversationID, messageID int64, name string, raw json.RawMessage) (ShellResult, string) {
	encode := func(value any) (ShellResult, string) {
		payload, err := json.Marshal(value)
		if err != nil {
			return ShellResult{ExitCode: -1, Output: err.Error()}, "error"
		}
		return ShellResult{Output: string(payload)}, "complete"
	}
	fail := func(err error) (ShellResult, string) {
		return ShellResult{ExitCode: -1, Output: err.Error()}, "error"
	}

	switch name {
	case "context_list":
		var arguments contextListArguments
		if err := decodeToolArguments(raw, &arguments); err != nil {
			return fail(err)
		}
		if arguments.Cursor < 0 || arguments.Limit < 0 || arguments.Limit > 20 {
			return fail(errors.New("cursor must be non-negative and limit must be between 1 and 20"))
		}
		if arguments.Limit == 0 {
			arguments.Limit = 10
		}
		artifacts, err := runner.Artifacts.List(ctx, conversationID)
		if err != nil {
			return fail(err)
		}
		total := len(artifacts)
		start := arguments.Cursor
		if start > total {
			start = total
		}
		end := start + arguments.Limit
		if end > total {
			end = total
		}
		items := make([]map[string]any, 0, end-start)
		for _, artifact := range artifacts[start:end] {
			items = append(items, map[string]any{
				"artifact_id": artifact.StorageKey, "kind": artifact.Kind, "name": artifact.DisplayName,
				"bytes": artifact.SizeBytes, "sha256": artifact.SHA256, "summary": artifact.Summary,
				"created_at": artifact.CreatedAt,
			})
		}
		response := map[string]any{"items": items, "total": total, "cursor": start, "has_more": end < total}
		if end < total {
			response["next_cursor"] = end
		}
		return encode(response)
	case "context_read":
		var arguments contextReadArguments
		if err := decodeToolArguments(raw, &arguments); err != nil {
			return fail(err)
		}
		if arguments.ArtifactID == "" {
			return fail(errors.New("artifact_id is required"))
		}
		if arguments.Limit == 0 {
			arguments.Limit = 4096
		}
		feedbackLimit := runner.ToolFeedbackLimit
		if feedbackLimit <= 0 {
			feedbackLimit = defaultToolFeedbackBytes
		}
		maxPage := int64(feedbackLimit / 3)
		if maxPage < 256 {
			maxPage = 256
		}
		if maxPage > 4096 {
			maxPage = 4096
		}
		if arguments.Limit > maxPage {
			arguments.Limit = maxPage
		}
		page, err := runner.Artifacts.Read(ctx, conversationID, arguments.ArtifactID, arguments.Offset, arguments.Limit)
		if err != nil {
			return fail(err)
		}
		return encode(map[string]any{
			"artifact_id": page.Artifact.StorageKey, "content": string(page.Data), "offset": page.Offset,
			"next_offset": page.NextOffset, "eof": page.EOF, "bytes": len(page.Data),
		})
	case "context_write":
		var arguments contextWriteArguments
		if err := decodeToolArguments(raw, &arguments); err != nil {
			return fail(err)
		}
		arguments.Name = strings.TrimSpace(arguments.Name)
		if arguments.Name == "" {
			return fail(errors.New("name is required"))
		}
		if len(arguments.Name) > 200 {
			return fail(errors.New("name exceeds 200 bytes"))
		}
		if len(arguments.Content) == 0 {
			return fail(errors.New("content is required"))
		}
		if len(arguments.Content) > 64<<10 {
			return fail(errors.New("content exceeds 64 KiB"))
		}
		var sourceMessageID *int64
		if messageID > 0 {
			messageIDCopy := messageID
			sourceMessageID = &messageIDCopy
		}
		artifact, err := runner.Artifacts.Write(ctx, store.CreateContextArtifactParams{
			ConversationID: conversationID, SourceMessageID: sourceMessageID, Kind: "agent_note",
			DisplayName: arguments.Name, MediaType: "text/plain", Summary: "Agent-authored durable conversation note.",
		}, []byte(arguments.Content))
		if err != nil {
			return fail(err)
		}
		return encode(map[string]any{
			"artifact_id": artifact.StorageKey, "name": artifact.DisplayName,
			"bytes": artifact.SizeBytes, "sha256": artifact.SHA256,
		})
	default:
		return fail(fmt.Errorf("unknown context tool %q", name))
	}
}

func decodeToolArguments(raw json.RawMessage, target any) error {
	if len(raw) > maxToolArguments {
		return errors.New("arguments exceed 128 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("arguments must contain one JSON object")
	}
	return nil
}

func (runner Runner) executeTool(ctx context.Context, conversationID, messageID int64, model string, vision bool, turn int, call ollama.ToolCall, emit func(Event) error) (string, error) {
	raw := call.Function.Arguments
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	step, err := runner.Steps.BeginAgentStep(ctx, messageID, turn, call.Function.Name, string(raw))
	if err != nil {
		return "", err
	}
	if err := emit(Event{Type: "tool.started", Turn: turn, Step: &step}); err != nil {
		return "", err
	}

	var result ShellResult
	status := "complete"
	switch call.Function.Name {
	case "shell":
		arguments, decodeErr := decodeShellArguments(raw)
		if decodeErr != nil {
			result = ShellResult{ExitCode: -1, Output: "invalid shell arguments: " + decodeErr.Error()}
			status = "error"
		} else {
			result = runner.Shell.Run(ctx, arguments.Command, time.Duration(arguments.TimeoutSeconds)*time.Second)
			if result.TimedOut {
				status = "error"
			} else if result.Cancelled {
				status = "cancelled"
			}
		}
	case "context_list", "context_read", "context_write":
		if runner.Artifacts == nil || conversationID <= 0 {
			result = ShellResult{ExitCode: -1, Output: "context storage is unavailable"}
			status = "error"
		} else {
			result, status = runner.executeContextTool(ctx, conversationID, messageID, call.Function.Name, raw)
		}
	case "delegate_task":
		if runner.Subagents == nil || conversationID <= 0 || messageID <= 0 {
			result = ShellResult{ExitCode: -1, Output: "subagent delegation is unavailable"}
			status = "error"
		} else {
			var arguments delegateTaskArguments
			if decodeErr := decodeToolArguments(raw, &arguments); decodeErr != nil {
				result = ShellResult{ExitCode: -1, Output: "invalid delegation arguments: " + decodeErr.Error()}
				status = "error"
			} else {
				delegated, delegateErr := runner.Subagents.Execute(ctx, SubagentRequest{
					ConversationID: conversationID, RequestingMessageID: messageID,
					Model: model, SupportsVision: vision, Task: arguments.Task, ImagePaths: arguments.ImagePaths, OutputPaths: arguments.OutputPaths,
				}, emit)
				result = ShellResult{Output: delegated.Feedback, ExitCode: delegated.ExitCode, Duration: delegated.Duration,
					Cancelled: errors.Is(delegateErr, context.Canceled)}
				if delegateErr != nil {
					status = "error"
					if result.Output == "" {
						result.Output = delegateErr.Error()
					}
				}
			}
		}
	case "label_image_directory":
		batcher, ok := runner.Subagents.(ImageBatchExecutor)
		if !ok || !vision || conversationID <= 0 || messageID <= 0 {
			result = ShellResult{ExitCode: -1, Output: "image batch delegation is unavailable"}
			status = "error"
		} else {
			var arguments labelImageDirectoryArguments
			if decodeErr := decodeToolArguments(raw, &arguments); decodeErr != nil {
				result = ShellResult{ExitCode: -1, Output: "invalid image batch arguments: " + decodeErr.Error()}
				status = "error"
			} else {
				delegated, delegateErr := batcher.LabelImageDirectory(ctx, ImageBatchRequest{
					ConversationID: conversationID, RequestingMessageID: messageID, Model: model, SupportsVision: vision,
					Directory: arguments.Directory, Instructions: arguments.Instructions, OutputPath: arguments.OutputPath,
					Recursive: arguments.Recursive, MaxImages: arguments.MaxImages,
				}, emit)
				result = ShellResult{Output: delegated.Feedback, ExitCode: delegated.ExitCode, Duration: delegated.Duration,
					Cancelled: errors.Is(delegateErr, context.Canceled)}
				if delegateErr != nil {
					status = "error"
					if result.Output == "" {
						result.Output = delegateErr.Error()
					}
				}
			}
		}
	default:
		result = ShellResult{ExitCode: -1, Output: fmt.Sprintf("unknown tool %q", call.Function.Name)}
		status = "error"
	}

	completed, persistErr := runner.completeStep(step, result, status)
	if persistErr != nil {
		return "", persistErr
	}
	completed.MessageID = step.MessageID
	completed.Turn = step.Turn
	completed.ToolName = step.ToolName
	completed.Input = step.Input
	completed.StartedAt = step.StartedAt
	if err := emit(Event{Type: "tool.done", Turn: turn, Step: &completed}); err != nil {
		return "", err
	}
	feedback, feedbackTruncated := boundedToolFeedback(result.Output, runner.ToolFeedbackLimit)
	payloadData := map[string]any{
		"output": feedback, "output_bytes": len(result.Output), "feedback_truncated": feedbackTruncated, "exit_code": result.ExitCode,
		"duration_ms": result.Duration.Milliseconds(), "timed_out": result.TimedOut,
		"cancelled": result.Cancelled, "truncated": result.Truncated,
	}
	if call.Function.Name == "shell" && feedbackTruncated && runner.Artifacts != nil && conversationID > 0 && messageID > 0 {
		messageIDCopy, stepID := messageID, completed.ID
		artifact, artifactErr := runner.Artifacts.Write(ctx, store.CreateContextArtifactParams{
			ConversationID: conversationID, SourceMessageID: &messageIDCopy, SourceStepID: &stepID,
			Kind: "shell_output", DisplayName: fmt.Sprintf("shell-turn-%03d.txt", turn), MediaType: "text/plain",
			Summary: "Captured shell output; use context_read with the artifact ID to inspect bounded ranges.",
		}, []byte(result.Output))
		if artifactErr == nil {
			payloadData["artifact_id"] = artifact.StorageKey
			payloadData["artifact_bytes"] = artifact.SizeBytes
			payloadData["artifact_sha256"] = artifact.SHA256
			payloadData["artifact_source_truncated"] = result.Truncated
		} else {
			payloadData["artifact_error"] = artifactErr.Error()
		}
	}
	payload, err := json.Marshal(payloadData)
	if err != nil {
		return "", err
	}
	if result.Cancelled {
		return string(payload), context.Canceled
	}
	return string(payload), nil
}

func (input RunInput) OptionsNumCtx() *int {
	if input.Options == nil {
		return nil
	}
	return input.Options.NumCtx
}

func boundedToolFeedback(output string, limit int) (string, bool) {
	if limit <= 0 {
		limit = defaultToolFeedbackBytes
	}
	if len(output) <= limit {
		return output, false
	}
	marker := fmt.Sprintf("\n[... %d bytes omitted from model context; full output remains in the activity log ...]\n", len(output)-limit)
	head := limit * 2 / 3
	tail := limit - head
	return output[:head] + marker + output[len(output)-tail:], true
}

func decodeShellArguments(raw json.RawMessage) (shellArguments, error) {
	if len(raw) > maxToolArguments {
		return shellArguments{}, errors.New("arguments exceed 128 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var arguments shellArguments
	if err := decoder.Decode(&arguments); err != nil {
		return shellArguments{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return shellArguments{}, errors.New("arguments must contain one JSON object")
	}
	arguments.Command = strings.TrimSpace(arguments.Command)
	if arguments.Command == "" {
		return shellArguments{}, errors.New("command is required")
	}
	if len(arguments.Command) > 64<<10 {
		return shellArguments{}, errors.New("command exceeds 64 KiB")
	}
	if arguments.TimeoutSeconds < 0 {
		return shellArguments{}, errors.New("timeout_seconds must be positive")
	}
	return arguments, nil
}

func (runner Runner) completeStep(step store.AgentStep, result ShellResult, status string) (store.AgentStep, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return runner.Steps.CompleteAgentStep(ctx, step.ID, result.Output, result.ExitCode, status)
}

func chunkMetadata(chunk ollama.ChatChunk, turn int, metrics runMetrics, mode string) map[string]any {
	if mode == "" {
		mode = "agent"
	}
	metadata := map[string]any{
		"agent_turns": turn, "done_reason": chunk.DoneReason, "total_duration": chunk.TotalDuration,
		"load_duration": chunk.LoadDuration, "prompt_eval_count": chunk.PromptEvalCount,
		"prompt_eval_duration": chunk.PromptEvalDuration, "eval_count": chunk.EvalCount,
		"eval_duration": chunk.EvalDuration, "run_mode": mode,
	}
	if mode == "agent" {
		metadata["agent_prompt_eval_count"] = metrics.promptEvalCount
		metadata["agent_prompt_eval_duration"] = metrics.promptEvalDuration
		metadata["agent_eval_count"] = metrics.evalCount
		metadata["agent_eval_duration"] = metrics.evalDuration
		metadata["agent_total_duration"] = metrics.totalDuration
		metadata["agent_load_duration"] = metrics.loadDuration
	}
	return metadata
}
