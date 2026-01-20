package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/template"
	"time"

	"cli/internal/ycloud"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

const TestNamespace = "saleor-stress-test"

const DefaultK6Template = `apiVersion: k6.io/v1alpha1
kind: TestRun
metadata:
  name: saleor-load
  namespace: {{ .Namespace }}
spec:
  parallelism: 2
  script:
    configMap:
      name: test-script
      file: test.js
  runner:
    tolerations:
      - key: "dedicated"
        operator: "Equal"
        value: "k6"
        effect: "NoSchedule"
    affinity:
      nodeAffinity:
        requiredDuringSchedulingIgnoredDuringExecution:
          nodeSelectorTerms:
          - matchExpressions:
            - key: role
              operator: In
              values:
              - load-generator
    env:
      - name: TARGET_DOMAIN
        value: "{{ .Target }}"
`

type TemplateData struct {
	Namespace string
	Target    string
}

func handleYCloudGetKubeconfig(savePath string) {
	fmt.Println("[GO] --- YCLOUD: GET KUBECONFIG MODE ---")
	token := os.Getenv("YCLOUD_TOKEN")
	folderID := os.Getenv("YCLOUD_FOLDER_ID")

	if token == "" || folderID == "" {
		fmt.Println("[ERROR] Error: Set environment variables: YCLOUD_TOKEN, YCLOUD_FOLDER_ID")
		os.Exit(1)
	}

	client := ycloud.NewClient(token, folderID)
	cloudCfg := GetYCloudConfig()
	clusterName := cloudCfg.ClusterName

	fmt.Printf("[SEARCH] Searching for cluster '%s'...\n", clusterName)
	clusterID, err := client.FindClusterIDByName(clusterName)
	if err != nil {
		fmt.Printf("[ERROR] Search API error: %v\n", err)
		return
	}
	if clusterID == "" {
		fmt.Println("[ERROR] Cluster not found. Create it first using -hload.")
		return
	}

	fmt.Printf("   -> Found Cluster ID: %s\n", clusterID)
	fmt.Println("[KEY] Downloading Kubeconfig...")
	cfgData, err := client.GetKubeconfig(clusterID)
	if err != nil {
		fmt.Printf("[ERROR] Failed to get config: %v\n", err)
		return
	}

	fmt.Printf("[SAVE] Saving to: %s\n", savePath)
	if err := os.WriteFile(savePath, cfgData, 0600); err != nil {
		fmt.Printf("[ERROR] File write error: %v\n", err)
		return
	}
	fmt.Println("[+OK+] Success.")
}

