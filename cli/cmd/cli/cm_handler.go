package main

import (
	"cli/internal/local"
	"cli/internal/state"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// handleCreateConfigMap manages the creation of a ConfigMap from JSON
func handleCreateConfigMap(backend *state.Backend, clusterName, ns, name, jsonData string) {
	fmt.Printf("[GEAR] Processing ConfigMap '%s/%s'...\n", ns, name)

	normalizedJSON := strings.ReplaceAll(jsonData, "'", "\"")

	// 1. Convert JSON -> YAML
	var obj interface{}
	if err := json.Unmarshal([]byte(normalizedJSON), &obj); err != nil {
		fmt.Printf("[ERROR] JSON parsing error: %v\n", err)
		fmt.Printf("   Input string (after normalization): %s\n", normalizedJSON)
		os.Exit(1)
	}

	yamlBytes, err := yaml.Marshal(obj)
	if err != nil {
		fmt.Printf("[ERROR] YAML conversion error: %v\n", err)
		os.Exit(1)
	}
	yamlString := string(yamlBytes)

	fmt.Printf("[DOCUMENT] Generated YAML:\n%s\n", yamlString)

	// 2. Load state
	if backend == nil {
		fmt.Println("[ERROR] S3 Backend unavailable.")
		os.Exit(1)
	}
	fmt.Printf("[REFRESH] Loading state for cluster '%s'...\n", clusterName)
	st, err := backend.LoadState()
	if err != nil || st == nil {
		fmt.Printf("[ERROR] State loading error: %v\n", err)
		os.Exit(1)
	}

	var bastionIP, masterIP string
	bastionPort := 22 // Default

	for _, n := range st.Nodes {
		if n.Role == "BASTION" {
			bastionIP = n.IP
			if n.SSHPort != 0 {
				bastionPort = n.SSHPort
			}
		}
		if n.Role == "master" && masterIP == "" {
			masterIP = n.IP
		}
	}

	if bastionIP == "" || masterIP == "" {
		fmt.Println("[ERROR] Error: Bastion or Master IP not found in state.")
		os.Exit(1)
	}

	keyPath := os.ExpandEnv("${HOME}/.ssh/clo")
	password, _ := local.LoadPassword(clusterName)

	// 3. Create ConfigMap in K8s (Pass bastionPort!)
	dataMap := map[string]string{
		"values-switch.yaml": yamlString,
	}

	err = CreateOrUpdateConfigMap(bastionIP, bastionPort, masterIP, st.SSHUser, keyPath, password, ns, name, dataMap)
	if err != nil {
		fmt.Printf("[ERROR] ConfigMap application error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("[+OK+] ConfigMap successfully created/updated.")
}
