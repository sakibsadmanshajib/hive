package metrics

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

// MailRelayUsable is 1 when the outbound mail relay accepted a connection,
// negotiated STARTTLS and authenticated on the last probe, and 0 when it did
// not.
//
// It is the alarmable half of the trade deploy/docker/Caddyfile.supabase makes
// on POST /auth/v1/recover. That route synthesizes an identical 200 {} for a
// 200, a 429 and a 5xx from GoTrue, so that a caller cannot learn which
// addresses hold accounts by reading the status code. The cost is that a broken
// mail relay tells every user "check your email" and sends nothing, and the
// response carries no trace of it. GoTrue logs the refusal, but nothing reads a
// container log on a schedule, so without this series a recovery outage reaches
// an operator only as a support ticket.
//
// The relay is shared, which is what makes one series enough:
// docker-compose.yml sets control-plane's HIVE_SMTP_HOST from
// ENTERPRISE_SMTP_HOST, the same variable that feeds GOTRUE_SMTP_HOST in
// docker-compose.enterprise.yml and Alertmanager's own smarthost. Password
// recovery, workspace invitations, signup confirmation and email change all
// ride it and all fail together.
var mailRelayUsable = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "hive_mail_relay_usable",
	Help: "1 when the shared outbound SMTP relay accepted a connection, STARTTLS and AUTH on the last probe, 0 when it did not. At 0, password recovery is answering 200 {} and sending nothing (the /auth/v1/recover gateway route cannot report the failure without leaking which addresses hold accounts), and workspace invitations, signup confirmation and email change are failing with it.",
})

// mailRelayLastVerdict is what keeps the gauge above honest. That gauge holds
// its last value and starts at 1, so "the relay is fine" and "nothing has ever
// checked" read identically: a probe goroutine that never starts, or one that
// wedges, leaves a healthy-looking 1 forever with nothing measured. This series
// carries the unix time of the last probe that actually reached a verdict, and
// MailRelayVerdictStale in deploy/prometheus/alerts.yml alerts on its age. It is
// 0 until the first verdict lands, which reads as an unbounded age and so fires
// that rule rather than passing it.
var mailRelayLastVerdict = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "hive_mail_relay_last_verdict_seconds",
	Help: "Unix time of the last mail-relay probe that reached a verdict. An age beyond a few probe intervals means hive_mail_relay_usable is holding a value nothing has confirmed. 0 until the first verdict.",
})

// RegisterMailRelay exports the two series above, and returns them so the
// prober can write them.
//
// Registration is the caller's decision and is meant to be conditional: a
// deployment with no relay configured sends no mail at all, and a gauge sitting
// at 0 there would page an operator over a stack that has nothing to break.
// Exporting no series lets absent() tell "not measured here" apart from
// "measured and failing", which is the same reasoning as
// registerOWUIShimKeyMetric in apps/edge-api/cmd/server/main.go.
//
// The initial value is 1 rather than 0 for the mirror-image reason: the first
// probe has not run yet at registration, and starting at 0 would fire the alert
// during every boot. The cost of that choice is that "usable" and "never
// measured" read the same, which is exactly what the verdict timestamp and its
// staleness rule exist to close.
func RegisterMailRelay(reg *prometheus.Registry) (usable, lastVerdict prometheus.Gauge, err error) {
	if reg == nil {
		return nil, nil, errors.New("metrics: mail relay collectors need a registry")
	}
	mailRelayUsable.Set(1)
	// Deliberately NOT set to the current time: 0 means "no verdict yet", which
	// is what the staleness rule must see if the first probe never lands.
	mailRelayLastVerdict.Set(0)
	if err := reg.Register(mailRelayUsable); err != nil {
		return nil, nil, err
	}
	if err := reg.Register(mailRelayLastVerdict); err != nil {
		return nil, nil, err
	}
	return mailRelayUsable, mailRelayLastVerdict, nil
}
