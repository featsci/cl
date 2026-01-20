package main

// YCloudConfig describes the cloud cluster configuration
type YCloudConfig struct {
	ClusterName string
	NodeGroups  []YCloudNodeGroup
}

// YCloudNodeGroup describes a single node group
type YCloudNodeGroup struct {
	Name     string
	Role     string // Logical role (e.g., system, worker)
	Replicas int    // Number of nodes
	CPU      int
	RAM      int // In GB
	DiskSize int // In GB
	PublicIP bool
	Labels   map[string]string
	Taints   []YCloudTaint
}

// YCloudTaint describes a Kubernetes Node Taint
type YCloudTaint struct {
	Key    string
	Value  string
	Effect string // NO_SCHEDULE, PREFER_NO_SCHEDULE, NO_EXECUTE
}

// GetYCloudConfig returns the desired configuration
func GetYCloudConfig() YCloudConfig {
	return YCloudConfig{
		ClusterName: "k6-load-cluster",
		NodeGroups: []YCloudNodeGroup{
			// 1. System Group (for K6 Operator, system pods, monitoring)
			{
				Name:     "system-pool",
				Role:     "system",
				Replicas: 1, // One node is enough for controllers
				CPU:      2,
				RAM:      4,
				DiskSize: 32,
				PublicIP: true, // Needed to pull images
				Labels: map[string]string{
					"role": "system",
				},
				// No Taints - standard pods will be scheduled here
			},
			// 2. Load Testing Group (K6 runners)
			{
				Name:     "k6-workers",
				Role:     "worker",
				Replicas: 2, // 2, // Powerful nodes for traffic generation
				CPU:      2,
				RAM:      4,
				DiskSize: 64,
				PublicIP: true,
				Labels: map[string]string{
					"role": "load-generator",
				},
				// Taints prevent system pods from consuming generator resources
				Taints: []YCloudTaint{
					{
						Key:    "dedicated",
						Value:  "k6",
						Effect: "NO_SCHEDULE",
					},
				},
			},
		},
	}
}
