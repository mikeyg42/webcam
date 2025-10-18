// storage/minio.go
package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/zap"
)

// MinIOStore implements ObjectStore using MinIO
type MinIOStore struct {
	client       *minio.Client
	bucket       string
	region       string
	logger       *zap.Logger
	config       MinIOConfig
	uploadPool   chan struct{}
	downloadPool chan struct{}

	metrics MinIOMetrics

	mu sync.RWMutex
}

// MinIOConfig contains MinIO configuration
type MinIOConfig struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	UseSSL          bool
	Bucket          string
	Region          string

	// Connection pooling
	MaxUploads   int
	MaxDownloads int

	// Timeouts
	ConnectTimeout time.Duration
	RequestTimeout time.Duration

	// Retry settings (best-effort; MinIO client also retries internally)
	MaxRetries   int
	RetryBackoff time.Duration

	// Multipart settings
	PartSize    int64 // default: 64MB
	Concurrency int   // concurrent part uploads
}

// MinIOMetrics tracks MinIO operations
type MinIOMetrics struct {
	TotalUploads    atomic.Uint64
	TotalDownloads  atomic.Uint64
	TotalDeletes    atomic.Uint64
	UploadBytes     atomic.Uint64
	DownloadBytes   atomic.Uint64
	UploadErrors    atomic.Uint64
	DownloadErrors  atomic.Uint64
	ActiveUploads   atomic.Int32
	ActiveDownloads atomic.Int32
}

// NewMinIOStore creates a new MinIO object store
func NewMinIOStore(config MinIOConfig) (*MinIOStore, error) {
	// Defaults
	if config.MaxUploads == 0 {
		config.MaxUploads = 10
	}
	if config.MaxDownloads == 0 {
		config.MaxDownloads = 20
	}
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = 30 * time.Second
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 5 * time.Minute
	}
	if config.PartSize == 0 {
		config.PartSize = 64 * 1024 * 1024 // 64MB
	}
	if config.Concurrency == 0 {
		config.Concurrency = 4
	}
	if config.MaxRetries < 0 {
		config.MaxRetries = 0
	}

	// Client
	minioClient, err := minio.New(config.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKeyID, config.SecretAccessKey, ""),
		Secure: config.UseSSL,
		Region: config.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	store := &MinIOStore{
		client:       minioClient,
		bucket:       config.Bucket,
		region:       config.Region,
		logger:       zap.L().Named("minio-store"),
		config:       config,
		uploadPool:   make(chan struct{}, config.MaxUploads),
		downloadPool: make(chan struct{}, config.MaxDownloads),
	}

	// Pools
	for i := 0; i < config.MaxUploads; i++ {
		store.uploadPool <- struct{}{}
	}
	for i := 0; i < config.MaxDownloads; i++ {
		store.downloadPool <- struct{}{}
	}

	// Ensure bucket exists (or create)
	ctx, cancel := context.WithTimeout(context.Background(), config.ConnectTimeout)
	defer cancel()

	exists, err := minioClient.BucketExists(ctx, config.Bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket: %w", err)
	}
	if !exists {
		err = minioClient.MakeBucket(ctx, config.Bucket, minio.MakeBucketOptions{Region: config.Region})
		if err != nil {
			return nil, fmt.Errorf("failed to create bucket: %w", err)
		}
		store.logger.Info("Created MinIO bucket", zap.String("bucket", config.Bucket))
	}

	return store, nil
}

