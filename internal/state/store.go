package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/destafajri/airun/internal/model"
)

var unsafeTaskID = regexp.MustCompile("[^a-zA-Z0-9._-]+")

type snapshot struct {
	Tasks map[string]model.Task `json:"tasks"`
}

type Store struct{ path string }

func New(path string) *Store  { return &Store{path: path} }
func (s *Store) Path() string { return s.path }

func (s *Store) GetTask(id string) (model.Task, bool, error) {
	var out model.Task
	var ok bool
	err := s.withStateLock(func() error {
		ss, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		out, ok = ss.Tasks[id]
		return nil
	})
	return out, ok, err
}

func (s *Store) UpsertTask(task model.Task) error {
	return s.withStateLock(func() error {
		ss, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if ss.Tasks == nil {
			ss.Tasks = map[string]model.Task{}
		}
		task.UpdatedAt = time.Now().UTC()
		ss.Tasks[task.ID] = task
		return s.saveUnlocked(ss)
	})
}

func (s *Store) ListTasks(statuses ...model.TaskStatus) ([]model.Task, error) {
	wanted := map[model.TaskStatus]bool{}
	for _, st := range statuses {
		wanted[st] = true
	}
	var out []model.Task
	err := s.withStateLock(func() error {
		ss, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		for _, task := range ss.Tasks {
			if len(wanted) == 0 || wanted[task.Status] {
				out = append(out, task)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, err
}

func (s *Store) AcquireTaskLock(taskID string) (func(), error) {
	lock, acquired, err := s.tryAcquireTaskLock(taskID)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, fmt.Errorf("task %s is already executing", taskID)
	}
	if err := lock.writeOwnerPID(); err != nil {
		lock.release()
		return nil, err
	}
	return lock.release, nil
}

// ReconcileOrphanedTasks converts running tasks with no live task-lock owner into
// paused tasks. Advisory locks are released by the OS when a process exits, so
// no pathname deletion or PID liveness heuristic is required.
func (s *Store) ReconcileOrphanedTasks() (int, error) {
	recovered := 0
	err := s.withStateLock(func() error {
		ss, err := s.loadUnlocked()
		if err != nil {
			return err
		}

		var held []*fileLock
		defer func() {
			for _, lock := range held {
				lock.release()
			}
		}()

		changed := false
		for id, task := range ss.Tasks {
			if task.Status != model.TaskRunning {
				continue
			}
			lock, acquired, err := s.tryAcquireTaskLock(id)
			if err != nil {
				return err
			}
			if !acquired {
				continue
			}
			held = append(held, lock)

			task.Status = model.TaskPaused
			task.ActiveProvider = ""
			task.LastError = "recovered orphaned running task after interrupted execution"
			task.UpdatedAt = time.Now().UTC()
			ss.Tasks[id] = task
			recovered++
			changed = true
		}
		if !changed {
			return nil
		}
		return s.saveUnlocked(ss)
	})
	return recovered, err
}

func (s *Store) tryAcquireTaskLock(taskID string) (*fileLock, bool, error) {
	dir := filepath.Join(filepath.Dir(s.path), "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false, err
	}
	return acquireFileLock(s.taskLockPath(taskID))
}

func (s *Store) taskLockPath(taskID string) string {
	safe := unsafeTaskID.ReplaceAllString(taskID, "_")
	return filepath.Join(filepath.Dir(s.path), "locks", safe+".lock")
}

type fileLock struct {
	file *os.File
	once sync.Once
}

func acquireFileLock(path string) (*fileLock, bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	acquired, err := tryLockFile(f)
	if err != nil {
		_ = f.Close()
		return nil, false, err
	}
	if !acquired {
		_ = f.Close()
		return nil, false, nil
	}
	return &fileLock{file: f}, true, nil
}

func (l *fileLock) writeOwnerPID() error {
	if err := l.file.Truncate(0); err != nil {
		return err
	}
	if _, err := l.file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(l.file, "%d\n", os.Getpid()); err != nil {
		return err
	}
	return l.file.Sync()
}

func (l *fileLock) release() {
	l.once.Do(func() {
		_ = unlockFile(l.file)
		_ = l.file.Close()
	})
}

func (s *Store) withStateLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	lockPath := s.path + ".lock"
	for i := 0; i < 100; i++ {
		lock, acquired, err := acquireFileLock(lockPath)
		if err != nil {
			return err
		}
		if acquired {
			defer lock.release()
			return fn()
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("timed out acquiring state lock")
}

func (s *Store) loadUnlocked() (snapshot, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot{Tasks: map[string]model.Task{}}, nil
	}
	if err != nil {
		return snapshot{}, err
	}
	var ss snapshot
	if len(b) == 0 {
		return snapshot{Tasks: map[string]model.Task{}}, nil
	}
	if err := json.Unmarshal(b, &ss); err != nil {
		return snapshot{}, fmt.Errorf("decode state: %w", err)
	}
	if ss.Tasks == nil {
		ss.Tasks = map[string]model.Task{}
	}
	return ss, nil
}

func (s *Store) saveUnlocked(ss snapshot) error {
	b, err := json.MarshalIndent(ss, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
