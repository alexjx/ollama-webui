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
	ConversationID int64
	Model          string
	SystemPrompt   string
	History        []store.Message
	ContextWindow  int
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
}

// ContextPreparer replaces old, complete conversation turns with an
// incremental summary when the estimated prompt reaches 70% of the window.
type ContextPreparer struct {
	Chat  ChatClient
	Store ContextCheckpointStore
}

func (preparer ContextPreparer) Prepare(ctx context.Context, input ContextPrepareInput) (PreparedContext, error) {
	history := completeHistory(input.History)
	checkpoint, checkpointOK := preparer.checkpoint(ctx, input.ConversationID, history)
	base := preparedMessages(input.SystemPrompt, checkpointSummary(checkpoint, checkpointOK), messagesAfter(history, checkpoint, checkpointOK))
	result := PreparedContext{
		Messages: base, EstimatedTokens: estimateContextTokens(base), ContextWindow: input.ContextWindow,
	}
	if checkpointOK {
		result.Compacted = true
		result.ThroughMessageID = checkpoint.ThroughMessageID
	}
	if input.ContextWindow <= 0 || result.EstimatedTokens*100 < input.ContextWindow*contextTriggerPercent {
		return result, nil
	}

	remaining := messagesAfter(history, checkpoint, checkpointOK)
	hardLimit := result.EstimatedTokens*100 >= input.ContextWindow*contextHardPercent
	cut := compactionCut(remaining)
	if cut == 0 && hardLimit {
		cut = compactionCutKeeping(remaining, 1)
	}
	if cut == 0 {
		result.Oversized = hardLimit
		return result, nil
	}
	prefix, tail := remaining[:cut], remaining[cut:]
	target := input.ContextWindow * contextTargetPercent / 100
	summaryTarget := target - estimateContextTokens(preparedMessages(input.SystemPrompt, "", tail))
	if summaryTarget < 128 {
		summaryTarget = 128
	}

	summary, err := preparer.summarize(ctx, input.Model, checkpointSummary(checkpoint, checkpointOK), prefix, summaryTarget, input.ContextWindow)
	if err == nil && strings.TrimSpace(summary) == "" {
		err = errors.New("summarizer returned an empty summary")
	}
	if err != nil {
		if ctx.Err() != nil {
			return PreparedContext{}, ctx.Err()
		}
		result.CompactionError = err.Error()
		if result.EstimatedTokens*100 < input.ContextWindow*contextHardPercent {
			return result, nil
		}
		summary = deterministicSummary(checkpointSummary(checkpoint, checkpointOK), prefix, summaryTarget)
		result.Fallback = true
	}
	summary = boundedText(summary, summaryTarget)

	throughID := prefix[len(prefix)-1].ID
	summary = boundedText(summary, summaryTarget)
	prepared := preparedMessages(input.SystemPrompt, summary, tail)
	if estimateContextTokens(prepared)*100 >= input.ContextWindow*contextHardPercent {
		if hardCut := compactionCutKeeping(tail, 1); hardCut > 0 {
			summary = deterministicSummary(summary, tail[:hardCut], summaryTarget)
			throughID = tail[hardCut-1].ID
			tail = tail[hardCut:]
			prepared = preparedMessages(input.SystemPrompt, summary, tail)
			result.Fallback = true
		}
	}
	result.Messages = prepared
	result.EstimatedTokens = estimateContextTokens(prepared)
	result.Oversized = result.EstimatedTokens*100 >= input.ContextWindow*contextHardPercent
	result.Compacted = true
	result.ThroughMessageID = throughID
	if !result.Fallback && preparer.Store != nil {
		_, persistErr := preparer.Store.UpsertCheckpoint(ctx, store.UpsertCheckpointParams{
			ConversationID: input.ConversationID, ThroughMessageID: throughID,
			Summary: summary, EstimatedTokens: result.EstimatedTokens,
		})
		if persistErr != nil {
			result.CompactionError = persistErr.Error()
		}
	}
	return result, nil
}

func (preparer ContextPreparer) checkpoint(ctx context.Context, conversationID int64, history []store.Message) (store.ContextCheckpoint, bool) {
	if preparer.Store == nil || conversationID <= 0 {
		return store.ContextCheckpoint{}, false
	}
	checkpoint, err := preparer.Store.GetCheckpoint(ctx, conversationID)
	if err != nil || strings.TrimSpace(checkpoint.Summary) == "" {
		return store.ContextCheckpoint{}, false
	}
	if checkpoint.ConversationID != conversationID {
		return store.ContextCheckpoint{}, false
	}
	for _, message := range history {
		if message.ID == checkpoint.ThroughMessageID && message.Role == "assistant" {
			return checkpoint, true
		}
	}
	return store.ContextCheckpoint{}, false
}

func (preparer ContextPreparer) summarize(ctx context.Context, model, previous string, messages []store.Message, target, contextWindow int) (string, error) {
	if preparer.Chat == nil {
		return "", errors.New("context summarizer is unavailable")
	}
	prompt := summaryPrompt(previous, target)
	temperature := 0.0
	numCtx := contextWindow
	var summary strings.Builder
	requestMessages := []ollama.Message{
		{Role: "system", Content: "Compress conversation history faithfully. Preserve goals, constraints, decisions, completed work, unresolved work, exact identifiers, file references, and essential facts from images. Do not invent facts. Return concise Markdown only."},
		{Role: "user", Content: prompt},
	}
	for _, message := range messages {
		images := make([][]byte, 0, len(message.Attachments))
		for _, attachment := range message.Attachments {
			images = append(images, attachment.Data)
		}
		requestMessages = append(requestMessages, ollama.Message{
			Role: message.Role, Content: fmt.Sprintf("[message %d]\n%s", message.ID, message.Content), Images: images,
		})
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

func preparedMessages(systemPrompt, summary string, history []store.Message) []ollama.Message {
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
		images := make([][]byte, 0, len(message.Attachments))
		for _, attachment := range message.Attachments {
			images = append(images, attachment.Data)
		}
		result = append(result, ollama.Message{Role: message.Role, Content: message.Content, Images: images})
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
	if previous != "" {
		source.WriteString("Previous memory:\n")
		source.WriteString(previous)
		source.WriteString("\n\n")
	}
	for _, message := range messages {
		fmt.Fprintf(&source, "[%s message %d] %s\n", message.Role, message.ID, strings.TrimSpace(message.Content))
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
