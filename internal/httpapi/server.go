package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"
	"ollama-webui/internal/agent"
	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

type Server struct {
	store    *store.Store
	ollama   *ollama.Client
	agent    agent.Runner
	runtime  RuntimeSettings
	webDir   string
	logger   *slog.Logger
	handler  http.Handler
	activeMu sync.Mutex
	active   map[int64]struct{}
}

type RuntimeSettings struct {
	Workspace             string
	MaxTurns              int
	ShellTimeout          time.Duration
	ShellMaxOutput        int
	InlineInputMax        int
	ToolFeedbackLimit     int
	ContextDirectory      string
	ContextTokens         int
	SubagentsEnabled      bool
	SubagentConcurrency   int
	SubagentContextTokens int
	SubagentMaxTurns      int
	SubagentResultBytes   int
}

func New(database *store.Store, ollamaClient *ollama.Client, runner agent.Runner, runtime RuntimeSettings, webDir string, logger *slog.Logger) *Server {
	server := &Server{store: database, ollama: ollamaClient, agent: runner, runtime: runtime, webDir: webDir, logger: logger, active: make(map[int64]struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", server.health)
	mux.HandleFunc("GET /api/settings", server.settings)
	mux.HandleFunc("GET /api/models", server.models)
	mux.HandleFunc("GET /api/conversations", server.listConversations)
	mux.HandleFunc("POST /api/conversations", server.createConversation)
	mux.HandleFunc("DELETE /api/conversations", server.clearConversations)
	mux.HandleFunc("GET /api/conversations/{id}", server.getConversation)
	mux.HandleFunc("PATCH /api/conversations/{id}", server.updateConversation)
	mux.HandleFunc("PUT /api/conversations/{id}/model", server.updateConversationModel)
	mux.HandleFunc("DELETE /api/conversations/{id}", server.deleteConversation)
	mux.HandleFunc("POST /api/conversations/{id}/messages", server.generate)
	mux.HandleFunc("DELETE /api/conversations/{id}/messages/{messageID}", server.deleteMessagesFrom)
	mux.HandleFunc("GET /api/attachments/{id}", server.attachment)
	mux.HandleFunc("/", server.frontend)
	server.handler = requestLogger(logger, mux)
	return server
}

func (s *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	s.handler.ServeHTTP(response, request)
}

func (s *Server) health(response http.ResponseWriter, request *http.Request) {
	status := "ok"
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	if _, err := s.ollama.ListModels(ctx); err != nil {
		status = "unavailable"
	}
	writeJSON(response, http.StatusOK, map[string]any{"status": "ok", "ollama": map[string]string{"status": status}})
}

func (s *Server) models(response http.ResponseWriter, request *http.Request) {
	models, err := s.ollama.ListModels(request.Context())
	if err != nil {
		writeError(response, http.StatusBadGateway, "ollama_unavailable", err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"models": models})
}

func (s *Server) settings(response http.ResponseWriter, request *http.Request) {
	stats, err := s.store.Stats(request.Context())
	if err != nil {
		s.internalError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"agent": map[string]any{
			"workspace":               s.runtime.Workspace,
			"max_turns":               s.runtime.MaxTurns,
			"shell_timeout_seconds":   int(s.runtime.ShellTimeout.Seconds()),
			"shell_max_output_bytes":  s.runtime.ShellMaxOutput,
			"inline_input_bytes":      s.runtime.InlineInputMax,
			"tool_feedback_bytes":     s.runtime.ToolFeedbackLimit,
			"context_path":            s.runtime.ContextDirectory,
			"context_budget_tokens":   s.runtime.ContextTokens,
			"subagents_enabled":       s.runtime.SubagentsEnabled,
			"subagent_concurrency":    s.runtime.SubagentConcurrency,
			"subagent_context_tokens": s.runtime.SubagentContextTokens,
			"subagent_max_turns":      s.runtime.SubagentMaxTurns,
			"subagent_result_bytes":   s.runtime.SubagentResultBytes,
		},
		"storage": stats,
	})
}

func (s *Server) listConversations(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListConversations(request.Context(), request.URL.Query().Get("q"))
	if err != nil {
		s.internalError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) clearConversations(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(request, &input); err != nil || input.Confirmation != "DELETE" {
		writeError(response, http.StatusBadRequest, "confirmation_required", "confirmation must be DELETE")
		return
	}
	s.activeMu.Lock()
	if len(s.active) > 0 {
		s.activeMu.Unlock()
		writeError(response, http.StatusConflict, "generation_active", "stop active responses before clearing conversations")
		return
	}
	contexts, err := s.store.ListConversationContexts(request.Context())
	if err != nil {
		s.activeMu.Unlock()
		s.internalError(response, err)
		return
	}
	deleted, err := s.store.ClearConversations(request.Context())
	s.activeMu.Unlock()
	if err != nil {
		s.internalError(response, err)
		return
	}
	if s.agent.Artifacts != nil {
		for _, item := range contexts {
			if err := s.agent.Artifacts.RemoveConversation(item.StorageKey); err != nil {
				s.logger.Warn("remove cleared conversation context", "error", err, "conversation_id", item.ConversationID)
			}
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"deleted": deleted})
}

type conversationInput struct {
	Title           string   `json:"title"`
	Model           string   `json:"model"`
	Mode            string   `json:"mode"`
	SystemPrompt    string   `json:"system_prompt"`
	ContextWindow   *int     `json:"context_window"`
	ThinkingMode    *string  `json:"thinking_mode"`
	ThinkingEnabled *bool    `json:"thinking_enabled"`
	Temperature     *float64 `json:"temperature"`
}

func (s *Server) createConversation(response http.ResponseWriter, request *http.Request) {
	var input conversationInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := validateConversation(input); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_settings", err.Error())
		return
	}
	thinkingMode := normalizedThinkingMode(input)
	conversation, err := s.store.CreateConversation(request.Context(), store.CreateConversationParams{
		Title: input.Title, Model: input.Model, Mode: input.Mode, SystemPrompt: input.SystemPrompt,
		ContextWindow: input.ContextWindow, ThinkingMode: thinkingMode, Temperature: input.Temperature,
	})
	if err != nil {
		s.internalError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, conversation)
}

func (s *Server) getConversation(response http.ResponseWriter, request *http.Request) {
	id, ok := pathID(response, request)
	if !ok {
		return
	}
	conversation, err := s.store.GetConversation(request.Context(), id)
	if err != nil {
		s.storeError(response, err)
		return
	}
	messages, err := s.store.Messages(request.Context(), id)
	if err != nil {
		s.internalError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"conversation": conversation, "messages": messages})
}

