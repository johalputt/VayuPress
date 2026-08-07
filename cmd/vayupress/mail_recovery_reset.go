// SPDX-License-Identifier: Apache-2.0

package main

// mail_recovery_reset.go — the one path every VayuMail password reset takes
// (ADR-0144).
//
// A reset that only replaces the password hash is the classic incomplete fix.
// App passwords are independent credentials, member sessions are live cookies,
// and an outstanding reset link is a bearer key — all three survive a naive
// password change, so an attacker who already holds one keeps their access
// THROUGH the victim's recovery, and the victim has every reason to believe they
// just locked the door.
//
// So there is exactly one function, and self-service, administrator and
// break-glass resets all go through it. The dependencies are function fields
// rather than a grab of App, which is what lets the tests assert that every step
// actually ran and in the right order — the property that matters here is
// completeness, and completeness is invisible in a code review.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/logging"
)

// mailResetReason records how a reset was authorised. It drives both the audit
// record and the wording of the notification, because "you used a recovery code"
// and "an administrator did this" need very different reactions from the reader.
type mailResetReason string

const (
	mailResetByCode       mailResetReason = "recovery-code"
	mailResetByLink       mailResetReason = "recovery-link"
	mailResetByAdmin      mailResetReason = "administrator"
	mailResetByBreakGlass mailResetReason = "break-glass"
	mailResetByDevice     mailResetReason = "trusted-device"
)

// mailResetOutcome reports what the pipeline actually destroyed. It is returned
// (not just logged) so the caller can tell the holder "and your 3 connected
// devices were signed out", which is the difference between a reset they trust
// and one they wonder about.
type mailResetOutcome struct {
	AppPasswordsRevoked int
	SessionsRevoked     int
	QueueHeld           int
	NotifiedContact     string
	// Problems lists steps that failed. A reset is never abandoned half-done —
	// leaving app passwords alive is worse than any error — so failures are
	// collected, reported and audited rather than aborting the sequence.
	Problems []string
}

// mailResetDeps is everything the pipeline touches. Every field is required
// except Notify and RecoveryContact, which are skipped when nil (a Tor Space has
// no external channel to notify over).
type mailResetDeps struct {
	SetPasswordHash    func(ctx context.Context, email, hash string) error
	RevokeAppPasswords func(ctx context.Context, email string) (int, error)
	RevokeSessions     func(ctx context.Context, email string) (int, error)
	// RevokeConsoleSessions ends the /os console sessions a mailbox credential
	// can mint. RevokeSessions above is the MEMBER store — a different table in a
	// different package — and for years it was the only one wired, so a reset
	// reported "0 web sessions ended" while the attacker's console cookie kept
	// resolving. ADR-0144 enumerated IMAP/SMTP, app passwords and the webmail
	// member session, and omitted the /os/login mailbox fallback entirely; this
	// is that fourth credential finally getting its revocation step.
	RevokeConsoleSessions func(ctx context.Context, email string) (int, error)
	InvalidateTokens      func(ctx context.Context, email string)
	HoldQueue             func(ctx context.Context, email string) (int, error)
	RecoveryContact       func(ctx context.Context, email string) string
	Notify                func(ctx context.Context, to, subject, body string) error
	FileToMailbox         func(ctx context.Context, email, subject, body string) error
	Audit                 func(action, actor, target, detail string)
	Now                   func() time.Time
	// handedOver reports whether this mailbox has been handed to its owner
	// (ADR-0152 D4). Nil in tests and in installs where VayuMail is absent, which
	// reads as "not handed over" — the historic behaviour.
	handedOver func(email string) bool
	// RecordAccess appends to the client-visible access ledger. Required for the
	// break-glass path and unused otherwise.
	RecordAccess func(ctx context.Context, mailbox, actor, action, detail string) error
}

// errMailHandedOver is returned when the operator's password-reset path is
// refused because the mailbox belongs to its owner now.
var errMailHandedOver = errors.New("this mailbox has been handed over; its password cannot be reset by an administrator")

