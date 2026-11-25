//go:build windows

package api

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

// setAnthropicStarterTreeKill makes a cancelled run kill the CLI's whole
// process tree. npm installs claude as claude.cmd, which runs node.exe under
// cmd.exe; killing only cmd.exe would leave node running the turn after
// onWatch has given up on it, holding the work folder open.
func setAnthropicStarterTreeKill(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		return cmd.Process.Kill()
	}
}
