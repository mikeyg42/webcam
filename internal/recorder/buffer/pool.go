package buffer

import (
	"image"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	// Project-wide logger abstraction
	"github.com/mikeyg42/webcam/internal/recorder/recorderlog"
)

// FramePool manages a pool of reusable frames to reduce GC pressure
type FramePool struct {
	pool    sync.Pool
	maxSize int
	logger  recorderlog.Logger

	// Metrics
	allocated atomic.Uint64
	inUse     atomic.Uint64
	returned  atomic.Uint64
	gets      atomic.Uint64
	puts      atomic.Uint64
	misses    atomic.Uint64

	// Memory tracking (declared; update here if you later wire actual tracking)
	totalMemory atomic.Uint64
	peakMemory  atomic.Uint64
}

// PooledImage is a wrapper for pooled image data
type PooledImage struct {
	Data   []byte
	Width  int
	Height int
	Format string // "RGBA", "RGB", "YCbCr", etc.
	pool   *ImageDataPool
}

// ImageDataPool manages raw image data buffers
type ImageDataPool struct {
	pools   map[int]*sync.Pool // Size (power of two) -> Pool
	maxSize int
	mu      sync.RWMutex

	// Metrics
	allocated atomic.Uint64
	inUse     atomic.Uint64
	hits      atomic.Uint64
	misses    atomic.Uint64
}

// NewFramePool creates a new frame pool
func NewFramePool(maxSize int) *FramePool {
	fp := &FramePool{
		maxSize: maxSize,
		logger:  recorderlog.L().Named("frame-pool"),
	}

	fp.pool.New = func() interface{} {
		fp.allocated.Add(1)
		return &Frame{pooled: true}
	}

	return fp
}

// Get retrieves a frame from the pool
func (fp *FramePool) Get() *Frame {
	fp.gets.Add(1)

	frame := fp.pool.Get()
	if frame == nil {
		fp.misses.Add(1)
		frame = &Frame{pooled: true}
		fp.allocated.Add(1)
	}

	fp.inUse.Add(1)

	f := frame.(*Frame)
	f.pooled = true
	return f
}

// Put returns a frame to the pool
func (fp *FramePool) Put(frame *Frame) {
	if frame == nil || !frame.pooled {
		return
	}

	fp.puts.Add(1)

	// Reset frame data
	frame.Image = nil
	frame.Timestamp = time.Time{}
	frame.PTS = 0
	frame.Size = 0
	frame.Sequence = 0
	frame.Keyframe = false

	fp.pool.Put(frame)

	// Decrement inUse safely
	if fp.inUse.Load() > 0 {
		fp.inUse.Add(^uint64(0)) // subtract 1 via wraparound
	}
	fp.returned.Add(1)
}

// GetWithImage gets a frame and sets its image
func (fp *FramePool) GetWithImage(img image.Image, timestamp time.Time) *Frame {
	frame := fp.Get()
	frame.Image = img
	frame.Timestamp = timestamp

	// Approximate byte size (RGBA worst-case)
	if img != nil {
		bounds := img.Bounds()
		frame.Size = bounds.Dx() * bounds.Dy() * 4
	}

	return frame
}

// Metrics returns pool statistics
func (fp *FramePool) Metrics() map[string]interface{} {
	return map[string]interface{}{
		"allocated": fp.allocated.Load(),
		"in_use":    fp.inUse.Load(),
		"returned":  fp.returned.Load(),
		"gets":      fp.gets.Load(),
		"puts":      fp.puts.Load(),
		"misses":    fp.misses.Load(),
	}
}

// Clear removes all frames from the pool
func (fp *FramePool) Clear() {
	// Force GC to clean up pooled objects
	runtime.GC()
	fp.logger.Info("Frame pool cleared")
}

// NewImageDataPool creates a new image data pool
func NewImageDataPool(maxSize int) *ImageDataPool {
	return &ImageDataPool{
		pools:   make(map[int]*sync.Pool),
		maxSize: maxSize,
	}
}

// Get retrieves a buffer of the specified size
func (p *ImageDataPool) Get(size int) []byte {
	if size <= 0 {
		return nil
	}

	// Round up to nearest power of 2 for better pooling
	poolSize := roundUpPowerOf2(size)

	if poolSize > p.maxSize {
		p.misses.Add(1)
		return make([]byte, size)
	}

	p.mu.RLock()
	pool, exists := p.pools[poolSize]
	p.mu.RUnlock()

	if !exists {
		p.mu.Lock()
		pool, exists = p.pools[poolSize]
		if !exists {
			localSize := poolSize
			pool = &sync.Pool{
				New: func() interface{} {
					p.allocated.Add(1)
					return make([]byte, localSize)
				},
			}
			p.pools[poolSize] = pool
		}
		p.mu.Unlock()
	}

	buf := pool.Get().([]byte)

	if len(buf) < size {
		p.misses.Add(1)
		return make([]byte, size)
	}

	p.hits.Add(1)
	p.inUse.Add(1)
	return buf[:size]
}

// Put returns a buffer to the pool
func (p *ImageDataPool) Put(buf []byte) {
	if buf == nil {
		return
	}

	size := cap(buf)
	if size <= 0 {
		return
	}

	poolSize := roundUpPowerOf2(size)

	// Don't pool very large buffers
	if poolSize > p.maxSize {
		return
	}

	p.mu.RLock()
	pool, exists := p.pools[poolSize]
	p.mu.RUnlock()

	if exists {
		// Zero the contents (optional but safer if frames may contain sensitive bytes)
		for i := range buf {
			buf[i] = 0
		}
		pool.Put(buf)

		// Decrement inUse safely
		if p.inUse.Load() > 0 {
			p.inUse.Add(^uint64(0)) // subtract 1
		}
	}
}

