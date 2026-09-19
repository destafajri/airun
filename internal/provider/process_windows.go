//go:build windows

package provider

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	out, err := exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").CombinedOutput()
	if err != nil {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return nil
		}
		return fmt.Errorf("taskkill: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
