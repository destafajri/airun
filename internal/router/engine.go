package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/destafajri/smart-routing/internal/config"
	"github.com/destafajri/smart-routing/internal/gitctx"
	"github.com/destafajri/smart-routing/internal/model"
	"github.com/destafajri/smart-routing/internal/state"
)

var ErrUnsafeProviderTermination = errors.New("provider process tree could not be terminated")

type RunResult struct {
	Output     string
	Diagnostic string
	Err        error
}

type Runner interface {
	Run(ctx context.Context, provider config.ProviderConfig, prompt, workdir string, out io.Writer) RunResult
	Health(ctx context.Context, provider config.ProviderConfig, workdir string) error
}

type EngineOptions struct {
	Workdir string
	Output  io.Writer
	Log     io.Writer
	Sleep   func(context.Context, time.Duration) error
}

type Engine struct {
	cfg    config.Config
	store  *state.Store
	runner Runner
	opts   EngineOptions
}

func NewEngine(cfg config.Config, store *state.Store, runner Runner, opts EngineOptions) *Engine {
	if opts.Workdir == "" {
		opts.Workdir = "."
	}
	if opts.Output == nil {
		opts.Output = io.Discard
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepContext
	}
	return &Engine{cfg: cfg, store: store, runner: runner, opts: opts}
}

func (e *Engine) Execute(ctx context.Context, taskID string) (model.Task, error) {
	release, err := e.store.AcquireTaskLock(taskID)
	if err != nil {
		return model.Task{}, err
	}
	defer release()

	task, ok, err := e.store.GetTask(taskID)
	if err != nil {
		return model.Task{}, err
	}
	if !ok {
		return model.Task{}, fmt.Errorf("task %s not found", taskID)
	}
	if task.Status == model.TaskCompleted {
		return task, nil
	}

	var lastErr error
	for _, p := range e.cfg.Providers {
		if err := ctx.Err(); err != nil {
			return e.pauseForCancellation(task, err)
		}

		if err := e.runner.Health(ctx, p, e.opts.Workdir); err != nil {
			if errors.Is(err, ErrUnsafeProviderTermination) {
				task.Status = model.TaskFailed
				task.ActiveProvider = p.Name
				task.LastError = err.Error()
				if persistErr := e.store.UpsertTask(task); persistErr != nil {
					return task, errors.Join(err, persistErr)
				}
				return task, err
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return e.pauseForCancellation(task, ctxErr)
			}

			kind := FailureUnavailable
			lastErr = fmt.Errorf("%s unavailable: %w", p.Name, err)
			now := time.Now().UTC()
			task.Attempts = append(task.Attempts, model.Attempt{Provider: p.Name, StartedAt: now, FinishedAt: now, FailureKind: string(kind), Error: lastErr.Error()})
			task.LastError = lastErr.Error()
			if !shouldFailover(p, kind) {
				task.Status = model.TaskFailed
			}
			if persistErr := e.store.UpsertTask(task); persistErr != nil {
				return task, errors.Join(lastErr, persistErr)
			}
			if task.Status == model.TaskFailed {
				return task, lastErr
			}
			continue
		}

		for attempt := 0; attempt <= p.MaxRetries; attempt++ {
			if err := ctx.Err(); err != nil {
				return e.pauseForCancellation(task, err)
			}

			task.Status = model.TaskRunning
			task.ActiveProvider = p.Name
			task.LastError = ""
			if err := e.store.UpsertTask(task); err != nil {
				return task, err
			}

			started := time.Now().UTC()
			prompt := e.handoffPrompt(task, p.Name)
			e.eventf("\n[%s] attempt %d/%d\n", p.Name, attempt+1, p.MaxRetries+1)
			res := e.runner.Run(ctx, p, prompt, e.opts.Workdir, e.opts.Output)
			finished := time.Now().UTC()
			if res.Err == nil {
				task.Status = model.TaskCompleted
				task.LastOutput = res.Output
				task.LastError = ""
				task.Attempts = append(task.Attempts, model.Attempt{Provider: p.Name, StartedAt: started, FinishedAt: finished, Output: truncate(res.Output, 16000)})
				task.CompletedAt = &finished
				if err := e.store.UpsertTask(task); err != nil {
					return task, err
				}
				return task, nil
			}

			if errors.Is(res.Err, ErrUnsafeProviderTermination) {
				task.Status = model.TaskFailed
				task.LastError = res.Err.Error()
				task.LastOutput = truncate(res.Output, 16000)
				task.Attempts = append(task.Attempts, model.Attempt{Provider: p.Name, StartedAt: started, FinishedAt: finished, FailureKind: string(FailureUnknown), Error: res.Err.Error(), Output: task.LastOutput})
				if err := e.store.UpsertTask(task); err != nil {
					return task, errors.Join(res.Err, err)
				}
				return task, res.Err
			}

			if ctxErr := ctx.Err(); ctxErr != nil {
				task.LastOutput = truncate(res.Output, 16000)
				task.Attempts = append(task.Attempts, model.Attempt{Provider: p.Name, StartedAt: started, FinishedAt: finished, FailureKind: "canceled", Error: ctxErr.Error(), Output: task.LastOutput})
				return e.pauseForCancellation(task, ctxErr)
			}

			failureText := res.Diagnostic + "\n" + res.Err.Error()
			kind := ClassifyProviderFailure(failureText, customPatterns(p.ErrorPatterns))
			lastErr = fmt.Errorf("%s failed (%s): %w", p.Name, kind, res.Err)
			task.LastError = lastErr.Error()
			task.LastOutput = truncate(res.Output, 16000)
			task.Attempts = append(task.Attempts, model.Attempt{Provider: p.Name, StartedAt: started, FinishedAt: finished, FailureKind: string(kind), Error: lastErr.Error(), Output: task.LastOutput})

			if !shouldFailover(p, kind) {
				task.Status = model.TaskFailed
			}
			if err := e.store.UpsertTask(task); err != nil {
				return task, errors.Join(lastErr, err)
			}
			if task.Status == model.TaskFailed {
				return task, lastErr
			}
			if attempt < p.MaxRetries && retrySameProvider(kind) {
				d := backoff(p.RetryBackoffMillis, attempt)
				e.eventf("[%s] retrying after %s (%s)\n", p.Name, d, kind)
				if err := e.sleep(ctx, d); err != nil {
					return e.pauseForCancellation(task, err)
				}
				continue
			}
			break
		}
		e.eventf("[%s] failover to next provider\n", p.Name)
	}

	task.Status = model.TaskPaused
	task.ActiveProvider = ""
	if lastErr == nil {
		lastErr = errors.New("no provider available")
	}
	task.LastError = lastErr.Error()
	if err := e.store.UpsertTask(task); err != nil {
		return task, errors.Join(lastErr, err)
	}
	return task, fmt.Errorf("all providers exhausted; task paused: %w", lastErr)
}

