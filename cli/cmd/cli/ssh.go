package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"time"

	"cli/internal/local"
	"cli/internal/state"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const ScreenSessionName = "kubespray-deploy"

func CheckPortConnection(targetIP, bastionIP string, bastionPort int, password string) error {
	authMethods := getAuthMethods(password, "")
	if len(authMethods) == 0 {
		return fmt.Errorf("no auth methods available")
	}
	config := &ssh.ClientConfig{
		User:            "root",
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         4 * time.Second,
	}

	bastionAddr := net.JoinHostPort(bastionIP, fmt.Sprintf("%d", bastionPort))

	if bastionIP == "" || targetIP == bastionIP {
		targetPort := "22"
		if targetIP == bastionIP {
			targetPort = fmt.Sprintf("%d", bastionPort)
		}

		client, err := ssh.Dial("tcp", net.JoinHostPort(targetIP, targetPort), config)
		if err != nil {
			return fmt.Errorf("ssh auth failed: %w", err)
		}
		client.Close()
		return nil
	}

	bClient, err := ssh.Dial("tcp", bastionAddr, config)
	if err != nil {
		return fmt.Errorf("bastion conn failed (%s): %w", bastionAddr, err)
	}
	defer bClient.Close()

	conn, err := bClient.Dial("tcp", net.JoinHostPort(targetIP, "22"))
	if err != nil {
		return fmt.Errorf("unreachable: %w", err)
	}
	conn.Close()
	return nil
}

func getAuthMethods(password string, priorityKeyPath string) []ssh.AuthMethod {
	var methods []ssh.AuthMethod
	if priorityKeyPath != "" {
		if keyBytes, err := os.ReadFile(priorityKeyPath); err == nil {
			if signer, err := ssh.ParsePrivateKey(keyBytes); err == nil {
				methods = append(methods, ssh.PublicKeys(signer))
			}
		}
	}
	if password != "" {
		methods = append(methods, ssh.Password(password))
	}
	stdPaths := []string{os.ExpandEnv("${HOME}/.ssh/clo"), os.ExpandEnv("${HOME}/.ssh/id_rsa")}
	for _, path := range stdPaths {
		if path == priorityKeyPath {
			continue
		}
		if keyBytes, err := os.ReadFile(path); err == nil {
			if signer, err := ssh.ParsePrivateKey(keyBytes); err == nil {
				methods = append(methods, ssh.PublicKeys(signer))
			}
		}
	}
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			agentClient := agent.NewClient(conn)
			if signers, err := agentClient.Signers(); err == nil {
				methods = append(methods, ssh.PublicKeys(signers...))
			}
		}
	}
	return methods
}

