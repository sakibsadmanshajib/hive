package metering

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// recordingLogger is a hand-rolled VerdictLogger fake used to assert both
// call count and the shape of the last record written, without a database.
type recordingLogger struct {
	calls    int
	last     VerdictRecord
	writeErr error
}

func (r *recordingLogger) LogVerdict(ctx context.Context, rec VerdictRecord) error {
	r.calls++
	r.last = rec
	return r.writeErr
}

// TestExecute_AlwaysDispatchesExactlyOnceRegardlessOfVerdict is the
// non-enforcement property under test (task brief, section 5): shadow mode
// must call dispatch exactly once and never itself surface an error, no
// matter which precedence rule fired or whether the billing account
// resolved.
func TestExecute_AlwaysDispatchesExactlyOnceRegardlessOfVerdict(t *testing.T) {
	tests := []struct {
		name     string
		settings fakeSettings
		billing  fakeBilling
		req      Request
	}{
		{
			name:     "not billable, no cost basis",
			settings: fakeSettings{ok: false},
			req: Request{
				Principal: Principal{TenantID: uuid.New()},
				Route:     RouteInfo{},
			},
		},
		{
			name:     "not billable, tenant setting explicitly disabled",
			settings: fakeSettings{enabled: false, ok: true},
			req: Request{
				Principal:  Principal{TenantID: uuid.New()},
				Deployment: DeploymentHiveCloud,
				Route:      RouteInfo{InputPriceCredits: 1, OutputPriceCredits: 1},
			},
		},
		{
			name:     "billable but billing account unresolved",
			settings: fakeSettings{ok: false},
			billing:  fakeBilling{found: false},
			req: Request{
				Principal:  Principal{TenantID: uuid.New()},
				Deployment: DeploymentHiveCloud,
				Route:      RouteInfo{InputPriceCredits: 1, OutputPriceCredits: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			logger := &recordingLogger{}
			g := New(Deps{Settings: tt.settings, Billing: tt.billing, Log: logger})

			dispatch := func(ctx context.Context) (DispatchResult, error) {
				calls++
				return DispatchResult{PromptTokens: 10, CompletionTokens: 5, Confirmed: true}, nil
			}

			_, err := g.Execute(context.Background(), tt.req, dispatch)
			if calls != 1 {
				t.Fatalf("dispatch called %d times, want exactly 1", calls)
			}
			if err != nil {
				t.Fatalf("Execute returned error %v; shadow mode must never surface an error from its own resolution", err)
			}
			if logger.calls != 1 {
				t.Fatalf("verdict logger called %d times, want 1", logger.calls)
			}
		})
	}
}

// TestExecute_NeverAltersCustomerVisibleOutcome is the explicit
// non-enforcement proof requested by the task brief: whatever dispatch
// returns is exactly what Execute hands back, independent of what verdict
// the gate resolved (here forced to a billing_not_configured case). Step 4,
// not this step, is allowed to change this.
func TestExecute_NeverAltersCustomerVisibleOutcome(t *testing.T) {
	wantErr := errors.New("upstream 503")
	g := New(Deps{
		Settings: fakeSettings{ok: false},
		Billing:  fakeBilling{found: false}, // forces billing_not_configured
		Log:      &recordingLogger{},
	})

	dispatch := func(ctx context.Context) (DispatchResult, error) {
		return DispatchResult{PromptTokens: 3, CompletionTokens: 2}, wantErr
	}

	_, gotErr := g.Execute(context.Background(), Request{
		Principal:  Principal{TenantID: uuid.New()},
		Deployment: DeploymentHiveCloud,
		Route:      RouteInfo{InputPriceCredits: 1, OutputPriceCredits: 1},
	}, dispatch)

	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("Execute error = %v, want exactly the dispatch error %v, unmodified", gotErr, wantErr)
	}
}

// TestExecute_VerdictLogWriteFailureIsSwallowed proves a verdict-log write
// failure is logged, not propagated -- matching the InsertTrace /
// insertAuditEvent convention this package deliberately mirrors
// (chat/trace.go, chat/audit.go).
func TestExecute_VerdictLogWriteFailureIsSwallowed(t *testing.T) {
	logger := &recordingLogger{writeErr: errors.New("insert failed")}
	g := New(Deps{
		Settings: fakeSettings{ok: false},
		Billing:  fakeBilling{found: true, accountID: uuid.New()},
		Log:      logger,
	})

	_, err := g.Execute(context.Background(), Request{
		Principal:  Principal{TenantID: uuid.New()},
		Deployment: DeploymentHiveCloud,
		Route:      RouteInfo{InputPriceCredits: 1, OutputPriceCredits: 1},
	}, func(ctx context.Context) (DispatchResult, error) {
		return DispatchResult{PromptTokens: 1, CompletionTokens: 1}, nil
	})

	if err != nil {
		t.Fatalf("Execute returned error %v on a verdict-log write failure, want nil (logged, not propagated)", err)
	}
	if logger.calls != 1 {
		t.Fatalf("logger called %d times, want 1", logger.calls)
	}
}

