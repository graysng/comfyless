package interfaces

// ComfyUIClient ComfyUI client interface
type ComfyUIClient interface {
	// SubmitWorkflow submits workflow
	SubmitWorkflow(endpoint string, workflow map[string]interface{}) (*ComfyUIResponse, error)

	// GetStatus gets task status
	GetStatus(endpoint string, promptID string) (*ComfyUIStatus, error)

	// GetResult gets task result
	GetResult(endpoint string, promptID string) (*ComfyUIResult, error)

	// CancelTask cancels task
	CancelTask(endpoint string, promptID string) error

	// HealthCheck performs health check
	HealthCheck(endpoint string) error

	// GetQueue gets queue status
	GetQueue(endpoint string) (map[string]interface{}, error)
}

// ComfyUIResponse ComfyUI response
type ComfyUIResponse struct {
	PromptID string `json:"prompt_id"`
	Number   int    `json:"number"`
}

// ComfyUIStatus ComfyUI status
type ComfyUIStatus struct {
	ExecInfo struct {
		QueueRemaining int `json:"queue_remaining"`
	} `json:"exec_info"`
}

// ComfyUIResult ComfyUI result
type ComfyUIResult struct {
	Outputs map[string]interface{} `json:"outputs"`
	Status  string                 `json:"status"`
}
