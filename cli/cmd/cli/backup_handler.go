package main

import (
	"fmt"
	"os"
	"time"

	"cli/internal/local"
	"cli/internal/state"
)

func handleCreateBackup(backend *state.Backend, clusterName string) {
	timestamp := time.Now().Format("20060102-150405")
	backupName := fmt.Sprintf("manual-backup-debug-%s", timestamp)

	namespace := "pg"
	pgClusterName := "pg-db"
	repoName := "repo1"
	backupType := "full"

	fmt.Printf("[GO] Launching manual backup: %s\n", backupName)

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

	err = CreatePerconaBackup(bastionIP, bastionPort, masterIP, st.SSHUser, keyPath, password, namespace, backupName, pgClusterName, repoName, backupType)
	if err != nil {
		fmt.Printf("[ERROR] Failed to start backup: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("[+OK+] Backup request successfully sent!")
	fmt.Printf("[NOTE] Check status: kubectl get perconapgbackup -n %s %s\n", namespace, backupName)
}