func (s *Server) updateConversation(response http.ResponseWriter, request *http.Request) {
	id, ok := pathID(response, request)
	if !ok {
		return
	}
	var input struct {
		Title string `json:"title"`
	}
	if err := decodeJSON(request, &input); err != nil || strings.TrimSpace(input.Title) == "" {
		writeError(response, http.StatusBadRequest, "invalid_request", "title is required")
		return
	}
	if err := s.store.UpdateTitle(request.Context(), id, input.Title); err != nil {
		s.storeError(response, err)
		return
	}
	conversation, _ := s.store.GetConversation(request.Context(), id)
	writeJSON(response, http.StatusOK, conversation)
}

func (s *Server) updateConversationModel(response http.ResponseWriter, request *http.Request) {
	id, ok := pathID(response, request)
	if !ok {
		return
	}
	var input struct {
		Model string `json:"model"`
	}
	if err := decodeJSON(request, &input); err != nil || strings.TrimSpace(input.Model) == "" {
		writeError(response, http.StatusUnprocessableEntity, "invalid_model", "model is required")
		return
	}
	input.Model = strings.TrimSpace(input.Model)
	if _, err := s.store.GetConversation(request.Context(), id); err != nil {
		s.storeError(response, err)
		return
	}
	// Fetch capabilities before taking the generation lock: Ollama may be slow.
	models, err := s.ollama.ListModels(request.Context())
	if err != nil {
		writeError(response, http.StatusBadGateway, "ollama_unavailable", err.Error())
		return
	}
	var selected *ollama.Model
	for _, model := range models {
		if model.Name == input.Model {
			selected = &model
			break
		}
	}
	if selected == nil {
		writeError(response, http.StatusUnprocessableEntity, "model_unavailable", "choose an installed chat model")
		return
	}
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if _, active := s.active[id]; active {
		writeError(response, http.StatusConflict, "generation_active", "stop the active response and wait for it to finish before changing models")
		return
	}
	conversation, err := s.store.GetConversation(request.Context(), id)
	if err != nil {
		s.storeError(response, err)
		return
	}
	if conversation.Mode != "chat" && !containsCapability(selected.Capabilities, "tools") {
		writeError(response, http.StatusUnprocessableEntity, "tools_unsupported", "choose a model that supports tools for this agent session")
		return
	}
	history, err := s.store.Messages(request.Context(), id)
	if err != nil {
		s.internalError(response, err)
		return
	}
	imageLimit := imageLimitForModel(*selected)
	for _, message := range history {
		if len(message.Attachments) > imageLimit {
			writeError(response, http.StatusUnprocessableEntity, "vision_unsupported", "choose a vision model that supports the images already in this session")
			return
		}
	}
	if err := s.store.UpdateModel(request.Context(), id, input.Model); err != nil {
		s.storeError(response, err)
		return
	}
	conversation, err = s.store.GetConversation(request.Context(), id)
	if err != nil {
		s.storeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, conversation)
}

