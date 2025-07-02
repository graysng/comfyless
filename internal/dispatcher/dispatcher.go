package dispatcher

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"comfyless/internal/config"
	"comfyless/internal/interfaces"
	"comfyless/internal/queue"

	"github.com/sirupsen/logrus"
)

// Dispatcher task dispatcher
type Dispatcher struct {
	queueManager  interfaces.QueueManager
	workerManager interfaces.WorkerManager
	comfyClient   interfaces.ComfyUIClient
	logger        *logrus.Logger

	// running task mapping: taskID -> execution info
	runningTasks sync.Map

	// configuration
	config DispatcherConfig
}

// DispatcherConfig dispatcher configuration
type DispatcherConfig struct {
	PollInterval        time.Duration // polling interval
	StatusCheckInterval time.Duration // status check interval
	TaskTimeout         time.Duration // task timeout
	RetryDelay          time.Duration // retry delay
}

// TaskExecution task execution information
type TaskExecution struct {
	Task       *queue.Task
	Worker     *interfaces.Worker
	PromptID   string
	StartTime  time.Time
	LastCheck  time.Time
	CheckCount int
}

// NewDispatcher creates a task dispatcher
func NewDispatcher(
	queueManager interfaces.QueueManager,
	workerManager interfaces.WorkerManager,
	comfyClient interfaces.ComfyUIClient,
) *Dispatcher {
	logger := config.NewLogger()

	config := DispatcherConfig{
		PollInterval:        2 * time.Second,  // check new tasks every 2 seconds
		StatusCheckInterval: 5 * time.Second,  // check task status every 5 seconds
		TaskTimeout:         30 * time.Minute, // task timeout 30 minutes
		RetryDelay:          10 * time.Second, // retry delay 10 seconds
	}

	return &Dispatcher{
		queueManager:  queueManager,
		workerManager: workerManager,
		comfyClient:   comfyClient,
		logger:        logger,
		config:        config,
	}
}

// Start starts the task dispatcher
func (d *Dispatcher) Start(ctx context.Context) error {
	d.logger.Info("Starting task dispatcher")

	// start task dispatch goroutine
	go d.dispatchLoop(ctx)

	// start task status check goroutine
	go d.statusCheckLoop(ctx)

	// start timeout check goroutine
	go d.timeoutCheckLoop(ctx)

	<-ctx.Done()
	d.logger.Info("Task dispatcher stopped")
	return nil
}

// dispatchLoop task dispatch loop
func (d *Dispatcher) dispatchLoop(ctx context.Context) {
	ticker := time.NewTicker(d.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.dispatchTasks()
		}
	}
}

// dispatchTasks dispatches tasks
func (d *Dispatcher) dispatchTasks() {
	// get next pending task
	task, err := d.queueManager.GetNextTask()
	if err != nil {
		if err.Error() != "no pending tasks" {
			d.logger.WithError(err).Error("Failed to get next task")
		}
		return
	}

	// verify task is not null
	if task == nil {
		d.logger.Error("GetNextTask returned nil task")
		return
	}

	// get available worker
	worker, err := d.workerManager.GetAvailableWorker()
	if err != nil {
		d.logger.WithError(err).Debug("No available workers")
		return
	}

	// verify worker is not null
	if worker == nil {
		d.logger.Error("GetAvailableWorker returned nil worker")
		return
	}

	// dispatch task to worker
	if err := d.assignTaskToWorker(task, worker); err != nil {
		// safely build log fields
		logFields := logrus.Fields{}
		if task != nil {
			logFields["task_id"] = task.ID
		}
		if worker != nil {
			logFields["worker_id"] = worker.ID
		}

		d.logger.WithError(err).WithFields(logFields).Error("Failed to assign task to worker")

		// mark task as failed (only when task is not null)
		if task != nil {
			task.MarkFailed(err.Error())
			d.queueManager.UpdateTask(task)
		}
		return
	}

	d.logger.WithFields(logrus.Fields{
		"task_id":     task.ID,
		"worker_id":   worker.ID,
		"workflow_id": task.WorkflowID,
	}).Info("Task assigned to worker")
}

