package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr            string
	OllamaBaseURL         string
	DatabasePath          string
	WebDistDir            string
	AgentWorkspace        string
	AgentContextDir       string
	AgentMaxTurns         int
	ContextTokens         int
	InlineInputMax        int
	ShellTimeout          time.Duration
	ShellMaxOutput        int
	ToolFeedbackMax       int
	SubagentsEnabled      bool
	SubagentConcurrency   int
	SubagentContextTokens int
	SubagentMaxTurns      int
	SubagentResultBytes   int
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:     valueOrDefault("LISTEN_ADDR", ":8080"),
		OllamaBaseURL:  strings.TrimRight(valueOrDefault("OLLAMA_BASE_URL", "http://localhost:11434"), "/"),
		DatabasePath:   valueOrDefault("DATABASE_PATH", "./data/ollama-webui.db"),
		WebDistDir:     valueOrDefault("WEB_DIST_DIR", "./web/dist/client"),
		AgentWorkspace: valueOrDefault("AGENT_WORKSPACE", "./workspace"),
	}
	cfg.AgentContextDir = valueOrDefault("AGENT_CONTEXT_PATH", filepath.Join(filepath.Dir(cfg.DatabasePath), "agent-context"))
	if cfg.OllamaBaseURL == "" {
		return Config{}, fmt.Errorf("OLLAMA_BASE_URL cannot be empty")
	}
	var err error
	if cfg.AgentMaxTurns, err = positiveInt("AGENT_MAX_TURNS", 200); err != nil {
		return Config{}, err
	}
	if cfg.ContextTokens, err = positiveInt("AGENT_CONTEXT_BUDGET_TOKENS", 32768); err != nil {
		return Config{}, err
	}
	if cfg.InlineInputMax, err = positiveInt("AGENT_INLINE_INPUT_BYTES", 16<<10); err != nil {
		return Config{}, err
	}
	timeoutSeconds, err := positiveInt("SHELL_TIMEOUT_SECONDS", 600)
	if err != nil {
		return Config{}, err
	}
	cfg.ShellTimeout = time.Duration(timeoutSeconds) * time.Second
	if cfg.ShellMaxOutput, err = positiveInt("SHELL_MAX_OUTPUT_BYTES", 64<<10); err != nil {
		return Config{}, err
	}
	if cfg.ToolFeedbackMax, err = positiveInt("AGENT_TOOL_FEEDBACK_BYTES", 8<<10); err != nil {
		return Config{}, err
	}
	if cfg.SubagentsEnabled, err = boolean("AGENT_SUBAGENTS_ENABLED", true); err != nil {
		return Config{}, err
	}
	if cfg.SubagentConcurrency, err = positiveInt("AGENT_SUBAGENT_CONCURRENCY", 1); err != nil {
		return Config{}, err
	}
	if cfg.SubagentContextTokens, err = positiveInt("AGENT_SUBAGENT_CONTEXT_TOKENS", 8192); err != nil {
		return Config{}, err
	}
	if cfg.SubagentMaxTurns, err = positiveInt("AGENT_SUBAGENT_MAX_TURNS", 20); err != nil {
		return Config{}, err
	}
	if cfg.SubagentResultBytes, err = positiveInt("AGENT_SUBAGENT_RESULT_BYTES", 4096); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func boolean(key string, fallback bool) (bool, error) {
	value := valueOrDefault(key, strconv.FormatBool(fallback))
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return parsed, nil
}

func positiveInt(key string, fallback int) (int, error) {
	value := valueOrDefault(key, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func valueOrDefault(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
