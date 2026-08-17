package inference

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
)

// A settlement that hands the hold back without charging anything must say so.
// The synchronous path always did; the streaming path released in silence,
// which is the shape of the three-day free-serve outage (issue #626), and it
// matters more now that agent traffic streams: a turn that only called tools
// accumulates no content, because AccumulateContent ignores tool-call deltas,
// so a real billable turn can land here and be filed as an upstream error.
//
// The assertion is on the log line rather than on the release call, which
// TestExecuteStreaming_UpstreamClosesWithoutDelivery_ReleasesAsUpstreamError
// already covers. Deleting the line is what this test exists to catch.
func TestExecuteStreaming_DeliveredNothing_SaysSoInTheLog(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := abruptUpstreamCloseServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	done, _ := runExecuteStreaming(orch, context.Background())
	waitDone(t, done)

	if got := logs.String(); !strings.Contains(got, "settle stream delivered nothing") {
		t.Fatalf("nothing was delivered and the hold was released with no log line; log was:\n%s", got)
	}
}
