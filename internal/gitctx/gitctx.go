package gitctx

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

func Capture(workdir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status := run(ctx, workdir, "git", "status", "--short", "--branch")
	if status == "" {
		return ""
	}
	diff := run(ctx, workdir, "git", "diff", "--no-ext-diff", "--stat")
	if len(diff) > 12000 {
		diff = diff[:12000] + "\n...[truncated]"
	}
	var b strings.Builder
	b.WriteString("Git status:\n")
	b.WriteString(status)
	if diff != "" {
		b.WriteString("\n\nGit diff summary:\n")
		b.WriteString(diff)
	}
	return b.String()
}

func run(ctx context.Context, dir, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
