package clo

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
)

// FindAvailableExternalIP searches for an existing external IP address that is not bound to a server or LB
func (c *Client) FindAvailableExternalIP() (string, error) {
	allAddrs, err := c.GetProjectAddressesMap()
	if err != nil {
		return "", fmt.Errorf("could not get project addresses: %w", err)
	}

	for id, addr := range allAddrs {
		// Conditions: address must be external AND must not be bound to anything
		if addr.External && addr.AttachedTo == nil && addr.ServerID == "" && addr.LoadBalancerID == "" {
			return id, nil // Return the ID of the first found free address
		}
	}

	// If nothing is found, return an empty string
	return "", nil
}

// CreateServer creates a server and returns its ID
func (c *Client) CreateServer(payload map[string]interface{}) (string, error) {
	path := fmt.Sprintf("/projects/%s/servers", c.ProjectID)
	body, status, err := c.sendRequest("POST", path, payload)
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return "", fmt.Errorf("API Error: %s Body: %s", http.StatusText(status), string(body))
	}
	var resp CreateServerResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	if resp.Result.ID == "" {
		return "", fmt.Errorf("no server ID in response: %s", string(body))
	}
	return resp.Result.ID, nil
}

// DeleteServer deletes a server
func (c *Client) DeleteServer(serverID string, payload DeleteServerPayload) error {
	path := fmt.Sprintf("/servers/%s", serverID)
	_, status, err := c.sendRequest("DELETE", path, payload)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusOK {
		return fmt.Errorf("delete failed. Status: %d", status)
	}
	return nil
}

// GetServersList gets a list of all servers in the project
func (c *Client) GetServersList() (*ServerListResponse, error) {
	path := fmt.Sprintf("/projects/%s/servers", c.ProjectID)
	body, status, err := c.sendRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("get servers list failed. Status: %d", status)
	}
	var resp ServerListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GenerateRandomPassword generates a cryptographically strong password
func GenerateRandomPassword() (string, error) {
	const length = 64
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	resultPw := make([]byte, length)
	for i := 0; i < length; i++ {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		resultPw[i] = chars[num.Int64()]
	}
	return string(resultPw), nil
}

// SetServerPassword sets the password on the server
func (c *Client) SetServerPassword(serverID, password string) error {
	path := fmt.Sprintf("/servers/%s/password", serverID)
	payload := map[string]string{"password": password}
	_, status, err := c.sendRequest("POST", path, payload)
	if err != nil {
		return err
	}
	if status != http.StatusAccepted && status != http.StatusNoContent {
		return fmt.Errorf("password set failed. Status: %d", status)
	}
	return nil
}

// GetProjectAddressesMap gets a map of all addresses in the project
func (c *Client) GetProjectAddressesMap() (map[string]AddressDetail, error) {
	path := fmt.Sprintf("/projects/%s/addresses", c.ProjectID)
	body, status, err := c.sendRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("get addresses failed. Status: %d", status)
	}
	var resp ProjectAddressesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	addrMap := make(map[string]AddressDetail)
	for _, addr := range resp.Result {
		addrMap[addr.ID] = addr
	}
	return addrMap, nil
}

// GetServerDetail gets the details of a specific server
func (c *Client) GetServerDetail(serverID string) (*ServerDetailResponse, error) {
	path := fmt.Sprintf("/servers/%s/detail", serverID)
	body, status, err := c.sendRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("get detail failed. Status: %d", status)
	}
	var resp ServerDetailResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
