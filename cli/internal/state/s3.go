package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type DiskState struct {
	ID         string `json:"id"`
	Size       int    `json:"size"`
	Type       string `json:"type"`
	Bootable   bool   `json:"bootable"`
	Device     string `json:"device,omitempty"`
	MountPoint string `json:"mount_point,omitempty"`
	Owner      string `json:"owner,omitempty"`
	Group      string `json:"group,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Critical   bool   `json:"critical,omitempty"`
	Created    string `json:"created_at,omitempty"`
	Updated    string `json:"updated_at,omitempty"`
}

type NodeState struct {
	Name      string            `json:"name"`
	Role      string            `json:"role"`
	ID        string            `json:"id"`
	IP        string            `json:"ip"`
	SSHPort   int               `json:"ssh_port,omitempty"`
	AddressID string            `json:"address_id,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Taints    []string          `json:"taints,omitempty"`
	Disks     []DiskState       `json:"disks,omitempty"`
	Created   string            `json:"created_at,omitempty"`
	Updated   string            `json:"updated_at,omitempty"`
}

type ClusterState struct {
	Version     string      `json:"version"`
	LastUpdated time.Time   `json:"last_updated"`
	SSHUser     string      `json:"ssh_user"`
	Nodes       []NodeState `json:"nodes"`
}

type Backend struct {
	Client     *minio.Client
	BucketName string
	StateKey   string
}

func NewBackend(endpoint, accessKey, secretKey, bucket, key string, useSSL bool) (*Backend, error) {
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}
	return &Backend{Client: minioClient, BucketName: bucket, StateKey: key}, nil
}

func (b *Backend) SaveState(data ClusterState) error {
	ctx := context.Background()
	exists, err := b.Client.BucketExists(ctx, b.BucketName)
	if err != nil {
		return fmt.Errorf("bucket check: %w", err)
	}
	if !exists {
		b.Client.MakeBucket(ctx, b.BucketName, minio.MakeBucketOptions{})
	}
	jsonData, _ := json.MarshalIndent(data, "", "  ")
	_, err = b.Client.PutObject(ctx, b.BucketName, b.StateKey, bytes.NewReader(jsonData), int64(len(jsonData)), minio.PutObjectOptions{ContentType: "application/json"})
	return err
}

func (b *Backend) LoadState() (*ClusterState, error) {
	ctx := context.Background()
	obj, err := b.Client.GetObject(ctx, b.BucketName, b.StateKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	if _, err := obj.Stat(); err != nil {
		return nil, nil
	}
	var s ClusterState
	if err := json.NewDecoder(obj).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (b *Backend) GetPresignedURL(objectKey string, expiry time.Duration) (string, error) {
	ctx := context.Background()
	reqParams := make(url.Values)
	presignedURL, err := b.Client.PresignedGetObject(ctx, b.BucketName, objectKey, expiry, reqParams)
	if err != nil {
		return "", err
	}
	return presignedURL.String(), nil
}

func (b *Backend) ObjectExists(objectKey string) (bool, error) {
	ctx := context.Background()
	_, err := b.Client.StatObject(ctx, b.BucketName, objectKey, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (b *Backend) UploadFile(objectKey, filePath string) error {
	ctx := context.Background()
	exists, err := b.Client.BucketExists(ctx, b.BucketName)
	if err == nil && !exists {
		b.Client.MakeBucket(ctx, b.BucketName, minio.MakeBucketOptions{})
	}
	info, err := b.Client.FPutObject(ctx, b.BucketName, objectKey, filePath, minio.PutObjectOptions{
		ContentType: "application/x-tar",
	})
	if err != nil {
		return err
	}
	fmt.Printf("      Successfully uploaded %s of size %d\n", objectKey, info.Size)
	return nil
}
