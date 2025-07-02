package worker

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"comfyless/internal/config"
	"comfyless/internal/interfaces"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// DockerManager worker manager based on Docker
type DockerManager struct {
	workerConfig  config.WorkerConfig
	dockerConfig  config.DockerConfig
	workers       sync.Map
	persistence   *WorkerPersistence
	comfyClient   interfaces.ComfyUIClient
	logger        *logrus.Logger
	lastScaleTime time.Time
}

// NewDockerManager creates Docker worker manager
func NewDockerManager(workerCfg config.WorkerConfig, dockerCfg config.DockerConfig, redisCfg config.RedisConfig, comfyClient interfaces.ComfyUIClient) interfaces.WorkerManager {
	logger := config.NewLogger()

	return &DockerManager{
		workerConfig:  workerCfg,
		dockerConfig:  dockerCfg,
		persistence:   NewWorkerPersistence(redisCfg),
		comfyClient:   comfyClient,
		logger:        logger,
		lastScaleTime: time.Now(),
	}
}

// Start starts worker manager
func (m *DockerManager) Start(ctx context.Context) error {
	m.logger.Info("Starting Docker Worker Manager")

	// First recover worker information from Redis
	if err := m.loadWorkersFromRedis(); err != nil {
		m.logger.WithError(err).Error("Failed to load workers from Redis")
	}

	// Initialize: discover existing workers (merge with Redis information)
	if err := m.discoverExistingWorkers(); err != nil {
		m.logger.WithError(err).Error("Failed to discover existing workers")
	}

	// Start management goroutines
	go m.scaleWorkers(ctx)
	go m.healthCheck(ctx)
	go m.cleanupWorkers(ctx)
	go m.persistenceCleanup(ctx) // New: Redis cleanup goroutine

	// Ensure minimum number of workers
	if err := m.ensureMinWorkers(); err != nil {
		m.logger.WithError(err).Error("Failed to ensure minimum workers")
	}

	<-ctx.Done()
	m.logger.Info("Docker Worker Manager stopped")
	return nil
}

// GetAvailableWorker gets available worker
func (m *DockerManager) GetAvailableWorker() (*interfaces.Worker, error) {
	var availableWorker *interfaces.Worker

	m.workers.Range(func(key, value interface{}) bool {
		worker := value.(*interfaces.Worker)
		if worker.IsIdle() && worker.HealthStatus {
			availableWorker = worker
			return false
		}
		return true
	})

	if availableWorker == nil {
		return nil, fmt.Errorf("no available worker found")
	}

	return availableWorker, nil
}

// GetWorkerByID gets worker by ID
func (m *DockerManager) GetWorkerByID(workerID string) (*interfaces.Worker, error) {
	if value, ok := m.workers.Load(workerID); ok {
		return value.(*interfaces.Worker), nil
	}
	return nil, fmt.Errorf("worker not found: %s", workerID)
}

// GetWorkerMetrics gets worker metrics
func (m *DockerManager) GetWorkerMetrics() *interfaces.WorkerMetrics {
	metrics := &interfaces.WorkerMetrics{}

	m.workers.Range(func(key, value interface{}) bool {
		worker := value.(*interfaces.Worker)
		metrics.TotalWorkers++

		switch worker.Status {
		case interfaces.WorkerStatusPending:
			// Pending workers are counted in total but not in specific categories
		case interfaces.WorkerStatusStarting:
			metrics.StartingWorkers++
		case interfaces.WorkerStatusRunning, interfaces.WorkerStatusBusy:
			metrics.RunningWorkers++
		case interfaces.WorkerStatusIdle:
			metrics.IdleWorkers++
		case interfaces.WorkerStatusError:
			metrics.FailedWorkers++
		}

		return true
	})

	return metrics
}

// CreateWorker creates new worker
func (m *DockerManager) CreateWorker() (*interfaces.Worker, error) {
	workerID := uuid.New().String()
	containerName := fmt.Sprintf("comfyui-worker-%s", workerID[:8])

	// Allocate port
	port, err := m.allocatePort()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate port: %w", err)
	}

	// Create worker object
	worker := &interfaces.Worker{
		ID:           workerID,
		Provider:     interfaces.DockerProvider,
		PodName:      containerName, // Used as container name in Docker
		Status:       interfaces.WorkerStatusPending,
		Port:         port,
		CreatedAt:    time.Now(),
		HealthStatus: false,
		Resources: interfaces.ResourceSpec{
			CPU:    "1000m",
			Memory: "4Gi",
			GPU:    "0", // Adjust as needed
		},
	}

	// Start Docker container
	if err := m.startContainer(worker); err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// Save to memory
	m.workers.Store(workerID, worker)

	// Save to Redis
	if err := m.persistence.SaveWorker(worker); err != nil {
		m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to save worker to Redis")
	}

	m.logger.WithFields(logrus.Fields{
		"worker_id":      workerID,
		"container_name": containerName,
		"port":           port,
	}).Info("Created new worker")

	return worker, nil
}

