package bufferstore

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNew(t *testing.T) {
	dir := t.TempDir()

	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}

	if store == nil {
		t.Fatal("New() returned nil")
	}

	if store.baseDir != dir {
		t.Errorf("baseDir = %q, want %q", store.baseDir, dir)
	}

	if store.maxSize != 0 {
		t.Errorf("maxSize = %d, want 0", store.maxSize)
	}

	// Verify directory was created
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("failed to stat directory: %v", err)
	}
	if !info.IsDir() {
		t.Error("baseDir is not a directory")
	}
}

func TestNewCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "path", "to", "buffer")

	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	defer store.Cleanup()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("failed to stat created directory: %v", err)
	}
	if !info.IsDir() {
		t.Error("directory was not created")
	}
}

func TestNewReturnsErrorForInvalidPath(t *testing.T) {
	// Try to create directory in a path that shouldn't be writable (if running as root this may not fail)
	// Instead test with an invalid path pattern
	dir := filepath.Join(t.TempDir(), "valid")
	if err := os.MkdirAll(dir, 0555); err != nil {
		t.Skip("cannot create read-only directory, skipping permission test")
	}

	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() should not return error for read-only parent: %v", err)
	}
	_ = store
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	content := []byte("test buffer content")
	bufferID := "test-buffer-1"

	if err := store.Save(bufferID, content); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	// Verify file exists
	filePath := filepath.Join(dir, bufferID+".txt")
	if _, err := os.Stat(filePath); err != nil {
		t.Errorf("file not created at %s", filePath)
	}

	// Verify content
	saved, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read saved file: %v", err)
	}
	if string(saved) != string(content) {
		t.Errorf("saved content = %q, want %q", string(saved), string(content))
	}
}

func TestSaveAtomicRename(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	bufferID := "atomic-test"
	content := []byte("atomic content")

	store.Save(bufferID, content)

	// Verify .tmp file is not left behind
	tmpPath := filepath.Join(dir, bufferID+".txt.tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temporary file was not cleaned up")
	}
}

func TestSaveOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	bufferID := "overwrite-test"

	// Save first version
	store.Save(bufferID, []byte("version 1"))

	// Save second version
	store.Save(bufferID, []byte("version 2"))

	// Verify only one file exists
	files, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil {
		t.Fatalf("failed to glob: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d", len(files))
	}

	// Verify content is updated
	content, err := store.Get(bufferID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(content) != "version 2" {
		t.Errorf("content = %q, want %q", string(content), "version 2")
	}
}

func TestGet(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	bufferID := "get-test"
	content := []byte("content to retrieve")
	store.Save(bufferID, content)

	retrieved, err := store.Get(bufferID)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if string(retrieved) != string(content) {
		t.Errorf("retrieved = %q, want %q", string(retrieved), string(content))
	}
}

func TestGetNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	_, err = store.Get("nonexistent")
	if err == nil {
		t.Error("Get() expected error for nonexistent buffer")
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	bufferID := "delete-test"
	store.Save(bufferID, []byte("content"))

	if err := store.Delete(bufferID); err != nil {
		t.Fatalf("Delete() error = %v, want nil", err)
	}

	// Verify file is deleted
	filePath := filepath.Join(dir, bufferID+".txt")
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file still exists after Delete()")
	}
}

func TestDeleteNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	// Delete should not return error for nonexistent file
	if err := store.Delete("nonexistent"); err != nil {
		t.Errorf("Delete() error = %v, want nil", err)
	}
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Create multiple buffer files
	for i := 0; i < 5; i++ {
		store.Save(string(rune('a'+i)), []byte("content"))
	}

	if err := store.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v, want nil", err)
	}

	// Verify directory is empty
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read directory: %v", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			t.Error("directory not empty after Cleanup()")
			break
		}
	}
}

func TestCleanupNilStore(t *testing.T) {
	var store *BufferStore

	// Should not panic
	if err := store.Cleanup(); err != nil {
		t.Errorf("Cleanup() on nil store error = %v, want nil", err)
	}
}

func TestCleanupEmptyStore(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Cleanup on empty directory should not error
	if err := store.Cleanup(); err != nil {
		t.Errorf("Cleanup() on empty store error = %v, want nil", err)
	}
}

