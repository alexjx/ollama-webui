package httpapi

import "net/http"

func (s *Server) forkConversation(response http.ResponseWriter, request *http.Request) {
	id, ok := pathID(response, request)
	if !ok {
		return
	}
	// Reserve the source so generation, compaction, and deletion cannot change
	// its context while the database and artifact files are being copied.
	s.activeMu.Lock()
	if _, active := s.active[id]; active {
		s.activeMu.Unlock()
		writeError(response, http.StatusConflict, "generation_active", "wait for the active response or context compaction to finish before forking")
		return
	}
	s.active[id] = struct{}{}
	s.activeMu.Unlock()
	defer func() {
		s.activeMu.Lock()
		delete(s.active, id)
		s.activeMu.Unlock()
	}()

	var destinationKey string
	var copyArtifacts []func(string, string, map[string]string) error
	if s.agent.Artifacts != nil {
		copyArtifacts = append(copyArtifacts, func(source, destination string, keys map[string]string) error {
			if err := s.agent.Artifacts.CopyConversation(source, destination, keys); err != nil {
				return err
			}
			destinationKey = destination
			return nil
		})
	}
	conversation, err := s.store.ForkConversation(request.Context(), id, copyArtifacts...)
	if err != nil {
		if destinationKey != "" {
			if cleanupErr := s.agent.Artifacts.RemoveConversation(destinationKey); cleanupErr != nil {
				s.logger.Warn("remove failed fork artifacts", "error", cleanupErr)
			}
		}
		s.storeError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, conversation)
}
