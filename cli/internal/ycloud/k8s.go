package ycloud

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// NodeGroupSpec describes the parameters for creating a node group
type NodeGroupSpec struct {
	Name     string
	Replicas int
	CPU      int
	RAM      int64 // bytes
	DiskSize int64 // bytes
	PublicIP bool
	Labels   map[string]string
	Taints   []TaintSpec
}

// TaintSpec describes a Taint for the API payload
type TaintSpec struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

// EnsureCluster creates a Managed Kubernetes cluster if it doesn't exist
func (c *Client) EnsureCluster(name, serviceAccountID, zoneID, netID, subID string) (string, error) {
	listURL := fmt.Sprintf("%s/clusters?folderId=%s", c.BaseURL, c.FolderID)
	body, _, _ := c.SendRequest("GET", listURL, nil)
	var list ClusterListResponse
	json.Unmarshal(body, &list)

	for _, cl := range list.Clusters {
		if cl.Name == name && (cl.Status == "RUNNING" || cl.Status == "PROVISIONING") {
			fmt.Printf("[INFO] Cluster %s already exists (ID: %s)\n", name, cl.ID)
			return cl.ID, nil
		}
	}

	fmt.Printf("[T] Creating cluster '%s' (v1.32)...\n", name)
	req := CreateClusterRequest{
		FolderID:             c.FolderID,
		Name:                 name,
		NetworkID:            netID,
		ServiceAccountID:     serviceAccountID,
		NodeServiceAccountID: serviceAccountID,
		ReleaseChannel:       "STABLE",
		IPAllocationPolicy: IPAllocationPolicy{
			ClusterIPv4CIDRBlock: "172.16.0.0/16",
			ServiceIPv4CIDRBlock: "10.0.0.0/16",
		},
		MasterSpec: MasterSpec{
			Version: "1.32",
			ZonalMasterSpec: ZonalMasterSpec{
				ZoneID: zoneID,
				InternalV4AddressSpec: &InternalV4AddressSpec{
					SubnetID: subID,
				},
				ExternalV4AddressSpec: &ExternalV4AddressSpec{},
			},
		},
	}

	body, status, err := c.SendRequest("POST", c.BaseURL+"/clusters", req)
	if err != nil || status != 200 {
		return "", fmt.Errorf("create cluster error (status %d): %s", status, string(body))
	}

	var op OperationResponse
	json.Unmarshal(body, &op)

	if err := c.WaitForOperation(op.ID); err != nil {
		return "", err
	}

	return c.EnsureCluster(name, serviceAccountID, zoneID, netID, subID)
}

// EnsureNodeGroup creates or verifies a node group with specific configuration
func (c *Client) EnsureNodeGroup(clusterID, zoneID, subID string, spec NodeGroupSpec) error {
	url := fmt.Sprintf("%s/nodeGroups", c.BaseURL)

	// Prepare taints for payload
	var taintsPayload []map[string]string
	if len(spec.Taints) > 0 {
		for _, t := range spec.Taints {
			taintsPayload = append(taintsPayload, map[string]string{
				"key":    t.Key,
				"value":  t.Value,
				"effect": t.Effect,
			})
		}
	}

	// Configure Network Interface (Public IP)
	var networkInterfaceSpecs []map[string]interface{}
	if spec.PublicIP {
		networkInterfaceSpecs = []map[string]interface{}{
			{
				"subnetIds": []string{subID},
				"primaryV4AddressSpec": map[string]interface{}{
					"oneToOneNatSpec": map[string]interface{}{
						"ipVersion": "IPV4",
					},
				},
			},
		}
	} else {
		networkInterfaceSpecs = []map[string]interface{}{
			{
				"subnetIds": []string{subID},
			},
		}
	}

	payload := map[string]interface{}{
		"clusterId": clusterID,
		"name":      spec.Name,
		// Kubernetes Node Labels (Top Level)
		"nodeLabels": spec.Labels,
		// Kubernetes Node Taints (Top Level)
		"nodeTaints": taintsPayload,

		"nodeTemplate": map[string]interface{}{
			"platformId": "standard-v3",
			"resourcesSpec": map[string]interface{}{
				"memory": spec.RAM,
				"cores":  spec.CPU,
			},
			"bootDiskSpec": map[string]interface{}{
				"diskTypeId": "network-hdd",
				"diskSize":   spec.DiskSize,
			},
			"networkInterfaceSpecs": networkInterfaceSpecs,
			// labels inside nodeTemplate are for VM tags, not K8s labels.
			// We can duplicate them if you want VM tags too, otherwise leave empty.
		},
		"scalePolicy": map[string]interface{}{
			"fixedScale": map[string]interface{}{
				"size": spec.Replicas,
			},
		},
		"allocationPolicy": map[string]interface{}{
			"locations": []map[string]interface{}{
				{"zoneId": zoneID},
			},
		},
	}

	body, status, err := c.SendRequest("POST", url, payload)

	if status == 409 {
		// Group already exists.
		fmt.Printf("[INFO] Node group '%s' already exists.\n", spec.Name)
		return nil
	}
	if err != nil || status != 200 {
		return fmt.Errorf("create node group '%s' error: %s", spec.Name, string(body))
	}

	var op OperationResponse
	json.Unmarshal(body, &op)
	fmt.Printf("[T] Creating node group '%s' (CPU:%d RAM:%dGB Nodes:%d)... ", spec.Name, spec.CPU, spec.RAM/1024/1024/1024, spec.Replicas)
	return c.WaitForOperation(op.ID)
}

