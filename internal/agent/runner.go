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

const SystemPrompt = `You are a general-purpose autonomous assistant. Continue until the user's task is actually complete, but answer directly when no tool is needed. A shell tool is available as an optional capability for calculation, search, inspection, execution, and verification; do not inspect or edit files unless that helps the user's request. When a user message says its full input was staged to a file, use bounded line ranges to inspect only the relevant portions, keep concise intermediate notes, and avoid printing the whole file into the conversation. Prefer narrow commands and small outputs so local model context remains focused. Do not stop after merely describing a plan. When the task is complete, reply with a concise final answer and do not call a tool. The user can interrupt you at any time.`

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
}

type RunInput struct {
	Model              string
	Messages           []ollama.Message
	Options            *ollama.ChatOptions
	Think              *bool
	AssistantMessageID int64
}

type Event struct {
	Type    string           `json:"type"`
	Content string           `json:"content,omitempty"`
	Turn    int              `json:"turn,omitempty"`
	Step    *store.AgentStep `json:"step,omitempty"`
}

type RunResult struct {
	Content  string
	Thinking string
	Metadata map[string]any
}

func (runner Runner) Run(ctx context.Context, input RunInput, emit func(Event) error) (RunResult, error) {
	if runner.MaxTurns <= 0 {
		return RunResult{}, errors.New("agent max turns must be positive")
	}
	messages, cleanup, err := runner.Stager.Stage(input.Messages, input.OptionsNumCtx())
	if err != nil {
		return RunResult{}, err
	}
	defer cleanup()
	var visible strings.Builder
	var thinking strings.Builder
	var metadata map[string]any

	for turn := 1; turn <= runner.MaxTurns; turn++ {
		if err := emit(Event{Type: "turn.started", Turn: turn}); err != nil {
			return RunResult{Content: visible.String(), Thinking: thinking.String(), Metadata: metadata}, err
		}
		var assistant ollama.Message
		var final ollama.ChatChunk
		turnStartedContent := false
		turnStartedThinking := false
		err = runner.Chat.Chat(ctx, ollama.ChatRequest{
			Model: input.Model, Messages: messages, Tools: []ollama.Tool{shellTool}, Options: input.Options, Think: input.Think,
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
		metadata = chunkMetadata(final, turn)
		if len(assistant.ToolCalls) == 0 {
			return RunResult{Content: visible.String(), Thinking: thinking.String(), Metadata: metadata}, nil
		}

		for _, call := range assistant.ToolCalls {
			resultContent, err := runner.executeTool(ctx, input.AssistantMessageID, turn, call, emit)
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

func (runner Runner) executeTool(ctx context.Context, messageID int64, turn int, call ollama.ToolCall, emit func(Event) error) (string, error) {
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
	if call.Function.Name != "shell" {
		result = ShellResult{ExitCode: -1, Output: fmt.Sprintf("unknown tool %q", call.Function.Name)}
		status = "error"
	} else if arguments, decodeErr := decodeShellArguments(raw); decodeErr != nil {
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
	payload, err := json.Marshal(map[string]any{
		"output": feedback, "output_bytes": len(result.Output), "feedback_truncated": feedbackTruncated, "exit_code": result.ExitCode,
		"duration_ms": result.Duration.Milliseconds(), "timed_out": result.TimedOut,
		"cancelled": result.Cancelled, "truncated": result.Truncated,
	})
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

func chunkMetadata(chunk ollama.ChatChunk, turn int) map[string]any {
	return map[string]any{
		"agent_turns": turn, "done_reason": chunk.DoneReason, "total_duration": chunk.TotalDuration,
		"load_duration": chunk.LoadDuration, "prompt_eval_count": chunk.PromptEvalCount,
		"prompt_eval_duration": chunk.PromptEvalDuration, "eval_count": chunk.EvalCount,
		"eval_duration": chunk.EvalDuration,
	}
}
