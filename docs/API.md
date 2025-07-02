# ComfyLess API Documentation

## Overview

ComfyLess provides complete REST API for managing ComfyUI task scheduling and worker management. This documentation is based on OpenAPI 3.0 specification and provides detailed API interface descriptions.

## Documentation Files

- `openapi.yaml` - OpenAPI specification in YAML format (recommended)
- `openapi.json` - OpenAPI specification in JSON format

## Quick Start

### 1. View API Documentation

You can use the following tools to view and test the API:

#### Swagger UI
```bash
# Run Swagger UI using Docker
docker run -p 8081:8080 -e SWAGGER_JSON=/openapi.yaml -v $(pwd)/openapi.yaml:/openapi.yaml swaggerapi/swagger-ui

# Visit http://localhost:8081 to view documentation
```

#### Swagger Editor
```bash
# Online editor
# Visit https://editor.swagger.io
# Paste openapi.yaml content into the editor
```

#### Redoc
```bash
# Generate static documentation using Redoc
npx redoc-cli build openapi.yaml --output docs/api.html
```

### 2. API Basic Information

- **Base URL**: `http://localhost:8080` (development environment)
- **Content-Type**: `application/json`
- **API Version**: v1

### 3. Main API Groups

#### Task Management (Tasks)
- `POST /api/v1/tasks` - Submit new task
- `GET /api/v1/tasks` - List tasks
- `GET /api/v1/tasks/{id}` - Get task details
- `DELETE /api/v1/tasks/{id}` - Cancel task

#### Worker Management (Workers)
- `GET /api/v1/workers` - List all workers
- `GET /api/v1/workers/{id}` - Get worker details
- `GET /api/v1/workers/metrics` - Get worker metrics

#### Queue Management (Queue)
- `GET /api/v1/queue/metrics` - Get queue metrics

#### Health Checks (Health)
- `GET /health` - Health check
- `GET /ready` - Readiness check

## Usage Examples

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

### Check Task Status

```bash
curl http://localhost:8080/api/v1/tasks/{task_id}
```

### Get Worker List

```bash
curl http://localhost:8080/api/v1/workers
```

### Get System Metrics

```bash
# Worker metrics
curl http://localhost:8080/api/v1/workers/metrics

# Queue metrics
curl http://localhost:8080/api/v1/queue/metrics
```

## Status Code Descriptions

### Task Status (TaskStatus)
- `pending` - Pending execution
- `running` - Executing
- `completed` - Completed
- `failed` - Failed
- `cancelled` - Cancelled

### Worker Status (WorkerStatus)
- `pending` - Pending startup
- `starting` - Starting
- `running` - Running
- `idle` - Idle
- `terminating` - Terminating
- `failed` - Failed

### HTTP Status Codes
- `200` - Success
- `201` - Created successfully
- `400` - Invalid request parameters
- `404` - Resource not found
- `500` - Internal server error
- `503` - Service unavailable

## Error Handling

All error responses follow a unified format:

```json
{
  "error": "error type",
  "code": 400,
  "message": "detailed error description"
}
```

### Common Errors

1. **Task submission failed**
   ```json
   {
     "error": "Invalid request parameters",
     "code": 400,
     "message": "workflow_id is required"
   }
   ```

2. **Resource not found**
   ```json
   {
     "error": "Resource not found",
     "code": 404,
     "message": "Task not found"
   }
   ```

3. **Service unavailable**
   ```json
   {
     "error": "Service unavailable",
     "code": 503,
     "message": "Service is not ready"
   }
   ```

## Data Models

### Task Payload Example

```json
{
  "workflow_id": "text_to_image_v1",
  "priority": 5,
  "payload": {
    "prompt": "A beautiful landscape with mountains",
    "negative_prompt": "blurry, low quality",
    "steps": 20,
    "cfg_scale": 7.5,
    "width": 512,
    "height": 512,
    "seed": -1,
    "sampler": "DPM++ 2M Karras"
  }
}
```

