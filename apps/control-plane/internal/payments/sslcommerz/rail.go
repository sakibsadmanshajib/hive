package sslcommerz

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // SSLCommerz IPN verification requires MD5 per spec
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/hivegpt/hive/apps/control-plane/internal/payments"
)

// Rail implements the payments.PaymentRail interface for SSLCommerz v4.
type Rail struct {
	httpClient  *http.Client
	baseURL     string
	storeID     string
	storePasswd string
}

// NewRail constructs an SSLCommerz Rail with the given HTTP client and credentials.
func NewRail(httpClient *http.Client, baseURL, storeID, storePasswd string) *Rail {
	return &Rail{
		httpClient:  httpClient,
		baseURL:     baseURL,
		storeID:     storeID,
		storePasswd: storePasswd,
	}
}

// RailName returns the rail identifier for SSLCommerz.
func (r *Rail) RailName() payments.Rail {
	return payments.RailSSLCommerz
}

// Initiate POSTs form-encoded params to the SSLCommerz v4 initiation API and
// returns the GatewayPageURL for redirect. ProviderIntentID is the sessionkey.
func (r *Rail) Initiate(ctx context.Context, input payments.InitiateInput) (payments.InitiateResult, error) {
	// Format amount: AmountLocal is in paisa (1/100 BDT), present as decimal BDT string.
	// float64 is used here only for display formatting of an integer value, not for arithmetic.
	amountStr := fmt.Sprintf("%.2f", float64(input.AmountLocal)/100.0)

	form := url.Values{}
	form.Set("store_id", r.storeID)
	form.Set("store_passwd", r.storePasswd)
	form.Set("total_amount", amountStr)
	form.Set("currency", "BDT")
	form.Set("tran_id", input.PaymentIntentID.String())
	form.Set("success_url", input.CallbackBaseURL+"/webhooks/sslcommerz/success")
	form.Set("fail_url", input.CallbackBaseURL+"/webhooks/sslcommerz/fail")
	form.Set("cancel_url", input.CallbackBaseURL+"/webhooks/sslcommerz/cancel")
	form.Set("ipn_url", input.CallbackBaseURL+"/webhooks/sslcommerz/ipn")
	form.Set("cus_name", input.CustomerName)
	form.Set("cus_email", input.CustomerEmail)
	form.Set("cus_add1", "N/A")
	form.Set("cus_city", "N/A")
	form.Set("cus_country", "Bangladesh")
	form.Set("cus_phone", "N/A")
	form.Set("shipping_method", "NO")
	form.Set("product_name", "Hive Credits")
	form.Set("product_category", "Digital Service")
	form.Set("product_profile", "digital-goods")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.baseURL+"/gwprocess/v4/api.php",
		strings.NewReader(form.Encode()))
	if err != nil {
		return payments.InitiateResult{}, fmt.Errorf("sslcommerz: create initiate request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return payments.InitiateResult{}, fmt.Errorf("sslcommerz: initiate request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return payments.InitiateResult{}, fmt.Errorf("sslcommerz: initiate response status: %d", resp.StatusCode)
	}

	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return payments.InitiateResult{}, fmt.Errorf("sslcommerz: read initiate response: %w", err)
	}

	var initResult struct {
		Status          string `json:"status"`
		GatewayPageURL  string `json:"GatewayPageURL"`
		Sessionkey      string `json:"sessionkey"`
	}
	if err := json.Unmarshal(rawResp, &initResult); err != nil {
		return payments.InitiateResult{}, fmt.Errorf("sslcommerz: parse initiate response: %w", err)
	}
	if initResult.Status != "SUCCESS" {
		return payments.InitiateResult{}, fmt.Errorf("sslcommerz: initiate status not SUCCESS: %s", initResult.Status)
	}
	if initResult.GatewayPageURL == "" {
		return payments.InitiateResult{}, fmt.Errorf("sslcommerz: empty GatewayPageURL in initiate response")
	}
	if initResult.Sessionkey == "" {
		return payments.InitiateResult{}, fmt.Errorf("sslcommerz: empty sessionkey in initiate response")
	}

	return payments.InitiateResult{
		ProviderIntentID: initResult.Sessionkey, // SSLCommerz sessionkey, NOT tran_id (Hive UUID)
		RedirectURL:      initResult.GatewayPageURL,
		ExpiresAt:        time.Now().Add(20 * time.Minute),
	}, nil
}

