package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShellExecutorUsesWorkspaceAndMinimalEnvironment(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("SHOULD_NOT_LEAK", "secret")
	executor := ShellExecutor{Workspace: workspace, Timeout: 2 * time.Second, MaxOutput: 4096}
	result := executor.Run(context.Background(), `pwd; printf '|%s|%s' "$AGENT_WORKSPACE" "${SHOULD_NOT_LEAK-unset}"`, 0)
	if result.ExitCode != 0 || result.TimedOut || result.Cancelled {
		t.Fatalf("unexpected shell result: %#v", result)
	}
	expected := workspace + "\n|" + workspace + "|unset"
	if result.Output != expected {
		t.Fatalf("workspace or environment mismatch: got %q, want %q", result.Output, expected)
	}
}

func TestShellExecutorLimitsOutputAndTimeoutKillsChildren(t *testing.T) {
	workspace := t.TempDir()
	executor := ShellExecutor{Workspace: workspace, Timeout: 120 * time.Millisecond, MaxOutput: 32}
	result := executor.Run(context.Background(), `printf '1234567890123456789012345678901234567890'; (sleep 1; touch leaked) & wait`, 0)
	if !result.TimedOut || !result.Truncated {
		t.Fatalf("expected timeout and truncation: %#v", result)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(workspace, "leaked")); !os.IsNotExist(err) {
		t.Fatalf("child process survived cancellation: %v", err)
	}
	if !strings.Contains(result.Output, "output truncated") {
		t.Fatalf("missing truncation marker: %q", result.Output)
	}
}