// errBreakGlassNeedsLedger refuses the emergency override when the access record
// is unavailable. The record IS the control here, so an override that cannot be
// recorded is simply an administrator reset under another name.
var errBreakGlassNeedsLedger = errors.New("the emergency override cannot run without recording it")

// minMailPasswordLen matches the admin update path so recovery cannot be used to
// set a weaker password than the console would allow.
const minMailPasswordLen = 8

// applyMailPasswordReset runs the complete reset. It returns an error only when
// the password itself could not be changed — that is the one failure that means
// nothing happened. Every later step is best-effort-but-recorded, because a
// mailbox left with a new password and a live attacker device is the outcome
// this whole function exists to prevent.
func applyMailPasswordReset(ctx context.Context, d mailResetDeps, email, newPassword string,
	reason mailResetReason, actor string) (mailResetOutcome, error) {
	// SEVERANCE (ADR-0152 D4). Resetting a handed-over mailbox's password is the
	// other half of "we cannot get into your account": reset the password, clear
	// the second factor, sign in. Refused here because the console, the assisted
	// recovery flow and the CLI all funnel through this one function — one
	// refusal covers three doors.
	//
	// The OWNER's own recovery paths are untouched — a recovery code, a recovery
	// link or a trusted device is the holder proving who they are, which is the
	// whole point of handing them the mailbox. What is refused is the
	// ADMINISTRATOR path.
	//
	// break-glass is permitted and is the one remaining way in, precisely because
	// it cannot run without writing a permanent, client-visible record. That is
	// the difference between an escape hatch and a back door: both exist, only one
	// leaves a mark nobody can remove.
	if d.handedOver != nil && reason == mailResetByAdmin && d.handedOver(email) {
		return mailResetOutcome{}, errMailHandedOver
	}
	if d.handedOver != nil && reason == mailResetByBreakGlass && d.handedOver(email) {
		if d.RecordAccess == nil {
			// No ledger, no break-glass. A break-glass whose record is optional is
			// an administrator reset with a louder name.
			return mailResetOutcome{}, errBreakGlassNeedsLedger
		}
		if err := d.RecordAccess(ctx, email, actor, "break-glass",
			"an administrator reset this mailbox's password using the emergency override"); err != nil {
			return mailResetOutcome{}, err
		}
	}

	var out mailResetOutcome
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return out, fmt.Errorf("mailbox required")
	}
	if len([]rune(newPassword)) < minMailPasswordLen {
		return out, fmt.Errorf("password must be at least %d characters", minMailPasswordLen)
	}
	if d.SetPasswordHash == nil {
		return out, fmt.Errorf("reset is not available")
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}

	// 1. The password. If this fails nothing else should run: revoking a holder's
	//    devices while leaving their old password working would lock them further
	//    out rather than recovering them.
	hash, err := auth.HashSecretArgon2id(newPassword)
	if err != nil {
		return out, fmt.Errorf("could not hash password")
	}
	if err := d.SetPasswordHash(ctx, email, hash); err != nil {
		return out, err
	}

	fail := func(step string, err error) {
		out.Problems = append(out.Problems, step+": "+err.Error())
		logging.LogError("vayumail", "recovery reset step failed for "+email, step+": "+err.Error())
	}

	// 2. Every app password. The step that makes the reset mean something.
	if d.RevokeAppPasswords != nil {
		if n, err := d.RevokeAppPasswords(ctx, email); err != nil {
			fail("revoke app passwords", err)
		} else {
			out.AppPasswordsRevoked = n
		}
	}
	// 3. Live webmail sessions, plus any pending magic link.
	if d.RevokeSessions != nil {
		if n, err := d.RevokeSessions(ctx, email); err != nil {
			fail("revoke sessions", err)
		} else {
			out.SessionsRevoked = n
		}
	}
	// 3b. And the /os console session that same mailbox password can mint. This
	//     is a SEPARATE store from step 3 — see RevokeConsoleSessions — and its
	//     absence was the gap between what the notice below claims and what the
	//     reset actually did. The count joins SessionsRevoked because the holder
	//     is being told how many things were signed out, not which table they
	//     lived in.
	if d.RevokeConsoleSessions != nil {
		if n, err := d.RevokeConsoleSessions(ctx, email); err != nil {
			fail("revoke console sessions", err)
		} else {
			out.SessionsRevoked += n
		}
	}
	// 4. Outstanding reset links, including the one that authorised this reset if
	//    it has not already been consumed, and any a second requester holds.
	if d.InvalidateTokens != nil {
		d.InvalidateTokens(ctx, email)
	}
	// 5. Queued outbound. Sending is an attacker's first act.
	if d.HoldQueue != nil {
		if n, err := d.HoldQueue(ctx, email); err != nil {
			fail("hold queued mail", err)
		} else {
			out.QueueHeld = n
		}
	}

	// 6. Tell the holder, on every channel that is not the credential just
	//    changed. The in-mailbox copy matters even though the attacker could
	//    delete it: most account takeovers are not that careful, and it is the
	//    only channel that exists when there is no recovery address.
	subject, body := mailResetNotice(email, reason, actor, out, now())
	if d.RecoveryContact != nil && d.Notify != nil {
		if to := d.RecoveryContact(ctx, email); to != "" {
			if err := d.Notify(ctx, to, subject, body); err != nil {
				fail("notify recovery address", err)
			} else {
				out.NotifiedContact = to
			}
		}
	}
	if d.FileToMailbox != nil {
		if err := d.FileToMailbox(ctx, email, subject, body); err != nil {
			fail("file notice to mailbox", err)
		}
	}

	// 7. Audit last, with the outcome, so the record says what was destroyed and
	//    not merely that something happened.
	if d.Audit != nil {
		detail := fmt.Sprintf("reason=%s app_passwords_revoked=%d sessions_revoked=%d queue_held=%d",
			reason, out.AppPasswordsRevoked, out.SessionsRevoked, out.QueueHeld)
		if len(out.Problems) > 0 {
			detail += " problems=" + strings.Join(out.Problems, "; ")
		}
		d.Audit("vayumail.password.reset", actor, email, detail)
	}
	return out, nil
}