func (s *Server) deleteConversation(response http.ResponseWriter, request *http.Request) {
	id, ok := pathID(response, request)
	if !ok {
		return
	}
	s.activeMu.Lock()
	if _, active := s.active[id]; active {
		s.activeMu.Unlock()
		writeError(response, http.StatusConflict, "generation_active", "stop the active response before deleting this conversation")
		return
	}
	storageKey := ""
	if item, err := s.store.GetConversationContext(request.Context(), id); err == nil {
		storageKey = item.StorageKey
	} else if !errors.Is(err, store.ErrNotFound) {
		s.activeMu.Unlock()
		s.internalError(response, err)
		return
	}
	if err := s.store.DeleteConversation(request.Context(), id); err != nil {
		s.activeMu.Unlock()
		s.storeError(response, err)
		return
	}
	s.activeMu.Unlock()
	if storageKey != "" && s.agent.Artifacts != nil {
		if err := s.agent.Artifacts.RemoveConversation(storageKey); err != nil {
			s.logger.Warn("remove conversation context", "error", err, "conversation_id", id)
		}
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteMessagesFrom(response http.ResponseWriter, request *http.Request) {
	id, ok := pathID(response, request)
	if !ok {
		return
	}
	messageID, err := strconv.ParseInt(request.PathValue("messageID"), 10, 64)
	if err != nil || messageID <= 0 {
		writeError(response, http.StatusBadRequest, "invalid_id", "message id must be a positive integer")
		return
	}
	// Hold the generation lock through deletion and the response snapshot so a
	// new response cannot start against history that is being removed.
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if _, active := s.active[id]; active {
		writeError(response, http.StatusConflict, "generation_active", "stop the active response and wait for it to finish before deleting turns")
		return
	}
	storageKey := ""
	if item, err := s.store.GetConversationContext(request.Context(), id); err == nil {
		storageKey = item.StorageKey
	} else if !errors.Is(err, store.ErrNotFound) {
		s.internalError(response, err)
		return
	}
	keys, err := s.store.DeleteMessagesFrom(request.Context(), id, messageID)
	if errors.Is(err, store.ErrInvalidTurn) {
		writeError(response, http.StatusBadRequest, "invalid_turn", err.Error())
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "conversation or message not found")
		return
	}
	if err != nil {
		s.internalError(response, err)
		return
	}
	if storageKey != "" && s.agent.Artifacts != nil {
		for _, key := range keys {
			if err := s.agent.Artifacts.RemoveFile(storageKey, key); err != nil {
				s.logger.Warn("remove deleted turn artifact", "error", err, "conversation_id", id)
			}
		}
	}
	s.getConversation(response, request)
}

func (s *Server) generate(response http.ResponseWriter, request *http.Request) {
	id, ok := pathID(response, request)
	if !ok {
		return
	}
	s.activeMu.Lock()
	if _, active := s.active[id]; active {
		s.activeMu.Unlock()
		writeError(response, http.StatusConflict, "generation_active", "a response is already running for this conversation")
		return
	}
	s.active[id] = struct{}{}
	s.activeMu.Unlock()
	defer func() {
		s.activeMu.Lock()
		delete(s.active, id)
		s.activeMu.Unlock()
	}()
	var input struct {
		Content string       `json:"content"`
		Images  []imageInput `json:"images"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	attachments, err := decodeImages(input.Images)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_image", err.Error())
		return
	}
	if strings.TrimSpace(input.Content) == "" && len(attachments) == 0 {
		writeError(response, http.StatusBadRequest, "invalid_request", "message content or an image is required")
		return
	}
	conversation, err := s.store.GetConversation(request.Context(), id)
	if err != nil {
		s.storeError(response, err)
		return
	}
	modelInfo, err := s.installedModel(request.Context(), conversation.Model)
	if err != nil {
		writeError(response, http.StatusBadGateway, "ollama_unavailable", err.Error())
		return
	}
	if len(modelInfo.Capabilities) > 0 && !containsCapability(modelInfo.Capabilities, "completion") {
		writeError(response, http.StatusUnprocessableEntity, "completion_unsupported", "the selected model does not support chat completion")
		return
	}
	agentMode := conversation.Mode != "chat"
	if agentMode && !containsCapability(modelInfo.Capabilities, "tools") {
		writeError(response, http.StatusUnprocessableEntity, "tools_unsupported", "choose Chat mode because the selected model does not support tools")
		return
	}
	if conversation.ThinkingMode != nil && !containsCapability(modelInfo.Capabilities, "thinking") {
		writeError(response, http.StatusUnprocessableEntity, "thinking_unsupported", "the selected model does not support thinking overrides")
		return
	}
	if len(attachments) > 0 {
		imageLimit := imageLimitForModel(modelInfo)
		if imageLimit == 0 {
			writeError(response, http.StatusUnprocessableEntity, "vision_unsupported", "the selected model does not support image input")
			return
		}
		if len(attachments) > imageLimit {
			writeError(response, http.StatusUnprocessableEntity, "too_many_images",
				fmt.Sprintf("the selected model accepts at most %d image per message", imageLimit))
			return
		}
	}
	userMessage, err := s.store.AddMessageWithAttachments(request.Context(), id, "user",
		input.Content, "complete", attachments)
	if err != nil {
		s.internalError(response, err)
		return
	}
	assistantMessage, err := s.store.AddMessage(request.Context(), id, "assistant", "", "streaming")
	if err != nil {
		s.internalError(response, err)
		return
	}
	history, err := s.store.MessagesWithAttachmentData(request.Context(), id)
	if err != nil {
		s.internalError(response, err)
		return
	}

	flusher, ok := response.(http.Flusher)
	if !ok {
		writeError(response, http.StatusInternalServerError, "streaming_unsupported", "streaming is unavailable")
		return
	}
	response.Header().Set("Content-Type", "application/x-ndjson")
	response.Header().Set("Cache-Control", "no-cache")
	response.WriteHeader(http.StatusOK)
	writeEvent(response, flusher, map[string]any{"type": "started", "user_message": userMessage, "assistant_message": assistantMessage})

	systemPrompt := conversation.SystemPrompt
	if agentMode {
		systemPrompt = agent.SystemPrompt
		if conversation.SystemPrompt != "" {
			systemPrompt += "\n\nAdditional instructions from the user:\n" + conversation.SystemPrompt
		}
	}
	upstreamMessages := make([]ollama.Message, 0, len(history)+1)
	var preparedContext agent.PreparedContext
	if agentMode && s.agent.Context != nil {
		contextWindow := s.runtime.ContextTokens
		if conversation.ContextWindow != nil {
			contextWindow = *conversation.ContextWindow
		}
		prepared, prepareErr := s.agent.Context.Prepare(request.Context(), agent.ContextPrepareInput{
			ConversationID: conversation.ID, Model: conversation.Model, SystemPrompt: systemPrompt,
			History: history, ContextWindow: contextWindow,
		})
		if prepareErr != nil {
			status := "error"
			if errors.Is(prepareErr, context.Canceled) || errors.Is(request.Context().Err(), context.Canceled) {
				status = "cancelled"
			}
			s.persistStreamResult(assistantMessage.ID, "", "", status, map[string]any{"context_compaction_failed": true})
			if status == "error" {
				_ = writeEvent(response, flusher, map[string]any{"type": "error", "message": prepareErr.Error()})
			}
			return
		}
		preparedContext = prepared
		if prepared.CompactionError != "" || prepared.Fallback || prepared.Oversized {
			s.logger.Warn("agent context compaction degraded", "conversation_id", conversation.ID,
				"fallback", prepared.Fallback, "oversized", prepared.Oversized, "error", prepared.CompactionError)
		}
		upstreamMessages = prepared.Messages
	} else {
		if systemPrompt != "" {
			upstreamMessages = append(upstreamMessages, ollama.Message{Role: "system", Content: systemPrompt})
		}
		for _, message := range history {
			if message.ID == assistantMessage.ID || message.Status == "error" || message.Status == "cancelled" {
				continue
			}
			images := make([][]byte, 0, len(message.Attachments))
			for _, attachment := range message.Attachments {
				images = append(images, attachment.Data)
			}
			upstreamMessages = append(upstreamMessages, ollama.Message{Role: message.Role, Content: message.Content, Images: images})
		}
	}
	var options *ollama.ChatOptions
	effectiveContextWindow := conversation.ContextWindow
	if effectiveContextWindow == nil && agentMode && s.runtime.ContextTokens > 0 {
		value := s.runtime.ContextTokens
		effectiveContextWindow = &value
	}
	if effectiveContextWindow != nil || conversation.Temperature != nil {
		options = &ollama.ChatOptions{NumCtx: effectiveContextWindow, Temperature: conversation.Temperature}
	}
	var think *ollama.ThinkValue
	if conversation.ThinkingMode != nil {
		value := ollama.ThinkValue(*conversation.ThinkingMode)
		think = &value
	}
	result, err := s.agent.Run(request.Context(), agent.RunInput{
		ConversationID: conversation.ID, Model: conversation.Model, Mode: conversation.Mode, Messages: upstreamMessages, Options: options,
		Think:              think,
		Vision:             containsCapability(modelInfo.Capabilities, "vision"),
		AssistantMessageID: assistantMessage.ID,
	}, func(event agent.Event) error {
		return writeEvent(response, flusher, event)
	})
	result.Metadata = withContextMetadata(result.Metadata, preparedContext)
	if err != nil {
		status := "error"
		if errors.Is(err, context.Canceled) || errors.Is(request.Context().Err(), context.Canceled) {
			status = "cancelled"
		}
		s.persistStreamResult(assistantMessage.ID, result.Content, result.Thinking, status, result.Metadata)
		if status == "error" {
			_ = writeEvent(response, flusher, map[string]any{"type": "error", "message": err.Error()})
		}
		return
	}
	s.persistStreamResult(assistantMessage.ID, result.Content, result.Thinking, "complete", result.Metadata)
	_ = writeEvent(response, flusher, map[string]any{"type": "done", "metadata": result.Metadata})
}

func (s *Server) attachment(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, "invalid_id", "attachment id must be a positive integer")
		return
	}
	attachment, err := s.store.GetAttachment(request.Context(), id)
	if err != nil {
		s.storeError(response, err)
		return
	}
	response.Header().Set("Content-Type", attachment.MediaType)
	response.Header().Set("Content-Length", strconv.Itoa(len(attachment.Data)))
	response.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename=%q`, attachment.FileName))
	response.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(attachment.Data)
}

