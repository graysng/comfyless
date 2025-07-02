package queue

import (
	"time"

	"github.com/google/uuid"
)

// TaskStatus task status
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

// Task task definition
type Task struct {
	ID          string                 `json:"id"`
	WorkflowID  string                 `json:"workflow_id"`
	Priority    int                    `json:"priority"`
	Status      TaskStatus             `json:"status"`
	Payload     map[string]interface{} `json:"payload"`
	WorkerID    string                 `json:"worker_id,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	StartedAt   *time.Time             `json:"started_at,omitempty"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	Result      map[string]interface{} `json:"result,omitempty"`
	Error       string                 `json:"error,omitempty"`
	RetryCount  int                    `json:"retry_count"`
	MaxRetries  int                    `json:"max_retries"`
}

// NewTask creates new task
func NewTask(workflowID string, priority int, payload map[string]interface{}) *Task {
	now := time.Now()
	return &Task{
		ID:         uuid.New().String(),
		WorkflowID: workflowID,
		Priority:   priority,
		Status:     TaskStatusPending,
		Payload:    payload,
		RetryCount: 0,
		MaxRetries: 3,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// CanRetry checks if task can be retried
func (t *Task) CanRetry() bool {
	return t.RetryCount < t.MaxRetries && t.Status == TaskStatusFailed
}

// MarkStarted marks task as started
func (t *Task) MarkStarted(workerID string) {
	t.Status = TaskStatusRunning
	t.WorkerID = workerID
	now := time.Now()
	t.StartedAt = &now
	t.UpdatedAt = now
}

// MarkCompleted marks task as completed
func (t *Task) MarkCompleted(result map[string]interface{}) {
	t.Status = TaskStatusCompleted
	t.Result = result
	now := time.Now()
	t.CompletedAt = &now
	t.UpdatedAt = now
}

// MarkFailed marks task as failed
func (t *Task) MarkFailed(errorMsg string) {
	t.Status = TaskStatusFailed
	t.Error = errorMsg
	t.RetryCount++
	t.UpdatedAt = time.Now()
}

// MarkCancelled marks task as cancelled
func (t *Task) MarkCancelled() {
	t.Status = TaskStatusCancelled
	t.UpdatedAt = time.Now()
}

// TaskCallback task callback function type
type TaskCallback func(*Task)

// QueueMetrics queue metrics
type QueueMetrics struct {
	TotalTasks     int `json:"total_tasks"`
	PendingTasks   int `json:"pending_tasks"`
	RunningTasks   int `json:"running_tasks"`
	CompletedTasks int `json:"completed_tasks"`
	FailedTasks    int `json:"failed_tasks"`
	CancelledTasks int `json:"cancelled_tasks"`
}
