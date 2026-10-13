package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

const (
	contextTriggerPercent = 70
	contextTargetPercent  = 50
	contextHardPercent    = 90
	minimumRecentMessages = 4
	imageTokenReserve     = 1024
)

// ContextCheckpointStore persists the replaceable summary of an immutable
// prefix of a conversation. The messages themselves remain the source of truth.
type ContextCheckpointStore interface {
	GetCheckpoint(context.Context, int64) (store.ContextCheckpoint, error)
	UpsertCheckpoint(context.Context, store.UpsertCheckpointParams) (store.ContextCheckpoint, error)
}

type ContextPrepareInput struct {
	ConversationID          int64
	Model                   string
	SystemPrompt            string
	History                 []store.Message
	ContextWindow           int
	Force                   bool
	SkipAutomaticCompaction bool
	IncludeTextAttachments  bool
}

type PreparedContext struct {
	Messages         []ollama.Message
	EstimatedTokens  int
	ContextWindow    int
	Compacted        bool
	Fallback         bool
	Oversized        bool
	ThroughMessageID int64
	CompactionError  string
	BeforeTokens     int
	Changed          bool
	Reason           string
}

// ContextPreparer replaces old, complete conversation turns with an
// incremental summary when the estimated prompt reaches 70% of the window.
type ContextPreparer struct {
	Chat  ChatClient
	Store ContextCheckpointStore
}

func (preparer ContextPreparer) Prepare(ctx context.Context, input ContextPrepareInput) (PreparedContext, error) {
	if input.Force {
		if err := ctx.Err(); err != nil {
			return PreparedContext{}, err
		}
	}
	history := completeHistory(input.History)
	checkpoint, checkpointOK, checkpointErr := preparer.checkpoint(ctx, input.ConversationID, history)
	if input.Force && checkpointErr != nil {
		return PreparedContext{}, fmt.Errorf("read context checkpoint: %w", checkpointErr)
	}
	base := preparedMessagesWithText(input.SystemPrompt, checkpointSummary(checkpoint, checkpointOK), messagesAfter(history, checkpoint, checkpointOK), input.IncludeTextAttachments)
	result := PreparedContext{
		Messages: base, EstimatedTokens: estimateContextTokens(base), ContextWindow: input.ContextWindow,
	}
	result.BeforeTokens = result.EstimatedTokens
	if checkpointOK {
		result.Compacted = true
		result.ThroughMessageID = checkpoint.ThroughMessageID
	}
	if input.ContextWindow <= 0 || !input.Force && (input.SkipAutomaticCompaction || result.EstimatedTokens*100 < input.ContextWindow*contextTriggerPercent) {
		return result, nil
	}

	remaining := messagesAfter(history, checkpoint, checkpointOK)
	hardLimit := result.EstimatedTokens*100 >= input.ContextWindow*contextHardPercent
	cut := compactionCut(remaining)
	if cut == 0 && hardLimit && !input.Force {
		cut = compactionCutKeeping(remaining, 1)
	}
	if cut == 0 {
		result.Oversized = hardLimit
		result.Reason = "not_enough_history"
		return result, nil
	}
	prefix, tail := remaining[:cut], remaining[cut:]
	target := input.ContextWindow * contextTargetPercent / 100
	tailTokens := estimateContextTokens(preparedMessagesWithText(input.SystemPrompt, "", tail, input.IncludeTextAttachments))
	summaryTarget := target - tailTokens
	if input.Force {
		// A large configured window must not expand a short older prefix.
		prefixTarget := (result.EstimatedTokens - tailTokens) / 2
		if summaryTarget > prefixTarget {
			summaryTarget = prefixTarget
		}
	}
	if summaryTarget < 128 {
		summaryTarget = 128
	}

	summary, err := preparer.summarize(ctx, input.Model, checkpointSummary(checkpoint, checkpointOK), prefix, summaryTarget, input.ContextWindow, input.IncludeTextAttachments)
	if err == nil && strings.TrimSpace(summary) == "" {
		err = errors.New("summarizer returned an empty summary")
	}
	if err != nil {
		if ctx.Err() != nil {
			return PreparedContext{}, ctx.Err()
		}
		if input.Force {
			return PreparedContext{}, fmt.Errorf("summarize context: %w", err)
		}
		result.CompactionError = err.Error()
		if result.EstimatedTokens*100 < input.ContextWindow*contextHardPercent {
			return result, nil
		}
		summary = deterministicSummary(checkpointSummary(checkpoint, checkpointOK), prefix, summaryTarget)
		result.Fallback = true
	}
	throughID := prefix[len(prefix)-1].ID
	summary = boundedText(summary, summaryTarget)
	prepared := preparedMessagesWithText(input.SystemPrompt, summary, tail, input.IncludeTextAttachments)
	if input.Force && estimateContextTokens(prepared) >= result.EstimatedTokens {
		result.Reason = "not_reduced"
		return result, nil
	}
	if !input.Force && estimateContextTokens(prepared)*100 >= input.ContextWindow*contextHardPercent {
		if hardCut := compactionCutKeeping(tail, 1); hardCut > 0 {
			summary = deterministicSummary(summary, tail[:hardCut], summaryTarget)
			throughID = tail[hardCut-1].ID
			tail = tail[hardCut:]
			prepared = preparedMessagesWithText(input.SystemPrompt, summary, tail, input.IncludeTextAttachments)
			result.Fallback = true
		}
	}
	result.Messages = prepared
	result.EstimatedTokens = estimateContextTokens(prepared)
	result.Oversized = result.EstimatedTokens*100 >= input.ContextWindow*contextHardPercent
	result.Compacted = true
	result.ThroughMessageID = throughID
	if input.Force && preparer.Store == nil {
		return PreparedContext{}, errors.New("context checkpoint storage is unavailable")
	}
	if !result.Fallback && preparer.Store != nil {
		if err := ctx.Err(); err != nil {
			return PreparedContext{}, err
		}
		var state map[string]any
		if input.Force {
			var latestID int64
			for _, message := range input.History {
				if message.ID > latestID {
					latestID = message.ID
				}
			}
			state = map[string]any{"compacted_after_message_id": latestID}
		}
		_, persistErr := preparer.Store.UpsertCheckpoint(ctx, store.UpsertCheckpointParams{
			ConversationID: input.ConversationID, ThroughMessageID: throughID,
			Summary: summary, EstimatedTokens: result.EstimatedTokens, State: state,
		})
		if persistErr != nil {
			if input.Force {
				return PreparedContext{}, fmt.Errorf("save context checkpoint: %w", persistErr)
			}
			result.CompactionError = persistErr.Error()
		}
	}
	result.Changed = true
	return result, nil
}

