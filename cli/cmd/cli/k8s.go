package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// SetupFluxSecrets: added bastionPort
func SetupFluxSecrets(sshClient *ssh.Client, masterIP, user, remoteKeyPath, s3Access, s3Secret, ageKey, acmeEmail, domain string) error {
	fmt.Println("[LOCK] Setting up secrets via k8s client-go (SSH Tunnel)...")

	// 1. Get Kubeconfig
	fmt.Println("   [INBOX] Getting kubeconfig from master...")
	cmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -i %s %s@%s cat /etc/kubernetes/admin.conf", remoteKeyPath, user, masterIP)
	kubeconfigBytes, err := runCommandOutput(sshClient, cmd)
	if err != nil {
		return fmt.Errorf("failed to fetch kubeconfig: %w", err)
	}

	// 2. Create client
	clientset, err := createTunneledK8sClient(sshClient, kubeconfigBytes, masterIP)
	if err != nil {
		return fmt.Errorf("k8s client init failed: %w", err)
	}

	ctx := context.TODO()
	ns := "flux-system"

	// 3. Secret s3-sops
	s3SecretData := map[string][]byte{
		"accesskey": []byte(s3Access),
		"secretkey": []byte(s3Secret),
	}
	if err := applySecret(ctx, clientset, ns, "s3-sops", s3SecretData); err != nil {
		return fmt.Errorf("failed to apply s3-sops: %w", err)
	}
	fmt.Println("   [+OK+] Secret 's3-sops' applied.")

	// 4. Secret sops-age
	ageSecretData := map[string][]byte{
		"sops.agekey": []byte(ageKey),
	}
	if err := applySecret(ctx, clientset, ns, "sops-age", ageSecretData); err != nil {
		return fmt.Errorf("failed to apply sops-age: %w", err)
	}
	fmt.Println("   [+OK+] Secret 'sops-age' applied.")

	// 5. Secret cert-manager-acme-email
	if acmeEmail != "" {
		emailSecretData := map[string][]byte{
			"ACME_EMAIL": []byte(acmeEmail),
		}
		if err := applySecret(ctx, clientset, ns, "cert-manager-acme-email", emailSecretData); err != nil {
			return fmt.Errorf("failed to apply cert-manager-acme-email: %w", err)
		}
		fmt.Println("   [+OK+] Secret 'cert-manager-acme-email' applied.")
	}

	// 6. cluster-domain secret
	if domain != "" {
		domainSecretData := map[string][]byte{
			"DOMAIN": []byte(domain),
		}
		if err := applySecret(ctx, clientset, ns, "cluster-domain", domainSecretData); err != nil {
			return fmt.Errorf("failed to apply cluster-domain: %w", err)
		}
		fmt.Println("   [+OK+] Secret 'cluster-domain' applied.")
	}

	return nil
}

// CreateSingleSecret: added bastionPort
func CreateSingleSecret(bastionIP string, bastionPort int, masterIP, user, keyPath, password, secretName, key, value string) {
	fmt.Printf("[LOCK] Creating secret '%s' (Key: %s)...\n", secretName, key)

	authMethods := getAuthMethods(password, keyPath)
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	// UPDATED: Use port
	addr := net.JoinHostPort(bastionIP, fmt.Sprintf("%d", bastionPort))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		fmt.Printf("[ERROR] SSH connection error: %v\n", err)
		return
	}
	defer client.Close()

	cmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -i /root/.ssh/id_rsa root@%s cat /etc/kubernetes/admin.conf", masterIP)
	kubeconfigBytes, err := runCommandOutput(client, cmd)
	if err != nil {
		fmt.Printf("[ERROR] Failed to download kubeconfig: %v\n", err)
		return
	}

	clientset, err := createTunneledK8sClient(client, kubeconfigBytes, masterIP)
	if err != nil {
		fmt.Printf("[ERROR] K8s client error: %v\n", err)
		return
	}

	ctx := context.TODO()
	ns := "flux-system"
	data := map[string][]byte{
		key: []byte(value),
	}

	if err := applySecret(ctx, clientset, ns, secretName, data); err != nil {
		fmt.Printf("[ERROR] Secret creation error: %v\n", err)
		return
	}

	fmt.Printf("   [+OK+] Secret '%s' successfully applied in namespace '%s'.\n", secretName, ns)
}

