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

func TestLoadSubagentDefaultsAndOverrides(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SubagentsEnabled || cfg.SubagentConcurrency != 1 || cfg.SubagentContextTokens != 8192 || cfg.SubagentMaxTurns != 20 || cfg.SubagentResultBytes != 4096 {
		t.Fatalf("unexpected subagent defaults: %#v", cfg)
	}

	t.Setenv("AGENT_SUBAGENTS_ENABLED", "false")
	t.Setenv("AGENT_SUBAGENT_CONCURRENCY", "3")
	t.Setenv("AGENT_SUBAGENT_CONTEXT_TOKENS", "4096")
	t.Setenv("AGENT_SUBAGENT_MAX_TURNS", "12")
	t.Setenv("AGENT_SUBAGENT_RESULT_BYTES", "2048")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SubagentsEnabled || cfg.SubagentConcurrency != 3 || cfg.SubagentContextTokens != 4096 || cfg.SubagentMaxTurns != 12 || cfg.SubagentResultBytes != 2048 {
		t.Fatalf("subagent overrides were not loaded: %#v", cfg)
	}
}

func TestLoadRejectsInvalidSubagentSettings(t *testing.T) {
	t.Setenv("AGENT_SUBAGENTS_ENABLED", "sometimes")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid subagent enabled flag to fail")
	}
}
