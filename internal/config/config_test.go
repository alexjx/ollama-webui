package config

import "testing"

func TestLoadContextBudgetDefaultsAndOverrides(t *testing.T) {
	t.Setenv("AGENT_INLINE_INPUT_BYTES", "12345")
	t.Setenv("AGENT_TOOL_FEEDBACK_BYTES", "6789")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InlineInputMax != 12345 || cfg.ToolFeedbackMax != 6789 {
		t.Fatalf("context budget overrides were not loaded: %#v", cfg)
	}
}

func TestLoadRejectsInvalidContextBudget(t *testing.T) {
	t.Setenv("AGENT_INLINE_INPUT_BYTES", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid inline input budget to fail")
	}
}
