package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSortsProvidersByPriority(t *testing.T) {
	cfg := Config{Providers: []ProviderConfig{
		{Name: "gemini", Priority: 30, Command: "gemini"},
		{Name: "claude", Priority: 10, Command: "claude"},
		{Name: "codex", Priority: 20, Command: "codex"},
	}}
	if err := cfg.ValidateAndNormalize(); err != nil {
		t.Fatal(err)
	}
	want := []string{"claude", "codex", "gemini"}
	for i, name := range want {
		if cfg.Providers[i].Name != name {
			t.Fatalf("index %d got %s want %s", i, cfg.Providers[i].Name, name)
		}
	}
}

func TestValidateRejectsDuplicateProviderNames(t *testing.T) {
	cfg := Config{Providers: []ProviderConfig{
		{Name: "codex", Priority: 1, Command: "codex"},
		{Name: "codex", Priority: 2, Command: "other"},
	}}
	if err := cfg.ValidateAndNormalize(); err == nil {
		t.Fatal("expected duplicate provider validation error")
	}
}

func TestDefaultFailoverIsLimitedToKnownInfrastructureFailures(t *testing.T) {
	cfg := Config{Providers: []ProviderConfig{{Name: "one", Command: "one"}}}
	if err := cfg.ValidateAndNormalize(); err != nil {
		t.Fatal(err)
	}
	want := []string{"quota", "rate_limit", "timeout", "outage", "unavailable"}
	got := cfg.Providers[0].FailoverOn
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestDefaultUsesAutomaticExternalStateDir(t *testing.T) {
	cfg := Default()
	if err := cfg.ValidateAndNormalize(); err != nil {
		t.Fatal(err)
	}
	if cfg.StateDir != "" {
		t.Fatalf("default state_dir = %q, want automatic external state location", cfg.StateDir)
	}
}

func TestDefaultCodexSkipsGitRepoCheck(t *testing.T) {
	cfg := Default()
	var codex *ProviderConfig
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == "codex" {
			codex = &cfg.Providers[i]
			break
		}
	}
	if codex == nil {
		t.Fatal("default codex provider not found")
	}

	want := []string{"exec", "--skip-git-repo-check", "{{prompt}}"}
	if len(codex.Args) != len(want) {
		t.Fatalf("codex args = %v, want %v", codex.Args, want)
	}
	for i := range want {
		if codex.Args[i] != want[i] {
			t.Fatalf("codex args = %v, want %v", codex.Args, want)
		}
	}
}

func TestSavePreservesExistingConfigWhenReplacementFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	oldCfg := Config{Providers: []ProviderConfig{{Name: "old", Command: "old-ai"}}}
	if err := Save(path, oldCfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	newCfg := Config{Providers: []ProviderConfig{{Name: "new", Command: "new-ai"}}}
	replaceErr := errors.New("replace failed")
	err = saveWithReplace(path, newCfg, func(tempPath, targetPath string) error {
		if targetPath != path {
			t.Fatalf("replacement target = %q want %q", targetPath, path)
		}
		if _, statErr := os.Stat(tempPath); statErr != nil {
			t.Fatalf("temp config missing before replacement: %v", statErr)
		}
		return replaceErr
	})
	if !errors.Is(err, replaceErr) {
		t.Fatalf("save error = %v want %v", err, replaceErr)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("existing config changed after failed replacement\nbefore=%s\nafter=%s", before, after)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".airun-config-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary config files leaked: %v", matches)
	}
}
