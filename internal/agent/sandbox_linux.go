//go:build linux

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const sandboxHelperArgument = "--ollama-webui-agent-sandbox-helper"

// Landlock ABI 3 added LANDLOCK_ACCESS_FS_TRUNCATE. Earlier ABIs cannot enforce
// the workspace-only write policy because opening an outside file with O_TRUNC
// would remain possible.
const minimumLandlockABI = 3

func init() {
	if len(os.Args) < 3 || os.Args[1] != sandboxHelperArgument {
		return
	}
	if err := enterWorkspaceWriteSandbox(os.Getenv("AGENT_WORKSPACE")); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to enter shell sandbox: %v\n", err)
		os.Exit(126)
	}
	if err := syscall.Exec("/bin/sh", []string{"/bin/sh", "-c", os.Args[2]}, os.Environ()); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to execute shell: %v\n", err)
		os.Exit(126)
	}
}

func sandboxedShellCommand(command string) (*exec.Cmd, error) {
	abi, err := landlockABI()
	if err != nil {
		return nil, err
	}
	if abi < minimumLandlockABI {
		return nil, fmt.Errorf("Landlock ABI %d is too old; ABI %d or newer is required", abi, minimumLandlockABI)
	}
	// Re-exec the already-running image through procfs. Unlike os.Executable(),
	// this cannot be redirected by replacing a server binary stored beneath the
	// writable workspace between shell calls.
	return exec.Command("/proc/self/exe", sandboxHelperArgument, command), nil
}

func landlockABI() (int, error) {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0, fmt.Errorf("query Landlock ABI: %w", errno)
	}
	return int(abi), nil
}

func enterWorkspaceWriteSandbox(workspace string) error {
	if workspace == "" {
		return fmt.Errorf("AGENT_WORKSPACE is empty")
	}
	abi, err := landlockABI()
	if err != nil {
		return err
	}
	if abi < minimumLandlockABI {
		return fmt.Errorf("Landlock ABI %d is too old; ABI %d or newer is required", abi, minimumLandlockABI)
	}

	writeAccess := uint64(
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
			unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
			unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
			unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
			unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
			unix.LANDLOCK_ACCESS_FS_MAKE_REG |
			unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
			unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
			unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
			unix.LANDLOCK_ACCESS_FS_MAKE_SYM |
			unix.LANDLOCK_ACCESS_FS_REFER |
			unix.LANDLOCK_ACCESS_FS_TRUNCATE)
	handledAccess := writeAccess
	if abi >= 5 {
		// Device ioctls can mutate external state without writing file contents.
		// Handle but do not grant this right, including beneath the workspace.
		handledAccess |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}

	ruleset := unix.LandlockRulesetAttr{Access_fs: handledAccess}
	rulesetFD, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&ruleset)), unsafe.Sizeof(ruleset), 0)
	if errno != 0 {
		return fmt.Errorf("create Landlock ruleset: %w", errno)
	}
	defer unix.Close(int(rulesetFD))

	workspaceFD, err := unix.Open(workspace, unix.O_PATH|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return fmt.Errorf("open agent workspace: %w", err)
	}
	defer unix.Close(workspaceFD)

	rule := unix.LandlockPathBeneathAttr{Allowed_access: writeAccess, Parent_fd: int32(workspaceFD)}
	_, _, errno = unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, rulesetFD, unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("allow writes beneath agent workspace: %w", errno)
	}

	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs: %w", err)
	}
	_, _, errno = unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, rulesetFD, 0, 0)
	if errno != 0 {
		return fmt.Errorf("enforce Landlock ruleset: %w", errno)
	}
	return nil
}