// mailResetNotice composes the "this just happened" message. It deliberately
// names what was destroyed and what to do if it was not you, because a
// notification that says only "your password changed" gives the reader nothing
// to act on.
func mailResetNotice(email string, reason mailResetReason, actor string,
	out mailResetOutcome, at time.Time) (subject, body string) {

	how := "using a recovery code"
	switch reason {
	case mailResetByLink:
		how = "using a link sent to your recovery address"
	case mailResetByAdmin:
		how = "by an administrator (" + actor + ")"
	case mailResetByBreakGlass:
		how = "from the server console by an operator with shell access"
	case mailResetByDevice:
		how = "from one of your signed-in devices"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "The password for %s was reset %s on %s.\n\n",
		email, how, at.UTC().Format("2 January 2006 at 15:04 UTC"))
	b.WriteString("For your security, everything that could still be signed in was disconnected:\n\n")
	fmt.Fprintf(&b, "  - %d connected app%s or device%s signed out\n",
		out.AppPasswordsRevoked, plural(out.AppPasswordsRevoked), plural(out.AppPasswordsRevoked))
	fmt.Fprintf(&b, "  - %d web session%s ended\n", out.SessionsRevoked, plural(out.SessionsRevoked))
	if out.QueueHeld > 0 {
		fmt.Fprintf(&b, "  - %d message%s waiting to be sent %s been held for review\n",
			out.QueueHeld, plural(out.QueueHeld), map[bool]string{true: "has", false: "have"}[out.QueueHeld == 1])
	}
	b.WriteString("\nYou will need to sign in again on every device, and set up your mail app once more.\n\n")
	b.WriteString("If this was NOT you, act now: someone else may have taken control of this mailbox. ")
	b.WriteString("Sign in and change the password immediately, then contact your administrator.\n")
	return "Your mailbox password was reset", b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// mailResetDepsFor wires the pipeline to this instance. Returns ok=false when
// VayuMail is not running, so callers fail closed rather than half-resetting.
func (a *App) mailResetDepsFor() (mailResetDeps, bool) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		return mailResetDeps{}, false
	}
	accts := a.vayuMail.Accounts()
	d := mailResetDeps{
		SetPasswordHash:    accts.SetPasswordHash,
		RevokeAppPasswords: accts.RevokeAllAppPasswords,
		InvalidateTokens:   accts.InvalidateRecoveryTokens,
		RecoveryContact:    accts.RecoveryContact,
		Audit:              dbpkg.AuditLog,
	}
	if a.members != nil {
		d.RevokeSessions = a.members.RevokeSessions
	}
	if a.sessions != nil {
		// "vmail:"+address is the user id handleOSLoginSubmit stores when a
		// mailbox credential signs in to the console (admin_os_ui.go). Ending it
		// here is what makes the notice's "everything that could still be signed
		// in was disconnected" true.
		d.RevokeConsoleSessions = func(ctx context.Context, email string) (int, error) {
			return a.sessions.DestroyForUser(ctx, "vmail:"+strings.ToLower(strings.TrimSpace(email)))
		}
	}
	d.HoldQueue = a.vayuMail.HoldOutboundFor
	d.Notify = a.sendRecoveryNotice
	// ADR-0152 D4: the operator's reset path is refused once a mailbox belongs to
	// its owner. Wired here so the console, the assisted-recovery flow and the CLI
	// all inherit it — they funnel through applyMailPasswordReset.
	d.handedOver = a.vayuMail.IsHandedOver
	d.RecordAccess = a.vayuMail.AppendLedger
	d.FileToMailbox = a.fileRecoveryNotice
	return d, true
}

