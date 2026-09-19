package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/destafajri/smart-routing/internal/model"
)

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
	safe := regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(taskID, "_")
	path := filepath.Join(dir, safe+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("task %s is already executing", taskID)
		}
		return nil, err
	}
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
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
