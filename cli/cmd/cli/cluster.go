package main

import (
	"bufio"
	"fmt"
	"html/template"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"cli/internal/clo"
	"cli/internal/local"
	"cli/internal/state"
)

type NodeResult struct {
	Name      string
	Role      string
	ID        string
	IP        string
	SSHPort   int
	AddressID string
	Err       error
	Labels    map[string]string
	Taints    []string
	Disks     []state.DiskState
	IsNew     bool
	Created   string
}

type InventoryData struct {
	User  string
	Nodes []NodeResult
}

func askForConfirmation(msg string) bool {
	fmt.Printf("%s [yes/N]: ", msg)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	input := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return input == "yes"
}

// --- UPDATED SIGNATURE: added noCheck bool ---
func handleClusterCreateFromCode(client *clo.Client, inventoryPath string, s3Backend *state.Backend, clusterName string, force bool, manualPassword string, configPath string, deleteFromCloud bool, attachDisks bool, noCheck bool) {
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime).Round(time.Second)
		fmt.Printf("\n[TIMER] Execution time (Cluster): %v\n", duration)
	}()
	cfg, err := GetClusterConfig(configPath)
	if err != nil {
		fmt.Printf("[ERROR] Configuration error: %v\n", err)
		return
	}

	fmt.Println("[CLOUD] Getting list of servers (API)...")
	allServersList, err := client.GetServersList()
	cloudServerMap := make(map[string]string)
	if err == nil {
		for _, srv := range allServersList.Result {
			cloudServerMap[srv.Name] = srv.ID
		}
	} else {
		fmt.Printf("[WARNING] API Error: %v\n", err)
	}

	aliveNodesMap := make(map[string]state.NodeState)
	var existingState *state.ClusterState

	if s3Backend != nil {
		fmt.Printf("[SEARCH] Loading State '%s'...\n", clusterName)
		existingState, err = s3Backend.LoadState()
		if err == nil && existingState != nil && len(existingState.Nodes) > 0 {
			if force {
				fmt.Println("[WARNING] -force: Ignoring state.")
			} else {
				fmt.Println("[+++] Verifying State against Cloud API...")
				for _, n := range existingState.Nodes {
					ip, addrID, disks, created, fetchErr := fetchNodeDetails(client, n.ID, n.Disks, nil)
					if fetchErr != nil {
						fmt.Printf("   [x] Node '%s' lost in the cloud.\n", n.Name)
						continue
					}

					finalIP := ip
					if n.SSHPort != 0 && n.IP != ip {
						finalIP = n.IP
					}

					aliveNodesMap[n.Name] = state.NodeState{
						Name: n.Name, Role: n.Role, ID: n.ID, IP: finalIP, SSHPort: n.SSHPort, AddressID: addrID, Labels: n.Labels, Taints: n.Taints, Disks: disks, Created: created, Updated: time.Now().Format(time.RFC3339),
					}
				}
			}
		}
	}

	currentPassword := manualPassword
	if currentPassword == "" {
		if loaded, err := local.LoadPassword(clusterName); err == nil && loaded != "" {
			currentPassword = loaded
			fmt.Println("[KEY] Found locally saved password.")
		}
	}
	if currentPassword == "" {
		genPass, _ := clo.GenerateRandomPassword()
		currentPassword = genPass
	}

	var finalNodes []NodeResult
	var nodesToCreate []struct {
		Name         string
		Group        NodeGroup
		OldDisks     []state.DiskState
		MergedLabels map[string]string
	}

	for _, group := range cfg.Groups {
		for i, instCfg := range group.Instances {
			if !instCfg.Enabled {
				continue
			}
			mergedLabels := make(map[string]string)
			for k, v := range group.Labels {
				mergedLabels[k] = v
			}
			for k, v := range instCfg.Labels {
				mergedLabels[k] = v
			}
			nodeName := fmt.Sprintf("%s-%s-%d", clusterName, group.NamePrefix, i)
			if existing, ok := aliveNodesMap[nodeName]; ok {
				finalNodes = append(finalNodes, NodeResult{
					Name: existing.Name, Role: group.Role, Labels: mergedLabels, Taints: group.Taints,
					ID: existing.ID, IP: existing.IP, SSHPort: existing.SSHPort, AddressID: existing.AddressID, Disks: existing.Disks, IsNew: false, Created: existing.Created,
				})
				continue
			}
			if realID, exists := cloudServerMap[nodeName]; exists {
				fmt.Printf("[WARNING] Adopting '%s' (Import)...\n", nodeName)
				ip, addrID, disks, created, err := fetchNodeDetails(client, realID, nil, group.Disks)
				if err != nil {
					continue
				}
				finalNodes = append(finalNodes, NodeResult{
					Name: nodeName, Role: group.Role, ID: realID, IP: ip, AddressID: addrID,
					Labels: mergedLabels, Taints: group.Taints, Disks: disks, IsNew: false, Created: created,
				})
				continue
			}
			var oldDisks []state.DiskState
			if existingState != nil {
				for _, n := range existingState.Nodes {
					if n.Name == nodeName {
						oldDisks = n.Disks
						break
					}
				}
			}
			nodesToCreate = append(nodesToCreate, struct {
				Name         string
				Group        NodeGroup
				OldDisks     []state.DiskState
				MergedLabels map[string]string
			}{nodeName, group, oldDisks, mergedLabels})
		}
	}

	if len(nodesToCreate) > 0 {
		maxConcurrency := 5
		fmt.Printf("\n[T] Creating nodes: %d (concurrency: %d)...\n", len(nodesToCreate), maxConcurrency)
		results := make(chan NodeResult, len(nodesToCreate))
		var wg sync.WaitGroup
		sem := make(chan struct{}, maxConcurrency)
		for _, item := range nodesToCreate {
			wg.Add(1)
			go func(itm struct {
				Name         string
				Group        NodeGroup
				OldDisks     []state.DiskState
				MergedLabels map[string]string
			}) {
				sem <- struct{}{}
				defer func() { <-sem }()
				createNodeAsync(&wg, client, itm.Name, itm.Group, itm.OldDisks, itm.MergedLabels, currentPassword, attachDisks, results)
			}(item)
		}
		go func() { wg.Wait(); close(results) }()
		for res := range results {
			if res.Err != nil {
				fmt.Printf("[ERROR] [%s] Error: %v\n", res.Name, res.Err)
			} else {
				fmt.Printf("[+OK+] [%s] Created (%s)\n", res.Name, res.IP)
				finalNodes = append(finalNodes, res)
			}
		}
	} else {
		fmt.Println("\n[CELEBRATION] All nodes (from config) are in order.")
	}

	for name, existing := range aliveNodesMap {
		foundInConfig := false
		for _, fn := range finalNodes {
			if fn.Name == name {
				foundInConfig = true
				break
			}
		}
		if !foundInConfig {
			fmt.Printf("\n[TRASH_CAN] EXTRA NODE DETECTED: '%s' (ID: %s)\n", name, existing.ID)
			if deleteFromCloud {
				if askForConfirmation(fmt.Sprintf("[WARNING] Delete server '%s'?", name)) {
					fmt.Printf("   [EXPLOSION] DELETING SERVER...\n")
					handleDelete(client, existing.ID)
				}
			} else {
				fmt.Println("   [INFO] -delnodes disabled. State only.")
			}
		}
	}

	// UPDATED: Pass noCheck flag
	runPostActions(s3Backend, inventoryPath, cfg.SSHUser, clusterName, currentPassword, finalNodes, noCheck)
}

