package state

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/destafajri/airun/internal/model"
)

func TestStorePersistsTask(t *testing.T) {
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


func TestReconcileOrphanedRunningTaskAfterProcessDeath(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	s := New(statePath)
	task := model.NewTask("crashed-task", "continue safely")
	task.Status = model.TaskRunning
	task.ActiveProvider = "claude"
	if err := s.UpsertTask(task); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestCrashLockHelper")
	cmd.Env = append(os.Environ(),
		"AIRUN_TEST_CRASH_LOCK=1",
		"AIRUN_TEST_STATE_PATH="+statePath,
		"AIRUN_TEST_TASK_ID="+task.ID,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("crash helper: %v: %s", err, out)
	}

	recovered, err := s.ReconcileOrphanedTasks()
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered %d tasks, want 1", recovered)
	}
	got, ok, err := s.GetTask(task.ID)
	if err != nil || !ok {
		t.Fatalf("get task: ok=%v err=%v", ok, err)
	}
	if got.Status != model.TaskPaused {
		t.Fatalf("status %s, want paused", got.Status)
	}
	if got.ActiveProvider != "" {
		t.Fatalf("active provider %q, want empty", got.ActiveProvider)
	}
	if release, err := s.AcquireTaskLock(task.ID); err != nil {
		t.Fatalf("stale task lock was not reclaimed: %v", err)
	} else {
		release()
	}
}

func TestCrashLockHelper(t *testing.T) {
	if os.Getenv("AIRUN_TEST_CRASH_LOCK") != "1" {
		return
	}
	s := New(os.Getenv("AIRUN_TEST_STATE_PATH"))
	if _, err := s.AcquireTaskLock(os.Getenv("AIRUN_TEST_TASK_ID")); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestReconcileLeavesLiveRunningTaskAlone(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "state.json"))
	task := model.NewTask("live-task", "work")
	task.Status = model.TaskRunning
	if err := s.UpsertTask(task); err != nil {
		t.Fatal(err)
	}
	release, err := s.AcquireTaskLock(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	recovered, err := s.ReconcileOrphanedTasks()
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 0 {
		t.Fatalf("recovered %d live tasks, want 0", recovered)
	}
	got, _, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.TaskRunning {
		t.Fatalf("status %s, want running", got.Status)
	}
}


func TestConcurrentStaleTaskLockReclaimHasSingleOwner(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	taskID := "stale-race"
	s := New(statePath)

	cmd := exec.Command(os.Args[0], "-test.run=TestCrashLockHelper")
	cmd.Env = append(os.Environ(),
		"AIRUN_TEST_CRASH_LOCK=1",
		"AIRUN_TEST_STATE_PATH="+statePath,
		"AIRUN_TEST_TASK_ID="+taskID,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("crash helper: %v: %s", err, out)
	}

	const contenders = 64
	start := make(chan struct{})
	releaseWinner := make(chan struct{})
	results := make(chan bool, contenders)
	var wg sync.WaitGroup
	wg.Add(contenders)
	for i := 0; i < contenders; i++ {
		go func() {
			defer wg.Done()
			<-start
			release, err := s.AcquireTaskLock(taskID)
			if err != nil {
				results <- false
				return
			}
			results <- true
			<-releaseWinner
			release()
		}()
	}
	close(start)
	successes := 0
	for i := 0; i < contenders; i++ {
		if <-results {
			successes++
		}
	}
	close(releaseWinner)
	wg.Wait()

	if successes != 1 {
		t.Fatalf("successful concurrent lock owners = %d, want 1", successes)
	}
}