// ProcessEvent parses the SSLCommerz IPN POST body, verifies the hash,
// and calls the server-side validation API before returning a normalized RailEvent.
//
// ProviderIntentID in the returned RailEvent is the SSLCommerz sessionkey (provider-assigned),
// which matches the value stored by Initiate — not the tran_id (Hive UUID).
func (r *Rail) ProcessEvent(ctx context.Context, rawBody []byte, _ map[string]string) (payments.RailEvent, error) {
	// Parse form-encoded IPN POST body.
	ipnValues, err := url.ParseQuery(string(rawBody))
	if err != nil {
		return payments.RailEvent{}, fmt.Errorf("sslcommerz: parse IPN body: %w", err)
	}

	valID := ipnValues.Get("val_id")
	sessionkey := ipnValues.Get("sessionkey")
	verifySign := ipnValues.Get("verify_sign")
	verifyKey := ipnValues.Get("verify_key")

	if valID == "" {
		return payments.RailEvent{}, fmt.Errorf("sslcommerz: missing val_id in IPN")
	}
	if sessionkey == "" {
		return payments.RailEvent{}, fmt.Errorf("sslcommerz: missing sessionkey in IPN")
	}

	// Verify hash: fields listed in verify_key (comma-separated), sorted, built into
	// key=value&key=value string, then append &store_passwd=MD5(storePasswd), then MD5 all.
	if verifyKey != "" && verifySign != "" {
		if err := r.verifyHash(ipnValues, verifyKey, verifySign); err != nil {
			return payments.RailEvent{}, fmt.Errorf("sslcommerz: IPN hash verification failed: %w", err)
		}
	}

	// Call server-side validation API.
	validationURL := fmt.Sprintf(
		"%s/validator/api/validationserverAPI.php?val_id=%s&store_id=%s&store_passwd=%s&format=json",
		r.baseURL,
		url.QueryEscape(valID),
		url.QueryEscape(r.storeID),
		url.QueryEscape(r.storePasswd),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, validationURL, nil)
	if err != nil {
		return payments.RailEvent{}, fmt.Errorf("sslcommerz: create validation request: %w", err)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return payments.RailEvent{}, fmt.Errorf("sslcommerz: validation request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return payments.RailEvent{}, fmt.Errorf("sslcommerz: validation response status: %d", resp.StatusCode)
	}

	rawValidation, err := io.ReadAll(resp.Body)
	if err != nil {
		return payments.RailEvent{}, fmt.Errorf("sslcommerz: read validation response: %w", err)
	}

	var validationResult struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rawValidation, &validationResult); err != nil {
		return payments.RailEvent{}, fmt.Errorf("sslcommerz: parse validation response: %w", err)
	}

	var eventType string
	switch validationResult.Status {
	case "VALID", "VALIDATED":
		eventType = "payment.succeeded"
	case "FAILED":
		eventType = "payment.failed"
	case "CANCELLED":
		eventType = "payment.cancelled"
	default:
		return payments.RailEvent{}, fmt.Errorf("sslcommerz: unexpected validation status: %s", validationResult.Status)
	}

	return payments.RailEvent{
		ProviderIntentID: sessionkey, // SSLCommerz sessionkey, NOT tran_id (Hive UUID)
		EventType:        eventType,
		RawPayload:       rawBody,
	}, nil
}

// verifyHash verifies the SSLCommerz IPN hash per the SSLCommerz specification:
// 1. Extract field names from verify_key (comma-separated)
// 2. Sort the field names
// 3. Build "key=value&key=value&..." from those fields using IPN values
// 4. Append "&store_passwd=MD5(storePasswd)"
// 5. MD5 the concatenated string and compare to verify_sign
func (r *Rail) verifyHash(ipnValues url.Values, verifyKey, verifySign string) error {
	fieldNames := strings.Split(verifyKey, ",")
	sort.Strings(fieldNames)

	var parts []string
	for _, field := range fieldNames {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		parts = append(parts, field+"="+ipnValues.Get(field))
	}

	//nolint:gosec // MD5 required by SSLCommerz IPN verification spec
	passwdHash := fmt.Sprintf("%x", md5.Sum([]byte(r.storePasswd)))
	parts = append(parts, "store_passwd="+passwdHash)

	toHash := strings.Join(parts, "&")
	//nolint:gosec // MD5 required by SSLCommerz IPN verification spec
	computed := fmt.Sprintf("%x", md5.Sum([]byte(toHash)))

	if !bytes.Equal([]byte(computed), []byte(verifySign)) {
		return fmt.Errorf("hash mismatch: computed %q != received %q", computed, verifySign)
	}

	return nil
}
