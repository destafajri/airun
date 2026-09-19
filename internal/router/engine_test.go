package router

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

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
		"claude": {{Err: errors.New("503 unavailable")}},
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
