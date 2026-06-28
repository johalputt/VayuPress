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
	persisted := false
	if dbpkg.DB != nil {
		if _, err := dbpkg.WDB.ExecContext(r.Context(),
			`INSERT INTO contact_messages(id,name,email,message,page,ip,is_read,created_at) VALUES(?,?,?,?,?,?,0,?)`,
			newUUID(), name, from, message, firstNonEmptyContact(body.Page, contactPageRef(r)), ip, time.Now().UTC()); err != nil {
			logging.LogError("contact", "persist failed", err.Error())
		} else {
			persisted = true
		}
	}

	// The operator's contact address + an enabled mailer are needed only to EMAIL
	// the submission, not to accept it.
	recipient := ""
	if a.siteSettings != nil {
		recipient = strings.TrimSpace(a.siteSettings.Get(r.Context(), settings.KeyContactEmail))
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
		if a.siteSettings == nil || a.siteSettings.Get(r.Context(), settings.KeyContactAutoReply) != "off" {
			siteName := r.Host
			if a.siteSettings != nil {
				if n := strings.TrimSpace(a.siteSettings.Get(r.Context(), settings.KeySiteName)); n != "" {
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

// clientIPForContact extracts a best-effort client IP for rate-limiting,
// honouring X-Forwarded-For's first hop and falling back to RemoteAddr.
func clientIPForContact(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
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
