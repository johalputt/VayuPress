// SPDX-License-Identifier: Apache-2.0

package main

// handlers_contact.go — public contact-form submission.
//
// The contact form is opt-in per page: an operator places the [[contact-form]]
// marker in a page's content, the render layer injects a CSP-safe widget, and
// that widget POSTs here. Submissions are validated, honeypot-screened and
// rate-limited, then delivered to the operator's configured contact address over
// the built-in VayuMail SMTP sender (a.mailer). No third-party form service.

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// contactLimiter caps each client IP to 5 contact submissions per minute — ample
// for a human, hostile to a flood.
var contactLimiter = newIngestLimiter(5, time.Minute)

// handleContactSubmit accepts {name,email,message,website} and emails it to the
// operator's configured contact address. "website" is a honeypot: a non-empty
// value means a bot, which we accept-and-drop (HTTP 200, no delivery) so the
// attacker gets no signal.
func (a *App) handleContactSubmit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Email   string `json:"email"`
		Message string `json:"message"`
		Website string `json:"website"` // honeypot
		Page    string `json:"page"`    // path of the page the form is on
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := readJSONDirect(r, &body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}

	// Honeypot tripped → pretend success, deliver nothing.
	if strings.TrimSpace(body.Website) != "" {
		writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Per-IP rate limit.
	ip := clientIPForContact(r)
	if !contactLimiter.allow(ip) {
		writeAPIError(w, r, http.StatusTooManyRequests, "rate-limited", "Too many messages — please try again in a minute", "")
		return
	}

	name := strings.TrimSpace(body.Name)
	from := strings.TrimSpace(body.Email)
	message := strings.TrimSpace(body.Message)
	if name == "" || from == "" || message == "" {
		writeAPIError(w, r, http.StatusBadRequest, "missing-fields", "Name, email and message are all required", "")
		return
	}
	if len(name) > 120 || len(from) > 200 || len(message) > 5000 {
		writeAPIError(w, r, http.StatusBadRequest, "too-long", "One of the fields is too long", "")
		return
	}
	if !looksLikeEmail(from) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-email", "Please enter a valid email address", "")
		return
	}

	// Persist the message first — the /os inbox (Messages tab) is the durable
	// record of every submission. Emailing the operator and auto-replying to the
	// visitor are best-effort niceties layered on top: a site WITHOUT VayuMail/
	// SMTP configured still collects contact messages, and the operator reads them
	// in the Messages tab. (Previously the handler refused the whole submission
	// when email delivery wasn't configured, so visitors saw an error and the
	// message was lost — even though it could have been stored.)
	// GDPR: we never store the raw IP. Resolve a coarse country (offline) and
	// region/city (from trusted proxy headers, when present) at submit time and
	// store only those; the IP above is used purely in-process for rate limiting.
	geo := geoFromHeaders(r)
	persisted := false
	if dbpkg.DB != nil {
		if _, err := dbpkg.WDB.ExecContext(r.Context(),
			`INSERT INTO contact_messages(id,name,email,message,page,country,region,city,is_read,created_at) VALUES(?,?,?,?,?,?,?,?,0,?)`,
			newUUID(), name, from, message, firstNonEmptyContact(body.Page, contactPageRef(r)), geo.Country, geo.Region, geo.City, time.Now().UTC()); err != nil {
			logging.LogError("contact", "persist failed", err.Error())
		} else {
			persisted = true
		}
	}

	// The operator's contact address + an enabled mailer are needed only to EMAIL
	// the submission, not to accept it.
	recipient := ""
	if a.siteSettings != nil {
		recipient = strings.TrimSpace(a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyContactEmail))
	}
	mailReady := recipient != "" && a.mailer != nil && a.mailer.Enabled()

	// If the message could be neither stored nor emailed it would simply be lost,
	// so only then refuse it (telling the visitor to reach out another way).
	if !persisted && !mailReady {
		writeAPIError(w, r, http.StatusServiceUnavailable, "contact-unavailable",
			"This site can't receive contact messages right now — please contact the site owner directly.", "")
		return
	}

	if mailReady {
		// Plain-text body; the sender sanitises control characters. The visitor's
		// address goes in the body (the From header stays the site's own identity
		// so SPF/DKIM remain valid); operators just hit reply to the quoted address.
		text := "New contact-form message\n\n" +
			"From: " + name + " <" + from + ">\n" +
			"Site: " + r.Host + "\n\n" +
			message + "\n"

		if err := a.mailer.Send(email.Message{
			To:      recipient,
			Subject: "Contact form: " + name,
			Text:    text,
		}); err != nil {
			logging.LogError("contact", "delivery failed", err.Error())
			// The message is already in the inbox, so a delivery failure must not
			// fail the visitor. Only when nothing was persisted do we surface it.
			if !persisted {
				writeAPIError(w, r, http.StatusBadGateway, "send-failed", "Could not send your message — please try again later", "")
				return
			}
		}

		// Auto-reply to the visitor (best-effort; never fails their request).
		// Enabled by default — only an explicit "off" suppresses it.
		if a.siteSettings == nil || a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyContactAutoReply) != "off" {
			siteName := r.Host
			if a.siteSettings != nil {
				if n := strings.TrimSpace(a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeySiteName)); n != "" {
					siteName = n
				}
			}
			// Per-page custom confirmation, if the page's marker carries one
			// ([[contact-form: …]]); otherwise the default line. The page content is
			// the single source of truth, re-parsed here at submit time.
			intro := "Thanks for getting in touch — we've received your message and will get back to you soon."
			if custom := a.pageContactReply(r.Context(), pageSlugFromPath(firstNonEmptyContact(body.Page, contactPageRef(r)))); custom != "" {
				intro = custom
			}
			reply := "Hi " + name + ",\n\n" +
				intro + "\n\n" +
				"For your records, here's what you sent:\n\n" +
				message + "\n\n" +
				"— " + siteName + "\n"
			if err := a.mailer.Send(email.Message{
				To:      from,
				Subject: "We got your message — " + siteName,
				Text:    reply,
			}); err != nil {
				// A failed confirmation must not fail the visitor's submission — the
				// operator already has the message. Log and move on.
				logging.LogError("contact", "auto-reply failed", err.Error())
			}
		}
	}

	outcome := "stored to inbox"
	if mailReady {
		outcome = "stored and emailed"
	}
	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "contact", Severity: "info",
		Msg: "contact message " + outcome, RequestID: getRequestID(r),
	})
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// clientIPForContact returns the client IP used to key the public rate limiters
// and to name the actor in the audit log.
//
// AUDIT FINDING (Section 1). This function used to read the raw
// X-Forwarded-For header and return its LEFTMOST element with no trusted-proxy
// check — ignoring r.RemoteAddr, which realIPMiddleware has already replaced
// with the correctly resolved auth.ClientIP. The shipped nginx templates set
// `$proxy_add_x_forwarded_for`, which APPENDS the real peer to whatever the
// client sent, so the leftmost element was entirely attacker-chosen behind the
// proxy and equally so on a direct-bind install. A fresh header per request
// minted a fresh budget every time, which is not a rate limit.
//
// Two harms, and the second is the one worth naming. Every limiter keyed on this
// stopped bounding anything — including deviceResetByIP, the ONLY budget on
// /api/v1/members/vayumail-device-reset, past which the server runs Argon2id
// over up to twenty stored credentials per request. And the value went straight
// into the WORM audit log as the actor ("public:"+ip, "device:"+ip), never
// validated, so an arbitrary string could be written into the actor column. The
// hazard is documented in as many words on AuditActor (internal/db/db.go): "An
// audit trail an attacker can author is worse than none: it does not merely fail
// to record them, it records someone else." That fix landed there and not here.
//
// The resolver it should always have called refuses forwarding headers unless
// the immediate peer is a configured trusted proxy. Reading r.RemoteAddr is
// equivalent and cheaper, since the middleware already normalised it — the same
// thing loginClientIP has always done — but auth.ClientIP is called directly so
// this stays correct if the handler is ever reached without that middleware.
func clientIPForContact(r *http.Request) string {
	ip := auth.ClientIP(r)
	// A key that is not an address is a key an attacker chose. Nothing downstream
	// parses it — not the limiter, not the audit log, not the recovery email the
	// victim reads — so validating here is the only place it happens.
	if net.ParseIP(ip) == nil {
		return "unknown"
	}
	return ip
}

