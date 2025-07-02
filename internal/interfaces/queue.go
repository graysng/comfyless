package interfaces

import (
	"context"

	"comfyless/internal/queue"
)

// QueueManager queue manager interface
type QueueManager interface {
	// Start starts the queue manager
	Start(ctx context.Context) error

	// AddTask adds a task to the queue
	AddTask(task *queue.Task) error

	// GetNextTask gets the next pending task
	GetNextTask() (*queue.Task, error)

	// UpdateTask updates task status
	UpdateTask(task *queue.Task) error

	// GetTask gets task by ID
	GetTask(taskID string) (*queue.Task, error)

	// GetTasksByStatus gets tasks by status
	GetTasksByStatus(status queue.TaskStatus) ([]*queue.Task, error)

	// GetMetrics gets queue metrics
	GetMetrics() (*queue.QueueMetrics, error)

	// AddCallback adds task status change callback
	AddCallback(callback queue.TaskCallback)

	// CancelTask cancels a task
	CancelTask(taskID string) error
}

// QueueManagerFactory queue manager factory
type QueueManagerFactory interface {
	// CreateQueueManager creates a queue manager
	CreateQueueManager(config interface{}) (QueueManager, error)
}
