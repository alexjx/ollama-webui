//go:build !linux

package agent

import (
	"fmt"
	"os/exec"
)

func sandboxedShellCommand(_ string) (*exec.Cmd, error) {
	return nil, fmt.Errorf("kernel-enforced shell filesystem isolation is only available on Linux")
}
