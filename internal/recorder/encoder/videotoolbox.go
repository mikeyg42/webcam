//go:build darwin && cgo

package encoder

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework VideoToolbox -framework CoreMedia -framework CoreVideo -framework CoreFoundation -framework Foundation

#import <VideoToolbox/VideoToolbox.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <CoreFoundation/CoreFoundation.h>
#import <Foundation/Foundation.h>
#import <dispatch/dispatch.h>
#import <pthread.h>
#import <stdlib.h>
#import <string.h>
#import <os/object.h>

// --- Encoder context --------------------------------------------------------

typedef struct {
    VTCompressionSessionRef session;
    CMFormatDescriptionRef  formatDesc;     // retained once available
    CMVideoCodecType        codecType;      // kCMVideoCodecType_H264 or _HEVC
    int                     nalLengthSize;  // bytes of length field (usually 4)

    // stats (atomic)
    int frameCount;
    int64_t totalBytes;
    int droppedFrames;
    int keyFrames;

    CFMutableArrayRef    encodedFrames;     // guarded by frames_mutex
    pthread_mutex_t      frames_mutex;      // protects encodedFrames
    dispatch_semaphore_t semaphore;

    OSStatus lastError;
} EncoderContext;

typedef struct {
    uint8_t *data;
    size_t   size;
    int      isKeyframe;
    int64_t  pts;
    int64_t  dts;
} EncodedFrame;

// --- Forward declarations for CGO visibility -------------------------------
EncoderContext* createEncoder(int width, int height, double frameRate, int bitrate,
                              int keyframeInterval, const char* codecType,
                              const char* profile, int maxBFrames);
int encodeFrame_wrap(EncoderContext *ctx, CVPixelBufferRef pixelBuffer,
                     uint64_t timestamp, uint64_t duration, int forceKeyframe);
int isNullPixelBuffer(CVPixelBufferRef pb);
EncodedFrame* getEncodedFrames(EncoderContext *ctx, int *count);
void freeEncodedFrames(EncodedFrame *frames, int count);
int flushEncoder(EncoderContext *ctx);
void destroyEncoder(EncoderContext *ctx);
int64_t getEncoderBytesEncoded(EncoderContext *ctx);
int getEncoderFrameCount(EncoderContext *ctx);
int getEncoderDroppedFrames(EncoderContext *ctx);
int getEncoderKeyFrames(EncoderContext *ctx);
int64_t getGlobalAllocatedMemory();
int getGlobalActiveBuffers();
// --- Memory tracking (debug/metrics) ---------------------------------------

static int64_t g_allocated_memory = 0;
static int     g_active_buffers   = 0;
static pthread_mutex_t g_memory_mutex = PTHREAD_MUTEX_INITIALIZER;

// --- Helpers: keyframe detection -------------------------------------------

static int sample_is_keyframe(CMSampleBufferRef sbuf) {
    CFArrayRef atts = CMSampleBufferGetSampleAttachmentsArray(sbuf, false);
    if (!atts || CFArrayGetCount(atts) == 0) return 1; // assume keyframe if no info
    CFDictionaryRef att = (CFDictionaryRef)CFArrayGetValueAtIndex(atts, 0);
    CFBooleanRef notSync = (CFBooleanRef)CFDictionaryGetValue(att, kCMSampleAttachmentKey_NotSync);
    return (notSync == NULL || !CFBooleanGetValue(notSync)) ? 1 : 0;
}

// --- Helpers: get/remember format description and NAL length size -----------

static void ensure_format_desc_and_nal_len(EncoderContext *ctx, CMSampleBufferRef sbuf) {
    if (ctx->formatDesc) return;

    CMFormatDescriptionRef fdesc = CMSampleBufferGetFormatDescription(sbuf);
    if (!fdesc) return;
    CFRetain(fdesc);
    ctx->formatDesc = fdesc;

    // Detect codec subtype from format description
    FourCharCode subtype = CMFormatDescriptionGetMediaSubType(fdesc);
    if (subtype == kCMVideoCodecType_H264 || subtype == kCMVideoCodecType_AV1) {
        ctx->codecType = kCMVideoCodecType_H264;

        size_t paramCount = 0;
        int nalLen = 0;
        // We don't need actual parameter set bytes here; just nalLen
        CMVideoFormatDescriptionGetH264ParameterSetAtIndex(
            (CMVideoFormatDescriptionRef)fdesc,
            0, NULL, NULL, &paramCount, &nalLen
        );
        ctx->nalLengthSize = nalLen > 0 ? nalLen : 4;
    } else {
        // Assume HEVC family
        ctx->codecType = kCMVideoCodecType_HEVC;

        size_t paramCount = 0;
        int nalLen = 0;
        CMVideoFormatDescriptionGetHEVCParameterSetAtIndex(
            (CMVideoFormatDescriptionRef)fdesc,
            0, NULL, NULL, &paramCount, &nalLen
        );
        ctx->nalLengthSize = nalLen > 0 ? nalLen : 4;
    }
}

