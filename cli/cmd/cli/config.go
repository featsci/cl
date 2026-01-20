package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	SSHUser        string      `yaml:"ssh_user"`
	Groups         []NodeGroup `yaml:"groups"`
	LoadBalancerIP string      `yaml:"load_balancer_ip,omitempty"`
}

// InstanceConfig - settings for a specific node
type InstanceConfig struct {
	Enabled bool              `yaml:"enabled"`
	Labels  map[string]string `yaml:"labels,omitempty"` // Additional labels
}

type LBRuleConfig struct {
	ExtPort int `yaml:"ext_port"`
	IntPort int `yaml:"int_port"`
}

type NodeGroup struct {
	NamePrefix string                 `yaml:"name_prefix"`
	Role       string                 `yaml:"role"`
	Instances  map[int]InstanceConfig `yaml:"instances"`
	Flavor     Flavor                 `yaml:"flavor"`
	Disks      []Disk                 `yaml:"disks"`
	ExternalIP bool                   `yaml:"external_ip"`
	StaticIP   string                 `yaml:"static_ip,omitempty"`
	Labels     map[string]string      `yaml:"labels"`
	Taints     []string               `yaml:"taints"`
	LBRules    []LBRuleConfig         `yaml:"lb_rules,omitempty"`
}

type Flavor struct {
	RAM   int `yaml:"ram"`
	VCPUs int `yaml:"vcpus"`
}

type Disk struct {
	Size       int    `yaml:"size"`
	Bootable   bool   `yaml:"bootable"`
	Type       string `yaml:"type"`
	MountPoint string `yaml:"mount_point"`
	Owner      string `yaml:"owner,omitempty"` // OWNER (UID)
	Group      string `yaml:"group,omitempty"` // GROUP (GID)
	Mode       string `yaml:"mode,omitempty"`  // PERM (Ex, "0750")

}

// GetClusterConfig loads the config from file (if specified) or returns the default
func GetClusterConfig(configPath string) (*Config, error) {
	if configPath != "" {
		fmt.Printf("[FILE_FOLDER] Reading configuration from file: %s\n", configPath)
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("file read error: %w", err)
		}

		var cfg Config
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("YAML parsing error: %w", err)
		}
		return &cfg, nil
	}
	return getDefaultConfig(), nil
}

