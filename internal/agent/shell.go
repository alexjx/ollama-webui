package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type ShellExecutor struct {
	Workspace string
	Timeout   time.Duration
	MaxOutput int
}

type ShellResult struct {
	Output    string        `json:"output"`
	ExitCode  int           `json:"exit_code"`
	Duration  time.Duration `json:"duration"`
	TimedOut  bool          `json:"timed_out"`
	Cancelled bool          `json:"cancelled"`
	Truncated bool          `json:"truncated"`
}

func (executor ShellExecutor) Run(ctx context.Context, command string, requestedTimeout time.Duration) ShellResult {
	timeout := executor.Timeout
	if requestedTimeout > 0 && requestedTimeout < timeout {
		timeout = requestedTimeout
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	output := &limitedBuffer{limit: executor.MaxOutput}
	workspace, err := filepath.Abs(executor.Workspace)
	if err != nil {
		return ShellResult{Output: fmt.Sprintf("invalid agent workspace: %v", err), ExitCode: -1, Duration: time.Since(started)}
	}
	if err := prepareShellWorkspace(workspace); err != nil {
		return ShellResult{Output: fmt.Sprintf("failed to prepare agent workspace: %v", err), ExitCode: -1, Duration: time.Since(started)}
	}
	process, err := sandboxedShellCommand(command)
	if err != nil {
		return ShellResult{Output: fmt.Sprintf("shell sandbox unavailable: %v", err), ExitCode: -1, Duration: time.Since(started)}
	}
	process.Dir = workspace
	process.Env = minimalEnvironment(workspace)
	process.Stdout = output
	process.Stderr = output
	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	result := ShellResult{ExitCode: -1}
	if err := process.Start(); err != nil {
		result.Output = fmt.Sprintf("failed to start command: %v", err)
		result.Duration = time.Since(started)
		return result
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	var runErr error
	select {
	case runErr = <-done:
	case <-commandCtx.Done():
		_ = syscall.Kill(-process.Process.Pid, syscall.SIGTERM)
		select {
		case runErr = <-done:
		case <-time.After(750 * time.Millisecond):
			_ = syscall.Kill(-process.Process.Pid, syscall.SIGKILL)
			runErr = <-done
		}
		result.TimedOut = errors.Is(commandCtx.Err(), context.DeadlineExceeded)
		result.Cancelled = errors.Is(commandCtx.Err(), context.Canceled)
	}
	if process.ProcessState != nil {
		result.ExitCode = process.ProcessState.ExitCode()
	}
	result.Output = output.String()
	result.Truncated = output.Truncated()
	result.Duration = time.Since(started)
	if result.Output == "" && runErr != nil {
		result.Output = runErr.Error()
	}
	return result
}

func minimalEnvironment(workspace string) []string {
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + workspace,
		"TMPDIR=" + workspace,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"TERM=dumb",
		"AGENT_WORKSPACE=" + workspace,
		"SHELL=/bin/sh",
	}
}

func prepareShellWorkspace(workspace string) error {
	info, err := os.Stat(workspace)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", workspace)
	}
	return nil
}

type limitedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	originalLength := len(value)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.truncated = true
		return originalLength, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		buffer.truncated = true
	}
	_, _ = buffer.buffer.Write(value)
	return originalLength, nil
}

func (buffer *limitedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	value := buffer.buffer.String()
	if buffer.truncated {
		value += "\n[output truncated]"
	}
	return value
}

func (buffer *limitedBuffer) Truncated() bool {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.truncated
}
