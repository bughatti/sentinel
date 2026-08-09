package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// ErrNotExist is returned by Stat when the file does not exist.
var ErrNotExist = errors.New("storage: file does not exist")

// LocalStorage implements Storage on the local filesystem.
type LocalStorage struct {
	recordingsDir string
	snapshotsDir  string
	clipsDir      string
	exportsDir    string
	tmpDir        string
}

// NewLocalStorage creates a LocalStorage rooted at the provided directories.
// All paths are cleaned with filepath.Clean.
func NewLocalStorage(recordingsDir, snapshotsDir, clipsDir, exportsDir, tmpDir string) *LocalStorage {
	return &LocalStorage{
		recordingsDir: filepath.Clean(recordingsDir),
		snapshotsDir:  filepath.Clean(snapshotsDir),
		clipsDir:      filepath.Clean(clipsDir),
		exportsDir:    filepath.Clean(exportsDir),
		tmpDir:        filepath.Clean(tmpDir),
	}
}

// EnsureDirs creates all media directories with 0755 permissions. It is safe
// to call multiple times.
func (s *LocalStorage) EnsureDirs() error {
	dirs := []string{
		s.recordingsDir,
		s.snapshotsDir,
		s.clipsDir,
		s.exportsDir,
		s.tmpDir,
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("storage: ensure dir %s: %w", d, err)
		}
		slog.Debug("storage dir ready", "path", d)
	}
	return nil
}

// RecordingPath returns the full path for a recording segment. The layout
// matches what the camera worker's ffmpeg segmenter actually writes:
//   <recordingsDir>/<camera>/<YYYY-MM-DD>/<HH>/<MM><SS>.mp4
// The segment directory is created automatically.
func (s *LocalStorage) RecordingPath(camera string, t time.Time) string {
	dir := filepath.Join(
		s.recordingsDir,
		filepath.Clean(camera),
		fmt.Sprintf("%04d-%02d-%02d", t.Year(), t.Month(), t.Day()),
		fmt.Sprintf("%02d", t.Hour()),
	)
	// Best-effort directory creation; errors surface when the file is written.
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, fmt.Sprintf("%02d%02d.mp4", t.Minute(), t.Second()))
}

// RecordingDir returns the base recordings directory for a camera. Use this
// when you need to construct a path for a specific date/hour without calling
// RecordingPath (which also creates the directory).
func (s *LocalStorage) RecordingDir(camera string) string {
	return filepath.Join(s.recordingsDir, filepath.Clean(camera))
}

// SnapshotPath returns the full path for an event snapshot JPEG.
func (s *LocalStorage) SnapshotPath(eventID string) string {
	return filepath.Clean(filepath.Join(s.snapshotsDir, eventID+".jpg"))
}

// ClipPath returns the full path for an event clip MP4.
func (s *LocalStorage) ClipPath(eventID string) string {
	return filepath.Clean(filepath.Join(s.clipsDir, eventID+".mp4"))
}

// ExportPath returns a writable path inside the exports directory.
func (s *LocalStorage) ExportPath(name string) string {
	return filepath.Clean(filepath.Join(s.exportsDir, filepath.Base(name)))
}

// Stat returns FileInfo for the given path, or ErrNotExist if missing.
func (s *LocalStorage) Stat(_ context.Context, path string) (FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return FileInfo{}, ErrNotExist
		}
		return FileInfo{}, fmt.Errorf("storage: stat %s: %w", path, err)
	}
	return FileInfo{
		Path:    path,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}, nil
}

// Delete removes path. It is not an error if the file does not exist.
func (s *LocalStorage) Delete(_ context.Context, path string) error {
	path = filepath.Clean(path)
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("storage: delete %s: %w", path, err)
	}
	return nil
}
