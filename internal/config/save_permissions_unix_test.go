//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSavePreservesExistingConfigPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	initial := Config{Providers: []ProviderConfig{{Name: "old", Command: "old-ai"}}}
	if err := Save(path, initial); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	updated := Config{Providers: []ProviderConfig{{Name: "new", Command: "new-ai"}}}
	if err := Save(path, updated); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %04o, want 0600", got)
	}
}