// GetPooledImage gets a pooled image buffer
func (p *ImageDataPool) GetPooledImage(width, height int, format string) *PooledImage {
	var size int
	switch format {
	case "RGBA":
		size = width * height * 4
	case "RGB":
		size = width * height * 3
	case "YCbCr":
		size = width * height * 3 / 2 // Approximate for YUV420
	default:
		size = width * height * 4 // Default to RGBA
	}

	return &PooledImage{
		Data:   p.Get(size),
		Width:  width,
		Height: height,
		Format: format,
		pool:   p,
	}
}

// Release returns the image data to the pool
func (pi *PooledImage) Release() {
	if pi.pool != nil && pi.Data != nil {
		pi.pool.Put(pi.Data)
		pi.Data = nil
	}
}

// Metrics returns pool statistics
func (p *ImageDataPool) Metrics() map[string]interface{} {
	p.mu.RLock()
	poolCount := len(p.pools)
	p.mu.RUnlock()

	h := p.hits.Load()
	m := p.misses.Load()
	hitRate := float64(h) / float64(h+m+1) // +1 to avoid div-by-zero

	return map[string]interface{}{
		"pools":     poolCount,
		"allocated": p.allocated.Load(),
		"in_use":    p.inUse.Load(),
		"hits":      h,
		"misses":    m,
		"hit_rate":  hitRate,
	}
}

// roundUpPowerOf2 rounds n up to the nearest power of 2 (minimum 1)
func roundUpPowerOf2(n int) int {
	if n <= 1 {
		return 1
	}
	// Next power of two: 1 << bits.Len(n-1)
	return 1 << bits.Len(uint(n-1))
}

// MemoryManager tracks and manages memory usage across all pools
type MemoryManager struct {
	framePools []*FramePool
	dataPools  []*ImageDataPool
	maxMemory  uint64
	mu         sync.RWMutex
	logger     recorderlog.Logger

	totalAllocated atomic.Uint64
	totalInUse     atomic.Uint64
}

// NewMemoryManager creates a new memory manager
func NewMemoryManager(maxMemoryMB int) *MemoryManager {
	return &MemoryManager{
		maxMemory:  uint64(maxMemoryMB) * 1024 * 1024,
		logger:     recorderlog.L().Named("memory-manager"),
		framePools: make([]*FramePool, 0),
		dataPools:  make([]*ImageDataPool, 0),
	}
}

// RegisterFramePool registers a frame pool for tracking
func (mm *MemoryManager) RegisterFramePool(pool *FramePool) {
	mm.mu.Lock()
	mm.framePools = append(mm.framePools, pool)
	mm.mu.Unlock()
}

// RegisterDataPool registers a data pool for tracking
func (mm *MemoryManager) RegisterDataPool(pool *ImageDataPool) {
	mm.mu.Lock()
	mm.dataPools = append(mm.dataPools, pool)
	mm.mu.Unlock()
}

// CheckMemoryPressure checks if memory usage is too high
func (mm *MemoryManager) CheckMemoryPressure() bool {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	currentUsage := m.Alloc

	if currentUsage > mm.maxMemory {
		mm.logger.Warn("Memory pressure detected",
			recorderlog.Uint64("current_mb", currentUsage/1024/1024),
			recorderlog.Uint64("max_mb", mm.maxMemory/1024/1024))
		return true
	}
	return false
}

// ForceClearPools clears all registered pools
func (mm *MemoryManager) ForceClearPools() {
	mm.mu.RLock()
	for _, pool := range mm.framePools {
		pool.Clear()
	}
	mm.mu.RUnlock()

	mm.logger.Info("Forced clear of all pools due to memory pressure")
	runtime.GC()
}

// GetMetrics returns memory manager metrics
func (mm *MemoryManager) GetMetrics() map[string]interface{} {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	mm.mu.RLock()
	framePoolCount := len(mm.framePools)
	dataPoolCount := len(mm.dataPools)
	mm.mu.RUnlock()

	// Aggregate metrics from all pools
	totalFramesInUse := uint64(0)
	for _, pool := range mm.framePools {
		totalFramesInUse += pool.inUse.Load()
	}

	totalDataInUse := uint64(0)
	for _, pool := range mm.dataPools {
		totalDataInUse += pool.inUse.Load()
	}

	return map[string]interface{}{
		"memory_alloc_mb":     m.Alloc / 1024 / 1024,
		"memory_sys_mb":       m.Sys / 1024 / 1024,
		"num_gc":              m.NumGC,
		"frame_pools":         framePoolCount,
		"data_pools":          dataPoolCount,
		"total_frames_in_use": totalFramesInUse,
		"total_data_in_use":   totalDataInUse,
		"max_memory_mb":       mm.maxMemory / 1024 / 1024,
	}
}

// StartMonitoring starts periodic memory monitoring
func (mm *MemoryManager) StartMonitoring(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			if mm.CheckMemoryPressure() {
				mm.ForceClearPools()
			}
			metrics := mm.GetMetrics()
			mm.logger.Debug("Memory stats", recorderlog.Any("metrics", metrics))
		}
	}()
}