// assignTaskToWorker assigns task to worker
func (d *Dispatcher) assignTaskToWorker(task *queue.Task, worker *interfaces.Worker) error {
	// validate input parameters
	if task == nil {
		return fmt.Errorf("task is nil")
	}

	if worker == nil {
		return fmt.Errorf("worker is nil")
	}

	// verify worker status and endpoint
	if worker.Status != interfaces.WorkerStatusIdle {
		return fmt.Errorf("worker is not idle, current status: %s", worker.Status)
	}

	if worker.Endpoint == "" {
		return fmt.Errorf("worker endpoint is not available")
	}

	if !worker.HealthStatus {
		return fmt.Errorf("worker is not healthy")
	}

	// build complete endpoint address
	endpoint := d.buildWorkerEndpoint(worker)

	// mark worker as busy
	if err := d.workerManager.AssignTask(worker.ID, task.ID); err != nil {
		return fmt.Errorf("failed to mark worker as busy: %w", err)
	}

	// submit workflow to ComfyUI
	response, err := d.comfyClient.SubmitWorkflow(endpoint, task.Payload)
	if err != nil {
		// if submission fails, release worker
		d.workerManager.CompleteTask(worker.ID)
		return fmt.Errorf("failed to submit workflow to ComfyUI: %w", err)
	}

	// mark task as started
	task.MarkStarted(worker.ID)
	if err := d.queueManager.UpdateTask(task); err != nil {
		d.logger.WithError(err).Error("Failed to update task status")
	}

	// remove task from pending queue (task successfully assigned)
	if queueManager, ok := d.queueManager.(interface{ RemoveTaskFromQueue(string) error }); ok {
		if err := queueManager.RemoveTaskFromQueue(task.ID); err != nil {
			d.logger.WithError(err).WithField("task_id", task.ID).Error("Failed to remove task from queue")
		}
	}

	// record task execution information
	execution := &TaskExecution{
		Task:      task,
		Worker:    worker,
		PromptID:  response.PromptID,
		StartTime: time.Now(),
		LastCheck: time.Now(),
	}

	d.runningTasks.Store(task.ID, execution)

	return nil
}

// buildWorkerEndpoint builds complete worker endpoint address
func (d *Dispatcher) buildWorkerEndpoint(worker *interfaces.Worker) string {
	// if endpoint already contains protocol and port, use directly
	if worker.Endpoint != "" {
		// check if it's already a complete URL
		if strings.HasPrefix(worker.Endpoint, "http://") || strings.HasPrefix(worker.Endpoint, "https://") {
			return worker.Endpoint
		}
		// if it's just IP or domain name, add protocol
		return fmt.Sprintf("http://%s", worker.Endpoint)
	}

	// if no endpoint but has port, try to build
	if worker.Port > 0 {
		// for Docker Worker, usually use localhost or container IP
		// for Novita Worker, endpoint should be set during creation
		return fmt.Sprintf("localhost:%d", worker.Port)
	}

	// if neither exists, return empty string, caller will handle error
	return ""
}

// statusCheckLoop status check loop
func (d *Dispatcher) statusCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(d.config.StatusCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.checkRunningTasks()
		}
	}
}

// checkRunningTasks checks running tasks status
func (d *Dispatcher) checkRunningTasks() {
	d.runningTasks.Range(func(key, value interface{}) bool {
		taskID := key.(string)
		execution := value.(*TaskExecution)

		if err := d.checkTaskStatus(execution); err != nil {
			d.logger.WithError(err).WithField("task_id", taskID).Error("Failed to check task status")
		}

		return true
	})
}

