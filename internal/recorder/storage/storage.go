// storage/store.go
package storage

import (
	"context"
	"io"
	"time"
)

// ObjectStore defines the interface for object storage operations
type ObjectStore interface {
	// Core operations
	Put(ctx context.Context, key string, reader io.Reader, size int64, opts ...PutOption) error
	PutFile(ctx context.Context, key, filePath string, opts ...PutOption) error
	Get(ctx context.Context, key string, opts ...GetOption) (io.ReadCloser, error)
	GetToFile(ctx context.Context, key, filePath string, opts ...GetOption) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	
	// Bulk operations
	List(ctx context.Context, prefix string, opts ...ListOption) ([]ObjectInfo, error)
	DeleteMultiple(ctx context.Context, keys []string) error
	
	// URL generation
	GeneratePresignedURL(ctx context.Context, key string, expiry time.Duration, opts ...URLOption) (string, error)
	GeneratePresignedUploadURL(ctx context.Context, key string, expiry time.Duration, opts ...URLOption) (string, error)
	
	// Multipart operations for large files
	InitiateMultipartUpload(ctx context.Context, key string, opts ...PutOption) (string, error)
	UploadPart(ctx context.Context, key, uploadID string, partNumber int, reader io.Reader, size int64) (string, error)
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []CompletedPart) error
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error
	
	// Storage management
	GetBucketInfo(ctx context.Context) (*BucketInfo, error)
	SetObjectMetadata(ctx context.Context, key string, metadata map[string]string) error
	GetObjectMetadata(ctx context.Context, key string) (map[string]string, error)
	
	// Health check
	HealthCheck(ctx context.Context) error
}

// ObjectInfo contains information about a stored object
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
	ContentType  string
	Metadata     map[string]string
	StorageClass string
}

// BucketInfo contains information about the storage bucket
type BucketInfo struct {
	Name         string
	Region       string
	CreatedAt    time.Time
	TotalSize    int64
	ObjectCount  int64
	Available    bool
}

// CompletedPart represents a completed multipart upload part
type CompletedPart struct {
	PartNumber int
	ETag       string
}

// PutOption configures Put operations
type PutOption interface {
	applyPut(*putOptions)
}

type putOptions struct {
	ContentType          string
	Metadata             map[string]string
	StorageClass         string
	ServerSideEncryption bool
	CacheControl         string
	ContentEncoding      string
	ProgressFn           ProgressFunc
}

// GetOption configures Get operations
type GetOption interface {
	applyGet(*getOptions)
}

type getOptions struct {
	Range       *ByteRange
	IfMatch     string
	IfNoneMatch string
	ProgressFn  ProgressFunc
}

// ListOption configures List operations
type ListOption interface {
	applyList(*listOptions)
}

type listOptions struct {
	MaxKeys    int
	Delimiter  string
	StartAfter string
	Recursive  bool
}

// URLOption configures URL generation
type URLOption interface {
	applyURL(*urlOptions)
}

type urlOptions struct {
	ContentType        string
	ContentDisposition string
	ResponseHeaders    map[string]string
}

// ByteRange specifies a byte range for partial downloads
type ByteRange struct {
	Start int64
	End   int64
}

// ProgressFunc is called to report upload/download progress
type ProgressFunc func(bytesTransferred int64, totalBytes int64)

// Option implementations
type contentTypeOption string

func (o contentTypeOption) applyPut(opts *putOptions) { opts.ContentType = string(o) }
func (o contentTypeOption) applyURL(opts *urlOptions) { opts.ContentType = string(o) }

type metadataOption map[string]string

func (o metadataOption) applyPut(opts *putOptions) { opts.Metadata = o }

type progressOption struct{ fn ProgressFunc }

func (o progressOption) applyPut(opts *putOptions) { opts.ProgressFn = o.fn }
func (o progressOption) applyGet(opts *getOptions) { opts.ProgressFn = o.fn }

type rangeOption ByteRange

func (o rangeOption) applyGet(opts *getOptions) { opts.Range = (*ByteRange)(&o) }

type recursiveOption bool

func (o recursiveOption) applyList(opts *listOptions) { opts.Recursive = bool(o) }