// Put uploads an object to storage
func (s *MinIOStore) Put(ctx context.Context, key string, reader io.Reader, size int64, opts ...PutOption) error {
	// Options
	options := &putOptions{
		ContentType: "application/octet-stream",
	}
	for _, opt := range opts {
		opt.applyPut(options)
	}

	// Acquire upload slot
	select {
	case <-s.uploadPool:
		defer func() { s.uploadPool <- struct{}{} }()
	case <-ctx.Done():
		return ctx.Err()
	}
	s.metrics.ActiveUploads.Add(1)
	defer s.metrics.ActiveUploads.Add(-1)

	// MinIO options
	putOpts := minio.PutObjectOptions{
		ContentType:  options.ContentType,
		UserMetadata: options.Metadata,
	}
	if options.CacheControl != "" {
		putOpts.CacheControl = options.CacheControl
	}
	if options.ContentEncoding != "" {
		putOpts.ContentEncoding = options.ContentEncoding
	}
	// Note: StorageClass and ServerSideEncryption would need additional mapping
	// For SSE, you'd import github.com/minio/minio-go/v7/pkg/encrypt and use encrypt.NewSSE()

	// Fresh backoff per operation
	newBackoff := func() backoff.BackOff {
		ebo := backoff.NewExponentialBackOff()
		if s.config.RetryBackoff > 0 {
			ebo.InitialInterval = s.config.RetryBackoff
		}
		ebo.Reset()
		if s.config.MaxRetries > 0 {
			return backoff.WithMaxRetries(ebo, uint64(s.config.MaxRetries))
		}
		return ebo
	}

	attempt := 0
	op := func() error {
		attempt++

		// If we need to retry, we must rewind seekable readers.
		if rs, ok := reader.(io.ReadSeeker); ok {
			if attempt > 1 {
				if _, err := rs.Seek(0, io.SeekStart); err != nil {
					return backoff.Permanent(fmt.Errorf("seek reset failed: %w", err))
				}
			}
		} else if attempt > 1 {
			// Non-seekable reader cannot be retried safely.
			return backoff.Permanent(fmt.Errorf("reader not seekable; not retrying"))
		}

		// Build per-attempt progress wrapper
		progressReader := reader
		if options.ProgressFn != nil {
			progressReader = &progressReaderWriter{
				reader:     reader,
				total:      size,
				progressFn: options.ProgressFn,
			}
		}

		info, err := s.client.PutObject(ctx, s.bucket, key, progressReader, size, putOpts)
		if err != nil {
			s.metrics.UploadErrors.Add(1)
			return err
		}

		s.metrics.TotalUploads.Add(1)
		s.metrics.UploadBytes.Add(uint64(info.Size))

		s.logger.Debug("Object uploaded",
			zap.String("key", key),
			zap.Int64("size", info.Size),
			zap.String("etag", info.ETag))

		return nil
	}

	if err := backoff.Retry(op, backoff.WithContext(newBackoff(), ctx)); err != nil {
		return &StorageError{
			Op:        "put",
			Key:       key,
			Err:       err,
			Retryable: true,
		}
	}

	return nil
}

// PutFile uploads a file to storage
func (s *MinIOStore) PutFile(ctx context.Context, key, filePath string, opts ...PutOption) error {
	file, err := os.Open(filePath)
	if err != nil {
		return &StorageError{Op: "put_file", Key: key, Err: err}
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return &StorageError{Op: "put_file", Key: key, Err: err}
	}

	options := &putOptions{}
	for _, opt := range opts {
		opt.applyPut(options)
	}
	if options.ContentType == "" {
		options.ContentType = detectContentType(filePath)
		opts = append(opts, WithContentType(options.ContentType))
	}

	return s.Put(ctx, key, file, stat.Size(), opts...)
}

