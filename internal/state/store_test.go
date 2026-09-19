package state

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/destafajri/smart-routing/internal/model"
)

func TestStorePersistsTaskAtomically(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "state.json"))
	task := model.Task{ID: "t1", Prompt: "fix tests", Status: model.TaskPaused, UpdatedAt: time.Now()}
	if err := s.UpsertTask(task); err != nil {
		t.Fatal(err)
	}

	s2 := New(filepath.Join(dir, "state.json"))
	got, ok, err := s2.GetTask("t1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("task not found")
	}
	if got.Prompt != task.Prompt || got.Status != model.TaskPaused {
		t.Fatalf("unexpected task: %+v", got)
	}
}

func TestAcquireTaskLockPreventsDuplicateExecution(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "state.json"))
	release, err := s.AcquireTaskLock("same-task")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := s.AcquireTaskLock("same-task"); err == nil {
		t.Fatal("expected second lock acquisition to fail")
	}
}