// --- UPDATED SIGNATURE: added noCheck bool ---
func runPostActions(s3Backend *state.Backend, inventoryPath, sshUser, clusterName, password string, nodes []NodeResult, noCheck bool) {
	if s3Backend != nil {
		fmt.Printf("\n[CLOUD] Syncing State to S3...\n")
		var stateNodes []state.NodeState
		now := time.Now().Format(time.RFC3339)
		for _, n := range nodes {
			cr := n.Created
			if cr == "" {
				cr = now
			}
			stateNodes = append(stateNodes, state.NodeState{
				Name: n.Name, Role: n.Role, ID: n.ID, IP: n.IP, SSHPort: n.SSHPort, AddressID: n.AddressID, Labels: n.Labels, Taints: n.Taints,
				Disks: n.Disks, Created: cr, Updated: now,
			})
		}
		newState := state.ClusterState{Version: "1.9", LastUpdated: time.Now(), SSHUser: sshUser, Nodes: stateNodes}
		if err := s3Backend.SaveState(newState); err != nil {
			fmt.Printf("[ERROR] Save error: %v\n", err)
		} else {
			fmt.Println("[SAVE] State updated.")
		}
	}
	if inventoryPath != "" {
		saveToAnsibleInventory(inventoryPath, sshUser, nodes, "")
	}

	// --- FIX: CHECK FLAG ---
	if !noCheck && len(nodes) > 0 {
		fmt.Println("\n[GO] Checking accessibility (TCP/SSH)...")
		var bastionIP string
		bastionPort := 22

		for _, n := range nodes {
			if n.Role == "BASTION" {
				bastionIP = n.IP
				if n.SSHPort != 0 {
					bastionPort = n.SSHPort
				}
				break
			}
		}

		sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
		for _, node := range nodes {
			fmt.Printf("Host: %-30s | IP: %-15s | ", node.Name, node.IP)
			if err := CheckPortConnection(node.IP, bastionIP, bastionPort, password); err != nil {
				fmt.Printf("[ERROR] DOWN (%v)\n", err)
			} else {
				fmt.Printf("[+OK+] UP\n")
			}
		}
	} else if noCheck {
		fmt.Println("\n[SKIP] SSH check disabled via -nocheck flag.")
	}
}

