package ycloud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	HTTPClient *http.Client
	BaseURL    string // MKS API
	VPCURL     string // VPC API
	IAMToken   string
	FolderID   string
}

func NewClient(token, folderID string) *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		// URLs remain yandex.net as they are the actual endpoints
		BaseURL:  "https://mks.api.cloud.yandex.net/managed-kubernetes/v1",
		VPCURL:   "https://vpc.api.cloud.yandex.net/vpc/v1",
		IAMToken: token,
		FolderID: folderID,
	}
}

func (c *Client) SendRequest(method, url string, payload interface{}) ([]byte, int, error) {
	var jsonPayload []byte
	var err error
	if payload != nil {
		jsonPayload, err = json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
	}

	req, err := http.NewRequest(method, url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.IAMToken)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode, nil
}

// WaitForOperation waits for an operation to complete
func (c *Client) WaitForOperation(opID string) error {
	opURL := fmt.Sprintf("https://operation.api.cloud.yandex.net/operations/%s", opID)

	fmt.Printf("[TIME] Waiting for operation %s ", opID)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		body, status, err := c.SendRequest("GET", opURL, nil)
		if err != nil || status != 200 {
			return fmt.Errorf("polling error: %v", err)
		}

		var op OperationResponse
		json.Unmarshal(body, &op)

		if op.Done {
			if op.Error.Message != "" {
				return fmt.Errorf("\n[ERROR] Operation Failed: %s", op.Error.Message)
			}
			fmt.Println("\n[+OK+] Done!")
			return nil
		}
		fmt.Print(".")
	}
	return nil
}
