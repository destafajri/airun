//go:build windows

package state

import (
	"fmt"
	"os/exec"
	"strings"
)

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false
	}
	line := strings.TrimSpace(string(out))
	return line != "" && !strings.HasPrefix(line, "INFO:") && strings.Contains(line, fmt.Sprintf("\"%d\"", pid))
}
