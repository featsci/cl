package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"cli/internal/local"
	"cli/internal/state"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func handleStatusRestoreDB(backend *state.Backend, clusterName, ns, pgClusterName string) {
	fmt.Printf("[SEARCH] Waiting for cluster restore '%s/%s'...\n", ns, pgClusterName)

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
		if n.Role == "master" {
			masterIP = n.IP
		}
	}

	if bastionIP == "" || masterIP == "" {
		fmt.Println("[ERROR] Bastion or Master IP not found.")
		os.Exit(1)
	}

	keyPath := os.ExpandEnv("${HOME}/.ssh/clo")
	password, _ := local.LoadPassword(clusterName)

	timeout := 20 * time.Minute
	startTime := time.Now()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	fmt.Println("[TIME] Starting monitoring. Press Ctrl+C to cancel.")

	for {
		if time.Since(startTime) > timeout {
			fmt.Println("\n[ERROR] Timeout waiting.")
			os.Exit(1)
		}

		statusMap, stateMsg, err := GetPGClusterStatus(bastionIP, bastionPort, masterIP, st.SSHUser, keyPath, password, ns, pgClusterName)

		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				fmt.Printf("\r[TIME] Object %s not created yet. Waiting...", pgClusterName)
			} else {
				fmt.Printf("\n[ERROR] API Error: %v\n", err)
			}
		} else {
			pgReady, _, _ := unstructured.NestedInt64(statusMap, "postgres", "ready")
			pgSize, _, _ := unstructured.NestedInt64(statusMap, "postgres", "size")
			msg, _, _ := unstructured.NestedString(statusMap, "message")

			if strings.EqualFold(stateMsg, "ready") {
				fmt.Printf("\n[+OK+] CLUSTER READY! (%d/%d)\n", pgReady, pgSize)
				os.Exit(0)
			}

			fmt.Printf("\r[REFRESH] Status: %-10s | Inst: %d/%d | Info: %-30s (%s)   \033[K",
				stateMsg,
				pgReady,
				pgSize,
				truncateString(msg, 30),
				time.Since(startTime).Round(time.Second))
		}

		<-ticker.C
	}
}

func truncateString(str string, num int) string {
	if len(str) > num {
		return str[0:num] + "..."
	}
	return str
}
