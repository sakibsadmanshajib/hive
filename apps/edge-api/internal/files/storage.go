package files

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// StorageClient wraps minio.Core with convenience methods for file storage operations.
// minio.Core embeds *minio.Client and additionally exposes low-level multipart methods.
type StorageClient struct {
	core minio.Core
}

// NewStorageClient creates a new S3-compatible storage client using minio-go.
func NewStorageClient(endpoint, accessKey, secretKey string, useSSL bool) (*StorageClient, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}
	return &StorageClient{core: minio.Core{Client: client}}, nil
}

// Upload stores an object in the given bucket at the given key.
func (s *StorageClient) Upload(ctx context.Context, bucket, key string, reader io.Reader, size int64, contentType string) error {
	_, err := s.core.Client.PutObject(ctx, bucket, key, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("storage: upload object %s/%s: %w", bucket, key, err)
	}
	return nil
}

// Download retrieves an object from the given bucket at the given key.
// The caller is responsible for closing the returned ReadCloser.
func (s *StorageClient) Download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	obj, err := s.core.Client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: download object %s/%s: %w", bucket, key, err)
	}
	return obj, nil
}

// Delete removes an object from the given bucket at the given key.
func (s *StorageClient) Delete(ctx context.Context, bucket, key string) error {
	err := s.core.Client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("storage: delete object %s/%s: %w", bucket, key, err)
	}
	return nil
}

// PresignedURL generates a presigned GET URL for the given object, valid for the given duration.
func (s *StorageClient) PresignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (*url.URL, error) {
	u, err := s.core.Client.PresignedGetObject(ctx, bucket, key, ttl, nil)
	if err != nil {
		return nil, fmt.Errorf("storage: presign object %s/%s: %w", bucket, key, err)
	}
	return u, nil
}

// InitMultipartUpload initiates a multipart upload and returns the upload ID.
func (s *StorageClient) InitMultipartUpload(ctx context.Context, bucket, key string, contentType string) (string, error) {
	uploadID, err := s.core.NewMultipartUpload(ctx, bucket, key, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", fmt.Errorf("storage: init multipart upload %s/%s: %w", bucket, key, err)
	}
	return uploadID, nil
}

// UploadPart uploads a single part of a multipart upload.
func (s *StorageClient) UploadPart(ctx context.Context, bucket, key, uploadID string, partNum int, reader io.Reader, size int64) (minio.ObjectPart, error) {
	part, err := s.core.PutObjectPart(ctx, bucket, key, uploadID, partNum, reader, size, minio.PutObjectPartOptions{})
	if err != nil {
		return minio.ObjectPart{}, fmt.Errorf("storage: upload part %d for %s/%s: %w", partNum, bucket, key, err)
	}
	return part, nil
}

// CompleteMultipartUpload finalizes a multipart upload by assembling all parts.
func (s *StorageClient) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []minio.CompletePart) error {
	_, err := s.core.CompleteMultipartUpload(ctx, bucket, key, uploadID, parts, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("storage: complete multipart upload %s/%s: %w", bucket, key, err)
	}
	return nil
}

// AbortMultipartUpload cancels an in-progress multipart upload.
func (s *StorageClient) AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error {
	err := s.core.AbortMultipartUpload(ctx, bucket, key, uploadID)
	if err != nil {
		return fmt.Errorf("storage: abort multipart upload %s/%s: %w", bucket, key, err)
	}
	return nil
}
