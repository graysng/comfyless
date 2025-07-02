package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"comfyless/internal/config"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// Manager queue manager
type Manager struct {
	redis     *redis.Client
	tasks     sync.Map // in-memory task cache
	callbacks []TaskCallback
	mu        sync.RWMutex
	stopCh    chan struct{}
	logger    *logrus.Logger
}

// NewManager creates a queue manager
func NewManager(cfg config.RedisConfig) *Manager {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	logger := config.NewLogger()

	return &Manager{
		redis:     rdb,
		callbacks: make([]TaskCallback, 0),
		stopCh:    make(chan struct{}),
		logger:    logger,
	}
}

// Start starts the queue manager
func (m *Manager) Start(ctx context.Context) error {
	m.logger.Info("Starting queue manager")

	// start task processing goroutines
	go m.processTaskUpdates(ctx)

	// recover unfinished tasks from Redis
	if err := m.loadTasksFromRedis(ctx); err != nil {
		m.logger.WithError(err).Error("Failed to load tasks from Redis")
	}

	<-ctx.Done()
	m.logger.Info("Queue manager stopped")
	return nil
}

// AddTask adds a task to the queue
func (m *Manager) AddTask(task *Task) error {
	// save to Redis
	taskJSON, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	ctx := context.Background()
	pipe := m.redis.TxPipeline()

	// save task details
	pipe.HSet(ctx, "tasks", task.ID, taskJSON)

	// add to pending queue (using priority queue)
	pipe.ZAdd(ctx, "pending_tasks", redis.Z{
		Score:  float64(-task.Priority), // negative number makes high priority come first
		Member: task.ID,
	})

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to add task to Redis: %w", err)
	}

	// save to memory cache
	m.tasks.Store(task.ID, task)

	m.logger.WithFields(logrus.Fields{
		"task_id":     task.ID,
		"workflow_id": task.WorkflowID,
		"priority":    task.Priority,
	}).Info("Task added to queue")

	return nil
}

// GetNextTask gets the next pending task (without removing from queue)
func (m *Manager) GetNextTask() (*Task, error) {
	ctx := context.Background()

	// use ZRange to view highest priority task without removing
	result := m.redis.ZRange(ctx, "pending_tasks", 0, 0)
	if result.Err() != nil {
		if result.Err() == redis.Nil {
			return nil, fmt.Errorf("no pending tasks")
		}
		return nil, fmt.Errorf("failed to get next task: %w", result.Err())
	}

	if len(result.Val()) == 0 {
		return nil, fmt.Errorf("no pending tasks")
	}

	taskID := result.Val()[0]

	// get task details from Redis
	taskJSON := m.redis.HGet(ctx, "tasks", taskID)
	if taskJSON.Err() != nil {
		return nil, fmt.Errorf("failed to get task details: %w", taskJSON.Err())
	}

	var task Task
	if err := json.Unmarshal([]byte(taskJSON.Val()), &task); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task: %w", err)
	}

	// update memory cache
	m.tasks.Store(task.ID, &task)

	return &task, nil
}

// RemoveTaskFromQueue removes task from pending queue (called when task is successfully assigned to worker)
func (m *Manager) RemoveTaskFromQueue(taskID string) error {
	ctx := context.Background()

	// remove task from pending_tasks queue
	result := m.redis.ZRem(ctx, "pending_tasks", taskID)
	if result.Err() != nil {
		return fmt.Errorf("failed to remove task from queue: %w", result.Err())
	}

	if result.Val() == 0 {
		// task not in queue, may have been processed by other dispatcher
		m.logger.WithField("task_id", taskID).Debug("Task was not in pending queue")
	}

	return nil
}

