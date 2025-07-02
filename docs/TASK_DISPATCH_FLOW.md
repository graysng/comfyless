# ComfyUI Serverless Task Dispatch and Status Check Flow

## Overview

This document details the core logic of task dispatch to workers and task completion status checking in the ComfyUI Serverless system.

## Architecture Components

### 1. Core Components

- **Queue Manager**: Task queue manager, responsible for task lifecycle management
- **Worker Manager**: Worker manager, responsible for worker creation, status management and resource allocation
- **Task Dispatcher**: Task dispatcher, responsible for dispatching tasks to available workers
- **ComfyUI Client**: ComfyUI API client, responsible for communicating with ComfyUI instances

### 2. Data Flow

```
User Request → API Handler → Queue Manager → Task Dispatcher → Worker Manager → ComfyUI Instance
```

## Task Dispatch Flow

### 1. Task Submission

```go
// User submits task through API
func (h *Handler) SubmitTask(c *gin.Context) {
    // Parse workflow
    var workflow map[string]interface{}
    if err := c.ShouldBindJSON(&workflow); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }
    
    // Create task
    task := queue.NewTask("workflow-1", 1, workflow)
    
    // Add to queue
    if err := h.queueManager.AddTask(task); err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    
    c.JSON(200, gin.H{"task_id": task.ID})
}
```

### 2. Task Dispatch Loop

```go
// Task dispatcher main loop
func (d *Dispatcher) dispatchLoop(ctx context.Context) {
    ticker := time.NewTicker(d.config.PollInterval) // Check every 2 seconds
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            d.dispatchTasks() // Dispatch tasks
        }
    }
}
```

### 3. Task Dispatch Logic

```go
func (d *Dispatcher) dispatchTasks() {
    // 1. Get next pending task
    task, err := d.queueManager.GetNextTask()
    if err != nil {
        return // No pending tasks
    }

    // 2. Get available worker
    worker, err := d.workerManager.GetAvailableWorker()
    if err != nil {
        return // No available workers
    }

    // 3. Dispatch task to worker
    if err := d.assignTaskToWorker(task, worker); err != nil {
        // Dispatch failed, mark task as failed
        task.MarkFailed(err.Error())
        d.queueManager.UpdateTask(task)
        return
    }
}
```

### 4. Task Assignment Detailed Flow

```go
func (d *Dispatcher) assignTaskToWorker(task *queue.Task, worker *interfaces.Worker) error {
    // 1. Mark worker as busy
    if err := d.workerManager.AssignTask(worker.ID, task.ID); err != nil {
        return fmt.Errorf("failed to mark worker as busy: %w", err)
    }

    // 2. Submit workflow to ComfyUI
    response, err := d.comfyClient.SubmitWorkflow(worker.Endpoint, task.Payload)
    if err != nil {
        // Submission failed, release worker
        d.workerManager.CompleteTask(worker.ID)
        return fmt.Errorf("failed to submit workflow to ComfyUI: %w", err)
    }

    // 3. Mark task as started
    task.MarkStarted(worker.ID)
    d.queueManager.UpdateTask(task)

    // 4. Record task execution information
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
```

## Task Status Check Flow

### 1. Status Check Loop

```go
func (d *Dispatcher) statusCheckLoop(ctx context.Context) {
    ticker := time.NewTicker(d.config.StatusCheckInterval) // Check every 5 seconds
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            d.checkRunningTasks() // Check running tasks
        }
    }
}
```

### 2. Batch Status Check

```go
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
```

### 3. Single Task Status Check

```go
func (d *Dispatcher) checkTaskStatus(execution *TaskExecution) error {
    // 1. Check task status through ComfyUI API
    status, err := d.comfyClient.GetStatus(execution.Worker.Endpoint, execution.PromptID)
    if err != nil {
        return err
    }

    // 2. Check if task is completed
    if d.isTaskCompleted(status) {
        return d.handleTaskCompletion(execution)
    }

    // 3. Check if task failed
    if d.isTaskFailed(status) {
        return d.handleTaskFailure(execution, "Task failed in ComfyUI")
    }

    // 4. Task still running, record status
    d.logger.WithFields(logrus.Fields{
        "task_id":           execution.Task.ID,
        "queue_remaining":   status.ExecInfo.QueueRemaining,
        "check_count":       execution.CheckCount,
    }).Debug("Task still running")

    return nil
}
```

