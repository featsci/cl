package main

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// --- CONFIGURATION ---

const (
	BucketName        = "images"
	KubesprayImageRef = "quay.io/kubespray/kubespray:v2.29.1"
	FluxImageRef      = "ghcr.io/fluxcd/flux-cli:v2.3.0"
)

var K8sImagesList = []string{
	"registry.k8s.io/kube-apiserver:v1.31.1",
	"registry.k8s.io/kube-controller-manager:v1.31.1",
	"registry.k8s.io/kube-scheduler:v1.31.1",
	"registry.k8s.io/kube-proxy:v1.31.1",
	"registry.k8s.io/pause:3.9",
	"registry.k8s.io/etcd:3.5.15-0",
	"registry.k8s.io/coredns/coredns:v1.11.3",
	"quay.io/calico/node:v3.28.1",
	"quay.io/calico/cni:v3.28.1",
	"quay.io/calico/kube-controllers:v3.28.1",
	"quay.io/calico/apiserver:v3.28.1",
	"registry.k8s.io/dns/k8s-dns-node-cache:1.22.28",
	"registry.k8s.io/ingress-nginx/controller:v1.11.2",
	"registry.k8s.io/metrics-server/metrics-server:v0.7.2",
	"quay.io/calico/node:v3.30.5",
}

// ArtifactsManager
type ArtifactsManager struct {
	Client   *minio.Client
	Endpoint string
	Bucket   string
}

// Docker archive manifest structure
type DockerManifestEntry struct {
	Config   string   `json:"Config"`
	RepoTags []string `json:"RepoTags"`
	Layers   []string `json:"Layers"`
}

// NewArtifactsManager
func NewArtifactsManager(endpoint, access, secret string, secure bool) (*ArtifactsManager, error) {
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(access, secret, ""),
		Secure: secure,
	})
	if err != nil {
		return nil, err
	}
	return &ArtifactsManager{
		Client:   minioClient,
		Endpoint: endpoint,
		Bucket:   BucketName,
	}, nil
}

// EnsureK8sBundleInS3 - Wrapper for calling from main.go (-k8simages flag)
func EnsureK8sBundleInS3(endpoint, access, secret string) (string, error) {
	am, err := NewArtifactsManager(endpoint, access, secret, true)
	if err != nil {
		return "", err
	}

	// Create bucket if it doesn't exist
	ctx := context.Background()
	exists, err := am.Client.BucketExists(ctx, am.Bucket)
	if err == nil && !exists {
		am.Client.MakeBucket(ctx, am.Bucket, minio.MakeBucketOptions{})
	}

	key := "k8s_images.tar"
	fmt.Println("[PACKAGE] Forming image bundle...")
	if err := am.ensureDockerBundle(K8sImagesList, key); err != nil {
		return "", err
	}
	return am.GetPresignedURL(key)
}

// StartBackgroundSync
func StartBackgroundSync(endpoint, access, secret string) {
	am, err := NewArtifactsManager(endpoint, access, secret, true)
	if err != nil {
		fmt.Printf("[WARNING] [Artifacts] S3 initialization error: %v\n", err)
		return
	}

	go func() {
		fmt.Println("[PACKAGE] [Background] Starting artifact preparation (Kubespray, Flux, K8s Images)...")
		if err := am.SyncAll(); err != nil {
			fmt.Printf("[ERROR] [Background] Artifact synchronization error: %v\n", err)
		} else {
			fmt.Println("[STAR] [Background] All artifacts successfully uploaded to S3!")
		}
	}()
}