// getDefaultConfig returns the hardcoded configuration
func getDefaultConfig() *Config {
	lbIP := os.Getenv("CLO_LB_IP")
	return &Config{
		SSHUser:        "root",
		LoadBalancerIP: lbIP,
		Groups: []NodeGroup{
			// --- MASTERS ---
			{
				NamePrefix: "masterbig",
				Role:       "master",
				// Count: 3 -> changed to Instances
				Instances: map[int]InstanceConfig{
					1: {Enabled: true},
					2: {Enabled: true},
					3: {Enabled: true},
				},
				Flavor:     Flavor{RAM: 4, VCPUs: 2},
				ExternalIP: false,
				Disks: []Disk{
					{Size: 20, Bootable: true, Type: "storage"},
				},
			},
			// --- MASTERS LOW ---
			{
				NamePrefix: "master",
				Role:       "master",
				// Count: 3 -> changed to Instances
				Instances: map[int]InstanceConfig{
					// 1: {Enabled: true},
					// 2: {Enabled: true},
					// 3: {Enabled: true},
				},
				Flavor:     Flavor{RAM: 2, VCPUs: 2},
				ExternalIP: false,
				Disks: []Disk{
					{Size: 20, Bootable: true, Type: "storage"},
				},
			},
			// --- MONITORING 2 ---
			{
				NamePrefix: "monitoring",
				Role:       "worker",
				// Count:      1,
				Instances: map[int]InstanceConfig{
					1: {Enabled: true},
				},
				Flavor:     Flavor{RAM: 4, VCPUs: 2},
				ExternalIP: false,
				Disks: []Disk{
					{Size: 20, Bootable: true, Type: "storage"},
					{Size: 6, Bootable: false, Type: "local"},
					// {
					// 	Size:     12,
					// 	Bootable: false,
					// 	Type:     "local",
					// 	Owner:    "1000",
					// 	Group:    "1000",
					// 	Mode:     "0700",
					// },
				},
				Labels: map[string]string{
					"prometheusnode": "yesnaff",
					"pmminstance":    "num1",
				},
				Taints: []string{
					"prometheustaint=yestaint:NoSchedule",
				},
			},
			// --- POSTGRESQL ---
			{
				NamePrefix: "postgresql",
				Role:       "worker",
				// Instead of Count: 3, specify concrete IDs
				Instances: map[int]InstanceConfig{
					1: {
						Enabled: true,
						Labels: map[string]string{
							"postgresqlinstance": "num1",
							// "zone":               "ru-central1-a",
						},
					},
					2: {
						Enabled: true, // Disabled
						Labels: map[string]string{
							"postgresqlinstance": "num2",
						},
					},
					3: {
						Enabled: true,
						Labels: map[string]string{
							"postgresqlinstance": "num3",
							// "zone":               "ru-central1-b",
						},
					},
				},
				Flavor:     Flavor{RAM: 2, VCPUs: 2},
				ExternalIP: false,
				Disks: []Disk{
					{Size: 20, Bootable: true, Type: "storage"},
					{Size: 15, Bootable: false, Type: "local"},
				},
				Labels: map[string]string{
					"postgresqlnode": "yesnaff",
				},
				Taints: []string{
					"postgresqltaint=yestaint:NoSchedule",
				},
			},

			// --- CACHE (Your request) ---
			{
				NamePrefix: "cache",
				Role:       "worker",
				// Instead of Count: 3, specify concrete IDs
				Instances: map[int]InstanceConfig{
					1: {
						Enabled: true,
						Labels: map[string]string{
							"cacheinstance": "num1",
							// "zone":               "ru-central1-a",
						},
					},
					2: {
						Enabled: true, // Disabled
						Labels: map[string]string{
							"cacheinstance": "num2",
						},
					},
					3: {
						Enabled: true,
						Labels: map[string]string{
							"cacheinstance": "num3",
							// "zone":               "ru-central1-b",
						},
					},
				},
				Flavor:     Flavor{RAM: 2, VCPUs: 2},
				ExternalIP: false,
				Disks: []Disk{
					{Size: 20, Bootable: true, Type: "storage"},
					// {Size: 5, Bootable: false, Type: "local"},
					{
						Size:     5,
						Bootable: false,
						Type:     "local",
						Owner:    "1000",
						Group:    "1000",
						Mode:     "0750",
					},
				},
				Labels: map[string]string{
					"cachenode": "yesnaff",
				},
				Taints: []string{
					"cachetaint=yestaint:NoSchedule",
				},
			},
			// --- SYSPOOL ---
			{
				NamePrefix: "syspool",
				Role:       "worker",
				Instances:  map[int]InstanceConfig{
					// 1: {Enabled: true},
					// 2: {Enabled: true},
					// 3: {Enabled: true},
					// 4: {Enabled: true},
					// 5: {Enabled: true},
				},
				Flavor:     Flavor{RAM: 4, VCPUs: 2},
				ExternalIP: false,
				Disks: []Disk{
					{Size: 20, Bootable: true, Type: "storage"},
				},
			},

			// --- SYSPOOLBIG ---
			{
				NamePrefix: "syspoolbig",
				Role:       "worker",
				Instances: map[int]InstanceConfig{
					1: {Enabled: true},
					2: {Enabled: true},
					// 3: {Enabled: true},
				},
				Flavor:     Flavor{RAM: 4, VCPUs: 2},
				ExternalIP: false,
				Disks: []Disk{
					{Size: 20, Bootable: true, Type: "storage"},
				},
			},
			// --- BASTION ---
			{
				NamePrefix: "bastion",
				Role:       "BASTION",
				LBRules: []LBRuleConfig{
					{ExtPort: 2205, IntPort: 22},
				},
				Instances: map[int]InstanceConfig{
					1: {Enabled: true},
				},
				Flavor:     Flavor{RAM: 4, VCPUs: 4},
				ExternalIP: false, // true,
				// StaticIP: "2ef4fe5a-e184-48e7-97c9-6f56e9a28ccf",
				Disks: []Disk{
					{Size: 20, Bootable: true, Type: "storage"},
				},
			},
			// --- web 2 ---
			{
				NamePrefix: "web",
				Role:       "worker",
				LBRules: []LBRuleConfig{
					{ExtPort: 80, IntPort: 30080},
					{ExtPort: 443, IntPort: 30443},
				},
				Instances: map[int]InstanceConfig{
					1: {Enabled: true},
					2: {Enabled: true},
					// 3: {Enabled: true},
				},
				Flavor:     Flavor{RAM: 4, VCPUs: 2},
				ExternalIP: false,
				Disks: []Disk{
					{Size: 20, Bootable: true, Type: "storage"},
				},
				Labels: map[string]string{
					"webnode": "yesnaff",
				},
				Taints: []string{
					"webtaint=yestaint:NoSchedule",
				},
			},
		},
	}
}