// Get downloads an object from storage
func (s *MinIOStore) Get(ctx context.Context, key string, opts ...GetOption) (io.ReadCloser, error) {
	options := &getOptions{}
	for _, opt := range opts {
		opt.applyGet(options)
	}

	// Acquire download slot
	select {
	case <-s.downloadPool:
		// released on Close
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.metrics.ActiveDownloads.Add(1)

	getOpts := minio.GetObjectOptions{}
	if options.Range != nil {
		if err := getOpts.SetRange(options.Range.Start, options.Range.End); err != nil {
			s.downloadPool <- struct{}{}
			s.metrics.ActiveDownloads.Add(-1)
			return nil, err
		}
	}

	obj, err := s.client.GetObject(ctx, s.bucket, key, getOpts)
	if err != nil {
		s.downloadPool <- struct{}{}
		s.metrics.ActiveDownloads.Add(-1)
		s.metrics.DownloadErrors.Add(1)
		return nil, &StorageError{Op: "get", Key: key, Err: err}
	}

	stat, err := obj.Stat()
	if err != nil {
		obj.Close()
		s.downloadPool <- struct{}{}
		s.metrics.ActiveDownloads.Add(-1)
		return nil, &StorageError{
			Op:         "get",
			Key:        key,
			Err:        err,
			StatusCode: getMinioStatusCode(err),
		}
	}

	s.metrics.TotalDownloads.Add(1)

	return &trackedReadCloser{
		ReadCloser: obj,
		size:       stat.Size,
		progressFn: options.ProgressFn,
		onClose: func() {
			s.downloadPool <- struct{}{}
			s.metrics.ActiveDownloads.Add(-1)
			s.metrics.DownloadBytes.Add(uint64(stat.Size))
		},
	}, nil
}

// GetToFile downloads an object to a file
func (s *MinIOStore) GetToFile(ctx context.Context, key, filePath string, opts ...GetOption) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &StorageError{Op: "get_to_file", Key: key, Err: err}
	}

	tmp := filePath + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return &StorageError{Op: "get_to_file", Key: key, Err: err}
	}
	defer file.Close()

	reader, err := s.Get(ctx, key, opts...)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	defer reader.Close()

	if _, err = io.Copy(file, reader); err != nil {
		_ = os.Remove(tmp)
		return &StorageError{Op: "get_to_file", Key: key, Err: err}
	}

	if err = file.Sync(); err != nil {
		_ = os.Remove(tmp)
		return &StorageError{Op: "get_to_file", Key: key, Err: err}
	}

	if err = os.Rename(tmp, filePath); err != nil {
		_ = os.Remove(tmp)
		return &StorageError{Op: "get_to_file", Key: key, Err: err}
	}

	return nil
}

// Delete removes an object from storage
func (s *MinIOStore) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return &StorageError{Op: "delete", Key: key, Err: err}
	}
	s.metrics.TotalDeletes.Add(1)
	return nil
}

// DeleteMultiple removes multiple objects
func (s *MinIOStore) DeleteMultiple(ctx context.Context, keys []string) error {
	objectsCh := make(chan minio.ObjectInfo)

	go func() {
		defer close(objectsCh)
		for _, key := range keys {
			select {
			case objectsCh <- minio.ObjectInfo{Key: key}:
			case <-ctx.Done():
				return
			}
		}
	}()

	errCh := s.client.RemoveObjects(ctx, s.bucket, objectsCh, minio.RemoveObjectsOptions{})

	var errs []error
	for e := range errCh {
		errs = append(errs, e.Err)
		s.logger.Error("Failed to delete object",
			zap.String("key", e.ObjectName),
			zap.Error(e.Err))
	}

	if len(errs) > 0 {
		return &StorageError{
			Op:  "delete_multiple",
			Err: fmt.Errorf("failed to delete %d objects", len(errs)),
		}
	}

	s.metrics.TotalDeletes.Add(uint64(len(keys)))
	return nil
}

// Exists checks if an object exists
func (s *MinIOStore) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return false, nil
		}
		return false, &StorageError{Op: "exists", Key: key, Err: err}
	}
	return true, nil
}