// TerminateWorker terminates worker
func (m *DockerManager) TerminateWorker(workerID string) error {
	value, ok := m.workers.Load(workerID)
	if !ok {
		return fmt.Errorf("worker not found: %s", workerID)
	}

	worker := value.(*interfaces.Worker)
	worker.Status = interfaces.WorkerStatusStopped

	// Stop Docker container
	if err := m.stopContainer(worker.PodName); err != nil {
		m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to stop container")
	}

	// Remove container
	if err := m.removeContainer(worker.PodName); err != nil {
		m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to remove container")
	}

	// Remove from memory
	m.workers.Delete(workerID)

	// Delete from Redis
	if err := m.persistence.DeleteWorker(workerID); err != nil {
		m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to delete worker from Redis")
	}

	m.logger.WithField("worker_id", workerID).Info("Terminated worker")
	return nil
}

// UpdateWorkerStatus updates worker status
func (m *DockerManager) UpdateWorkerStatus(workerID string, status interfaces.WorkerStatus) error {
	value, ok := m.workers.Load(workerID)
	if !ok {
		return fmt.Errorf("worker not found: %s", workerID)
	}

	worker := value.(*interfaces.Worker)
	worker.Status = status
	worker.UpdateLastActive()

	// Save to Redis
	if err := m.persistence.SaveWorker(worker); err != nil {
		m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to update worker status in Redis")
	}

	return nil
}

// AssignTask assigns task to worker
func (m *DockerManager) AssignTask(workerID string, taskID string) error {
	value, ok := m.workers.Load(workerID)
	if !ok {
		return fmt.Errorf("worker not found: %s", workerID)
	}

	worker := value.(*interfaces.Worker)
	worker.MarkBusy(taskID)

	// Save to Redis
	if err := m.persistence.SaveWorker(worker); err != nil {
		m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to update worker task assignment in Redis")
	}

	return nil
}

// CompleteTask marks worker as completed task
func (m *DockerManager) CompleteTask(workerID string) error {
	value, ok := m.workers.Load(workerID)
	if !ok {
		return fmt.Errorf("worker not found: %s", workerID)
	}

	worker := value.(*interfaces.Worker)
	worker.MarkIdle()

	// Save to Redis
	if err := m.persistence.SaveWorker(worker); err != nil {
		m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to update worker task completion in Redis")
	}

	return nil
}

// ListWorkers lists all workers
func (m *DockerManager) ListWorkers() ([]*interfaces.Worker, error) {
	var workers []*interfaces.Worker

	m.workers.Range(func(key, value interface{}) bool {
		worker := value.(*interfaces.Worker)
		workers = append(workers, worker)
		return true
	})

	return workers, nil
}

// loadWorkersFromRedis loads worker information from Redis
func (m *DockerManager) loadWorkersFromRedis() error {
	workers, err := m.persistence.LoadAllWorkers()
	if err != nil {
		return fmt.Errorf("failed to load workers from Redis: %w", err)
	}

	count := 0
	resetCount := 0
	skippedCount := 0
	for _, worker := range workers {
		// Filter: Docker manager only loads Docker workers
		if worker.Provider != interfaces.DockerProvider {
			// This is not a Docker Worker, skip
			skippedCount++
			m.logger.WithFields(logrus.Fields{
				"worker_id": worker.ID,
				"provider":  worker.Provider,
				"endpoint":  worker.Endpoint,
			}).Debug("Skipped non-Docker worker")
			continue
		}

		// After service restart, reset all running workers to idle
		// This ensures worker status matches task status
		// If the task is really running, the scheduler will reassign
		if worker.Status == interfaces.WorkerStatusRunning {
			oldTaskID := worker.CurrentTaskID
			worker.MarkIdle()
			resetCount++

			// Update worker status in Redis
			if err := m.persistence.SaveWorker(worker); err != nil {
				m.logger.WithError(err).WithField("worker_id", worker.ID).Error("Failed to update worker status in Redis")
			}

			m.logger.WithFields(logrus.Fields{
				"worker_id":   worker.ID,
				"old_status":  "running",
				"new_status":  "idle",
				"old_task_id": oldTaskID,
			}).Info("Reset worker status after service restart")
		}

		// Load worker into memory
		m.workers.Store(worker.ID, worker)
		count++

		m.logger.WithFields(logrus.Fields{
			"worker_id": worker.ID,
			"status":    worker.Status,
			"endpoint":  worker.Endpoint,
			"pod_name":  worker.PodName,
		}).Debug("Loaded worker from Redis")
	}

	if count > 0 || skippedCount > 0 {
		m.logger.WithFields(logrus.Fields{
			"loaded_count":  count,
			"reset_count":   resetCount,
			"skipped_count": skippedCount,
		}).Info("Loaded workers from Redis persistence")
	}

	return nil
}

