package worker

import (
	"fmt"

	"comfyless/internal/config"
	"comfyless/internal/interfaces"
)

// WorkerManagerType worker manager type
type WorkerManagerType string

const (
	WorkerManagerTypeDocker WorkerManagerType = "docker" // Use Docker
	WorkerManagerTypeNovita WorkerManagerType = "novita" // Use Novita
)

// Factory worker manager factory
type Factory struct{}

// NewFactory creates factory instance
func NewFactory() *Factory {
	return &Factory{}
}

// CreateWorkerManager creates worker manager based on configuration
func (f *Factory) CreateWorkerManager(cfg *config.Config, comfyClient interfaces.ComfyUIClient) (interfaces.WorkerManager, error) {
	switch WorkerManagerType(cfg.WorkerManager) {
	case WorkerManagerTypeDocker:
		// Validate Docker configuration
		if err := cfg.ValidateDockerConfig(); err != nil {
			return nil, fmt.Errorf("docker configuration validation failed: %w", err)
		}
		// Use Docker manager, pass common config, Docker-specific config and Redis config
		return NewDockerManager(cfg.Worker, cfg.GetDockerConfig(), cfg.Redis, comfyClient), nil
	case WorkerManagerTypeNovita:
		// Validate Novita configuration
		if err := cfg.ValidateNovitaConfig(); err != nil {
			return nil, fmt.Errorf("novita configuration validation failed: %w", err)
		}
		// Use Novita manager, pass common config, Novita-specific config and Redis config
		return NewNovitaManager(cfg.Worker, cfg.GetNovitaConfig(), cfg.Redis, comfyClient), nil
	default:
		return nil, fmt.Errorf("unsupported worker manager type: %s", cfg.WorkerManager)
	}
}

// GetSupportedTypes gets supported worker manager types
func (f *Factory) GetSupportedTypes() []string {
	return []string{
		string(WorkerManagerTypeDocker),
		string(WorkerManagerTypeNovita),
	}
}

// ValidateWorkerManagerType validates if worker manager type is supported
func (f *Factory) ValidateWorkerManagerType(managerType string) bool {
	supportedTypes := f.GetSupportedTypes()
	for _, t := range supportedTypes {
		if t == managerType {
			return true
		}
	}
	return false
}
