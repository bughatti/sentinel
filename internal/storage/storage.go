// Package storage defines the Storage interface and path helpers for Sentinel
// NVR media files.
package storage

import (
	"context"
	"time"
)

// Storage is the media-file abstraction used throughout Sentinel. The local
// filesystem implementation is in local.go; object-store implementations
// (S3, GCS) can follow the same interface.
type Storage interface {
	// EnsureDirs creates all required directory trees. Must be called once
	// during application startup before any reads or writes.
	EnsureDirs() error

	// RecordingPath returns the absolute path where a recording segment for
	// the given camera at the given time should be written.
	RecordingPath(camera string, t time.Time) string

	// SnapshotPath returns the absolute path for the best-score snapshot
	// image belonging to eventID.
	SnapshotPath(eventID string) string

	// ClipPath returns the absolute path for the exported clip belonging to
	// eventID.
	ClipPath(eventID string) string

	// ExportPath returns a writable path for a user-initiated export file.
	ExportPath(name string) string

	// Stat returns metadata about a file (exists, size). Returns
	// ErrNotExist if the file does not exist.
	Stat(ctx context.Context, path string) (FileInfo, error)

	// Delete removes a file. It is not an error if the file does not exist.
	Delete(ctx context.Context, path string) error
}

// FileInfo holds basic metadata about a stored file.
type FileInfo struct {
	Path    string
	Size    int64
	ModTime time.Time
}
