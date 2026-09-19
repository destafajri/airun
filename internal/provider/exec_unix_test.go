//go:build !windows

package provider

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/destafajri/airun/internal/config"
)

func TestRunTimeoutStopsDescendantProcessTree(t *testing.T) {
	dir := t.TempDir()
	heartbeat := filepath.Join(dir, "heartbeat")
	p := config.ProviderConfig{
		Name:           "fake",
		Command:        "sh",
		Args:           []string{"-c", "while :; do echo tick >> heartbeat; sleep 0.1; done & wait"},
		PromptMode:     "stdin",
		TimeoutSeconds: 1,
	}
	res := NewExecRunner().Run(context.Background(), p, "", dir, io.Discard)
	if res.Err == nil {
		t.Fatal("expected timeout")
	}
	info1, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatalf("heartbeat not created: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	info2, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if info2.Size() != info1.Size() {
		t.Fatalf("descendant kept running after timeout: size grew from %d to %d", info1.Size(), info2.Size())
	}
}
