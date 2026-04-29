package archiver

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// OSSBackend archives files to Alibaba Cloud OSS using the official SDK.
type OSSBackend struct {
	client   *oss.Client
	bucket   *oss.Bucket
	bucketName string
	prefix   string
	endpoint string // e.g. oss-cn-guangzhou.aliyuncs.com
}

// NewOSSBackend creates an Alibaba Cloud OSS archive backend.
// Supported URL formats:
//
//	oss://<region>/<bucket>[/prefix]                          e.g. oss://cn-guangzhou/my-bucket/prefix
//	https://<bucket>.oss-<region>.aliyuncs.com[/prefix]       e.g. https://my-bucket.oss-cn-guangzhou.aliyuncs.com/prefix
func NewOSSBackend(rawURL string, accessKeyID string, secretAccessKey string) (*OSSBackend, error) {
	var bucketName, region, prefix string

	if strings.HasPrefix(rawURL, "oss://") {
		// oss://cn-guangzhou/my-bucket/prefix
		parts := strings.SplitN(strings.TrimPrefix(rawURL, "oss://"), "/", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("oss:// URL must be in format: oss://<region>/<bucket>[/prefix]")
		}
		region = parts[0]
		bucketName = parts[1]
		if len(parts) > 2 {
			prefix = strings.TrimSuffix(parts[2], "/")
		}
	} else {
		// https://my-bucket.oss-cn-guangzhou.aliyuncs.com/prefix
		host := strings.SplitN(strings.TrimPrefix(rawURL, "https://"), "/", 2)[0]
		// host: <bucket>.oss-<region>.aliyuncs.com
		parts := strings.SplitN(host, ".oss-", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid OSS virtual-hosted domain: %s", host)
		}
		bucketName = parts[0]
		regionPart := parts[1] // cn-guangzhou.aliyuncs.com
		region = strings.TrimSuffix(regionPart, ".aliyuncs.com")
		path := strings.TrimPrefix(strings.SplitN(rawURL, host, 2)[1], "/")
		prefix = strings.TrimSuffix(path, "/")
	}

	endpoint := "oss-" + region + ".aliyuncs.com"

	// Read credentials from parameters or environment variables
	keyID := accessKeyID
	keySecret := secretAccessKey
	if keyID == "" {
		keyID = os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID")
	}
	if keySecret == "" {
		keySecret = os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET")
	}

	client, err := oss.New(endpoint, keyID, keySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to create OSS client: %w", err)
	}

	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return nil, fmt.Errorf("failed to get OSS bucket %s: %w", bucketName, err)
	}

	return &OSSBackend{
		client:     client,
		bucket:     bucket,
		bucketName: bucketName,
		prefix:     prefix,
		endpoint:   endpoint,
	}, nil
}

func (b *OSSBackend) Upload(ctx context.Context, key string, reader io.Reader, size int64) error {
	objectKey := key
	if b.prefix != "" {
		objectKey = b.prefix + "/" + key
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// For large files (>5MB), use multipart upload from temp file
	if size > 5*1024*1024 {
		return b.multipartUpload(ctx, objectKey, reader, size)
	}

	return b.bucket.PutObject(objectKey, reader)
}

func (b *OSSBackend) multipartUpload(ctx context.Context, objectKey string, reader io.Reader, size int64) error {
	tmpFile, err := os.CreateTemp("", "sepiida-upload-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, reader); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	return b.bucket.UploadFile(objectKey, tmpFile.Name(), 10*1024*1024,
		oss.Routines(5), oss.Checkpoint(true, ""))
}

func (b *OSSBackend) BasePath() string {
	// Virtual-hosted: https://<bucket>.oss-<region>.aliyuncs.com
	host := b.bucketName + "." + b.endpoint
	if b.prefix != "" {
		return "https://" + host + "/" + b.prefix
	}
	return "https://" + host
}

func (b *OSSBackend) Close() error {
	return nil
}