### Worker Resource Specification Example

```json
{
  "resources": {
    "cpu": "2",
    "memory": "4Gi",
    "gpu": "1"
  }
}
```

## SDK and Clients

### Python Client Example

```python
import requests
import json

class ComfyLessClient:
    def __init__(self, base_url="http://localhost:8080"):
        self.base_url = base_url
    
    def submit_task(self, workflow_id, payload, priority=0):
        url = f"{self.base_url}/api/v1/tasks"
        data = {
            "workflow_id": workflow_id,
            "priority": priority,
            "payload": payload
        }
        response = requests.post(url, json=data)
        return response.json()
    
    def get_task(self, task_id):
        url = f"{self.base_url}/api/v1/tasks/{task_id}"
        response = requests.get(url)
        return response.json()
    
    def list_workers(self):
        url = f"{self.base_url}/api/v1/workers"
        response = requests.get(url)
        return response.json()

# Usage example
client = ComfyLessClient()

# Submit task
task = client.submit_task(
    workflow_id="text_to_image_v1",
    payload={
        "prompt": "A beautiful landscape",
        "steps": 20
    },
    priority=5
)

print(f"Task ID: {task['id']}")

# Check task status
status = client.get_task(task['id'])
print(f"Task Status: {status['status']}")
```

### JavaScript Client Example

```javascript
class ComfyLessClient {
    constructor(baseUrl = 'http://localhost:8080') {
        this.baseUrl = baseUrl;
    }
    
    async submitTask(workflowId, payload, priority = 0) {
        const response = await fetch(`${this.baseUrl}/api/v1/tasks`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({
                workflow_id: workflowId,
                priority: priority,
                payload: payload
            })
        });
        return response.json();
    }
    
    async getTask(taskId) {
        const response = await fetch(`${this.baseUrl}/api/v1/tasks/${taskId}`);
        return response.json();
    }
    
    async listWorkers() {
        const response = await fetch(`${this.baseUrl}/api/v1/workers`);
        return response.json();
    }
}

// Usage example
const client = new ComfyLessClient();

// Submit task
const task = await client.submitTask('text_to_image_v1', {
    prompt: 'A beautiful landscape',
    steps: 20
}, 5);

console.log(`Task ID: ${task.id}`);

// Check task status
const status = await client.getTask(task.id);
console.log(`Task Status: ${status.status}`);
```

## Production Environment Deployment

### 1. API Gateway Configuration

In a production environment, it's recommended to manage API access through an API gateway:

```yaml
# nginx.conf example
upstream comfyless_backend {
    server 127.0.0.1:8080;
}

server {
    listen 443 ssl;
    server_name api.comfyless.com;
    
    location /api/ {
        proxy_pass http://comfyless_backend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
    
    location /health {
        proxy_pass http://comfyless_backend;
    }
    
    location /ready {
        proxy_pass http://comfyless_backend;
    }
}
```

### 2. Monitoring and Logging

```yaml
# docker-compose.yml example
version: '3.8'
services:
  comfyless:
    image: comfyless:latest
    ports:
      - "8080:8080"
    environment:
      - WORKER_MANAGER=docker
      - REDIS_HOST=redis
    depends_on:
      - redis
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"
  
  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
```

### 3. Performance Optimization

- Use connection pool to manage database connections
- Implement request throttling and circuit breaker mechanisms
- Enable gzip compression
- Configure appropriate caching strategies

## Version Control

API uses semantic versioning:

- **Major Version**: Incompatible API changes
- **Minor Version**: Downward compatible feature additions
- **Patch Version**: Downward compatible bug fixes

Current version: `v1.0.0`

## Support and Feedback

If you encounter issues with the API or have suggestions for improvement, please:

1. Check GitHub Issues
2. Submit Bug Report
3. Request Feature
4. Participate in Community Discussions

## License

This API documentation is licensed under the MIT License. 