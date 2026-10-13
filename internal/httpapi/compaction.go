package httpapi

import (
	"context"
	"errors"
	"net/http"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/store"
)

func (s *Server) contextCheckpoint(ctx context.Context, id int64) (*store.ContextCheckpoint, error) {
	checkpoint, err := s.store.GetCheckpoint(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &checkpoint, nil
}

func (s *Server) compactConversation(response http.ResponseWriter, request *http.Request) {
	id, ok := pathID(response, request)
	if !ok {
		return
	}
	s.activeMu.Lock()
	if _, active := s.active[id]; active {
		s.activeMu.Unlock()
		writeError(response, http.StatusConflict, "generation_active", "wait for the active response or context compaction to finish")
		return
	}
	s.active[id] = struct{}{}
	s.activeMu.Unlock()
	defer func() {
		s.activeMu.Lock()
		delete(s.active, id)
		s.activeMu.Unlock()
	}()

	conversation, err := s.store.GetConversation(request.Context(), id)
	if err != nil {
		s.storeError(response, err)
		return
	}
	history, err := s.store.MessagesWithAttachmentData(request.Context(), id)
	if err != nil {
		s.internalError(response, err)
		return
	}
	agentMode := conversation.Mode != "chat"
	systemPrompt := conversation.SystemPrompt
	if agentMode {
		systemPrompt = agent.SystemPrompt
		if conversation.SystemPrompt != "" {
			systemPrompt += "\n\nAdditional instructions from the user:\n" + conversation.SystemPrompt
		}
	}
	window := s.runtime.ContextTokens
	if conversation.ContextWindow != nil {
		window = *conversation.ContextWindow
	}
	if window <= 0 {
		// Sizing for the summarizer only; Chat's inherited limit remains unknown.
		window = 32768
	}
	preparer := s.agent.Context
	if preparer == nil {
		preparer = &agent.ContextPreparer{Chat: s.ollama, Store: s.store}
	}
	prepared, err := preparer.Prepare(request.Context(), agent.ContextPrepareInput{
		ConversationID: id, Model: conversation.Model, SystemPrompt: systemPrompt,
		History: history, ContextWindow: window, Force: true, IncludeTextAttachments: !agentMode,
	})
	if err != nil {
		writeError(response, http.StatusBadGateway, "context_compaction_failed", err.Error())
		return
	}
	checkpoint, err := s.contextCheckpoint(request.Context(), id)
	if err != nil {
		s.internalError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"compacted": prepared.Changed, "before_tokens": prepared.BeforeTokens,
		"estimated_tokens": prepared.EstimatedTokens, "reason": prepared.Reason,
		"context_checkpoint": checkpoint,
	})
}
