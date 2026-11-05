package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ollama-webui/internal/store"
)

// Child activity is persisted for the user interface, never appended to the
// parent's model messages. Only observed tools and bounded output are recorded.
func (orchestrator *SubagentOrchestrator) childProgressEmitter(ctx context.Context, conversationID int64, run *store.AgentRun, emit func(Event) error) func(Event) error {
	attachmentNames := map[int64]string{}
	return func(event Event) error {
		progress := store.AgentRunProgress{}
		if run.Progress != nil {
			progress = *run.Progress
			progress.Activities = append([]store.AgentRunActivity(nil), run.Progress.Activities...)
		}
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
				step := event.Step
				switch step.ToolName {
				case "shell":
					phase = "shell"
				case "web_search":
					phase = "web_search"
				case "attachment_read", "attachment_list", "context_read", "context_list":
					phase = "reading"
				}
				title, detail := childToolDescription(step, func(id int64) string {
					if name, ok := attachmentNames[id]; ok {
						return name
					}
					if attachments, ok := orchestrator.Store.(AttachmentStore); ok {
						if file, err := attachments.ReadTextAttachment(ctx, conversationID, id, 0, 1); err == nil {
							attachmentNames[id] = file.FileName
							return file.FileName
						}
					}
					return "text attachment"
				})
				progress.Activities = append(progress.Activities, store.AgentRunActivity{ID: step.ID, ToolName: step.ToolName, Title: title, Detail: detail, Status: store.AgentStatusRunning, StartedAt: step.StartedAt})
			}
		case "tool.done":
			phase = "reviewing"
			if event.Step != nil {
				step := event.Step
				for i := range progress.Activities {
					activity := &progress.Activities[i]
					if activity.ID != step.ID {
						continue
					}
					activity.Status = step.Status
					if activity.Status == "error" {
						activity.Status = store.AgentStatusFailed
					}
					activity.CompletedAt = step.CompletedAt
					if activity.CompletedAt == nil {
						now := time.Now().UTC()
						activity.CompletedAt = &now
					}
					activity.Output = childToolOutput(step)
					break
				}
			}
		}
		if phase == "" {
			return nil
		}
		turn := event.Turn
		if turn == 0 {
			turn = progress.Turn
		}
		toolEvent := event.Type == "tool.started" || event.Type == "tool.done"
		if !toolEvent && phase == progress.Phase && turn == progress.Turn {
			return nil
		}
		progress.Phase, progress.Turn = phase, turn
		updated, err := orchestrator.Store.UpdateAgentRunProgress(ctx, run.ID, &progress)
		if err != nil {
			return err
		}
		*run = updated
		return emitChildProgress(updated, emit)
	}
}

func emitChildProgress(run store.AgentRun, emit func(Event) error) error {
	data, err := json.Marshal(run.Progress)
	if err != nil {
		return err
	}
	return emit(Event{Type: "subagent.progress", Run: &run, Content: string(data)})
}

func childToolDescription(step *store.AgentStep, attachmentName func(int64) string) (string, string) {
	var arguments struct {
		Command      string `json:"command"`
		Query        string `json:"query"`
		AttachmentID int64  `json:"attachment_id"`
		Offset       int64  `json:"offset"`
		Limit        *int64 `json:"limit"`
		ArtifactID   string `json:"artifact_id"`
		Name         string `json:"name"`
	}
	_ = json.Unmarshal([]byte(step.Input), &arguments)
	switch step.ToolName {
	case "shell", "":
		return "Run command", arguments.Command
	case "web_search":
		return "Search: " + arguments.Query, arguments.Query
	case "attachment_read":
		detail := fmt.Sprintf("Attachment #%d · offset %d · remaining bytes", arguments.AttachmentID, arguments.Offset)
		if arguments.Limit != nil {
			detail = fmt.Sprintf("Attachment #%d · offset %d · up to %d bytes", arguments.AttachmentID, arguments.Offset, *arguments.Limit)
		}
		return "Read " + attachmentName(arguments.AttachmentID), detail
	case "attachment_list":
		return "List attached text files", "Files attached to this conversation"
	case "context_read":
		return "Read saved context", arguments.ArtifactID
	case "context_list":
		return "List saved context", "Notes and outputs saved for this conversation"
	case "context_write":
		return "Save context note", arguments.Name
	default:
		return strings.ReplaceAll(step.ToolName, "_", " "), ""
	}
}

func childToolOutput(step *store.AgentStep) string {
	if step.ToolName == "attachment_read" || step.ToolName == "context_read" {
		var value struct {
			Content *string `json:"content"`
		}
		if json.Unmarshal([]byte(step.Output), &value) == nil && value.Content != nil {
			if *value.Content == "" {
				return "No text in the requested range."
			}
			return *value.Content
		}
	}
	if step.ToolName == "web_search" {
		var response WebSearchResponse
		if json.Unmarshal([]byte(step.Output), &response) == nil && response.Results != nil {
			if len(response.Results) == 0 {
				return "No search results."
			}
			var output strings.Builder
			for _, result := range response.Results {
				fmt.Fprintf(&output, "%s\n%s\n%s\n\n", result.Title, result.URL, result.Content)
			}
			return strings.TrimSpace(output.String())
		}
	}
	return step.Output
}
