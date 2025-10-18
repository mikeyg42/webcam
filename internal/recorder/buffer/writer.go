package buffer

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	// Use your unified logger abstraction
	"github.com/mikeyg42/webcam/internal/recorder/recorderlog"
)

// WALWriter implements Write-Ahead Logging for frame reliability
type WALWriter struct {
	dir         string
	currentFile *os.File
	writer      *bufio.Writer
	logger      recorderlog.Logger

	fileSize    int64
	maxFileSize int64
	fileIndex   int

	mu     sync.Mutex
	closed atomic.Bool

	// Metrics
	totalBytes  atomic.Uint64
	totalFrames atomic.Uint64
	totalFiles  atomic.Uint64
	writeErrors atomic.Uint64
	syncErrors  atomic.Uint64
}

// WALEntry represents a single WAL entry
type WALEntry struct {
	Timestamp time.Time
	Sequence  uint64
	Size      uint32
	Checksum  uint32
	Data      []byte
	Type      EntryType
}

// EntryType defines the type of WAL entry
type EntryType uint8

const (
	EntryTypeFrame EntryType = iota
	EntryTypeKeyframe
	EntryTypeCheckpoint
	EntryTypeMetadata
)

// WALHeader is the header for each WAL entry
// NOTE: Do not change field order lightly; it is persisted on disk.
type WALHeader struct {
	Magic     [4]byte // "WALF"
	Version   uint16
	Type      EntryType
	Flags     uint8
	Timestamp int64
	Sequence  uint64
	Size      uint32
	Checksum  uint32
}

const (
	WALMagic          = "WALF"
	WALVersion        = 1
	DefaultMaxWALSize = 100 * 1024 * 1024 // 100MB per file
	WALBufferSize     = 64 * 1024         // 64KB buffer
)

// compute header size once, using the actual encoding shape
var walHeaderSize = func() int64 {
	n := binary.Size(WALHeader{})
	if n <= 0 {
		// Should never happen; defensive fallback
		return 32
	}
	return int64(n)
}()

// NewWALWriter creates a new Write-Ahead Log writer
func NewWALWriter(dir string, maxFileSize int64) (*WALWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	if maxFileSize <= 0 {
		maxFileSize = DefaultMaxWALSize
	}

	w := &WALWriter{
		dir:         dir,
		maxFileSize: maxFileSize,
		logger:      recorderlog.L().Named("wal-writer"),
	}

	// Open initial file
	if err := w.rotateFile(); err != nil {
		return nil, err
	}

	return w, nil
}

// Write writes a frame to the WAL
func (w *WALWriter) Write(frame *Frame, entryType EntryType) error {
	if w.closed.Load() {
		return fmt.Errorf("WAL writer is closed")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed.Load() {
		return fmt.Errorf("WAL writer is closed")
	}

	// Serialize frame data (Duration prints as ns via %d, intentionally compact)
	data, err := w.serializeFrame(frame)
	if err != nil {
		w.writeErrors.Add(1)
		return fmt.Errorf("failed to serialize frame: %w", err)
	}

	// Create WAL entry
	entry := WALEntry{
		Timestamp: frame.Timestamp,
		Sequence:  frame.Sequence,
		Size:      uint32(len(data)),
		Checksum:  crc32.ChecksumIEEE(data),
		Data:      data,
		Type:      entryType,
	}

	// Check if rotation is needed (header + data)
	if w.fileSize+walHeaderSize+int64(len(data)) > w.maxFileSize {
		if err := w.rotateFile(); err != nil {
			w.writeErrors.Add(1)
			return fmt.Errorf("failed to rotate WAL file: %w", err)
		}
	}

	// Write entry
	if err := w.writeEntry(&entry); err != nil {
		w.writeErrors.Add(1)
		return err
	}

	w.totalFrames.Add(1)
	w.totalBytes.Add(uint64(len(data)))

	return nil
}

// writeEntry writes a single WAL entry. Caller holds w.mu.
func (w *WALWriter) writeEntry(entry *WALEntry) error {
	// Create header
	header := WALHeader{
		Version:   WALVersion,
		Type:      entry.Type,
		// Flags reserved for future use (e.g., compression)
		Timestamp: entry.Timestamp.UnixNano(),
		Sequence:  entry.Sequence,
		Size:      entry.Size,
		Checksum:  entry.Checksum,
	}
	copy(header.Magic[:], WALMagic)

	// Write header
	if err := binary.Write(w.writer, binary.BigEndian, header); err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}

	// Write data
	if _, err := w.writer.Write(entry.Data); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	w.fileSize += walHeaderSize + int64(len(entry.Data))
	return nil
}

