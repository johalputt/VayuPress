package main

// handlers_signup.go — public reader/member signup page.
//
// VayuPress memberships are passwordless: a reader "signs up" by entering their
// email and receiving a one-time magic link (handleMemberLogin upserts the
// member on first use). This page is the friendly, branded front door for that
// flow — reachable at /signup and linked from paywalls and the newsletter.
//
// It is a public page (no auth) and uses the site theme (/theme.css) so it
// matches the reader-facing site, not the VayuOS admin shell.

import (
	"html"
	"net/http"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
)

// handleMemberSignup renders the public signup page. If a member is already
// signed in it sends them to the homepage.
func (a *App) handleMemberSignup(w http.ResponseWriter, r *http.Request) {
	if m := a.resolveMember(r); m != nil {
		http.Redirect(w, r, "/?member=1", http.StatusSeeOther)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "index, follow")

	brand := html.EscapeString(config.Cfg.Domain)
	nonce := render.CSPNonce(r)

	// A success/notice banner driven by query flags from the POST redirect.
	notice := ""
	switch r.URL.Query().Get("status") {
	case "sent":
		notice = `<div class="su-notice su-notice--ok" role="status">Check your inbox — we just emailed you a secure sign-in link. It is valid for 30 minutes.</div>`
	case "error":
		notice = `<div class="su-notice su-notice--err" role="alert">Something went wrong sending your link. Please try again.</div>`
	}

	page := `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Join ` + brand + `</title>
<meta name="description" content="Become a member of ` + brand + ` — sign up free and read everything.">
<link rel="stylesheet" href="/theme.css">
<link rel="stylesheet" href="/static/css/signup.css">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head>
<body class="su-body">
<main class="su-shell" id="main-content">
  <section class="su-card">
    <div class="su-brand">
      <img class="su-logo" src="/static/favicon-light.png" alt="" width="40" height="40">
      <span class="su-brand-name">` + brand + `</span>
    </div>
    <h1 class="su-title">Become a member</h1>
    <p class="su-sub">Join free and unlock every story. No password to remember — we email you a one-time sign-in link.</p>
    ` + notice + `
    <form class="su-form" method="POST" action="/members/login" novalidate>
      <label class="su-label" for="su-email">Email address</label>
      <input class="su-input" id="su-email" type="email" name="email" required autocomplete="email" placeholder="you@example.com" aria-label="Email address">
      <button class="su-btn" type="submit">Sign up free →</button>
    </form>
    <ul class="su-perks">
      <li><span class="su-perk-dot"></span>Full access to members-only posts</li>
      <li><span class="su-perk-dot"></span>New stories delivered to your inbox</li>
      <li><span class="su-perk-dot"></span>One link to sign in on any device</li>
    </ul>
    <p class="su-foot">Already a member? <a href="/members" class="su-link">Sign in</a> with the same email — it works for both.</p>
  </section>
  <p class="su-legal">Powered by VayuPress · your email is used only to send your sign-in link.</p>
</main>
<script nonce="` + nonce + `">
(function(){'use strict';
var f=document.querySelector('.su-form');
if(f){f.addEventListener('submit',function(){var b=f.querySelector('.su-btn');if(b){b.disabled=true;b.textContent='Sending your link…';}});}
})();
</script>
</body></html>`

	// The global middleware already sets a strict CSP carrying this request's
	// nonce, so the inline bootstrap script above is permitted.
	_, _ = w.Write([]byte(page))
}