// checkTaskStatus checks single task status
func (d *Dispatcher) checkTaskStatus(execution *TaskExecution) error {
	// Verify execution is not null
	if execution == nil {
		return fmt.Errorf("execution is nil")
	}

	if execution.Task == nil {
		return fmt.Errorf("execution.Task is nil")
	}

	if execution.Worker == nil {
		return fmt.Errorf("execution.Worker is nil")
	}

	execution.LastCheck = time.Now()
	execution.CheckCount++

	// Build worker access address
	endpoint := d.buildWorkerEndpoint(execution.Worker)
	if endpoint == "" {
		return fmt.Errorf("worker endpoint is not available for status check")
	}

	// Check task status via ComfyUI API
	status, err := d.comfyClient.GetStatus(endpoint, execution.PromptID)
	if err != nil {
		d.logger.WithError(err).WithFields(logrus.Fields{
			"task_id":   execution.Task.ID,
			"prompt_id": execution.PromptID,
			"worker_id": execution.Worker.ID,
			"endpoint":  endpoint,
		}).Warn("Failed to get task status from ComfyUI")
		return err
	}

	// Check if task is completed
	if d.isTaskCompleted(status) {
		return d.handleTaskCompletion(execution)
	}

	// Check if task is failed
	if d.isTaskFailed(status) {
		return d.handleTaskFailure(execution, "Task failed in ComfyUI")
	}

	// Task is still running
	d.logger.WithFields(logrus.Fields{
		"task_id":         execution.Task.ID,
		"prompt_id":       execution.PromptID,
		"worker_id":       execution.Worker.ID,
		"queue_remaining": status.ExecInfo.QueueRemaining,
		"check_count":     execution.CheckCount,
	}).Debug("Task still running")

	return nil
}

// isTaskCompleted checks if task is completed
func (d *Dispatcher) isTaskCompleted(status *interfaces.ComfyUIStatus) bool {
	// If queue has no remaining tasks, it means completed
	return status.ExecInfo.QueueRemaining == 0
}

// isTaskFailed checks if task is failed
func (d *Dispatcher) isTaskFailed(status *interfaces.ComfyUIStatus) bool {
	// Here we can check based on ComfyUI's specific status fields
	// For now, return false, can be adjusted based on actual needs later
	return false
}

// handleTaskCompletion handles task completion
func (d *Dispatcher) handleTaskCompletion(execution *TaskExecution) error {
	// Verify execution is not null
	if execution == nil {
		return fmt.Errorf("execution is nil")
	}

	if execution.Task == nil {
		return fmt.Errorf("execution.Task is nil")
	}

	if execution.Worker == nil {
		return fmt.Errorf("execution.Worker is nil")
	}

	// Build worker access address
	endpoint := d.buildWorkerEndpoint(execution.Worker)

	// Get task result
	var result *interfaces.ComfyUIResult
	var err error

	if endpoint != "" {
		result, err = d.comfyClient.GetResult(endpoint, execution.PromptID)
	} else {
		err = fmt.Errorf("worker endpoint is not available")
	}

	if err != nil {
		d.logger.WithError(err).WithFields(logrus.Fields{
			"task_id":   execution.Task.ID,
			"worker_id": execution.Worker.ID,
			"endpoint":  endpoint,
		}).Error("Failed to get task result")
		// Even if getting result fails, mark task as completed
		result = &interfaces.ComfyUIResult{
			Status:  "completed",
			Outputs: map[string]interface{}{},
		}
	}

	// Mark task as completed
	execution.Task.MarkCompleted(result.Outputs)
	if err := d.queueManager.UpdateTask(execution.Task); err != nil {
		d.logger.WithError(err).Error("Failed to update completed task")
	}

	// Release worker
	if err := d.workerManager.CompleteTask(execution.Worker.ID); err != nil {
		d.logger.WithError(err).Error("Failed to mark worker as available")
	}

	// Remove from running tasks list
	d.runningTasks.Delete(execution.Task.ID)

	d.logger.WithFields(logrus.Fields{
		"task_id":     execution.Task.ID,
		"worker_id":   execution.Worker.ID,
		"duration":    time.Since(execution.StartTime),
		"check_count": execution.CheckCount,
	}).Info("Task completed successfully")

	return nil
}