type imageInput struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

const (
	maxImagesPerMessage = 4
	maxImageBytes       = 10 << 20
	maxTotalImageBytes  = 20 << 20
)

func decodeImages(inputs []imageInput) ([]store.NewAttachment, error) {
	if len(inputs) > maxImagesPerMessage {
		return nil, fmt.Errorf("attach at most %d images per message", maxImagesPerMessage)
	}
	attachments := make([]store.NewAttachment, 0, len(inputs))
	total := 0
	for _, input := range inputs {
		data, err := base64.StdEncoding.DecodeString(input.Data)
		if err != nil {
			return nil, fmt.Errorf("%q is not valid base64 image data", input.Name)
		}
		if len(data) == 0 || len(data) > maxImageBytes {
			return nil, fmt.Errorf("%q must be between 1 byte and 10 MiB", input.Name)
		}
		total += len(data)
		if total > maxTotalImageBytes {
			return nil, errors.New("images may total at most 20 MiB per message")
		}
		detected := http.DetectContentType(data)
		if detected != "image/jpeg" && detected != "image/png" && detected != "image/webp" {
			return nil, fmt.Errorf("%q must be a JPEG, PNG, or WebP image", input.Name)
		}
		if input.MediaType != "" && input.MediaType != detected {
			return nil, fmt.Errorf("%q content does not match its media type", input.Name)
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%q could not be decoded as an image", input.Name)
		}
		if config.Width <= 0 || config.Height <= 0 || config.Width > 16384 || config.Height > 16384 || int64(config.Width)*int64(config.Height) > 40_000_000 {
			return nil, fmt.Errorf("%q exceeds the 40-megapixel or 16,384-pixel dimension limit", input.Name)
		}
		name := strings.TrimSpace(filepath.Base(input.Name))
		if name == "" || name == "." {
			name = "image"
		}
		if len(name) > 180 {
			name = name[:180]
		}
		attachments = append(attachments, store.NewAttachment{FileName: name, MediaType: detected, Data: data})
	}
	return attachments, nil
}