// List lists objects with a prefix
func (s *MinIOStore) List(ctx context.Context, prefix string, opts ...ListOption) ([]ObjectInfo, error) {
	options := &listOptions{MaxKeys: 1000, Recursive: false}
	for _, opt := range opts {
		opt.applyList(options)
	}

	listOpts := minio.ListObjectsOptions{
		Prefix:     prefix,
		Recursive:  options.Recursive,
		MaxKeys:    options.MaxKeys,
		StartAfter: options.StartAfter,
	}

	var objects []ObjectInfo
	for obj := range s.client.ListObjects(ctx, s.bucket, listOpts) {
		if obj.Err != nil {
			return nil, &StorageError{Op: "list", Err: obj.Err}
		}
		objects = append(objects, ObjectInfo{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
			ETag:         obj.ETag,
			ContentType:  obj.ContentType,
			Metadata:     nil,
			StorageClass: obj.StorageClass,
		})
	}
	return objects, nil
}

// GeneratePresignedURL generates a pre-signed URL for downloading
func (s *MinIOStore) GeneratePresignedURL(ctx context.Context, key string, expiry time.Duration, opts ...URLOption) (string, error) {
	options := &urlOptions{}
	for _, opt := range opts {
		opt.applyURL(options)
	}

	reqParams := url.Values{}
	if options.ContentDisposition != "" {
		reqParams.Set("response-content-disposition", options.ContentDisposition)
	}
	if options.ContentType != "" {
		reqParams.Set("response-content-type", options.ContentType)
	}
	for k, v := range options.ResponseHeaders {
		reqParams.Set(k, v)
	}

	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, expiry, reqParams)
	if err != nil {
		return "", &StorageError{Op: "generate_url", Key: key, Err: err}
	}
	return u.String(), nil
}

// GeneratePresignedUploadURL generates a pre-signed URL for uploading
func (s *MinIOStore) GeneratePresignedUploadURL(ctx context.Context, key string, expiry time.Duration, _ ...URLOption) (string, error) {
	u, err := s.client.PresignedPutObject(ctx, s.bucket, key, expiry)
	if err != nil {
		return "", &StorageError{Op: "generate_upload_url", Key: key, Err: err}
	}
	return u.String(), nil
}

// InitiateMultipartUpload starts a multipart upload (simplified placeholder)
func (s *MinIOStore) InitiateMultipartUpload(ctx context.Context, key string, _ ...PutOption) (string, error) {
	uploadID := fmt.Sprintf("upload_%s_%d", key, time.Now().UnixNano())
	s.logger.Info("Initiated multipart upload",
		zap.String("key", key),
		zap.String("upload_id", uploadID))
	return uploadID, nil
}

// UploadPart uploads a part of a multipart upload (simplified placeholder)
func (s *MinIOStore) UploadPart(_ context.Context, key, uploadID string, partNumber int, _ io.Reader, size int64) (string, error) {
	etag := fmt.Sprintf("etag_%d", partNumber)
	s.logger.Debug("Uploaded part",
		zap.String("key", key),
		zap.String("upload_id", uploadID),
		zap.Int("part_number", partNumber),
		zap.Int64("size", size))
	return etag, nil
}

// CompleteMultipartUpload completes a multipart upload (simplified placeholder)
func (s *MinIOStore) CompleteMultipartUpload(_ context.Context, key, uploadID string, parts []CompletedPart) error {
	s.logger.Info("Completed multipart upload",
		zap.String("key", key),
		zap.String("upload_id", uploadID),
		zap.Int("parts", len(parts)))
	return nil
}

// AbortMultipartUpload aborts a multipart upload (simplified placeholder)
func (s *MinIOStore) AbortMultipartUpload(_ context.Context, key, uploadID string) error {
	s.logger.Info("Aborted multipart upload",
		zap.String("key", key),
		zap.String("upload_id", uploadID))
	return nil
}

// GetBucketInfo returns information about the bucket
func (s *MinIOStore) GetBucketInfo(ctx context.Context) (*BucketInfo, error) {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return nil, &StorageError{Op: "get_bucket_info", Err: err}
	}

	info := &BucketInfo{
		Name:      s.bucket,
		Region:    s.region,
		Available: exists,
	}

	var totalSize int64
	var objectCount int64
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			break
		}
		objectCount++
		totalSize += obj.Size
	}
	info.TotalSize = totalSize
	info.ObjectCount = objectCount

	return info, nil
}

