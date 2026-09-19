package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/destafajri/smart-routing/internal/config"
)

func (a *App) setupProviders(configPath string) int {
	cfg, err := config.Load(configPath)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		cfg = config.Config{PollIntervalSeconds: 60}
	default:
		fmt.Fprintln(a.ErrOut, "error:", err)
		return 1
	}

	scanner := bufio.NewScanner(a.In)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	fmt.Fprintf(a.Out, "AI provider setup — %s\n", configPath)

	for {
		printProviderSetupList(a.Out, cfg.Providers)
		fmt.Fprint(a.Out, "\n1) Add provider\n2) Edit provider\n3) Remove provider\n4) Save and exit\n5) Exit without saving\n")
		choice, ok := promptLine(scanner, a.Out, "Choice", "")
		if !ok {
			fmt.Fprintln(a.ErrOut, "input ended before setup was saved")
			return 1
		}

		switch strings.TrimSpace(choice) {
		case "1":
			p, ok := a.promptProvider(scanner, config.ProviderConfig{Priority: nextProviderPriority(cfg.Providers)})
			if !ok {
				return 1
			}
			cfg.Providers = append(cfg.Providers, p)
		case "2":
			if len(cfg.Providers) == 0 {
				fmt.Fprintln(a.Out, "no providers to edit")
				continue
			}
			idx, ok := promptProviderIndex(scanner, a.Out, len(cfg.Providers), "Provider number to edit")
			if !ok {
				continue
			}
			p, inputOK := a.promptProvider(scanner, cfg.Providers[idx])
			if !inputOK {
				return 1
			}
			cfg.Providers[idx] = p
		case "3":
			if len(cfg.Providers) == 0 {
				fmt.Fprintln(a.Out, "no providers to remove")
				continue
			}
			idx, ok := promptProviderIndex(scanner, a.Out, len(cfg.Providers), "Provider number to remove")
			if !ok {
				continue
			}
			answer, inputOK := promptLine(scanner, a.Out, fmt.Sprintf("Remove %q? (y/N)", cfg.Providers[idx].Name), "n")
			if !inputOK {
				return 1
			}
			if strings.EqualFold(strings.TrimSpace(answer), "y") || strings.EqualFold(strings.TrimSpace(answer), "yes") {
				cfg.Providers = append(cfg.Providers[:idx], cfg.Providers[idx+1:]...)
			}
		case "4":
			if err := config.Save(configPath, cfg); err != nil {
				fmt.Fprintln(a.ErrOut, "cannot save provider configuration:", err)
				continue
			}
			fmt.Fprintf(a.Out, "saved provider configuration to %s\n", configPath)
			a.warnMissingProviders(cfg)
			return 0
		case "5":
			fmt.Fprintln(a.Out, "provider configuration not changed")
			return 0
		default:
			fmt.Fprintln(a.Out, "choose 1, 2, 3, 4, or 5")
		}
	}
}

func printProviderSetupList(out interface{ Write([]byte) (int, error) }, providers []config.ProviderConfig) {
	if len(providers) == 0 {
		fmt.Fprintln(out, "\nConfigured providers: none")
		return
	}
	fmt.Fprintln(out, "\nConfigured providers:")
	for i, p := range providers {
		fmt.Fprintf(out, "  %d) %s — priority %d — %s %s\n", i+1, p.Name, p.Priority, p.Command, strings.Join(p.Args, " "))
	}
}

