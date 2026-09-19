package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/destafajri/smart-routing/internal/config"
)

func TestWarnMissingProvidersReportsConfiguredCommandNotFound(t *testing.T) {
	var errOut bytes.Buffer
	app := &App{
		ErrOut: &errOut,
		LookPath: func(command string) (string, error) {
			if command == "installed-ai" {
				return "/usr/bin/installed-ai", nil
			}
			return "", errors.New("not found")
		},
	}
	cfg := config.Config{Providers: []config.ProviderConfig{
		{Name: "installed", Command: "installed-ai"},
		{Name: "missing", Command: "missing-ai"},
	}}

	app.warnMissingProviders(cfg)

	got := errOut.String()
	if strings.Contains(got, "installed-ai") {
		t.Fatalf("installed provider should not warn: %q", got)
	}
	if !strings.Contains(got, `provider "missing"`) || !strings.Contains(got, `"missing-ai"`) {
		t.Fatalf("missing provider warning lacks provider/command: %q", got)
	}
	if !strings.Contains(got, "airun setup") {
		t.Fatalf("warning should point to interactive setup: %q", got)
	}
}

func TestSetupCommandCreatesProviderConfigInteractively(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".airun", "config.json")
	input := strings.Join([]string{
		"1",             // add provider
		"custom",        // name
		"custom-ai",     // command
		"run {{prompt}}",// args
		"arg",           // prompt mode
		"15",            // priority
		"--version",     // health args
		"1200",          // timeout
		"2",             // max retries
		"500",           // retry backoff
		"4",             // save and exit
		"",
	}, "\n")
	var out, errOut bytes.Buffer
	app := &App{
		In: strings.NewReader(input),
		Out: &out,
		ErrOut: &errOut,
		Getwd: func() (string, error) { return dir, nil },
		LookPath: func(command string) (string, error) { return "", os.ErrNotExist },
	}

	code := app.Run(context.Background(), []string{"--config", configPath, "setup"})
	if code != 0 {
		t.Fatalf("setup exit code = %d, stderr=%q stdout=%q", code, errOut.String(), out.String())
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(cfg.Providers))
	}
	p := cfg.Providers[0]
	if p.Name != "custom" || p.Command != "custom-ai" || p.Priority != 15 {
		t.Fatalf("provider = %+v", p)
	}
	if strings.Join(p.Args, " ") != "run {{prompt}}" {
		t.Fatalf("args = %v", p.Args)
	}
	if p.PromptMode != "arg" || p.TimeoutSeconds != 1200 || p.MaxRetries != 2 || p.RetryBackoffMillis != 500 {
		t.Fatalf("provider advanced settings = %+v", p)
	}
	if !strings.Contains(out.String(), "saved provider configuration") {
		t.Fatalf("setup did not confirm save: %q", out.String())
	}
}

func TestSetupCommandEditsAndRemovesProviders(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	initial := config.Config{
		PollIntervalSeconds: 60,
		Providers: []config.ProviderConfig{
			{Name: "one", Priority: 10, Command: "one", Args: []string{"{{prompt}}"}},
			{Name: "two", Priority: 20, Command: "two", Args: []string{"{{prompt}}"}},
		},
	}
	if err := config.Save(configPath, initial); err != nil {
		t.Fatal(err)
	}

	// Edit provider #1: keep most values, change command and priority.
	// Then remove provider #2 and save.
	input := strings.Join([]string{
		"2", // edit
		"1", // provider one
		"",  // keep name
		"one-new",
		"",  // keep args
		"",  // keep prompt mode
		"5", // priority
		"",  // health args
		"",  // timeout
		"",  // retries
		"",  // backoff
		"3", // remove
		"2", // provider two
		"y", // confirm
		"4", // save
		"",
	}, "\n")
	var out, errOut bytes.Buffer
	app := &App{In: strings.NewReader(input), Out: &out, ErrOut: &errOut, Getwd: func() (string, error) { return dir, nil }}

	if code := app.Run(context.Background(), []string{"--config", configPath, "configure"}); code != 0 {
		t.Fatalf("configure exit = %d stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %+v", cfg.Providers)
	}
	if cfg.Providers[0].Name != "one" || cfg.Providers[0].Command != "one-new" || cfg.Providers[0].Priority != 5 {
		t.Fatalf("edited provider = %+v", cfg.Providers[0])
	}
}


func TestSetupCommandCanClearArgsAndHealthCheck(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	input := strings.Join([]string{
		"1",         // add provider
		"custom",    // name
		"custom-ai", // command
		"-",         // clear args
		"stdin",     // prompt mode
		"10",        // priority
		"-",         // clear health args
		"1200",      // timeout
		"1",         // retries
		"500",       // backoff
		"4",         // save
		"",
	}, "\n")
	var out, errOut bytes.Buffer
	app := &App{
		In: strings.NewReader(input),
		Out: &out,
		ErrOut: &errOut,
		Getwd: func() (string, error) { return dir, nil },
		LookPath: func(command string) (string, error) { return "", os.ErrNotExist },
	}

	if code := app.Run(context.Background(), []string{"--config", configPath, "setup"}); code != 0 {
		t.Fatalf("setup exit = %d stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Providers[0]
	if p.PromptMode != "stdin" {
		t.Fatalf("prompt mode = %q want stdin", p.PromptMode)
	}
	if len(p.Args) != 0 {
		t.Fatalf("args = %v, want empty", p.Args)
	}
	if len(p.HealthArgs) != 0 {
		t.Fatalf("health args = %v, want empty", p.HealthArgs)
	}
}
