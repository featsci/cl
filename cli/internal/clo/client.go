package clo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client encapsulates API connection settings
type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	AuthToken  string
	ProjectID  string
}

// NewClient creates a new client instance
func NewClient(token, projectID string) *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		BaseURL:    "https://api.clo.ru/v2",
		AuthToken:  token,
		ProjectID:  projectID,
	}
}

// sendRequest - internal method with automatic retries (Retries)
func (c *Client) sendRequest(method, path string, payload interface{}) ([]byte, int, error) {
	var jsonPayload []byte
	var err error

	// 1. Prepare JSON once (to avoid doing it in the loop)
	if payload != nil {
		jsonPayload, err = json.Marshal(payload)
		if err != nil {
			return nil, 0, fmt.Errorf("marshalling payload: %w", err)
		}
	}

	// Retry settings
	maxRetries := 50              // Try 5 times
	baseDelay := 25 * time.Second // Wait 3 seconds between attempts

	for attempt := 0; attempt < maxRetries; attempt++ {
		// 2. Important: BodyReader must be recreated on each iteration,
		// as it is "read out" when sent
		var bodyReader io.Reader
		if jsonPayload != nil {
			bodyReader = bytes.NewBuffer(jsonPayload)
		}

		url := c.BaseURL + path
		req, err := http.NewRequest(method, url, bodyReader)
		if err != nil {
			return nil, 0, fmt.Errorf("creating request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)

		// 3. Send request
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			// Network error (e.g., timeout or disconnection). Log and retry.
			fmt.Printf("[WARNING] [Attempt %d/%d] Network error: %v. Retrying in %v...\n", attempt+1, maxRetries, err, baseDelay)
			time.Sleep(baseDelay)
			continue
		}

		// Read body immediately
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close() // Close immediately after reading
		if readErr != nil {
			fmt.Printf("[WARNING] [Attempt %d/%d] Error reading body: %v. Retrying...\n", attempt+1, maxRetries, readErr)
			time.Sleep(baseDelay)
			continue
		}

		// 4. Check status
		// If it's 5xx (Internal Server Error, Gateway Timeout, etc.) - RETRY
		if resp.StatusCode >= 500 {
			fmt.Printf("[FIRE] [Attempt %d/%d] API Error %d: %s. Retrying in %v...\n", attempt+1, maxRetries, resp.StatusCode, string(body), baseDelay)
			time.Sleep(baseDelay)
			continue
		}

		// If it's 429 (Too Many Requests) - should also retry
		if resp.StatusCode == 429 {
			fmt.Printf("[TIME] [Attempt %d/%d] Rate Limit (429). Retrying in %v...\n", attempt+1, maxRetries, baseDelay)
			time.Sleep(baseDelay + 2*time.Second) // Wait a bit longer
			continue
		}

		// If it's 2xx, 400, 401, 404, etc. (client errors or success) - return result
		return body, resp.StatusCode, nil
	}

	return nil, 0, fmt.Errorf("failed after %d attempts", maxRetries)
}

// DeleteAddress deletes an IP address by ID
func (c *Client) DeleteAddress(addrID string) error {
	path := fmt.Sprintf("/addresses/%s", addrID)

	body, status, err := c.sendRequest("DELETE", path, nil)
	if err != nil {
		return err
	}

	if status != http.StatusNoContent && status != http.StatusOK {
		return fmt.Errorf("delete address failed. Status: %d, Body: %s", status, string(body))
	}
	return nil
}
