package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config application configuration
type Config struct {
	Port          int
	WorkerManager string // worker manager type: "docker", "novita"
	Redis         RedisConfig
	Worker        WorkerConfig    // common worker configuration
	Providers     ProvidersConfig // provider-specific configurations
}

// RedisConfig Redis configuration
type RedisConfig struct {
	Host     string
	Port     int
	Password string
	DB       int
}

// WorkerConfig common worker configuration
type WorkerConfig struct {
	// general configuration
	Image           string        `yaml:"image" env:"IMAGE"`
	MaxWorkers      int           `yaml:"max_workers" env:"MAX_WORKERS"`
	MinWorkers      int           `yaml:"min_workers" env:"MIN_WORKERS"`
	IdleTimeout     time.Duration `yaml:"idle_timeout" env:"IDLE_TIMEOUT"`
	ScaleUpCooldown time.Duration `yaml:"scale_up_cooldown" env:"SCALE_UP_COOLDOWN"`
}

// ProvidersConfig provider configurations
type ProvidersConfig struct {
	Docker DockerConfig `yaml:"docker"`
	Novita NovitaConfig `yaml:"novita"`
}

// DockerConfig Docker provider specific configuration
type DockerConfig struct {
	PortRange PortRange `yaml:"port_range"`
}

// NovitaConfig Novita provider specific configuration
type NovitaConfig struct {
	// authentication configuration
	APIKey  string `yaml:"api_key" env:"NOVITA_API_KEY"`
	BaseURL string `yaml:"base_url" env:"NOVITA_BASE_URL"`

	// instance configuration
	ProductID  string `yaml:"product_id" env:"NOVITA_PRODUCT_ID"`
	ImageURL   string `yaml:"image_url" env:"NOVITA_IMAGE_URL"`
	GPUNum     int    `yaml:"gpu_num" env:"NOVITA_GPU_NUM"`
	RootfsSize int    `yaml:"rootfs_size" env:"NOVITA_ROOTFS_SIZE"`

	// advanced configuration
	Timeout time.Duration `yaml:"timeout" env:"NOVITA_TIMEOUT"`
}

// PortRange port range
type PortRange struct {
	Start int `yaml:"start" env:"PORT_RANGE_START"`
	End   int `yaml:"end" env:"PORT_RANGE_END"`
}

// Load loads configuration
func Load() *Config {
	cfg := &Config{
		Port:          getEnvInt("PORT", 8080),
		WorkerManager: getEnv("WORKER_MANAGER", "docker"), // default to docker
		Redis: RedisConfig{
			Host:     getEnv("REDIS_HOST", "localhost"),
			Port:     getEnvInt("REDIS_PORT", 6379),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       getEnvInt("REDIS_DB", 0),
		},
		Worker: WorkerConfig{
			// common worker configuration
			Image:           getEnv("IMAGE", "comfyui-worker:latest"),
			MaxWorkers:      getEnvInt("MAX_WORKERS", 10),
			MinWorkers:      getEnvInt("MIN_WORKERS", 1),
			IdleTimeout:     time.Duration(getEnvInt("IDLE_TIMEOUT", 300)) * time.Second,
			ScaleUpCooldown: time.Duration(getEnvInt("SCALE_UP_COOLDOWN", 60)) * time.Second,
		},
		Providers: ProvidersConfig{
			Docker: DockerConfig{
				PortRange: PortRange{
					Start: getEnvInt("PORT_RANGE_START", 8188),
					End:   getEnvInt("PORT_RANGE_END", 8288),
				},
			},
			Novita: NovitaConfig{
				// authentication configuration
				APIKey:  getEnv("NOVITA_API_KEY", ""),
				BaseURL: getEnv("NOVITA_BASE_URL", "https://api.novita.ai"),

				// instance configuration
				ProductID:  getEnv("NOVITA_PRODUCT_ID", ""),
				ImageURL:   getEnv("NOVITA_IMAGE_URL", ""),
				GPUNum:     getEnvInt("NOVITA_GPU_NUM", 1),
				RootfsSize: getEnvInt("NOVITA_ROOTFS_SIZE", 50),

				// advanced configuration
				Timeout: time.Duration(getEnvInt("NOVITA_TIMEOUT", 1800)) * time.Second,
			},
		},
	}

	return cfg
}

// GetNovitaConfig gets Novita configuration (convenience method)
func (c *Config) GetNovitaConfig() NovitaConfig {
	return c.Providers.Novita
}

// GetDockerConfig gets Docker configuration (convenience method)
func (c *Config) GetDockerConfig() DockerConfig {
	return c.Providers.Docker
}

// ValidateNovitaConfig validates Novita configuration
func (c *Config) ValidateNovitaConfig() error {
	novita := c.GetNovitaConfig()
	if novita.APIKey == "" {
		return ErrNovitaAPIKeyRequired
	}
	if novita.ProductID == "" {
		return ErrNovitaProductIDRequired
	}
	// Novita can use common IMAGE or its own ImageURL
	if novita.ImageURL == "" && c.Worker.Image == "" {
		return ErrNovitaImageRequired
	}
	return nil
}

// ValidateDockerConfig validates Docker configuration
func (c *Config) ValidateDockerConfig() error {
	if c.Worker.Image == "" {
		return ErrDockerImageRequired
	}
	docker := c.GetDockerConfig()
	if docker.PortRange.Start >= docker.PortRange.End {
		return ErrDockerPortRangeInvalid
	}
	return nil
}

// configuration validation errors
var (
	ErrNovitaAPIKeyRequired    = fmt.Errorf("novita API key is required")
	ErrNovitaProductIDRequired = fmt.Errorf("novita product ID is required")
	ErrNovitaImageRequired     = fmt.Errorf("novita image URL or common image is required")
	ErrDockerImageRequired     = fmt.Errorf("docker image is required")
	ErrDockerPortRangeInvalid  = fmt.Errorf("docker port range is invalid")
)

// getEnv gets environment variable, returns default value if not exists
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt gets integer environment variable, returns default value if not exists
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}
