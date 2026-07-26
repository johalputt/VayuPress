// SPDX-License-Identifier: Apache-2.0

package main

// handlers_legal.go — built-in legal pages served with the site's public
// styling. The VayuMail Android app's privacy policy lives here (a stable,
// in-binary URL) so Google Play has a policy link that needs no CMS content
// and ships with every VayuPress build.

import (
	"html"
	"net/http"

	"github.com/johalputt/vayupress/internal/config"
)

// handleVayuMailPrivacy serves the VayuMail app privacy policy at
// /vayumail/privacy. Pure HTML + the site theme (no inline scripts/styles),
// so it inherits the site's typography and colours and stays CSP-clean.
func (a *App) handleVayuMailPrivacy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "index, follow")

	brand := html.EscapeString(config.Cfg.Domain)
	if brand == "" {
		brand = "VayuPress"
	}
	contact := "ankush@" + brand

	page := `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>VayuMail Privacy Policy · ` + brand + `</title>
<meta name="description" content="Privacy policy for the VayuMail Android app (com.vayu.mail): no data collection, no trackers, no telemetry.">
<link rel="canonical" href="https://` + brand + `/vayumail/privacy">
<link rel="stylesheet" href="/theme.css">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head><body>
<div class="container">
<nav class="vayu-nav" aria-label="Primary">
  <a href="/" class="vayu-nav-brand"><img src="/static/favicon-light.png" alt="" width="24" height="24">` + brand + `</a>
</nav>
<main id="main-content">
<article class="vayu-prose">
<header class="vayu-article-header"><h1>VayuMail Privacy Policy</h1></header>
<div class="content">
<p><em>Effective 11 July 2026</em></p>

<p>VayuMail (<code>com.vayu.mail</code>) is an email client for mail servers
you control. This policy explains exactly what the app does and does not do
with your information.</p>

<h2>What we collect</h2>
<p><strong>Nothing.</strong> VayuMail contains no analytics, advertising,
crash-reporting, or tracking of any kind. The developer receives no data
about you or your use of the app. There are no third-party SDKs and no
advertising identifiers.</p>

<h2>Your data</h2>
<p>The app connects only to the mail server you configure — your own domain.
Your email, contacts, and credentials are exchanged solely between your
device and that server. Account credentials are stored encrypted on your
device (AES-256-GCM) and are never transmitted to the developer or any third
party.</p>

<h2>Network activity</h2>
<p>VayuMail communicates only with your mail server over encrypted
connections (TLS), and with that server's key-directory endpoints for PGP
public keys. It never loads remote content inside messages, so senders
cannot use tracking pixels to learn that you opened their mail.</p>

<h2>Permissions</h2>
<p>Internet access (to reach your mail server) and notifications (to alert
you to new mail). VayuMail does not request access to your camera, contacts,
location, or files beyond attachments you explicitly choose to send.</p>

<h2>Data retention and deletion</h2>
<p>Your mail resides on your own server; manage or delete it there. VayuMail
keeps only a local cache on your device, which is removed when you uninstall
the app or clear its storage. Because the developer holds none of your data,
there is nothing for us to delete on your behalf.</p>

<h2>Children</h2>
<p>VayuMail is a general-purpose email client and is not directed at
children. It collects no personal data from anyone.</p>

<h2>Changes to this policy</h2>
<p>If this policy changes, the updated version will be published at this same
address with a new effective date.</p>

<h2>Contact</h2>
<p>Questions about this policy: <a href="mailto:` + contact + `">` + contact + `</a>.</p>
</div>
</article>
</main>
</div>
</body></html>`
	_, _ = w.Write([]byte(page))
}
