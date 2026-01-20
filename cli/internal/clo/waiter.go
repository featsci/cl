package clo

import (
	"fmt"
	"strings"
	"time"
)

// WaitForStatus waits for the target status.
// If it encounters ERROR or 404, it immediately returns an error to trigger recreation.
func (c *Client) WaitForStatus(serverID string, targetStatuses []string, maxAttempts int, interval time.Duration) (string, []string, []string, error) {

	for i := 0; i < maxAttempts; i++ {
		detail, err := c.GetServerDetail(serverID)

		// 1. Handle request error (including 404)
		if err != nil {
			// If the server is deleted (404), there's no point in waiting further
			if strings.Contains(err.Error(), "Status: 404") {
				return "DELETED", nil, nil, fmt.Errorf("server %s not found (404), aborting wait", serverID)
			}
			// Other network errors - log and try again (retry)
			fmt.Printf("   [TIME] Attempt %d: API error: %v. Waiting...\n", i+1, err)
			time.Sleep(interval)
			continue
		}

		currentStatus := detail.Result.Status

		// Collect resource IDs
		addrIDs := detail.Result.Addresses
		volIDs := make([]string, len(detail.Result.Storages))
		for idx, s := range detail.Result.Storages {
			volIDs[idx] = s.ID
		}

		// 2. Log current status (UNCOMMENTED)
		// Output status if it's not the target status yet
		isTarget := false
		for _, target := range targetStatuses {
			if currentStatus == target {
				isTarget = true
				break
			}
		}

		if !isTarget {
			fmt.Printf("   ... [%s] Current status: %s\n", serverID, currentStatus)
		}

		// 3. Check for fatal ERROR status
		if currentStatus == "ERROR" {
			return "ERROR", addrIDs, volIDs, fmt.Errorf("server entered ERROR state")
		}

		// 4. Check for success
		if isTarget {
			return currentStatus, addrIDs, volIDs, nil
		}

		time.Sleep(interval)
	}

	return "", nil, nil, fmt.Errorf("timeout: server did not reach status %v", targetStatuses)
}