// --- Helpers: build Annex-B buffer (prepend SPS/PPS[/VPS] on keyframes) ----

static void write_start_code(uint8_t **dst) {
    // 4-byte start code 0x00000001
    (*dst)[0] = 0x00; (*dst)[1] = 0x00; (*dst)[2] = 0x00; (*dst)[3] = 0x01;
    *dst += 4;
}

static int append_parameter_sets_annexb(EncoderContext *ctx, uint8_t **dst) {
    if (!ctx->formatDesc) return 0;

    if (ctx->codecType == kCMVideoCodecType_H264) {
        size_t count = 0;
        int nalLenDummy = 0;
        CMVideoFormatDescriptionGetH264ParameterSetAtIndex(
            (CMVideoFormatDescriptionRef)ctx->formatDesc, 0, NULL, NULL, &count, &nalLenDummy);

        for (size_t i = 0; i < count; i++) {
            const uint8_t *ps = NULL;
            size_t psSize = 0;
            OSStatus s = CMVideoFormatDescriptionGetH264ParameterSetAtIndex(
                (CMVideoFormatDescriptionRef)ctx->formatDesc, (int)i, &ps, &psSize, NULL, NULL);
            if (s == noErr && ps && psSize > 0) {
                write_start_code(dst);
                memcpy(*dst, ps, psSize);
                *dst += psSize;
            }
        }
    } else {
        // HEVC: VPS, SPS, PPS (parameter set indices are contiguous; we just iterate)
        size_t count = 0;
        int nalLenDummy = 0;
        CMVideoFormatDescriptionGetHEVCParameterSetAtIndex(
            (CMVideoFormatDescriptionRef)ctx->formatDesc, 0, NULL, NULL, &count, &nalLenDummy);

        for (size_t i = 0; i < count; i++) {
            const uint8_t *ps = NULL;
            size_t psSize = 0;
            OSStatus s = CMVideoFormatDescriptionGetHEVCParameterSetAtIndex(
                (CMVideoFormatDescriptionRef)ctx->formatDesc, (int)i, &ps, &psSize, NULL, NULL);
            if (s == noErr && ps && psSize > 0) {
                write_start_code(dst);
                memcpy(*dst, ps, psSize);
                *dst += psSize;
            }
        }
    }
    return 0;
}