func (a *App) promptProvider(scanner *bufio.Scanner, current config.ProviderConfig) (config.ProviderConfig, bool) {
	adding := strings.TrimSpace(current.Name) == ""
	nameDefault := current.Name
	name, ok := promptLine(scanner, a.Out, "Provider name", nameDefault)
	if !ok {
		return config.ProviderConfig{}, false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		fmt.Fprintln(a.ErrOut, "provider name cannot be empty")
		return config.ProviderConfig{}, false
	}

	if adding {
		current = providerTemplate(name, current.Priority)
	} else {
		current.Name = name
	}

	command, ok := promptLine(scanner, a.Out, "CLI command", current.Command)
	if !ok {
		return config.ProviderConfig{}, false
	}
	args, ok := promptLine(scanner, a.Out, "Arguments (space-separated)", strings.Join(current.Args, " "))
	if !ok {
		return config.ProviderConfig{}, false
	}
	promptMode, ok := promptLine(scanner, a.Out, "Prompt mode (arg/stdin)", defaultString(current.PromptMode, "arg"))
	if !ok {
		return config.ProviderConfig{}, false
	}
	priority, ok := promptInt(scanner, a.Out, "Priority", current.Priority)
	if !ok {
		return config.ProviderConfig{}, false
	}
	healthArgs, ok := promptLine(scanner, a.Out, "Health-check arguments", strings.Join(current.HealthArgs, " "))
	if !ok {
		return config.ProviderConfig{}, false
	}
	timeout, ok := promptInt(scanner, a.Out, "Timeout seconds", positiveOr(current.TimeoutSeconds, 1800))
	if !ok {
		return config.ProviderConfig{}, false
	}
	retries, ok := promptInt(scanner, a.Out, "Max retries", current.MaxRetries)
	if !ok {
		return config.ProviderConfig{}, false
	}
	backoff, ok := promptInt(scanner, a.Out, "Retry backoff milliseconds", positiveOr(current.RetryBackoffMillis, 1500))
	if !ok {
		return config.ProviderConfig{}, false
	}

	current.Name = name
	current.Command = strings.TrimSpace(command)
	current.Args = fieldsOrNil(args)
	current.PromptMode = strings.ToLower(strings.TrimSpace(promptMode))
	current.Priority = priority
	current.HealthArgs = fieldsOrNil(healthArgs)
	current.TimeoutSeconds = timeout
	current.MaxRetries = retries
	current.RetryBackoffMillis = backoff
	return current, true
}

func providerTemplate(name string, priority int) config.ProviderConfig {
	for _, p := range config.Default().Providers {
		if strings.EqualFold(p.Name, name) {
			p.Name = name
			p.Priority = priority
			if p.PromptMode == "" {
				p.PromptMode = "arg"
			}
			return p
		}
	}
	return config.ProviderConfig{
		Name:               name,
		Priority:           priority,
		Command:            name,
		Args:               []string{"{{prompt}}"},
		PromptMode:         "arg",
		TimeoutSeconds:     1800,
		MaxRetries:         1,
		RetryBackoffMillis: 1500,
		HealthArgs:         []string{"--version"},
	}
}

func nextProviderPriority(providers []config.ProviderConfig) int {
	maxPriority := 0
	for _, p := range providers {
		if p.Priority > maxPriority {
			maxPriority = p.Priority
		}
	}
	if maxPriority == 0 {
		return 10
	}
	return ((maxPriority / 10) + 1) * 10
}

func promptProviderIndex(scanner *bufio.Scanner, out interface{ Write([]byte) (int, error) }, count int, label string) (int, bool) {
	value, ok := promptLine(scanner, out, label, "")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 1 || n > count {
		fmt.Fprintf(out, "choose a provider number from 1 to %d\n", count)
		return 0, false
	}
	return n - 1, true
}

func promptLine(scanner *bufio.Scanner, out interface{ Write([]byte) (int, error) }, label, defaultValue string) (string, bool) {
	if defaultValue == "" {
		fmt.Fprintf(out, "%s: ", label)
	} else {
		fmt.Fprintf(out, "%s [%s]: ", label, defaultValue)
	}
	if !scanner.Scan() {
		return "", false
	}
	value := strings.TrimSpace(scanner.Text())
	if value == "" {
		return defaultValue, true
	}
	return value, true
}

func promptInt(scanner *bufio.Scanner, out interface{ Write([]byte) (int, error) }, label string, defaultValue int) (int, bool) {
	for {
		value, ok := promptLine(scanner, out, label, strconv.Itoa(defaultValue))
		if !ok {
			return 0, false
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return n, true
		}
		fmt.Fprintln(out, "enter a whole number")
	}
}

func fieldsOrNil(value string) []string {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func positiveOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