func createNodeAsync(wg *sync.WaitGroup, client *clo.Client, name string, group NodeGroup, oldDisks []state.DiskState, labels map[string]string, password string, attachDisks bool, ch chan<- NodeResult) {
	defer wg.Done()
	const MaxRetries = 3
	var lastErr error

	allProjectVolumes, _ := client.GetProjectVolumes()
	var availableVolumes []clo.DiskResult
	if allProjectVolumes != nil {
		for _, v := range allProjectVolumes.Result {
			if v.Status == "AVAILABLE" {
				availableVolumes = append(availableVolumes, v)
			}
		}
	}

	for attempt := 1; attempt <= MaxRetries; attempt++ {
		if attempt > 1 {
			time.Sleep(10 * time.Second)
		}
		fmt.Printf("... [%s] (Attempt %d) Preparation...\n", name, attempt)

		var storagesPayload []map[string]interface{}
		var disksToAttach []string
		usedVolIDs := make(map[string]bool)

		for _, dConf := range group.Disks {
			if dConf.Bootable {
				t := dConf.Type
				if t == "" {
					t = "storage"
				}
				storagesPayload = append(storagesPayload, map[string]interface{}{"bootable": true, "storage_type": t, "size": dConf.Size})
				continue
			}
			foundDiskID := ""
			for _, oldD := range oldDisks {
				if oldD.Bootable || oldD.Size != dConf.Size {
					continue
				}
				for _, av := range availableVolumes {
					if av.ID == oldD.ID && !usedVolIDs[av.ID] {
						foundDiskID = av.ID
						usedVolIDs[av.ID] = true
						break
					}
				}
				if foundDiskID != "" {
					break
				}
			}
			if foundDiskID == "" {
				for _, av := range availableVolumes {
					if !usedVolIDs[av.ID] && av.Size == dConf.Size {
						foundDiskID = av.ID
						usedVolIDs[av.ID] = true
						break
					}
				}
			}
			if foundDiskID != "" {
				disksToAttach = append(disksToAttach, foundDiskID)
			} else {
				t := dConf.Type
				if t == "" {
					t = "storage"
				}
				storagesPayload = append(storagesPayload, map[string]interface{}{"bootable": false, "storage_type": t, "size": dConf.Size})
			}
		}

		var addressesPayload []map[string]interface{}
		if group.ExternalIP {
			useIP := group.StaticIP
			if useIP == "" {
				useIP, _ = client.FindAvailableExternalIP()
			}
			if useIP != "" {
				addressesPayload = []map[string]interface{}{{"address_id": useIP}}
			} else {
				addressesPayload = []map[string]interface{}{{"ddos_protection": false, "external": true, "version": 4, "bandwidth_max_mbps": 1024}}
			}
		} else {
			addressesPayload = []map[string]interface{}{{"ddos_protection": false, "external": false, "version": 4, "bandwidth_max_mbps": 1024}}
		}

		payload := map[string]interface{}{
			"name":      name,
			"flavor":    map[string]interface{}{"ram": group.Flavor.RAM, "vcpus": group.Flavor.VCPUs, "cpu_type": "SHARED"},
			"storages":  storagesPayload,
			"addresses": addressesPayload,
			"image":     "389d732c-a53c-4566-984e-e01a7617ff25",
			"keypairs":  []string{"dd6b1251-ceeb-4fba-afbc-aa34cc4a6990", "92fb9ff8-9396-455f-9aed-a6482fc54ce6"},
		}

		id, err := client.CreateServer(payload)
		if err != nil {
			fmt.Printf("   [ERROR] [%s] Create API Error: %v\n", name, err)
			lastErr = err
			continue
		}

		status, _, volIDs, err := client.WaitForStatus(id, []string{"ACTIVE", "RUNNING"}, 60, 10*time.Second)
		if err != nil {
			fmt.Printf("   [WARNING] [%s] Failed (Status: %s). Deleting...\n", name, status)
			client.DeleteServer(id, clo.DeleteServerPayload{ClearFstab: true, DeleteVolumes: volIDs})
			lastErr = err
			time.Sleep(15 * time.Second)
			continue
		}

		if len(disksToAttach) > 0 {
			for _, volID := range disksToAttach {
				client.AttachVolume(volID, id)
			}
			time.Sleep(15 * time.Second)
		}

		finalIP, finalAddrID, createdDisks, createdDate, err := fetchNodeDetails(client, id, oldDisks, group.Disks)
		if err != nil {
			fmt.Printf("[WARNING] Details error: %v\n", err)
		}

		client.SetServerPassword(id, password)

		ch <- NodeResult{
			Name: name, Role: group.Role, ID: id, IP: finalIP, AddressID: finalAddrID,
			Labels: labels, Taints: group.Taints, IsNew: true, Disks: createdDisks, Created: createdDate,
		}
		return
	}
	ch <- NodeResult{Name: name, Err: fmt.Errorf("failed after %d attempts: %v", MaxRetries, lastErr)}
}

