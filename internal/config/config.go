package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ProviderConfig struct {
	Name               string              `json:"name"`
	Priority           int                 `json:"priority"`
	Command            string              `json:"command"`
	Args               []string            `json:"args,omitempty"`
	PromptMode         string              `json:"prompt_mode,omitempty"`
	TimeoutSeconds     int                 `json:"timeout_seconds,omitempty"`
	MaxRetries         int                 `json:"max_retries,omitempty"`
	RetryBackoffMillis int                 `json:"retry_backoff_millis,omitempty"`
	HealthArgs         []string            `json:"health_args,omitempty"`
	Env                map[string]string   `json:"env,omitempty"`
	ErrorPatterns      map[string][]string `json:"error_patterns,omitempty"`
	FailoverOn         []string            `json:"failover_on,omitempty"`
}

type Config struct {
	StateDir            string           `json:"state_dir,omitempty"`
	PollIntervalSeconds int              `json:"poll_interval_seconds,omitempty"`
	Providers           []ProviderConfig `json:"providers"`
}

func Default() Config {
	return Config{
		PollIntervalSeconds: 60,
		Providers: []ProviderConfig{
			{Name: "claude", Priority: 10, Command: "claude", Args: []string{"-p", "{{prompt}}"}, TimeoutSeconds: 1800, MaxRetries: 1, RetryBackoffMillis: 1500, HealthArgs: []string{"--version"}},
			{Name: "codex", Priority: 20, Command: "codex", Args: []string{"exec", "--skip-git-repo-check", "{{prompt}}"}, TimeoutSeconds: 1800, MaxRetries: 1, RetryBackoffMillis: 1500, HealthArgs: []string{"--version"}},
			{Name: "gemini", Priority: 30, Command: "gemini", Args: []string{"-p", "{{prompt}}"}, TimeoutSeconds: 1800, MaxRetries: 1, RetryBackoffMillis: 1500, HealthArgs: []string{"--version"}},
		},
	}
}

func DefaultPath() string {
	if p := strings.TrimSpace(os.Getenv("AIRUN_CONFIG")); p != "" {
		return p
	}
	return filepath.Join(".airun", "config.json")
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	migrateLegacyProviderDefaults(&cfg)
	if err := cfg.ValidateAndNormalize(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func migrateLegacyProviderDefaults(c *Config) {
	for i := range c.Providers {
		p := &c.Providers[i]
		if strings.TrimSpace(p.Name) != "codex" || strings.TrimSpace(p.Command) != "codex" {
			continue
		}
		promptMode := strings.TrimSpace(p.PromptMode)
		if promptMode != "" && promptMode != "arg" {
			continue
		}
		if len(p.Args) == 2 && p.Args[0] == "exec" && p.Args[1] == "{{prompt}}" {
			p.Args = []string{"exec", "--skip-git-repo-check", "{{prompt}}"}
		}
	}
}

func WriteDefault(path string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config already exists: %s", path)
		}
	}
	return Save(path, Default())
}

func Save(path string, cfg Config) error {
	return saveWithReplace(path, cfg, replaceConfigFile)
}

func saveWithReplace(path string, cfg Config, replace func(tempPath, targetPath string) error) error {
	if err := cfg.ValidateAndNormalize(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}

	f, err := os.CreateTemp(dir, ".airun-config-*")
	if err != nil {
		return err
	}
	tempPath := f.Name()
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
		_ = os.Remove(tempPath)
	}()

	if err := f.Chmod(mode); err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		closed = true
		return err
	}
	closed = true

	if err := replace(tempPath, path); err != nil {
		return err
	}
	return nil
}

func (c *Config) ValidateAndNormalize() error {
	if len(c.Providers) == 0 {
		return errors.New("at least one provider is required")
	}
	c.StateDir = strings.TrimSpace(c.StateDir)
	if c.PollIntervalSeconds <= 0 {
		c.PollIntervalSeconds = 60
	}

	seen := map[string]struct{}{}
	defaultFailover := []string{"quota", "rate_limit", "timeout", "outage", "unavailable"}
	for i := range c.Providers {
		p := &c.Providers[i]
		p.Name = strings.TrimSpace(p.Name)
		p.Command = strings.TrimSpace(p.Command)
		if p.Name == "" {
			return fmt.Errorf("provider %d has empty name", i)
		}
		if p.Command == "" {
			return fmt.Errorf("provider %q has empty command", p.Name)
		}
		if _, ok := seen[p.Name]; ok {
			return fmt.Errorf("duplicate provider name %q", p.Name)
		}
		seen[p.Name] = struct{}{}
		if p.PromptMode == "" {
			p.PromptMode = "arg"
		}
		if p.PromptMode != "arg" && p.PromptMode != "stdin" {
			return fmt.Errorf("provider %q prompt_mode must be arg or stdin", p.Name)
		}
		if p.TimeoutSeconds <= 0 {
			p.TimeoutSeconds = 1800
		}
		if p.MaxRetries < 0 {
			return fmt.Errorf("provider %q max_retries cannot be negative", p.Name)
		}
		if p.RetryBackoffMillis <= 0 {
			p.RetryBackoffMillis = 1000
		}
		if len(p.FailoverOn) == 0 {
			p.FailoverOn = append([]string(nil), defaultFailover...)
		}
	}
	sort.SliceStable(c.Providers, func(i, j int) bool { return c.Providers[i].Priority < c.Providers[j].Priority })
	return nil
}
