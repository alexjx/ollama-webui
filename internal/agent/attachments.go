package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

const maxAttachmentReadBytes = 8192

// AttachmentStore exposes only text files owned by a conversation.
type AttachmentStore interface {
	ListTextAttachments(context.Context, int64) ([]store.Attachment, error)
	ReadTextAttachment(context.Context, int64, int64, int64, int64) (store.Attachment, error)
}

var attachmentListTool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name:        "attachment_list",
		Description: "List user-attached text files in this conversation, including older files whose messages were compacted. Use attachment_read to inspect a file.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"cursor":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":20}},"additionalProperties":false}`),
	},
}

var attachmentReadTool = ollama.Tool{
	Type: "function",
	Function: ollama.ToolFunction{
		Name:        "attachment_read",
		Description: "Read a bounded UTF-8 byte range from a user-attached text file in this conversation. Continue from next_offset when eof is false. Read only relevant ranges instead of dumping the whole file.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"attachment_id":{"type":"integer","minimum":1},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":8192}},"required":["attachment_id"],"additionalProperties":false}`),
	},
}

// ModelMessage is the single translation boundary for stored attachments.
// Chat models receive file text; agents and summaries receive stable references.
func ModelMessage(message store.Message, includeText bool) ollama.Message {
	result := ollama.Message{Role: message.Role, Content: message.Content}
	for _, attachment := range message.Attachments {
		if strings.HasPrefix(attachment.MediaType, "image/") {
			result.Images = append(result.Images, attachment.Data)
			continue
		}
		if attachment.MediaType != "text/plain" {
			continue
		}
		if result.Content != "" {
			result.Content += "\n\n"
		}
		if includeText {
			result.Content += fmt.Sprintf("[Attached text file %q, attachment_id=%d]\n%s\n[End attached text file]", attachment.FileName, attachment.ID, string(attachment.Data))
		} else {
			result.Content += fmt.Sprintf("[User-attached text file %q: attachment_id=%d, %d bytes. Use attachment_read with this ID and bounded byte ranges to inspect the relevant contents; attachment_list can rediscover this file later.]", attachment.FileName, attachment.ID, attachment.Size)
		}
	}
	return result
}

func MessageImageCount(message store.Message) int {
	count := 0
	for _, attachment := range message.Attachments {
		if strings.HasPrefix(attachment.MediaType, "image/") {
			count++
		}
	}
	return count
}

type attachmentReadArguments struct {
	AttachmentID int64 `json:"attachment_id"`
	Offset       int64 `json:"offset,omitempty"`
	Limit        int64 `json:"limit,omitempty"`
}

func (runner Runner) executeAttachmentTool(ctx context.Context, conversationID int64, name string, raw json.RawMessage) (ShellResult, string) {
	fail := func(err error) (ShellResult, string) {
		return ShellResult{ExitCode: -1, Output: err.Error()}, "error"
	}
	feedbackLimit := runner.ToolFeedbackLimit
	if feedbackLimit <= 0 {
		feedbackLimit = defaultToolFeedbackBytes
	}
	encode := func(value any) (ShellResult, string) {
		data, err := json.Marshal(value)
		if err != nil {
			return fail(err)
		}
		if len(data) > feedbackLimit {
			return fail(errors.New("attachment response exceeds the tool feedback budget; increase AGENT_TOOL_FEEDBACK_BYTES"))
		}
		return ShellResult{Output: string(data)}, "complete"
	}
	switch name {
	case "attachment_list":
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
		attachments, err := runner.Attachments.ListTextAttachments(ctx, conversationID)
		if err != nil {
			return fail(err)
		}
		start := min(arguments.Cursor, len(attachments))
		end := min(start+arguments.Limit, len(attachments))
		for {
			items := make([]map[string]any, 0, end-start)
			for _, attachment := range attachments[start:end] {
				items = append(items, map[string]any{"attachment_id": attachment.ID, "message_id": attachment.MessageID, "name": attachment.FileName, "bytes": attachment.Size})
			}
			value := map[string]any{"items": items, "total": len(attachments), "cursor": start, "next_cursor": end, "has_more": end < len(attachments)}
			result, status := encode(value)
			if status == "complete" || end == start || end == start+1 {
				return result, status
			}
			end--
		}
	case "attachment_read":
		var arguments attachmentReadArguments
		if err := decodeToolArguments(raw, &arguments); err != nil {
			return fail(err)
		}
		if arguments.AttachmentID <= 0 || arguments.Offset < 0 || arguments.Limit < 0 || arguments.Limit > maxAttachmentReadBytes {
			return fail(errors.New("attachment_id must be positive, offset non-negative, and limit between 1 and 8192 bytes"))
		}
		if arguments.Limit == 0 {
			arguments.Limit = 4096
		}
		// JSON can escape each input byte into six bytes. Reserve metadata space
		// so next_offset is never hidden by the outer feedback truncation.
		maxPage := (feedbackLimit - 512) / 6
		if maxPage < utf8.UTFMax {
			return fail(errors.New("tool feedback budget is too small for attachment reads"))
		}
		arguments.Limit = min(arguments.Limit, int64(maxPage))
		attachment, err := runner.Attachments.ReadTextAttachment(ctx, conversationID, arguments.AttachmentID, arguments.Offset, arguments.Limit)
		if err != nil {
			return fail(err)
		}
		if arguments.Offset > int64(attachment.Size) {
			return fail(errors.New("attachment offset exceeds the file size"))
		}
		if len(attachment.Data) > 0 && !utf8.RuneStart(attachment.Data[0]) {
			return fail(errors.New("attachment offset must be on a UTF-8 character boundary"))
		}
		data := attachment.Data
		if arguments.Offset+int64(len(data)) == int64(attachment.Size) && !utf8.Valid(data) {
			return fail(errors.New("text attachment is not valid UTF-8"))
		}
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
		end := arguments.Offset + int64(len(data))
		if len(data) == 0 && end < int64(attachment.Size) {
			return fail(errors.New("attachment limit is too small for the next UTF-8 character"))
		}
		return encode(map[string]any{"attachment_id": attachment.ID, "content": string(data), "offset": arguments.Offset, "next_offset": end, "eof": end == int64(attachment.Size), "bytes": len(data)})
	default:
		return fail(fmt.Errorf("unknown attachment tool %q", name))
	}
}