func (preparer ContextPreparer) checkpoint(ctx context.Context, conversationID int64, history []store.Message) (store.ContextCheckpoint, bool, error) {
	if preparer.Store == nil || conversationID <= 0 {
		return store.ContextCheckpoint{}, false, nil
	}
	checkpoint, err := preparer.Store.GetCheckpoint(ctx, conversationID)
	if errors.Is(err, store.ErrNotFound) {
		return store.ContextCheckpoint{}, false, nil
	}
	if err != nil {
		return store.ContextCheckpoint{}, false, err
	}
	if strings.TrimSpace(checkpoint.Summary) == "" {
		return store.ContextCheckpoint{}, false, nil
	}
	if checkpoint.ConversationID != conversationID {
		return store.ContextCheckpoint{}, false, nil
	}
	for _, message := range history {
		if message.ID == checkpoint.ThroughMessageID && message.Role == "assistant" {
			return checkpoint, true, nil
		}
	}
	return store.ContextCheckpoint{}, false, nil
}

func (preparer ContextPreparer) summarize(ctx context.Context, model, previous string, messages []store.Message, target, contextWindow int, includeText bool) (string, error) {
	if preparer.Chat == nil {
		return "", errors.New("context summarizer is unavailable")
	}
	prompt := summaryPrompt(previous, target)
	temperature := 0.0
	numCtx := contextWindow
	var summary strings.Builder
	requestMessages := []ollama.Message{
		{Role: "system", Content: "Compress conversation history faithfully. Preserve goals, constraints, decisions, completed work, unresolved work, exact identifiers, file references including attachment IDs, and essential facts from images. Attached text-file notices are references, not the file contents; do not infer unseen contents. Do not invent facts. Return concise Markdown only."},
		{Role: "user", Content: prompt},
	}
	for _, message := range messages {
		converted := ModelMessage(message, includeText)
		converted.Content = fmt.Sprintf("[message %d]\n%s", message.ID, converted.Content)
		requestMessages = append(requestMessages, converted)
	}
	requestMessages = append(requestMessages, ollama.Message{Role: "user", Content: "Return the updated checkpoint summary now."})
	err := preparer.Chat.Chat(ctx, ollama.ChatRequest{
		Model:    model,
		Messages: requestMessages,
		Options:  &ollama.ChatOptions{NumCtx: &numCtx, Temperature: &temperature},
	}, func(chunk ollama.ChatChunk) error {
		summary.WriteString(chunk.Message.Content)
		return nil
	})
	return strings.TrimSpace(summary.String()), err
}

