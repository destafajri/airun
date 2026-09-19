package model

import "time"

type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"
	TaskRunning   TaskStatus = "running"
	TaskPaused    TaskStatus = "paused"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
)

type Attempt struct {
	Provider    string    `json:"provider"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	FailureKind string    `json:"failure_kind,omitempty"`
	Error       string    `json:"error,omitempty"`
	Output      string    `json:"output,omitempty"`
}

type Task struct {
	ID             string     `json:"id"`
	Prompt         string     `json:"prompt"`
	Status         TaskStatus `json:"status"`
	ActiveProvider string     `json:"active_provider,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	LastOutput     string     `json:"last_output,omitempty"`
	Attempts       []Attempt  `json:"attempts,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

func NewTask(id, prompt string) Task {
	now := time.Now().UTC()
	return Task{ID: id, Prompt: prompt, Status: TaskQueued, CreatedAt: now, UpdatedAt: now}
}
