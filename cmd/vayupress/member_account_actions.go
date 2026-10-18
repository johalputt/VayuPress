// SPDX-License-Identifier: Apache-2.0

package main

// member_account_actions.go — what a member can change about their own account
// that the profile form does not cover: whether their plan renews, deleting the
// account, and previews of the pictures they can choose from.
//
// Both writes are plain form posts, answered with a redirect back to the page
// and one line saying what happened, so they work without the page's script.
// The member cookie is SameSite=Lax and the routes refuse a cross-origin post
// (refuseCrossOrigin), as every member write does.

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/avatar"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/payments"
)

// handleMemberRenewal stops a Stripe plan renewing at the end of its period
// (renew=0) or takes that back (renew=1). Stripe is changed first and the
// local record follows only when Stripe agreed: the other order could show a
// member "Ends 2 November" while Stripe went on to charge them.
// POST /members/account/renewal
func (a *App) handleMemberRenewal(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		http.Redirect(w, r, "/members", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	renew := r.PostFormValue("renew") == "1"
	failed := func(why string) {
		logging.LogError("members", "renewal change refused for "+m.Email, why)
		http.Redirect(w, r, "/members/account?renewal=failed#plan", http.StatusSeeOther)
	}
	sub, _ := a.members.ActiveSubscription(r.Context(), m.ID)
	key, on := a.stripeSecretKey(r.Context())
	switch {
	case sub == nil:
		failed("no active subscription")
		return
	case sub.Cadence == members.CadenceComplimentary:
		failed("a complimentary plan has no renewal")
		return
	case !on || m.StripeCustomer == "":
		failed("the plan does not renew through Stripe")
		return
	}
	n, err := payments.NewStripeClient(a.outboundClient, key).SetCancelAtPeriodEnd(r.Context(), m.StripeCustomer, !renew)
	if err != nil {
		failed(err.Error())
		return
	}
	if renew {
		// Nothing in Stripe renews, so there is no renewal to restore.
		if n == 0 {
			failed("Stripe holds no subscription to renew")
			return
		}
		_ = a.members.ResumeSubscription(r.Context(), m.ID)
		logging.LogInfo("members", "plan renewal restored by the member: "+m.Email)
		http.Redirect(w, r, "/members/account?renewal=on#plan", http.StatusSeeOther)
		return
	}
	// n == 0 is still a stopped renewal: Stripe has nothing that would charge
	// again, and the local record now says so too.
	if err := a.members.ScheduleCancellation(r.Context(), m.ID); err != nil {
		failed(err.Error())
		return
	}
	logging.LogInfo("members", "plan renewal stopped by the member: "+m.Email)
	http.Redirect(w, r, "/members/account?renewal=off#plan", http.StatusSeeOther)
}

// handleMemberAccountDelete removes the signed-in member's account at their
// own request, confirmed by typing their email address. Payments are stopped
// first, and nothing is removed if they cannot be: an account deleted while
// Stripe goes on charging for it is the worst outcome this page could have.
// A mailbox the member holds is not removed: it is the operator's to remove,
// and the page says so before the member confirms.
// POST /members/account/delete
func (a *App) handleMemberAccountDelete(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		http.Redirect(w, r, "/members", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(r.PostFormValue("confirm")), m.Email) {
		http.Redirect(w, r, "/members/account?delete=confirm#leave", http.StatusSeeOther)
		return
	}
	if m.StripeCustomer != "" {
		key, on := a.stripeSecretKey(r.Context())
		sub, _ := a.members.ActiveSubscription(r.Context(), m.ID)
		paying := sub != nil && sub.Cadence != members.CadenceComplimentary
		switch {
		case on:
			if err := payments.NewStripeClient(a.outboundClient, key).CancelSubscriptionsNow(r.Context(), m.StripeCustomer); err != nil {
				logging.LogError("members", "account deletion refused for "+m.Email+": payments not stopped", err.Error())
				http.Redirect(w, r, "/members/account?delete=failed#leave", http.StatusSeeOther)
				return
			}
		case paying:
			// Stripe is switched off on this install, so its subscription cannot
			// be reached from here, and may still charge.
			logging.LogError("members", "account deletion refused for "+m.Email, "Stripe is not configured, so the paid plan cannot be stopped")
			http.Redirect(w, r, "/members/account?delete=failed#leave", http.StatusSeeOther)
			return
		}
	}
	if a.newsletterStore != nil {
		_ = a.newsletterStore.UnsubscribeEmail(r.Context(), m.Email)
	}
	if err := a.members.Erase(r.Context(), m.Email); err != nil {
		logging.LogError("members", "account deletion failed for "+m.Email, err.Error())
		http.Redirect(w, r, "/members/account?delete=failed#leave", http.StatusSeeOther)
		return
	}
	logging.LogInfo("members", "account deleted by the member: "+m.Email)
	expireMemberCookie(w)
	http.Redirect(w, r, "/members?deleted=1", http.StatusSeeOther)
}

// handleMemberAvatarPreview draws one picture the signed-in member can choose,
// as it would look for them: the automatic one or a cartoon, both seeded by
// their own address, so the preview is the picture they get.
// GET /api/v1/members/avatar/preview/{choice}   choice: auto | 0..CartoonCount-1
func (a *App) handleMemberAvatarPreview(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	var svg string
	switch c := chi.URLParam(r, "choice"); c {
	case "auto":
		svg = avatar.Auto(m.Email, m.Gender)
	default:
		n, err := strconv.Atoi(c)
		if err != nil || n < 0 || n >= avatar.CartoonCount {
			http.NotFound(w, r)
			return
		}
		svg = avatar.Cartoon(n, m.Email)
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(svg))
}
