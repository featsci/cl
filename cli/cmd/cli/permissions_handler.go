package main

import (
	"fmt"
	"os"
	"strings"

	"cli/internal/state"
)

// handlePermissions applies disk permissions defined in config.go to the running nodes
func handlePermissions(backend *state.Backend, clusterName, configPath, outputFile string, forks int, limit string) {
	fmt.Println("[DISK/SEC] Preparing to apply disk permissions from Config...")

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

	cfg, err := GetClusterConfig(configPath)
	if err != nil {
		fmt.Printf("[ERROR] Configuration error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("[MERGE] Overlaying 'Owner/Group/Mode' from Config onto State disks (Matching by Size)...")

	applyCount := 0

	for i := range st.Nodes {
		node := &st.Nodes[i]

		var matchedGroup *NodeGroup
		for _, grp := range cfg.Groups {
			prefix := fmt.Sprintf("%s-%s-", clusterName, grp.NamePrefix)
			if strings.HasPrefix(node.Name, prefix) {
				matchedGroup = &grp
				break
			}
		}

		if matchedGroup != nil {
			for j := range node.Disks {
				dState := &node.Disks[j]
				if dState.Bootable {
					continue
				}

				var cfgDisk *Disk
				for _, cd := range matchedGroup.Disks {
					if !cd.Bootable && cd.Size == dState.Size {
						cfgDisk = &cd
						break
					}
				}

				if cfgDisk != nil {
					if cfgDisk.Owner != "" || cfgDisk.Group != "" || cfgDisk.Mode != "" {
						dState.Owner = cfgDisk.Owner
						dState.Group = cfgDisk.Group
						dState.Mode = cfgDisk.Mode
						applyCount++
					}
				}
			}
		}
	}

	if applyCount == 0 {
		fmt.Println("[WARNING] No matching disks with explicit 'owner/group/mode' found in Config.")
	} else {
		fmt.Printf("[INFO] Configured permissions for %d disks.\n", applyCount)
	}

	bastionIP := ""
	bastionPort := 22
	for _, n := range st.Nodes {
		if n.Role == "BASTION" {
			bastionIP = n.IP
			if n.SSHPort != 0 {
				bastionPort = n.SSHPort
			}
			break
		}
	}
	if bastionIP == "" {
		fmt.Println("[ERROR] Bastion not found in state.")
		os.Exit(1)
	}

	keyPath := os.ExpandEnv("${HOME}/.ssh/clo")
	invPath := "inventory.gen.yaml"
	if outputFile != "" {
		invPath = outputFile
	}

	var nodesForInv []NodeResult
	for _, n := range st.Nodes {
		nodesForInv = append(nodesForInv, NodeResult{
			Name: n.Name, IP: n.IP, Role: n.Role, Labels: n.Labels, Taints: n.Taints, Disks: n.Disks,
			SSHPort: n.SSHPort,
		})
	}
	saveToAnsibleInventory(invPath, st.SSHUser, nodesForInv, "")

	runnerArgs := ""
	if limit != "" {
		runnerArgs = fmt.Sprintf("-l %s", limit)
	}

	fmt.Println("[GO] Launching permission update on nodes...")
	DeployAndRunKubespray(bastionIP, bastionPort, st.SSHUser, keyPath, invPath, forks, "permissions", runnerArgs, backend)
}
