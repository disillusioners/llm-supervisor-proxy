package bufferstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// BufferStore manages persistent storage of buffer contents
type BufferStore struct {
	baseDir string
	maxSize int64 // Maximum total storage size in bytes
	mu      sync.RWMutex
}

// New creates a new BufferStore instance
func New(baseDir string, maxSize int64) (*BufferStore, error) {
	// Ensure directory exists
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create buffer directory: %w", err)
	}

	return &BufferStore{
		baseDir: baseDir,
		maxSize: maxSize,
	}, nil
}

// Save writes buffer content to a file with the given ID
func (s *BufferStore) Save(bufferID string, content []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if we need to cleanup old files first
	if s.maxSize > 0 {
		if err := s.cleanupIfNeededLocked(len(content)); err != nil {
			// Log but don't fail - storage limit is best effort
			fmt.Printf("Warning: buffer cleanup failed: %v\n", err)
		}
	}

	filePath := s.getFilePath(bufferID)
	tmpPath := filePath + ".tmp"

	// Write to temp file first for atomicity
	if err := os.WriteFile(tmpPath, content, 0644); err != nil {
		return fmt.Errorf("failed to write buffer file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to save buffer file: %w", err)
	}

	return nil
}

// Get retrieves buffer content by ID
func (s *BufferStore) Get(bufferID string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filePath := s.getFilePath(bufferID)
	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("buffer not found")
		}
		return nil, fmt.Errorf("failed to read buffer file: %w", err)
	}

	return content, nil
}

// Delete removes a buffer file by ID
func (s *BufferStore) Delete(bufferID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.getFilePath(bufferID)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete buffer file: %w", err)
	}

	return nil
}

// Cleanup removes old buffer files to free up space
func (s *BufferStore) Cleanup() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupAllLocked()
}

// getFilePath returns the full path for a buffer ID
func (s *BufferStore) getFilePath(bufferID string) string {
	return filepath.Join(s.baseDir, bufferID+".txt")
}

// cleanupIfNeededLocked removes old files if we're approaching the size limit
func (s *BufferStore) cleanupIfNeededLocked(newFileSize int) error {
	if s.maxSize <= 0 {
		return nil // No size limit configured
	}

	currentSize, err := s.getTotalSizeLocked()
	if err != nil {
		return err
	}

	// If adding this file would exceed limit, cleanup old files
	if currentSize+int64(newFileSize) > s.maxSize {
		return s.cleanupAllLocked()
	}

	return nil
}

// getTotalSizeLocked calculates total storage size
func (s *BufferStore) getTotalSizeLocked() (int64, error) {
	var totalSize int64
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return 0, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			totalSize += info.Size()
		}
	}

	return totalSize, nil
}

// cleanupAllLocked removes all buffer files
func (s *BufferStore) cleanupAllLocked() error {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			filePath := filepath.Join(s.baseDir, entry.Name())
			if err := os.Remove(filePath); err != nil {
				fmt.Printf("Warning: failed to remove buffer file %s: %v\n", entry.Name(), err)
			}
		}
	}

	return nil
}
