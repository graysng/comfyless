# ComfyUI Serverless Configuration File Examples

This directory contains various configuration file examples for the ComfyUI Serverless project.

## Configuration File Types

### 1. YAML Configuration File (`config.yaml`)
Complete YAML format configuration file with all configurable parameters and detailed comments. Suitable for complex production environment configurations.

```yaml
# Usage example
server:
  port: 8080
redis:
  host: "localhost"
  port: 6379
# ... more configurations
```

### 2. JSON Configuration File (`config.json`)
JSON format configuration file, suitable for programmatic configuration management and API integration.

### 3. Environment Variable Configuration (`env.example`)
Environment variable configuration example, suitable for containerized deployment and cloud environments. Copy as `.env` file for use.

### 4. Docker Compose Configuration (`docker-compose.yml`)
Complete Docker Compose configuration including all related service configurations.

## Configuration Loading Priority

Configuration parameter loading priority (from high to low):

1. **Environment Variables**: `REDIS_HOST`, `PORT`, etc.
2. **Command Line Arguments**: `--port=8080`, `--redis-host=localhost`
3. **Configuration Files**: `config.yaml`, `config.json`
4. **Default Values**: Default values defined in code

## Core Configuration Items Description

Based on the actual configuration structure in `internal/config/config.go`:

### Basic Service Configuration

| Parameter | Environment Variable | Default | Description |
|-----------|---------------------|---------|-------------|
| `port` | `PORT` | `8080` | HTTP service port |

### Redis Configuration

| Parameter | Environment Variable | Default | Description |
|-----------|---------------------|---------|-------------|
| `redis.host` | `REDIS_HOST` | `localhost` | Redis host |
| `redis.port` | `REDIS_PORT` | `6379` | Redis port |
| `redis.password` | `REDIS_PASSWORD` | `""` | Redis password |
| `redis.db` | `REDIS_DB` | `0` | Database number |

### Docker Worker Configuration

| Parameter | Environment Variable | Default | Description |
|-----------|---------------------|---------|-------------|
| `worker.image` | `WORKER_IMAGE` | `comfyui-worker:latest` | Docker image |
| `worker.max_workers` | `MAX_WORKERS` | `10` | Maximum worker count |
| `worker.min_workers` | `MIN_WORKERS` | `1` | Minimum worker count |
| `worker.idle_timeout` | `IDLE_TIMEOUT` | `300` | Idle timeout (seconds) |
| `worker.scale_up_cooldown` | `SCALE_UP_COOLDOWN` | `60` | Scale up cooldown (seconds) |
| `worker.port_range.start` | `PORT_RANGE_START` | `8188` | Port range start |
| `worker.port_range.end` | `PORT_RANGE_END` | `8288` | Port range end |

### Novita AI Configuration (only used when WORKER_MANAGER=novita)

| Parameter | Environment Variable | Default | Description |
|-----------|---------------------|---------|-------------|
| `novita.api_key` | `NOVITA_API_KEY` | `""` | API key (required) |
| `novita.base_url` | `NOVITA_BASE_URL` | `https://api.novita.ai` | API base URL |
| `novita.product_id` | `NOVITA_PRODUCT_ID` | `""` | Product ID (required) |
| `novita.image_url` | `NOVITA_IMAGE_URL` | `""` | Image URL (optional) |
| `novita.gpu_num` | `NOVITA_GPU_NUM` | `1` | GPU count per instance |
| `novita.rootfs_size` | `NOVITA_ROOTFS_SIZE` | `50` | Root filesystem size (GB) |
| `novita.timeout` | `NOVITA_TIMEOUT` | `1800` | Instance timeout (seconds) |

**Note**: Novita uses the common worker configuration (`MIN_WORKERS`, `MAX_WORKERS`, `IDLE_TIMEOUT`) for scaling behavior.

## Usage Methods

### 1. Using Environment Variables

```bash
# Set environment variables
export PORT=8080
export REDIS_HOST=localhost
export NOVITA_API_KEY=your_api_key

# Run application
./comfyless
```

### 2. Using Configuration Files

```bash
# Use YAML configuration file
./comfyless --config=config/config.yaml

# Use JSON configuration file
./comfyless --config=config/config.json
```

### 3. Using Docker Compose

