package clo

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// GetVolumeDetail gets information about a specific disk by ID
func (c *Client) GetVolumeDetail(volumeID string) (*DiskResult, error) {
	path := fmt.Sprintf("/volumes/%s", volumeID)

	body, status, err := c.sendRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}

	if status != http.StatusOK {
		return nil, fmt.Errorf("get volume detail failed. Status: %d", status)
	}

	var resp DiskDetailResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp.Result, nil
}

// GetProjectVolumes gets a list of all volumes in the project
func (c *Client) GetProjectVolumes() (*DiskListResponse, error) {
	path := fmt.Sprintf("/projects/%s/volumes", c.ProjectID)

	body, status, err := c.sendRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}

	if status != http.StatusOK {
		return nil, fmt.Errorf("get volumes failed. Status: %d", status)
	}

	var resp DiskListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteVolume deletes a volume by ID
func (c *Client) DeleteVolume(volumeID string) error {
	path := fmt.Sprintf("/volumes/%s", volumeID)

	payload := DeleteVolumePayload{
		ClearFstab: true,
		Force:      false,
	}

	body, status, err := c.sendRequest("DELETE", path, payload)
	if err != nil {
		return err
	}

	if status != http.StatusNoContent && status != http.StatusOK && status != http.StatusAccepted {
		return fmt.Errorf("delete volume failed. Status: %d, Body: %s", status, string(body))
	}
	return nil
}

// AttachVolume attaches an existing disk to a server and returns the device path (/dev/...)
func (c *Client) AttachVolume(volumeID, serverID string) (string, error) {
	path := fmt.Sprintf("/volumes/%s/attach", volumeID)

	// Form the JSON payload: {'server_id': '...'}
	payload := map[string]string{
		"server_id": serverID,
	}

	body, status, err := c.sendRequest("POST", path, payload)
	if err != nil {
		return "", err
	}

	if status != http.StatusOK && status != http.StatusAccepted && status != http.StatusNoContent {
		return "", fmt.Errorf("attach volume failed. Status: %d, Body: %s", status, string(body))
	}

	// Parse the response to get "device": "/dev/vdb"
	var resp AttachVolumeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		// If parsing failed, but status is OK - return empty string without error (although this is strange)
		return "", fmt.Errorf("failed to parse attach response: %w", err)
	}

	return resp.Result.AttachedToServer.Device, nil
}