// contactPageRef records which page the message was sent from, for the admin
// inbox. It uses the Referer path (same-origin only); anything else yields "".
func contactPageRef(r *http.Request) string {
	ref := r.Referer()
	if ref == "" {
		return ""
	}
	if i := strings.Index(ref, "://"); i >= 0 {
		rest := ref[i+3:]
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			p := rest[slash:]
			if len(p) > 200 {
				p = p[:200]
			}
			return p
		}
	}
	return ""
}

// firstNonEmptyContact returns the first non-blank trimmed string.
func firstNonEmptyContact(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// pageSlugFromPath turns a same-origin path ("/contact", "/contact?x=1") into a
// bare slug ("contact"). Returns "" for the empty/root path or anything with a
// slash inside the slug (only single-segment page slugs are valid here).
func pageSlugFromPath(path string) string {
	p := strings.TrimSpace(path)
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.Trim(p, "/")
	if p == "" || strings.Contains(p, "/") {
		return ""
	}
	return p
}

// pageContactReply loads a page's content by slug and returns its per-page
// custom contact auto-reply (the [[contact-form: …]] message), or "" when the
// slug is unknown or the marker carries no custom text.
func (a *App) pageContactReply(ctx context.Context, slug string) string {
	if slug == "" || dbpkg.DB == nil {
		return ""
	}
	var content string
	if err := dbpkg.DB.QueryRowContext(ctx, `SELECT content FROM articles WHERE slug=?`, slug).Scan(&content); err != nil {
		return ""
	}
	custom, _ := render.ParseContactForm(content)
	return custom
}

// looksLikeEmail is a deliberately permissive sanity check (exactly one '@', a
// dot in the domain, no spaces). Real validation is delivery itself.
func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at != strings.LastIndexByte(s, '@') {
		return false
	}
	domain := s[at+1:]
	if strings.ContainsAny(s, " \t\r\n") || !strings.Contains(domain, ".") {
		return false
	}
	return len(domain) >= 3
}
