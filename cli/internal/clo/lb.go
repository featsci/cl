package clo

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// GetLoadBalancers gets a list of all load balancers in the project.
func (c *Client) GetLoadBalancers() (*LoadBalancerListResponse, error) {
	path := fmt.Sprintf("/projects/%s/loadbalancers", c.ProjectID)

	body, status, err := c.sendRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("get lb list failed. Status: %d, Body: %s", status, string(body))
	}

	var resp LoadBalancerListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteLoadBalancer deletes a load balancer by its ID.
func (c *Client) DeleteLoadBalancer(lbID string) error {
	path := fmt.Sprintf("/loadbalancers/%s", lbID)

	body, status, err := c.sendRequest("DELETE", path, nil)
	if err != nil {
		return err
	}

	// Successful deletion can return 200, 202 (in progress) or 204 (no content)
	if status != http.StatusOK && status != http.StatusAccepted && status != http.StatusNoContent {
		return fmt.Errorf("delete lb failed. Status: %d, Body: %s", status, string(body))
	}
	return nil
}

// CreateLoadBalancer creates a load balancer via API
func (c *Client) CreateLoadBalancer(req CreateLBRequest) (string, error) {
	path := fmt.Sprintf("/projects/%s/loadbalancers", c.ProjectID)

	body, status, err := c.sendRequest("POST", path, req)
	if err != nil {
		return "", err
	}

	if status != http.StatusCreated && status != http.StatusOK {
		return "", fmt.Errorf("create lb failed. Status: %d, Body: %s", status, string(body))
	}

	var resp CreateLBResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}

	return resp.Result.ID, nil
}
