package main

import (
	"fmt"
	"os"
	"strings"

	"cli/internal/clo"
	"cli/internal/local"
	"cli/internal/state"
)

func handleWaitCert(client *clo.Client, backend *state.Backend, clusterName, inputStr string) {
	parts := strings.SplitN(inputStr, ":", 2)
	if len(parts) != 2 {
		fmt.Println("[ERROR] Format error. Use: -wait-cert 'NAMESPACE:CERT_NAME'")
		os.Exit(1)
	}
	namespace := parts[0]
	certName := parts[1]

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

	keyPath := os.ExpandEnv("${HOME}/.ssh/clo")
	password, _ := local.LoadPassword(clusterName)

	if err := WaitForCertificateReady(bastionIP, bastionPort, masterIP, st.SSHUser, keyPath, password, namespace, certName); err != nil {
		fmt.Printf("[ERROR] Certificate is not ready: %v\n", err)
		os.Exit(1)
	}
}