func DeployAndRunKubespray(bastionIP string, bastionPort int, user, keyPath, inventoryPath string, forks int, runnerMode string, extraArgs string, s3Backend *state.Backend) {
	err := func() error {
		var envVars string
		if s3Backend != nil {
			endpoint := os.Getenv("S3_ENDPOINT")
			access := os.Getenv("S3_ACCESS_KEY")
			secret := os.Getenv("S3_SECRET_KEY")
			if endpoint != "" {
				am, _ := NewArtifactsManager(endpoint, access, secret, true)
				if am != nil {
					if url, err := am.GetPresignedURL("kubespray.tar"); err == nil {
						envVars += fmt.Sprintf("export KUBESPRAY_URL='%s'\n", url)
						fmt.Println("[LINK] S3 Link generated: kubespray.tar")
					}
					if url, err := am.GetPresignedURL("k8s_images.tar"); err == nil {
						envVars += fmt.Sprintf("export K8S_IMAGES_URL='%s'\n", url)
						fmt.Println("[LINK] S3 Link generated: k8s_images.tar")
					}
				}
			}
		}

		bastionAddr := net.JoinHostPort(bastionIP, fmt.Sprintf("%d", bastionPort))
		fmt.Printf("[PLUG] [1/5] Connecting to %s...\n", bastionAddr)

		authMethods := getAuthMethods("", keyPath)
		if len(authMethods) == 0 {
			return fmt.Errorf("key not found")
		}
		config := &ssh.ClientConfig{User: user, Auth: authMethods, HostKeyCallback: ssh.InsecureIgnoreHostKey()}

		client, err := ssh.Dial("tcp", bastionAddr, config)
		if err != nil {
			return fmt.Errorf("SSH Error: %w", err)
		}
		defer client.Close()

		fmt.Println("[PACKAGE] [2/5] Checking dependencies...")
		runCommand(client, "export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get install -y rsync screen curl")

		sessionName := ScreenSessionName
		if runnerMode == "mount" {
			sessionName = "kubespray-mount"
		} else if runnerMode == "permissions" {
			sessionName = "set-perms"
		} else if runnerMode == "remove-node" {
			sessionName = "kubespray-remove"
		} else if runnerMode == "create-user" {
			sessionName = "create-user-ops"
		} else if runnerMode == "os-update" {
			sessionName = "os-upgrade"
		}

		if checkScreenSession(client, sessionName) {
			fmt.Println("[ANNOUNCE] Connecting to existing session...")
		} else {
			fmt.Println("[T] [4/5] Uploading files...")
			localBinary := "./k8s-runner-temp"
			if err := exec.Command("go", "build", "-o", localBinary, "./cmd/runner/main.go").Run(); err != nil {
				return fmt.Errorf("compile error: %w", err)
			}
			defer os.Remove(localBinary)

			const remoteRoot = "/root/kubespray-fs"
			remoteBin := "/root/k8s-runner"

			runCommand(client, fmt.Sprintf("mkdir -p %s/root/.ssh", remoteRoot))
			runCommand(client, "rm -f "+remoteBin)

			uploadFile(client, inventoryPath, remoteRoot+"/inventory.yaml")
			uploadFile(client, keyPath, remoteRoot+"/root/.ssh/id_rsa")
			if err := uploadFile(client, localBinary, remoteBin); err != nil {
				return fmt.Errorf("upload error: %w", err)
			}

			runCommand(client, "chmod +x "+remoteBin)

			wrapperScript := fmt.Sprintf("/root/run_%s.sh", runnerMode)
			runArgs := runnerMode

			switch runnerMode {
			case "remove-node":
				runArgs += fmt.Sprintf(" %s", extraArgs)
			case "create-user":
				runArgs += fmt.Sprintf(" -f %d %s", forks, extraArgs)
			case "os-update":
				runArgs += fmt.Sprintf(" -f %d %s", forks, extraArgs)
			case "permissions":
				runArgs += fmt.Sprintf(" -f %d %s", forks, extraArgs)
			default:
				runArgs += fmt.Sprintf(" -f %d", forks)
				if extraArgs != "" {
					runArgs += fmt.Sprintf(" -l '%s'", extraArgs)
				}
			}

			scriptContent := fmt.Sprintf(`#!/bin/bash
%s
echo "[GO] Starting Runner (%s)..."
%s %s
RET=$?
echo "---------------------------------------------------"
if [ $RET -eq 0 ]; then
    echo "[+OK+] Success! Closing session..."
    sleep 3
    exit 0
else
    echo "[ERROR] Failed with exit code $RET."
    exec bash
fi
`, envVars, runnerMode, remoteBin, runArgs)

			runCommand(client, fmt.Sprintf("cat <<'EOF' > %s\n%s\nEOF", wrapperScript, scriptContent))
			runCommand(client, "chmod +x "+wrapperScript)

			screenConfig := `
defscrollback 50000
termcapinfo xterm* ti@:te@
startup_message off
`
			runCommand(client, fmt.Sprintf("cat <<EOF > /root/.screenrc\n%s\nEOF", screenConfig))
			fmt.Println("   [+OK+] Uploaded.")
		}

		fmt.Printf("[GO] [5/5] Entering Screen (%s)...\n", runnerMode)
		session, _ := client.NewSession()
		defer session.Close()
		modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
		session.RequestPty("xterm", 80, 40, modes)

		mw := local.GetLogWriter()
		session.Stdout = mw
		session.Stderr = mw
		session.Stdin = os.Stdin

		cmd := ""
		if checkScreenSession(client, sessionName) {
			cmd = fmt.Sprintf("screen -x %s", sessionName)
		} else {
			wrapperScript := fmt.Sprintf("/root/run_%s.sh", runnerMode)
			cmd = fmt.Sprintf("screen -dmS %s %s; sleep 1; screen -r %s", sessionName, wrapperScript, sessionName)
		}
		return session.Run(cmd)
	}()

	if err != nil {
		log.Fatalf("[ERROR] Error: %v", err)
	}
}

// Helpers
func checkScreenSession(client *ssh.Client, name string) bool {
	session, err := client.NewSession()
	if err != nil {
		return false
	}
	defer session.Close()
	return session.Run(fmt.Sprintf("screen -list | grep -q %s", name)) == nil
}

func uploadFile(client *ssh.Client, localPath, remotePath string) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	go func() { w, _ := session.StdinPipe(); defer w.Close(); io.Copy(w, f) }()
	return session.Run(fmt.Sprintf("cat > %s", remotePath))
}

func runCommand(client *ssh.Client, cmd string) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	return session.Run(cmd)
}