// handleTaskFailure handles task failure
func (d *Dispatcher) handleTaskFailure(execution *TaskExecution, reason string) error {
	// Verify execution is not null
	if execution == nil {
		return fmt.Errorf("execution is nil")
	}

	if execution.Task == nil {
		return fmt.Errorf("execution.Task is nil")
	}

	if execution.Worker == nil {
		return fmt.Errorf("execution.Worker is nil")
	}

	// Mark task as failed
	execution.Task.MarkFailed(reason)

	// Check if can be retried
	if execution.Task.CanRetry() {
		// Re-add to queue after delay
		go func() {
			time.Sleep(d.config.RetryDelay)
			execution.Task.Status = queue.TaskStatusPending
			if err := d.queueManager.UpdateTask(execution.Task); err != nil {
				d.logger.WithError(err).Error("Failed to requeue failed task")
			}
		}()

		d.logger.WithFields(logrus.Fields{
			"task_id":     execution.Task.ID,
			"retry_count": execution.Task.RetryCount,
			"max_retries": execution.Task.MaxRetries,
		}).Info("Task failed, will retry")
	} else {
		// Update task status to final failure
		if err := d.queueManager.UpdateTask(execution.Task); err != nil {
			d.logger.WithError(err).Error("Failed to update failed task")
		}

		d.logger.WithFields(logrus.Fields{
			"task_id": execution.Task.ID,
			"reason":  reason,
		}).Error("Task failed permanently")
	}

	// Release worker
	if err := d.workerManager.CompleteTask(execution.Worker.ID); err != nil {
		d.logger.WithError(err).Error("Failed to mark worker as available")
	}

	// Remove from running tasks list
	d.runningTasks.Delete(execution.Task.ID)

	return nil
}

// timeoutCheckLoop timeout check loop
func (d *Dispatcher) timeoutCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute) // check every minute
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.checkTimeouts()
		}
	}
}

// checkTimeouts checks task timeout
func (d *Dispatcher) checkTimeouts() {
	now := time.Now()

	d.runningTasks.Range(func(key, value interface{}) bool {
		taskID := key.(string)
		execution := value.(*TaskExecution)

		// Verify execution is not null
		if execution == nil {
			d.logger.WithField("task_id", taskID).Error("Found nil execution in running tasks")
			return true
		}

		if execution.Task == nil {
			d.logger.WithField("task_id", taskID).Error("Found nil task in execution")
			return true
		}

		if execution.Worker == nil {
			d.logger.WithField("task_id", taskID).Error("Found nil worker in execution")
			return true
		}

		// Check if task is timeout
		if now.Sub(execution.StartTime) > d.config.TaskTimeout {
			d.logger.WithFields(logrus.Fields{
				"task_id":  taskID,
				"duration": now.Sub(execution.StartTime),
				"timeout":  d.config.TaskTimeout,
			}).Warn("Task timeout detected")

			// Cancel task in ComfyUI
			if err := d.comfyClient.CancelTask(execution.Worker.Endpoint, execution.PromptID); err != nil {
				d.logger.WithError(err).Error("Failed to cancel timed out task")
			}

			// Handle task failure
			d.handleTaskFailure(execution, "Task timeout")
		}

		return true
	})
}

// GetRunningTasksCount gets running tasks count
func (d *Dispatcher) GetRunningTasksCount() int {
	count := 0
	d.runningTasks.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

// GetDispatcherMetrics gets dispatcher metrics
func (d *Dispatcher) GetDispatcherMetrics() DispatcherMetrics {
	runningCount := d.GetRunningTasksCount()

	return DispatcherMetrics{
		RunningTasks:    runningCount,
		TotalDispatched: d.getTotalDispatchedTasks(),
	}
}

// DispatcherMetrics dispatcher metrics
type DispatcherMetrics struct {
	RunningTasks    int `json:"running_tasks"`
	TotalDispatched int `json:"total_dispatched"`
}

// getTotalDispatchedTasks gets total dispatched tasks
func (d *Dispatcher) getTotalDispatchedTasks() int {
	// Here can add persistent counter
	return 0
}
