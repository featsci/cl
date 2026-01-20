package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"cli/internal/clo"
	"cli/internal/state"
)

func handleMarkDiskCritical(backend *state.Backend, clusterName, diskID string) {
	fmt.Printf("[SEARCH] Searching for disk %s in state '%s'...\n", diskID, clusterName)

	st, err := backend.LoadState()
	if err != nil || st == nil {
		fmt.Printf("[ERROR] State loading error: %v\n", err)
		return
	}

	found := false
	for i := range st.Nodes {
		for j := range st.Nodes[i].Disks {
			if st.Nodes[i].Disks[j].ID == diskID {
				st.Nodes[i].Disks[j].Critical = true
				st.Nodes[i].Disks[j].Updated = time.Now().Format(time.RFC3339)
				fmt.Printf("   [LOCK] Disk %s (Node: %s) marked as CRITICAL (protected from deletion).\n", diskID, st.Nodes[i].Name)
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		fmt.Println("[ERROR] Disk not found in state.")
		return
	}

	st.LastUpdated = time.Now()
	if err := backend.SaveState(*st); err != nil {
		fmt.Printf("[ERROR] Error saving state: %v\n", err)
	} else {
		fmt.Println("[SAVE] State successfully updated.")
	}
}

func handleCleanDisks(client *clo.Client, s3Backend *state.Backend, clusterName string) {
	criticalDisks := make(map[string]bool)
	if s3Backend != nil {
		fmt.Printf("[REFRESH] Loading state '%s' to check disk protection...\n", clusterName)
		st, err := s3Backend.LoadState()
		if err == nil && st != nil {
			for _, n := range st.Nodes {
				for _, d := range n.Disks {
					if d.Critical {
						criticalDisks[d.ID] = true
					}
				}
			}
		} else {
			fmt.Printf("[WARNING] State not found or loading error (%v). Disk protection might not work!\n", err)
		}
	} else {
		fmt.Println("[WARNING] S3 Backend not connected. CRITICAL disk protection IS NOT WORKING.")
	}

	fmt.Println("[SEARCH] Getting Volumes list...")
	list, err := client.GetProjectVolumes()
	if err != nil {
		fmt.Printf("Error getting volumes list: %v\n", err)
		return
	}
	if list.Count == 0 {
		fmt.Println("Volumes not found.")
		return
	}
	fmt.Printf("Found volumes: %d\n", list.Count)
	fmt.Println("-------------------------------------------------------------------------------------------------------------")
	fmt.Printf("%-36s | %-20s | %-6s | %-5s | %-8s | %s\n", "ID", "NAME", "SIZE", "BOOT", "CRITICAL", "STATUS")
	fmt.Println("-------------------------------------------------------------------------------------------------------------")
	for _, d := range list.Result {
		critMark := ""
		if criticalDisks[d.ID] {
			critMark = "[LOCK] YES"
		}
		fmt.Printf("%-36s | %-20s | %dGb   | %-5t | %-8s | %s\n", d.ID, d.Name, d.Size, d.Bootable, critMark, d.Status)
	}
	fmt.Println("-------------------------------------------------------------------------------------------------------------")
	fmt.Println("\n[WARNING] ATTENTION! You are about to delete disks.")
	fmt.Println("Enter 'DELETE-ALL' to delete ALL BOOTABLE (System) volumes.")
	fmt.Println("Enter 'DELETE-FORCE' to delete ALL volumes whatsoever (including Data).")
	fmt.Println("Or enter a specific ID to delete a single volume.")
	fmt.Print("Your choice > ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	input := strings.TrimSpace(scanner.Text())
	if input == "" {
		fmt.Println("Cancellation.")
		return
	}

	deleteVol := func(id, name string) {
		if criticalDisks[id] {
			fmt.Printf("[SHIELD] [%s] Disk is PROTECTED (Critical=true). SKIP DELETION.\n", id)
			return
		}
		fmt.Printf("Deleting volume %s (%s)... ", name, id)
		if err := client.DeleteVolume(id); err != nil {
			fmt.Printf("[ERROR] Error: %v\n", err)
		} else {
			fmt.Printf("[+OK+] Success.\n")
		}
	}

	if input == "DELETE-ALL" {
		fmt.Println("[GO] Launching cleanup (Bootable only)...")
		for _, d := range list.Result {
			if !d.Bootable {
				continue
			}
			deleteVol(d.ID, d.Name)
			time.Sleep(200 * time.Millisecond)
		}
	} else if input == "DELETE-FORCE" {
		fmt.Println("[HAZARD] Launching FULL deletion (INCLUDING DATA)...")
		for _, d := range list.Result {
			deleteVol(d.ID, d.Name)
			time.Sleep(200 * time.Millisecond)
		}
	} else {
		deleteVol(input, "Manual")
	}
}

func handleCreateLB(client *clo.Client, s3Backend *state.Backend, clusterName string, configPath string) {
	fmt.Printf("[REFRESH] [LB Mode] Loading State '%s'...\n", clusterName)

	st, err := s3Backend.LoadState()
	if err != nil || st == nil {
		fmt.Println("[ERROR] State not found or loading error.")
		return
	}

	cfg, err := GetClusterConfig(configPath)
	if err != nil {
		fmt.Printf("[ERROR] Configuration error: %v\n", err)
		return
	}

	allAddrs, err := client.GetProjectAddressesMap()
	if err != nil {
		fmt.Printf("[ERROR] Addresses API error: %v\n", err)
		return
	}

	var aggregatedRules []clo.LBRule
	groupsWithLB := 0

	fmt.Println("[GEAR] Aggregating LB rules from all groups...")

	for _, group := range cfg.Groups {
		if len(group.LBRules) == 0 {
			continue
		}
		groupsWithLB++

		var targetAddressIDs []string
		prefix := fmt.Sprintf("%s-%s-", clusterName, group.NamePrefix)

		for _, n := range st.Nodes {
			if strings.HasPrefix(n.Name, prefix) {
				detail, err := client.GetServerDetail(n.ID)
				if err != nil {
					fmt.Printf("   [WARNING] Error getting details for %s: %v\n", n.Name, err)
					continue
				}
				for _, addrID := range detail.Result.Addresses {
					if info, ok := allAddrs[addrID]; ok && !info.External {
						targetAddressIDs = append(targetAddressIDs, addrID)
						break
					}
				}
			}
		}

		if len(targetAddressIDs) == 0 {
			fmt.Printf("   [WARNING] Group '%s' has LB rules defined but no active nodes found. Skipping rules.\n", group.NamePrefix)
			continue
		}

		for _, rule := range group.LBRules {
			for _, addrID := range targetAddressIDs {
				aggregatedRules = append(aggregatedRules, clo.LBRule{
					ExtPort: rule.ExtPort,
					IntPort: rule.IntPort,
					AddrID:  addrID,
				})
			}
			fmt.Printf("   + Rule Group '%s': :%d -> :%d (Targets: %d)\n", group.NamePrefix, rule.ExtPort, rule.IntPort, len(targetAddressIDs))
		}
	}

	if len(aggregatedRules) == 0 {
		fmt.Println("[STAR] No LB rules found in config or no active nodes. Nothing to create.")
		return
	}

	fmt.Println("---------------------------------------------------")

	var lbAddressSettings clo.LBAddressSettings

	// --- FIX: SEARCH FOR SPECIFIC IP ---
	if cfg.LoadBalancerIP != "" {
		fmt.Printf("[SEARCH] Looking for specific IP: %s...\n", cfg.LoadBalancerIP)
		foundSpecific := false
		for id, addr := range allAddrs {
			if addr.Address == cfg.LoadBalancerIP {
				fmt.Printf("   [RECYCLE] Found requested IP. ID: %s (Status: %s)\n", id, addr.Status)
				lbAddressSettings.ID = id
				foundSpecific = true
				break
			}
		}

		if !foundSpecific {
			fmt.Printf("[ERROR] Requested IP %s NOT FOUND in project addresses!\n", cfg.LoadBalancerIP)
			fmt.Println("   Please check the IP or remove 'load_balancer_ip' from config to auto-allocate.")
			return
		}
	} else {
		fmt.Println("[SEARCH] Searching for ANY free external IP...")
		freeIP, err := client.FindAvailableExternalIP()
		if err != nil {
			fmt.Printf("   [WARNING] Search error: %v. New IP will be created.\n", err)
			ddos := false
			lbAddressSettings.DDOSProtection = &ddos
		} else if freeIP != "" {
			fmt.Printf("   [RECYCLE] Found FREE address ID: %s.\n", freeIP)
			lbAddressSettings.ID = freeIP
		} else {
			fmt.Println("   [STAR] NO free addresses. A NEW IP will be created.")
			ddos := false
			lbAddressSettings.DDOSProtection = &ddos
		}
	}
	// -----------------------------------

	lbName := fmt.Sprintf("%s-main-lb", clusterName)
	req := clo.CreateLBRequest{
		Name:      lbName,
		Algorithm: "ROUND_ROBIN",
		Address:   lbAddressSettings,
		HealthMonitor: clo.LBHealthMonitor{
			Delay:      80,
			MaxRetries: 5,
			Timeout:    15,
			Type:       "TCP",
		},
		SessionPersistence: false,
		Rules:              aggregatedRules,
	}

	fmt.Printf("[GO] Creating Unified Load Balancer '%s' with %d rules...\n", lbName, len(aggregatedRules))
	lbID, err := client.CreateLoadBalancer(req)
	if err != nil {
		fmt.Printf("[ERROR] Failed to create LB: %v\n", err)
		return
	} else {
		fmt.Printf("[+OK+] Unified Load Balancer created! ID: %s\n", lbID)
	}

	// --- UPDATE STATE LOGIC ---
	fmt.Println("[SYNC] Updating State with Load Balancer IP and Ports...")

	lbIP := ""
	if lbAddressSettings.ID != "" {
		if info, ok := allAddrs[lbAddressSettings.ID]; ok {
			lbIP = info.Address
		}
	} else if cfg.LoadBalancerIP != "" {
		lbIP = cfg.LoadBalancerIP
	}

	if lbIP != "" {
		stateModified := false

		for _, group := range cfg.Groups {
			if len(group.LBRules) == 0 {
				continue
			}
			// -------------------------------

			var sshExtPort int
			for _, r := range group.LBRules {
				if r.IntPort == 22 {
					sshExtPort = r.ExtPort
					break
				}
			}

			prefix := fmt.Sprintf("%s-%s-", clusterName, group.NamePrefix)
			for i := range st.Nodes {
				node := &st.Nodes[i]
				if strings.HasPrefix(node.Name, prefix) {
					if node.IP != lbIP {
						fmt.Printf("   [UPDATE] Node %s: IP %s -> %s (LB)\n", node.Name, node.IP, lbIP)
						node.IP = lbIP
						stateModified = true
					}
					if sshExtPort != 0 && node.SSHPort != sshExtPort {
						fmt.Printf("   [UPDATE] Node %s: SSH Port %d -> %d (LB)\n", node.Name, node.SSHPort, sshExtPort)
						node.SSHPort = sshExtPort
						stateModified = true
					}
				}
			}
		}

		if stateModified {
			st.LastUpdated = time.Now()
			if err := s3Backend.SaveState(*st); err != nil {
				fmt.Printf("[ERROR] Failed to save updated state: %v\n", err)
			} else {
				fmt.Println("[SAVE] State successfully updated with LB details.")
			}
		} else {
			fmt.Println("[STAR] State is up to date.")
		}
	} else {
		fmt.Println("[WARNING] Could not determine LB IP, skipping state update.")
	}

	fmt.Println("\n[TIME] IP address will appear in the control panel in a few seconds.")
}
func handleAttachDisks(client *clo.Client, s3Backend *state.Backend, clusterName string) {
	fmt.Printf("[REFRESH] [Attach Mode] Loading State '%s'...\n", clusterName)
	st, err := s3Backend.LoadState()
	if err != nil || st == nil {
		fmt.Println("[ERROR] State not found.")
		return
	}

	fmt.Println("[SEARCH] Searching for detached disks in the cloud...")

	allVolumes, err := client.GetProjectVolumes()
	if err != nil {
		fmt.Printf("[ERROR] API error: %v\n", err)
		return
	}

	cloudVolMap := make(map[string]clo.DiskResult)
	for _, v := range allVolumes.Result {
		cloudVolMap[v.ID] = v
	}

	attachedCount := 0
	stateChanged := false

	for i := range st.Nodes {
		node := &st.Nodes[i]
		if node.ID == "" {
			continue
		}

		for j := range node.Disks {
			disk := &node.Disks[j]

			if disk.Bootable || disk.ID == "" {
				continue
			}

			if volInfo, exists := cloudVolMap[disk.ID]; exists {
				if volInfo.Status == "AVAILABLE" {
					fmt.Printf("   [SAVE_DISK] [%s] Found detached disk %s (%dGb). Attaching to server %s...\n", node.Name, disk.ID, disk.Size, node.ID)

					devicePath, err := client.AttachVolume(disk.ID, node.ID)
					if err != nil {
						fmt.Printf("      [ERROR] Attachment error: %v\n", err)
					} else {
						fmt.Printf("      [+OK+] Success! Device: %s\n", devicePath)
						disk.Device = devicePath
						disk.Updated = time.Now().Format(time.RFC3339)
						stateChanged = true
						attachedCount++
					}
				} else if volInfo.Status == "IN_USE" {
					if volInfo.AttachedToServer != nil && volInfo.AttachedToServer.ID == node.ID {
						if disk.Device != volInfo.AttachedToServer.Device {
							fmt.Printf("      [NOTE] [%s] Updating Device path for disk %s: %s -> %s\n", node.Name, disk.ID, disk.Device, volInfo.AttachedToServer.Device)
							disk.Device = volInfo.AttachedToServer.Device
							stateChanged = true
						}
					} else {
						fmt.Printf("      [WARNING] [%s] Disk %s is attached to ANOTHER server (%s)!\n", node.Name, disk.ID, volInfo.AttachedToServer.ID)
					}
				}
			} else {
				fmt.Printf("      [QUESTION] [%s] Disk %s from state NOT FOUND in cloud.\n", node.Name, disk.ID)
			}
		}
	}

	if attachedCount == 0 && !stateChanged {
		fmt.Println("[STAR] No action (all disks in place).")
	} else {
		if attachedCount > 0 {
			fmt.Println("[TIME] Waiting for operations to complete...")
			time.Sleep(5 * time.Second)
		}
		if stateChanged {
			fmt.Println("[SAVE] Saving updated state (Device paths)...")
			if err := s3Backend.SaveState(*st); err != nil {
				fmt.Printf("[ERROR] Error saving state: %v\n", err)
			} else {
				fmt.Println("[+OK+] State successfully updated.")
			}
		}
	}
}

func handleCreate(client *clo.Client, nameSuffix string) {
	fmt.Printf("Launching single server creation with suffix: %s...\n", nameSuffix)
	payload := map[string]interface{}{"name": "test-server-" + nameSuffix, "flavor": map[string]interface{}{"ram": 2, "vcpus": 1, "cpu_type": "SHARED"}, "storages": []map[string]interface{}{{"bootable": true, "size": 10, "storage_type": "storage"}}, "addresses": []map[string]interface{}{{"ddos_protection": false, "external": true, "version": 4, "bandwidth_max_mbps": 1024}}, "image": "45705da5-3b52-4e75-a0c6-7d4cd0911830", "keypairs": []string{"dd6b1251-ceeb-4fba-afbc-aa34cc4a6990", "92fb9ff8-9396-455f-9aed-a6482fc54ce6"}}
	serverID, err := client.CreateServer(payload)
	if err != nil {
		fmt.Printf("Creation error: %v\n", err)
		return
	}
	fmt.Printf("Server created, ID: %s. Waiting for readiness...\n", serverID)
	finalStatus, addrIDs, volIDs, err := client.WaitForStatus(serverID, []string{"ACTIVE", "RUNNING"}, 60, 5*time.Second)
	if err != nil {
		fmt.Printf("Wait error (%v). Attempting to delete resources...\n", err)
		cleanupPayload := clo.DeleteServerPayload{ClearFstab: true, DeleteVolumes: volIDs, DeleteAddresses: addrIDs}
		if delErr := client.DeleteServer(serverID, cleanupPayload); delErr != nil {
			fmt.Printf("Critical error: failed to delete server: %v\n", delErr)
		}
		return
	}
	fmt.Printf("Server is ready! Status: %s\n", finalStatus)
	printIPDetails(client, addrIDs)
	pass, err := clo.GenerateRandomPassword()
	if err != nil {
		fmt.Printf("Password generation error: %v\n", err)
		return
	}
	err = client.SetServerPassword(serverID, pass)
	if err != nil {
		fmt.Printf("Password setting error: %v\n", err)
	} else {
		fmt.Printf("PASSWORD: %s\n", pass)
	}
}

func handleDelete(client *clo.Client, serverID string) {
	fmt.Printf("Preparing to delete server %s...\n", serverID)

	detail, err := client.GetServerDetail(serverID)
	var volIDsToDelete, addrIDsToDelete []string

	if err != nil {
		fmt.Printf("Warning: failed to get server details (%v). Deleting VM only.\n", err)
	} else {
		allAddrs, addrErr := client.GetProjectAddressesMap()
		if addrErr != nil {
			fmt.Printf("Warning: failed to get IP addresses list (%v)\n", addrErr)
		} else {
			for _, addrID := range detail.Result.Addresses {
				if addrInfo, ok := allAddrs[addrID]; ok && !addrInfo.External {
					addrIDsToDelete = append(addrIDsToDelete, addrID)
				} else {
					fmt.Printf("   [SHIELD] External IP %s (%s) will be preserved.\n", addrInfo.Address, addrID)
				}
			}
		}

		for _, s := range detail.Result.Storages {
			volInfo, vErr := client.GetVolumeDetail(s.ID)
			if vErr == nil {
				if volInfo.Bootable {
					volIDsToDelete = append(volIDsToDelete, s.ID)
				} else {
					fmt.Printf("   [SAVE_DISK] Disk %s (Data) is being preserved.\n", s.ID)
				}
			} else {
				fmt.Printf("   [WARNING] Error getting info about disk %s, skipping deletion.\n", s.ID)
			}
		}

		fmt.Printf("Found resources -> Addresses to delete: %v, Volumes to delete: %v\n", addrIDsToDelete, volIDsToDelete)
	}

	payload := clo.DeleteServerPayload{
		ClearFstab:      true,
		DeleteVolumes:   volIDsToDelete,
		DeleteAddresses: addrIDsToDelete,
	}

	if err := client.DeleteServer(serverID, payload); err != nil {
		fmt.Printf("Deletion error: %v\n", err)
	} else {
		fmt.Printf("Server %s successfully deleted.\n", serverID)
	}
}

func printIPDetails(client *clo.Client, addrIDs []string) {
	allAddrs, err := client.GetProjectAddressesMap()
	if err != nil {
		return
	}
	fmt.Println("--- IP Addresses ---")
	for _, id := range addrIDs {
		if info, ok := allAddrs[id]; ok {
			fmt.Printf("IP: %s (Ext: %t)\n", info.Address, info.External)
		}
	}
}

func handleCleanAll(client *clo.Client) {
	fmt.Println("[WARNING] ATTENTION! You are about to delete ALL servers, load balancers, and NON-EXTERNAL IP addresses in the project.")
	fmt.Print("Are you sure? Enter 'yes' to confirm: ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	if strings.TrimSpace(scanner.Text()) != "yes" {
		fmt.Println("Cancellation.")
		return
	}

	fmt.Println("\n[KNIFE] Starting full cleanup...")

	// --- 1. Server Deletion ---
	fmt.Println("\n[BOO] Deleting servers...")
	list, err := client.GetServersList()
	if err != nil {
		fmt.Printf("  [ERROR] Error getting server list: %v\n", err)
	} else if list.Count == 0 {
		fmt.Println("  [STAR] Servers not found.")
	} else {
		fmt.Printf("  Found servers: %d\n", list.Count)
		allAddrs, _ := client.GetProjectAddressesMap()
		for _, srv := range list.Result {
			fmt.Printf("--- Deleting server [%s] (%s) ---\n", srv.Name, srv.ID)
			detail, err := client.GetServerDetail(srv.ID)
			var payload clo.DeleteServerPayload
			if err != nil {
				fmt.Printf("  [WARNING] Failed to get details: %v. Simple deletion.\n", err)
				payload = clo.DeleteServerPayload{ClearFstab: false}
			} else {
				var volIDsToDelete, addrIDsToDelete []string
				for _, st := range detail.Result.Storages {
					volIDsToDelete = append(volIDsToDelete, st.ID)
				}
				if allAddrs != nil {
					for _, addrID := range detail.Result.Addresses {
						if addrInfo, ok := allAddrs[addrID]; ok && !addrInfo.External {
							addrIDsToDelete = append(addrIDsToDelete, addrID)
						}
					}
				}
				fmt.Printf("  Resources to delete -> IP: %v, Disks: %v\n", addrIDsToDelete, volIDsToDelete)
				payload = clo.DeleteServerPayload{
					ClearFstab:      true,
					DeleteVolumes:   volIDsToDelete,
					DeleteAddresses: addrIDsToDelete,
				}
			}
			if err := client.DeleteServer(srv.ID, payload); err != nil {
				fmt.Printf("  [ERROR] Error deleting server: %v\n", err)
			} else {
				fmt.Printf("  [+OK+] Server sent for deletion.\n")
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	// --- 2. Load Balancers Deletion (CORRECTED) ---
	fmt.Println("\n[BOO] Deleting Load Balancers...")
	lbList, err := client.GetLoadBalancers()
	if err != nil {
		fmt.Printf("  [ERROR] Error getting load balancers list: %v\n", err)
	} else if lbList.Count == 0 {
		fmt.Println("  [STAR] Load balancers not found.")
	} else {
		fmt.Printf("  Found load balancers: %d\n", lbList.Count)
		for _, lb := range lbList.Result {
			fmt.Printf("   Deleting load balancer %s (ID: %s)... ", lb.Name, lb.ID)
			if err := client.DeleteLoadBalancer(lb.ID); err != nil {
				fmt.Printf("[ERROR] Error: %v\n", err)
			} else {
				fmt.Printf("[+OK+] Deleted.\n")
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	// --- 3. Cleanup remaining IP addresses ---
	fmt.Println("\n[BROOM] Checking remaining IP addresses...")
	addrs, err := client.GetProjectAddressesMap()
	if err != nil {
		fmt.Printf("Error getting addresses list: %v\n", err)
		return
	}
	deletedCount := 0
	for id, addr := range addrs {
		if addr.External {
			fmt.Printf("[SHIELD] Skipping external address %s (%s).\n", addr.Address, id)
			continue
		}
		fmt.Printf("Deleting address %s (%s)...\n", addr.Address, id)
		if err := client.DeleteAddress(id); err != nil {
			fmt.Printf("  [WARNING] (Skip) Failed to delete: %v\n", err)
		} else {
			fmt.Println("  [+OK+] Deleted.")
			deletedCount++
		}
	}

	fmt.Printf("\n[FINISH_FLAG] Cleanup finished. Deleted extra addresses: %d\n", deletedCount)
}
