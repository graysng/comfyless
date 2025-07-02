# ComfyUI Serverless Complete Workflow

## System Architecture Overview

The ComfyUI Serverless system consists of the following core components:

- **API Handler**: Entry layer for handling HTTP requests
- **Queue Manager**: Manages task queues and status
- **Task Dispatcher**: Responsible for task dispatch and status monitoring
- **Worker Manager**: Manages worker lifecycle (supports Docker and Novita AI)
- **ComfyUI Client**: Communication client with ComfyUI instances

## Complete Request Flow

### Phase 1: Task Submission

#### 1.1 User Initiates Request
User submits ComfyUI workflow through HTTP API:

```bash
curl -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{
  "workflow_id": "text_to_image_v1",
  "priority": 5,
  "payload": {
    "3": {
      "class_type": "KSampler",
      "inputs": {
        "cfg": 8,
        "denoise": 1,
        "latent_image": [
          "5",
          0
        ],
        "model": [
          "4",
          0
        ],
        "negative": [
          "7",
          0
        ],
        "positive": [
          "6",
          0
        ],
        "sampler_name": "euler",
        "scheduler": "normal",
        "seed": 46588125086418,
        "steps": 20
      }
    },
    "4": {
      "class_type": "CheckpointLoaderSimple",
      "inputs": {
        "ckpt_name": "flux1-dev-fp8.safetensors"
      }
    },
    "5": {
      "class_type": "EmptyLatentImage",
      "inputs": {
        "batch_size": 1,
        "height": 512,
        "width": 512
      }
    },
    "6": {
      "class_type": "CLIPTextEncode",
      "inputs": {
        "clip": [
          "4",
          1
        ],
        "text": "beautiful scenery nature glass bottle landscape, , purple galaxy bottle,"
      }
    },
    "7": {
      "class_type": "CLIPTextEncode",
      "inputs": {
        "clip": [
          "4",
          1
        ],
        "text": "text, watermark"
      }
    },
    "8": {
      "class_type": "VAEDecode",
      "inputs": {
        "samples": [
          "3",
          0
        ],
        "vae": [
          "4",
          2
        ]
      }
    },
    "9": {
      "class_type": "SaveImage",
      "inputs": {
        "filename_prefix": "ComfyUI",
        "images": [
          "8",
          0
        ]
      }
    }
  }
}'
```

#### 1.2 API Handler Processes Request
```go
// internal/api/handler.go
func (h *Handler) submitTask(c *gin.Context) {
    var req SubmitTaskRequest
    
    // 1. Parse request body
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }
    
    // 2. Create task object
    task := queue.NewTask(req.WorkflowID, req.Priority, req.Payload)
    
    // 3. Add to queue
    if err := h.queueManager.AddTask(task); err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    
    // 4. Return task ID
    c.JSON(201, TaskResponse{
        ID:         task.ID,
        WorkflowID: task.WorkflowID,
        Status:     "pending",
        CreatedAt:  task.CreatedAt,
    })
}
```

**Current Status**: 
- Task status: `pending`
- User receives: Task ID and creation time
- System status: Task added to priority queue

### Phase 2: Task Dispatch

#### 2.1 Task Dispatcher Polling Check
Task Dispatcher checks for pending tasks every 2 seconds:

```go
func (d *Dispatcher) dispatchLoop(ctx context.Context) {
    ticker := time.NewTicker(2 * time.Second)
    
    for {
        select {
        case <-ticker.C:
            d.dispatchTasks()
        }
    }
}
```

#### 2.2 Assign Task to Worker
```go
func (d *Dispatcher) assignTaskToWorker(task *queue.Task, worker *interfaces.Worker) error {
    // 1. Mark worker as busy
    d.workerManager.AssignTask(worker.ID, task.ID)
    
    // 2. Submit workflow to ComfyUI
    response, err := d.comfyClient.SubmitWorkflow(worker.Endpoint, task.Payload)
    
    // 3. Update task status
    task.MarkStarted(worker.ID)
    d.queueManager.UpdateTask(task)
    
    // 4. Record execution information
    execution := &TaskExecution{
        Task:      task,
        Worker:    worker,
        PromptID:  response.PromptID,
        StartTime: time.Now(),
    }
    d.runningTasks.Store(task.ID, execution)
    
    return nil
}
```

### Phase 3: Status Monitoring

#### 3.1 Periodic Status Check
Task Dispatcher checks status of all running tasks every 5 seconds:

```go
func (d *Dispatcher) checkTaskStatus(execution *TaskExecution) error {
    // 1. Call ComfyUI API to query status
    status, err := d.comfyClient.GetStatus(execution.Worker.Endpoint, execution.PromptID)
    
    // 2. Check if task is completed
    if status.ExecInfo.QueueRemaining == 0 {
        return d.handleTaskCompletion(execution)
    }
    
    return nil
}
```

### Phase 4: Task Completion

#### 4.1 Handle Task Completion
```go
func (d *Dispatcher) handleTaskCompletion(execution *TaskExecution) error {
    // 1. Get task result
    result, err := d.comfyClient.GetResult(execution.Worker.Endpoint, execution.PromptID)
    
    // 2. Update task status
    execution.Task.MarkCompleted(result.Outputs)
    d.queueManager.UpdateTask(execution.Task)
    
    // 3. Release worker
    d.workerManager.CompleteTask(execution.Worker.ID)
    
    // 4. Clean up execution information
    d.runningTasks.Delete(execution.Task.ID)
    
    return nil
}
```