static uint8_t* make_annexb_from_block(EncoderContext *ctx, const uint8_t *src, size_t srcLen, int isKey, size_t *outLen) {
    if (srcLen == 0) { *outLen = 0; return NULL; }

    // First pass: count NALs and compute output size (start codes + payloads)
    size_t pos = 0, nalCount = 0, payloadBytes = 0;
    const int N = ctx->nalLengthSize > 0 ? ctx->nalLengthSize : 4;

    while (pos + (size_t)N <= srcLen) {
        uint32_t nalSize = 0;
        for (int i = 0; i < N; i++) { nalSize = (nalSize << 8) | src[pos + i]; }
        pos += (size_t)N;
        if (pos + nalSize > srcLen) break; // corrupted; stop at last good

        payloadBytes += nalSize;
        nalCount++;
        pos += nalSize;
    }

    // Parameter sets size (for keyframes)
    size_t paramBytes = 0;
    if (isKey && ctx->formatDesc) {
        if (ctx->codecType == kCMVideoCodecType_H264) {
            size_t count = 0; int dummy=0;
            CMVideoFormatDescriptionGetH264ParameterSetAtIndex((CMVideoFormatDescriptionRef)ctx->formatDesc, 0, NULL, NULL, &count, &dummy);
            for (size_t i = 0; i < count; i++) {
                const uint8_t *ps=NULL; size_t pss=0;
                if (CMVideoFormatDescriptionGetH264ParameterSetAtIndex((CMVideoFormatDescriptionRef)ctx->formatDesc, (int)i, &ps, &pss, NULL, NULL)==noErr) {
                    paramBytes += 4 + pss;
                }
            }
        } else {
            size_t count = 0; int dummy=0;
            CMVideoFormatDescriptionGetHEVCParameterSetAtIndex((CMVideoFormatDescriptionRef)ctx->formatDesc, 0, NULL, NULL, &count, &dummy);
            for (size_t i = 0; i < count; i++) {
                const uint8_t *ps=NULL; size_t pss=0;
                if (CMVideoFormatDescriptionGetHEVCParameterSetAtIndex((CMVideoFormatDescriptionRef)ctx->formatDesc, (int)i, &ps, &pss, NULL, NULL)==noErr) {
                    paramBytes += 4 + pss;
                }
            }
        }
    }

    // Each NAL gets a 4-byte start code
    size_t outSz = paramBytes + (size_t)4 * nalCount + payloadBytes;
    if (outSz == 0) { *outLen = 0; return NULL; }

    uint8_t *out = (uint8_t *)malloc(outSz);
    if (!out) { *outLen = 0; return NULL; }

    uint8_t *dst = out;

    // Write parameter sets first on keyframes
    if (isKey) {
        append_parameter_sets_annexb(ctx, &dst);
    }

    // Second pass: write all NALs with start codes
    pos = 0;
    while (pos + (size_t)N <= srcLen) {
        uint32_t nalSize = 0;
        for (int i = 0; i < N; i++) { nalSize = (nalSize << 8) | src[pos + i]; }
        pos += (size_t)N;
        if (pos + nalSize > srcLen) break;

        write_start_code(&dst);
        memcpy(dst, src + pos, nalSize);
        dst += nalSize;
        pos += nalSize;
    }

    *outLen = (size_t)(dst - out);
    return out;
}

// --- VT callback ------------------------------------------------------------

void compressionOutputCallback(void *outputCallbackRefCon,
                               void *sourceFrameRefCon,
                               OSStatus status,
                               VTEncodeInfoFlags infoFlags,
                               CMSampleBufferRef sampleBuffer) {
    EncoderContext *ctx = (EncoderContext *)outputCallbackRefCon;

    if (status != noErr) {
        ctx->lastError = status;
        if (sourceFrameRefCon) {
            dispatch_semaphore_signal(ctx->semaphore);
        }
        return;
    }

    if (infoFlags & kVTEncodeInfo_FrameDropped) {
        __sync_add_and_fetch(&ctx->droppedFrames, 1);
    }

    if (sampleBuffer != NULL) {
        // Ensure we know format details (codec / NAL length)
        ensure_format_desc_and_nal_len(ctx, sampleBuffer);

        // Retain and queue the sample
        CFRetain(sampleBuffer);
        pthread_mutex_lock(&ctx->frames_mutex);
        CFArrayAppendValue(ctx->encodedFrames, sampleBuffer);
        pthread_mutex_unlock(&ctx->frames_mutex);

        // Track keyframes and bytes (raw block size)
        if (sample_is_keyframe(sampleBuffer)) {
            __sync_add_and_fetch(&ctx->keyFrames, 1);
        }
        CMBlockBufferRef bb = CMSampleBufferGetDataBuffer(sampleBuffer);
        if (bb) {
            size_t length = CMBlockBufferGetDataLength(bb);
            __sync_add_and_fetch(&ctx->totalBytes, length);
        }
    }

    if (sourceFrameRefCon) {
        dispatch_semaphore_signal(ctx->semaphore);
    }
}

// --- Encoder creation/config ------------------------------------------------