// WaitForCertificateReady: added bastionPort
func WaitForCertificateReady(bastionIP string, bastionPort int, masterIP, user, keyPath, password, ns, certName string) error {
	fmt.Printf("[TIME] Waiting for certificate readiness %s/%s...\n", ns, certName)

	authMethods := getAuthMethods(password, keyPath)
	sshConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         50 * time.Second,
	}

	// UPDATED
	addr := net.JoinHostPort(bastionIP, fmt.Sprintf("%d", bastionPort))
	sshClient, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return fmt.Errorf("ssh dial: %w", err)
	}
	defer sshClient.Close()

	cmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -i /root/.ssh/id_rsa root@%s cat /etc/kubernetes/admin.conf", masterIP)
	kubeconfigBytes, err := runCommandOutput(sshClient, cmd)
	if err != nil {
		return fmt.Errorf("get kubeconfig: %w", err)
	}

	restConfig, err := getTunneledRestConfig(sshClient, kubeconfigBytes, masterIP)
	if err != nil {
		return err
	}
	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "certificates",
	}

	timeout := 50 * time.Minute
	start := time.Now()

	for {
		if time.Since(start) > timeout {
			return fmt.Errorf("timeout waiting for certificate")
		}

		unstruct, err := dynClient.Resource(gvr).Namespace(ns).Get(context.TODO(), certName, metav1.GetOptions{})
		if err != nil {
			fmt.Printf("   [WARNING] Cert not found/error: %v. Retrying...\n", err)
			time.Sleep(10 * time.Second)
			continue
		}

		conditions, found, err := unstructured.NestedSlice(unstruct.Object, "status", "conditions")
		if !found || err != nil {
			fmt.Print(".")
			time.Sleep(5 * time.Second)
			continue
		}

		isReady := false
		for _, c := range conditions {
			cond, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			ctype, _ := cond["type"].(string)
			cstatus, _ := cond["status"].(string)

			if ctype == "Ready" && cstatus == "True" {
				isReady = true
				break
			}
		}

		if isReady {
			fmt.Println("\n[+OK+] Certificate issued and ready!")
			return nil
		}

		fmt.Print(".")
		time.Sleep(10 * time.Second)
	}
}

// CreateOrUpdateConfigMap: added bastionPort
func CreateOrUpdateConfigMap(bastionIP string, bastionPort int, masterIP, user, keyPath, password, ns, name string, data map[string]string) error {
	fmt.Printf("[PACKAGE] Kubernetes: Applying ConfigMap %s/%s...\n", ns, name)

	authMethods := getAuthMethods(password, keyPath)
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	// UPDATED
	addr := net.JoinHostPort(bastionIP, fmt.Sprintf("%d", bastionPort))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("SSH dial failed: %w", err)
	}
	defer client.Close()

	cmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -i /root/.ssh/id_rsa root@%s cat /etc/kubernetes/admin.conf", masterIP)
	kubeconfigBytes, err := runCommandOutput(client, cmd)
	if err != nil {
		return fmt.Errorf("failed to fetch kubeconfig: %w", err)
	}

	clientset, err := createTunneledK8sClient(client, kubeconfigBytes, masterIP)
	if err != nil {
		return fmt.Errorf("k8s client init failed: %w", err)
	}

	ctx := context.TODO()

	fmt.Printf("[MAGNIFYING_GLASS] Checking for existence of namespace '%s'...\n", ns)
	timeout := 10 * time.Minute
	interval := 10 * time.Second
	start := time.Now()

	for {
		_, err := clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
		if err == nil {
			fmt.Printf("[+OK+] Namespace '%s' found.\n", ns)
			break
		}
		if !errors.IsNotFound(err) {
			return fmt.Errorf("check namespace error: %w", err)
		}
		if time.Since(start) > timeout {
			return fmt.Errorf("timeout waiting for namespace '%s'", ns)
		}
		fmt.Printf("[TIME] Namespace '%s' does not exist yet. Waiting... (%s elapsed)\n", ns, time.Since(start).Round(time.Second))
		time.Sleep(interval)
	}

	cmClient := clientset.CoreV1().ConfigMaps(ns)
	existing, err := cmClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			newCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ns,
				},
				Data: data,
			}
			_, err := cmClient.Create(ctx, newCM, metav1.CreateOptions{})
			return err
		}
		return fmt.Errorf("get cm error: %w", err)
	}

	existing.Data = data
	_, err = cmClient.Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// CreatePerconaBackup: added bastionPort
