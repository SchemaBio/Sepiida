package archiver

import (
	"context"
	"io"
)

// Backend defines the storage interface for archival destinations.
type Backend interface {
	// Upload uploads content from reader to the given key path.
	Upload(ctx context.Context, key string, reader io.Reader, size int64) error

	// Close releases any resources held by the backend.
	Close() error
}