func (s *Server) modelImageLimit(ctx context.Context, modelName string) (int, error) {
	model, err := s.installedModel(ctx, modelName)
	if err != nil {
		return 0, err
	}
	return imageLimitForModel(model), nil
}

func (s *Server) installedModel(ctx context.Context, modelName string) (ollama.Model, error) {
	models, err := s.ollama.ListModels(ctx)
	if err != nil {
		return ollama.Model{}, err
	}
	for _, model := range models {
		if model.Name == modelName {
			return model, nil
		}
	}
	return ollama.Model{}, fmt.Errorf("model %q is not installed", modelName)
}

func imageLimitForModel(model ollama.Model) int {
	if len(model.Capabilities) == 0 {
		return maxImagesPerMessage
	}
	if !containsCapability(model.Capabilities, "vision") {
		return 0
	}
	if model.Details["family"] == "mllama" || containsString(model.Details["families"], "mllama") {
		return 1
	}
	return maxImagesPerMessage
}

func containsCapability(capabilities []string, wanted string) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

func containsString(value any, wanted string) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

func withContextMetadata(metadata map[string]any, prepared agent.PreparedContext) map[string]any {
	if prepared.ContextWindow <= 0 {
		return metadata
	}
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata["context_estimated_tokens"] = prepared.EstimatedTokens
	metadata["context_budget_tokens"] = prepared.ContextWindow
	metadata["context_compacted"] = prepared.Compacted
	if prepared.ThroughMessageID > 0 {
		metadata["context_through_message_id"] = prepared.ThroughMessageID
	}
	if prepared.Fallback {
		metadata["context_compaction_fallback"] = true
	}
	if prepared.Oversized {
		metadata["context_oversized"] = true
	}
	if prepared.CompactionError != "" {
		metadata["context_compaction_failed"] = true
		metadata["context_compaction_error"] = prepared.CompactionError
	}
	return metadata
}

