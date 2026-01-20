package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"time"

	"cli/internal/local"
	"cli/internal/state"

	"golang.org/x/crypto/ssh"
)

// handleFlux - entry point for -flux command
func handleFlux(backend *state.Backend, clusterName, token, s3Access, s3Secret, ageKey, acmeEmail, domain string) {
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

	DeployAndRunFlux(bastionIP, bastionPort, masterIP, st.SSHUser, keyPath, token, password, s3Access, s3Secret, ageKey, acmeEmail, domain)
}

// DeployAndRunFlux - Logic for Flux deployment and secret creation
func DeployAndRunFlux(bastionIP string, bastionPort int, masterIP, user, keyPath, githubToken, password, s3Access, s3Secret, ageKey, acmeEmail, domain string) {
	fmt.Println("[GO] Preparing for Flux Bootstrap...")

	authMethods := getAuthMethods(password, keyPath)
	if len(authMethods) == 0 {
		log.Fatalf("No available keys for SSH.")
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	// UPDATED
	addr := net.JoinHostPort(bastionIP, fmt.Sprintf("%d", bastionPort))
	fmt.Printf("[PLUG] Connecting to %s...\n", addr)

	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		log.Fatalf("SSH connection error: %v", err)
	}
	defer client.Close()

	// 2. Compile Runner
	fmt.Println("[T] Compiling Runner...")
	localBinary := "./k8s-runner-flux-temp"
	if err := exec.Command("go", "build", "-o", localBinary, "./cmd/runner/main.go").Run(); err != nil {
		log.Fatalf("Compilation error: %v", err)
	}
	defer os.Remove(localBinary)

	// 3. Upload files to Bastion
	remoteBin := "/root/k8s-runner-flux"
	remoteFluxDir := "/root/flux-fs"

	runCommand(client, fmt.Sprintf("mkdir -p %s", remoteFluxDir))
	runCommand(client, "rm -f "+remoteBin)

	if uploadFile(client, localBinary, remoteBin) != nil {
		log.Fatalf("Failed to upload runner to server")
	}
	runCommand(client, "chmod +x "+remoteBin)

	fmt.Println("[KEY] Updating keys on Bastion...")
	remoteKeyPath := "/root/.ssh/id_rsa"
	runCommand(client, "mkdir -p /root/.ssh")

	if uploadFile(client, keyPath, remoteKeyPath) != nil {
		log.Fatalf("Error uploading key to bastion")
	}
	runCommand(client, "chmod 600 "+remoteKeyPath)

	// 4. Download kubeconfig from master to bastion
	fmt.Println("[PACKAGE] Downloading admin.conf from Master...")
	fetchCmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -i %s root@%s cat /etc/kubernetes/admin.conf > %s/kubeconfig", remoteKeyPath, masterIP, remoteFluxDir)
	if err := runCommand(client, fetchCmd); err != nil {
		log.Fatalf("Error downloading config: %v", err)
	}

	sedCmd := fmt.Sprintf("sed -i 's/127.0.0.1/%s/g' %s/kubeconfig", masterIP, remoteFluxDir)
	runCommand(client, sedCmd)

	// 5. Launch Flux Bootstrap via Runner
	fmt.Println("[GO] LAUNCHING FLUX BOOTSTRAP...")
	session, _ := client.NewSession()

	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	session.RequestPty("xterm", 80, 40, modes)
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	cmd := fmt.Sprintf("GITHUB_TOKEN='%s' %s flux", githubToken, remoteBin)

	if err := session.Run(cmd); err != nil {
		fmt.Printf("[ERROR] Flux Bootstrap error: %v\n", err)
		session.Close()
		os.Exit(1)
	} else {
		fmt.Println("[+OK+] Flux successfully installed!")
	}
	session.Close()

	// 6. Create secrets (Go Native via k8s client-go)
	// Re-dial needed because client might be closed or busy, but we can reuse if mutexed.
	// For simplicity, we create a new client inside SetupFluxSecrets logic or pass the existing one.
	// SetupFluxSecrets creates a NEW connection internally now because we updated k8s.go

	// BUT SetupFluxSecrets requires *ssh.Client.
	// We need to re-dial inside SetupFluxSecrets?
	// Ah, SetupFluxSecrets in k8s.go logic takes *ssh.Client.
	// So we need to dial again because previous session closed? No, defer client.Close() is at end of this func.
	// So we can pass `client`.

	// Wait, k8s.go functions NOW use Dial internally inside SetupFluxSecrets?
	// Let's check k8s.go update: No, I didn't update SetupFluxSecrets to do dialing!
	// I updated CreateOrUpdateConfigMap, but SetupFluxSecrets takes an existing *ssh.Client!

	// FIX: Update SetupFluxSecrets in k8s.go to NOT take *ssh.Client but take IP/Port and Dial itself,
	// OR keep passing client.
	// Passing client is better if connection is open.
	// BUT `SetupFluxSecrets` in my previous response WAS NOT UPDATED to take port.
	// Let's look at k8s.go provided above.
	// It says: func SetupFluxSecrets(sshClient *ssh.Client, ...)
	// This means it uses an existing connection.
	// So we don't need to change signature in k8s.go for SetupFluxSecrets if we pass an already connected client.
	// BUT the client was connected using the correct port in `DeployAndRunFlux`.
	// So it should be fine!

	// However, `SetupFluxSecrets` calls `createTunneledK8sClient` which calls `getTunneledRestConfig`
	// which sets `Dial` to `bastionClient.Dial`.
	// This works fine over the existing SSH connection.

	err = SetupFluxSecrets(client, masterIP, "root", remoteKeyPath, s3Access, s3Secret, ageKey, acmeEmail, domain)
	if err != nil {
		fmt.Printf("[ERROR] Error setting up secrets: %v\n", err)
	}
}