// discoverExistingWorkers discovers existing workers
func (m *DockerManager) discoverExistingWorkers() error {
	m.logger.Info("Discovering existing Docker workers...")

	// List all comfyui related containers
	cmd := exec.Command("docker", "ps", "-a", "--filter", "name=comfyui-worker", "--format", "{{.Names}}\t{{.Status}}\t{{.Ports}}")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list docker containers: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	discoveredCount := 0

	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}

		containerName := parts[0]
		status := parts[1]
		ports := parts[2]

		// Extract worker ID from container name
		workerID := m.extractWorkerIDFromContainerName(containerName)
		if workerID == "" {
			continue
		}

		// Parse port mapping
		port := m.extractPortFromMapping(ports)
		if port == 0 {
			m.logger.WithField("container", containerName).Warn("Could not extract port from container")
			continue
		}

		// Create worker object
		worker := &interfaces.Worker{
			ID:              workerID,
			Provider:        interfaces.DockerProvider,
			PodName:         containerName,
			Port:            port,
			CreatedAt:       time.Now(), // Cannot get real creation time, use current time
			LastActiveAt:    time.Now(),
			LastHealthCheck: time.Now(),
			HealthStatus:    false,
			Resources: interfaces.ResourceSpec{
				CPU:    "1000m",
				Memory: "4Gi",
				GPU:    "0",
			},
		}

		// Set worker status based on container status
		if strings.Contains(status, "Up") {
			// Container is running, need to get endpoint
			endpoint, err := m.getContainerEndpoint(containerName)
			if err != nil {
				m.logger.WithError(err).WithField("container", containerName).Warn("Failed to get container endpoint")
				worker.Status = interfaces.WorkerStatusError
			} else {
				worker.MarkStarted(endpoint, port)
				// Check if there is a running task
				if m.hasRunningTask(worker) {
					worker.Status = interfaces.WorkerStatusRunning
					// Here we can try to get the current task ID from ComfyUI API, leave it empty for now
					worker.CurrentTaskID = ""
				} else {
					worker.MarkIdle()
				}
				worker.HealthStatus = true
			}
		} else {
			// Container stopped
			worker.Status = interfaces.WorkerStatusError
		}

		// Store worker
		m.workers.Store(workerID, worker)
		discoveredCount++

		m.logger.WithFields(logrus.Fields{
			"worker_id": workerID,
			"container": containerName,
			"status":    worker.Status,
			"endpoint":  worker.Endpoint,
			"port":      port,
		}).Info("Discovered existing worker")
	}

	m.logger.WithField("count", discoveredCount).Info("Finished discovering existing workers")
	return nil
}

// extractWorkerIDFromContainerName extracts worker ID from container name
func (m *DockerManager) extractWorkerIDFromContainerName(containerName string) string {
	// Container name format: comfyui-worker-{first-8-chars-of-uuid}
	prefix := "comfyui-worker-"
	if !strings.HasPrefix(containerName, prefix) {
		return ""
	}

	shortID := strings.TrimPrefix(containerName, prefix)
	if len(shortID) != 8 {
		return ""
	}

	// Cannot recover full UUID, so use short ID as worker ID
	// In actual application, it may be necessary to set environment variables in the container to store the full worker ID
	return fmt.Sprintf("%s-recovered", shortID)
}

// extractPortFromMapping extracts port from port mapping string
func (m *DockerManager) extractPortFromMapping(ports string) int {
	// Port format may be: "0.0.0.0:8080->8188/tcp" or "8080->8188/tcp"
	if ports == "" {
		return 0
	}

	// Find port before ->
	parts := strings.Split(ports, "->")
	if len(parts) < 1 {
		return 0
	}

	hostPart := parts[0]
	// Remove possible IP address part
	if strings.Contains(hostPart, ":") {
		hostPart = strings.Split(hostPart, ":")[1]
	}

	port, err := strconv.Atoi(hostPart)
	if err != nil {
		return 0
	}

	return port
}

