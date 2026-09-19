package config

import "testing"

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
