package httpapi

import (
	"net/http"
	"strings"
	"unicode"

	"ollama-webui/internal/store"
)

func publicWebSearchSettings(settings store.WebSearchSettings) map[string]any {
	return map[string]any{"provider": "tavily", "enabled": settings.Enabled, "configured": settings.Token != ""}
}

func (s *Server) updateConversationWebSearch(response http.ResponseWriter, request *http.Request) {
	id, ok := pathID(response, request)
	if !ok {
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(request, &input); err != nil || input.Enabled == nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "enabled must be a boolean")
		return
	}
	// Hold the generation lock until the permission and response snapshot agree.
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if _, active := s.active[id]; active {
		writeError(response, http.StatusConflict, "generation_active", "stop the active response and wait for it to finish before changing web search")
		return
	}
	if err := s.store.UpdateConversationWebSearch(request.Context(), id, *input.Enabled); err != nil {
		s.storeError(response, err)
		return
	}
	conversation, err := s.store.GetConversation(request.Context(), id)
	if err != nil {
		s.storeError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, conversation)
}

func (s *Server) updateWebSearchSettings(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	var input struct {
		Enabled     *bool   `json:"enabled"`
		Token       *string `json:"api_token"`
		RemoveToken bool    `json:"remove_token"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_web_search_settings", "invalid web search settings")
		return
	}
	token := ""
	if input.Token != nil {
		token = strings.TrimSpace(*input.Token)
	}
	if len(token) > 512 || strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_api_token", "Tavily API token must contain at most 512 bytes and no whitespace or control characters")
		return
	}
	if input.RemoveToken && token != "" {
		writeError(response, http.StatusUnprocessableEntity, "conflicting_token_update", "replace or remove the token in separate actions")
		return
	}
	// Partial token/toggle updates must not overwrite concurrent configuration edits.
	s.webSearchSettingsMu.Lock()
	defer s.webSearchSettingsMu.Unlock()
	settings, err := s.store.GetWebSearchSettings(request.Context())
	if err != nil {
		s.internalError(response, err)
		return
	}
	if token != "" {
		settings.Token = token
	}
	if input.Enabled != nil {
		settings.Enabled = *input.Enabled
	}
	if input.RemoveToken {
		settings = store.WebSearchSettings{}
	}
	if settings.Enabled && settings.Token == "" {
		writeError(response, http.StatusUnprocessableEntity, "api_token_required", "Add a Tavily API token before enabling web search.")
		return
	}
	if err = s.store.SetWebSearchSettings(request.Context(), settings); err != nil {
		s.internalError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, publicWebSearchSettings(settings))
}

func (s *Server) testWebSearch(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	var input struct{}
	if !strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/json") || decodeJSON(request, &input) != nil {
		writeError(response, http.StatusBadRequest, "invalid_search_test", "send an empty JSON object to test the saved token")
		return
	}
	if s.agent.WebSearch == nil || !s.agent.WebSearch.Enabled(request.Context()) {
		writeError(response, http.StatusUnprocessableEntity, "web_search_disabled", "Enable web search with a saved Tavily API token before testing.")
		return
	}
	result, err := s.agent.WebSearch.Search(request.Context(), "Tavily", 1)
	if err != nil {
		writeError(response, http.StatusBadGateway, "web_search_failed", err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "result_count": len(result.Results)})
}
