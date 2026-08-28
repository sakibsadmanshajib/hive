package usage

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The console privacy page (/console/privacy) tells customers that the
// per-request metering record carries token counts, cost, status, model
// alias, endpoint, error codes and operational metadata, and that message
// content is stripped from that metadata before the record is written.
//
// UsageEventRow in the web console cannot guard that sentence: it is a
// read projection that already omits three real columns of this table
// (provider_request_id, internal_metadata, customer_tags), so pinning its
// key set would pass while the table drifted. The sentence is decided by
// the column list this insert writes, which is what this test pins.
var allowedUsageEventInsertColumns = map[string]bool{
	"account_id":          true,
	"request_attempt_id":  true,
	"api_key_id":          true,
	"request_id":          true,
	"event_type":          true,
	"endpoint":            true,
	"model_alias":         true,
	"status":              true,
	"input_tokens":        true,
	"output_tokens":       true,
	"cache_read_tokens":   true,
	"cache_write_tokens":  true,
	"hive_credit_delta":   true,
	"provider_request_id": true,
	"internal_metadata":   true,
	"customer_tags":       true,
	"error_code":          true,
	"error_type":          true,
}

func TestUsageEventInsertWritesOnlyKnownColumns(t *testing.T) {
	source, err := os.ReadFile("repository.go")
	if err != nil {
		t.Fatalf("read repository.go: %v", err)
	}

	pattern := regexp.MustCompile(`(?s)INSERT INTO public\.usage_events\s*\(([^)]*)\)`)
	match := pattern.FindSubmatch(source)
	if match == nil {
		t.Fatal("could not find the usage_events INSERT column list in repository.go")
	}

	for _, raw := range strings.Split(string(match[1]), ",") {
		column := strings.TrimSpace(raw)
		if column == "" {
			continue
		}
		if !allowedUsageEventInsertColumns[column] {
			t.Errorf("usage_events insert writes an unrecognised column %q; /console/privacy tells customers this record carries no message content, so add the column here only after confirming that is still true, and correct the page if it is not", column)
		}
	}
}
