package audio

// Audio charge derivation (#627).
//
// Before this file existed, /v1/audio/speech charged a flat 1000 credits and
// /v1/audio/transcriptions a flat 500, whatever the request size, and the
// model catalog was never consulted. Every charge now comes from the resolved
// route's catalog price times the quantity the endpoint actually meters:
// characters synthesized for speech, seconds of audio transcribed for
// transcription and translation. Neither modality is priced per token
// upstream, so a per-token figure for either would be invented; the alias row
// carries its unit explicitly (model_aliases.price_unit) and a row whose unit
// this endpoint cannot meter is refused rather than silently converted.

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
)

// Price units, mirroring the values allowed by public.model_aliases.price_unit.
const (
	// PriceUnitTokens prices per million tokens. Text modalities only; no
	// audio endpoint can meter it.
	PriceUnitTokens = "tokens"
	// PriceUnitCharacters prices per million characters of synthesized text
	// (/v1/audio/speech).
	PriceUnitCharacters = "characters"
	// PriceUnitSeconds prices per million seconds of transcribed audio
	// (/v1/audio/transcriptions, /v1/audio/translations).
	PriceUnitSeconds = "seconds"
)

// minBillableSeconds is the shortest transcription the upstream itself bills,
// so it is also the shortest we can charge without serving below cost: Groq
// bills "a minimum of 10s per request" (https://groq.com/pricing, ASR table
// footnote, fetched 2026-08-01). A one second clip costs us ten seconds.
const minBillableSeconds = 10

// sttEstimatedSeconds is the audio length assumed for the pre-dispatch hold on
// a transcription. A duration is not knowable before the upstream reports one,
// and the hold is an authorization floor rather than a cap: a settlement that
// carries TerminalUsageConfirmed bills the real figure in full even past the
// hold (apps/control-plane/internal/accounting/service.go finalizeLocked), so
// under-holding never under-charges.
//
// ponytail: one flat minute, matching the flat-hold convention the chat and
// embeddings paths already use. A size-derived estimate would need an assumed
// bitrate, which is exactly the kind of invented number this fix exists to
// remove.
const sttEstimatedSeconds = 60

// creditsForQuantity converts a metered quantity into whole credits at the
// route's catalog price, flooring any nonzero quantity at one credit so a
// request that consumed real work is never free. The arithmetic itself is
// metering.ChargeCredits: per million units, math/big, round half up.
func creditsForQuantity(quantity int64, route RouteResult) int64 {
	credits := metering.ChargeCredits(metering.UnitCharge{
		Quantity:          quantity,
		CreditsPerMillion: route.UnitPriceCredits,
	})
	if quantity > 0 && credits < 1 {
		credits = 1
	}
	return credits
}

// canPrice reports whether the route's catalog price can be applied to the
// unit this endpoint meters. A mismatch is a data problem, not a client
// problem, and it fails closed: the alternative is inventing a conversion or
// serving the request free.
func canPrice(route RouteResult, meteredUnit string) bool {
	return route.PriceUnit == meteredUnit && route.UnitPriceCredits > 0
}