func fetchNodeDetails(client *clo.Client, serverID string, oldDisks []state.DiskState, configDisks []Disk) (string, string, []state.DiskState, string, error) {
	detail, err := client.GetServerDetail(serverID)
	if err != nil {
		return "", "", nil, "", err
	}

	finalIP := "unknown"
	finalAddressID := ""
	allAddrs, _ := client.GetProjectAddressesMap()

	for _, addrID := range detail.Result.Addresses {
		if info, ok := allAddrs[addrID]; ok {
			if info.External {
				finalIP = info.Address
				finalAddressID = addrID
				break
			}
		}
	}
	if finalIP == "unknown" && len(detail.Result.Addresses) > 0 {
		finalAddressID = detail.Result.Addresses[0]
		if info, ok := allAddrs[finalAddressID]; ok {
			finalIP = info.Address
		}
	}

	var disks []state.DiskState
	allVolumes, _ := client.GetProjectVolumes()
	criticalMap := make(map[string]bool)
	for _, d := range oldDisks {
		if d.Critical {
			criticalMap[d.ID] = true
		}
	}

	if allVolumes != nil {
		for _, vol := range allVolumes.Result {
			if vol.AttachedToServer != nil && vol.AttachedToServer.ID == serverID {
				mountPoint := ""
				for _, old := range oldDisks {
					if old.ID == vol.ID && old.MountPoint != "" {
						mountPoint = old.MountPoint
						break
					}
				}
				if mountPoint == "" && !vol.Bootable && configDisks != nil {
					for _, cd := range configDisks {
						if !cd.Bootable && cd.MountPoint != "" && cd.Size == vol.Size {
							mountPoint = cd.MountPoint
							break
						}
					}
				}
				if mountPoint == "" && !vol.Bootable {
					parts := strings.Split(vol.AttachedToServer.Device, "/")
					mountPoint = fmt.Sprintf("/mnt/disks/%s", parts[len(parts)-1])
				}
				disks = append(disks, state.DiskState{
					ID: vol.ID, Size: vol.Size, Type: vol.Type, Bootable: vol.Bootable, Device: vol.AttachedToServer.Device,
					MountPoint: mountPoint, Critical: criticalMap[vol.ID], Created: vol.Created, Updated: time.Now().Format(time.RFC3339),
				})
			}
		}
		for _, old := range oldDisks {
			if old.Bootable {
				continue
			}
			found := false
			for _, d := range disks {
				if d.ID == old.ID {
					found = true
					break
				}
			}
			if !found {
				for _, vol := range allVolumes.Result {
					if vol.ID == old.ID {
						disks = append(disks, state.DiskState{
							ID: vol.ID, Size: vol.Size, Type: vol.Type, Bootable: false, Device: "",
							MountPoint: old.MountPoint, Critical: old.Critical, Created: vol.Created, Updated: time.Now().Format(time.RFC3339),
						})
						break
					}
				}
			}
		}
	}
	return finalIP, finalAddressID, disks, detail.Result.Created, nil
}