// SyncAll
func (am *ArtifactsManager) SyncAll() error {
	ctx := context.Background()
	exists, err := am.Client.BucketExists(ctx, am.Bucket)
	if err != nil {
		return fmt.Errorf("check bucket: %w", err)
	}
	if !exists {
		fmt.Printf("   [CLOUD] Creating bucket '%s'...\n", am.Bucket)
		if err := am.Client.MakeBucket(ctx, am.Bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("make bucket: %w", err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(3)
	errChan := make(chan error, 3)

	go func() {
		defer wg.Done()
		if err := am.ensureRootFS(KubesprayImageRef, "kubespray.tar"); err != nil {
			errChan <- fmt.Errorf("kubespray: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		if err := am.ensureRootFS(FluxImageRef, "flux.tar"); err != nil {
			errChan <- fmt.Errorf("flux: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		if err := am.ensureDockerBundle(K8sImagesList, "k8s_images.tar"); err != nil {
			errChan <- fmt.Errorf("k8s_bundle: %v", err)
		}
	}()

	wg.Wait()
	close(errChan)

	for e := range errChan {
		if e != nil {
			return e
		}
	}
	return nil
}

// GetPresignedURL
func (am *ArtifactsManager) GetPresignedURL(key string) (string, error) {
	ctx := context.Background()
	u, err := am.Client.PresignedGetObject(ctx, am.Bucket, key, 2*time.Hour, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (am *ArtifactsManager) ensureRootFS(imgRef, key string) error {
	ctx := context.Background()
	_, err := am.Client.StatObject(ctx, am.Bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return nil
	}

	fmt.Printf("   [DOWNLOAD] Downloading %s (RootFS)...\n", imgRef)
	tmpFile := "temp_" + key
	defer os.Remove(tmpFile)

	if err := downloadImageToFsTar(imgRef, tmpFile); err != nil {
		return err
	}

	fmt.Printf("   [CLOUD] Uploading %s to S3...\n", key)
	_, err = am.Client.FPutObject(ctx, am.Bucket, key, tmpFile, minio.PutObjectOptions{ContentType: "application/x-tar"})
	return err
}

// --- NEW WRAPPER FUNCTION ---
func ListImagesInS3(endpoint, access, secret string) error {
	am, err := NewArtifactsManager(endpoint, access, secret, true)
	if err != nil {
		return err
	}
	return am.ListBundleContent("k8s_images.tar")
}

// --- CLASS METHOD ---
func (am *ArtifactsManager) ListBundleContent(key string) error {
	ctx := context.Background()

	// Check if the file exists
	_, err := am.Client.StatObject(ctx, am.Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return fmt.Errorf("file %s not found in bucket %s", key, am.Bucket)
	}

	// Get object stream
	obj, err := am.Client.GetObject(ctx, am.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("error getting object: %w", err)
	}
	defer obj.Close()

	// Read TAR on the fly
	tr := tar.NewReader(obj)
	foundManifest := false

	fmt.Println("[TIME] Reading stream (this may take a few seconds)...")

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read error: %w", err)
		}

		// Docker always puts manifest.json file at the root of the archive
		if header.Name == "manifest.json" {
			var manifest []DockerManifestEntry
			if err := json.NewDecoder(tr).Decode(&manifest); err != nil {
				return fmt.Errorf("manifest.json parsing error: %w", err)
			}

			fmt.Println("\n[PACKAGE] Contents of k8s_images.tar:")
			fmt.Println("---------------------------------------------------")
			count := 0
			for _, entry := range manifest {
				for _, tag := range entry.RepoTags {
					fmt.Printf(" • %s\n", tag)
					count++
				}
			}
			fmt.Println("---------------------------------------------------")
			fmt.Printf("Total images: %d\n", count)
			foundManifest = true
			break // We found what we were looking for, no need to read gigabytes of layers
		}
	}

	if !foundManifest {
		return fmt.Errorf("manifest.json file not found inside the archive (is the archive corrupted?)")
	}

	return nil
}

// CORRECTED FUNCTION:
func (am *ArtifactsManager) ensureDockerBundle(images []string, key string) error {
	ctx := context.Background()

	// --- REMOVE OR COMMENT OUT THE CHECK ---
	// _, err := am.Client.StatObject(ctx, am.Bucket, key, minio.StatObjectOptions{})
	// if err == nil {
	// 	 return nil
	// }
	// ----------------------------------------

	// Now it will ALWAYS rebuild the bundle.
	// Yes, this will take time on every -cluster run,
	// but it guarantees the list is up-to-date.

	fmt.Printf("   [DOWNLOAD] Building K8s bundle (%d images)... (Force Update)\n", len(images))
	tmpFile := "temp_" + key
	defer os.Remove(tmpFile)

	if err := downloadImagesToBundle(images, tmpFile); err != nil {
		return err
	}

	fmt.Printf("   [CLOUD] Uploading %s to S3...\n", key)
	_, err := am.Client.FPutObject(ctx, am.Bucket, key, tmpFile, minio.PutObjectOptions{ContentType: "application/x-tar"})
	return err
}

// Low-level

func downloadImageToFsTar(imgRef, destFile string) error {
	ref, err := name.ParseReference(imgRef)
	if err != nil {
		return err
	}
	img, err := remote.Image(ref)
	if err != nil {
		return err
	}
	outFile, err := os.Create(destFile)
	if err != nil {
		return err
	}
	defer outFile.Close()

	tarStream := mutate.Extract(img)
	defer tarStream.Close()

	_, err = io.Copy(outFile, tarStream)
	return err
}

func downloadImagesToBundle(images []string, destFile string) error {
	imgMap := make(map[name.Tag]v1.Image)
	for _, imgStr := range images {
		ref, err := name.ParseReference(imgStr)
		if err != nil {
			return fmt.Errorf("bad ref %s: %w", imgStr, err)
		}
		img, err := remote.Image(ref)
		if err != nil {
			return fmt.Errorf("pull %s: %w", imgStr, err)
		}
		tag, ok := ref.(name.Tag)
		if !ok {
			continue
		}
		imgMap[tag] = img
	}

	outFile, err := os.Create(destFile)
	if err != nil {
		return err
	}
	defer outFile.Close()

	return tarball.MultiWrite(imgMap, outFile)
}
