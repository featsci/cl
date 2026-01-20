package ycloud

// --- REQUESTS ---

type CreateClusterRequest struct {
	FolderID             string             `json:"folder_id"`
	Name                 string             `json:"name"`
	NetworkID            string             `json:"network_id"`
	ServiceAccountID     string             `json:"service_account_id"`
	NodeServiceAccountID string             `json:"node_service_account_id"`
	ReleaseChannel       string             `json:"release_channel"`
	MasterSpec           MasterSpec         `json:"master_spec"`
	IPAllocationPolicy   IPAllocationPolicy `json:"ip_allocation_policy,omitempty"`
}

type MasterSpec struct {
	Version         string          `json:"version"`
	ZonalMasterSpec ZonalMasterSpec `json:"zonal_master_spec"`
	PublicIP        bool            `json:"public_ip"`
}

type ZonalMasterSpec struct {
	ZoneID                string                 `json:"zone_id"`
	InternalV4AddressSpec *InternalV4AddressSpec `json:"internal_v4_address_spec,omitempty"`
	ExternalV4AddressSpec *ExternalV4AddressSpec `json:"external_v4_address_spec,omitempty"`
}

type InternalV4AddressSpec struct {
	SubnetID string `json:"subnet_id"`
}

type ExternalV4AddressSpec struct{}

type IPAllocationPolicy struct {
	ClusterIPv4CIDRBlock string `json:"cluster_ipv4_cidr_block,omitempty"`
	ServiceIPv4CIDRBlock string `json:"service_ipv4_cidr_block,omitempty"`
	NodeIPv4CIDRMask     int    `json:"node_ipv4_cidr_mask_size,omitempty"`
}

// --- RESPONSES ---

type OperationResponse struct {
	ID    string `json:"id"`
	Done  bool   `json:"done"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

type ClusterListResponse struct {
	Clusters []Cluster `json:"clusters"`
}

type Cluster struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type ClusterDetailResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Master struct {
		Endpoints struct {
			ExternalV4Endpoint string `json:"externalV4Endpoint"` // camelCase in REST
		} `json:"endpoints"`
		MasterAuth struct {
			ClusterCACertificate string `json:"clusterCaCertificate"` // camelCase in REST
		} `json:"masterAuth"`
	} `json:"master"`
}

type NetworkListResponse struct {
	Networks []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"networks"`
}

type SubnetListResponse struct {
	Subnets []struct {
		ID           string   `json:"id"`
		Name         string   `json:"name"`
		ZoneID       string   `json:"zoneId"`       // camelCase
		NetworkID    string   `json:"networkId"`    // camelCase
		V4CIDRBlocks []string `json:"v4CidrBlocks"` // camelCase
	} `json:"subnets"`
}

type NodeGroupListResponse struct {
	NodeGroups []NodeGroup `json:"node_groups"`
}

type NodeGroup struct {
	ID        string `json:"id"`
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
}
