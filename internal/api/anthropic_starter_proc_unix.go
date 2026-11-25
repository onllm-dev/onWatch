//go:build !windows

package api

import (
	"os/exec"
	"syscall"
)

// setAnthropicStarterTreeKill starts the CLI in its own process group and,
// when the context ends, kills the whole group. An npm claude is a script
// that runs node, and killing only the script would leave node running the
// turn after onWatch has given up on it.
func setAnthropicStarterTreeKill(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// With Setpgid the group id is the CLI's own pid, never onWatch's.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return cmd.Process.Kill()
	}
}
