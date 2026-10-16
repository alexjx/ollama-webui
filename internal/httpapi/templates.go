package httpapi

import (
	"net/http"
	"strconv"

	"ollama-webui/internal/store"
)

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListTemplates(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func templateInput(w http.ResponseWriter, r *http.Request) (store.TemplateInput, bool) {
	var input store.TemplateInput
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	err := decodeJSON(r, &input)
	if err == nil {
		err = input.Validate()
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_template", err.Error())
		return input, false
	}
	return input, true
}
func templateID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_id", "template id must be a positive integer")
		return 0, false
	}
	return id, true
}
func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	input, ok := templateInput(w, r)
	if !ok {
		return
	}
	item, err := s.store.CreateTemplate(r.Context(), input)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := templateID(w, r)
	if !ok {
		return
	}
	input, ok := templateInput(w, r)
	if !ok {
		return
	}
	item, err := s.store.UpdateTemplate(r.Context(), id, input)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := templateID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteTemplate(r.Context(), id); err != nil {
		s.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