func (e *Engine) pauseForCancellation(task model.Task, cause error) (model.Task, error) {
	task.Status = model.TaskPaused
	task.ActiveProvider = ""
	task.LastError = cause.Error()
	if err := e.store.UpsertTask(task); err != nil {
		return task, errors.Join(cause, err)
	}
	return task, cause
}

func (e *Engine) sleep(ctx context.Context, d time.Duration) error {
	return e.opts.Sleep(ctx, d)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (e *Engine) eventf(format string, args ...any) {
	fmt.Fprintf(e.opts.Output, format, args...)
	fmt.Fprintf(e.opts.Log, time.Now().UTC().Format(time.RFC3339)+" "+format, args...)
}

func (e *Engine) handoffPrompt(task model.Task, nextProvider string) string {
	if len(task.Attempts) == 0 {
		return task.Prompt
	}
	var b strings.Builder
	b.WriteString("You are taking over an interrupted task from another AI provider.\n\nOriginal task:\n")
	b.WriteString(task.Prompt)
	b.WriteString("\n\nSafety / continuity rules:\n- Inspect existing work before editing.\n- Continue from the current working tree; do not redo completed side effects.\n- Do not revert existing changes merely because you would implement them differently.\n- Verify the final result with the project's existing tests/checks when practical.\n")
	b.WriteString("\nPrevious attempts:\n")
	start := 0
	if len(task.Attempts) > 4 {
		start = len(task.Attempts) - 4
	}
	for _, a := range task.Attempts[start:] {
		fmt.Fprintf(&b, "- provider=%s failure=%s error=%s\n", a.Provider, a.FailureKind, truncate(a.Error, 800))
	}
	if g := gitctx.Capture(e.opts.Workdir); g != "" {
		b.WriteString("\nCurrent repository context:\n")
		b.WriteString(g)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\nContinue the task using provider %s.\n", nextProvider)
	return b.String()
}

func customPatterns(in map[string][]string) map[FailureKind][]string {
	out := map[FailureKind][]string{}
	for k, v := range in {
		out[FailureKind(k)] = v
	}
	return out
}

func shouldFailover(p config.ProviderConfig, kind FailureKind) bool {
	for _, k := range p.FailoverOn {
		if k == string(kind) {
			return true
		}
	}
	return false
}

func retrySameProvider(kind FailureKind) bool {
	switch kind {
	case FailureRateLimit, FailureTimeout, FailureOutage:
		return true
	default:
		return false
	}
}

func backoff(baseMillis, retryIndex int) time.Duration {
	if baseMillis <= 0 {
		baseMillis = 1000
	}
	mult := 1 << min(retryIndex, 5)
	d := time.Duration(baseMillis*mult) * time.Millisecond
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n...[truncated]"
}
