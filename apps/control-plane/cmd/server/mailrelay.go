package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/mailer"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/metrics"
)

// mailRelayProbeInterval is how often the outbound relay is checked.
//
// Ten minutes, not the five the OWUI shim-key probe uses, because this probe
// costs a third party an authenticated SMTP session rather than a local HTTP
// call. Detection lands inside about twenty minutes, which is two probes and
// what MailRelayUnusable's `for` requires; password recovery is not a
// sub-minute-SLA surface and a false page over one dropped connection is worse
// than ten more minutes of delay.
const mailRelayProbeInterval = 10 * time.Minute

// mailRelayProbeTimeout bounds one probe. A relay that accepts a connection and
// then stops talking must not be able to wedge the loop, which would freeze the
// verdict timestamp and be reported by MailRelayVerdictStale rather than
// silently.
const mailRelayProbeTimeout = 20 * time.Second

// mailRelayProber is the slice of mailer.SMTPSender this loop needs.
type mailRelayProber interface {
	Probe(ctx context.Context) error
}

// startMailRelayWatch wires the relay probe if, and only if, a relay is
// configured.
//
// It returns whether it started one, so a caller can say so at boot: a
// deployment that sends no mail exports no series here, and the difference
// between "not measured on this stack" and "measured and healthy" has to be
// legible somewhere other than a Prometheus query.
func startMailRelayWatch(ctx context.Context, reg *prometheus.Registry) bool {
	cfg := mailer.ConfigFromEnv()
	if !cfg.Configured() {
		return false
	}
	usable, lastVerdict, err := metrics.RegisterMailRelay(reg)
	if err != nil {
		log.Printf("WARN mail relay probe disabled: %v", err)
		return false
	}
	go watchMailRelay(ctx, mailer.NewSMTPSender(cfg), usable, lastVerdict, mailRelayProbeInterval)
	return true
}

// watchMailRelay probes the relay at boot and every interval after, writing
// both series on every verdict and logging only the transitions.
//
// Every probe writes, because a gauge that stops being written is a gauge no
// alert can trust; only transitions log, because a repeated line is noise and a
// healthy deployment should be quiet. There is no transient state here, unlike
// the OWUI shim-key probe: every way this can fail (host unresolvable, relay
// refusing connections, STARTTLS gone, credentials rejected) is a way mail is
// not being delivered, and none of them deserves to be filed as "unknown". A
// single dropped connection is absorbed by the alert's `for` window rather than
// by a third state here.
func watchMailRelay(ctx context.Context, prober mailRelayProber, usable, lastVerdict prometheus.Gauge, interval time.Duration) {
	reported := false
	healthy := false
	for {
		probeCtx, cancel := context.WithTimeout(ctx, mailRelayProbeTimeout)
		err := prober.Probe(probeCtx)
		cancel()

		// A cancelled root context is shutdown, not a relay verdict. Writing 0
		// here would leave a dead process's last scrape looking like an outage.
		if err != nil && ctx.Err() != nil {
			return
		}

		lastVerdict.SetToCurrentTime()
		if err == nil {
			usable.Set(1)
		} else {
			usable.Set(0)
		}

		if !reported || (err == nil) != healthy {
			if err == nil {
				log.Printf("mail: relay accepted a connection, STARTTLS and AUTH; password recovery, invitations and confirmation mail can be delivered")
			} else if errors.Is(err, mailer.ErrNotConfigured) {
				// Unreachable in practice, since startMailRelayWatch checks
				// first, and worth naming rather than reporting as an outage if
				// the configuration is ever emptied at runtime.
				log.Printf("WARN mail: relay is no longer configured; no product email is being sent")
			} else {
				log.Printf("mail: ERROR the outbound relay is not usable: %v. Password recovery is the surface this hides: POST /auth/v1/recover answers 200 {} whatever happens, so users are being told to check an inbox nothing will arrive in. Workspace invitations, signup confirmation and email change are failing with it. Check ENTERPRISE_SMTP_HOST, _PORT, _USER and _PASS, and the relay account's own status", err)
			}
			reported = true
			healthy = err == nil
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
