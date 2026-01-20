package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"cli/internal/clo"
	"cli/internal/local"
	"cli/internal/state"

	"golang.org/x/crypto/ssh"
)

// handleAddUser adds a user
func handleAddUser(client *clo.Client, backend *state.Backend, clusterName, inputStr string, forks int, limit string) {
	// 1. Parse the argument
	parts := strings.SplitN(inputStr, ":", 2)
	if len(parts) != 2 {
		fmt.Println("[ERROR] Format error. Use: -add-user 'username:key'")
		os.Exit(1)
	}
	targetUser := parts[0]
	keyData := parts[1]

	var keyContent []byte
	var err error

	// 2. Determine if it's a file or content
	if strings.HasPrefix(keyData, "ssh-") || strings.HasPrefix(keyData, "ecdsa-") || strings.HasPrefix(keyData, "sk-") {
		fmt.Printf("[KEY] RAW key (string) detected for user '%s'\n", targetUser)
		keyContent = []byte(keyData)
	} else {
		fmt.Printf("[FILE_FOLDER] Reading key file: %s\n", keyData)
		keyContent, err = os.ReadFile(keyData)
		if err != nil {
			fmt.Printf("[ERROR] Failed to read key file: %v\n", err)
			os.Exit(1)
		}
	}

	// 3. Load state
	st, err := loadStateAndBastion(backend, clusterName)
	if err != nil {
		fmt.Printf("[ERROR] Error: %v\n", err)
		os.Exit(1)
	}

	// 4. Upload key to bastion
	sshKeyPath := os.ExpandEnv("${HOME}/.ssh/clo")
	password, _ := local.LoadPassword(clusterName)

	bastionIP, bastionPort := getBastionDetails(st)

	fmt.Printf("[UPLOAD] Uploading public key for '%s' to bastion (%s:%d)...\n", targetUser, bastionIP, bastionPort)
	if err := uploadUserKeyToBastion(bastionIP, bastionPort, st.SSHUser, password, sshKeyPath, keyContent); err != nil {
		fmt.Printf("[ERROR] Error uploading key to bastion: %v\n", err)
		os.Exit(1)
	}

	// 5. Generate inventory and run Runner
	runnerArgs := fmt.Sprintf("-u %s", targetUser)
	if limit != "" {
		runnerArgs += fmt.Sprintf(" -l %s", limit)
	}

	generateInventory(st, st.SSHUser)

	fmt.Printf("[GO] Launching user creation '%s' (System Update disabled, use -osupd)...\n", targetUser)
	// UPDATED: Pass bastionPort
	DeployAndRunKubespray(bastionIP, bastionPort, st.SSHUser, sshKeyPath, "inventory.gen.yaml", forks, "create-user", runnerArgs, backend)
}

// handleOSUpdate runs system update
func handleOSUpdate(client *clo.Client, backend *state.Backend, clusterName string, forks int, limit string) {
	fmt.Println("[REFRESH] Preparing for OS update (apt dist-upgrade)...")

	// 1. Load state
	st, err := loadStateAndBastion(backend, clusterName)
	if err != nil {
		fmt.Printf("[ERROR] Error: %v\n", err)
		os.Exit(1)
	}

	bastionIP, bastionPort := getBastionDetails(st)
	sshKeyPath := os.ExpandEnv("${HOME}/.ssh/clo")

	// 2. Generate inventory
	generateInventory(st, st.SSHUser)

	// 3. Run runner in os-update mode
	runnerArgs := ""
	if limit != "" {
		runnerArgs = fmt.Sprintf("-l %s", limit)
	}

	fmt.Println("[GO] Launching system package update...")
	// UPDATED: Pass bastionPort
	DeployAndRunKubespray(bastionIP, bastionPort, st.SSHUser, sshKeyPath, "inventory.gen.yaml", forks, "os-update", runnerArgs, backend)
}

// --- HELPERS ---

func loadStateAndBastion(backend *state.Backend, clusterName string) (*state.ClusterState, error) {
	if backend == nil {
		return nil, fmt.Errorf("backend unavailable")
	}
	fmt.Printf("[REFRESH] Loading state for cluster '%s'...\n", clusterName)
	st, err := backend.LoadState()
	if err != nil || st == nil {
		return nil, fmt.Errorf("load state failed: %v", err)
	}
	return st, nil
}

// getBastionDetails returns IP and Port
func getBastionDetails(st *state.ClusterState) (string, int) {
	for _, n := range st.Nodes {
		if n.Role == "BASTION" {
			port := 22
			if n.SSHPort != 0 {
				port = n.SSHPort
			}
			return n.IP, port
		}
	}
	fmt.Println("[ERROR] Bastion not found in state.")
	os.Exit(1)
	return "", 0
}

func generateInventory(st *state.ClusterState, sshUser string) {
	invPath := "inventory.gen.yaml"
	var nodesForInv []NodeResult
	for _, n := range st.Nodes {
		nodesForInv = append(nodesForInv, NodeResult{
			Name: n.Name, IP: n.IP, Role: n.Role, Labels: n.Labels, Taints: n.Taints, Disks: n.Disks,
			SSHPort: n.SSHPort, // Pass port to inventory
		})
	}
	saveToAnsibleInventory(invPath, sshUser, nodesForInv, "")
}

func uploadUserKeyToBastion(ip string, port int, user, password, keyPath string, content []byte) error {
	authMethods := getAuthMethods(password, keyPath)
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	// UPDATED: Use port
	address := net.JoinHostPort(ip, fmt.Sprintf("%d", port))
	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		return err
	}
	defer client.Close()

	remoteDir := "/root/kubespray-fs/root"
	remotePath := remoteDir + "/new_user.pub"

	runCommand(client, "mkdir -p "+remoteDir)

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	session.Stdin = strings.NewReader(string(content))
	return session.Run(fmt.Sprintf("cat > %s", remotePath))
}
