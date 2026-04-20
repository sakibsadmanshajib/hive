package storage

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

var _ Storage = (*S3Client)(nil)

type S3Client struct {
	endpoint   *url.URL
	accessKey  string
	secretKey  string
	region     string
	httpClient *http.Client
	signer     *v4.Signer
	now        func() time.Time
}

type completeMultipartUpload struct {
	XMLName xml.Name                `xml:"CompleteMultipartUpload"`
	Parts   []completeMultipartPart `xml:"Part"`
}

type completeMultipartPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type initiateMultipartUploadResult struct {
	UploadID string `xml:"UploadId"`
}

func NewS3Client(cfg Config) (*S3Client, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("S3_ENDPOINT is required")
	}
	if cfg.AccessKey == "" {
		return nil, fmt.Errorf("S3_ACCESS_KEY is required")
	}
	if cfg.SecretKey == "" {
		return nil, fmt.Errorf("S3_SECRET_KEY is required")
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("S3_REGION is required")
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		if err == nil {
			err = fmt.Errorf("missing URL scheme or host")
		}
		return nil, fmt.Errorf("S3_ENDPOINT is invalid: %w", err)
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &S3Client{
		endpoint:   endpoint,
		accessKey:  cfg.AccessKey,
		secretKey:  cfg.SecretKey,
		region:     cfg.Region,
		httpClient: cfg.HTTPClient,
		signer:     v4.NewSigner(),
		now:        cfg.Now,
	}, nil
}

func (c *S3Client) Upload(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	req, err := c.newObjectRequest(ctx, http.MethodPut, bucket, key, body, size, contentType)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return s3StatusError(resp, http.MethodPut, req.URL.Path)
}

func (c *S3Client) Download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	req, err := c.newObjectRequest(ctx, http.MethodGet, bucket, key, nil, 0, "")
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if err := s3StatusError(resp, http.MethodGet, req.URL.Path); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp.Body, nil
}

func (c *S3Client) Delete(ctx context.Context, bucket, key string) error {
	req, err := c.newObjectRequest(ctx, http.MethodDelete, bucket, key, nil, 0, "")
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return s3StatusError(resp, http.MethodDelete, req.URL.Path)
}

func (c *S3Client) PresignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.objectURL(bucket, key).String(), nil)
	if err != nil {
		return "", err
	}
	return presignHTTP(ctx, c.signer, req, c.accessKey, c.secretKey, c.region, c.now(), ttl)
}

func (c *S3Client) InitMultipartUpload(ctx context.Context, bucket, key, contentType string) (string, error) {
	u := c.objectURL(bucket, key)
	query := u.Query()
	query.Set("uploads", "")
	u.RawQuery = query.Encode()
	req, err := c.newSignedRequest(ctx, http.MethodPost, u, nil, 0, contentType)
	if err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := s3StatusError(resp, http.MethodPost, req.URL.Path); err != nil {
		return "", err
	}
	var result initiateMultipartUploadResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode multipart init response: %w", err)
	}
	if result.UploadID == "" {
		return "", fmt.Errorf("missing UploadId in multipart init response")
	}
	return result.UploadID, nil
}

func (c *S3Client) UploadPart(ctx context.Context, bucket, key, uploadID string, partNum int, body io.Reader, size int64) (string, error) {
	u := c.objectURL(bucket, key)
	query := u.Query()
	query.Set("partNumber", strconv.Itoa(partNum))
	query.Set("uploadId", uploadID)
	u.RawQuery = query.Encode()
	req, err := c.newSignedRequest(ctx, http.MethodPut, u, body, size, "")
	if err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := s3StatusError(resp, http.MethodPut, req.URL.Path); err != nil {
		return "", err
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return "", fmt.Errorf("missing ETag in multipart upload part response")
	}
	return etag, nil
}

func (c *S3Client) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []CompletePart) error {
	payload := completeMultipartUpload{
		Parts: make([]completeMultipartPart, 0, len(parts)),
	}
	for _, part := range parts {
		payload.Parts = append(payload.Parts, completeMultipartPart{
			PartNumber: part.PartNumber,
			ETag:       part.ETag,
		})
	}
	body, err := xml.Marshal(payload)
	if err != nil {
		return err
	}
	u := c.objectURL(bucket, key)
	query := u.Query()
	query.Set("uploadId", uploadID)
	u.RawQuery = query.Encode()
	req, err := c.newSignedRequest(ctx, http.MethodPost, u, bytes.NewReader(body), int64(len(body)), "application/xml")
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return s3StatusError(resp, http.MethodPost, req.URL.Path)
}

func (c *S3Client) AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error {
	u := c.objectURL(bucket, key)
	query := u.Query()
	query.Set("uploadId", uploadID)
	u.RawQuery = query.Encode()
	req, err := c.newSignedRequest(ctx, http.MethodDelete, u, nil, 0, "")
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return s3StatusError(resp, http.MethodDelete, req.URL.Path)
}

func (c *S3Client) newObjectRequest(ctx context.Context, method, bucket, key string, body io.Reader, size int64, contentType string) (*http.Request, error) {
	return c.newSignedRequest(ctx, method, c.objectURL(bucket, key), body, size, contentType)
}

func (c *S3Client) newSignedRequest(ctx context.Context, method string, u *url.URL, body io.Reader, size int64, contentType string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if size >= 0 {
		req.ContentLength = size
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if err := SignHTTP(ctx, c.signer, req, c.accessKey, c.secretKey, c.region, c.now(), ""); err != nil {
		return nil, err
	}
	return req, nil
}

func (c *S3Client) objectURL(bucket, key string) *url.URL {
	objectPath := appendEscapedPathSegment(nil, bucket)
	if key != "" {
		for _, segment := range strings.Split(key, "/") {
			objectPath = appendEscapedPathSegment(objectPath, segment)
		}
	}

	u := *c.endpoint
	u.RawQuery = ""
	u.Fragment = ""
	basePath := strings.TrimRight(u.Path, "/")
	baseRawPath := strings.TrimRight(u.EscapedPath(), "/")
	decodedObjectPath := bucket
	if key != "" {
		decodedObjectPath += "/" + key
	}
	if basePath == "" {
		u.Path = "/" + decodedObjectPath
		u.RawPath = "/" + strings.Join(objectPath, "/")
		return &u
	}
	u.Path = basePath + "/" + decodedObjectPath
	u.RawPath = baseRawPath + "/" + strings.Join(objectPath, "/")
	return &u
}

func appendEscapedPathSegment(parts []string, segment string) []string {
	return append(parts, url.PathEscape(segment))
}

func s3StatusError(resp *http.Response, method, requestPath string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if readErr != nil {
		return fmt.Errorf("s3 %s %s failed with status %d: read error body: %w", method, requestPath, resp.StatusCode, readErr)
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("s3 %s %s failed with status %d: %s", method, requestPath, resp.StatusCode, message)
}
