package ycloud

import (
	"encoding/json"
	"fmt"
)

// EnsureNetworkAndSubnet (Left unchanged, provided for context)
func (c *Client) GetDefaultNetworkAndSubnet(zoneID string) (string, string, error) {
	// ... (entire search/creation code that was there previously) ...
	// To save space, I will not duplicate the entire search code, you already have the correct one.
	// If needed, I can send the entire file.
	// The new methods follow below.

	// TEMPORARILY duplicating the beginning so the file is valid for copying
	netURL := fmt.Sprintf("%s/networks?folderId=%s", c.VPCURL, c.FolderID)
	body, _, err := c.SendRequest("GET", netURL, nil)
	if err != nil {
		return "", "", err
	}

	var netList NetworkListResponse
	json.Unmarshal(body, &netList)

	var networkID string
	targetNetName := "netwrk-kman"

	for _, n := range netList.Networks {
		if n.Name == targetNetName {
			networkID = n.ID
			break
		}
	}
	if networkID == "" {
		// Fallback logic...
		for _, n := range netList.Networks {
			if n.Name == "ycloud-network" {
				networkID = n.ID
				break
			}
		}
	}
	if networkID == "" && len(netList.Networks) > 0 {
		networkID = netList.Networks[0].ID
	}
	if networkID == "" {
		fmt.Printf("   [WARNING] Network missing. Creating '%s'...\n", targetNetName)
		newNetID, err := c.CreateNetwork(targetNetName)
		if err != nil {
			return "", "", fmt.Errorf("create network: %w", err)
		}
		networkID = newNetID
	}

	subURL := fmt.Sprintf("%s/subnets?folderId=%s", c.VPCURL, c.FolderID)
	body, _, err = c.SendRequest("GET", subURL, nil)
	if err != nil {
		return "", "", err
	}

	var subList SubnetListResponse
	json.Unmarshal(body, &subList)

	var subnetID string
	targetSubName := "subnetwork-kman"
	targetCIDR := "192.168.0.0/16"

	for _, s := range subList.Subnets {
		if s.NetworkID != networkID {
			continue
		}
		if s.Name == targetSubName {
			subnetID = s.ID
			break
		}
		for _, cidr := range s.V4CIDRBlocks {
			if cidr == targetCIDR {
				subnetID = s.ID
				break
			}
		}
		if subnetID != "" {
			break
		}
	}

	if subnetID == "" {
		fmt.Printf("   [WARNING] Subnet not found. Creating '%s' in zone %s...\n", targetSubName, zoneID)
		newSubID, err := c.CreateSubnet(targetSubName, networkID, zoneID, targetCIDR)
		if err != nil {
			return "", "", fmt.Errorf("create subnet: %w", err)
		}
		subnetID = newSubID
	}

	return networkID, subnetID, nil
}

// ... CreateNetwork, CreateSubnet, Find... (old methods) ...
func (c *Client) CreateNetwork(name string) (string, error) {
	url := fmt.Sprintf("%s/networks", c.VPCURL)
	payload := map[string]string{"folderId": c.FolderID, "name": name}
	body, status, err := c.SendRequest("POST", url, payload)
	if err != nil || status != 200 {
		return "", fmt.Errorf("err %d: %s", status, string(body))
	}
	var op OperationResponse
	json.Unmarshal(body, &op)
	if err := c.WaitForOperation(op.ID); err != nil {
		return "", err
	}
	return c.FindNetworkIDByName(name)
}

func (c *Client) CreateSubnet(name, networkID, zoneID, cidr string) (string, error) {
	url := fmt.Sprintf("%s/subnets", c.VPCURL)
	payload := map[string]interface{}{
		"folderId": c.FolderID, "name": name, "networkId": networkID, "zoneId": zoneID, "v4CidrBlocks": []string{cidr},
	}
	body, status, err := c.SendRequest("POST", url, payload)
	if err != nil || status != 200 {
		return "", fmt.Errorf("err %d: %s", status, string(body))
	}
	var op OperationResponse
	json.Unmarshal(body, &op)
	if err := c.WaitForOperation(op.ID); err != nil {
		return "", err
	}
	return c.FindSubnetIDByName(name)
}

func (c *Client) FindNetworkIDByName(name string) (string, error) {
	url := fmt.Sprintf("%s/networks?folderId=%s", c.VPCURL, c.FolderID)
	body, _, _ := c.SendRequest("GET", url, nil)
	var list NetworkListResponse
	json.Unmarshal(body, &list)
	for _, n := range list.Networks {
		if n.Name == name {
			return n.ID, nil
		}
	}
	if len(list.Networks) > 0 {
		return list.Networks[0].ID, nil
	}
	return "", fmt.Errorf("not found")
}

func (c *Client) FindSubnetIDByName(name string) (string, error) {
	url := fmt.Sprintf("%s/subnets?folderId=%s", c.VPCURL, c.FolderID)
	body, _, _ := c.SendRequest("GET", url, nil)
	var list SubnetListResponse
	json.Unmarshal(body, &list)
	for _, s := range list.Subnets {
		if s.Name == name {
			return s.ID, nil
		}
	}
	return "", fmt.Errorf("not found")
}

// --- NEW NETWORK DELETION METHODS ---

func (c *Client) DeleteSubnet(id string) error {
	url := fmt.Sprintf("%s/subnets/%s", c.VPCURL, id)
	body, status, err := c.SendRequest("DELETE", url, nil)
	if err != nil || (status != 200 && status != 202) {
		return fmt.Errorf("delete subnet err: %s", string(body))
	}

	var op OperationResponse
	json.Unmarshal(body, &op)

	fmt.Printf("[TRASH_CAN] Deleting subnet %s... ", id)
	return c.WaitForOperation(op.ID)
}

func (c *Client) DeleteNetwork(id string) error {
	url := fmt.Sprintf("%s/networks/%s", c.VPCURL, id)
	body, status, err := c.SendRequest("DELETE", url, nil)
	if err != nil || (status != 200 && status != 202) {
		return fmt.Errorf("delete network err: %s", string(body))
	}

	var op OperationResponse
	json.Unmarshal(body, &op)

	fmt.Printf("[TRASH_CAN] Deleting network %s... ", id)
	return c.WaitForOperation(op.ID)
}
