package httpapi

import (
	"ollama-webui/internal/agent"
	"ollama-webui/internal/store"
)

// Use the same system context for generation and compaction; user settings remain
// stored separately so existing sessions receive current runtime instructions.
func (s *Server) conversationSystemPrompt(conversation store.Conversation) string {
	prompt := conversation.SystemPrompt
	if conversation.Mode != "chat" {
		prompt = agent.SystemPrompt
		if conversation.SystemPrompt != "" {
			prompt += "\n\nAdditional instructions from the user:\n" + conversation.SystemPrompt
		}
	}
	return prompt + agent.WorkspaceInstructions(s.runtime.Workspace)
}