func (s *Server) persistStreamResult(messageID int64, content, thinking, status string, metadata map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.store.UpdateMessage(ctx, messageID, content, thinking, status, metadata); err != nil {
		s.logger.Error("persist stream result", "error", err, "message_id", messageID)
	}
}

func (s *Server) frontend(response http.ResponseWriter, request *http.Request) {
	if strings.HasPrefix(request.URL.Path, "/api/") {
		writeError(response, http.StatusNotFound, "not_found", "API endpoint not found")
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.NotFound(response, request)
		return
	}
	path := filepath.Join(s.webDir, filepath.Clean("/"+request.URL.Path))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(response, request, path)
		return
	}
	index := filepath.Join(s.webDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		writeError(response, http.StatusNotFound, "frontend_unavailable", "build the frontend before serving it from Go")
		return
	}
	http.ServeFile(response, request, index)
}

func validateConversation(input conversationInput) error {
	if strings.TrimSpace(input.Model) == "" {
		return errors.New("model is required")
	}
	if input.Mode != "" && input.Mode != "agent" && input.Mode != "chat" {
		return errors.New("mode must be agent or chat")
	}
	if input.ThinkingMode != nil && input.ThinkingEnabled != nil {
		return errors.New("send thinking_mode or thinking_enabled, not both")
	}
	if input.ThinkingMode != nil && !validThinkingMode(*input.ThinkingMode) {
		return errors.New("thinking_mode must be off, on, low, medium, high, or max")
	}
	if input.ContextWindow != nil && *input.ContextWindow <= 0 {
		return errors.New("context_window must be positive")
	}
	if input.Temperature != nil && (*input.Temperature < 0 || *input.Temperature > 2) {
		return errors.New("temperature must be between 0 and 2")
	}
	return nil
}