// SetObjectMetadata sets metadata for an object (copy-with-metadata)
func (s *MinIOStore) SetObjectMetadata(ctx context.Context, key string, metadata map[string]string) error {
	src := minio.CopySrcOptions{Bucket: s.bucket, Object: key}
	dst := minio.CopyDestOptions{Bucket: s.bucket, Object: key, UserMetadata: metadata}

	if _, err := s.client.CopyObject(ctx, dst, src); err != nil {
		return &StorageError{Op: "set_metadata", Key: key, Err: err}
	}
	return nil
}

// GetObjectMetadata gets metadata for an object
func (s *MinIOStore) GetObjectMetadata(ctx context.Context, key string) (map[string]string, error) {
	stat, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, &StorageError{
			Op:         "get_metadata",
			Key:        key,
			Err:        err,
			StatusCode: getMinioStatusCode(err),
		}
	}
	return stat.UserMetadata, nil
}

// HealthCheck verifies the storage is accessible
func (s *MinIOStore) HealthCheck(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return &StorageError{Op: "health_check", Err: err}
	}
	if !exists {
		return &StorageError{Op: "health_check", Err: fmt.Errorf("bucket %s does not exist", s.bucket)}
	}
	return nil
}

// GetMetrics returns storage metrics
func (s *MinIOStore) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"total_uploads":    s.metrics.TotalUploads.Load(),
		"total_downloads":  s.metrics.TotalDownloads.Load(),
		"total_deletes":    s.metrics.TotalDeletes.Load(),
		"upload_bytes":     s.metrics.UploadBytes.Load(),
		"download_bytes":   s.metrics.DownloadBytes.Load(),
		"upload_errors":    s.metrics.UploadErrors.Load(),
		"download_errors":  s.metrics.DownloadErrors.Load(),
		"active_uploads":   s.metrics.ActiveUploads.Load(),
		"active_downloads": s.metrics.ActiveDownloads.Load(),
	}
}

// progressReaderWriter reports per-read progress.
type progressReaderWriter struct {
	reader     io.Reader
	total      int64
	read       int64
	progressFn ProgressFunc
	mu         sync.Mutex
}

func (p *progressReaderWriter) Read(b []byte) (int, error) {
	n, err := p.reader.Read(b)
	p.mu.Lock()
	p.read += int64(n)
	if p.progressFn != nil {
		p.progressFn(p.read, p.total)
	}
	p.mu.Unlock()
	return n, err
}

// trackedReadCloser wraps a ReadCloser to track progress and release resources
type trackedReadCloser struct {
	io.ReadCloser
	size       int64
	read       int64
	progressFn ProgressFunc
	onClose    func()
	mu         sync.Mutex
}

func (t *trackedReadCloser) Read(b []byte) (int, error) {
	n, err := t.ReadCloser.Read(b)
	t.mu.Lock()
	t.read += int64(n)
	if t.progressFn != nil {
		t.progressFn(t.read, t.size)
	}
	t.mu.Unlock()
	return n, err
}

func (t *trackedReadCloser) Close() error {
	err := t.ReadCloser.Close()
	if t.onClose != nil {
		t.onClose()
	}
	return err
}

// detectContentType attempts to detect content type from file extension
func detectContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".mp4":
		return "video/mp4"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	default:
		return "application/octet-stream"
	}
}

// getMinioStatusCode extracts HTTP status code from MinIO error
func getMinioStatusCode(err error) int {
	if errResp := minio.ToErrorResponse(err); errResp.Code != "" {
		switch errResp.Code {
		case "NoSuchKey":
			return 404
		case "AccessDenied":
			return 403
		case "InvalidArgument":
			return 400
		default:
			return 500
		}
	}
	return 500
}
