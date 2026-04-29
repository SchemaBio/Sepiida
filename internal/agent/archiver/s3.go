package archiver

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Backend archives files to S3-compatible object storage.
type S3Backend struct {
	client *minio.Client
	bucket string
	prefix string
}

// ParseS3URL parses an object storage URL into endpoint, bucket, prefix, and SSL flag.
// Supported formats:
//
//	s3://bucket-name/prefix                                      → AWS S3
//	http://host:port/bucket/prefix                               → path-style (MinIO etc.)
//	https://host:port/bucket/prefix                              → path-style (MinIO etc.)
//	oss://region/bucket-name/prefix                              → Alibaba Cloud OSS
//	cos://region/bucket-name/prefix                              → Tencent Cloud COS (short URL)
//	https://<BucketName-APPID>.cos.<Region>.myqcloud.com/prefix  → Tencent Cloud COS (virtual-hosted)
//	https://cos.<Region>.myqcloud.com/<BucketName-APPID>/prefix  → Tencent Cloud COS (path-style)
func ParseS3URL(rawURL string) (endpoint, bucket, prefix string, useSSL bool, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", "", false, fmt.Errorf("invalid URL: %w", err)
	}

	switch u.Scheme {
	case "s3":
		endpoint = "s3.amazonaws.com"
		bucket = u.Host
		prefix = strings.TrimPrefix(u.Path, "/")
		useSSL = true
	case "oss":
		region, bkt, pre, err := parseCloudURL(u, "oss")
		if err != nil {
			return "", "", "", false, err
		}
		endpoint = "oss-" + region + ".aliyuncs.com"
		bucket = bkt
		prefix = pre
		useSSL = true
	case "cos":
		region, bkt, pre, err := parseCloudURL(u, "cos")
		if err != nil {
			return "", "", "", false, err
		}
		endpoint = "cos." + region + ".myqcloud.com"
		bucket = bkt
		prefix = pre
		useSSL = true
	case "http", "https":
		endpoint, bucket, prefix, err = parseHTTPURL(u)
		if err != nil {
			return "", "", "", false, err
		}
		useSSL = u.Scheme == "https"
	default:
		return "", "", "", false, fmt.Errorf("unsupported URL scheme: %s (use s3://, oss://, cos://, http://, or https://)", u.Scheme)
	}

	prefix = strings.TrimSuffix(prefix, "/")
	return endpoint, bucket, prefix, useSSL, nil
}

// parseHTTPURL parses http(s) URLs, auto-detecting Tencent Cloud COS virtual-hosted domains.
//
// COS virtual-hosted: https://<BucketName-APPID>.cos.<Region>.myqcloud.com/prefix
//
//	endpoint: cos.<Region>.myqcloud.com, bucket: <BucketName-APPID>, prefix: prefix
//
// Path-style (MinIO, COS, etc.): https://host:port/bucket/prefix
//
//	endpoint: host:port, bucket: bucket, prefix: prefix
func parseHTTPURL(u *url.URL) (endpoint, bucket, prefix string, err error) {
	host := u.Hostname()

	// Detect COS virtual-hosted: <BucketName-APPID>.cos.<Region>.myqcloud.com
	if strings.HasSuffix(host, ".myqcloud.com") && strings.Contains(host, ".cos.") {
		return parseCOSVirtualHosted(u)
	}

	// Default: path-style — first path segment is the bucket
	endpoint = u.Host
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		return "", "", "", fmt.Errorf("bucket name required in URL path")
	}
	bucket = parts[0]
	if len(parts) > 1 {
		prefix = parts[1]
	}
	return endpoint, bucket, prefix, nil
}

// parseCOSVirtualHosted parses COS virtual-hosted URLs.
// Format: https://<BucketName-APPID>.cos.<Region>.myqcloud.com[/prefix]
func parseCOSVirtualHosted(u *url.URL) (endpoint, bucket, prefix string, err error) {
	host := u.Hostname()
	// host: mybucket-1250000000.cos.ap-guangzhou.myqcloud.com
	parts := strings.SplitN(host, ".cos.", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf("invalid COS virtual-hosted domain: %s", host)
	}
	bucket = parts[0]            // mybucket-1250000000
	endpoint = "cos." + parts[1] // cos.ap-guangzhou.myqcloud.com
	prefix = strings.TrimPrefix(u.Path, "/")
	return endpoint, bucket, prefix, nil
}

// parseCloudURL parses oss:// and cos:// URLs with format: scheme://region/bucket/prefix
func parseCloudURL(u *url.URL, scheme string) (region, bucket, prefix string, err error) {
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf("%s:// URL must be in format: %s://region/bucket[/prefix]", scheme, scheme)
	}
	region = parts[0]
	bucket = parts[1]
	if len(parts) > 2 {
		prefix = parts[2]
	}
	return region, bucket, prefix, nil
}

// NewS3Backend creates an S3-compatible archive backend.
// If accessKeyID and secretAccessKey are non-empty, they are used directly.
// Otherwise, credentials are read from environment variables:
//   - AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY (AWS S3 / MinIO)
//   - MINIO_ROOT_USER / MINIO_ROOT_PASSWORD (MinIO)
//   - ALIBABA_CLOUD_ACCESS_KEY_ID / ALIBABA_CLOUD_ACCESS_KEY_SECRET (Alibaba Cloud OSS)
//   - TENCENT_CLOUD_SECRET_ID / TENCENT_CLOUD_SECRET_KEY (Tencent Cloud COS)
func NewS3Backend(rawURL string, accessKeyID string, secretAccessKey string) (*S3Backend, error) {
	endpoint, bucket, prefix, useSSL, err := ParseS3URL(rawURL)
	if err != nil {
		return nil, err
	}

	var creds *credentials.Credentials
	if accessKeyID != "" && secretAccessKey != "" {
		creds = credentials.NewStatic(accessKeyID, secretAccessKey, "", credentials.SignatureDefault)
	} else {
		creds = credentials.NewEnvAWS()
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  creds,
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	return &S3Backend{
		client: client,
		bucket: bucket,
		prefix: prefix,
	}, nil
}

func (b *S3Backend) Upload(ctx context.Context, key string, reader io.Reader, size int64) error {
	objectKey := key
	if b.prefix != "" {
		objectKey = b.prefix + "/" + key
	}

	_, err := b.client.PutObject(ctx, b.bucket, objectKey, reader, size, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to upload to S3 (%s/%s): %w", b.bucket, objectKey, err)
	}
	return nil
}

func (b *S3Backend) Close() error {
	return nil
}