### 4. Task Completion Detection

```go
func (d *Dispatcher) isTaskCompleted(status *interfaces.ComfyUIStatus) bool {
    // Queue remaining count of 0 in ComfyUI indicates task completion
    return status.ExecInfo.QueueRemaining == 0
}
```

## Task Completion Handling

### 1. Task Completion Flow

```go
func (d *Dispatcher) handleTaskCompletion(execution *TaskExecution) error {
    // 1. Get task result
    result, err := d.comfyClient.GetResult(execution.Worker.Endpoint, execution.PromptID)
    if err != nil {
        // Failed to get result, still mark as completed
        result = &interfaces.ComfyUIResult{
            Status:  "completed",
            Outputs: map[string]interface{}{},
        }
    }

    // 2. Mark task as completed
    execution.Task.MarkCompleted(result.Outputs)
    d.queueManager.UpdateTask(execution.Task)

    // 3. Release worker
    d.workerManager.CompleteTask(execution.Worker.ID)

    // 4. Clean up runtime information
    d.runningTasks.Delete(execution.Task.ID)

    return nil
}
```

### 2. Task Failure Handling

```go
func (d *Dispatcher) handleTaskFailure(execution *TaskExecution, reason string) error {
    // 1. Mark task as failed
    execution.Task.MarkFailed(reason)
    
    // 2. Check if retry is possible
    if execution.Task.CanRetry() {
        // Re-add to queue after delay
        go func() {
            time.Sleep(d.config.RetryDelay)
            execution.Task.Status = queue.TaskStatusPending
            d.queueManager.UpdateTask(execution.Task)
        }()
    } else {
        // Permanent failure
        d.queueManager.UpdateTask(execution.Task)
    }

    // 3. Release worker
    d.workerManager.CompleteTask(execution.Worker.ID)

    // 4. Clean up runtime information
    d.runningTasks.Delete(execution.Task.ID)

    return nil
}
```

## Timeout Handling

### 1. Timeout Check Loop

```go
func (d *Dispatcher) timeoutCheckLoop(ctx context.Context) {
    ticker := time.NewTicker(time.Minute) // Check every minute
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
```

### 2. Timeout Handling Logic

```go
func (d *Dispatcher) checkTimeouts() {
    now := time.Now()
    
    d.runningTasks.Range(func(key, value interface{}) bool {
        execution := value.(*TaskExecution)
        
        // Check if task is timed out (default 30 minutes)
        if now.Sub(execution.StartTime) > d.config.TaskTimeout {
            // Cancel task in ComfyUI
            d.comfyClient.CancelTask(execution.Worker.Endpoint, execution.PromptID)
            
            // Handle task failure
            d.handleTaskFailure(execution, "Task timeout")
        }
        
        return true
    })
}
```

## ComfyUI API Interaction

### 1. Workflow Submission

```go
func (c *Client) SubmitWorkflow(endpoint string, workflow map[string]interface{}) (*interfaces.ComfyUIResponse, error) {
    url := fmt.Sprintf("http://%s/prompt", endpoint)
    
    requestBody := map[string]interface{}{
        "prompt": workflow,
    }
    
    jsonData, _ := json.Marshal(requestBody)
    resp, err := c.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
    
    // Parse response to get prompt_id
    var response interfaces.ComfyUIResponse
    json.NewDecoder(resp.Body).Decode(&response)
    
    return &response, nil
}
```

### 2. Status Query

```go
func (c *Client) GetStatus(endpoint string, promptID string) (*interfaces.ComfyUIStatus, error) {
    url := fmt.Sprintf("http://%s/prompt/%s", endpoint, promptID)
    
    resp, err := c.httpClient.Get(url)
    
    var status interfaces.ComfyUIStatus
    json.NewDecoder(resp.Body).Decode(&status)
    
    return &status, nil
}
```

### 3. Result Retrieval

```go
func (c *Client) GetResult(endpoint string, promptID string) (*interfaces.ComfyUIResult, error) {
    url := fmt.Sprintf("http://%s/history/%s", endpoint, promptID)
    
    resp, err := c.httpClient.Get(url)
    
    var historyResponse map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&historyResponse)
    
    // Extract result from history record
    if promptData, exists := historyResponse[promptID]; exists {
        // Build result object
        result := &interfaces.ComfyUIResult{
            Status:  "completed",
            Outputs: extractOutputs(promptData),
        }
        return result, nil
    }
    
    return nil, fmt.Errorf("result not found")
}
```

