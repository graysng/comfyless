package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"comfyless/internal/config"
	"comfyless/internal/interfaces"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// NovitaManager based on Novita AI worker manager
type NovitaManager struct {
	workerConfig  config.WorkerConfig
	novitaConfig  config.NovitaConfig
	workers       sync.Map
	persistence   *WorkerPersistence
	client        *http.Client
	comfyClient   interfaces.ComfyUIClient
	logger        *logrus.Logger
	lastScaleTime time.Time
}

// NovitaCreateInstanceRequest create instance request
type NovitaCreateInstanceRequest struct {
	Name       string         `json:"name"`
	ProductID  string         `json:"productId"`
	GPUNum     int            `json:"gpuNum"`
	RootfsSize int            `json:"rootfsSize"`
	ImageURL   string         `json:"imageUrl"`
	Ports      string         `json:"ports"`
	Kind       string         `json:"kind"`
	Month      int            `json:"month"`
	Envs       []NovitaEnvVar `json:"envs,omitempty"`
	Tools      []NovitaTool   `json:"tools,omitempty"`
	Command    string         `json:"command,omitempty"`
}

// NovitaEnvVar environment variable
type NovitaEnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// NovitaTool tool configuration
type NovitaTool struct {
	Name string `json:"name"`
	Port string `json:"port"`
	Type string `json:"type"`
}

// NovitaCreateInstanceResponse create instance response
type NovitaCreateInstanceResponse struct {
	ID string `json:"id"`
}

// NovitaInstance Novita instance information
type NovitaInstance struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	ClusterID    string              `json:"clusterId"`
	Status       string              `json:"status"`
	StatusError  *NovitaStatusError  `json:"statusError,omitempty"`
	PortMappings []NovitaPortMapping `json:"portMappings"`
	Envs         []NovitaEnvVar      `json:"envs,omitempty"`
	Kind         string              `json:"kind"`
	BillingMode  string              `json:"billingMode"`
	EndTime      string              `json:"endTime"`
}

// NovitaStatusError status error
type NovitaStatusError struct {
	State   string `json:"state"`
	Message string `json:"message"`
}

// NovitaPortMapping port mapping
type NovitaPortMapping struct {
	Port     int    `json:"port"`
	Endpoint string `json:"endpoint"`
	Type     string `json:"type"`
}

// NovitaDeleteInstanceRequest delete instance request
type NovitaDeleteInstanceRequest struct {
	InstanceID string `json:"instanceId"`
}

// NewNovitaManager create Novita Worker manager
func NewNovitaManager(workerCfg config.WorkerConfig, novitaCfg config.NovitaConfig, redisCfg config.RedisConfig, comfyClient interfaces.ComfyUIClient) interfaces.WorkerManager {
	logger := config.NewLogger()

	// Set default values
	if novitaCfg.BaseURL == "" {
		novitaCfg.BaseURL = "https://api.novita.ai"
	}
	if novitaCfg.GPUNum == 0 {
		novitaCfg.GPUNum = 1
	}
	if novitaCfg.RootfsSize == 0 {
		novitaCfg.RootfsSize = 50
	}
	if novitaCfg.Timeout == 0 {
		novitaCfg.Timeout = 30 * time.Minute
	}

	return &NovitaManager{
		workerConfig: workerCfg,
		novitaConfig: novitaCfg,
		persistence:  NewWorkerPersistence(redisCfg),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		comfyClient:   comfyClient,
		logger:        logger,
		lastScaleTime: time.Now(),
	}
}

// Start starts Worker manager
func (m *NovitaManager) Start(ctx context.Context) error {
	m.logger.Info("Starting Novita Worker Manager")

	// First, restore Worker information from Redis
	if err := m.loadWorkersFromRedis(); err != nil {
		m.logger.WithError(err).Error("Failed to load workers from Redis")
	}

	// Initialize: discover existing workers (merge with information in Redis)
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
	m.logger.Info("Novita Worker Manager stopped")
	return nil
}

