# ComfyLess - ComfyUI Serverless Platform

ComfyLess is a ComfyUI serverless platform developed in Go, providing task queue management and automatic worker scaling capabilities.

## Features

- 🚀 **Component-based Design**: Queue management and worker management use interface design for pluggable replacement
- ☁️ **Multi-cloud Support**: Supports Docker local deployment and Novita AI cloud GPU instances
- 📦 **Docker Containerization**: Uses Docker to manage ComfyUI worker instances
- 🔄 **Auto-scaling**: Automatically scales worker count based on task load
- 💾 **Task Queue**: Redis-based task queue
- 🎯 **Standard API**: Compatible with ComfyUI standard API interface
- 📊 **Monitoring Metrics**: Provides detailed queue and worker metrics monitoring
- 🔧 **Health Checks**: Automatic worker health status monitoring

## Worker Providers

ComfyLess supports multiple worker providers:

### 🐳 Docker Provider (Default)
- Uses local Docker to manage worker instances
- Suitable for development environments and small-scale deployments
- Requires Docker environment

### ☁️ Novita AI Provider
- Uses [Novita AI](https://novita.ai/) cloud GPU instances
- Auto-scaling, pay-as-you-go
- Supports multiple GPU specifications
- See [Novita Provider Documentation](docs/NOVITA_PROVIDER.md)

## Quick Start

### Prerequisites

- Go 1.21+
- Redis
- Docker (if using Docker Provider)
- Novita AI account (if using Novita Provider)

### Install Dependencies

```bash
go mod download
```

### Using Docker Provider

```bash
# Option 1: Use quick start configuration
cp deploy/quickstart.env .env
source .env

# Build and run
go build -o comfyless ./cmd/main.go
./comfyless

# Option 2: Manual configuration
export REDIS_HOST=localhost
export REDIS_PORT=6379
export WORKER_IMAGE=novitalabs/comfyui:flux1-dev-fp8v5
export MAX_WORKERS=10
export MIN_WORKERS=1

go run ./cmd/main.go
```

### Using Novita AI Provider

```bash
# Use environment configuration
cp deploy/env.example .env
# Edit .env file with your Novita credentials
vim .env

# Or set environment variables directly
export NOVITA_API_KEY="your-api-key"
export NOVITA_PRODUCT_ID="your-product-id"
export REDIS_HOST=localhost
export REDIS_PORT=6379

# Build and run
go build -o comfyless ./cmd/main.go
./comfyless
```

### Using Docker Compose

```bash
# Quick start with Docker Compose
cd deploy/
cp env.example .env
# Edit .env with your configuration
docker-compose up -d
```

For detailed configuration, please refer to the [Novita Provider Documentation](docs/NOVITA_PROVIDER.md) and [Deployment Documentation](deploy/README.md).

## API Interface

### Submit Task

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

### Query Task Status

```bash
curl http://localhost:8080/api/v1/tasks/{task_id}
```

### View Worker Status

```bash
# Get worker metrics
curl http://localhost:8080/api/v1/workers/metrics

# List all workers
curl http://localhost:8080/api/v1/workers

# Novita Provider specific status interface
curl http://localhost:8080/novita/status
```

## Component Architecture

Both queue manager and worker manager are designed as interfaces that can be replaced as needed.

### Queue Manager Interface

```go
type QueueManager interface {
    Start(ctx context.Context) error
    AddTask(task *queue.Task) error
    GetNextTask() (*queue.Task, error)
    UpdateTask(task *queue.Task) error
}
```

### Worker Manager Interface

```go
type WorkerManager interface {
    Start(ctx context.Context) error
    GetAvailableWorker() (*interfaces.Worker, error)
    CreateWorker() (*interfaces.Worker, error)
    TerminateWorker(workerID string) error
    GetWorkerMetrics() *interfaces.WorkerMetrics
}
```

## Architecture Overview

ComfyLess follows a modular architecture with clear separation of concerns:

- **API Layer** (`internal/api/`): Handles HTTP requests and responses
- **Queue Management** (`internal/queue/`): Redis-based task queue with priority support
- **Worker Management** (`internal/worker/`): Pluggable worker providers (Docker, Novita AI)
- **Task Dispatcher** (`internal/dispatcher/`): Orchestrates task assignment and monitoring
- **ComfyUI Client** (`internal/comfyui/`): Communicates with ComfyUI instances
- **Configuration** (`internal/config/`): Environment-based configuration management

## Project Structure

```
comfyless/
├── cmd/
│   └── main.go                    # Main program entry point
├── internal/                      # Internal application code
│   ├── api/                       # HTTP API layer
│   │   ├── handler.go             # API request handlers
│   │   └── types.go               # API request/response types
│   ├── queue/                     # Task queue management
│   │   ├── manager.go             # Queue manager implementation
│   │   └── types.go               # Queue data types
│   ├── worker/                    # Worker management
│   │   ├── docker_manager.go      # Docker provider implementation
│   │   ├── novita_manager.go      # Novita AI provider implementation
│   │   ├── factory.go             # Worker factory
│   │   └── persistence.go         # Worker state persistence
│   ├── dispatcher/                # Task dispatcher
│   │   └── dispatcher.go          # Task dispatch logic
│   ├── interfaces/                # Interface definitions
│   │   ├── queue.go               # Queue interface
│   │   ├── worker.go              # Worker interface
│   │   └── types.go               # Common types
│   ├── comfyui/                   # ComfyUI client
│   │   └── client.go              # ComfyUI API client
│   └── config/                    # Configuration management
│       └── config.go              # Configuration loading
├── deploy/                        # Deployment configurations
│   ├── Dockerfile                 # Docker image build file
│   ├── docker-compose.yml         # Docker Compose configuration
│   ├── config.json                # JSON configuration example
│   ├── env.example                # Environment variables template
│   ├── quickstart.env             # Quick start configuration
│   ├── development.env            # Development environment config
│   ├── production.env             # Production environment config
│   └── README.md                  # Deployment documentation
├── docs/                          # Documentation
│   ├── openapi.yaml               # OpenAPI specification (YAML)
│   ├── openapi.json               # OpenAPI specification (JSON)
│   ├── API.md                     # API documentation
│   ├── TASK_DISPATCH_FLOW.md      # Task dispatch flow documentation
│   ├── COMPLETE_WORKFLOW.md       # Complete workflow documentation
│   └── NOVITA_PROVIDER.md         # Novita AI provider documentation
├── examples/                      # Example files
│   └── create_task.json           # Task creation example
├── go.mod                         # Go module definition
├── go.sum                         # Go module checksums
├── LICENSE                        # License
├── .gitignore                     # Git ignore rules
└── README.md                      # Project documentation
```

## Documentation

Comprehensive documentation is available in the `docs/` directory:

- **[API Documentation](docs/API.md)** - Complete REST API reference with examples
- **[Task Dispatch Flow](docs/TASK_DISPATCH_FLOW.md)** - Detailed task lifecycle and dispatch logic
- **[Complete Workflow](docs/COMPLETE_WORKFLOW.md)** - End-to-end system workflow documentation
- **[Novita Provider](docs/NOVITA_PROVIDER.md)** - Cloud GPU provider integration guide
- **[OpenAPI Specification](docs/openapi.yaml)** - Machine-readable API specification

For deployment and configuration:

- **[Deployment Guide](deploy/README.md)** - Deployment configurations and environment setup
- **[Environment Examples](deploy/)** - Ready-to-use configuration templates

## License

This project is licensed under the Apache 2.0 License.