```bash
# Copy environment variable file
cp config/env.example .env

# Edit environment variables
vim .env

# Start all services
docker-compose up -d

# Start core services only
docker-compose up -d comfyless redis

# Start development environment (including workers)
docker-compose --profile dev up -d

# Start monitoring environment
docker-compose --profile monitoring up -d
```

## Environment-Specific Configurations

### Development Environment

```bash
# Enable debug mode
DEBUG=true
LOG_LEVEL=debug
MOCK_WORKERS=true

# Reduce resource usage
MAX_WORKERS=2
NOVITA_MAX_WORKERS=1
```

### Production Environment

```bash
# Security configuration
JWT_SECRET=your_very_strong_jwt_secret
CORS_ALLOWED_ORIGINS=https://yourdomain.com

# Performance configuration
MAX_WORKERS=50
NOVITA_MAX_WORKERS=20
DISPATCHER_MAX_CONCURRENT_TASKS=200

# Logging configuration
LOG_OUTPUT=file
LOG_FILE=/var/log/comfyless/app.log

# Storage configuration
STORAGE_S3_ENABLED=true
STORAGE_S3_BUCKET=your-bucket
```

### Test Environment

```bash
# Use in-memory database
REDIS_DB=1

# Enable mock mode
MOCK_WORKERS=true
DEBUG=true

# Reduce timeout values
DISPATCHER_TASK_TIMEOUT=300
IDLE_TIMEOUT=60
```

## Configuration Validation

### Automatic Validation

The application automatically validates basic configuration validity on startup:

- **Environment Variable Loading**: All configuration items are loaded from environment variables, using default values if not set
- **Data Type Conversion**: Automatically converts string environment variables to corresponding data types
- **Time Format**: Time-related configurations are automatically converted to Go's `time.Duration` type

### Manual Configuration Validation

You can validate configuration correctness through the following methods:

```bash
# 1. Check if environment variables are set correctly
env | grep -E "(PORT|REDIS_|WORKER_|NOVITA_)"

# 2. Use Go program to validate configuration loading
go run -c 'package main
import (
    "fmt"
    "comfyless/internal/config"
)
func main() {
    cfg := config.Load()
    fmt.Printf("Port: %d\n", cfg.Port)
    fmt.Printf("Redis: %s:%d\n", cfg.Redis.Host, cfg.Redis.Port)
    fmt.Printf("Worker Max: %d\n", cfg.Worker.MaxWorkers)
    fmt.Printf("Novita API: %s\n", cfg.Novita.BaseURL)
}'
```

### Configuration Check List

Before deployment, please check:

- ✅ **Required Items**: If using Novita, ensure `NOVITA_API_KEY`, `NOVITA_PRODUCT_ID`, `NOVITA_IMAGE_URL` are set
- ✅ **Redis Connection**: Ensure Redis service is accessible
- ✅ **Port Conflict**: Ensure configured ports are not occupied
- ✅ **Docker Permissions**: Ensure permission to access Docker socket (if using Docker Worker)
- ✅ **Resource Limits**: Ensure Worker count and timeout values meet actual needs

## Common Issues

### 1. Novita API Configuration

```bash
# Get Novita API Key
# 1. Login to Novita AI Console
# 2. Create API Key
# 3. Get Product ID and Image URL

NOVITA_API_KEY=novita_xxxxxxxxxxxxxxxx
NOVITA_PRODUCT_ID=your-product-id
NOVITA_IMAGE_URL=https://your-comfyui-image-url
```

### 2. Docker Permissions Issue

```bash
# Ensure user is in docker group
sudo usermod -aG docker $USER

# Or use sudo to run
sudo docker-compose up -d
```

### 3. Port Conflict

```bash
# Modify port configuration
PORT=8081
REDIS_PORT=6380
METRICS_PORT=9091
```

### 4. Memory Insufficient

```bash
# Adjust Worker count
MAX_WORKERS=5
NOVITA_MAX_WORKERS=3

# Adjust resource limits
WORKER_MEMORY_LIMIT=2Gi
```

### Logging View

```bash
# View application logs
docker-compose logs -f comfyless

# View all service logs
docker-compose logs -f

# View logs for specific time
docker-compose logs --since="2024-01-01T00:00:00" comfyless
``` 