// Sync flushes pending writes to disk
func (w *WALWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.writer != nil {
		if err := w.writer.Flush(); err != nil {
			w.syncErrors.Add(1)
			return err
		}
	}
	if w.currentFile != nil {
		if err := w.currentFile.Sync(); err != nil {
			w.syncErrors.Add(1)
			return err
		}
	}
	return nil
}

// rotateFile closes current file and opens a new one. Caller holds w.mu.
func (w *WALWriter) rotateFile() error {
	// Close current file if present
	if w.currentFile != nil {
		// Flush + Sync before close; log on error, but prefer returning it.
		if w.writer != nil {
			if err := w.writer.Flush(); err != nil {
				w.syncErrors.Add(1)
				w.logger.Error("Failed to flush WAL writer on rotate", recorderlog.Error(err))
				return err
			}
		}
		if err := w.currentFile.Sync(); err != nil {
			w.syncErrors.Add(1)
			w.logger.Error("Failed to sync WAL file on rotate", recorderlog.Error(err))
			return err
		}
		if err := w.currentFile.Close(); err != nil {
			w.logger.Error("Failed to close WAL file on rotate", recorderlog.Error(err))
			return err
		}
	}

	// Generate new filename
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("wal_%s_%04d.log", timestamp, w.fileIndex)
	fullpath := filepath.Join(w.dir, filename)

	// Open new file
	file, err := os.OpenFile(fullpath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open WAL file: %w", err)
	}

	w.currentFile = file
	w.writer = bufio.NewWriterSize(file, WALBufferSize)
	w.fileSize = 0
	w.fileIndex++
	w.totalFiles.Add(1)

	w.logger.Info("Rotated WAL file",
		recorderlog.String("filename", filename),
		recorderlog.Int("index", w.fileIndex))

	return nil
}

// serializeFrame converts a frame to bytes
func (w *WALWriter) serializeFrame(frame *Frame) ([]byte, error) {
	// Simple serialization: timestamp|pts_ns|size|keyframe
	// NOTE: PTS is printed as ns (int) for compactness.
	metadata := fmt.Sprintf("%d|%d|%d|%t",
		frame.Timestamp.UnixNano(),
		frame.PTS, // prints as ns with %d
		frame.Size,
		frame.Keyframe,
	)
	return []byte(metadata), nil
}