func saveToAnsibleInventory(filename, user string, nodes []NodeResult, sinkNode string) {
	f, err := os.Create(filename)
	if err != nil {
		fmt.Printf("[WARNING] Error: %v\n", err)
		return
	}
	defer f.Close()

	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Name == sinkNode {
			return false
		}
		if nodes[j].Name == sinkNode {
			return true
		}
		return nodes[i].Name < nodes[j].Name
	})

	const inventoryTmpl = `all:
  hosts:
{{- range .Nodes }}
    {{ .Name }}:
      ansible_host: {{ .IP }}
      ip: {{ .IP }}
      ansible_user: {{ $.User }}
      {{- if .SSHPort }}
      ansible_port: {{ .SSHPort }}
      {{- end }}
      kube_override_hostname: {{ .Name }}
      {{- if .Labels }}
      node_labels:
        {{- range $k, $v := .Labels }}
        {{ $k }}: "{{ $v }}"
        {{- end }}
      {{- end }}
      {{- if .Taints }}
      node_taints:
        {{- range .Taints }}
        - "{{ . }}"
        {{- end }}
      {{- end }}
      data_disks:
      {{- range .Disks }}
        {{- if and (not .Bootable) .Device }}
        - device: "{{ .Device }}"
          mount: "{{ .MountPoint }}"
          {{- if .Owner }}
          owner: "{{ .Owner }}"
          {{- end }}
          {{- if .Group }}
          group: "{{ .Group }}"
          {{- end }}
          {{- if .Mode }}
          mode: "{{ .Mode }}"
          {{- end }}
        {{- end }}
      {{- end }}
{{- end }}
  children:
    kube_control_plane:
      hosts:
{{- range .Nodes }}
  {{- if eq .Role "master" }}
        {{ .Name }}:
  {{- end }}
{{- end }}
    kube_node:
      hosts:
{{- range .Nodes }}
  {{- if eq .Role "worker" }}
        {{ .Name }}:
  {{- end }}
{{- end }}
    etcd:
      hosts:
{{- range .Nodes }}
  {{- if eq .Role "master" }}
        {{ .Name }}:
  {{- end }}
{{- end }}
    k8s_cluster:
      vars:
        download_run_once: true
        download_localhost: true
        kube_network_plugin: calico
        enable_network_policy: true 
        calico_datastore: "kdd"      
        calico_ipip_mode: "Never"    
        calico_ip_auto_method: "kubernetes-internal-ip" 
        kube_pods_subnet: 10.42.0.0/16
        kube_service_addresses: 10.43.0.0/16
        kube_proxy_metrics_bind_address: 0.0.0.0:10249
        kube_read_only_port: 10255 
        kubelet_bind_address: 0.0.0.0
        kube_proxy_nodeport_addresses:
          - "0.0.0.0/0"
        etcd_backup_v2: false
        metrics_server_enabled: true
        metrics_server_metric_resolution: 15s
        metrics_server_kubelet_insecure_tls: true
        local_volume_provisioner_enabled: true
        local_volume_provisioner_storage_classes:
          local-storage:
            host_dir: /mnt/disks
            mount_dir: /mnt/disks
            volume_mode: Filesystem
            fsType: ext4
            reclaim_policy: Retain
      children:
        kube_control_plane:
        kube_node:
    calico_rr:
      hosts: {}
    bastion:
      hosts:
{{- range .Nodes }}
  {{- if eq .Role "BASTION" }}
        {{ .Name }}:
  {{- end }}
{{- end }}
`
	data := InventoryData{User: user, Nodes: nodes}
	t := template.Must(template.New("inventory").Parse(inventoryTmpl))
	t.Execute(f, data)
}

