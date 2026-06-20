package main

// handlers_members.go — reader memberships & paywalls (Tier 2).
//
// Member login is passwordless (emailed magic link). Per-article access levels
// (public|members|paid) gate content; non-authorised readers see a preview plus
// a sign-in/subscribe call to action instead of the full body. An optional,
// signature-verified Stripe webhook upgrades members to the paid tier.

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/emailtmpl"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
)

const memberCookie = "vp_member"

// resolveMember returns the logged-in member for the request, or nil.
func (a *App) resolveMember(r *http.Request) *members.Member {
	if a.members == nil {
		return nil
	}
	c, err := r.Cookie(memberCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	m, err := a.members.ValidateSession(r.Context(), c.Value)
	if err != nil {
		return nil
	}
	return m
}

// authorizedFor reports whether member m may view content at the given level.
func authorizedFor(level string, m *members.Member) bool {
	switch level {
	case members.AccessMembers:
		return m != nil
	case members.AccessPaid:
		return m != nil && m.IsPaid()
	default: // public
		return true
	}
}

// POST /api/v1/members/login  {email}
// Issues a magic link by email. Always responds 200 with a generic message so
// the endpoint cannot be used to enumerate which emails are members.
func (a *App) handleMemberLogin(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	// Accept both JSON (API clients) and HTML form posts (the paywall CTA).
	formPost := strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
	var emailRaw string
	if formPost {
		emailRaw = r.PostFormValue("email")
	} else {
		var body struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
			return
		}
		emailRaw = body.Email
	}
	email := strings.TrimSpace(strings.ToLower(emailRaw))

	// Always behave the same way regardless of whether the email is known, so
	// the endpoint cannot enumerate members.
	if _, err := a.members.Upsert(r.Context(), email); err == nil {
		if token, err := a.members.CreateLoginToken(r.Context(), email); err == nil {
			go a.sendMemberMagicLink(email, token)
		}
	}
	if formPost {
		http.Redirect(w, r, "/?check_email=1", http.StatusSeeOther)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "check your email"})
}

// sendMemberMagicLink emails the one-time sign-in link, honouring any
// operator-customised template (Tier 4).
func (a *App) sendMemberMagicLink(addr, token string) {
	if a.mailer == nil {
		return
	}
	link := "https://" + config.Cfg.Domain + "/members/verify?token=" + token
	msg := a.renderEmail(emailtmpl.MagicLink, map[string]interface{}{
		"Domain":     config.Cfg.Domain,
		"Link":       link,
		"TTLMinutes": 30,
	})
	if err := a.mailer.Send(email.Message{To: addr, Subject: msg.Subject, Text: msg.Text, HTML: msg.HTML}); err != nil {
		logging.LogError("members", "magic link email failed", err.Error())
	}
}

// GET /members/verify?token= — consumes a magic link and starts a member session.
func (a *App) handleMemberVerify(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		http.Error(w, "memberships not available", http.StatusServiceUnavailable)
		return
	}
	email, err := a.members.ConsumeLoginToken(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		http.Error(w, "Invalid or expired sign-in link", http.StatusBadRequest)
		return
	}
	m, err := a.members.Upsert(r.Context(), email)
	if err != nil {
		http.Error(w, "could not sign in", http.StatusInternalServerError)
		return
	}
	token, err := a.members.CreateSession(r.Context(), m.ID)
	if err != nil {
		http.Error(w, "could not start session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: memberCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: config.Cfg.Domain != "localhost", SameSite: http.SameSiteLaxMode,
		MaxAge: int(members.SessionTTL.Seconds()),
	})
	logging.LogInfo("members", "member signed in: "+m.Email)
	http.Redirect(w, r, "/?member=1", http.StatusSeeOther)
}

// POST /members/logout — ends the member session.
func (a *App) handleMemberLogout(w http.ResponseWriter, r *http.Request) {
	if a.members != nil {
		if c, err := r.Cookie(memberCookie); err == nil {
			_ = a.members.DestroySession(r.Context(), c.Value)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: memberCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// =============================================================================
// Admin: member + access management
// =============================================================================

// GET /api/v1/admin/members
func (a *App) handleMemberListAdmin(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	list, err := a.members.List(r.Context(), 500)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	counts, _ := a.members.Count(r.Context())
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"members": list, "counts": counts, "count": len(list)})
}

// PUT /api/v1/admin/members/{email}/tier  {tier}
func (a *App) handleMemberSetTier(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	var body struct {
		Tier string `json:"tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if err := a.members.SetTier(r.Context(), chi.URLParam(r, "email"), body.Tier); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "tier-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// GET /api/v1/admin/articles/{slug}/access  →  {level}
func (a *App) handleArticleAccessGet(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	level := a.members.GetAccess(r.Context(), chi.URLParam(r, "slug"))
	writeJSON(w, r, http.StatusOK, map[string]string{"level": level})
}

// PUT /api/v1/admin/articles/{slug}/access  {level}
func (a *App) handleArticleAccessSet(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	var body struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	slug := chi.URLParam(r, "slug")
	if err := a.members.SetAccess(r.Context(), slug, body.Level); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "access-error", err.Error(), "")
		return
	}
	// Purge any cached full-content page so the paywall takes effect immediately.
	render.CachePurgePost(slug)
	writeJSON(w, r, http.StatusOK, map[string]string{"level": body.Level})
}

// =============================================================================
// Optional Stripe webhook — paid upgrades without an embedded SDK
// =============================================================================

// POST /api/v1/stripe/webhook
// Verifies the Stripe-Signature header against STRIPE_WEBHOOK_SECRET and, on a
// checkout.session.completed event, upgrades the customer's member to paid.
func (a *App) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	secret := config.Cfg.StripeWebhookSecret
	if a.members == nil || secret == "" {
		http.Error(w, "stripe not configured", http.StatusServiceUnavailable)
		return
	}
	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	if !verifyStripeSignature(r.Header.Get("Stripe-Signature"), payload, secret) {
		http.Error(w, "bad signature", http.StatusBadRequest)
		return
	}
	var evt struct {
		Type string `json:"type"`
		Data struct {
			Object struct {
				CustomerEmail   string `json:"customer_email"`
				CustomerDetails struct {
					Email string `json:"email"`
				} `json:"customer_details"`
				Customer string `json:"customer"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if evt.Type == "checkout.session.completed" {
		email := evt.Data.Object.CustomerEmail
		if email == "" {
			email = evt.Data.Object.CustomerDetails.Email
		}
		if email != "" {
			if err := a.members.UpgradeByEmail(r.Context(), email, evt.Data.Object.Customer); err != nil {
				logging.LogError("stripe", "upgrade failed", err.Error())
			} else {
				logging.LogInfo("stripe", "member upgraded to paid: "+email)
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

// gofmt: keep helpers below

// verifyStripeSignature implements Stripe's t=…,v1=… HMAC-SHA256 scheme over
// "<timestamp>.<payload>" using the endpoint signing secret.
func verifyStripeSignature(header string, payload []byte, secret string) bool {
	var ts, v1 string
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			ts = kv[1]
		case "v1":
			v1 = kv[1]
		}
	}
	if ts == "" || v1 == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(v1)) == 1
}
