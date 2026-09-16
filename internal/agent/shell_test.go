package agent

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
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

func TestShellExecutorAllowsGlobalReadsButWritesOnlyInWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "existing.txt")
	if err := os.WriteFile(outsideFile, []byte("outside-readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := ShellExecutor{Workspace: workspace, Timeout: 2 * time.Second, MaxOutput: 4096}

	read := executor.Run(context.Background(), "cat "+strconv.Quote(outsideFile), 0)
	if read.ExitCode != 0 || read.Output != "outside-readable" {
		t.Fatalf("outside read should succeed: %#v", read)
	}

	workspaceFile := filepath.Join(workspace, "created.txt")
	writeWorkspace := executor.Run(context.Background(), "printf workspace > "+strconv.Quote(workspaceFile), 0)
	if writeWorkspace.ExitCode != 0 {
		t.Fatalf("workspace write should succeed: %#v", writeWorkspace)
	}
	if content, err := os.ReadFile(workspaceFile); err != nil || string(content) != "workspace" {
		t.Fatalf("unexpected workspace content %q, err=%v", content, err)
	}

	outsideCreated := filepath.Join(outside, "created.txt")
	createOutside := executor.Run(context.Background(), "printf forbidden > "+strconv.Quote(outsideCreated), 0)
	if createOutside.ExitCode == 0 {
		t.Fatalf("outside create unexpectedly succeeded: %#v", createOutside)
	}
	if _, err := os.Stat(outsideCreated); !os.IsNotExist(err) {
		t.Fatalf("outside file was created: %v", err)
	}

	truncateOutside := executor.Run(context.Background(), "printf changed > "+strconv.Quote(outsideFile), 0)
	if truncateOutside.ExitCode == 0 {
		t.Fatalf("outside truncate/write unexpectedly succeeded: %#v", truncateOutside)
	}
	if content, err := os.ReadFile(outsideFile); err != nil || string(content) != "outside-readable" {
		t.Fatalf("outside content changed to %q, err=%v", content, err)
	}

	symlink := filepath.Join(workspace, "outside-link.txt")
	if err := os.Symlink(outsideFile, symlink); err != nil {
		t.Fatal(err)
	}
	writeThroughSymlink := executor.Run(context.Background(), "printf changed > "+strconv.Quote(symlink), 0)
	if writeThroughSymlink.ExitCode == 0 {
		t.Fatalf("workspace symlink escaped the write boundary: %#v", writeThroughSymlink)
	}
	if content, err := os.ReadFile(outsideFile); err != nil || string(content) != "outside-readable" {
		t.Fatalf("outside content changed through workspace symlink to %q, err=%v", content, err)
	}

	deleteOutside := executor.Run(context.Background(), "rm "+strconv.Quote(outsideFile), 0)
	if deleteOutside.ExitCode == 0 {
		t.Fatalf("outside delete unexpectedly succeeded: %#v", deleteOutside)
	}
	if content, err := os.ReadFile(outsideFile); err != nil || string(content) != "outside-readable" {
		t.Fatalf("outside file was deleted or changed: content=%q, err=%v", content, err)
	}

	outsideRenamed := filepath.Join(outside, "renamed.txt")
	renameOutside := executor.Run(context.Background(), "mv "+strconv.Quote(outsideFile)+" "+strconv.Quote(outsideRenamed), 0)
	if renameOutside.ExitCode == 0 {
		t.Fatalf("outside rename unexpectedly succeeded: %#v", renameOutside)
	}
	if _, err := os.Stat(outsideRenamed); !os.IsNotExist(err) {
		t.Fatalf("outside rename target exists: %v", err)
	}
	if content, err := os.ReadFile(outsideFile); err != nil || string(content) != "outside-readable" {
		t.Fatalf("outside source was renamed or changed: content=%q, err=%v", content, err)
	}
}

func TestSandboxHelperReexecutesCurrentImageThroughProcfs(t *testing.T) {
	command, err := sandboxedShellCommand("true")
	if err != nil {
		t.Fatal(err)
	}
	if command.Path != "/proc/self/exe" {
		t.Fatalf("sandbox helper may be replaceable through a writable executable path: %q", command.Path)
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
