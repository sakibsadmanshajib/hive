package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/cpauth"
)

// IngestClient calls the control-plane's internal RAG ingest endpoint so the
// document uploaded through /v1/rag/documents gets chunked, embedded, and
// stored (blueprint Step 2.1, #232). Mirrors files.FilestoreClient.
type IngestClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewIngestClient creates an IngestClient pointing at the control-plane base URL.
func NewIngestClient(controlPlaneURL string) *IngestClient {
	return &IngestClient{
		baseURL:    strings.TrimRight(controlPlaneURL, "/"),
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

type ingestRequestBody struct {
	TenantID   string `json:"tenant_id"`
	DocumentID string `json:"document_id"`
	Content    string `json:"content"`
}

// Ingest posts the document content to the control-plane for chunking,
// embedding, and storage. POST /internal/rag/ingest.
func (c *IngestClient) Ingest(ctx context.Context, tenantID, docID uuid.UUID, content string) error {
	body, err := json.Marshal(ingestRequestBody{
		TenantID:   tenantID.String(),
		DocumentID: docID.String(),
		Content:    content,
	})
	if err != nil {
		return fmt.Errorf("rag.ingestclient: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/rag/ingest", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("rag.ingestclient: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	cpauth.SetHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("rag.ingestclient: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("rag.ingestclient: status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// AsIngestFunc adapts Ingest to the fire-and-forget IngestFunc signature the
// Handler expects. Failures are logged by logf (main.go wires log.Printf) --
// the document stays in "pending" status and never surfaces the raw error to
// the customer, matching the provider-blind error contract elsewhere in rag.
func (c *IngestClient) AsIngestFunc(logf func(format string, args ...any)) IngestFunc {
	return func(ctx context.Context, tenantID, docID uuid.UUID, content string) {
		if err := c.Ingest(ctx, tenantID, docID, content); err != nil {
			if logf != nil {
				logf("rag: ingest failed doc=%s: %v", docID, err)
			}
		}
	}
}
