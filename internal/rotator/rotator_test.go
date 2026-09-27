package rotator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingWriter(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	// Max size of 1KB for easy testing
	w, err := New(logPath, 1, 2)
	if err != nil {
		t.Fatalf("failed to create writer: %v", err)
	}
	defer w.Close()

	// Override maxBytes to 50 bytes for test
	w.maxBytes = 50

	// Write 40 bytes
	data1 := []byte("1234567890123456789012345678901234567890\n")
	n, err := w.Write(data1)
	if err != nil || n != len(data1) {
		t.Fatalf("write 1 failed: %v", err)
	}

	// Write another 40 bytes -> triggers rotation
	data2 := []byte("abcdefghijabcdefghijabcdefghijabcdefghij\n")
	n, err = w.Write(data2)
	if err != nil || n != len(data2) {
		t.Fatalf("write 2 failed: %v", err)
	}

	// Check that test.log.1 exists
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("expected rotated file %s.1 to exist: %v", logPath, err)
	}

	// Check that active test.log exists
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected active file %s to exist: %v", logPath, err)
	}
}
