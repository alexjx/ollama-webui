package httpapi

import (
	"ollama-webui/internal/agent"
	"ollama-webui/internal/store"
	"strings"
	"testing"
)

func TestConversationSystemPromptPreservesUserAndModeWithConfiguredWorkspace(t *testing.T) {
	for _, mode := range []string{"chat", "agent"} {
		for _, root := range []string{"", "/workspace", "/custom/工作 space"} {
			t.Run(mode+root, func(t *testing.T) {
				s := &Server{runtime: RuntimeSettings{Workspace: root}}
				conversation := store.Conversation{Mode: mode, SystemPrompt: "CUSTOM instructions"}
				prompt := s.conversationSystemPrompt(conversation)
				if !strings.Contains(prompt, conversation.SystemPrompt) {
					t.Fatal("lost custom instructions")
				}
				if strings.Contains(prompt, agent.SystemPrompt) != (mode == "agent") {
					t.Fatal("wrong mode instructions")
				}
				if root != "" {
					if !strings.Contains(prompt, root) || !strings.Contains(prompt, "full absolute paths") || !strings.Contains(prompt, "inline code") {
						t.Fatalf("missing path contract: %s", prompt)
					}
					if root != "/workspace" && strings.Contains(prompt, `"/workspace"`) {
						t.Fatal("hardcoded workspace root")
					}
				} else if strings.Contains(prompt, "Workspace file references:") {
					t.Fatal("invented unconfigured workspace")
				}
				if conversation.SystemPrompt != "CUSTOM instructions" {
					t.Fatal("mutated saved user instructions")
				}
			})
		}
	}
}