// Close closes the WAL writer
func (w *WALWriter) Close() error {
	if !w.closed.CompareAndSwap(false, true) {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Flush + Sync + Close in order; surface the first error encountered.
	if w.writer != nil {
		if err := w.writer.Flush(); err != nil {
			w.syncErrors.Add(1)
			_ = w.currentFile.Close() // best-effort
			return err
		}
	}
	if w.currentFile != nil {
		if err := w.currentFile.Sync(); err != nil {
			w.syncErrors.Add(1)
			_ = w.currentFile.Close() // best-effort
			return err
		}
		if err := w.currentFile.Close(); err != nil {
			return err
		}
	}
	return nil
}

// Checkpoint writes a checkpoint entry
func (w *WALWriter) Checkpoint() error {
	entry := WALEntry{
		Timestamp: time.Now(),
		Type:      EntryTypeCheckpoint,
		Data:      []byte("CHECKPOINT"),
	}
	entry.Size = uint32(len(entry.Data))
	entry.Checksum = crc32.ChecksumIEEE(entry.Data)

	w.mu.Lock()
	defer w.mu.Unlock()

	// Rotate if needed for the checkpoint too
	if w.fileSize+walHeaderSize+int64(entry.Size) > w.maxFileSize {
		if err := w.rotateFile(); err != nil {
			w.writeErrors.Add(1)
			return fmt.Errorf("failed to rotate WAL file: %w", err)
		}
	}

	return w.writeEntry(&entry)
}

// GetMetrics returns WAL writer metrics
func (w *WALWriter) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"total_bytes":  w.totalBytes.Load(),
		"total_frames": w.totalFrames.Load(),
		"total_files":  w.totalFiles.Load(),
		"write_errors": w.writeErrors.Load(),
		"sync_errors":  w.syncErrors.Load(),
		"current_size": w.fileSize,
		"file_index":   w.fileIndex,
	}
}

// WALReader reads WAL files for recovery
type WALReader struct {
	dir    string
	logger recorderlog.Logger
}

// NewWALReader creates a new WAL reader
func NewWALReader(dir string) *WALReader {
	return &WALReader{
		dir:    dir,
		logger: recorderlog.L().Named("wal-reader"),
	}
}

// ReadAll reads all WAL entries from all files in the directory
func (r *WALReader) ReadAll() ([]*WALEntry, error) {
	files, err := filepath.Glob(filepath.Join(r.dir, "wal_*.log"))
	if err != nil {
		return nil, err
	}

	var allEntries []*WALEntry

	for _, file := range files {
		entries, err := r.readFile(file)
		if err != nil {
			r.logger.Error("Failed to read WAL file",
				recorderlog.String("file", file),
				recorderlog.Error(err))
			continue
		}
		allEntries = append(allEntries, entries...)
	}

	return allEntries, nil
}

// readFile reads all entries from a single WAL file
func (r *WALReader) readFile(path string) ([]*WALEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []*WALEntry
	reader := bufio.NewReader(file)

	for {
		// Read header
		var header WALHeader
		if err := binary.Read(reader, binary.BigEndian, &header); err != nil {
			if err == io.EOF {
				break
			}
			return entries, err
		}

		// Verify magic
		if string(header.Magic[:]) != WALMagic {
			r.logger.Warn("Invalid WAL magic, skipping entry")
			continue
		}

		// Read data
		data := make([]byte, header.Size)
		if _, err := io.ReadFull(reader, data); err != nil {
			return entries, err
		}

		// Verify checksum
		if crc32.ChecksumIEEE(data) != header.Checksum {
			r.logger.Warn("Checksum mismatch, skipping entry",
				recorderlog.Uint64("sequence", header.Sequence))
			continue
		}

		entry := &WALEntry{
			Timestamp: time.Unix(0, header.Timestamp),
			Sequence:  header.Sequence,
			Size:      header.Size,
			Checksum:  header.Checksum,
			Data:      data,
			Type:      header.Type,
		}
		entries = append(entries, entry)
	}

	r.logger.Info("Read WAL file",
		recorderlog.String("file", path),
		recorderlog.Int("entries", len(entries)))

	return entries, nil
}

// Cleanup removes old WAL files
func (r *WALReader) Cleanup(olderThan time.Duration) error {
	files, err := filepath.Glob(filepath.Join(r.dir, "wal_*.log"))
	if err != nil {
		return err
	}

	cutoff := time.Now().Add(-olderThan)
	removed := 0

	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(file); err != nil {
				r.logger.Error("Failed to remove old WAL file",
					recorderlog.String("file", file),
					recorderlog.Error(err))
			} else {
				removed++
			}
		}
	}

	if removed > 0 {
		r.logger.Info("Cleaned up old WAL files",
			recorderlog.Int("removed", removed))
	}
	return nil
}