EncoderContext* createEncoder(int width, int height, double frameRate, int bitrate,
                              int keyframeInterval, const char* codecType,
                              const char* profile, int maxBFrames) {
    EncoderContext *ctx = (EncoderContext *)calloc(1, sizeof(EncoderContext));
    if (!ctx) return NULL;

    ctx->encodedFrames = CFArrayCreateMutable(NULL, 0, &kCFTypeArrayCallBacks);
    if (!ctx->encodedFrames) {
        free(ctx);
        return NULL;
    }

    if (pthread_mutex_init(&ctx->frames_mutex, NULL) != 0) {
        CFRelease(ctx->encodedFrames);
        free(ctx);
        return NULL;
    }

    ctx->semaphore = dispatch_semaphore_create(0);
    if (!ctx->semaphore) {
        pthread_mutex_destroy(&ctx->frames_mutex);
        CFRelease(ctx->encodedFrames);
        free(ctx);
        return NULL;
    }

    // Encoder specification
    CFMutableDictionaryRef encoderSpec = CFDictionaryCreateMutable(
        NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);

    // Prefer/require hardware encoder
    CFDictionarySetValue(encoderSpec,
        kVTVideoEncoderSpecification_EnableHardwareAcceleratedVideoEncoder, kCFBooleanTrue);
#if defined(__arm64__) || defined(__aarch64__)
    CFDictionarySetValue(encoderSpec,
        kVTVideoEncoderSpecification_RequireHardwareAcceleratedVideoEncoder, kCFBooleanTrue);
#endif

    // Codec selection
    CMVideoCodecType codecTypeValue = kCMVideoCodecType_H264;
    if (strcmp(codecType, "h265") == 0 || strcmp(codecType, "hevc") == 0) {
        codecTypeValue = kCMVideoCodecType_HEVC;
    }
    ctx->codecType = codecTypeValue;

    // Create compression session
    OSStatus status = VTCompressionSessionCreate(
        NULL, width, height, codecTypeValue, encoderSpec, NULL, NULL,
        compressionOutputCallback, ctx, &ctx->session);

    CFRelease(encoderSpec);

    if (status != noErr || !ctx->session) {
        if (ctx->encodedFrames) CFRelease(ctx->encodedFrames);
        pthread_mutex_destroy(&ctx->frames_mutex);
        free(ctx);
        return NULL;
    }

    // Real-time
    VTSessionSetProperty(ctx->session, kVTCompressionPropertyKey_RealTime, kCFBooleanTrue);

    // Frame reordering (B-frames)
    if (maxBFrames > 0) {
        VTSessionSetProperty(ctx->session, kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanTrue);
    } else {
        VTSessionSetProperty(ctx->session, kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse);
    }

    // Profile
    CFStringRef profileLevel = NULL;
    if (codecTypeValue == kCMVideoCodecType_HEVC) {
        if (strcmp(profile, "main10") == 0) {
            profileLevel = kVTProfileLevel_HEVC_Main10_AutoLevel;
        } else {
            profileLevel = kVTProfileLevel_HEVC_Main_AutoLevel;
        }
    } else {
        if (strcmp(profile, "high") == 0) {
            profileLevel = kVTProfileLevel_H264_High_AutoLevel;
        } else if (strcmp(profile, "main") == 0) {
            profileLevel = kVTProfileLevel_H264_Main_AutoLevel;
        } else {
            profileLevel = kVTProfileLevel_H264_Baseline_AutoLevel;
        }
    }
    if (profileLevel) {
        VTSessionSetProperty(ctx->session, kVTCompressionPropertyKey_ProfileLevel, profileLevel);
    }

    // Average bitrate
    CFNumberRef bitrateRef = CFNumberCreate(NULL, kCFNumberSInt32Type, &bitrate);
    VTSessionSetProperty(ctx->session, kVTCompressionPropertyKey_AverageBitRate, bitrateRef);
    CFRelease(bitrateRef);

    // Data rate limits: [bytesPerSecond, oneSecond]
    int bytesPerSec = bitrate * 3 / 2 / 8; // 1.5x average, convert bps->Bps
    int oneSecond = 1;
    CFNumberRef limitBytesRef = CFNumberCreate(NULL, kCFNumberSInt32Type, &bytesPerSec);
    CFNumberRef limitSecsRef  = CFNumberCreate(NULL, kCFNumberSInt32Type, &oneSecond);
    const void *vals[2] = { limitBytesRef, limitSecsRef };
    CFArrayRef limits = CFArrayCreate(NULL, vals, 2, &kCFTypeArrayCallBacks);
    VTSessionSetProperty(ctx->session, kVTCompressionPropertyKey_DataRateLimits, limits);
    CFRelease(limits);
    CFRelease(limitBytesRef);
    CFRelease(limitSecsRef);

    // Keyframe interval (in frames)
    CFNumberRef keyIntRef = CFNumberCreate(NULL, kCFNumberSInt32Type, &keyframeInterval);
    VTSessionSetProperty(ctx->session, kVTCompressionPropertyKey_MaxKeyFrameInterval, keyIntRef);
    CFRelease(keyIntRef);

    // Also set by duration (seconds)
    double keyIntervalSeconds = keyframeInterval / (frameRate > 0.0 ? frameRate : 30.0);
    CFNumberRef keyDurRef = CFNumberCreate(NULL, kCFNumberDoubleType, &keyIntervalSeconds);
    VTSessionSetProperty(ctx->session, kVTCompressionPropertyKey_MaxKeyFrameIntervalDuration, keyDurRef);
    CFRelease(keyDurRef);

    // Expected frame rate
    CFNumberRef fpsRef = CFNumberCreate(NULL, kCFNumberDoubleType, &frameRate);
    VTSessionSetProperty(ctx->session, kVTCompressionPropertyKey_ExpectedFrameRate, fpsRef);
    CFRelease(fpsRef);

    VTCompressionSessionPrepareToEncodeFrames(ctx->session);

    // Memory tracking (debug)
    pthread_mutex_lock(&g_memory_mutex);
    g_allocated_memory += sizeof(EncoderContext);
    g_active_buffers++;
    pthread_mutex_unlock(&g_memory_mutex);

    return ctx;
}

