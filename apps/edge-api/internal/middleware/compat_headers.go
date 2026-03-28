package middleware

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// CompatHeaders returns middleware that adds OpenAI compatibility headers
// to every response: x-request-id, openai-version, and openai-processing-ms.
func CompatHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Generate UUID v4 for request ID
			requestID := generateUUIDv4()

			// Set headers before calling next so they appear even on error responses
			w.Header().Set("x-request-id", requestID)
			w.Header().Set("openai-version", "2020-10-01")

			// Wrap the writer to intercept WriteHeader and inject processing-ms
			rw := &responseRecorder{
				ResponseWriter: w,
				start:          start,
				headerWritten:  false,
			}

			next.ServeHTTP(rw, r)

			// If the handler never called WriteHeader, set processing-ms now
			if !rw.headerWritten {
				elapsed := time.Since(start).Milliseconds()
				w.Header().Set("openai-processing-ms", strconv.FormatInt(elapsed, 10))
			}
		})
	}
}

// responseRecorder wraps http.ResponseWriter to inject openai-processing-ms
// before the status code is written.
type responseRecorder struct {
	http.ResponseWriter
	start         time.Time
	headerWritten bool
}

func (rw *responseRecorder) WriteHeader(code int) {
	if !rw.headerWritten {
		elapsed := time.Since(rw.start).Milliseconds()
		rw.ResponseWriter.Header().Set("openai-processing-ms", strconv.FormatInt(elapsed, 10))
		rw.headerWritten = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseRecorder) Write(b []byte) (int, error) {
	if !rw.headerWritten {
		elapsed := time.Since(rw.start).Milliseconds()
		rw.ResponseWriter.Header().Set("openai-processing-ms", strconv.FormatInt(elapsed, 10))
		rw.headerWritten = true
	}
	return rw.ResponseWriter.Write(b)
}

// generateUUIDv4 generates a random UUID v4 string using crypto/rand.
func generateUUIDv4() string {
	var uuid [16]byte
	_, err := rand.Read(uuid[:])
	if err != nil {
		// Fallback to a fixed ID if crypto/rand fails (extremely unlikely)
		return "req-fallback-00000000"
	}
	// Set version 4
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	// Set variant bits
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return fmt.Sprintf("req-%x-%x-%x-%x-%x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}