func CreatePerconaBackup(bastionIP string, bastionPort int, masterIP, user, keyPath, password, ns, backupName, clusterName, repoName, typ string) error {
	fmt.Printf("[PACKAGE] Kubernetes: Creating PerconaPGBackup '%s'...\n", backupName)

	authMethods := getAuthMethods(password, keyPath)
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	// UPDATED
	addr := net.JoinHostPort(bastionIP, fmt.Sprintf("%d", bastionPort))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("SSH dial failed: %w", err)
	}
	defer client.Close()

	cmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -i /root/.ssh/id_rsa root@%s cat /etc/kubernetes/admin.conf", masterIP)
	kubeconfigBytes, err := runCommandOutput(client, cmd)
	if err != nil {
		return fmt.Errorf("failed to fetch kubeconfig: %w", err)
	}

	restConfig, err := getTunneledRestConfig(client, kubeconfigBytes, masterIP)
	if err != nil {
		return fmt.Errorf("rest config error: %w", err)
	}

	gv := schema.GroupVersion{Group: "pgv2.percona.com", Version: "v2"}
	restConfig.GroupVersion = &gv
	restConfig.APIPath = "/apis"

	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("dynamic client init failed: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "pgv2.percona.com",
		Version:  "v2",
		Resource: "perconapgbackups",
	}

	backupObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "pgv2.percona.com/v2",
			"kind":       "PerconaPGBackup",
			"metadata": map[string]interface{}{
				"name":      backupName,
				"namespace": ns,
			},
			"spec": map[string]interface{}{
				"pgCluster": clusterName,
				"repoName":  repoName,
				"type":      typ,
			},
		},
	}

	_, err = dynClient.Resource(gvr).Namespace(ns).Create(context.TODO(), backupObj, metav1.CreateOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("resource not found (404). Check CRD group 'pgv2.percona.com': %w", err)
		}
		return fmt.Errorf("create backup resource failed: %w", err)
	}

	return nil
}

// GetPGClusterStatus: added bastionPort
func GetPGClusterStatus(bastionIP string, bastionPort int, masterIP, user, keyPath, password, ns, pgClusterName string) (map[string]interface{}, string, error) {
	authMethods := getAuthMethods(password, keyPath)
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	// UPDATED
	addr := net.JoinHostPort(bastionIP, fmt.Sprintf("%d", bastionPort))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, "", fmt.Errorf("ssh dial failed: %w", err)
	}
	defer client.Close()

	cmd := fmt.Sprintf("ssh -o StrictHostKeyChecking=no -i /root/.ssh/id_rsa root@%s cat /etc/kubernetes/admin.conf", masterIP)
	kubeconfigBytes, err := runCommandOutput(client, cmd)
	if err != nil {
		return nil, "", fmt.Errorf("fetch kubeconfig failed: %w", err)
	}

	restConfig, err := getTunneledRestConfig(client, kubeconfigBytes, masterIP)
	if err != nil {
		return nil, "", err
	}

	gv := schema.GroupVersion{Group: "pgv2.percona.com", Version: "v2"}
	restConfig.GroupVersion = &gv
	restConfig.APIPath = "/apis"

	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, "", fmt.Errorf("dynamic client init failed: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "pgv2.percona.com",
		Version:  "v2",
		Resource: "perconapgclusters",
	}

	u, err := dynClient.Resource(gvr).Namespace(ns).Get(context.TODO(), pgClusterName, metav1.GetOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("failed to get pg cluster: %w", err)
	}

	status, found, err := unstructured.NestedMap(u.Object, "status")
	if !found || err != nil {
		return nil, "Unknown (No status field)", nil
	}

	stateStr, _, _ := unstructured.NestedString(status, "state")
	return status, stateStr, nil
}

// --- Helpers (unchanged) ---
func getTunneledRestConfig(bastionClient *ssh.Client, kubeconfig []byte, masterIP string) (*rest.Config, error) {
	config, err := clientcmd.NewClientConfigFromBytes(kubeconfig)
	if err != nil {
		return nil, err
	}
	clientConfig, err := config.ClientConfig()
	if err != nil {
		return nil, err
	}
	clientConfig.Host = fmt.Sprintf("https://%s:6443", masterIP)
	clientConfig.Insecure = true
	clientConfig.CAData = nil
	clientConfig.CAFile = ""
	clientConfig.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return bastionClient.Dial(network, addr)
	}
	return clientConfig, nil
}

func createTunneledK8sClient(bastionClient *ssh.Client, kubeconfig []byte, masterIP string) (*kubernetes.Clientset, error) {
	restConfig, err := getTunneledRestConfig(bastionClient, kubeconfig, masterIP)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(restConfig)
}

func applySecret(ctx context.Context, clientset *kubernetes.Clientset, ns, name string, data map[string][]byte) error {
	secretsClient := clientset.CoreV1().Secrets(ns)
	existing, err := secretsClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			newSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ns,
				},
				Type: corev1.SecretTypeOpaque,
				Data: data,
			}
			_, err := secretsClient.Create(ctx, newSecret, metav1.CreateOptions{})
			return err
		}
		return err
	}
	existing.Data = data
	_, err = secretsClient.Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func runCommandOutput(client *ssh.Client, cmd string) ([]byte, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	return session.Output(cmd)
}
