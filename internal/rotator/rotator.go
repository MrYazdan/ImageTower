package rotator

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RotatingWriter is a thread-safe io.WriteCloser that rotates log files when
// they reach a maximum size, retaining a bounded number of timestamp-ordered
// backups. All methods are guarded by the embedded mutex, so a single writer
// can be shared safely across multiple goroutines (e.g. the standard logger).
type RotatingWriter struct {
	mu         sync.Mutex
	filename   string
	maxBytes   int64
	maxBackups int
	file       *os.File
	size       int64
}

// New creates a new RotatingWriter that writes to filename, rotating once the
// file exceeds maxSizeMB megabytes and keeping at most maxBackups rotated
// copies. Non-positive maxSizeMB defaults to 10 MB and non-positive maxBackups
// defaults to 3, so the writer is always usable even with zero-value inputs.
func New(filename string, maxSizeMB int, maxBackups int) (*RotatingWriter, error) {
	if maxSizeMB <= 0 {
		maxSizeMB = 10
	}
	if maxBackups <= 0 {
		maxBackups = 3
	}

	dir := filepath.Dir(filename)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create log directory: %w", err)
		}
	}

	w := &RotatingWriter{
		filename: filename,
		// Convert the megabyte budget to an absolute byte threshold once at
		// construction so Write only needs a cheap int64 comparison per call.
		maxBytes:   int64(maxSizeMB) * 1024 * 1024,
		maxBackups: maxBackups,
	}

	if err := w.openFile(); err != nil {
		return nil, err
	}

	return w, nil
}

// openFile opens the log file in append mode and records its current size so
// that rotation decisions use the true on-disk size (preserving existing
// content across restarts) rather than assuming an empty file.
func (w *RotatingWriter) openFile() error {
	f, err := os.OpenFile(w.filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("stat log file: %w", err)
	}

	w.file = f
	w.size = info.Size()
	return nil
}

// Write writes bytes to the log file and rotates if the file exceeds maxBytes.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	writeLen := int64(len(p))
	if w.size+writeLen > w.maxBytes {
		_ = w.rotate()
	}

	if w.file == nil {
		if err := w.openFile(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate closes the current file and shifts existing backups up by one before
// renaming the active log to "<filename>.1". Backups are numbered such that a
// higher suffix is older (".1" is newest, ".N" is oldest), so the loop walks
// from the oldest retainable index down to 1, promoting each to the next slot
// and quietly discarding any copy beyond maxBackups. Errors are ignored
// because rotation must never block the in-flight write it was triggered from.
func (w *RotatingWriter) rotate() error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}

	for i := w.maxBackups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", w.filename, i)
		dst := fmt.Sprintf("%s.%d", w.filename, i+1)
		_ = os.Rename(src, dst)
	}

	_ = os.Rename(w.filename, fmt.Sprintf("%s.1", w.filename))
	return w.openFile()
}

// Close closes the underlying file handle.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}
