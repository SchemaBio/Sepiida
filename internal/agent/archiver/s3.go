package archiver

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Backend archives files to S3-compatible object storage (AWS S3, MinIO, OSS).
type S3Backend struct {
	client   *minio.Client
	bucket   string
	prefix   string
	endpoint string
	useSSL   bool
}

// ParseS3URL parses an S3/MinIO/OSS URL into endpoint, bucket, prefix, and SSL flag.
// Supported formats:
//
//	s3://bucket-name/prefix                 → AWS S3
//	oss://region/bucket-name/prefix         → Alibaba Cloud OSS
//	http://host:port/bucket/prefix          → path-style (MinIO etc.)
//	https://host:port/bucket/prefix         → path-style (MinIO etc.)
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
	case "http", "https":
		endpoint, bucket, prefix, err = parseHTTPURL(u)
		if err != nil {
			return "", "", "", false, err
		}
		useSSL = u.Scheme == "https"
	default:
		return "", "", "", false, fmt.Errorf("unsupported URL scheme: %s (use s3://, oss://, http://, or https://)", u.Scheme)
	}

	prefix = strings.TrimSuffix(prefix, "/")
	prefix, err = normalizeObjectPrefix(prefix)
	if err != nil {
		return "", "", "", false, err
	}
	bucket, err = validateArchiveComponent("bucket", bucket)
	if err != nil {
		return "", "", "", false, err
	}
	if endpoint, err = validateArchiveEndpoint(endpoint); err != nil {
		return "", "", "", false, err
	}
	return endpoint, bucket, prefix, useSSL, nil
}

// parseHTTPURL parses http(s) URLs in path-style: https://host:port/bucket/prefix
func parseHTTPURL(u *url.URL) (endpoint, bucket, prefix string, err error) {
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

// parseCloudURL parses oss:// URLs with format: scheme://region/bucket/prefix
func parseCloudURL(u *url.URL, scheme string) (region, bucket, prefix string, err error) {
	region, err = validateArchiveComponent("region", u.Host)
	if err != nil {
		return "", "", "", err
	}

	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		return "", "", "", fmt.Errorf("%s:// URL must be in format: %s://region/bucket[/prefix]", scheme, scheme)
	}
	bucket, err = validateArchiveComponent("bucket", parts[0])
	if err != nil {
		return "", "", "", err
	}
	if len(parts) > 1 {
		prefix = parts[1]
	}
	return region, bucket, prefix, nil
}

func validateArchiveEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("endpoint is required")
	}
	if len(endpoint) > 255 {
		return "", fmt.Errorf("endpoint exceeds 255 bytes")
	}
	if strings.Contains(endpoint, "/") || strings.Contains(endpoint, `\`) || strings.Contains(endpoint, "://") {
		return "", fmt.Errorf("endpoint contains unsafe path or URL characters")
	}
	if strings.ContainsFunc(endpoint, func(r rune) bool {
		return r < 0x20 || r == 0x7f || r == '@' || unicode.IsSpace(r)
	}) {
		return "", fmt.Errorf("endpoint contains unsafe characters")
	}
	return endpoint, nil
}

// NewS3Backend creates an S3-compatible archive backend.
// If accessKeyID and secretAccessKey are non-empty, they are used directly.
// Otherwise, credentials are read from environment variables:
//   - AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY (AWS S3 / MinIO)
//   - MINIO_ROOT_USER / MINIO_ROOT_PASSWORD (MinIO)
//   - ALIBABA_CLOUD_ACCESS_KEY_ID / ALIBABA_CLOUD_ACCESS_KEY_SECRET (Alibaba Cloud OSS)
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
		client:   client,
		bucket:   bucket,
		prefix:   prefix,
		endpoint: endpoint,
		useSSL:   useSSL,
	}, nil
}

func (b *S3Backend) BasePath() string {
	scheme := "https"
	if !b.useSSL {
		scheme = "http"
	}
	if b.prefix != "" {
		return scheme + "://" + b.endpoint + "/" + b.bucket + "/" + b.prefix
	}
	return scheme + "://" + b.endpoint + "/" + b.bucket
}

func (b *S3Backend) Upload(ctx context.Context, key string, reader io.Reader, size int64) error {
	objectKey, err := joinObjectKey(b.prefix, key)
	if err != nil {
		return err
	}

	_, err = b.client.PutObject(ctx, b.bucket, objectKey, reader, size, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to upload to S3 (%s/%s): %w", b.bucket, objectKey, err)
	}
	return nil
}

func (b *S3Backend) Close() error {
	return nil
}
