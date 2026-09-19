package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/destafajri/smart-routing/internal/config"
)

func TestResolveStateDirDefaultsOutsideProviderWorkdir(t *testing.T) {
	stateHome := filepath.Join(t.TempDir(), "state-home")
	t.Setenv("AIRUN_STATE_HOME", stateHome)
	workdir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := resolveStateDir(config.Config{}, workdir)
	if err != nil {
		t.Fatal(err)
	}
	if pathWithin(got, workdir) {
		t.Fatalf("default state dir %q must be outside provider workdir %q", got, workdir)
	}
	wantRoot := filepath.Join(stateHome, "projects")
	if !pathWithin(got, wantRoot) {
		t.Fatalf("default state dir %q must be under %q", got, wantRoot)
	}

	again, err := resolveStateDir(config.Config{}, workdir)
	if err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Fatalf("state dir not stable: first=%q second=%q", got, again)
	}
}

func TestResolveStateDirNamespacesProjectsByCanonicalPath(t *testing.T) {
	stateHome := filepath.Join(t.TempDir(), "state-home")
	t.Setenv("AIRUN_STATE_HOME", stateHome)
	root := t.TempDir()
	projectA := filepath.Join(root, "a")
	projectB := filepath.Join(root, "b")
	for _, dir := range []string{projectA, projectB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	a, err := resolveStateDir(config.Config{}, projectA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := resolveStateDir(config.Config{}, projectB)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("different projects share state dir %q", a)
	}
}

func TestResolveStateDirHonorsExplicitOverride(t *testing.T) {
	workdir := t.TempDir()
	got, err := resolveStateDir(config.Config{StateDir: "custom-state"}, workdir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(workdir, "custom-state")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && rel != "." && !filepath.IsAbs(rel) &&
		len(rel) > 0 && rel[:1] != "."
}
