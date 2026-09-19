package router

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/destafajri/smart-routing/internal/config"
	"github.com/destafajri/smart-routing/internal/model"
	"github.com/destafajri/smart-routing/internal/state"
)

type fakeRunner struct {
	results map[string][]RunResult
	calls   []string
}

func (f *fakeRunner) Run(ctx context.Context, p config.ProviderConfig, prompt, workdir string, out io.Writer) RunResult {
	f.calls = append(f.calls, p.Name)
	rs := f.results[p.Name]
	if len(rs) == 0 {
		return RunResult{Err: errors.New("no result")}
	}
	r := rs[0]
	f.results[p.Name] = rs[1:]
	return r
}
func (f *fakeRunner) Health(ctx context.Context, p config.ProviderConfig, workdir string) error {
	return nil
}

func TestEngineFailsOverAndCompletes(t *testing.T) {
	cfg := config.Config{Providers: []config.ProviderConfig{
		{Name: "claude", Priority: 1, Command: "claude", MaxRetries: 0},
		{Name: "codex", Priority: 2, Command: "codex", MaxRetries: 0},
	}}
	if err := cfg.ValidateAndNormalize(); err != nil {
		t.Fatal(err)
	}
	store := state.New(filepath.Join(t.TempDir(), "state.json"))
	runner := &fakeRunner{results: map[string][]RunResult{
		"claude": {{Err: errors.New("weekly usage limit")}},
		"codex":  {{Output: "done"}},
	}}
	eng := NewEngine(cfg, store, runner, EngineOptions{})
	task := model.NewTask("t1", "implement feature")
	if err := store.UpsertTask(task); err != nil {
		t.Fatal(err)
	}
	got, err := eng.Execute(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.TaskCompleted {
		t.Fatalf("status %s", got.Status)
	}
	if got.ActiveProvider != "codex" {
		t.Fatalf("provider %s", got.ActiveProvider)
	}
	if len(runner.calls) != 2 || runner.calls[0] != "claude" || runner.calls[1] != "codex" {
		t.Fatalf("calls %v", runner.calls)
	}
}

func TestEnginePausesWhenAllProvidersFail(t *testing.T) {
	cfg := config.Config{Providers: []config.ProviderConfig{
		{Name: "claude", Priority: 1, Command: "claude"},
		{Name: "codex", Priority: 2, Command: "codex"},
	}}
	if err := cfg.ValidateAndNormalize(); err != nil {
		t.Fatal(err)
	}
	store := state.New(filepath.Join(t.TempDir(), "state.json"))
	runner := &fakeRunner{results: map[string][]RunResult{
		"claude": {{Err: errors.New("service unavailable HTTP 503")}},
		"codex":  {{Err: errors.New("rate limit 429")}},
	}}
	eng := NewEngine(cfg, store, runner, EngineOptions{})
	if err := store.UpsertTask(model.NewTask("t2", "task")); err != nil {
		t.Fatal(err)
	}
	got, err := eng.Execute(context.Background(), "t2")
	if err == nil {
		t.Fatal("expected error")
	}
	if got.Status != model.TaskPaused {
		t.Fatalf("status %s", got.Status)
	}
}


func TestEngineUnknownFailureFailsClosedWithoutFailover(t *testing.T) {
	cfg := config.Config{Providers: []config.ProviderConfig{
		{Name: "one", Priority: 1, Command: "one"},
		{Name: "two", Priority: 2, Command: "two"},
	}}
	if err := cfg.ValidateAndNormalize(); err != nil {
		t.Fatal(err)
	}
	store := state.New(filepath.Join(t.TempDir(), "state.json"))
	runner := &fakeRunner{results: map[string][]RunResult{
		"one": {{Err: errors.New("tests failed: expected 2 got 3")}},
		"two": {{Output: "must not run"}},
	}}
	eng := NewEngine(cfg, store, runner, EngineOptions{})
	if err := store.UpsertTask(model.NewTask("unknown", "fix tests")); err != nil {
		t.Fatal(err)
	}
	got, err := eng.Execute(context.Background(), "unknown")
	if err == nil {
		t.Fatal("expected task-level failure")
	}
	if got.Status != model.TaskFailed {
		t.Fatalf("status %s, want failed", got.Status)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "one" {
		t.Fatalf("unexpected provider calls: %v", runner.calls)
	}
}

func TestEngineCancellationStopsDuringBackoff(t *testing.T) {
	cfg := config.Config{Providers: []config.ProviderConfig{
		{Name: "one", Priority: 1, Command: "one", MaxRetries: 1, RetryBackoffMillis: 30000},
		{Name: "two", Priority: 2, Command: "two"},
	}}
	if err := cfg.ValidateAndNormalize(); err != nil {
		t.Fatal(err)
	}
	store := state.New(filepath.Join(t.TempDir(), "state.json"))
	if err := store.UpsertTask(model.NewTask("cancel", "task")); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{results: map[string][]RunResult{
		"one": {{Err: errors.New("service unavailable HTTP 503")}},
		"two": {{Output: "must not run"}},
	}}
	sleepStarted := make(chan struct{})
	eng := NewEngine(cfg, store, runner, EngineOptions{
		Sleep: func(ctx context.Context, d time.Duration) error {
			close(sleepStarted)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := eng.Execute(ctx, "cancel")
		result <- err
	}()
	<-sleepStarted
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error %v, want context canceled", err)
		}
		if len(runner.calls) != 1 {
			t.Fatalf("provider calls %v, want one call", runner.calls)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop backoff promptly")
	}
}


type cancelUnsafeRunner struct {
	cancel context.CancelFunc
	calls  []string
}

func (r *cancelUnsafeRunner) Health(ctx context.Context, p config.ProviderConfig, workdir string) error {
	return nil
}

func (r *cancelUnsafeRunner) Run(ctx context.Context, p config.ProviderConfig, prompt, workdir string, out io.Writer) RunResult {
	r.calls = append(r.calls, p.Name)
	r.cancel()
	return RunResult{Err: ErrUnsafeProviderTermination}
}

func TestEngineUnsafeTerminationBeatsCancellationAndFailsClosed(t *testing.T) {
	cfg := config.Config{Providers: []config.ProviderConfig{
		{Name: "one", Priority: 1, Command: "one"},
		{Name: "two", Priority: 2, Command: "two"},
	}}
	if err := cfg.ValidateAndNormalize(); err != nil {
		t.Fatal(err)
	}
	store := state.New(filepath.Join(t.TempDir(), "state.json"))
	if err := store.UpsertTask(model.NewTask("unsafe-cancel", "task")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runner := &cancelUnsafeRunner{cancel: cancel}
	eng := NewEngine(cfg, store, runner, EngineOptions{})
	got, err := eng.Execute(ctx, "unsafe-cancel")
	if !errors.Is(err, ErrUnsafeProviderTermination) {
		t.Fatalf("error %v, want unsafe termination", err)
	}
	if got.Status != model.TaskFailed {
		t.Fatalf("status %s, want failed", got.Status)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "one" {
		t.Fatalf("unexpected provider calls: %v", runner.calls)
	}
}

func TestEngineDoesNotClassifyNormalOutputAsInfrastructureFailure(t *testing.T) {
	cfg := config.Config{Providers: []config.ProviderConfig{
		{Name: "one", Priority: 1, Command: "one"},
		{Name: "two", Priority: 2, Command: "two"},
	}}
	if err := cfg.ValidateAndNormalize(); err != nil {
		t.Fatal(err)
	}
	store := state.New(filepath.Join(t.TempDir(), "state.json"))
	if err := store.UpsertTask(model.NewTask("output-mention", "task")); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{results: map[string][]RunResult{
		"one": {{Output: "documentation example: HTTP 429 rate limit", Err: errors.New("tests failed")}},
		"two": {{Output: "must not run"}},
	}}
	eng := NewEngine(cfg, store, runner, EngineOptions{})
	got, err := eng.Execute(context.Background(), "output-mention")
	if err == nil {
		t.Fatal("expected unrelated task failure")
	}
	if got.Status != model.TaskFailed {
		t.Fatalf("status %s, want failed", got.Status)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "one" {
		t.Fatalf("unexpected provider calls: %v", runner.calls)
	}
}


type healthCancelUnsafeRunner struct {
	cancel context.CancelFunc
}

func (r *healthCancelUnsafeRunner) Health(ctx context.Context, p config.ProviderConfig, workdir string) error {
	r.cancel()
	return ErrUnsafeProviderTermination
}

func (r *healthCancelUnsafeRunner) Run(ctx context.Context, p config.ProviderConfig, prompt, workdir string, out io.Writer) RunResult {
	return RunResult{Output: "must not run"}
}

func TestEngineUnsafeHealthTerminationBeatsCancellationAndFailsClosed(t *testing.T) {
	cfg := config.Config{Providers: []config.ProviderConfig{
		{Name: "one", Priority: 1, Command: "one"},
		{Name: "two", Priority: 2, Command: "two"},
	}}
	if err := cfg.ValidateAndNormalize(); err != nil {
		t.Fatal(err)
	}
	store := state.New(filepath.Join(t.TempDir(), "state.json"))
	if err := store.UpsertTask(model.NewTask("unsafe-health", "task")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	eng := NewEngine(cfg, store, &healthCancelUnsafeRunner{cancel: cancel}, EngineOptions{})
	got, err := eng.Execute(ctx, "unsafe-health")
	if !errors.Is(err, ErrUnsafeProviderTermination) {
		t.Fatalf("error %v, want unsafe termination", err)
	}
	if got.Status != model.TaskFailed {
		t.Fatalf("status %s, want failed", got.Status)
	}
}
