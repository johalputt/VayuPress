package main

// handlers_contact.go — public contact-form submission.
//
// The contact form is opt-in per page: an operator places the [[contact-form]]
// marker in a page's content, the render layer injects a CSP-safe widget, and
// that widget POSTs here. Submissions are validated, honeypot-screened and
// rate-limited, then delivered to the operator's configured contact address over
// the built-in VayuMail SMTP sender (a.mailer). No third-party form service.

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/logging"
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

	// Recipient must be configured by the operator.
	recipient := ""
	if a.siteSettings != nil {
		recipient = strings.TrimSpace(a.siteSettings.Get(r.Context(), settings.KeyContactEmail))
	}
	if recipient == "" {
		writeAPIError(w, r, http.StatusServiceUnavailable, "contact-unconfigured", "Contact form is not configured yet", "")
		return
	}
	if a.mailer == nil || !a.mailer.Enabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-unconfigured", "Email delivery is not configured on this site", "")
		return
	}

	// Plain-text body; the sender sanitises control characters. The visitor's
	// address goes in the body (the From header stays the site's own identity so
	// SPF/DKIM remain valid); operators just hit reply to the quoted address.
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
		writeAPIError(w, r, http.StatusBadGateway, "send-failed", "Could not send your message — please try again later", "")
		return
	}

	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "contact", Severity: "info",
		Msg: "contact message delivered", RequestID: getRequestID(r),
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