func normalizedThinkingMode(input conversationInput) *string {
	if input.ThinkingMode != nil {
		return input.ThinkingMode
	}
	if input.ThinkingEnabled == nil {
		return nil
	}
	mode := "off"
	if *input.ThinkingEnabled {
		mode = "on"
	}
	return &mode
}

func validThinkingMode(mode string) bool {
	switch mode {
	case "off", "on", "low", "medium", "high", "max":
		return true
	default:
		return false
	}
}

func pathID(response http.ResponseWriter, request *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(response, http.StatusBadRequest, "invalid_id", "conversation id must be a positive integer")
		return 0, false
	}
	return id, true
}

func decodeJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 32<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON value")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeEvent(response http.ResponseWriter, flusher http.Flusher, value any) error {
	if err := json.NewEncoder(response).Encode(value); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (s *Server) storeError(response http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "conversation not found")
		return
	}
	s.internalError(response, err)
}

func (s *Server) internalError(response http.ResponseWriter, err error) {
	s.logger.Error("request failed", "error", err)
	writeError(response, http.StatusInternalServerError, "internal_error", "the request could not be completed")
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *responseRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseRecorder) Flush() {
	if flusher, ok := recorder.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		start := time.Now()
		recorder := &responseRecorder{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		logger.Info("request", "method", request.Method, "path", request.URL.Path,
			"status", recorder.status, "duration", time.Since(start))
	})
}