## Component Interaction Flow Diagram

```
User    API     Queue   Dispatcher  Worker    ComfyUI
 |       |        |         |        |         |
 |------>|        |         |        |         |  1. POST /tasks
 |       |------->|         |        |         |  2. AddTask()
 |<------|        |         |        |         |  3. Return task ID
 |       |        |<--------|        |         |  4. GetNextTask()
 |       |        |-------->|        |         |  5. Return task
 |       |        |         |------->|         |  6. GetAvailableWorker()
 |       |        |         |<-------|         |  7. Return worker
 |       |        |         |        |-------->|  8. SubmitWorkflow()
 |       |        |         |        |<--------|  9. Return prompt_id
 |       |        |<--------|        |         | 10. UpdateTask(running)
 |       |        |         |        |<------->| 11. Periodic status check
 |       |        |         |        |<--------| 12. Task completed
 |       |        |         |        |-------->| 13. GetResult()
 |       |        |         |        |<--------| 14. Return result
 |       |        |<--------|        |         | 15. UpdateTask(completed)
 |------>|        |         |        |         | 16. GET /tasks/{id}
 |       |------->|         |        |         | 17. GetTask()
 |<------|        |         |        |         | 18. Return complete result
```

## Core Data Structures

### Task Object
```go
type Task struct {
    ID          string                 `json:"id"`
    WorkflowID  string                 `json:"workflow_id"`
    Priority    int                    `json:"priority"`
    Status      TaskStatus             `json:"status"`
    Payload     map[string]interface{} `json:"payload"`
    WorkerID    string                 `json:"worker_id"`
    CreatedAt   time.Time              `json:"created_at"`
    StartedAt   *time.Time             `json:"started_at"`
    CompletedAt *time.Time             `json:"completed_at"`
    Result      map[string]interface{} `json:"result"`
    Error       string                 `json:"error"`
    RetryCount  int                    `json:"retry_count"`
    MaxRetries  int                    `json:"max_retries"`
}
```

### Worker Object
```go
type Worker struct {
    ID              string       `json:"id"`
    PodName         string       `json:"pod_name"`
    Status          WorkerStatus `json:"status"`
    Endpoint        string       `json:"endpoint"`
    Port            int          `json:"port"`
    CurrentTaskID   string       `json:"current_task_id"`
    CreatedAt       time.Time    `json:"created_at"`
    LastActiveAt    time.Time    `json:"last_active_at"`
    HealthStatus    bool         `json:"health_status"`
}
```

### Execution Information (TaskExecution)
```go
type TaskExecution struct {
    Task      *queue.Task
    Worker    *interfaces.Worker
    PromptID  string    // Task ID returned by ComfyUI
    StartTime time.Time
    LastCheck time.Time
    CheckCount int
}
```

## Error Handling Mechanism

### 1. Task Failure Retry
```go
func (d *Dispatcher) handleTaskFailure(execution *TaskExecution, reason string) error {
    execution.Task.MarkFailed(reason)
    
    if execution.Task.CanRetry() {
        // Retry after 10 seconds
        go func() {
            time.Sleep(10 * time.Second)
            execution.Task.Status = queue.TaskStatusPending
            d.queueManager.UpdateTask(execution.Task)
        }()
    }
    
    d.workerManager.CompleteTask(execution.Worker.ID)
    d.runningTasks.Delete(execution.Task.ID)
    return nil
}
```

### 2. Task Timeout Handling
```go
func (d *Dispatcher) checkTimeouts() {
    d.runningTasks.Range(func(key, value interface{}) bool {
        execution := value.(*TaskExecution)
        
        if time.Since(execution.StartTime) > 30*time.Minute {
            // Cancel ComfyUI task
            d.comfyClient.CancelTask(execution.Worker.Endpoint, execution.PromptID)
            d.handleTaskFailure(execution, "Task timeout")
        }
        
        return true
    })
}
```

## Monitoring and Metrics

### Queue Metrics
```go
type QueueMetrics struct {
    PendingTasks   int `json:"pending_tasks"`
    RunningTasks   int `json:"running_tasks"`
    CompletedTasks int `json:"completed_tasks"`
    FailedTasks    int `json:"failed_tasks"`
}
```

### Worker Metrics
```go
type WorkerMetrics struct {
    TotalWorkers       int `json:"total_workers"`
    IdleWorkers        int `json:"idle_workers"`
    RunningWorkers     int `json:"running_workers"`
    StartingWorkers    int `json:"starting_workers"`
    FailedWorkers      int `json:"failed_workers"`
}
```

## Configuration Parameters

```go
type DispatcherConfig struct {
    PollInterval        time.Duration // Polling interval (2 seconds)
    StatusCheckInterval time.Duration // Status check interval (5 seconds)
    TaskTimeout         time.Duration // Task timeout (30 minutes)
    RetryDelay          time.Duration // Retry delay (10 seconds)
}
```

This complete workflow ensures a reliable, monitorable, and efficient process from user task submission to result retrieval. 