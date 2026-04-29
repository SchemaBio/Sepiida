package archiver

import (
	"context"
	"io"
)

// Backend defines the storage interface for archival destinations.
type Backend interface {
	// Upload uploads content from reader to the given key path.
	Upload(ctx context.Context, key string, reader io.Reader, size int64) error

	// BasePath returns the root path or URL prefix for this backend.
	// For local backends this is the absolute directory path;
	// for S3 backends this is the bucket prefix (e.g. s3://bucket/prefix).
	BasePath() string

	// Close releases any resources held by the backend.
	Close() error
}
