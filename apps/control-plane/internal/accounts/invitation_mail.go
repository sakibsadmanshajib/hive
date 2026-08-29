package accounts

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/mailer"
)

// Delivery outcomes for an invitation. The value travels to the console and is
// the only thing allowed to decide what the interface tells the user, so that no
// surface can claim an email was sent unless a transport returned success
// (issue #1440).
const (
	// DeliverySent means a relay accepted the message.
	DeliverySent = "sent"
	// DeliveryNotConfigured means this deployment has no mail transport. The
	// invitation exists and its link works; nobody mailed it.
	DeliveryNotConfigured = "not_configured"
	// DeliveryFailed means a transport ran and the message was refused. The
	// invitation still exists and its link still works.
	DeliveryFailed = "failed"
)

// InvitationEmail is everything the invitation message needs.
//
// Token is the raw acceptance token. It is bearer equivalent: it belongs in the
// message body and nowhere else, never in a log line, a redirect, or an error.
type InvitationEmail struct {
	To            string
	WorkspaceName string
	InviterEmail  string
	Role          string
	Token         string
	ExpiresAt     time.Time
}

// InvitationMailer delivers an invitation. A Service without one reports
// DeliveryNotConfigured rather than pretending.
type InvitationMailer interface {
	SendInvitation(ctx context.Context, inv InvitationEmail) error
}

// invitationMailer renders the invitation and hands it to a transport.
type invitationMailer struct {
	sender         mailer.Sender
	consoleBaseURL string
}

// NewInvitationMailer returns an InvitationMailer that renders the invitation
// and sends it through sender. consoleBaseURL is the public console origin the
// acceptance link is built on, and it is server configuration on purpose: a
// link origin taken from a request would let anyone who can invite point an
// invitation email at a host of their choosing.
func NewInvitationMailer(sender mailer.Sender, consoleBaseURL string) InvitationMailer {
	return &invitationMailer{
		sender:         sender,
		consoleBaseURL: strings.TrimRight(strings.TrimSpace(consoleBaseURL), "/"),
	}
}

func (m *invitationMailer) SendInvitation(ctx context.Context, inv InvitationEmail) error {
	if m.sender == nil || m.consoleBaseURL == "" {
		return mailer.ErrNotConfigured
	}
	acceptURL := AcceptURL(m.consoleBaseURL, inv.Token)
	text, htmlBody := renderInvitation(inv, acceptURL)
	return m.sender.Send(ctx, mailer.Message{
		To:      inv.To,
		Subject: invitationSubject(inv.WorkspaceName),
		Text:    text,
		HTML:    htmlBody,
	})
}

// AcceptURL builds the link an invitee opens. The path is the acceptance page
// that already exists in the console (apps/web-console/app/invitations/accept).
func AcceptURL(consoleBaseURL, token string) string {
	base := strings.TrimRight(strings.TrimSpace(consoleBaseURL), "/")
	return base + "/invitations/accept?token=" + url.QueryEscape(token)
}

func invitationSubject(workspace string) string {
	bounded := singleLine(workspace, 80)
	if bounded == "" {
		return "You have been invited to a Hive workspace"
	}
	return fmt.Sprintf("Join %s on Hive", bounded)
}

