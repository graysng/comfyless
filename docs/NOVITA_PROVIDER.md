# Novita AI Provider

ComfyLess now supports using [Novita AI](https://novita.ai/) as a GPU instance provider, offering powerful cloud GPU computing capabilities to run ComfyUI workflows.

## Features

- ✅ **Auto-scaling**: Automatically manages GPU instances based on task load
- ✅ **Multi-GPU Support**: Supports single instance multi-GPU configuration
- ✅ **Cost Optimization**: Pay-as-you-go, automatically reclaims idle instances
- ✅ **Health Checks**: Real-time monitoring of instance status
- ✅ **Fault Recovery**: Automatic handling of instance creation failures and recovery

## Configuration

### Environment Variables

Using Novita Provider requires configuring the following environment variables:

#### Required Configuration

```bash
# Novita API key
export NOVITA_API_KEY="your-api-key-here"

# GPU product ID (can be queried through Novita API)
export NOVITA_PRODUCT_ID="your-product-id"

# ComfyUI image URL
export NOVITA_IMAGE_URL="your-comfyui-image-url"
```

#### Optional Configuration

```bash
# API base URL (default: https://api.novita.ai)
export NOVITA_BASE_URL="https://api.novita.ai"

# Number of GPUs per instance (default: 1)
export NOVITA_GPU_NUM=1

# Root filesystem size in GB (default: 50)
export NOVITA_ROOTFS_SIZE=50

# Minimum number of workers (default: 1)
export NOVITA_MIN_WORKERS=1

# Maximum number of workers (default: 10)
export NOVITA_MAX_WORKERS=10

# Idle timeout in seconds (default: 600)
export NOVITA_IDLE_TIMEOUT=600

# Redis configuration
export REDIS_HOST=localhost
export REDIS_PORT=6379

# Service port (default: 8080)
export PORT=8080
```

## Getting Configuration Information

### 1. Get API Key

1. Visit [Novita AI Console](https://novita.ai/console)
2. Register/login to your account
3. Create a new API key in API settings

### 2. Query Available GPU Products

Use Novita API to query available GPU products:

```bash
curl -X GET "https://api.novita.ai/gpu-instance/openapi/v1/gpu/product/list" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json"
```

### 3. Prepare ComfyUI Image

You need to prepare a Docker image containing ComfyUI with the following requirements:

- ComfyUI and dependencies installed
- Supports starting service on port 8188
- Responds to standard ComfyUI API

## Usage

### 1. Using Example Program

```bash
# Set environment variables
export NOVITA_API_KEY="your-api-key"
export NOVITA_PRODUCT_ID="your-product-id"
export NOVITA_IMAGE_URL="your-comfyui-image"

# Run the main program
go run cmd/main.go
```

### 2. Integration into Existing Code

```go
package main

import (
    "comfyless/internal/config"
    "comfyless/internal/worker"
    "comfyless/internal/queue"
    "comfyless/internal/api"
)

func main() {
    // Load configuration
    cfg := config.Load()
    
    // Create Novita Worker manager
    workerManager := worker.NewNovitaManager(cfg.Novita)
    
    // Create queue manager
    queueManager := queue.NewManager(cfg.Redis)
    
    // Create API handler
    apiHandler := api.NewHandler(queueManager, workerManager)
    
    // ... start service
}
```

## API Interfaces

### Instance Status Monitoring

```bash
# Get Novita Worker status
curl "http://localhost:8080/novita/status"
```

Response example:

```json
{
  "provider": "novita",
  "metrics": {
    "total_workers": 2,
    "pending_workers": 0,
    "starting_workers": 1,
    "running_workers": 1,
    "idle_workers": 0,
    "terminating_workers": 0,
    "failed_workers": 0
  }
}
```

### Worker Management

```bash
# List all workers
curl "http://localhost:8080/api/v1/workers"

# Get specific worker information
curl "http://localhost:8080/api/v1/workers/{worker-id}"

# Get worker metrics
curl "http://localhost:8080/api/v1/workers/metrics"
```

### Task Management

```bash
# Submit task
curl -X POST "http://localhost:8080/api/v1/tasks" \
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

# Check task status
curl "http://localhost:8080/api/v1/tasks/{task-id}"
```

## Instance Lifecycle

### Creation Process

1. **Request Creation**: Call Novita API to create GPU instance
2. **Status Monitoring**: Periodically check instance status (creating → pulling → running)
3. **Port Mapping**: Get external access address of the instance
4. **Health Check**: Verify ComfyUI service is ready
5. **Mark Available**: Add instance to available worker pool

### Scaling Strategy

- **Scale Up Condition**: No idle workers and running tasks exist, not exceeding maximum limit
- **Scale Down Condition**: More than 1 idle worker and exceeding idle timeout, not below minimum limit
- **Cooldown Period**: At least 2 minutes between scale-up operations

### Fault Handling

- **Creation Timeout**: Mark as failed if not ready within 10 minutes
- **Health Check**: Check instance status every 60 seconds
- **Auto Cleanup**: Clean up failed instances every 5 minutes

## Cost Optimization Recommendations

1. **Set Reasonable Minimum Workers**: Avoid unnecessary idle instances
2. **Adjust Idle Timeout**: Adjust timeout based on task frequency
3. **Choose Appropriate GPU Specs**: Select appropriate product based on model size
4. **Monitor Usage**: Monitor instance utilization through API

## Troubleshooting

### Common Issues

1. **Instance Creation Failed**
   - Check if API key is correct
   - Confirm product ID is valid
   - Check Novita account balance

2. **Image Pull Timeout**
   - Use smaller images
   - Ensure image URL is accessible
   - Check network connection

3. **ComfyUI Service Not Responding**
   - Confirm image contains ComfyUI
   - Check startup command is correct
   - Verify port configuration (8188)

### Log Viewing

The program outputs detailed log information, including:

- Instance creation and deletion events
- Status change records
- Health check results
- Scaling decisions

## Limitations

- Instance creation takes time (usually 2-5 minutes)
- Depends on Novita AI service availability
- Requires prepaid or valid payment method
- Network latency may affect response time

## Example Configuration File

Create `.env` file:

```bash
# Novita configuration
NOVITA_API_KEY=your-api-key-here
NOVITA_PRODUCT_ID=your-product-id
NOVITA_IMAGE_URL=registry.example.com/comfyui:latest
NOVITA_GPU_NUM=1
NOVITA_ROOTFS_SIZE=50
NOVITA_MIN_WORKERS=1
NOVITA_MAX_WORKERS=5
NOVITA_IDLE_TIMEOUT=300

# Redis configuration
REDIS_HOST=localhost
REDIS_PORT=6379

# Service configuration
PORT=8080
```

Start with configuration file:

```bash
set -a; source .env; set +a
go run cmd/main.go
```

## More Information

- [Novita AI Official Documentation](https://novita.ai/docs)
- [GPU Instance API Documentation](https://novita.ai/docs/api-reference/gpu-instance-create-instance)
- [ComfyUI Official Documentation](https://github.com/comfyanonymous/ComfyUI) 