// UpdateTask updates task status
func (m *Manager) UpdateTask(task *Task) error {
	ctx := context.Background()

	// If task is completed, failed, or cancelled, delete it instead of updating
	if task.Status == TaskStatusCompleted || task.Status == TaskStatusFailed || task.Status == TaskStatusCancelled {
		// trigger callbacks first
		m.mu.RLock()
		callbacks := make([]TaskCallback, len(m.callbacks))
		copy(callbacks, m.callbacks)
		m.mu.RUnlock()

		for _, callback := range callbacks {
			go callback(task)
		}

		// Remove from Redis
		if err := m.redis.HDel(ctx, "tasks", task.ID).Err(); err != nil {
			m.logger.WithError(err).WithField("task_id", task.ID).Error("Failed to delete completed task from Redis")
		}

		// Remove from memory cache
		m.tasks.Delete(task.ID)

		m.logger.WithFields(logrus.Fields{
			"task_id": task.ID,
			"status":  task.Status,
		}).Info("Task completed and deleted")

		return nil
	}

	// For pending/running tasks, update as usual
	taskJSON, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	if err := m.redis.HSet(ctx, "tasks", task.ID, taskJSON).Err(); err != nil {
		return fmt.Errorf("failed to update task in Redis: %w", err)
	}

	// update memory cache
	m.tasks.Store(task.ID, task)

	// trigger callbacks
	m.mu.RLock()
	callbacks := make([]TaskCallback, len(m.callbacks))
	copy(callbacks, m.callbacks)
	m.mu.RUnlock()

	for _, callback := range callbacks {
		go callback(task)
	}

	m.logger.WithFields(logrus.Fields{
		"task_id": task.ID,
		"status":  task.Status,
	}).Info("Task updated")

	return nil
}

// GetTask gets task by ID
func (m *Manager) GetTask(taskID string) (*Task, error) {
	// first search from memory cache
	if value, ok := m.tasks.Load(taskID); ok {
		return value.(*Task), nil
	}

	// search from Redis
	ctx := context.Background()
	taskJSON := m.redis.HGet(ctx, "tasks", taskID)
	if taskJSON.Err() != nil {
		if taskJSON.Err() == redis.Nil {
			return nil, fmt.Errorf("task not found: %s", taskID)
		}
		return nil, fmt.Errorf("failed to get task from Redis: %w", taskJSON.Err())
	}

	var task Task
	if err := json.Unmarshal([]byte(taskJSON.Val()), &task); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task: %w", err)
	}

	// update memory cache
	m.tasks.Store(taskID, &task)

	return &task, nil
}

// GetTasksByStatus gets task list by status
func (m *Manager) GetTasksByStatus(status TaskStatus) ([]*Task, error) {
	var tasks []*Task

	// Reload all tasks from Redis to ensure state synchronization
	ctx := context.Background()
	allTasks := m.redis.HGetAll(ctx, "tasks")
	if allTasks.Err() != nil {
		return nil, fmt.Errorf("failed to get tasks from Redis: %w", allTasks.Err())
	}

	for _, taskJSON := range allTasks.Val() {
		var task Task
		if err := json.Unmarshal([]byte(taskJSON), &task); err != nil {
			m.logger.WithError(err).Warn("Failed to unmarshal task")
			continue
		}

		// Update memory cache
		m.tasks.Store(task.ID, &task)

		// If status matches, add to result
		if task.Status == status {
			tasks = append(tasks, &task)
		}
	}

	// Sort by creation time
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
	})

	return tasks, nil
}

// GetMetrics gets queue metrics
func (m *Manager) GetMetrics() (*QueueMetrics, error) {
	metrics := &QueueMetrics{}

	m.tasks.Range(func(key, value interface{}) bool {
		task := value.(*Task)
		metrics.TotalTasks++

		switch task.Status {
		case TaskStatusPending:
			metrics.PendingTasks++
		case TaskStatusRunning:
			metrics.RunningTasks++
			// Note: Completed, Failed, and Cancelled tasks are immediately deleted
			// so they won't appear in these metrics
		}

		return true
	})

	return metrics, nil
}

// AddCallback adds task status change callback
func (m *Manager) AddCallback(callback TaskCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacks = append(m.callbacks, callback)
}