// TestExecute_VerdictRecordShape asserts the full VerdictRecord shape Gate
// hands to VerdictLogger, including the disconnect/delivered-tokens fields
// and both credit figures logged side by side per design brief section 3.5.
func TestExecute_VerdictRecordShape(t *testing.T) {
	logger := &recordingLogger{}
	tenantID := uuid.New()
	accountID := uuid.New()
	g := New(Deps{
		Settings: fakeSettings{ok: false},
		Billing:  fakeBilling{found: true, accountID: accountID},
		Log:      logger,
	})

	outcome, err := g.Execute(context.Background(), Request{
		RequestID:  "req-123",
		Principal:  Principal{TenantID: tenantID},
		Deployment: DeploymentHiveCloud,
		Endpoint:   "/v1/chat/completions",
		AliasID:    "hive-fast",
		Route:      RouteInfo{InputPriceCredits: 8, OutputPriceCredits: 24},
	}, func(ctx context.Context) (DispatchResult, error) {
		return DispatchResult{
			PromptTokens:     100,
			CompletionTokens: 50,
			Confirmed:        true,
			Disconnected:     true,
			Delivered:        40,
		}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec := logger.last
	if rec.RequestID != "req-123" {
		t.Errorf("RequestID = %q, want req-123", rec.RequestID)
	}
	if rec.TenantID != tenantID {
		t.Errorf("TenantID = %v, want %v", rec.TenantID, tenantID)
	}
	if rec.AccountID != accountID {
		t.Errorf("AccountID = %v, want %v", rec.AccountID, accountID)
	}
	if rec.PrincipalType != "session" {
		t.Errorf("PrincipalType = %q, want session", rec.PrincipalType)
	}
	if rec.Endpoint != "/v1/chat/completions" {
		t.Errorf("Endpoint = %q", rec.Endpoint)
	}
	if rec.ModelAlias != "hive-fast" {
		t.Errorf("ModelAlias = %q", rec.ModelAlias)
	}
	if rec.PromptTokens != 100 || rec.CompletionTokens != 50 {
		t.Errorf("tokens = %d/%d, want 100/50", rec.PromptTokens, rec.CompletionTokens)
	}
	if !rec.TerminalUsageConfirmed {
		t.Errorf("TerminalUsageConfirmed = false, want true")
	}
	if !rec.Disconnected {
		t.Errorf("Disconnected = false, want true")
	}
	if rec.DeliveredTokens == nil || *rec.DeliveredTokens != 40 {
		t.Errorf("DeliveredTokens = %v, want pointer to 40", rec.DeliveredTokens)
	}
	if rec.EstimatedCreditsLegacy != 150 {
		t.Errorf("EstimatedCreditsLegacy = %d, want 150", rec.EstimatedCreditsLegacy)
	}
	// (100*8 + 50*24) / 1e6 = 2000/1e6 -> rounds to 0, floored to 1 because
	// the verdict is billable and tokens were produced.
	if rec.EstimatedCreditsPerModel != 1 {
		t.Errorf("EstimatedCreditsPerModel = %d, want 1 (floored)", rec.EstimatedCreditsPerModel)
	}
	if outcome.Verdict != VerdictBillable {
		t.Errorf("Verdict = %q, want billable", outcome.Verdict)
	}
	if outcome.EstimatedCreditsPerModel != rec.EstimatedCreditsPerModel {
		t.Errorf("Outcome and VerdictRecord disagree on EstimatedCreditsPerModel: %d vs %d",
			outcome.EstimatedCreditsPerModel, rec.EstimatedCreditsPerModel)
	}
}

// TestExecute_DeliveredTokensNilWhenNotDisconnected proves the "null unless
// disconnected" migration contract (metering_shadow_verdicts.delivered_tokens):
// a normal, fully-delivered request must not fabricate a delivered-tokens
// figure.
func TestExecute_DeliveredTokensNilWhenNotDisconnected(t *testing.T) {
	logger := &recordingLogger{}
	g := New(Deps{
		Settings: fakeSettings{ok: false},
		Billing:  fakeBilling{found: true, accountID: uuid.New()},
		Log:      logger,
	})

	_, err := g.Execute(context.Background(), Request{
		Principal:  Principal{TenantID: uuid.New()},
		Deployment: DeploymentHiveCloud,
		Route:      RouteInfo{InputPriceCredits: 1, OutputPriceCredits: 1},
	}, func(ctx context.Context) (DispatchResult, error) {
		return DispatchResult{PromptTokens: 1, CompletionTokens: 1, Confirmed: true}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logger.last.DeliveredTokens != nil {
		t.Errorf("DeliveredTokens = %v, want nil when not disconnected", logger.last.DeliveredTokens)
	}
}
