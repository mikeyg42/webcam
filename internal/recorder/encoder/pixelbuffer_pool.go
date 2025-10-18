//go:build darwin && cgo
package encoder

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreVideo -framework CoreFoundation
#import <CoreVideo/CoreVideo.h>
#import <CoreFoundation/CoreFoundation.h>

CVPixelBufferRef createPixelBuffer(int width, int height);

// ---- Prototypes visible to cgo in THIS file ----
void  cvpb_release(void *buffer);
int   cvpb_lock(void *buffer);
void  cvpb_unlock(void *buffer);
void* cvpb_base_addr(void *buffer);
size_t cvpb_bpr(void *buffer);
size_t cvpb_w(void *buffer);
size_t cvpb_h(void *buffer);

// ---- Definitions ----

// Create a CVPixelBuffer (BGRA) using CoreFoundation-only attributes.
CVPixelBufferRef createPixelBuffer(int width, int height) {
    CVPixelBufferRef pb = NULL;

    CFMutableDictionaryRef attrs =
        CFDictionaryCreateMutable(kCFAllocatorDefault, 2,
                                  &kCFTypeDictionaryKeyCallBacks,
                                  &kCFTypeDictionaryValueCallBacks);
    if (!attrs) { return NULL; }

    CFDictionaryRef emptyIOSurfaceProps =
        CFDictionaryCreate(kCFAllocatorDefault, NULL, NULL, 0,
                           &kCFTypeDictionaryKeyCallBacks,
                           &kCFTypeDictionaryValueCallBacks);
    if (!emptyIOSurfaceProps) {
        CFRelease(attrs);
        return NULL;
    }

    CFDictionarySetValue(attrs, kCVPixelBufferIOSurfacePropertiesKey, emptyIOSurfaceProps);
    CFDictionarySetValue(attrs, kCVPixelBufferMetalCompatibilityKey, kCFBooleanTrue);

    CVReturn r = CVPixelBufferCreate(kCFAllocatorDefault,
                                     (size_t)width,
                                     (size_t)height,
                                     kCVPixelFormatType_32BGRA,
                                     attrs,
                                     &pb);

    CFRelease(emptyIOSurfaceProps);
    CFRelease(attrs);

    if (r != kCVReturnSuccess) {
        return NULL;
    }
    return pb;
}

void cvpb_release(void *buffer) {
    if (buffer) { CFRelease((CFTypeRef)buffer); }
}

int cvpb_lock(void *buffer) {
    return CVPixelBufferLockBaseAddress((CVPixelBufferRef)buffer, 0);
}

void cvpb_unlock(void *buffer) {
    CVPixelBufferUnlockBaseAddress((CVPixelBufferRef)buffer, 0);
}

void* cvpb_base_addr(void *buffer) {
    return CVPixelBufferGetBaseAddress((CVPixelBufferRef)buffer);
}

size_t cvpb_bpr(void *buffer) {
    return CVPixelBufferGetBytesPerRow((CVPixelBufferRef)buffer);
}

size_t cvpb_w(void *buffer) {
    return CVPixelBufferGetWidth((CVPixelBufferRef)buffer);
}

size_t cvpb_h(void *buffer) {
    return CVPixelBufferGetHeight((CVPixelBufferRef)buffer);
}
*/
import "C"

import (
	"image"
	"sync"
	"unsafe"
)

// PixelBufferPool manages a pool of reusable pixel buffers.
type PixelBufferPool struct {
	pool   chan C.CVPixelBufferRef
	width  int
	height int
	mu     sync.Mutex
	closed bool
}

// NewPixelBufferPool creates a new pool.
func NewPixelBufferPool(width, height, size int) *PixelBufferPool {
	if size <= 0 {
		size = 4
	}
	p := &PixelBufferPool{
		pool:   make(chan C.CVPixelBufferRef, size),
		width:  width,
		height: height,
	}
	// Pre-allocate best-effort.
	for i := 0; i < size; i++ {
		if pb := C.createPixelBuffer(C.int(width), C.int(height)); unsafe.Pointer(pb) != nil {
			p.pool <- pb
		}
	}
	return p
}