// hasRunningTask checks if worker has a running task
func (m *DockerManager) hasRunningTask(worker *interfaces.Worker) bool {
	if worker.Endpoint == "" {
		return false
	}

	// Check queue status via ComfyUI API
	queue, err := m.comfyClient.GetQueue(worker.Endpoint)
	if err != nil {
		m.logger.WithError(err).WithField("worker_id", worker.ID).Debug("Failed to get queue status during discovery")
		return false
	}

	// Check running queue
	if running, ok := queue["queue_running"]; ok {
		if runningList, ok := running.([]interface{}); ok && len(runningList) > 0 {
			// If there is a running task, try to get task ID
			if len(runningList) > 0 {
				if taskData, ok := runningList[0].([]interface{}); ok && len(taskData) > 1 {
					if promptID, ok := taskData[1].(string); ok {
						worker.CurrentTaskID = promptID
					}
				}
			}
			return true
		}
	}

	// Check pending queue
	if pending, ok := queue["queue_pending"]; ok {
		if pendingList, ok := pending.([]interface{}); ok && len(pendingList) > 0 {
			// If there is a pending task, also consider worker is working
			return true
		}
	}

	return false
}

// startContainer starts Docker container
func (m *DockerManager) startContainer(worker *interfaces.Worker) error {
	cmd := exec.Command("docker", "run", "-d",
		"--name", worker.PodName,
		"-p", fmt.Sprintf("%d:8188", worker.Port),
		"-e", fmt.Sprintf("WORKER_ID=%s", worker.ID),
		m.workerConfig.Image,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start container: %s, output: %s", err, string(output))
	}

	worker.Status = interfaces.WorkerStatusStarting

	// Wait for container to start and get IP
	go m.waitForContainerReady(worker)

	return nil
}

// stopContainer stops Docker container
func (m *DockerManager) stopContainer(containerName string) error {
	cmd := exec.Command("docker", "stop", containerName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop container %s: %w", containerName, err)
	}
	return nil
}

// removeContainer removes Docker container
func (m *DockerManager) removeContainer(containerName string) error {
	cmd := exec.Command("docker", "rm", containerName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to remove container %s: %w", containerName, err)
	}
	return nil
}

// waitForContainerReady waits for container to be ready
func (m *DockerManager) waitForContainerReady(worker *interfaces.Worker) {
	maxAttempts := 30
	for i := 0; i < maxAttempts; i++ {
		time.Sleep(2 * time.Second)

		// Get container IP
		endpoint, err := m.getContainerEndpoint(worker.PodName)
		if err != nil {
			continue
		}

		// Simple health check simulation
		worker.MarkStarted(endpoint, worker.Port)
		worker.MarkIdle()
		worker.HealthStatus = true
		m.logger.WithField("worker_id", worker.ID).Info("Worker is ready")
		return
	}

	// Start failed
	worker.MarkFailed()
	m.logger.WithField("worker_id", worker.ID).Error("Worker failed to start")

	// Remove from memory
	m.workers.Delete(worker.ID)

	// Remove from Redis
	if err := m.persistence.DeleteWorker(worker.ID); err != nil {
		m.logger.WithError(err).WithField("worker_id", worker.ID).Error("Failed to delete worker from Redis")
	}
}

// getContainerEndpoint gets container endpoint
func (m *DockerManager) getContainerEndpoint(containerName string) (string, error) {
	// Find corresponding worker and return its port
	var port int
	found := false

	m.workers.Range(func(key, value interface{}) bool {
		worker := value.(*interfaces.Worker)
		if worker.PodName == containerName {
			port = worker.Port
			found = true
			return false // Stop iteration
		}
		return true
	})

	if !found {
		return "", fmt.Errorf("worker not found for container: %s", containerName)
	}

	// Docker container can be accessed via localhost
	return fmt.Sprintf("localhost:%d", port), nil
}