// GetKubeconfig retrieves the kubeconfig file content
func (c *Client) GetKubeconfig(clusterID string) ([]byte, error) {
	url := fmt.Sprintf("%s/clusters/%s", c.BaseURL, clusterID)
	body, _, err := c.SendRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	var det ClusterDetailResponse
	json.Unmarshal(body, &det)

	// Ensure cert is Base64 encoded
	caData := base64.StdEncoding.EncodeToString([]byte(det.Master.MasterAuth.ClusterCACertificate))

	configTmpl := `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: %s
    server: %s
  name: ycloud-k8s
contexts:
- context:
    cluster: ycloud-k8s
    user: ycloud-user
  name: default
current-context: default
users:
- name: ycloud-user
  user:
    token: %s
`
	return []byte(fmt.Sprintf(configTmpl, caData, det.Master.Endpoints.ExternalV4Endpoint, c.IAMToken)), nil
}

// --- DELETION & FIND METHODS ---

func (c *Client) FindClusterIDByName(name string) (string, error) {
	listURL := fmt.Sprintf("%s/clusters?folderId=%s", c.BaseURL, c.FolderID)
	body, _, err := c.SendRequest("GET", listURL, nil)
	if err != nil {
		return "", err
	}
	var list ClusterListResponse
	json.Unmarshal(body, &list)
	for _, cl := range list.Clusters {
		if cl.Name == name && cl.Status != "DELETING" {
			return cl.ID, nil
		}
	}
	return "", nil
}

func (c *Client) FindNodeGroupsByClusterID(clusterID string) ([]NodeGroup, error) {
	url := fmt.Sprintf("%s/nodeGroups?folderId=%s", c.BaseURL, c.FolderID)
	body, _, err := c.SendRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	var list NodeGroupListResponse
	json.Unmarshal(body, &list)
	var result []NodeGroup
	for _, ng := range list.NodeGroups {
		if ng.ClusterID == clusterID && ng.Status != "DELETING" {
			result = append(result, ng)
		}
	}
	return result, nil
}

func (c *Client) DeleteNodeGroup(id string) error {
	url := fmt.Sprintf("%s/nodeGroups/%s", c.BaseURL, id)
	body, status, err := c.SendRequest("DELETE", url, nil)
	if err != nil || (status != 200 && status != 202) {
		return fmt.Errorf("err: %s", string(body))
	}
	var op OperationResponse
	json.Unmarshal(body, &op)
	fmt.Printf("[TIME] Deleting group %s... ", id)
	return c.WaitForOperation(op.ID)
}

func (c *Client) DeleteCluster(id string) error {
	url := fmt.Sprintf("%s/clusters/%s", c.BaseURL, id)
	body, status, err := c.SendRequest("DELETE", url, nil)
	if err != nil || (status != 200 && status != 202) {
		return fmt.Errorf("err: %s", string(body))
	}
	var op OperationResponse
	json.Unmarshal(body, &op)
	fmt.Printf("[TIME] Deleting cluster %s... ", id)
	return c.WaitForOperation(op.ID)
}