## Configuration Parameters

```go
type DispatcherConfig struct {
    PollInterval        time.Duration // Poll interval (default 2 seconds)
    StatusCheckInterval time.Duration // Status check interval (default 5 seconds)
    TaskTimeout         time.Duration // Task timeout (default 30 minutes)
    RetryDelay          time.Duration // Retry delay (default 10 seconds)
}
```

## Monitoring Metrics

```go
type DispatcherMetrics struct {
    RunningTasks    int `json:"running_tasks"`    // Number of running tasks
    TotalDispatched int `json:"total_dispatched"` // Total dispatched tasks
}
```

## Error Handling

1. **Worker Unavailable**: Wait for worker to become available or trigger auto scaling
2. **ComfyUI Connection Failure**: Retry mechanism, Worker health check
3. **Task Timeout**: Auto cancel and retry
4. **Result Retrieval Failure**: Still mark task as completed, avoid deadlock

## Performance Optimization

1. **Concurrent Processing**: Multiple tasks can be dispatched to different workers
2. **Batch Checking**: Status check uses batch mode, reducing API calls
3. **Caching Mechanism**: Worker status caching, reducing repeated queries
4. **Asynchronous Processing**: All I/O operations are asynchronous, not blocking main flow

This system ensures that tasks can be reliably dispatched to workers and accurately track execution status, achieving high-availability ComfyUI Serverless service.

## Task Lifecycle

### 1. Task Submission
- Client submits task via REST API
- Task is assigned unique ID and priority
- Task is added to Redis queue with status `pending`
- Task is cached in memory for fast access

### 2. Task Dispatch
- Dispatcher polls for pending tasks
- When idle worker is available, task is assigned
- Task status changes to `running`
- Worker begins processing

### 3. Task Completion
- Worker completes task processing
- Task result is retrieved from worker
- **Task is immediately deleted from both Redis and memory**
- Client receives final result through callback or polling

## Optimized Storage Strategy

### Key Principles
- **Immediate Deletion**: Completed tasks (success/failure/cancelled) are deleted immediately
- **Memory Efficiency**: No long-term storage of completed task data
- **Cost Optimization**: Reduces Redis memory usage and costs
- **Simplicity**: Eliminates complex cleanup logic

### Storage Behavior
```
Task States:
├── pending   → Stored in Redis + Memory
├── running   → Stored in Redis + Memory  
├── completed → DELETED immediately
├── failed    → DELETED immediately
└── cancelled → DELETED immediately
```

### Benefits
1. **Reduced Memory Usage**: No accumulation of completed task data
2. **Lower Costs**: Minimal Redis storage requirements
3. **Simplified Architecture**: No periodic cleanup processes
4. **Better Performance**: Faster queue operations

### Trade-offs
- **No Task History**: Completed tasks cannot be queried after completion
- **Immediate Retrieval Required**: Clients must capture results immediately
- **No Audit Trail**: No persistent record of completed tasks

### Implementation Details
- Task deletion occurs in `UpdateTask()` method
- Callbacks are triggered before deletion to ensure result delivery
- Metrics only show active tasks (pending/running)
- API endpoints return "Task not found" for completed tasks

## Worker Management

### Worker States
- Workers are persisted in Redis for recovery after service restarts
- Worker state synchronization helps maintain cluster consistency
- Health checks update worker availability without affecting task lifecycle

## Error Handling

### Task Failures
- Failed tasks are immediately deleted after callback notification
- Retry logic operates before final failure status
- No persistent storage of failure history

### System Recovery
- On restart, only pending/running tasks are recovered
- Running tasks are reset to pending for re-processing
- Completed tasks from previous sessions are not recovered

## Monitoring

### Available Metrics
- `total_tasks`: Currently active tasks only
- `pending_tasks`: Tasks waiting for workers
- `running_tasks`: Tasks being processed
- Note: `completed_tasks`, `failed_tasks` metrics are always 0

### Logging
- All task lifecycle events are logged
- Task completion/deletion events are logged for observability
- Worker assignment and release events are tracked

This system ensures that tasks can be reliably dispatched to workers and accurately track execution status, achieving high-availability ComfyUI Serverless service. 