package config

import "testing"

func TestLoadContextBudgetDefaultsAndOverrides(t *testing.T) {
	t.Setenv("AGENT_INLINE_INPUT_BYTES", "12345")
	t.Setenv("AGENT_TOOL_FEEDBACK_BYTES", "6789")
	t.Setenv("AGENT_CONTEXT_BUDGET_TOKENS", "24576")
	t.Setenv("AGENT_CONTEXT_PATH", "/tmp/test-agent-context")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InlineInputMax != 12345 || cfg.ToolFeedbackMax != 6789 || cfg.ContextTokens != 24576 || cfg.AgentContextDir != "/tmp/test-agent-context" {
		t.Fatalf("context budget overrides were not loaded: %#v", cfg)
	}
}

func TestLoadRejectsInvalidContextBudget(t *testing.T) {
	t.Setenv("AGENT_INLINE_INPUT_BYTES", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid inline input budget to fail")
	}
}

func TestLoadDerivesContextPathFromDatabase(t *testing.T) {
	t.Setenv("DATABASE_PATH", "/var/lib/ollama-webui/chat.db")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AgentContextDir != "/var/lib/ollama-webui/agent-context" {
		t.Fatalf("unexpected context path %q", cfg.AgentContextDir)
	}
}