// GetAvailableWorker gets available worker
func (m *NovitaManager) GetAvailableWorker() (*interfaces.Worker, error) {
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
func (m *NovitaManager) GetWorkerByID(workerID string) (*interfaces.Worker, error) {
	if value, ok := m.workers.Load(workerID); ok {
		return value.(*interfaces.Worker), nil
	}
	return nil, fmt.Errorf("worker not found: %s", workerID)
}

// GetWorkerMetrics gets worker metrics
func (m *NovitaManager) GetWorkerMetrics() *interfaces.WorkerMetrics {
	metrics := &interfaces.WorkerMetrics{}

	m.workers.Range(func(key, value interface{}) bool {
		worker := value.(*interfaces.Worker)
		metrics.TotalWorkers++

		switch worker.Status {
		case interfaces.WorkerStatusPending:
			// Pending workers are counted in total but not in specific categories
		case interfaces.WorkerStatusStarting:
			metrics.StartingWorkers++
		case interfaces.WorkerStatusRunning:
			metrics.RunningWorkers++
		case interfaces.WorkerStatusIdle:
			metrics.IdleWorkers++
		case interfaces.WorkerStatusStopped:
			// Stopped workers are not counted in active metrics
		case interfaces.WorkerStatusError:
			metrics.FailedWorkers++
		}

		return true
	})

	return metrics
}

// CreateWorker creates new worker
func (m *NovitaManager) CreateWorker() (*interfaces.Worker, error) {
	workerID := uuid.New().String()
	workerName := fmt.Sprintf("novita-worker-%s", workerID[:8])

	worker := &interfaces.Worker{
		ID:              workerID,
		Provider:        interfaces.NovitaProvider,
		NodeName:        "",
		Status:          interfaces.WorkerStatusPending,
		HealthStatus:    false,
		CreatedAt:       time.Now(),
		LastActiveAt:    time.Now(),
		LastHealthCheck: time.Now(),
		Resources: interfaces.ResourceSpec{
			CPU:    fmt.Sprintf("%d", m.novitaConfig.GPUNum*4), // Estimated CPU cores
			Memory: fmt.Sprintf("%dGi", m.novitaConfig.RootfsSize/10),
			GPU:    fmt.Sprintf("%d", m.novitaConfig.GPUNum),
		},
	}

	// Create Novita instance
	instanceID, err := m.createNovitaInstance(workerName)
	if err != nil {
		worker.MarkFailed()
		return nil, fmt.Errorf("failed to create novita instance: %w", err)
	}

	worker.NodeName = instanceID
	worker.Status = interfaces.WorkerStatusStarting

	// Store worker
	m.workers.Store(workerID, worker)

	// Save to Redis
	if err := m.persistence.SaveWorker(worker); err != nil {
		m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to save worker to Redis")
	}

	// Start monitoring goroutine
	go m.monitorWorkerCreation(worker)

	m.logger.WithFields(logrus.Fields{
		"worker_id":   workerID,
		"instance_id": instanceID,
	}).Info("Created Novita worker")

	return worker, nil
}

// TerminateWorker terminates specified worker
func (m *NovitaManager) TerminateWorker(workerID string) error {
	worker, err := m.GetWorkerByID(workerID)
	if err != nil {
		return err
	}

	worker.Status = interfaces.WorkerStatusStopped

	// Delete Novita instance
	if err := m.deleteNovitaInstance(worker.NodeName); err != nil {
		m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to delete novita instance")
	}

	// Remove from workers
	m.workers.Delete(workerID)

	// Delete from Redis
	if err := m.persistence.DeleteWorker(workerID); err != nil {
		m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to delete worker from Redis")
	}

	m.logger.WithField("worker_id", workerID).Info("Terminated Novita worker")
	return nil
}

// UpdateWorkerStatus updates worker status
func (m *NovitaManager) UpdateWorkerStatus(workerID string, status interfaces.WorkerStatus) error {
	worker, err := m.GetWorkerByID(workerID)
	if err != nil {
		return err
	}

	worker.Status = status
	worker.UpdateLastActive()

	// Save to Redis
	if err := m.persistence.SaveWorker(worker); err != nil {
		m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to update worker status in Redis")
	}

	return nil
}

// AssignTask assigns task to worker
func (m *NovitaManager) AssignTask(workerID string, taskID string) error {
	worker, err := m.GetWorkerByID(workerID)
	if err != nil {
		return err
	}

	worker.MarkBusy(taskID)

	// Save to Redis
	if err := m.persistence.SaveWorker(worker); err != nil {
		m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to update worker task assignment in Redis")
	}

	return nil
}

// CompleteTask completes worker task
func (m *NovitaManager) CompleteTask(workerID string) error {
	worker, err := m.GetWorkerByID(workerID)
	if err != nil {
		return err
	}

	worker.MarkIdle()

	// Save to Redis
	if err := m.persistence.SaveWorker(worker); err != nil {
		m.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to update worker task completion in Redis")
	}

	return nil
}

// ListWorkers lists all workers
func (m *NovitaManager) ListWorkers() ([]*interfaces.Worker, error) {
	var workers []*interfaces.Worker

	m.workers.Range(func(key, value interface{}) bool {
		workers = append(workers, value.(*interfaces.Worker))
		return true
	})

	return workers, nil
}

// loadWorkersFromRedis loads worker information from Redis
func (m *NovitaManager) loadWorkersFromRedis() error {
	workers, err := m.persistence.LoadAllWorkers()
	if err != nil {
		return fmt.Errorf("failed to load workers from Redis: %w", err)
	}

	count := 0
	resetCount := 0
	skippedCount := 0
	for _, worker := range workers {
		// Filter: Novita manager only loads Novita workers
		if worker.Provider != interfaces.NovitaProvider {
			// This is not a Novita worker, skip
			skippedCount++
			m.logger.WithFields(logrus.Fields{
				"worker_id": worker.ID,
				"provider":  worker.Provider,
				"endpoint":  worker.Endpoint,
			}).Debug("Skipped non-Novita worker")
			continue
		}

		// After service restart, reset all running workers to idle
		// This ensures worker status is synchronized with task status
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
func (m *NovitaManager) discoverExistingWorkers() error {
	m.logger.Info("Discovering existing Novita workers...")

	// List all Novita instances
	instances, err := m.listNovitaInstances()
	if err != nil {
		return fmt.Errorf("failed to list novita instances: %w", err)
	}

	discoveredCount := 0

	for _, instance := range instances {
		// Only process workers we created
		if !strings.HasPrefix(instance.Name, "novita-worker-") {
			continue
		}

		// Extract worker ID from instance name
		workerID := m.extractWorkerIDFromInstanceName(instance.Name)
		if workerID == "" {
			continue
		}

		// Create worker object
		worker := &interfaces.Worker{
			ID:              workerID,
			Provider:        interfaces.NovitaProvider,
			NodeName:        instance.ID,
			CreatedAt:       time.Now(), // Cannot get real creation time, use current time
			LastActiveAt:    time.Now(),
			LastHealthCheck: time.Now(),
			HealthStatus:    false,
			Resources: interfaces.ResourceSpec{
				CPU:    fmt.Sprintf("%d", m.novitaConfig.GPUNum*4),
				Memory: fmt.Sprintf("%dGi", m.novitaConfig.RootfsSize/10),
				GPU:    fmt.Sprintf("%d", m.novitaConfig.GPUNum),
			},
		}

		// Set worker status and endpoint
		worker.Status = m.novitaStatusToWorkerStatus(instance.Status)

		if instance.Status == "running" {
			// Find ComfyUI port mapping
			var endpoint string
			var port int
			for _, mapping := range instance.PortMappings {
				if mapping.Port == 8188 {
					endpoint = mapping.Endpoint
					port = mapping.Port
					break
				}
			}

			if endpoint != "" {
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
		}

		// Store worker
		m.workers.Store(workerID, worker)
		discoveredCount++

		m.logger.WithFields(logrus.Fields{
			"worker_id":   workerID,
			"instance_id": instance.ID,
			"status":      worker.Status,
			"endpoint":    worker.Endpoint,
		}).Info("Discovered existing Novita worker")
	}

	m.logger.WithField("count", discoveredCount).Info("Finished discovering existing Novita workers")
	return nil
}

// listNovitaInstances lists all Novita instances
func (m *NovitaManager) listNovitaInstances() ([]*NovitaInstance, error) {
	req, err := http.NewRequest("GET",
		fmt.Sprintf("%s/gpu-instance/openapi/v1/gpu/instances", m.novitaConfig.BaseURL),
		nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", m.novitaConfig.APIKey))

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		// Try to parse error response
		var errorResp NovitaErrorResponse
		if json.Unmarshal(body, &errorResp) == nil && errorResp.IsResourceNotFound() {
			// No instance, return empty list
			return []*NovitaInstance{}, nil
		}

		return nil, fmt.Errorf("API error: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var listResp struct {
		Instances []*NovitaInstance `json:"instances"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return listResp.Instances, nil
}

// extractWorkerIDFromInstanceName extracts worker ID from instance name
func (m *NovitaManager) extractWorkerIDFromInstanceName(instanceName string) string {
	// Instance name format: novita-worker-{first-8-chars-of-uuid}
	prefix := "novita-worker-"
	if !strings.HasPrefix(instanceName, prefix) {
		return ""
	}

	shortID := strings.TrimPrefix(instanceName, prefix)
	if len(shortID) != 8 {
		return ""
	}

	// Here we cannot recover the full UUID, so use short ID as worker ID
	// In actual application, it may be necessary to set tags in the instance to store the full worker ID
	return fmt.Sprintf("%s-recovered", shortID)
}

// hasRunningTask checks if worker has a running task
func (m *NovitaManager) hasRunningTask(worker *interfaces.Worker) bool {
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

// getImageURL gets image URL to use
func (m *NovitaManager) getImageURL() string {
	// Use Novita specific ImageURL first, if not set, use public ImageURL
	if m.novitaConfig.ImageURL != "" {
		return m.novitaConfig.ImageURL
	}
	return m.workerConfig.Image
}

// createNovitaInstance creates Novita instance
func (m *NovitaManager) createNovitaInstance(name string) (string, error) {
	req := NovitaCreateInstanceRequest{
		Name:       name,
		ProductID:  m.novitaConfig.ProductID,
		GPUNum:     m.novitaConfig.GPUNum,
		RootfsSize: m.novitaConfig.RootfsSize,
		ImageURL:   m.getImageURL(),
		Ports:      "8188/http",
		Kind:       "gpu",
		Month:      0,
		Envs:       []NovitaEnvVar{},
		Command:    "",
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST",
		fmt.Sprintf("%s/gpu-instance/openapi/v1/gpu/instance/create", m.novitaConfig.BaseURL),
		bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", m.novitaConfig.APIKey))

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var createResp NovitaCreateInstanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return createResp.ID, nil
}

// NovitaErrorResponse Novita error response
type NovitaErrorResponse struct {
	Code     int                    `json:"code"`
	Reason   string                 `json:"reason"`
	Message  string                 `json:"message"`
	Metadata map[string]interface{} `json:"metadata"`
}

// IsResourceNotFound checks if it is a resource not found error
func (e *NovitaErrorResponse) IsResourceNotFound() bool {
	return e.Code == 400 && e.Reason == "RESOURCE_NOT_FOUND"
}

// ResourceNotFoundError resource not found error
type ResourceNotFoundError struct {
	InstanceID string
}

func (e *ResourceNotFoundError) Error() string {
	return fmt.Sprintf("novita instance not found: %s", e.InstanceID)
}

// IsResourceNotFound checks if the error is a resource not found error
func IsResourceNotFound(err error) bool {
	_, ok := err.(*ResourceNotFoundError)
	return ok
}

// getNovitaInstance gets Novita instance
func (m *NovitaManager) getNovitaInstance(instanceID string) (*NovitaInstance, error) {
	httpReq, err := http.NewRequest("GET",
		fmt.Sprintf("%s/gpu-instance/openapi/v1/gpu/instance?instanceId=%s", m.novitaConfig.BaseURL, instanceID),
		nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", m.novitaConfig.APIKey))

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		// Try to parse error response
		var errorResp NovitaErrorResponse
		if json.Unmarshal(body, &errorResp) == nil {
			if errorResp.IsResourceNotFound() {
				// Resource not found, return special error
				return nil, &ResourceNotFoundError{InstanceID: instanceID}
			}
		}
		return nil, fmt.Errorf("API error: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var instance NovitaInstance
	if err := json.Unmarshal(body, &instance); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &instance, nil
}

// deleteNovitaInstance deletes Novita instance
func (m *NovitaManager) deleteNovitaInstance(instanceID string) error {
	req := NovitaDeleteInstanceRequest{
		InstanceID: instanceID,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST",
		fmt.Sprintf("%s/gpu-instance/openapi/v1/gpu/instance/delete", m.novitaConfig.BaseURL),
		bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", m.novitaConfig.APIKey))

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		// Try to parse error response
		var errorResp NovitaErrorResponse
		if json.Unmarshal(body, &errorResp) == nil {
			if errorResp.IsResourceNotFound() {
				// Resource already not exists, consider delete success
				m.logger.WithField("instance_id", instanceID).Info("Novita instance already deleted")
				return nil
			}
		}
		return fmt.Errorf("API error: status=%d, body=%s", resp.StatusCode, string(body))
	}

	return nil
}

// novitaStatusToWorkerStatus converts Novita status to worker status
func (m *NovitaManager) novitaStatusToWorkerStatus(status string) interfaces.WorkerStatus {
	switch status {
	case "toCreate", "creating", "pulling":
		return interfaces.WorkerStatusStarting
	case "running":
		return interfaces.WorkerStatusRunning
	case "toStart", "starting":
		return interfaces.WorkerStatusStarting
	case "toStop", "stopping", "exited":
		return interfaces.WorkerStatusStopped
	case "toRestart", "restarting":
		return interfaces.WorkerStatusStarting
	case "toRemove", "removing", "removed":
		return interfaces.WorkerStatusStopped
	case "toReset", "resetting", "migrating", "freezing":
		return interfaces.WorkerStatusPending
	default:
		return interfaces.WorkerStatusError
	}
}

// monitorWorkerCreation monitors worker creation process
func (m *NovitaManager) monitorWorkerCreation(worker *interfaces.Worker) {
	maxAttempts := 60 // Maximum wait time is 10 minutes
	for i := 0; i < maxAttempts; i++ {
		time.Sleep(10 * time.Second)

		instance, err := m.getNovitaInstance(worker.NodeName)
		if err != nil {
			if IsResourceNotFound(err) {
				// Resource has been deleted, mark worker as failed and clean up
				worker.MarkFailed()
				m.logger.WithField("worker_id", worker.ID).Error("Novita instance was deleted during creation")

				// Remove from memory
				m.workers.Delete(worker.ID)

				// Remove from Redis
				if err := m.persistence.DeleteWorker(worker.ID); err != nil {
					m.logger.WithError(err).WithField("worker_id", worker.ID).Error("Failed to delete worker from Redis")
				}
				return
			}
			m.logger.WithError(err).WithField("worker_id", worker.ID).Warn("Failed to get instance status")
			continue
		}

		// Update worker status
		worker.Status = m.novitaStatusToWorkerStatus(instance.Status)

		if instance.Status == "running" {
			// Find ComfyUI port mapping
			var endpoint string
			var port int
			for _, mapping := range instance.PortMappings {
				if mapping.Port == 8188 {
					endpoint = mapping.Endpoint
					port = mapping.Port
					break
				}
			}

			if endpoint != "" {
				worker.MarkStarted(endpoint, port)
				worker.MarkIdle()
				worker.HealthStatus = true
				m.logger.WithField("worker_id", worker.ID).Info("Novita worker is ready")
				return
			}
		}

		if instance.Status == "removed" || instance.Status == "removing" {
			worker.MarkFailed()
			m.logger.WithField("worker_id", worker.ID).Error("Novita worker creation failed")
			return
		}
	}

	// Timeout failed
	worker.MarkFailed()
	m.logger.WithField("worker_id", worker.ID).Error("Novita worker creation timeout")

	// Remove from memory
	m.workers.Delete(worker.ID)

	// Remove from Redis
	if err := m.persistence.DeleteWorker(worker.ID); err != nil {
		m.logger.WithError(err).WithField("worker_id", worker.ID).Error("Failed to delete worker from Redis")
	}
}

// ensureMinWorkers ensures minimum worker count
func (m *NovitaManager) ensureMinWorkers() error {
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
func (m *NovitaManager) scaleWorkers(ctx context.Context) {
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
func (m *NovitaManager) autoScale() {
	metrics := m.GetWorkerMetrics()

	// Scale up logic
	if metrics.IdleWorkers == 0 && metrics.RunningWorkers > 0 && metrics.TotalWorkers < m.workerConfig.MaxWorkers {
		if time.Since(m.lastScaleTime) > m.workerConfig.ScaleUpCooldown {
			if wk, err := m.CreateWorker(); err != nil {
				m.logger.WithError(err).Error("Failed to scale up worker")
			} else {
				m.lastScaleTime = time.Now()
				m.logger.Infof("scaled up Novita worker %s", wk.ID)
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
					m.logger.Infof("scaled down Novita worker %s", worker.ID)
				}
				return false
			} else {
				m.logger.Debugf("Worker %s is not idle, skipping termination", worker.ID)
			}
			return true
		})
	}
}

// healthCheck health check
func (m *NovitaManager) healthCheck(ctx context.Context) {
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
func (m *NovitaManager) performHealthCheck() {
	m.workers.Range(func(key, value interface{}) bool {
		worker := value.(*interfaces.Worker)
		go m.checkWorkerHealth(worker)
		return true
	})
}

// checkWorkerHealth checks worker health
func (m *NovitaManager) checkWorkerHealth(worker *interfaces.Worker) {
	if worker.Status != interfaces.WorkerStatusRunning && worker.Status != interfaces.WorkerStatusIdle {
		return
	}

	// Get instance status
	instance, err := m.getNovitaInstance(worker.NodeName)
	if err != nil {
		if IsResourceNotFound(err) {
			// Instance has been deleted, mark worker as failed and immediately clean up
			worker.MarkFailed()
			worker.HealthStatus = false
			m.logger.WithField("worker_id", worker.ID).Warn("Novita instance was deleted, cleaning up worker immediately")

			// Remove from memory immediately
			m.workers.Delete(worker.ID)

			// Remove from Redis
			if err := m.persistence.DeleteWorker(worker.ID); err != nil {
				m.logger.WithError(err).WithField("worker_id", worker.ID).Error("Failed to delete worker from Redis")
			}
			return
		}
		m.logger.WithError(err).WithField("worker_id", worker.ID).Warn("Failed to get instance status during health check")
		worker.HealthStatus = false
		return
	}

	// Update health status
	if instance.Status == "running" {
		worker.HealthStatus = true
		// Don't update LastActive during health check - this prevents scale down
	} else {
		worker.HealthStatus = false
		worker.Status = m.novitaStatusToWorkerStatus(instance.Status)
	}
}

// cleanupWorkers cleans up failed workers
func (m *NovitaManager) cleanupWorkers(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
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
func (m *NovitaManager) performCleanup() {
	var workersToDelete []string

	m.workers.Range(func(key, value interface{}) bool {
		worker := value.(*interfaces.Worker)

		// Clean up failed workers
		if worker.Status == interfaces.WorkerStatusError {
			if time.Since(worker.LastActiveAt) > 10*time.Minute {
				workersToDelete = append(workersToDelete, worker.ID)
			}
		}

		// Clean up workers that have not responded for a long time
		if worker.Status == interfaces.WorkerStatusRunning || worker.Status == interfaces.WorkerStatusIdle {
			if time.Since(worker.LastActiveAt) > 30*time.Minute {
				// Check if instance still exists
				_, err := m.getNovitaInstance(worker.NodeName)
				if IsResourceNotFound(err) {
					// Instance has been deleted, mark as failed and clean up
					worker.MarkFailed()
					workersToDelete = append(workersToDelete, worker.ID)
					m.logger.WithField("worker_id", worker.ID).Info("Cleaning up worker with deleted instance")
				}
			}
		}
		return true
	})

	// Delete marked workers
	for _, workerID := range workersToDelete {
		m.workers.Delete(workerID)
		m.logger.WithField("worker_id", workerID).Info("Cleaned up worker")
	}
}

// persistenceCleanup Redis persistence cleanup goroutine
func (m *NovitaManager) persistenceCleanup(ctx context.Context) {
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
func (m *NovitaManager) performPersistenceCleanup() {
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
