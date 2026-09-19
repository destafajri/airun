package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/destafajri/smart-routing/internal/model"
)

const incompleteLockGrace = 2 * time.Second

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
	dir := filepath.Join(filepath.Dir(s.path), "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := s.taskLockPath(taskID)
	for attempt := 0; attempt < 3; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if _, writeErr := fmt.Fprintf(f, "%d\n", os.Getpid()); writeErr != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return nil, writeErr
			}
			if closeErr := f.Close(); closeErr != nil {
				_ = os.Remove(path)
				return nil, closeErr
			}
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		stale, staleErr := taskLockStale(path)
		if staleErr != nil {
			return nil, staleErr
		}
		if !stale {
			return nil, fmt.Errorf("task %s is already executing", taskID)
		}
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return nil, removeErr
		}
	}
	return nil, fmt.Errorf("task %s lock could not be acquired", taskID)
}

// ReconcileOrphanedTasks converts running tasks whose owner process is gone into
// paused tasks so they can be resumed safely after a crash or machine restart.
func (s *Store) ReconcileOrphanedTasks() (int, error) {
	recovered := 0
	err := s.withStateLock(func() error {
		ss, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		changed := false
		for id, task := range ss.Tasks {
			if task.Status != model.TaskRunning {
				continue
			}
			path := s.taskLockPath(id)
			stale, err := taskLockStale(path)
			if err != nil {
				return err
			}
			if !stale {
				continue
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
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

func (s *Store) taskLockPath(taskID string) string {
	safe := unsafeTaskID.ReplaceAllString(taskID, "_")
	return filepath.Join(filepath.Dir(s.path), "locks", safe+".lock")
}

func taskLockStale(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return time.Since(info.ModTime()) >= incompleteLockGrace, nil
	}
	return !processAlive(pid), nil
}

func (s *Store) withStateLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	lock := s.path + ".lock"
	for i := 0; i < 100; i++ {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			defer os.Remove(lock)
			return fn()
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(lock)
			continue
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