// allocatePort allocates port
func (m *DockerManager) allocatePort() (int, error) {
	// Simple port allocation strategy
	for port := m.dockerConfig.PortRange.Start; port <= m.dockerConfig.PortRange.End; port++ {
		if m.isPortAvailable(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available port in range %d-%d", m.dockerConfig.PortRange.Start, m.dockerConfig.PortRange.End)
}

// isPortAvailable checks if port is available
func (m *DockerManager) isPortAvailable(port int) bool {
	inUse := false
	m.workers.Range(func(key, value interface{}) bool {
		worker := value.(*interfaces.Worker)
		if worker.Port == port {
			inUse = true
			return false
		}
		return true
	})
	return !inUse
}

// ensureMinWorkers ensures minimum worker count
func (m *DockerManager) ensureMinWorkers() error {
	current := m.GetWorkerMetrics().TotalWorkers
	if current < m.workerConfig.MinWorkers {
		needed := m.workerConfig.MinWorkers - current
		for i := 0; i < needed; i++ {
			if _, err := m.CreateWorker(); err != nil {
				return err
			}
		}
	}
	return nil
}

// scaleWorkers auto scale workers
func (m *DockerManager) scaleWorkers(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.autoScale()
		}
	}
}

// autoScale auto scale logic
func (m *DockerManager) autoScale() {
	metrics := m.GetWorkerMetrics()

	// Scale up logic
	if metrics.IdleWorkers == 0 && metrics.RunningWorkers > 0 && metrics.TotalWorkers < m.workerConfig.MaxWorkers {
		if time.Since(m.lastScaleTime) > m.workerConfig.ScaleUpCooldown {
			if _, err := m.CreateWorker(); err != nil {
				m.logger.WithError(err).Error("Failed to scale up worker")
			} else {
				m.lastScaleTime = time.Now()
				m.logger.Info("Scaled up worker")
			}
		}
	}

	// Scale down logic
	if metrics.IdleWorkers > 1 && metrics.TotalWorkers > m.workerConfig.MinWorkers {
		m.workers.Range(func(key, value interface{}) bool {
			worker := value.(*interfaces.Worker)
			if worker.CanBeTerminated(m.workerConfig.IdleTimeout) {
				if err := m.TerminateWorker(worker.ID); err != nil {
					m.logger.WithError(err).Error("Failed to scale down worker")
				} else {
					m.logger.Info("Scaled down worker")
				}
				return false // Only scale down one worker at a time
			}
			return true
		})
	}
}

// healthCheck health check
func (m *DockerManager) healthCheck(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.performHealthCheck()
		}
	}
}

// performHealthCheck performs health check
func (m *DockerManager) performHealthCheck() {
	m.workers.Range(func(key, value interface{}) bool {
		worker := value.(*interfaces.Worker)
		if worker.Status == interfaces.WorkerStatusRunning || worker.Status == interfaces.WorkerStatusIdle {
			go m.checkWorkerHealth(worker)
		}
		return true
	})
}

// checkWorkerHealth checks worker health
func (m *DockerManager) checkWorkerHealth(worker *interfaces.Worker) {
	if worker.Endpoint == "" {
		return
	}

	// Simple health check
	worker.HealthStatus = true
	worker.LastHealthCheck = time.Now()
}

// cleanupWorkers cleans up failed workers
func (m *DockerManager) cleanupWorkers(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.performCleanup()
		}
	}
}

// performCleanup performs cleanup
func (m *DockerManager) performCleanup() {
	var toDelete []string

	m.workers.Range(func(key, value interface{}) bool {
		worker := value.(*interfaces.Worker)
		if worker.Status == interfaces.WorkerStatusError {
			toDelete = append(toDelete, worker.ID)
		}
		return true
	})

	for _, workerID := range toDelete {
		if err := m.TerminateWorker(workerID); err != nil {
			m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to cleanup worker")
		}
	}
}

// persistenceCleanup Redis persistence cleanup
func (m *DockerManager) persistenceCleanup(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute) // Every 10 minutes
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.performPersistenceCleanup()
		}
	}
}

// performPersistenceCleanup performs Redis persistence cleanup
func (m *DockerManager) performPersistenceCleanup() {
	// Clean up inactive workers (more than 1 hour inactive)
	if err := m.persistence.CleanupInactiveWorkers(1 * time.Hour); err != nil {
		m.logger.WithError(err).Error("Failed to cleanup inactive workers from Redis")
	}

	// Sync worker status in memory to Redis
	m.workers.Range(func(key, value interface{}) bool {
		worker := value.(*interfaces.Worker)
		if err := m.persistence.SaveWorker(worker); err != nil {
			m.logger.WithError(err).WithField("worker_id", worker.ID).Error("Failed to sync worker to Redis")
		}
		return true
	})
}