// --- UPDATED SIGNATURE ---
func handleYCloudLoadTest(targetDomain, saveKubeconfigPath, scriptPath string) {
	fmt.Println("[GO] --- YCLOUD CLOUD LOAD TESTING MODE (Native Go Client) ---")

	token := os.Getenv("YCLOUD_TOKEN")
	folderID := os.Getenv("YCLOUD_FOLDER_ID")
	saID := os.Getenv("YCLOUD_SA_ID")

	if token == "" || folderID == "" || saID == "" {
		fmt.Println("[ERROR] Error: Set environment variables: YCLOUD_TOKEN, YCLOUD_FOLDER_ID, YCLOUD_SA_ID")
		os.Exit(1)
	}

	cloudCfg := GetYCloudConfig()

	client := ycloud.NewClient(token, folderID)
	zone := "ru-central1-d"
	clusterName := cloudCfg.ClusterName

	fmt.Println("[SEARCH] Searching for network and subnet...")
	netID, subID, err := client.GetDefaultNetworkAndSubnet(zone)
	if err != nil {
		fmt.Printf("[ERROR] Network search error: %v\n", err)
		return
	}
	fmt.Printf("   [+OK+] Using Network: %s | Subnet: %s\n", netID, subID)

	clusterID, err := client.EnsureCluster(clusterName, saID, zone, netID, subID)
	if err != nil {
		fmt.Printf("[ERROR] Cluster creation error: %v\n", err)
		return
	}

	fmt.Println("[KEY] Getting Kubeconfig...")
	cfgData, err := client.GetKubeconfig(clusterID)
	if err != nil {
		fmt.Printf("[ERROR] Config error: %v\n", err)
		return
	}

	if saveKubeconfigPath != "" {
		fmt.Printf("[SAVE] Kubeconfig will be saved to: %s\n", saveKubeconfigPath)
		if err := os.WriteFile(saveKubeconfigPath, cfgData, 0600); err != nil {
			fmt.Printf("[ERROR] File write error: %v\n", err)
			return
		}
	}

	fmt.Println("[T] Ensuring Node Groups from config...")
	for _, group := range cloudCfg.NodeGroups {
		var taints []ycloud.TaintSpec
		for _, t := range group.Taints {
			taints = append(taints, ycloud.TaintSpec{
				Key:    t.Key,
				Value:  t.Value,
				Effect: t.Effect,
			})
		}

		spec := ycloud.NodeGroupSpec{
			Name:     group.Name,
			Replicas: group.Replicas,
			CPU:      group.CPU,
			RAM:      int64(group.RAM) * 1024 * 1024 * 1024,
			DiskSize: int64(group.DiskSize) * 1024 * 1024 * 1024,
			PublicIP: group.PublicIP,
			Labels:   group.Labels,
			Taints:   taints,
		}

		if err := client.EnsureNodeGroup(clusterID, zone, subID, spec); err != nil {
			fmt.Printf("[ERROR] Failed to ensure group %s: %v\n", group.Name, err)
			return
		}
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(cfgData)
	if err != nil {
		fmt.Printf("[ERROR] REST config error: %v\n", err)
		return
	}

	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		fmt.Printf("[ERROR] Dynamic client error: %v\n", err)
		return
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		fmt.Printf("[ERROR] Clientset error: %v\n", err)
		return
	}

	dc, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		fmt.Printf("[ERROR] Discovery client error: %v\n", err)
		return
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))

	fmt.Println("[PACKAGE] Installing K6 Operator via client-go...")
	k6BundleURL := "https://raw.githubusercontent.com/grafana/k6-operator/main/bundle.yaml"
	if err := applyManifestsFromURL(k6BundleURL, dynClient, mapper); err != nil {
		fmt.Printf("[WARNING] Operator install partial error (might exist): %v\n", err)
	}

	fmt.Println("[TIME] Waiting for CRD registration (testruns.k6.io)...")
	if err := waitForCRD(dynClient, "testruns.k6.io"); err != nil {
		fmt.Printf("[ERROR] CRD wait failed: %v\n", err)
		return
	}
	fmt.Println("[+OK+] CRD is ready.")

	fmt.Printf("[FILE_FOLDER] Creating Namespace '%s'...\n", TestNamespace)
	nsSpec := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: TestNamespace}}
	_, err = clientset.CoreV1().Namespaces().Create(context.TODO(), nsSpec, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		fmt.Printf("[ERROR] Namespace creation failed: %v\n", err)
		return
	}

	// 6. Upload scripts (ConfigMap)
	// --- UPDATED: Pass scriptPath ---
	if err := createK6ConfigMap(clientset, scriptPath); err != nil {
		fmt.Printf("[ERROR] ConfigMap creation error: %v\n", err)
		return
	}

	fmt.Printf("\n[BOO] STARTING TEST AGAINST: %s\n", targetDomain)

	templateFile := "k6_template.yaml"

	if _, err := os.Stat(templateFile); os.IsNotExist(err) {
		fmt.Printf("[INFO] Creating default template '%s'...\n", templateFile)
		os.WriteFile(templateFile, []byte(DefaultK6Template), 0644)
	}

	tmpl, err := template.ParseFiles(templateFile)
	if err != nil {
		fmt.Printf("[ERROR] Template parse error: %v\n", err)
		return
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, TemplateData{Namespace: TestNamespace, Target: targetDomain}); err != nil {
		fmt.Printf("[ERROR] YAML gen error: %v\n", err)
		return
	}

	gvr := schema.GroupVersionResource{Group: "k6.io", Version: "v1alpha1", Resource: "testruns"}

	fmt.Println("[K8S] Cleaning up old TestRun...")
	err = dynClient.Resource(gvr).Namespace(TestNamespace).Delete(context.TODO(), "saleor-load", metav1.DeleteOptions{})
	if err == nil {
		time.Sleep(3 * time.Second)
	}

	var testRunObj unstructured.Unstructured
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(rendered.Bytes()), 4096)
	if err := decoder.Decode(&testRunObj); err != nil {
		fmt.Printf("[ERROR] JSON/YAML decode error: %v\n", err)
		return
	}

	fmt.Println("[K8S] Creating new TestRun object...")
	_, err = dynClient.Resource(gvr).Namespace(TestNamespace).Create(context.TODO(), &testRunObj, metav1.CreateOptions{})
	if err != nil {
		fmt.Printf("[ERROR] TestRun creation error: %v\n", err)
		return
	}

	fmt.Println("\n[+OK+] Test successfully launched!")

	fmt.Println("[SATELLITE] Connecting to logs (Ctrl+C to interrupt)...")

	fmt.Print("[TIME] Waiting for INITIALIZER pod...")
	initPodName, err := waitForPod(clientset, TestNamespace, "k6_cr=saleor-load", "initializer")
	if err != nil {
		fmt.Printf("\n[WARNING] Could not find initializer (it might have finished quickly): %v\n", err)
	} else {
		fmt.Printf("\n[LOGS] Streaming INITIALIZER logs (%s)...\n", initPodName)
		fmt.Println("---------------------------------------------------")
		streamPodLogs(clientset, TestNamespace, initPodName)
		fmt.Println("\n---------------------------------------------------")
		fmt.Println("[+OK+] Initializer finished.")
	}

	fmt.Print("[TIME] Waiting for RUNNER pod...")
	runnerPodName, err := waitForPod(clientset, TestNamespace, "k6_cr=saleor-load", "runner")
	if err != nil {
		fmt.Printf("\n[ERROR] Could not find runner pod: %v\n", err)
		return
	}

	fmt.Printf("\n[LOGS] Streaming RUNNER logs (%s)...\n", runnerPodName)
	fmt.Println("===================================================")
	streamPodLogs(clientset, TestNamespace, runnerPodName)
	fmt.Println("\n===================================================")
	fmt.Println("[FINISH] Test execution completed.")
}