// --- Encode frame -----------------------------------------------------------

int encodeFrame(EncoderContext *ctx, CVPixelBufferRef pixelBuffer,
                uint64_t timestamp, uint64_t duration, int forceKeyframe) {
    if (!ctx || !ctx->session || !pixelBuffer) return -1;

    // timestamps are in microseconds
    CMTime pts = CMTimeMakeWithSeconds((Float64)timestamp / 1000000.0, 1000000);
    CMTime dur = CMTimeMakeWithSeconds((Float64)duration  / 1000000.0, 1000000);

    VTEncodeInfoFlags infoFlags = 0;
    CFMutableDictionaryRef properties = NULL;

    if (forceKeyframe) {
        properties = CFDictionaryCreateMutable(NULL, 0,
            &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
        CFDictionarySetValue(properties, kVTEncodeFrameOptionKey_ForceKeyFrame, kCFBooleanTrue);
    }

    OSStatus status = VTCompressionSessionEncodeFrame(
        ctx->session, pixelBuffer, pts, dur, properties, (void *)1, &infoFlags);

    if (properties) CFRelease(properties);
    if (status != noErr) return status;

    return status;
}
int encodeFrame_wrap(EncoderContext *ctx,
                     CVPixelBufferRef pixelBuffer,
                     uint64_t timestamp,
                     uint64_t duration,
                     int forceKeyframe) {
    return encodeFrame(ctx, pixelBuffer, timestamp, duration, forceKeyframe);
}
int isNullPixelBuffer(CVPixelBufferRef pb) {
    return pb == NULL ? 1 : 0;
}

// --- Extract encoded frames -------------------------------------------------

EncodedFrame* getEncodedFrames(EncoderContext *ctx, int *count) {
    if (!ctx || !ctx->encodedFrames) {
        *count = 0;
        return NULL;
    }

    pthread_mutex_lock(&ctx->frames_mutex);
    CFIndex frameCount = CFArrayGetCount(ctx->encodedFrames);
    if (frameCount == 0) {
        pthread_mutex_unlock(&ctx->frames_mutex);
        *count = 0;
        return NULL;
    }

    // Copy out references to process outside the lock
    CMSampleBufferRef *local = (CMSampleBufferRef *)calloc(frameCount, sizeof(CMSampleBufferRef));
    for (CFIndex i = 0; i < frameCount; i++) {
        CMSampleBufferRef sb = (CMSampleBufferRef)CFArrayGetValueAtIndex(ctx->encodedFrames, i);
        local[i] = sb; // retained already in callback
    }
    // Clear array and release the lock here
    CFArrayRemoveAllValues(ctx->encodedFrames);
    pthread_mutex_unlock(&ctx->frames_mutex);

    EncodedFrame *frames = (EncodedFrame *)calloc(frameCount, sizeof(EncodedFrame));
    if (!frames) {
        // Clean up the sample buffers we dequeued
        for (CFIndex i = 0; i < frameCount; i++) {
            if (local[i]) CFRelease(local[i]);
        }
        free(local);
        *count = 0;
        return NULL;
    }

    for (CFIndex i = 0; i < frameCount; i++) {
        CMSampleBufferRef sbuf = local[i];
        if (!sbuf) continue;

        // Timing
        CMTime pts = CMSampleBufferGetPresentationTimeStamp(sbuf);
        CMTime dts = CMSampleBufferGetDecodeTimeStamp(sbuf);
        frames[i].pts = (int64_t)(CMTimeGetSeconds(pts) * 1000000.0);
        frames[i].dts = CMTIME_IS_VALID(dts) ? (int64_t)(CMTimeGetSeconds(dts) * 1000000.0) : frames[i].pts;

        // Keyframe?
        frames[i].isKeyframe = sample_is_keyframe(sbuf);

        // Make Annex-B payload
        CMBlockBufferRef bb = CMSampleBufferGetDataBuffer(sbuf);
        if (bb) {
            size_t srcLen = CMBlockBufferGetDataLength(bb);
            if (srcLen > 0) {
                uint8_t *tmp = (uint8_t *)malloc(srcLen);
                if (tmp) {
                    CMBlockBufferCopyDataBytes(bb, 0, srcLen, tmp);
                    size_t outLen = 0;
                    uint8_t *out = make_annexb_from_block(ctx, tmp, srcLen, frames[i].isKeyframe, &outLen);
                    free(tmp);
                    if (out && outLen > 0) {
                        frames[i].data = out;
                        frames[i].size = outLen;

                        pthread_mutex_lock(&g_memory_mutex);
                        g_allocated_memory += outLen;
                        pthread_mutex_unlock(&g_memory_mutex);
                    }
                }
            }
        }

        // release sample buffer now that we've consumed it
        CFRelease(sbuf);
    }

    free(local);

    *count = (int)frameCount;
    return frames;
}

void freeEncodedFrames(EncodedFrame *frames, int count) {
    if (!frames) return;
    for (int i = 0; i < count; i++) {
        if (frames[i].data) {
            pthread_mutex_lock(&g_memory_mutex);
            g_allocated_memory -= frames[i].size;
            pthread_mutex_unlock(&g_memory_mutex);
            free(frames[i].data);
        }
    }
    free(frames);
}

// --- Flush & destroy --------------------------------------------------------

int flushEncoder(EncoderContext *ctx) {
    if (!ctx || !ctx->session) return -1;
    return VTCompressionSessionCompleteFrames(ctx->session, kCMTimeInvalid);
}

void destroyEncoder(EncoderContext *ctx) {
    if (!ctx) return;

    if (ctx->session) {
        VTCompressionSessionInvalidate(ctx->session);
        CFRelease(ctx->session);
        ctx->session = NULL;
    }

    if (ctx->encodedFrames) {
        // release any queued sample buffers
        pthread_mutex_lock(&ctx->frames_mutex);
        CFIndex count = CFArrayGetCount(ctx->encodedFrames);
        for (CFIndex i = 0; i < count; i++) {
            CMSampleBufferRef sb = (CMSampleBufferRef)CFArrayGetValueAtIndex(ctx->encodedFrames, i);
            if (sb) CFRelease(sb);
        }
        CFRelease(ctx->encodedFrames);
        ctx->encodedFrames = NULL;
        pthread_mutex_unlock(&ctx->frames_mutex);
    }

    if (ctx->formatDesc) {
        CFRelease(ctx->formatDesc);
        ctx->formatDesc = NULL;
    }

#if !OS_OBJECT_USE_OBJC
    if (ctx->semaphore) {
        dispatch_release(ctx->semaphore);
    }
#endif
    ctx->semaphore = NULL;

    pthread_mutex_destroy(&ctx->frames_mutex);

    pthread_mutex_lock(&g_memory_mutex);
    g_allocated_memory -= sizeof(EncoderContext);
    g_active_buffers--;
    pthread_mutex_unlock(&g_memory_mutex);

    free(ctx);
}

// --- Metrics helpers --------------------------------------------------------

int64_t getEncoderBytesEncoded(EncoderContext *ctx) {
    return ctx ? ctx->totalBytes : 0;
}
int getEncoderFrameCount(EncoderContext *ctx) {
    return ctx ? ctx->frameCount : 0;
}
int getEncoderDroppedFrames(EncoderContext *ctx) {
    return ctx ? ctx->droppedFrames : 0;
}
int getEncoderKeyFrames(EncoderContext *ctx) {
    return ctx ? ctx->keyFrames : 0;
}
int64_t getGlobalAllocatedMemory() {
    pthread_mutex_lock(&g_memory_mutex);
    int64_t mem = g_allocated_memory;
    pthread_mutex_unlock(&g_memory_mutex);
    return mem;
}
int getGlobalActiveBuffers() {
    pthread_mutex_lock(&g_memory_mutex);
    int buffers = g_active_buffers;
    pthread_mutex_unlock(&g_memory_mutex);
    return buffers;
}
*/
import "C"

import (
	"fmt"
	"image"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// VideoToolboxEncoder implements hardware-accelerated encoding on macOS
type VideoToolboxEncoder struct {
	ctx        *C.EncoderContext
	config     EncoderConfig
	metrics    EncoderMetrics
	mu         sync.RWMutex
	closed     atomic.Bool
	pixelPool  *PixelBufferPool
	startTime  time.Time
	lastEncode time.Time
}

// NewVideoToolboxEncoder creates a new hardware-accelerated encoder
func NewVideoToolboxEncoder(config EncoderConfig) (*VideoToolboxEncoder, error) {
	codecType := C.CString(config.Codec)
	defer C.free(unsafe.Pointer(codecType))

	profile := C.CString(config.Profile)
	defer C.free(unsafe.Pointer(profile))

	ctx := C.createEncoder(
		C.int(config.Width),
		C.int(config.Height),
		C.double(config.FrameRate),
		C.int(config.Bitrate),
		C.int(config.KeyframeInterval),
		codecType,
		profile,
		C.int(config.MaxBFrames),
	)
	if ctx == nil {
		return nil, &EncoderError{
			Code:    -1,
			Message: "Failed to create VideoToolbox encoder - ensure hardware acceleration is available",
			Fatal:   true,
		}
	}

	encoder := &VideoToolboxEncoder{
		ctx:       ctx,
		config:    config,
		startTime: time.Now(),
		pixelPool: NewPixelBufferPool(config.Width, config.Height, 5),
	}

	// Finalizer as a safety net
	runtime.SetFinalizer(encoder, (*VideoToolboxEncoder).finalize)
	encoder.metrics.HardwareAccelerated = true
	return encoder, nil
}

// Encode processes a frame and returns encoded data (first output sample, if any)
// Encode processes a frame and returns encoded data (first output sample, if any)
func (e *VideoToolboxEncoder) Encode(frame image.Image, pts time.Duration) ([]byte, error) {
	if e.closed.Load() {
		return nil, &EncoderError{Code: -1, Message: "Encoder is closed", Fatal: true}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Get pixel buffer from pool; ensure the pool returns C.CVPixelBufferRef
	pb := e.pixelPool.Get() // type: C.CVPixelBufferRef
	if C.isNullPixelBuffer(pb) != 0 {
		return nil, &EncoderError{Code: -2, Message: "Failed to get pixel buffer from pool", Fatal: false}
	}
	defer e.pixelPool.Put(pb)

	// Copy the image into the pixel buffer
	if err := copyImageToPixelBuffer(frame, pb); err != nil {
		return nil, err
	}

	encodeStart := time.Now()

	// Frame duration in microseconds
	frameDurationMicros := uint64(math.Round(1_000_000.0 / math.Max(e.config.FrameRate, 1.0)))
	timestamp := C.uint64_t(pts.Microseconds())
	duration := C.uint64_t(frameDurationMicros)

	// Force a keyframe on interval
	forceKey := C.int(0)
	if e.metrics.FramesEncoded > 0 && e.config.KeyframeInterval > 0 &&
		e.metrics.FramesEncoded%uint64(e.config.KeyframeInterval) == 0 {
		forceKey = 1
	}

	// Call the C wrapper directly (no unsafe casts)
	status := C.encodeFrame_wrap(e.ctx, pb, timestamp, duration, forceKey)
	if status != 0 {
		return nil, &EncoderError{Code: int(status), Message: fmt.Sprintf("VT encode failed: %d", status), Fatal: false}
	}

	// Adaptive timeout: max(500ms, 10× frame duration)
	waitNS := time.Duration(frameDurationMicros*10) * time.Microsecond
	if waitNS < 500*time.Millisecond {
		waitNS = 500 * time.Millisecond
	}
	timeout := C.dispatch_time(C.DISPATCH_TIME_NOW, C.int64_t(waitNS.Nanoseconds()))
	if C.dispatch_semaphore_wait(e.ctx.semaphore, timeout) != 0 {
		atomic.AddUint64(&e.metrics.DroppedFrames, 1)
		return nil, &EncoderError{Code: -2, Message: "Encode timeout waiting for VT callback", Fatal: false}
	}

	encodeTime := time.Since(encodeStart)

	// Pull encoded frames (could be 0, 1, or >1 due to B-frames)
	var count C.int
	cFrames := C.getEncodedFrames(e.ctx, &count)
	if cFrames == nil || int(count) == 0 {
		return nil, nil // nothing ready yet; encoder buffered internally
	}
	defer C.freeEncodedFrames(cFrames, count)

	// Convert the C array into a Go slice view (this use of unsafe is conventional in cgo)
	goFrames := (*[1 << 28]C.EncodedFrame)(unsafe.Pointer(cFrames))[:int(count):int(count)]

	// Update metrics with produced frames
	atomic.AddUint64(&e.metrics.FramesEncoded, uint64(len(goFrames)))

	// Return the first frame's bytes
	first := goFrames[0]
	data := C.GoBytes(unsafe.Pointer(first.data), C.int(first.size))

	atomic.AddUint64(&e.metrics.BytesEncoded, uint64(first.size))
	e.metrics.LastFrameSize = int(first.size)
	if int(first.isKeyframe) == 1 {
		atomic.AddUint64(&e.metrics.KeyFrames, 1)
	}

	e.metrics.EncodingTime += encodeTime
	if e.metrics.FramesEncoded > 0 {
		e.metrics.AverageFrameTime = e.metrics.EncodingTime / time.Duration(e.metrics.FramesEncoded)
	}

	// Compute current bitrate
	elapsed := time.Since(e.startTime).Seconds()
	if elapsed > 0 {
		e.metrics.CurrentBitrate = float64(e.metrics.BytesEncoded*8) / elapsed
	}
	e.lastEncode = time.Now()

	return data, nil
}

// internal helper: must be called with e.mu held
func (e *VideoToolboxEncoder) flushLocked() ([][]byte, error) {
	status := C.flushEncoder(e.ctx)
	if status != 0 {
		return nil, &EncoderError{Code: int(status), Message: fmt.Sprintf("Flush failed with status %d", status), Fatal: false}
	}

	var count C.int
	cFrames := C.getEncodedFrames(e.ctx, &count)
	if cFrames == nil || int(count) == 0 {
		return nil, nil
	}
	defer C.freeEncodedFrames(cFrames, count)

	goFrames := (*[1 << 28]C.EncodedFrame)(unsafe.Pointer(cFrames))[:int(count):int(count)]
	out := make([][]byte, 0, len(goFrames))
	for i := range goFrames {
		f := goFrames[i]
		b := C.GoBytes(unsafe.Pointer(f.data), C.int(f.size))
		out = append(out, b)

		atomic.AddUint64(&e.metrics.BytesEncoded, uint64(f.size))
		if int(f.isKeyframe) == 1 {
			atomic.AddUint64(&e.metrics.KeyFrames, 1)
		}
	}
	// Update frame count with however many were flushed
	atomic.AddUint64(&e.metrics.FramesEncoded, uint64(len(goFrames)))
	return out, nil
}

// Flush forces encoding of any buffered frames
func (e *VideoToolboxEncoder) Flush() ([][]byte, error) {
	if e.closed.Load() {
		return nil, &EncoderError{Code: -1, Message: "Encoder is closed", Fatal: true}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.flushLocked()
}

// GetMetrics returns current encoder statistics
func (e *VideoToolboxEncoder) GetMetrics() *EncoderMetrics {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Pull C-side counters
	e.metrics.DroppedFrames = uint64(C.getEncoderDroppedFrames(e.ctx))
	e.metrics.MemoryAllocated = uint64(C.getGlobalAllocatedMemory())
	e.metrics.ActiveBuffers = int(C.getGlobalActiveBuffers())

	m := e.metrics // copy
	return &m
}

// Close releases all resources
func (e *VideoToolboxEncoder) Close() error {
	if !e.closed.CompareAndSwap(false, true) {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Flush under the lock with the no-deadlock helper
	_, _ = e.flushLocked()

	if e.pixelPool != nil {
		e.pixelPool.Close()
		e.pixelPool = nil
	}

	if e.ctx != nil {
		C.destroyEncoder(e.ctx)
		e.ctx = nil
	}

	runtime.SetFinalizer(e, nil)
	return nil
}

// finalize is called by GC if Close wasn't called
func (e *VideoToolboxEncoder) finalize() {
	if !e.closed.Load() {
		_ = e.Close()
	}
}
