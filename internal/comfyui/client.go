package comfyui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"comfyless/internal/config"
	"comfyless/internal/interfaces"
)

// Client ComfyUI API client
type Client struct {
	httpClient *http.Client
	timeout    time.Duration
	logger     *logrus.Logger
}

// NewClient creates ComfyUI client
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		timeout: 30 * time.Second,
		logger:  config.NewLogger(),
	}
}

// buildURL builds complete URL, properly handling endpoint
func (c *Client) buildURL(endpoint, path string) string {
	// If endpoint already contains protocol, use it directly
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		// Ensure endpoint doesn't end with /, path starts with /
		endpoint = strings.TrimSuffix(endpoint, "/")
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return endpoint + path
	}

	// If endpoint doesn't contain protocol, add http://
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "http://" + strings.TrimSuffix(endpoint, "/") + path
}

// SubmitWorkflow submits workflow to ComfyUI
func (c *Client) SubmitWorkflow(endpoint string, workflow map[string]interface{}) (*interfaces.ComfyUIResponse, error) {
	logrus.Debugf("Submitting workflow to endpoint: %s", endpoint)

	// Build request body
	requestBody := map[string]interface{}{
		"prompt": workflow,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal workflow: %w", err)
	}

	url := c.buildURL(endpoint, "/prompt")
	logrus.Debugf("Request URL: %s", url)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result interfaces.ComfyUIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	logrus.Debugf("Workflow submitted successfully, prompt_id: %s", result.PromptID)
	return &result, nil
}

// GetStatus gets task status
func (c *Client) GetStatus(endpoint, promptID string) (*interfaces.ComfyUIStatus, error) {
	// First try to get status from history
	historyURL := c.buildURL(endpoint, "/history/"+promptID)
	logrus.Debugf("Checking history URL: %s", historyURL)

	resp, err := c.httpClient.Get(historyURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		var history map[string]interface{}
		if json.NewDecoder(resp.Body).Decode(&history) == nil && len(history) > 0 {
			// Task completed, get from history
			logrus.Debugf("Task %s found in history, marking as completed", promptID)
			return &interfaces.ComfyUIStatus{
				ExecInfo: struct {
					QueueRemaining int `json:"queue_remaining"`
				}{
					// Task completed
					QueueRemaining: 0, // Completed tasks have 0 queue remaining
				},
			}, nil
		}
	}

	// If not in history, check queue
	queueURL := c.buildURL(endpoint, "/queue")
	logrus.Debugf("Checking queue URL: %s", queueURL)

	queueResp, err := c.httpClient.Get(queueURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get queue status: %w", err)
	}
	defer queueResp.Body.Close()

	if queueResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected queue status code: %d", queueResp.StatusCode)
	}

	var queueData map[string]interface{}
	if err := json.NewDecoder(queueResp.Body).Decode(&queueData); err != nil {
		return nil, fmt.Errorf("failed to decode queue response: %w", err)
	}

	// Parse queue information
	queueRemaining := 0
	if queueRunning, ok := queueData["queue_running"].([]interface{}); ok {
		queueRemaining += len(queueRunning)
	}
	if queuePending, ok := queueData["queue_pending"].([]interface{}); ok {
		queueRemaining += len(queuePending)
	}

	return &interfaces.ComfyUIStatus{
		ExecInfo: struct {
			QueueRemaining int `json:"queue_remaining"`
		}{
			QueueRemaining: queueRemaining,
		},
	}, nil
}

// GetResult gets task result
func (c *Client) GetResult(endpoint, promptID string) (*interfaces.ComfyUIResult, error) {
	url := c.buildURL(endpoint, "/history/"+promptID)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get result: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var history map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&history); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Extract outputs from history
	outputs := make(map[string]interface{})
	status := "completed"

	if promptData, exists := history[promptID]; exists {
		if promptMap, ok := promptData.(map[string]interface{}); ok {
			if outputsData, exists := promptMap["outputs"]; exists {
				if outputsMap, ok := outputsData.(map[string]interface{}); ok {
					outputs = outputsMap
				}
			}
		}
	}

	return &interfaces.ComfyUIResult{
		Outputs: outputs,
		Status:  status,
	}, nil
}

// CancelTask cancels task
func (c *Client) CancelTask(endpoint, promptID string) error {
	url := c.buildURL(endpoint, "/interrupt")

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create cancel request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to cancel task: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code when canceling: %d", resp.StatusCode)
	}

	return nil
}

// HealthCheck performs health check
func (c *Client) HealthCheck(endpoint string) error {
	url := c.buildURL(endpoint, "/system_stats")

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status: %d", resp.StatusCode)
	}

	return nil
}

// GetQueue gets queue status
func (c *Client) GetQueue(endpoint string) (map[string]interface{}, error) {
	url := c.buildURL(endpoint, "/queue")

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get queue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode queue response: %w", err)
	}

	return result, nil
}

// GetSystemStats gets system statistics
func (c *Client) GetSystemStats(endpoint string) (map[string]interface{}, error) {
	url := c.buildURL(endpoint, "/system_stats")

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get system stats: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ComfyUI returned status %d: %s", resp.StatusCode, string(body))
	}

	var stats map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, fmt.Errorf("failed to decode system stats: %w", err)
	}

	return stats, nil
}