func waitForPod(clientset *kubernetes.Clientset, ns, selector, filterType string) (string, error) {
	for i := 0; i < 60; i++ {
		pods, err := clientset.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{
			LabelSelector: selector,
		})
		if err == nil && len(pods.Items) > 0 {
			for _, p := range pods.Items {
				if p.Status.Phase != corev1.PodRunning && p.Status.Phase != corev1.PodPending && p.Status.Phase != corev1.PodSucceeded {
					continue
				}

				isInit := strings.Contains(p.Name, "initializer")

				if filterType == "initializer" && isInit {
					return p.Name, nil
				}
				if filterType == "runner" && !isInit {
					return p.Name, nil
				}
			}
		}
		time.Sleep(2 * time.Second)
		fmt.Print(".")
	}
	return "", fmt.Errorf("timeout waiting for %s pod", filterType)
}

// --- UPDATED SIGNATURE: takes filePath ---
func createK6ConfigMap(clientset *kubernetes.Clientset, filePath string) error {
	fmt.Printf("[NOTE] Reading script from %s...\n", filePath)
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read '%s'. Ensure you are running from project root. Error: %w", filePath, err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-script",
			Namespace: TestNamespace,
		},
		Data: map[string]string{
			"test.js": string(content),
		},
	}

	cmClient := clientset.CoreV1().ConfigMaps(TestNamespace)

	_ = cmClient.Delete(context.TODO(), "test-script", metav1.DeleteOptions{})

	fmt.Println("[K8S] Creating ConfigMap 'test-script'...")
	_, err = cmClient.Create(context.TODO(), cm, metav1.CreateOptions{})
	return err
}

func applyManifestsFromURL(url string, dynClient dynamic.Interface, mapper meta.RESTMapper) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		var rawObj unstructured.Unstructured
		if err := decoder.Decode(&rawObj); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("yaml decode failed: %w", err)
		}

		if len(rawObj.Object) == 0 {
			continue
		}

		gvk := rawObj.GroupVersionKind()
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			fmt.Printf("   [SKIP] Unknown resource type %s: %v\n", gvk.Kind, err)
			continue
		}

		var dr dynamic.ResourceInterface
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			ns := rawObj.GetNamespace()
			if ns == "" {
				ns = "default"
			}
			dr = dynClient.Resource(mapping.Resource).Namespace(ns)
		} else {
			dr = dynClient.Resource(mapping.Resource)
		}

		_, err = dr.Create(context.TODO(), &rawObj, metav1.CreateOptions{})
		if err != nil {
			if errors.IsAlreadyExists(err) {
				existing, getErr := dr.Get(context.TODO(), rawObj.GetName(), metav1.GetOptions{})
				if getErr == nil {
					rawObj.SetResourceVersion(existing.GetResourceVersion())
					_, err = dr.Update(context.TODO(), &rawObj, metav1.UpdateOptions{})
				}
			}
		}
		if err != nil {
			fmt.Printf("   [WARN] Failed to apply %s/%s: %v\n", gvk.Kind, rawObj.GetName(), err)
		}
	}
	return nil
}