// CancelTask cancels task
func (m *Manager) CancelTask(taskID string) error {
	// Get task
	task, err := m.GetTask(taskID)
	if err != nil {
		return err
	}

	// Check if task can be cancelled
	if task.Status != TaskStatusPending && task.Status != TaskStatusRunning {
		return fmt.Errorf("task cannot be cancelled, current status: %s", task.Status)
	}

	// Mark task as cancelled
	task.MarkCancelled()

	// If task is in pending queue, remove it from queue
	if task.Status == TaskStatusPending {
		ctx := context.Background()
		if err := m.redis.ZRem(ctx, "pending_tasks", taskID).Err(); err != nil {
			m.logger.WithError(err).WithField("task_id", taskID).Warn("Failed to remove task from pending queue")
		}
	}

	// Update task status
	return m.UpdateTask(task)
}

// processTaskUpdates processes task status updates
func (m *Manager) processTaskUpdates(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.handleRetryableTasks()
		}
	}
}

// handleRetryableTasks handles retryable failed tasks
func (m *Manager) handleRetryableTasks() {
	m.tasks.Range(func(key, value interface{}) bool {
		task := value.(*Task)
		if task.CanRetry() {
			// Reset task status and re-add to queue
			task.Status = TaskStatusPending
			task.WorkerID = ""
			task.StartedAt = nil
			task.CompletedAt = nil
			task.Error = ""

			ctx := context.Background()
			m.redis.ZAdd(ctx, "pending_tasks", redis.Z{
				Score:  float64(-task.Priority),
				Member: task.ID,
			})

			m.UpdateTask(task)

			m.logger.WithFields(logrus.Fields{
				"task_id":     task.ID,
				"retry_count": task.RetryCount,
			}).Info("Task retried")
		}
		return true
	})
}

// loadTasksFromRedis loads all tasks from Redis to memory and rebuilds queue
func (m *Manager) loadTasksFromRedis(ctx context.Context) error {
	tasks := m.redis.HGetAll(ctx, "tasks")
	if tasks.Err() != nil {
		return fmt.Errorf("failed to load tasks from Redis: %w", tasks.Err())
	}

	count := 0
	pendingCount := 0
	runningRecoveredCount := 0

	// Use pipeline to bulk rebuild pending_tasks queue
	pipe := m.redis.TxPipeline()

	for _, taskJSON := range tasks.Val() {
		var task Task
		if err := json.Unmarshal([]byte(taskJSON), &task); err != nil {
			m.logger.WithError(err).Warn("Failed to unmarshal task")
			continue
		}

		// Process different status tasks
		switch task.Status {
		case TaskStatusPending:
			// Re-add to pending_tasks queue
			pipe.ZAdd(ctx, "pending_tasks", redis.Z{
				Score:  float64(-task.Priority), // Negative number makes high priority come first
				Member: task.ID,
			})
			pendingCount++

		case TaskStatusRunning:
			// Running tasks need to be re-checked because they may have been completed during service restart
			// Reset to pending status and re-add to queue, let scheduler re-process
			task.Status = TaskStatusPending
			task.WorkerID = ""
			task.StartedAt = nil

			pipe.ZAdd(ctx, "pending_tasks", redis.Z{
				Score:  float64(-task.Priority),
				Member: task.ID,
			})

			// Update task status in Redis
			taskJSON, err := json.Marshal(&task)
			if err == nil {
				pipe.HSet(ctx, "tasks", task.ID, taskJSON)
			}

			runningRecoveredCount++

			m.logger.WithFields(logrus.Fields{
				"task_id":     task.ID,
				"workflow_id": task.WorkflowID,
			}).Info("Recovered running task, reset to pending for status check")
		}

		// Load to memory cache (after status modification)
		m.tasks.Store(task.ID, &task)
		count++
	}

	// Execute pipeline operation
	if pendingCount > 0 || runningRecoveredCount > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			m.logger.WithError(err).Error("Failed to rebuild pending_tasks queue")
			return fmt.Errorf("failed to rebuild pending_tasks queue: %w", err)
		}
	}

	m.logger.WithFields(logrus.Fields{
		"total_tasks":             count,
		"pending_tasks":           pendingCount,
		"running_recovered_tasks": runningRecoveredCount,
	}).Info("Loaded tasks from Redis and rebuilt pending queue")

	return nil
}