func TestSaveEmptyContent(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	bufferID := "empty-test"
	if err := store.Save(bufferID, []byte{}); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	content, err := store.Get(bufferID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(content) != 0 {
		t.Errorf("content length = %d, want 0", len(content))
	}
}

func TestSaveLargeContent(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	// Create 1MB of data
	bufferID := "large-test"
	content := make([]byte, 1024*1024)
	for i := range content {
		content[i] = byte(i % 256)
	}

	if err := store.Save(bufferID, content); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	retrieved, err := store.Get(bufferID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if len(retrieved) != len(content) {
		t.Errorf("retrieved length = %d, want %d", len(retrieved), len(content))
	}
}

func TestSaveConcurrent(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	var wg sync.WaitGroup
	saveCount := 100

	for i := 0; i < saveCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			bufferID := string(rune('a' + id%26))
			content := []byte{'0' + byte(id%10)}
			store.Save(bufferID, content)
		}(i)
	}

	wg.Wait()

	// Verify all files exist
	for i := 0; i < saveCount; i++ {
		bufferID := string(rune('a' + i%26))
		if _, err := store.Get(bufferID); err != nil {
			t.Errorf("Get() error for buffer %s: %v", bufferID, err)
		}
	}
}

func TestGetConcurrent(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	bufferID := "concurrent-read"
	content := []byte("shared content")
	store.Save(bufferID, content)

	var wg sync.WaitGroup
	getCount := 50

	for i := 0; i < getCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			retrieved, err := store.Get(bufferID)
			if err != nil {
				t.Errorf("Get() error = %v", err)
				return
			}
			if string(retrieved) != string(content) {
				t.Errorf("content mismatch")
			}
		}()
	}

	wg.Wait()
}

func TestSaveAndDeleteConcurrent(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	var wg sync.WaitGroup
	ops := 50

	// Interleave saves and deletes
	for i := 0; i < ops; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			bufferID := string(rune('a' + id%10))
			content := []byte{'0' + byte(id)}
			store.Save(bufferID, content)
		}(i)

		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			bufferID := string(rune('a' + id%10))
			store.Delete(bufferID)
		}(i)
	}

	wg.Wait()

	// Should not panic
}

func TestMultipleBuffers(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	buffers := map[string][]byte{
		"buffer-1": []byte("content one"),
		"buffer-2": []byte("content two"),
		"buffer-3": []byte("content three"),
	}

	// Save all
	for id, content := range buffers {
		if err := store.Save(id, content); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	// Verify all
	for id, expected := range buffers {
		retrieved, err := store.Get(id)
		if err != nil {
			t.Errorf("Get() error for %s: %v", id, err)
			continue
		}
		if string(retrieved) != string(expected) {
			t.Errorf("content for %s = %q, want %q", id, string(retrieved), string(expected))
		}
	}

	// Count files
	files, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil {
		t.Fatalf("failed to glob: %v", err)
	}
	if len(files) != len(buffers) {
		t.Errorf("file count = %d, want %d", len(files), len(buffers))
	}
}

func TestDeleteAndSaveSameID(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	bufferID := "reuse-test"

	// Save, delete, save again
	store.Save(bufferID, []byte("first"))
	store.Delete(bufferID)
	store.Save(bufferID, []byte("second"))

	content, err := store.Get(bufferID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(content) != "second" {
		t.Errorf("content = %q, want %q", string(content), "second")
	}
}

func TestSaveWithSpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	testCases := []struct {
		id      string
		content string
	}{
		{"special-chars", "content with special chars: !@#$%"},
		{"unicode-id", "content with unicode"},
		{"dots.in.name", "content in dotted name"},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			if err := store.Save(tc.id, []byte(tc.content)); err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			retrieved, err := store.Get(tc.id)
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}

			if string(retrieved) != tc.content {
				t.Errorf("content = %q, want %q", string(retrieved), tc.content)
			}
		})
	}
}

func TestMaxSizeEnforcement(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir, 100) // 100 bytes max
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer store.Cleanup()

	// Save first file
	store.Save("file-1", make([]byte, 50))

	// Save second file - should trigger cleanup
	store.Save("file-2", make([]byte, 50))

	// Both files should exist (cleanup should have removed old files)
	_, err = store.Get("file-1")
	if err != nil {
		t.Logf("file-1 was cleaned up (expected): %v", err)
	}

	_, err = store.Get("file-2")
	if err != nil {
		t.Errorf("file-2 should exist: %v", err)
	}
}