// requestedResponseFormat normalizes the caller's response_format.
func requestedResponseFormat(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// isSubtitleFormat reports the two response formats that are not JSON and
// cannot be reconstructed from verbose_json. They carry their own timestamps,
// so they are forwarded verbatim and priced off the last cue instead.
func isSubtitleFormat(format string) bool {
	return format == "srt" || format == "vtt"
}

// upstreamResponseFormat is the response_format this handler asks the upstream
// for, which is not always what the caller asked for. The default (json)
// reports text only, with no duration, so a transcription served that way
// could not be priced at all; verbose_json reports the duration and every
// field json carries, and reshapeTranscription reduces it back to the caller's
// requested shape before it is returned.
func upstreamResponseFormat(requested string) string {
	if isSubtitleFormat(requested) {
		return requested
	}
	return "verbose_json"
}

// billableSeconds returns the whole seconds of audio a successful
// transcription response accounts for, rounded up, and whether a duration
// could be established at all. ok=false means the response reported nothing
// this endpoint can meter: the request is unpriceable and must be refused,
// never charged at a guess and never served free (D-034).
//
// Two sources are read, in this order (#680):
//
//  1. the top-level duration, which is what the provider reports when it
//     reports one at all, and
//  2. otherwise the largest segments[].end, which verbose_json carries per
//     segment whether or not the top-level field is present.
//
// The order matters for the money: a body that carries a top-level duration is
// priced from it alone and is charged exactly what it was charged before this
// fallback existed. The fallback is never a maximum taken across both sources.
// A segment end is a timestamp inside the submitted audio, so the largest of
// them is bounded above by the real audio length and deriving from it cannot
// charge for more audio than the caller sent. It is also the derivation
// lastCueSeconds already applies to subtitle bodies.
//
// Rounding up to the second, and the float64 that JSON forces on the reported
// duration, both stop here: everything downstream of this function is integer
// seconds and math/big.
func billableSeconds(body []byte, requestedFormat string) (int64, bool) {
	if isSubtitleFormat(requestedFormat) {
		// Subtitle cues are timestamped, so the last cue's end time IS the
		// duration. A body with no cues at all (silence, no speech detected)
		// yields zero, which the minimum below still charges for.
		return lastCueSeconds(body), true
	}

	var parsed struct {
		Duration *float64 `json:"duration"`
		Segments []struct {
			End *float64 `json:"end"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, false
	}
	if parsed.Duration != nil {
		return ceilSeconds(*parsed.Duration), true
	}

	var longest float64
	for _, segment := range parsed.Segments {
		// A missing, null or non-positive end reports no audio, so it is
		// skipped rather than treated as a zero-second transcription: zero
		// would still be charged at the ten second provider minimum, which is
		// a real charge for audio nothing in the body accounts for.
		if segment.End == nil || *segment.End <= 0 {
			continue
		}
		if *segment.End > longest {
			longest = *segment.End
		}
	}
	if longest <= 0 {
		return 0, false
	}
	return ceilSeconds(longest), true
}

// ceilSeconds rounds a reported float64 duration up to whole seconds, flooring
// at zero. A negative figure is nonsense from the upstream rather than a
// negative charge, so it collapses to zero and is then billed at the provider
// minimum, which is what a reported negative duration did before #680 too.
//
// A figure too large for an int64 collapses to zero for the same reason, and
// explicitly rather than incidentally: Go leaves a float64 to int64 conversion
// implementation-dependent when the value does not fit, so the same nonsense
// duration that yields the minimum charge on amd64 could saturate to
// math.MaxInt64 elsewhere and bill an astronomical figure for a short clip.
// The guard is a no-op on the amd64 the services are built for and makes the
// outcome the same on every architecture.
func ceilSeconds(reported float64) int64 {
	if reported <= 0 || reported >= math.MaxInt64 {
		return 0
	}
	seconds := int64(reported)
	if float64(seconds) < reported {
		seconds++
	}
	return seconds
}

// billedSeconds applies the upstream's own minimum billable duration.
func billedSeconds(seconds int64) int64 {
	if seconds < minBillableSeconds {
		return minBillableSeconds
	}
	return seconds
}

// cueTimestampPattern matches one SRT (00:00:12,500) or WebVTT (00:00:12.500)
// timestamp. Milliseconds are optional in WebVTT.
var cueTimestampPattern = regexp.MustCompile(`(\d{2,}):([0-5]\d):([0-5]\d)(?:[.,](\d{1,3}))?`)

// lastCueSeconds returns the largest timestamp in a subtitle body, rounded up
// to the second. Taking the maximum over every timestamp rather than parsing
// cue structure keeps this to one pass and tolerates either dialect's header,
// numbering, and line endings.
func lastCueSeconds(body []byte) int64 {
	var latest int64
	for _, match := range cueTimestampPattern.FindAllSubmatch(body, -1) {
		hours, _ := strconv.ParseInt(string(match[1]), 10, 64)
		minutes, _ := strconv.ParseInt(string(match[2]), 10, 64)
		seconds, _ := strconv.ParseInt(string(match[3]), 10, 64)
		total := hours*3600 + minutes*60 + seconds
		if len(match[4]) > 0 && strings.Trim(string(match[4]), "0") != "" {
			total++ // round the fractional part up, same as a reported duration
		}
		if total > latest {
			latest = total
		}
	}
	return latest
}

// reshapeTranscription converts the upstream body into the shape the caller
// asked for, returning the Content-Type to answer with. The handler always
// requests verbose_json (see upstreamResponseFormat) so it can read a
// duration, so a caller that asked for the default json shape must not be
// handed the verbose one instead.
//
// An unparseable body is passed through untouched rather than replaced with an
// error: by the time this runs the upstream has already returned 2xx and the
// request has been priced, so the caller is better served by the bytes the
// upstream produced than by a synthesized failure.
func reshapeTranscription(body []byte, requestedFormat string) (contentType string, out []byte) {
	if isSubtitleFormat(requestedFormat) {
		return "text/plain; charset=utf-8", body
	}

	if requestedFormat == "verbose_json" {
		return "application/json", body
	}

	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "application/json", body
	}
	if requestedFormat == "text" {
		return "text/plain; charset=utf-8", []byte(parsed.Text)
	}

	reduced, err := json.Marshal(parsed)
	if err != nil {
		return "application/json", body
	}
	return "application/json", reduced
}