// Get retrieves a buffer from the pool.
func (p *PixelBufferPool) Get() C.CVPixelBufferRef {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		var z C.CVPixelBufferRef
		return z
	}
	p.mu.Unlock()

	select {
	case buffer := <-p.pool:
		return buffer
	default:
		return C.createPixelBuffer(C.int(p.width), C.int(p.height))
	}
}

// Put returns a buffer to the pool (or releases if full/closed).
func (p *PixelBufferPool) Put(buffer C.CVPixelBufferRef) {
	if unsafe.Pointer(buffer) == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		C.cvpb_release(unsafe.Pointer(buffer))
		return
	}
	p.mu.Unlock()

	select {
	case p.pool <- buffer:
	default:
		C.cvpb_release(unsafe.Pointer(buffer))
	}
}

// Close releases all buffers.
func (p *PixelBufferPool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.pool)
	for buffer := range p.pool {
		if unsafe.Pointer(buffer) != nil {
			C.cvpb_release(unsafe.Pointer(buffer))
		}
	}
	p.mu.Unlock()
}

// copyImageToPixelBuffer copies Go image data to a CVPixelBuffer (BGRA).
func copyImageToPixelBuffer(img image.Image, buffer C.CVPixelBufferRef) error {
	if unsafe.Pointer(buffer) == nil {
		return &EncoderError{Code: -3, Message: "nil pixel buffer", Fatal: false}
	}

	if C.cvpb_lock(unsafe.Pointer(buffer)) != 0 {
		return &EncoderError{Code: -3, Message: "Failed to lock pixel buffer", Fatal: false}
	}
	defer C.cvpb_unlock(unsafe.Pointer(buffer))

	base := C.cvpb_base_addr(unsafe.Pointer(buffer))
	if base == nil {
		return &EncoderError{Code: -3, Message: "pixel buffer base address is nil", Fatal: false}
	}

	bytesPerRow := int(C.cvpb_bpr(unsafe.Pointer(buffer)))
	bufW := int(C.cvpb_w(unsafe.Pointer(buffer)))
	bufH := int(C.cvpb_h(unsafe.Pointer(buffer)))

	b := img.Bounds()
	srcW := b.Dx()
	srcH := b.Dy()

	// Copy overlap region
	copyW := srcW
	if bufW < copyW {
		copyW = bufW
	}
	copyH := srcH
	if bufH < copyH {
		copyH = bufH
	}
	if copyW <= 0 || copyH <= 0 {
		return nil
	}
	if bytesPerRow < copyW*4 {
		return &EncoderError{Code: -3, Message: "bytesPerRow too small for BGRA copy", Fatal: false}
	}

	dst := unsafe.Slice((*byte)(base), bufH*bytesPerRow)

	switch src := img.(type) {
	case *image.RGBA:
		for y := 0; y < copyH; y++ {
			srcRow := src.Pix[y*src.Stride : y*src.Stride+copyW*4]
			dstRow := dst[y*bytesPerRow : y*bytesPerRow+copyW*4]
			for x := 0; x < copyW; x++ {
				dstRow[x*4+0] = srcRow[x*4+2] // B
				dstRow[x*4+1] = srcRow[x*4+1] // G
				dstRow[x*4+2] = srcRow[x*4+0] // R
				dstRow[x*4+3] = srcRow[x*4+3] // A
			}
		}
	default:
		for y := 0; y < copyH; y++ {
			row := dst[y*bytesPerRow : y*bytesPerRow+copyW*4]
			for x := 0; x < copyW; x++ {
				r, g, b, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				i := x * 4
				row[i+0] = byte(b >> 8) // B
				row[i+1] = byte(g >> 8) // G
				row[i+2] = byte(r >> 8) // R
				row[i+3] = byte(a >> 8) // A
			}
		}
	}

	return nil
}