// singleLine bounds a caller-controlled string before it reaches an email body.
//
// CodeQL's go/email-injection is right to flag this class even though the HTML
// escaping already closes markup injection: a workspace display name is chosen
// by a customer, and an unbounded multi-line one can be used to compose what
// looks like a second message inside ours ("your account is suspended, call
// this number"). Escaping stops them adding a link. This stops them adding a
// paragraph.
//
// So: every line break, tab and control character becomes a space, runs of
// whitespace collapse, and the result is capped. What survives is one short
// phrase in a sentence that already says who it is from and what it is for.
func singleLine(value string, max int) string {
	var b strings.Builder
	space := false
	for _, r := range value {
		if r == '\r' || r == '\n' || r == '\t' || unicode.IsControl(r) || r == ' ' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteRune(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := []rune(strings.TrimSpace(b.String()))
	if len(out) > max {
		return string(out[:max]) + "…"
	}
	return string(out)
}

// workspaceLabel keeps the copy readable when the display name is missing,
// which is a data problem rather than a reason to mail a sentence with a hole
// in it.
func workspaceLabel(workspace string) string {
	bounded := singleLine(workspace, 80)
	if bounded == "" {
		return "a workspace"
	}
	return bounded
}

func roleLabel(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleOwner:
		return "an owner"
	default:
		return "a member"
	}
}

func inviterLabel(email string) string {
	bounded := singleLine(email, 120)
	if bounded == "" {
		return "Someone"
	}
	return bounded
}

// renderInvitation returns the plain-text and HTML alternatives.
//
// Design constraints this template is built to, all of them load bearing for a
// product sold to regulated buyers whose first artifact from us is this email:
// one action and one action only, no images at all so nothing depends on remote
// content loading, the full URL repeated as text for clients that strip the
// anchor, an explicit expiry, an explicit "ignore this" line, and colours
// declared on every surface so a client's automatic dark mode cannot invert the
// card into unreadable text.
func renderInvitation(inv InvitationEmail, acceptURL string) (string, string) {
	workspace := workspaceLabel(inv.WorkspaceName)
	inviter := inviterLabel(inv.InviterEmail)
	role := roleLabel(inv.Role)
	expires := inv.ExpiresAt.UTC().Format("2 January 2006 at 15:04 UTC")

	text := strings.Join([]string{
		fmt.Sprintf("%s invited you to join %s on Hive as %s.", inviter, workspace, role),
		"",
		"Open this link to accept the invitation:",
		acceptURL,
		"",
		fmt.Sprintf("The link expires on %s.", expires),
		"",
		"If you were not expecting this invitation you can ignore this email. Nothing happens until you open the link.",
		"",
		"Hive",
	}, "\n")

	esc := html.EscapeString
	htmlBody := `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<meta name="supported-color-schemes" content="light dark">
<title>` + esc(invitationSubject(inv.WorkspaceName)) + `</title>
<style>
  /* Declared rather than inherited, so a client that synthesises a dark theme
     recolours the card as a whole instead of leaving dark text on dark. */
  @media (prefers-color-scheme: dark) {
    .page { background-color: #14161a !important; }
    .card { background-color: #1d2026 !important; border-color: #2f343d !important; }
    .ink { color: #eef0f4 !important; }
    .ink-muted { color: #b3b9c4 !important; }
    .rule { border-color: #2f343d !important; }
    .url { color: #b3b9c4 !important; }
  }
</style>
</head>
<body class="page" style="margin:0;padding:0;background-color:#f4f5f7;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;">` +
		esc(fmt.Sprintf("%s invited you to join %s on Hive.", inviter, workspace)) + `</div>
<table role="presentation" class="page" width="100%" cellpadding="0" cellspacing="0" border="0" style="background-color:#f4f5f7;padding:24px 12px;">
  <tr>
    <td align="center">
      <table role="presentation" class="card" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:520px;background-color:#ffffff;border:1px solid #e2e5ea;border-radius:12px;">
        <tr>
          <td style="padding:28px 28px 8px 28px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
            <p class="ink-muted" style="margin:0 0 18px 0;font-size:13px;line-height:18px;color:#5b6472;letter-spacing:0.08em;text-transform:uppercase;">Hive</p>
            <h1 class="ink" style="margin:0 0 14px 0;font-size:22px;line-height:30px;font-weight:600;color:#12141a;">Join ` + esc(workspace) + `</h1>
            <p class="ink" style="margin:0 0 20px 0;font-size:15px;line-height:23px;color:#12141a;">` +
		esc(inviter) + ` invited you to join <strong>` + esc(workspace) + `</strong> on Hive as ` + esc(role) + `.</p>
          </td>
        </tr>
        <tr>
          <td style="padding:0 28px 20px 28px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
            <table role="presentation" cellpadding="0" cellspacing="0" border="0">
              <tr>
                <td align="center" bgcolor="#1c4ed8" style="border-radius:8px;">
                  <a href="` + esc(acceptURL) + `" style="display:inline-block;padding:13px 26px;font-size:15px;line-height:20px;font-weight:600;color:#ffffff;text-decoration:none;border-radius:8px;background-color:#1c4ed8;">Accept the invitation</a>
                </td>
              </tr>
            </table>
          </td>
        </tr>
        <tr>
          <td style="padding:0 28px 24px 28px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
            <p class="ink-muted" style="margin:0 0 6px 0;font-size:13px;line-height:19px;color:#5b6472;">If the button does not work, copy this address into your browser:</p>
            <p class="url" style="margin:0 0 18px 0;font-size:13px;line-height:19px;color:#5b6472;word-break:break-all;">` + esc(acceptURL) + `</p>
            <hr class="rule" style="border:0;border-top:1px solid #e2e5ea;margin:0 0 18px 0;">
            <p class="ink-muted" style="margin:0 0 8px 0;font-size:13px;line-height:19px;color:#5b6472;">The link expires on ` + esc(expires) + `.</p>
            <p class="ink-muted" style="margin:0;font-size:13px;line-height:19px;color:#5b6472;">If you were not expecting this invitation you can ignore this email. Nothing happens until you open the link.</p>
          </td>
        </tr>
      </table>
    </td>
  </tr>
</table>
</body>
</html>`

	return text, htmlBody
}
