package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"cli/internal/clo"
	"cli/internal/local"
	"cli/internal/state"
)

var (
	// String parameters
	addPtr        string
	delPtr        string
	outputFile    string
	clusterName   string
	sshPass       string
	configPath    string
	delNodePtr    string
	ansibleLimit  string
	removeK8sNode string

	// Boolean flags
	createCluster   bool
	cleanAll        bool
	cleanDisks      bool
	checkState      bool
	jsonFormat      bool
	deployKubespray bool
	resetPass       bool
	forceCreate     bool
	fluxMode        bool
	syncState       bool
	enableLog       bool
	mountDisks      bool
	deleteNodes     bool
	attachDisks     bool
	createLB        bool
	listImages      bool
	osUpd           bool
	ansibleForks    int

	// --- NEW FLAGS ---
	setPermissions bool
	noCheck        bool
	// -----------------

	// YCloud & Other flags
	hloadDomain      string
	k6ScriptPath     string
	deleteYCloud     bool
	outputKubeconfig string
	ycloudOkubecfg   string
	k8sImagesBundle  bool
	kvSecStr         string
	waitCertStr      string
	addUserStr       string
	cmCreateStr      string
	createBackup     bool
	statusRestoreDB  bool
	criticalDiskID   string
)

func init() {
	flag.BoolVar(&createCluster, "cluster", false, "Create or Reconcile cluster based on config")
	flag.StringVar(&configPath, "config", "", "Path to YAML configuration file")
	flag.BoolVar(&deleteNodes, "delnodes", false, "Physically delete nodes removed from config (GC)")
	flag.BoolVar(&attachDisks, "attach-disks", false, "Attach existing detached disks to nodes based on state")

	flag.BoolVar(&noCheck, "nocheck", false, "Skip SSH/TCP accessibility check after cluster creation")

	flag.BoolVar(&createLB, "lbclo", false, "Create Cloud Load Balancer for the cluster")

	flag.BoolVar(&deployKubespray, "deploy", false, "Deploy Kubespray")
	flag.BoolVar(&mountDisks, "mount", false, "Mount disks only")

	flag.BoolVar(&setPermissions, "permissions", false, "Apply disk ownership/permissions from config recursively")

	flag.IntVar(&ansibleForks, "f", 5, "Ansible forks")
	flag.StringVar(&ansibleLimit, "l", "", "Limit Ansible hosts")
	flag.StringVar(&removeK8sNode, "remove-k8s-node", "", "Gracefully remove node from K8s cluster (runs remove-node.yml)")

	flag.BoolVar(&fluxMode, "flux", false, "Run Flux Bootstrap")
	flag.BoolVar(&syncState, "sync", false, "Synchronize state")
	flag.BoolVar(&checkState, "state", false, "Check state")
	flag.BoolVar(&jsonFormat, "json", false, "JSON output")
	flag.BoolVar(&resetPass, "reset-password", false, "Reset root password")
	flag.BoolVar(&cleanAll, "clean-all", false, "Delete ALL servers")
	flag.BoolVar(&cleanDisks, "clean-disks", false, "List Disks")

	flag.StringVar(&clusterName, "name", "default", "Cluster name")
	flag.BoolVar(&forceCreate, "force", false, "Force recreation")
	flag.StringVar(&sshPass, "ssh-pass", "", "Manual password")
	flag.StringVar(&outputFile, "o", "", "Output inventory file")
	flag.BoolVar(&enableLog, "log", false, "Enable logging")

	flag.StringVar(&delPtr, "del", "", "Delete server via API")
	flag.StringVar(&delNodePtr, "delnode", "", "Remove node from State ONLY")
	flag.StringVar(&addPtr, "add", "", "Add single server")

	flag.StringVar(&hloadDomain, "hload", "", "YCloud Cloud: Run K6 stress test against domain")
	flag.StringVar(&k6ScriptPath, "script", "k6scripts/test.js", "Path to K6 test script file")
	flag.BoolVar(&deleteYCloud, "delete", false, "YCloud Cloud: Delete cluster and resources defined by -hload")
	flag.StringVar(&outputKubeconfig, "okubecfg", "", "YCloud Cloud: Path to save generated Kubeconfig")
	flag.StringVar(&ycloudOkubecfg, "ycloudokubecfg", "", "YCloud: Download Kubeconfig to file (without running tests)")

	flag.BoolVar(&k8sImagesBundle, "k8simages", false, "Download and bundle K8s images to S3 (for offline install)")
	flag.BoolVar(&listImages, "list-images", false, "List Docker images inside the S3 bundle")
	flag.StringVar(&kvSecStr, "kvsec", "", "Create generic secret. Format: 'SECRET_NAME:KEY=VALUE'")
	flag.StringVar(&waitCertStr, "wait-cert", "", "Wait for Certificate to be Ready. Format: 'NAMESPACE:CERT_NAME'")
	flag.StringVar(&addUserStr, "add-user", "", "Add sudo user. Format: 'username:path/to/key.pub'")
	flag.BoolVar(&osUpd, "osupd", false, "Update OS on nodes (apt dist-upgrade)")
	flag.StringVar(&cmCreateStr, "cmcreate", "", "Create ConfigMap from JSON. Format: 'namespace:name:json_string'")
	flag.BoolVar(&createBackup, "create-backup", false, "Trigger manual Percona PG Backup with timestamp")
	flag.BoolVar(&statusRestoreDB, "statusrestoredb", false, "Check if Percona PG Cluster restore is complete")
	flag.StringVar(&criticalDiskID, "critical-disk", "", "Mark disk ID as critical (protected from deletion)")
}