func waitForCRD(dynClient dynamic.Interface, crdName string) error {
	gvr := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	timeout := time.After(60 * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for CRD %s", crdName)
		case <-ticker.C:
			_, err := dynClient.Resource(gvr).Get(context.TODO(), crdName, metav1.GetOptions{})
			if err == nil {
				return nil
			}
		}
	}
}

func streamPodLogs(clientset *kubernetes.Clientset, ns, podName string) {
	var stream io.ReadCloser
	var err error

	for i := 0; i < 45; i++ {
		req := clientset.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{
			Follow: true,
		})

		stream, err = req.Stream(context.TODO())
		if err == nil {
			break
		}

		if i == 0 {
			fmt.Print("   ... waiting for container start")
		} else if i%5 == 0 {
			fmt.Print(".")
		}
		time.Sleep(2 * time.Second)
	}
	fmt.Println("")

	if err != nil {
		fmt.Printf("[ERROR] Stream open error (timeout): %v\n", err)
		return
	}
	defer stream.Close()

	reader := bufio.NewReader(stream)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				fmt.Printf("[END] Log stream ended: %v\n", err)
			}
			break
		}
		fmt.Print(line)
	}
}

func handleYCloudDelete() {
	fmt.Println("[GO] --- YCLOUD CLOUD DELETION MODE ---")

	token := os.Getenv("YCLOUD_TOKEN")
	folderID := os.Getenv("YCLOUD_FOLDER_ID")
	if token == "" || folderID == "" {
		fmt.Println("[ERROR] Error: Set YCLOUD_TOKEN and YCLOUD_FOLDER_ID")
		os.Exit(1)
	}

	client := ycloud.NewClient(token, folderID)
	clusterName := "k6-load-cluster"

	fmt.Printf("[SEARCH] Searching for cluster '%s'...\n", clusterName)
	clusterID, err := client.FindClusterIDByName(clusterName)
	if err != nil {
		fmt.Printf("[ERROR] Search error: %v\n", err)
		return
	}

	if clusterID != "" {
		fmt.Printf("   -> Found ID: %s\n", clusterID)
		ngs, err := client.FindNodeGroupsByClusterID(clusterID)
		if err != nil {
			fmt.Printf("[ERROR] Node list error: %v\n", err)
		} else {
			if len(ngs) > 0 {
				fmt.Printf("[FIRE] Found node groups: %d. Deleting...\n", len(ngs))
				for _, ng := range ngs {
					fmt.Printf("   -> Deleting group: %s (%s)\n", ng.Name, ng.ID)
					if err := client.DeleteNodeGroup(ng.ID); err != nil {
						fmt.Printf("[ERROR] Failed to delete group %s: %v\n", ng.Name, err)
					}
				}
			}
		}
		fmt.Println("[FIRE] Deleting Managed K8s cluster...")
		if err := client.DeleteCluster(clusterID); err != nil {
			fmt.Printf("[ERROR] Cluster deletion error: %v\n", err)
		}
	} else {
		fmt.Println("[STAR] Cluster not found (already deleted).")
	}

	fmt.Println("\n[BROOM] Cleaning up network resources...")
	time.Sleep(5 * time.Second)

	targetNetName := "panel-man"
	targetSubName := "subnetwork-kman"

	subID, _ := client.FindSubnetIDByName(targetSubName)
	if subID != "" {
		if err := client.DeleteSubnet(subID); err != nil {
			fmt.Printf("[ERROR] Subnet deletion error %s: %v\n", targetSubName, err)
		} else {
			fmt.Printf("[+OK+] Subnet %s deleted.\n", targetSubName)
		}
	} else {
		fmt.Printf("[STAR] Subnet %s not found.\n", targetSubName)
	}

	netID, _ := client.FindNetworkIDByName(targetNetName)
	if netID != "" {
		if err := client.DeleteNetwork(netID); err != nil {
			fmt.Printf("[ERROR] Network deletion error %s: %v\n", targetNetName, err)
		} else {
			fmt.Printf("[+OK+] Network %s deleted.\n", targetNetName)
		}
	} else {
		fmt.Printf("[STAR] Network %s not found.\n", targetNetName)
	}

	os.Remove("k6_kubeconfig.yaml")
	fmt.Println("\n[+OK+][+OK+][+OK+] Success! All resources cleaned up.")
}
