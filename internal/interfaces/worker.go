package interfaces

import (
	"context"
	"time"
)

// WorkerManager worker manager interface
type WorkerManager interface {
	// Start starts the worker manager
	Start(ctx context.Context) error

	// GetAvailableWorker gets an available worker
	GetAvailableWorker() (*Worker, error)

	// GetWorkerByID gets worker by ID
	GetWorkerByID(workerID string) (*Worker, error)

	// GetWorkerMetrics gets worker metrics
	GetWorkerMetrics() *WorkerMetrics

	// CreateWorker creates a new worker
	CreateWorker() (*Worker, error)

	// TerminateWorker terminates a worker
	TerminateWorker(workerID string) error

	// UpdateWorkerStatus updates worker status
	UpdateWorkerStatus(workerID string, status WorkerStatus) error

	// AssignTask assigns a task to worker
	AssignTask(workerID string, taskID string) error

	// CompleteTask marks worker as completed task
	CompleteTask(workerID string) error

	// ListWorkers lists all workers
	ListWorkers() ([]*Worker, error)
}

// WorkerManagerFactory worker manager factory
type WorkerManagerFactory interface {
	// CreateWorkerManager creates a worker manager
	CreateWorkerManager(config interface{}) (WorkerManager, error)
}

// WorkerInterface defines the worker contract
type WorkerInterface interface {
	ID() string
	Status() WorkerStatus
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Execute(taskID string, workflow map[string]interface{}) error
	GetMetrics() WorkerMetrics
	GetResourceSpec() ResourceSpec
}

// WorkerProvider Worker provider type
type WorkerProvider string

const (
	DockerProvider WorkerProvider = "docker"
	NovitaProvider WorkerProvider = "novita"
)

// WorkerStatus Worker status
type WorkerStatus string

const (
	WorkerStatusPending  WorkerStatus = "pending"
	WorkerStatusStarting WorkerStatus = "starting"
	WorkerStatusRunning  WorkerStatus = "running"
	WorkerStatusIdle     WorkerStatus = "idle"
	WorkerStatusBusy     WorkerStatus = "busy"
	WorkerStatusStopped  WorkerStatus = "stopped"
	WorkerStatusError    WorkerStatus = "error"
)

// ResourceSpec Resource specification
type ResourceSpec struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
	GPU    string `json:"gpu,omitempty"`
}

// WorkerMetrics Worker metrics
type WorkerMetrics struct {
	TotalWorkers    int `json:"total_workers"`
	IdleWorkers     int `json:"idle_workers"`
	RunningWorkers  int `json:"running_workers"`
	StartingWorkers int `json:"starting_workers"`
	FailedWorkers   int `json:"failed_workers"`
}

// Worker represents worker information and state
type Worker struct {
	ID              string         `json:"id"`
	Provider        WorkerProvider `json:"provider"`
	PodName         string         `json:"pod_name"`
	NodeName        string         `json:"node_name"`
	Status          WorkerStatus   `json:"status"`
	Endpoint        string         `json:"endpoint"`
	Port            int            `json:"port"`
	CurrentTaskID   string         `json:"current_task_id,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	StartedAt       *time.Time     `json:"started_at,omitempty"`
	LastActiveAt    time.Time      `json:"last_active_at"`
	LastHealthCheck time.Time      `json:"last_health_check"`
	HealthStatus    bool           `json:"health_status"`
	Capabilities    []string       `json:"capabilities,omitempty"`
	Resources       ResourceSpec   `json:"resources"`
}

// Worker methods
func (w *Worker) IsIdle() bool {
	return w.Status == WorkerStatusIdle && w.CurrentTaskID == ""
}

func (w *Worker) CanBeTerminated(idleTimeout time.Duration) bool {
	return w.IsIdle() && time.Since(w.LastActiveAt) > idleTimeout
}

func (w *Worker) UpdateLastActive() {
	w.LastActiveAt = time.Now()
}

func (w *Worker) MarkStarted(endpoint string, port int) {
	w.Status = WorkerStatusRunning
	w.Endpoint = endpoint
	w.Port = port
	now := time.Now()
	w.StartedAt = &now
	w.UpdateLastActive()
}

func (w *Worker) MarkIdle() {
	w.Status = WorkerStatusIdle
	w.CurrentTaskID = ""
	w.UpdateLastActive()
}

func (w *Worker) MarkBusy(taskID string) {
	w.Status = WorkerStatusRunning
	w.CurrentTaskID = taskID
	w.UpdateLastActive()
}

func (w *Worker) MarkFailed() {
	w.Status = WorkerStatusError
}
