package archiver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	cos "github.com/tencentyun/cos-go-sdk-v5"
)

// COSBackend archives files to Tencent Cloud COS using the official SDK.
type COSBackend struct {
	client    *cos.Client
	bucket    string
	region    string
	prefix    string
	bucketURL string // https://<bucket>.cos.<region>.myqcloud.com
}

// NewCOSBackend creates a Tencent Cloud COS archive backend.
// Supported URL formats:
//
//	cos://<region>/<bucket>[/prefix]
//	https://<bucket>.cos.<region>.myqcloud.com[/prefix]
func NewCOSBackend(rawURL string, accessKeyID string, secretAccessKey string) (*COSBackend, error) {
	var bucket, region, prefix string

	if strings.HasPrefix(rawURL, "cos://") {
		// cos://ap-guangzhou/schemabio-user-1327430028/test
		parts := strings.SplitN(strings.TrimPrefix(rawURL, "cos://"), "/", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("cos:// URL must be in format: cos://<region>/<bucket>[/prefix]")
		}
		var err error
		region, err = validateArchiveComponent("region", parts[0])
		if err != nil {
			return nil, err
		}
		bucket, err = validateArchiveComponent("bucket", parts[1])
		if err != nil {
			return nil, err
		}
		if len(parts) > 2 {
			prefix = strings.TrimSuffix(parts[2], "/")
		}
	} else {
		// https://schemabio-user-1327430028.cos.ap-guangzhou.myqcloud.com/test
		u, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("invalid URL: %w", err)
		}
		host := u.Hostname()
		// host: <bucket>.cos.<region>.myqcloud.com
		parts := strings.SplitN(host, ".cos.", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid COS virtual-hosted domain: %s", host)
		}
		var componentErr error
		bucket, componentErr = validateArchiveComponent("bucket", parts[0])
		if componentErr != nil {
			return nil, componentErr
		}
		regionPart := parts[1] // ap-guangzhou.myqcloud.com
		region, componentErr = validateArchiveComponent("region", strings.TrimSuffix(regionPart, ".myqcloud.com"))
		if componentErr != nil {
			return nil, componentErr
		}
		prefix = strings.TrimPrefix(u.Path, "/")
		prefix = strings.TrimSuffix(prefix, "/")
	}

	var err error
	prefix, err = normalizeObjectPrefix(prefix)
	if err != nil {
		return nil, err
	}

	bucketURLStr := fmt.Sprintf("https://%s.cos.%s.myqcloud.com", bucket, region)
	bucketURL, err := url.Parse(bucketURLStr)
	if err != nil {
		return nil, fmt.Errorf("invalid bucket URL: %w", err)
	}

	baseURL := &cos.BaseURL{BucketURL: bucketURL}

	// Build transport with credentials
	var transport http.RoundTripper
	if accessKeyID != "" && secretAccessKey != "" {
		transport = &cos.AuthorizationTransport{
			SecretID:  accessKeyID,
			SecretKey: secretAccessKey,
		}
	} else {
		// Read from environment variables
		transport = &cos.AuthorizationTransport{
			SecretID:  firstNonEmptyEnv("TENCENT_CLOUD_SECRET_ID", "COS_SECRET_ID"),
			SecretKey: firstNonEmptyEnv("TENCENT_CLOUD_SECRET_KEY", "COS_SECRET_KEY"),
		}
	}

	client := cos.NewClient(baseURL, &http.Client{
		Timeout:   300 * time.Second,
		Transport: transport,
	})

	return &COSBackend{
		client:    client,
		bucket:    bucket,
		region:    region,
		prefix:    prefix,
		bucketURL: bucketURLStr,
	}, nil
}

func (b *COSBackend) Upload(ctx context.Context, key string, reader io.Reader, size int64) error {
	objectKey, err := joinObjectKey(b.prefix, key)
	if err != nil {
		return err
	}

	// For large files (>5MB), use multipart upload
	if size > 5*1024*1024 {
		return b.multipartUpload(ctx, objectKey, reader, size)
	}

	// For small files, use simple put
	opt := &cos.ObjectPutOptions{
		ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{
			ContentLength: size,
		},
	}
	_, err = b.client.Object.Put(ctx, objectKey, reader, opt)
	if err != nil {
		return fmt.Errorf("failed to upload %s: %w", objectKey, err)
	}
	return nil
}

// multipartUpload uploads a file using multipart upload with a temp file.
func (b *COSBackend) multipartUpload(ctx context.Context, objectKey string, reader io.Reader, size int64) error {
	// Write to temp file for multipart upload (COS SDK requires io.ReadSeeker or filepath)
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

	opt := &cos.MultiUploadOptions{
		ThreadPoolSize: 5,
		PartSize:       20,
		CheckPoint:     true,
	}

	_, _, err = b.client.Object.Upload(ctx, objectKey, tmpFile.Name(), opt)
	if err != nil {
		return fmt.Errorf("failed to multipart upload %s: %w", objectKey, err)
	}
	return nil
}

func (b *COSBackend) BasePath() string {
	if b.prefix != "" {
		return b.bucketURL + "/" + b.prefix
	}
	return b.bucketURL
}

func (b *COSBackend) Close() error {
	return nil
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}
