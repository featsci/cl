package clo

// AttachedTo describes which entity the IP is attached to
type AttachedTo struct {
	Entity string `json:"entity"` // "server"
	ID     string `json:"id"`
}

// AddressDetail describes one IP address in the project
type AddressDetail struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Address   string `json:"address"`
	External  bool   `json:"external"`
	Ptr       string `json:"ptr"`
	Type      string `json:"type"`
	MacAddr   string `json:"mac_addr"`
	IsPrimary bool   `json:"is_primary"`

	// Fields for checking if the IP is occupied
	AttachedTo     *AttachedTo `json:"attached_to"`
	ServerID       string      `json:"server_id"`
	LoadBalancerID string      `json:"loadbalancer_id"`
}

// ServerListResponse ...
type ServerListResponse struct {
	Count  int `json:"count"`
	Result []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"result"`
}

// ServerDetailResponse ...
type ServerDetailResponse struct {
	Result struct {
		ID        string   `json:"id"`
		Status    string   `json:"status"`
		Name      string   `json:"name"`
		Created   string   `json:"created"`
		Addresses []string `json:"addresses"`
		Storages  []struct {
			ID string `json:"id"`
		} `json:"storages"`
	} `json:"result"`
}

// CreateServerResponse ...
type CreateServerResponse struct {
	Result struct {
		ID string `json:"id"`
	} `json:"result"`
}

// ProjectAddressesResponse ...
type ProjectAddressesResponse struct {
	Count  int             `json:"count"`
	Result []AddressDetail `json:"result"`
}

// DeleteServerPayload ...
type DeleteServerPayload struct {
	ClearFstab      bool     `json:"clear_fstab"`
	DeleteVolumes   []string `json:"delete_volumes"`
	DeleteAddresses []string `json:"delete_addresses"`
}

// VolumeAttachment ...
type VolumeAttachment struct {
	ID     string `json:"id"`
	Device string `json:"device"`
}

// DiskResult ...
type DiskResult struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Size             int               `json:"size"`
	Status           string            `json:"status"`
	Type             string            `json:"storage_type"`
	Bootable         bool              `json:"bootable"`
	Created          string            `json:"created_in"`
	AttachedToServer *VolumeAttachment `json:"attached_to_server"`
}

// DiskDetailResponse ...
type DiskDetailResponse struct {
	Result DiskResult `json:"result"`
}

// DiskListResponse ...
type DiskListResponse struct {
	Count  int          `json:"count"`
	Result []DiskResult `json:"result"`
}

// DeleteVolumePayload ...
type DeleteVolumePayload struct {
	ClearFstab bool `json:"clear_fstab"`
	Force      bool `json:"force"`
}

// AttachVolumeResponse ...
type AttachVolumeResponse struct {
	Result struct {
		AttachedToServer VolumeAttachment `json:"attached_to_server"`
	} `json:"result"`
}

// --- STRUCTURES FOR LOAD BALANCER ---

// LBRule describes a rule for the load balancer
type LBRule struct {
	ExtPort int    `json:"external_protocol_port"`
	IntPort int    `json:"internal_protocol_port"`
	AddrID  string `json:"address_id"`
}

// LBHealthMonitor describes a health check
type LBHealthMonitor struct {
	Delay      int    `json:"delay"`
	MaxRetries int    `json:"max_retries"`
	Timeout    int    `json:"timeout"`
	Type       string `json:"type"`
}

// LBAddressSettings describes IP parameters for the load balancer
type LBAddressSettings struct {
	DDOSProtection *bool  `json:"ddos_protection,omitempty"`
	ID             string `json:"id,omitempty"`
}

// CreateLBRequest describes the request body for creating an LB
type CreateLBRequest struct {
	Algorithm          string            `json:"algorithm"`
	Address            LBAddressSettings `json:"address"`
	HealthMonitor      LBHealthMonitor   `json:"healthmonitor"`
	Name               string            `json:"name"`
	SessionPersistence bool              `json:"session_persistence"`
	Rules              []LBRule          `json:"rules"`
}

// CreateLBResponse describes the response when creating an LB
type CreateLBResponse struct {
	Result struct {
		ID string `json:"id"`
	} `json:"result"`
}

// LoadBalancerDetail describes one LB in the API response list
type LoadBalancerDetail struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// LoadBalancerListResponse describes the API response with a list of all load balancers
type LoadBalancerListResponse struct {
	Count  int                  `json:"count"`
	Result []LoadBalancerDetail `json:"result"`
}
