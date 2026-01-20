package main

import (
	"fmt"
	"os"
	"strings"

	"cli/internal/clo"
	"cli/internal/local"
	"cli/internal/state"
)

// handleKVSecret handles the -kvsec flag
func handleKVSecret(client *clo.Client, backend *state.Backend, clusterName, inputStr string) {
	parts := strings.SplitN(inputStr, ":", 2)
	if len(parts) != 2 {
		fmt.Println("[ERROR] Format error. Use: -kvsec 'SECRET_NAME:KEY=VALUE'")
		os.Exit(1)
	}
	secretName := parts[0]

	kvParts := strings.SplitN(parts[1], "=", 2)
	if len(kvParts) != 2 {
		fmt.Println("[ERROR] Data format error. Use: 'KEY=VALUE'")
		os.Exit(1)
	}
	key := kvParts[0]
	value := kvParts[1]

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
	bastionPort := 22

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

	CreateSingleSecret(bastionIP, bastionPort, masterIP, st.SSHUser, keyPath, password, secretName, key, value)
}
