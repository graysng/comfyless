package api

import (
	"net/http"
	"sort"

	"comfyless/internal/interfaces"
	"comfyless/internal/queue"

	"github.com/gin-gonic/gin"
)

// Handler API handler
type Handler struct {
	queueManager  *queue.Manager
	workerManager interfaces.WorkerManager
}

// NewHandler creates API handler
func NewHandler(queueManager *queue.Manager, workerManager interfaces.WorkerManager) *Handler {
	return &Handler{
		queueManager:  queueManager,
		workerManager: workerManager,
	}
}

// RegisterRoutes registers routes
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Task related routes
	taskGroup := r.Group("/api/v1/tasks")
	{
		taskGroup.POST("", h.submitTask)
		taskGroup.GET("/:id", h.getTask)
		taskGroup.GET("", h.listTasks)
		taskGroup.DELETE("/:id", h.cancelTask)
	}

	// Worker related routes
	workerGroup := r.Group("/api/v1/workers")
	{
		workerGroup.GET("", h.listWorkers)
		workerGroup.GET("/:id", h.getWorker)
		workerGroup.GET("/metrics", h.getWorkerMetrics)
	}

	// Queue related routes
	queueGroup := r.Group("/api/v1/queue")
	{
		queueGroup.GET("/metrics", h.getQueueMetrics)
	}

	// Health checks
	r.GET("/health", h.healthCheck)
	r.GET("/ready", h.readinessCheck)
}

// submitTask submits task
func (h *Handler) submitTask(c *gin.Context) {
	var req SubmitTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create task
	task := queue.NewTask(req.WorkflowID, req.Priority, req.Payload)

	// Add to queue
	if err := h.queueManager.AddTask(task); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, TaskResponse{
		ID:         task.ID,
		WorkflowID: task.WorkflowID,
		Status:     string(task.Status),
		Priority:   task.Priority,
		CreatedAt:  task.CreatedAt,
	})
}

// getTask gets task details
func (h *Handler) getTask(c *gin.Context) {
	taskID := c.Param("id")

	task, err := h.queueManager.GetTask(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	c.JSON(http.StatusOK, TaskResponse{
		ID:          task.ID,
		WorkflowID:  task.WorkflowID,
		Status:      string(task.Status),
		Priority:    task.Priority,
		Payload:     task.Payload,
		WorkerID:    task.WorkerID,
		CreatedAt:   task.CreatedAt,
		StartedAt:   task.StartedAt,
		CompletedAt: task.CompletedAt,
		Result:      task.Result,
		Error:       task.Error,
		RetryCount:  task.RetryCount,
		MaxRetries:  task.MaxRetries,
	})
}

// listTasks lists tasks
func (h *Handler) listTasks(c *gin.Context) {
	status := c.Query("status")

	var tasks []*queue.Task
	var err error

	if status != "" {
		tasks, err = h.queueManager.GetTasksByStatus(queue.TaskStatus(status))
	} else {
		// If no status specified, return tasks of all statuses
		allTasks := make([]*queue.Task, 0)

		// Get tasks of various statuses
		pendingTasks, _ := h.queueManager.GetTasksByStatus(queue.TaskStatusPending)
		runningTasks, _ := h.queueManager.GetTasksByStatus(queue.TaskStatusRunning)
		completedTasks, _ := h.queueManager.GetTasksByStatus(queue.TaskStatusCompleted)
		failedTasks, _ := h.queueManager.GetTasksByStatus(queue.TaskStatusFailed)
		cancelledTasks, _ := h.queueManager.GetTasksByStatus(queue.TaskStatusCancelled)

		// Merge all tasks
		allTasks = append(allTasks, pendingTasks...)
		allTasks = append(allTasks, runningTasks...)
		allTasks = append(allTasks, completedTasks...)
		allTasks = append(allTasks, failedTasks...)
		allTasks = append(allTasks, cancelledTasks...)

		// Sort by creation time in descending order (newest first)
		sort.Slice(allTasks, func(i, j int) bool {
			return allTasks[i].CreatedAt.After(allTasks[j].CreatedAt)
		})

		tasks = allTasks
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var response []TaskResponse
	for _, task := range tasks {
		response = append(response, TaskResponse{
			ID:          task.ID,
			WorkflowID:  task.WorkflowID,
			Status:      string(task.Status),
			Priority:    task.Priority,
			WorkerID:    task.WorkerID,
			CreatedAt:   task.CreatedAt,
			StartedAt:   task.StartedAt,
			CompletedAt: task.CompletedAt,
			RetryCount:  task.RetryCount,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"tasks": response,
		"count": len(response),
	})
}

// cancelTask cancels task
func (h *Handler) cancelTask(c *gin.Context) {
	taskID := c.Param("id")

	task, err := h.queueManager.GetTask(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	if task.Status != queue.TaskStatusPending && task.Status != queue.TaskStatusRunning {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Task cannot be cancelled"})
		return
	}

	task.MarkCancelled()
	if err := h.queueManager.UpdateTask(task); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Task cancelled successfully"})
}

// listWorkers lists workers
func (h *Handler) listWorkers(c *gin.Context) {
	workers, err := h.workerManager.ListWorkers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var response []WorkerResponse
	for _, worker := range workers {
		response = append(response, WorkerResponse{
			ID:              worker.ID,
			PodName:         worker.PodName,
			Status:          string(worker.Status),
			Endpoint:        worker.Endpoint,
			Port:            worker.Port,
			CurrentTaskID:   worker.CurrentTaskID,
			CreatedAt:       worker.CreatedAt,
			StartedAt:       worker.StartedAt,
			LastActiveAt:    worker.LastActiveAt,
			LastHealthCheck: worker.LastHealthCheck,
			HealthStatus:    worker.HealthStatus,
			Resources:       worker.Resources,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"workers": response,
		"count":   len(response),
	})
}

// getWorker gets worker details
func (h *Handler) getWorker(c *gin.Context) {
	workerID := c.Param("id")

	worker, err := h.workerManager.GetWorkerByID(workerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Worker not found"})
		return
	}

	c.JSON(http.StatusOK, WorkerResponse{
		ID:              worker.ID,
		PodName:         worker.PodName,
		Status:          string(worker.Status),
		Endpoint:        worker.Endpoint,
		Port:            worker.Port,
		CurrentTaskID:   worker.CurrentTaskID,
		CreatedAt:       worker.CreatedAt,
		StartedAt:       worker.StartedAt,
		LastActiveAt:    worker.LastActiveAt,
		LastHealthCheck: worker.LastHealthCheck,
		HealthStatus:    worker.HealthStatus,
		Resources:       worker.Resources,
	})
}

// getWorkerMetrics gets worker metrics
func (h *Handler) getWorkerMetrics(c *gin.Context) {
	metrics := h.workerManager.GetWorkerMetrics()
	c.JSON(http.StatusOK, metrics)
}

// getQueueMetrics gets queue metrics
func (h *Handler) getQueueMetrics(c *gin.Context) {
	metrics, err := h.queueManager.GetMetrics()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, metrics)
}

// healthCheck performs health check
func (h *Handler) healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
	})
}

// readinessCheck performs readiness check
func (h *Handler) readinessCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ready",
	})
}