func handleSync(client *clo.Client, s3Backend *state.Backend, clusterName string) {
	if s3Backend == nil {
		return
	}
	fmt.Printf("[REFRESH] Loading state '%s' from S3...\n", clusterName)
	st, err := s3Backend.LoadState()
	if err != nil || st == nil {
		return
	}
	fmt.Println("[CLOUD] Updating data from API...")
	var updatedNodes []state.NodeState
	hasChanges := false
	for _, node := range st.Nodes {
		fmt.Printf("   Checking %s... ", node.Name)
		newIP, newAddrID, newDisks, createdDate, err := fetchNodeDetails(client, node.ID, node.Disks, nil)
		if err != nil {
			fmt.Printf("[ERROR] Deleted\n")
			hasChanges = true
			continue
		}
		node.Created = createdDate
		node.Updated = time.Now().Format(time.RFC3339)
		nodeChanges := false
		if node.IP != newIP {
			node.IP = newIP
			nodeChanges = true
		}
		if node.AddressID != newAddrID {
			node.AddressID = newAddrID
			nodeChanges = true
		}
		if len(node.Disks) != len(newDisks) {
			node.Disks = newDisks
			nodeChanges = true
		} else {
			for i := range node.Disks {
				if node.Disks[i].ID == newDisks[i].ID && node.Disks[i].Device != newDisks[i].Device {
					node.Disks = newDisks
					nodeChanges = true
					break
				}
			}
		}
		if nodeChanges {
			hasChanges = true
		}
		fmt.Printf("[+OK+]\n")
		updatedNodes = append(updatedNodes, node)
	}
	if !hasChanges {
		fmt.Println("\n[STAR] No changes.")
		return
	}
	if askForConfirmation("[WARNING] Save changes to State?") {
		st.Nodes = updatedNodes
		st.LastUpdated = time.Now()
		s3Backend.SaveState(*st)
		fmt.Println("[SAVE] Saved.")
	}
}

func handleDeleteNodeFromState(s3Backend *state.Backend, clusterName, nodeName string, inventoryPath string) {
	if s3Backend == nil {
		return
	}
	st, err := s3Backend.LoadState()
	if err != nil || st == nil {
		return
	}
	if !askForConfirmation(fmt.Sprintf("Delete node '%s' from state?", nodeName)) {
		return
	}
	newNodes := []state.NodeState{}
	for _, n := range st.Nodes {
		if n.Name != nodeName {
			newNodes = append(newNodes, n)
		}
	}
	st.Nodes = newNodes
	st.LastUpdated = time.Now()
	s3Backend.SaveState(*st)
	fmt.Println("[SAVE] State updated.")
}

func handleResetPassword(client *clo.Client, s3Backend *state.Backend, clusterName string) {
	st, _ := s3Backend.LoadState()
	if st == nil {
		return
	}
	newPass, _ := clo.GenerateRandomPassword()
	for _, node := range st.Nodes {
		fmt.Printf("Updating %s... ", node.Name)
		client.SetServerPassword(node.ID, newPass)
		fmt.Printf("[+OK+]\n")
		time.Sleep(100 * time.Millisecond)
	}
}

func handleDeploy(client *clo.Client, backend *state.Backend, clusterName, outputFile string, forks int, runnerMode string, extraArgs string, targetNode string) {
	if backend == nil {
		fmt.Println("[ERROR] S3 unavailable")
		os.Exit(1)
	}
	fmt.Printf("[REFRESH] Loading state '%s'...\n", clusterName)
	st, err := backend.LoadState()
	if err != nil || st == nil {
		fmt.Printf("[ERROR] State error\n")
		os.Exit(1)
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
		fmt.Println("[ERROR] Bastion missing")
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
	saveToAnsibleInventory(invPath, st.SSHUser, nodesForInv, targetNode)

	DeployAndRunKubespray(bastionIP, bastionPort, st.SSHUser, keyPath, invPath, forks, runnerMode, extraArgs, backend)
}

func handleRemoveK8sNode(client *clo.Client, backend *state.Backend, clusterName, outputFile string, forks int, nodeName string) {
	runnerArgs := fmt.Sprintf("-node %s", nodeName)
	handleDeploy(client, backend, clusterName, outputFile, forks, "remove-node", runnerArgs, nodeName)
}
