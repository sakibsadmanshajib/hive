package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// ScanLines streams reader line-by-line, sending one ScanResult per non-empty
// line into ch. The scanner buffer is raised to ScannerBufferMaxBytes so
// OpenAI's ~1MB per-line limit fits with headroom; lines exceeding that
// produce an io error which is yielded as ScanResult{Err: ...} once and the
// scan stops. Malformed JSON lines are yielded as ScanResult{Err: errInvalidJSON,
// RawLine: ...} and the scan continues. The function closes ch when done.
func ScanLines(ctx context.Context, reader io.Reader, ch chan<- ScanResult) {
	defer close(ch)
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, ScannerBufferMaxBytes)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		raw := scanner.Bytes()
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		// Copy raw because bufio.Scanner reuses its internal buffer.
		dup := make([]byte, len(raw))
		copy(dup, raw)
		var line InputLine
		if err := json.Unmarshal(dup, &line); err != nil {
			select {
			case <-ctx.Done():
				return
			case ch <- ScanResult{Err: fmt.Errorf("%w: %v", errInvalidJSON, err), RawLine: dup}:
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case ch <- ScanResult{Line: &line}:
		}
	}
	if err := scanner.Err(); err != nil {
		select {
		case <-ctx.Done():
			return
		case ch <- ScanResult{Err: fmt.Errorf("scan: %w", err)}:
		}
	}
}

// JSONLWriter accumulates one JSON object per line in a thread-safe buffer.
// Append is mutex-guarded so multiple dispatcher goroutines can write
// concurrently. Finalize uploads the buffer to object storage.
type JSONLWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	count int
}

// Append marshals v as a JSON line ending with "\n". Returns the ordinal index
// (0-based) of the appended line for caller bookkeeping.
func (w *JSONLWriter) Append(v any) (int, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return -1, fmt.Errorf("marshal jsonl line: %w", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	idx := w.count
	w.buf.Write(encoded)
	w.buf.WriteByte('\n')
	w.count++
	return idx, nil
}

// Count returns the number of lines appended so far. Safe for concurrent use.
func (w *JSONLWriter) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}

// Bytes returns a defensive copy of the accumulated content. Mostly used in tests.
func (w *JSONLWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	dup := make([]byte, w.buf.Len())
	copy(dup, w.buf.Bytes())
	return dup
}

// StoragePort is the minimal upload contract the executor depends on; concrete
// implementation is the existing packages/storage S3 client.
type StoragePort interface {
	Upload(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error
	Download(ctx context.Context, bucket, key string) (io.ReadCloser, error)
}

// Finalize uploads the accumulated buffer to storage. If skipEmpty is true
// and the writer is empty, no upload is performed and Finalize returns
// (false, 0, nil). Otherwise it uploads and returns (true, size, nil) where
// size is the number of bytes uploaded.
func (w *JSONLWriter) Finalize(ctx context.Context, storage StoragePort, bucket, key string, skipEmpty bool) (bool, int64, error) {
	w.mu.Lock()
	size := w.buf.Len()
	body := make([]byte, size)
	copy(body, w.buf.Bytes())
	w.mu.Unlock()
	if size == 0 && skipEmpty {
		return false, 0, nil
	}
	if err := storage.Upload(ctx, bucket, key, bytes.NewReader(body), int64(size), "application/jsonl"); err != nil {
		return false, 0, fmt.Errorf("upload %s: %w", key, err)
	}
	return true, int64(size), nil
}
