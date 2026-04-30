package budgets

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// Phase 14 — Spend alert notifiers.
//
// Implementations of AlertNotifier (workspace surface) and EmailNotifier
// (legacy threshold surface). The CompositeNotifier dispatches to email and
// webhook channels in parallel when both are configured.
// =============================================================================

// LogNotifier is a no-op EmailNotifier that logs budget alerts instead of sending email.
// It is used in development and as a fallback until a real email sender is configured.
type LogNotifier struct {
	logger *slog.Logger
}

// NewLogNotifier creates a LogNotifier using the provided logger.
func NewLogNotifier(logger *slog.Logger) *LogNotifier {
	return &LogNotifier{logger: logger}
}

// SendBudgetAlert logs the budget alert to the configured logger.
func (n *LogNotifier) SendBudgetAlert(ctx context.Context, accountID uuid.UUID, threshold BudgetThreshold, currentBalance int64) error {
	n.logger.WarnContext(ctx, "BUDGET ALERT: balance below threshold (email not configured)",
		"account_id", accountID,
		"threshold_credits", threshold.ThresholdCredits,
		"current_balance", currentBalance,
	)
	return nil
}

// =============================================================================
// AlertNotifier — workspace spend-alert dispatch
// =============================================================================

// LogAlertNotifier logs spend alerts; default dev fallback when SMTP / webhook
// are not configured.
type LogAlertNotifier struct {
	logger *slog.Logger
}

// NewLogAlertNotifier returns a LogAlertNotifier.
func NewLogAlertNotifier(logger *slog.Logger) *LogAlertNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogAlertNotifier{logger: logger}
}

// NotifySpendAlert emits a structured log line for a spend alert crossing.
func (n *LogAlertNotifier) NotifySpendAlert(ctx context.Context, alert SpendAlert, workspaceID uuid.UUID, mtd, softCap *big.Int) error {
	n.logger.WarnContext(ctx, "SPEND ALERT: workspace MTD spend crossed threshold",
		"workspace_id", workspaceID,
		"alert_id", alert.ID,
		"threshold_pct", alert.ThresholdPct,
		"mtd_bdt_subunits", mtd.String(),
		"soft_cap_bdt_subunits", softCap.String(),
	)
	return nil
}

// CompositeNotifier dispatches to email (when alert.Email != nil) and webhook
// (when alert.WebhookURL != nil + alert.WebhookSecret != nil for HMAC).
type CompositeNotifier struct {
	httpClient    *http.Client
	logger        *slog.Logger
	emailFallback AlertNotifier // log-style fallback when email not implemented yet
}

// NewCompositeNotifier returns a CompositeNotifier. `httpClient` is used for
// webhook delivery (with retry + HMAC); pass nil to use a default client.
func NewCompositeNotifier(httpClient *http.Client, logger *slog.Logger) *CompositeNotifier {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &CompositeNotifier{
		httpClient:    httpClient,
		logger:        logger,
		emailFallback: NewLogAlertNotifier(logger),
	}
}

// NotifySpendAlert dispatches to all configured channels.
func (n *CompositeNotifier) NotifySpendAlert(ctx context.Context, alert SpendAlert, workspaceID uuid.UUID, mtd, softCap *big.Int) error {
	// Email channel — Phase 14 ships log-fallback. Phase 17 will wire SMTP.
	if alert.Email != nil && *alert.Email != "" {
		if err := n.emailFallback.NotifySpendAlert(ctx, alert, workspaceID, mtd, softCap); err != nil {
			n.logger.WarnContext(ctx, "email channel failed", "alert_id", alert.ID, "error", err)
		}
	}

	// Webhook channel — HMAC-SHA256 over JSON body, 3 retries with backoff.
	if alert.WebhookURL != nil && *alert.WebhookURL != "" {
		if err := n.dispatchWebhook(ctx, alert, workspaceID, mtd, softCap); err != nil {
			n.logger.WarnContext(ctx, "webhook channel failed",
				"alert_id", alert.ID, "error", err)
			// Webhook failure does NOT block the email channel. Treat as non-fatal:
			// the cron will not stamp last_fired_period if BOTH channels fail.
			return err
		}
	}

	return nil
}

// webhookPayload is the JSON body POSTed to subscriber webhooks. Provider-blind:
// no provider names, no USD strings, no FX language. BDT subunits only.
type webhookPayload struct {
	Event             string `json:"event"`
	WorkspaceID       string `json:"workspace_id"`
	AlertID           string `json:"alert_id"`
	ThresholdPct      int    `json:"threshold_pct"`
	MTDBDTSubunits    string `json:"mtd_bdt_subunits"`
	SoftCapBDTSubunit string `json:"soft_cap_bdt_subunits"`
	Currency          string `json:"currency"`
	FiredAt           string `json:"fired_at"`
}

func (n *CompositeNotifier) dispatchWebhook(ctx context.Context, alert SpendAlert, workspaceID uuid.UUID, mtd, softCap *big.Int) error {
	payload := webhookPayload{
		Event:             "spend_alert.threshold_crossed",
		WorkspaceID:       workspaceID.String(),
		AlertID:           alert.ID.String(),
		ThresholdPct:      alert.ThresholdPct,
		MTDBDTSubunits:    mtd.String(),
		SoftCapBDTSubunit: softCap.String(),
		Currency:          "BDT",
		FiredAt:           time.Now().UTC().Format(time.RFC3339Nano),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook body: %w", err)
	}

	signature := ""
	if alert.WebhookSecret != nil && *alert.WebhookSecret != "" {
		signature = signWebhook(body, *alert.WebhookSecret)
	}

	// Retry with exponential backoff: 200ms, 400ms, 800ms.
	backoffs := []time.Duration{200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}
	var lastErr error
	for attempt := 0; attempt < len(backoffs)+1; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(backoffs[attempt-1]):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, *alert.WebhookURL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build webhook request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "hive-spend-alerts/1.0")
		if signature != "" {
			req.Header.Set("X-Hive-Signature", signature)
		}

		resp, err := n.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("webhook attempt %d: %w", attempt+1, err)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("webhook attempt %d: status %d", attempt+1, resp.StatusCode)
	}
	return lastErr
}

// signWebhook returns the HMAC-SHA256 hex digest of body using secret. The
// receiving server should recompute the digest over the raw body bytes and
// constant-time compare to the X-Hive-Signature header.
func signWebhook(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
