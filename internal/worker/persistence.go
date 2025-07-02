package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"comfyless/internal/config"
	"comfyless/internal/interfaces"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// WorkerPersistence worker persistence manager
type WorkerPersistence struct {
	redis  *redis.Client
	logger *logrus.Logger
}

// NewWorkerPersistence creates worker persistence manager
func NewWorkerPersistence(cfg config.RedisConfig) *WorkerPersistence {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	logger := config.NewLogger()

	return &WorkerPersistence{
		redis:  rdb,
		logger: logger,
	}
}

// SaveWorker saves worker to Redis
func (p *WorkerPersistence) SaveWorker(worker *interfaces.Worker) error {
	ctx := context.Background()

	// Serialize worker
	workerJSON, err := json.Marshal(worker)
	if err != nil {
		return fmt.Errorf("failed to marshal worker: %w", err)
	}

	// Save to Redis
	key := fmt.Sprintf("worker:%s", worker.ID)
	if err := p.redis.Set(ctx, key, workerJSON, 0).Err(); err != nil {
		return fmt.Errorf("failed to save worker to Redis: %w", err)
	}

	// Add to active worker set
	if err := p.redis.SAdd(ctx, "active_workers", worker.ID).Err(); err != nil {
		p.logger.WithError(err).Warn("Failed to add worker to active set")
	}

	// Update last active time
	if err := p.redis.ZAdd(ctx, "worker_last_active", redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: worker.ID,
	}).Err(); err != nil {
		p.logger.WithError(err).Warn("Failed to update worker last active time")
	}

	p.logger.WithFields(logrus.Fields{
		"worker_id": worker.ID,
		"status":    worker.Status,
		"endpoint":  worker.Endpoint,
	}).Debug("Worker saved to Redis")

	return nil
}

// LoadWorker loads worker from Redis
func (p *WorkerPersistence) LoadWorker(workerID string) (*interfaces.Worker, error) {
	ctx := context.Background()

	key := fmt.Sprintf("worker:%s", workerID)
	workerJSON := p.redis.Get(ctx, key)
	if workerJSON.Err() != nil {
		if workerJSON.Err() == redis.Nil {
			return nil, fmt.Errorf("worker not found: %s", workerID)
		}
		return nil, fmt.Errorf("failed to load worker from Redis: %w", workerJSON.Err())
	}

	var worker interfaces.Worker
	if err := json.Unmarshal([]byte(workerJSON.Val()), &worker); err != nil {
		return nil, fmt.Errorf("failed to unmarshal worker: %w", err)
	}

	return &worker, nil
}

// LoadAllWorkers loads all workers from Redis
func (p *WorkerPersistence) LoadAllWorkers() ([]*interfaces.Worker, error) {
	ctx := context.Background()

	// Get all active worker IDs
	workerIDs := p.redis.SMembers(ctx, "active_workers")
	if workerIDs.Err() != nil {
		return nil, fmt.Errorf("failed to get active workers: %w", workerIDs.Err())
	}

	var workers []*interfaces.Worker
	for _, workerID := range workerIDs.Val() {
		worker, err := p.LoadWorker(workerID)
		if err != nil {
			p.logger.WithError(err).WithField("worker_id", workerID).Warn("Failed to load worker")
			continue
		}
		workers = append(workers, worker)
	}

	p.logger.WithField("count", len(workers)).Info("Loaded workers from Redis")
	return workers, nil
}

// DeleteWorker deletes worker from Redis
func (p *WorkerPersistence) DeleteWorker(workerID string) error {
	ctx := context.Background()

	// Delete worker data
	key := fmt.Sprintf("worker:%s", workerID)
	if err := p.redis.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to delete worker from Redis: %w", err)
	}

	// Remove from active worker set
	if err := p.redis.SRem(ctx, "active_workers", workerID).Err(); err != nil {
		p.logger.WithError(err).Warn("Failed to remove worker from active set")
	}

	// Remove from last active time record
	if err := p.redis.ZRem(ctx, "worker_last_active", workerID).Err(); err != nil {
		p.logger.WithError(err).Warn("Failed to remove worker from last active set")
	}

	p.logger.WithField("worker_id", workerID).Debug("Worker deleted from Redis")
	return nil
}

// UpdateWorkerStatus updates worker status
func (p *WorkerPersistence) UpdateWorkerStatus(workerID string, status interfaces.WorkerStatus) error {
	// Load worker first
	worker, err := p.LoadWorker(workerID)
	if err != nil {
		return err
	}

	// Update status
	worker.Status = status
	worker.UpdateLastActive()

	// Save back to Redis
	return p.SaveWorker(worker)
}

// UpdateWorkerTask updates worker task information
func (p *WorkerPersistence) UpdateWorkerTask(workerID string, taskID string, busy bool) error {
	// Load worker first
	worker, err := p.LoadWorker(workerID)
	if err != nil {
		return err
	}

	// Update task information
	if busy {
		worker.MarkBusy(taskID)
	} else {
		worker.MarkIdle()
	}

	// Save back to Redis
	return p.SaveWorker(worker)
}

// CleanupInactiveWorkers cleans up inactive workers
func (p *WorkerPersistence) CleanupInactiveWorkers(maxInactiveTime time.Duration) error {
	ctx := context.Background()

	// Get timed out workers
	cutoff := time.Now().Add(-maxInactiveTime).Unix()
	result := p.redis.ZRangeByScore(ctx, "worker_last_active", &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%d", cutoff),
	})

	if result.Err() != nil {
		return fmt.Errorf("failed to get inactive workers: %w", result.Err())
	}

	inactiveWorkers := result.Val()
	if len(inactiveWorkers) == 0 {
		return nil
	}

	// Delete inactive workers
	for _, workerID := range inactiveWorkers {
		if err := p.DeleteWorker(workerID); err != nil {
			p.logger.WithError(err).WithField("worker_id", workerID).Error("Failed to cleanup inactive worker")
		}
	}

	p.logger.WithField("count", len(inactiveWorkers)).Info("Cleaned up inactive workers")
	return nil
}

// GetWorkerCount gets worker count
func (p *WorkerPersistence) GetWorkerCount() (int, error) {
	ctx := context.Background()
	count := p.redis.SCard(ctx, "active_workers")
	if count.Err() != nil {
		return 0, fmt.Errorf("failed to get worker count: %w", count.Err())
	}
	return int(count.Val()), nil
}

// GetWorkersByStatus gets worker list by status
func (p *WorkerPersistence) GetWorkersByStatus(status interfaces.WorkerStatus) ([]*interfaces.Worker, error) {
	workers, err := p.LoadAllWorkers()
	if err != nil {
		return nil, err
	}

	var filteredWorkers []*interfaces.Worker
	for _, worker := range workers {
		if worker.Status == status {
			filteredWorkers = append(filteredWorkers, worker)
		}
	}

	return filteredWorkers, nil
}

// HealthCheck checks Redis connection health status
func (p *WorkerPersistence) HealthCheck() error {
	ctx := context.Background()
	return p.redis.Ping(ctx).Err()
}
