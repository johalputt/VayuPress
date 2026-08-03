// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_feedback.go — the VayuOS "Report a bug / suggest an improvement"
// affordance. A single topbar button (rendered in adminOSShellHead, so it shows
// on every page in BOTH the clearnet and the Tor console) links to the VayuMail
// composer in feedback mode: recipient pre-filled to the feedback inbox, a
// structured template in the body, and PGP encryption pre-enabled. The operator
// can attach screenshots or files just like any other message.
//
// The recipient defaults to feedback@<domain> so it works the moment that
// mailbox exists; operators can point it elsewhere from Power & Maintenance
// (setting vayupress.feedback_email).

import (
	"context"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/settings"
)

// defaultFeedbackEmail is where bug reports / improvement ideas / feature
// requests go by default: the VayuPress project inbox. Operators can override it
// per-install (Power & Maintenance → Feedback inbox) to collect reports
// themselves, but out of the box every install reaches the team.
const defaultFeedbackEmail = "feedback@vayupress.com"

// feedbackEmail resolves the mailbox the feedback button composes to: the
// operator-configured address if set, otherwise the VayuPress project inbox.
func (a *App) feedbackEmail(ctx context.Context) string {
	if a.siteSettings != nil {
		if v := strings.TrimSpace(a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyFeedbackEmail)); v != "" {
			return v
		}
	}
	return defaultFeedbackEmail
}

// feedbackSubject / feedbackBody are the prefill for a feedback message. The
// body is a light, friendly template that nudges the reporter to say whether
// it's a bug, an improvement or a feature request, and to include the context
// that makes a report actionable — with the install's version and world stamped
// in automatically.
const feedbackSubject = "VayuPress feedback"

// feedbackBody builds the composer body template, stamping in the running
// version and world so every report carries the context we'd otherwise have to
// ask for.
func feedbackBody() string {
	world := "Clearnet"
	if config.Cfg.OnionMode {
		world = "Tor"
	}
	return "Hi VayuPress team,\n\n" +
		"Type (keep one): Bug · Improvement · Feature request\n\n" +
		"What I'd like to report or suggest:\n\n\n" +
		"Steps to reproduce (for a bug):\n1. \n2. \n3. \n\n" +
		"What I expected:\n\n\n" +
		"What actually happened:\n\n\n" +
		"You can attach screenshots or files below.\n\n" +
		"— Sent from VayuOS v" + Version + " · " + world + " Space · " + config.Cfg.Domain + "\n"
}