// Additional option implementations
type cacheControlOption string

func (o cacheControlOption) applyPut(opts *putOptions) { opts.CacheControl = string(o) }

type contentEncodingOption string

func (o contentEncodingOption) applyPut(opts *putOptions) { opts.ContentEncoding = string(o) }

type storageClassOption string

func (o storageClassOption) applyPut(opts *putOptions) { opts.StorageClass = string(o) }

type serverSideEncryptionOption bool

func (o serverSideEncryptionOption) applyPut(opts *putOptions) { opts.ServerSideEncryption = bool(o) }

type ifMatchOption string

func (o ifMatchOption) applyGet(opts *getOptions) { opts.IfMatch = string(o) }

type ifNoneMatchOption string

func (o ifNoneMatchOption) applyGet(opts *getOptions) { opts.IfNoneMatch = string(o) }

type maxKeysOption int

func (o maxKeysOption) applyList(opts *listOptions) { opts.MaxKeys = int(o) }

type delimiterOption string

func (o delimiterOption) applyList(opts *listOptions) { opts.Delimiter = string(o) }

type startAfterOption string

func (o startAfterOption) applyList(opts *listOptions) { opts.StartAfter = string(o) }

type contentDispositionOption string

func (o contentDispositionOption) applyURL(opts *urlOptions) { opts.ContentDisposition = string(o) }

type responseHeadersOption map[string]string

func (o responseHeadersOption) applyURL(opts *urlOptions) {
	if opts.ResponseHeaders == nil {
		opts.ResponseHeaders = make(map[string]string, len(o))
	}
	for k, v := range o {
		opts.ResponseHeaders[k] = v
	}
}

// Helper functions for options
func WithContentType(contentType string) interface {
	PutOption
	URLOption
} {
	return contentTypeOption(contentType)
}

func WithMetadata(metadata map[string]string) PutOption {
	return metadataOption(metadata)
}

func WithProgress(fn ProgressFunc) interface {
	PutOption
	GetOption
} {
	return progressOption{fn: fn}
}

func WithRange(start, end int64) GetOption {
	return rangeOption{Start: start, End: end}
}

func WithRecursive(recursive bool) ListOption {
	return recursiveOption(recursive)
}

func WithCacheControl(cacheControl string) PutOption {
	return cacheControlOption(cacheControl)
}

func WithContentEncoding(encoding string) PutOption {
	return contentEncodingOption(encoding)
}

func WithStorageClass(class string) PutOption {
	return storageClassOption(class)
}

func WithServerSideEncryption(enabled bool) PutOption {
	return serverSideEncryptionOption(enabled)
}

func WithIfMatch(etag string) GetOption {
	return ifMatchOption(etag)
}

func WithIfNoneMatch(etag string) GetOption {
	return ifNoneMatchOption(etag)
}

func WithMaxKeys(n int) ListOption {
	return maxKeysOption(n)
}

func WithDelimiter(d string) ListOption {
	return delimiterOption(d)
}

func WithStartAfter(key string) ListOption {
	return startAfterOption(key)
}

func WithContentDisposition(disposition string) URLOption {
	return contentDispositionOption(disposition)
}

func WithResponseHeaders(h map[string]string) URLOption {
	return responseHeadersOption(h)
}

// StorageError represents a storage operation error
type StorageError struct {
	Op         string
	Key        string
	Err        error
	StatusCode int
	Retryable  bool
}

func (e *StorageError) Error() string {
	if e.Key != "" {
		return e.Op + " " + e.Key + ": " + e.Err.Error()
	}
	return e.Op + ": " + e.Err.Error()
}

func (e *StorageError) Unwrap() error {
	return e.Err
}

// IsNotExist returns true if the error indicates the object doesn't exist
func IsNotExist(err error) bool {
	if serr, ok := err.(*StorageError); ok {
		return serr.StatusCode == 404
	}
	return false
}

// IsAccessDenied returns true if the error indicates access was denied
func IsAccessDenied(err error) bool {
	if serr, ok := err.(*StorageError); ok {
		return serr.StatusCode == 403
	}
	return false
}