// sendRecoveryNotice mails a recovery notification to an EXTERNAL address. It is
// a plain-text message on purpose: this is a security notice, and a reader should
// never have to trust rendered markup to understand that their mailbox changed
// hands.
func (a *App) sendRecoveryNotice(_ context.Context, to, subject, body string) error {
	if a.mailer == nil {
		return fmt.Errorf("no outbound mailer configured")
	}
	return a.mailer.Send(email.Message{To: to, Subject: subject, Text: body})
}

// fileRecoveryNotice drops the same notice into the mailbox itself. An attacker
// with control of the account could delete it, which is exactly why it is not the
// only channel — but it is the only channel that exists when no recovery address
// is enrolled, and most takeovers are not careful enough to clean up.
func (a *App) fileRecoveryNotice(_ context.Context, mailbox, subject, body string) error {
	if a.vayuMail == nil {
		return fmt.Errorf("vayumail is not running")
	}
	from := "security@" + a.vayuMail.Config().Domain
	raw := buildNoticeMessage(from, mailbox, subject, body, time.Now())
	_, err := a.vayuMail.DeliverInbound(mailbox, raw)
	return err
}

// buildNoticeMessage assembles a minimal RFC 5322 message. Header values are
// stripped of CR and LF: the subject is composed here, but folding the sanitiser
// in at the point of assembly means a future caller cannot introduce a header
// injection by passing through user input.
func buildNoticeMessage(from, to, subject, body string, at time.Time) []byte {
	var b strings.Builder
	b.WriteString("From: VayuMail Security <" + headerSafe(from) + ">\r\n")
	b.WriteString("To: <" + headerSafe(to) + ">\r\n")
	b.WriteString("Subject: " + headerSafe(subject) + "\r\n")
	b.WriteString("Date: " + at.UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	return []byte(b.String())
}

// headerSafe TRUNCATES at the first CR or LF rather than deleting them.
//
// Deleting is the obvious move and it is wrong: stripping the newlines out of
// "victim@example.com\r\nBcc: attacker@evil.test" splices the forged text into
// the surviving header, yielding a corrupt address that still reads as an
// instruction to a lenient parser. Cutting the value at the first control
// character keeps the legitimate prefix and discards everything the injector
// appended.
func headerSafe(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
