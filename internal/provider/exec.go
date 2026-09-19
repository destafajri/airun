package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/destafajri/smart-routing/internal/config"
	"github.com/destafajri/smart-routing/internal/router"
)

type ExecRunner struct{}

func NewExecRunner() *ExecRunner { return &ExecRunner{} }

func (r *ExecRunner) Health(ctx context.Context, p config.ProviderConfig, workdir string) error {
	if _, err := exec.LookPath(p.Command); err != nil {
		return err
	}
	if len(p.HealthArgs) == 0 {
		return nil
	}
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(hctx, p.Command, p.HealthArgs...)
	cmd.Dir = workdir
	cmd.Env = mergedEnv(p.Env)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("health check: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (r *ExecRunner) Run(ctx context.Context, p config.ProviderConfig, prompt, workdir string, out io.Writer) router.RunResult {
	timeout := time.Duration(p.TimeoutSeconds) * time.Second
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append([]string(nil), p.Args...)
	if p.PromptMode == "stdin" {
		// prompt is sent below through stdin
	} else {
		replaced := false
		for i := range args {
			if strings.Contains(args[i], "{{prompt}}") {
				args[i] = strings.ReplaceAll(args[i], "{{prompt}}", prompt)
				replaced = true
			}
		}
		if !replaced {
			args = append(args, prompt)
		}
	}

	cmd := exec.CommandContext(rctx, p.Command, args...)
	cmd.Dir = workdir
	cmd.Env = mergedEnv(p.Env)
	if p.PromptMode == "stdin" {
		cmd.Stdin = strings.NewReader(prompt)
	}

	cw := &captureWriter{out: out}
	cmd.Stdout = cw
	cmd.Stderr = cw
	err := cmd.Run()
	output := cw.String()
	if rctx.Err() == context.DeadlineExceeded {
		return router.RunResult{Output: output, Err: fmt.Errorf("request timed out after %s: %w", timeout, rctx.Err())}
	}
	if err != nil {
		return router.RunResult{Output: output, Err: err}
	}
	return router.RunResult{Output: output}
}

type captureWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
	out io.Writer
}

func (w *captureWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = w.buf.Write(p)
	if w.out != nil {
		_, _ = w.out.Write(p)
	}
	return len(p), nil
}
func (w *captureWriter) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.buf.String() }

func mergedEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