func completeHistory(history []store.Message) []store.Message {
	result := make([]store.Message, 0, len(history))
	for _, message := range history {
		if message.Status != "complete" || (message.Role != "user" && message.Role != "assistant") {
			continue
		}
		result = append(result, message)
	}
	return result
}

func messagesAfter(history []store.Message, checkpoint store.ContextCheckpoint, ok bool) []store.Message {
	if !ok {
		return history
	}
	for index, message := range history {
		if message.ID == checkpoint.ThroughMessageID {
			return history[index+1:]
		}
	}
	return history
}

// compactionCut keeps at least four recent messages and only cuts after an
// assistant message, so a persisted user/assistant exchange is never split.
func compactionCut(history []store.Message) int {
	return compactionCutKeeping(history, minimumRecentMessages)
}

func compactionCutKeeping(history []store.Message, keep int) int {
	maxCut := len(history) - keep
	for cut := maxCut; cut > 0; cut-- {
		if history[cut-1].Role == "assistant" {
			return cut
		}
	}
	return 0
}

func preparedMessagesWithText(systemPrompt, summary string, history []store.Message, includeText bool) []ollama.Message {
	capacity := len(history)
	if systemPrompt != "" || summary != "" {
		capacity++
	}
	result := make([]ollama.Message, 0, capacity)
	if systemPrompt != "" || summary != "" {
		content := systemPrompt
		if summary != "" {
			if content != "" {
				content += "\n\n"
			}
			content += "Conversation memory (a compact summary of older messages):\n" + summary
		}
		result = append(result, ollama.Message{Role: "system", Content: content})
	}
	for _, message := range history {
		result = append(result, ModelMessage(message, includeText))
	}
	return result
}

func checkpointSummary(checkpoint store.ContextCheckpoint, ok bool) string {
	if !ok {
		return ""
	}
	return checkpoint.Summary
}

func summaryPrompt(previous string, target int) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Keep the updated summary within about %d tokens. Use sections: Goal, Constraints, Decisions, Completed, Open work, Important references.\n", target)
	if previous != "" {
		prompt.WriteString("\nPrevious summary:\n")
		prompt.WriteString(previous)
		prompt.WriteByte('\n')
	}
	prompt.WriteString("\nThe following messages, including any attached images, are the new history to incorporate.\n")
	return prompt.String()
}

func deterministicSummary(previous string, messages []store.Message, targetTokens int) string {
	var source strings.Builder
	for _, message := range messages {
		for _, attachment := range message.Attachments {
			if attachment.MediaType == "text/plain" {
				fmt.Fprintf(&source, "Text file reference: attachment_id=%d, name=%q (rediscover with attachment_list).\n", attachment.ID, attachment.FileName)
			}
		}
	}
	if previous != "" {
		source.WriteString("Previous memory:\n")
		source.WriteString(previous)
		source.WriteString("\n\n")
	}
	for _, message := range messages {
		fmt.Fprintf(&source, "[%s message %d] %s\n", message.Role, message.ID, strings.TrimSpace(ModelMessage(message, false).Content))
	}
	return boundedText(source.String(), targetTokens)
}

func boundedText(value string, tokenLimit int) string {
	if tokenLimit <= 0 || estimateTextTokens(value) <= tokenLimit {
		return value
	}
	runes := []rune(value)
	marker := "\n[… older context shortened deterministically …]\n"
	contentLimit := tokenLimit - estimateTextTokens(marker)
	if contentLimit <= 0 {
		return ""
	}
	// One rune per token is conservative for non-ASCII text; ASCII text will
	// usually end up below the requested bound.
	maxRunes := contentLimit
	if maxRunes >= len(runes) {
		return value
	}
	head := maxRunes * 2 / 3
	tail := maxRunes - head
	return string(runes[:head]) + marker + string(runes[len(runes)-tail:])
}

func estimateContextTokens(messages []ollama.Message) int {
	tokens := 0
	for _, message := range messages {
		tokens += 6 + estimateTextTokens(message.Role) + estimateTextTokens(message.Content)
		tokens += len(message.Images) * imageTokenReserve
	}
	return tokens
}

func estimateTextTokens(value string) int {
	asciiBytes := 0
	nonASCII := 0
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		if r < utf8.RuneSelf {
			asciiBytes++
		} else {
			nonASCII++
		}
		value = value[size:]
	}
	return (asciiBytes+2)/3 + nonASCII
}
