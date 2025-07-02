package api

import (
	"comfyless/internal/interfaces"
	"time"
)

// SubmitTaskRequest submit task request
type SubmitTaskRequest struct {
	WorkflowID string                 `json:"workflow_id" binding:"required"`
	Priority   int                    `json:"priority"`
	Payload    map[string]interface{} `json:"payload" binding:"required"`
}

// TaskResponse task response
type TaskResponse struct {
	ID          string                 `json:"id"`
	WorkflowID  string                 `json:"workflow_id"`
	Status      string                 `json:"status"`
	Priority    int                    `json:"priority"`
	Payload     map[string]interface{} `json:"payload,omitempty"`
	WorkerID    string                 `json:"worker_id,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	StartedAt   *time.Time             `json:"started_at,omitempty"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	Result      map[string]interface{} `json:"result,omitempty"`
	Error       string                 `json:"error,omitempty"`
	RetryCount  int                    `json:"retry_count"`
	MaxRetries  int                    `json:"max_retries"`
}

// WorkerResponse worker response
type WorkerResponse struct {
	ID              string                  `json:"id"`
	PodName         string                  `json:"pod_name"`
	Status          string                  `json:"status"`
	Endpoint        string                  `json:"endpoint"`
	Port            int                     `json:"port"`
	CurrentTaskID   string                  `json:"current_task_id,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	StartedAt       *time.Time              `json:"started_at,omitempty"`
	LastActiveAt    time.Time               `json:"last_active_at"`
	LastHealthCheck time.Time               `json:"last_health_check"`
	HealthStatus    bool                    `json:"health_status"`
	Resources       interfaces.ResourceSpec `json:"resources"`
}

// ErrorResponse error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}