func main() {
	flag.Parse()

	if enableLog {
		if err := local.InitLog(clusterName); err == nil {
			defer local.CloseLog()
		}
	}

	if ycloudOkubecfg != "" {
		handleYCloudGetKubeconfig(ycloudOkubecfg)
		return
	}
	if hloadDomain != "" {
		if deleteYCloud {
			handleYCloudDelete()
		} else {
			// --- UPDATED: Pass k6ScriptPath ---
			handleYCloudLoadTest(hloadDomain, outputKubeconfig, k6ScriptPath)
		}
		return
	}

	var s3Backend *state.Backend
	s3Endpoint := os.Getenv("S3_ENDPOINT")
	s3Access := os.Getenv("S3_ACCESS_KEY")
	s3Secret := os.Getenv("S3_SECRET_KEY")
	s3Bucket := os.Getenv("S3_BUCKET")
	s3Key := fmt.Sprintf("clusters/%s/state.json", clusterName)

	if s3Endpoint != "" && s3Bucket != "" {
		var err error
		s3Backend, err = state.NewBackend(s3Endpoint, s3Access, s3Secret, s3Bucket, s3Key, true)
		if err != nil {
			fmt.Printf("[WARNING] S3 Init: %v\n", err)
		}
	} else {
		if resetPass || deployKubespray || checkState || fluxMode || syncState || mountDisks || removeK8sNode != "" || attachDisks || createLB || k8sImagesBundle || listImages || kvSecStr != "" || waitCertStr != "" || addUserStr != "" || osUpd || cmCreateStr != "" || createBackup || statusRestoreDB || criticalDiskID != "" || setPermissions {
			fmt.Println("[ERROR] Error: S3 required.")
			os.Exit(1)
		}
	}

	if listImages {
		if s3Backend == nil {
			fmt.Println("[ERROR] S3 unavailable.")
			os.Exit(1)
		}
		if err := ListImagesInS3(s3Endpoint, s3Access, s3Secret); err != nil {
			fmt.Printf("[ERROR] Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if k8sImagesBundle {
		if s3Backend == nil {
			fmt.Println("[ERROR] S3 unavailable.")
			os.Exit(1)
		}
		url, err := EnsureK8sBundleInS3(s3Endpoint, s3Access, s3Secret)
		if err != nil {
			fmt.Printf("[ERROR] Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n[STAR] Bundle link: %s\n", url)
		return
	}

	if createCluster && s3Endpoint != "" {
		StartBackgroundSync(s3Endpoint, s3Access, s3Secret)
	}

	if fluxMode {
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			fmt.Println("Error: GITHUB_TOKEN required")
			os.Exit(1)
		}
		ageKey := os.Getenv("SOPS_AGE_KEY")
		acmeEmail := os.Getenv("ACME_EMAIL")
		domain := os.Getenv("DOMAIN")
		handleFlux(s3Backend, clusterName, token, s3Access, s3Secret, ageKey, acmeEmail, domain)
		return
	}

	if criticalDiskID != "" {
		handleMarkDiskCritical(s3Backend, clusterName, criticalDiskID)
		return
	}

	token := os.Getenv("CLO_AUTH_TOKEN")
	projectID := os.Getenv("CLO_OBJECT_ID")
	apiRequired := createCluster || cleanAll || cleanDisks || delPtr != "" || addPtr != "" || resetPass || deployKubespray || syncState || attachDisks || createLB || osUpd || setPermissions
	if (token == "" || projectID == "") && apiRequired {
		fmt.Println("Error: CLO_AUTH_TOKEN required.")
		os.Exit(1)
	}

	var client *clo.Client
	if token != "" {
		client = clo.NewClient(token, projectID)
	}

	if statusRestoreDB {
		if s3Backend == nil {
			fmt.Println("[ERROR] S3 Backend unavailable.")
			os.Exit(1)
		}
		handleStatusRestoreDB(s3Backend, clusterName, "pg", "pg-db")
		return
	}

	if createBackup {
		if s3Backend == nil {
			fmt.Println("[ERROR] S3 Backend unavailable.")
			os.Exit(1)
		}
		handleCreateBackup(s3Backend, clusterName)
		return
	}

	if cmCreateStr != "" {
		parts := strings.SplitN(cmCreateStr, ":", 3)
		if len(parts) != 3 {
			fmt.Println("[ERROR] Format error. Use: -cmcreate 'namespace:name:json_data'")
			os.Exit(1)
		}
		handleCreateConfigMap(s3Backend, clusterName, parts[0], parts[1], parts[2])
		return
	}

	if kvSecStr != "" {
		handleKVSecret(client, s3Backend, clusterName, kvSecStr)
		return
	}

	if waitCertStr != "" {
		handleWaitCert(client, s3Backend, clusterName, waitCertStr)
		return
	}

	if addUserStr != "" {
		if s3Backend == nil {
			fmt.Println("[ERROR] S3 Backend unavailable.")
			os.Exit(1)
		}
		handleAddUser(client, s3Backend, clusterName, addUserStr, ansibleForks, ansibleLimit)
		return
	}

	if osUpd {
		if s3Backend == nil {
			fmt.Println("[ERROR] S3 Backend unavailable.")
			os.Exit(1)
		}
		handleOSUpdate(client, s3Backend, clusterName, ansibleForks, ansibleLimit)
		return
	}

	if setPermissions {
		if s3Backend == nil {
			fmt.Println("[ERROR] S3 Backend unavailable.")
			os.Exit(1)
		}
		if clusterName == "default" && configPath == "" {
			fmt.Println("[ERROR] Specify -name or -config")
			os.Exit(1)
		}
		handlePermissions(s3Backend, clusterName, configPath, outputFile, ansibleForks, ansibleLimit)
		return
	}

	if deployKubespray {
		handleDeploy(client, s3Backend, clusterName, outputFile, ansibleForks, "run", ansibleLimit, "")
		return
	}
	if mountDisks {
		handleDeploy(client, s3Backend, clusterName, outputFile, ansibleForks, "mount", ansibleLimit, "")
		return
	}

	if removeK8sNode != "" {
		handleRemoveK8sNode(client, s3Backend, clusterName, outputFile, ansibleForks, removeK8sNode)
		return
	}

	switch {
	case createLB:
		handleCreateLB(client, s3Backend, clusterName, configPath)
	case attachDisks:
		handleAttachDisks(client, s3Backend, clusterName)
	case delNodePtr != "":
		handleDeleteNodeFromState(s3Backend, clusterName, delNodePtr, outputFile)
	case syncState:
		handleSync(client, s3Backend, clusterName)
	case resetPass:
		handleResetPassword(client, s3Backend, clusterName)
	case checkState:
		handleCheckState(s3Backend, jsonFormat)
	case createCluster:
		handleClusterCreateFromCode(client, outputFile, s3Backend, clusterName, forceCreate, sshPass, configPath, deleteNodes, attachDisks, noCheck)
	case cleanAll:
		handleCleanAll(client)
	case cleanDisks:
		nameFlagSet := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "name" {
				nameFlagSet = true
			}
		})
		if !nameFlagSet {
			fmt.Println("[ERROR] Security Error: -name flag is required for clean-disks.")
			os.Exit(1)
		}
		if s3Backend == nil {
			fmt.Println("[ERROR] S3 unavailable.")
			os.Exit(1)
		}
		handleCleanDisks(client, s3Backend, clusterName)
	case delPtr != "":
		handleDelete(client, delPtr)
	case addPtr != "":
		handleCreate(client, addPtr)
	default:
		flag.PrintDefaults()
	}
}
