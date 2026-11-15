// SPDX-License-Identifier: Apache-2.0

package main

import "github.com/johalputt/vayupress/internal/i18n"

// saWord is one of the app registry's labels (vayuos_shell.go) in lang. The
// registry stays in English, which is what its tests, the command palette's
// keys and every href are written against; a label is translated only as it
// is drawn. Each case hands i18n.T a literal, so the catalogue test sees every
// label. Product names (VayuMCP, YouTube, Shield…) and acronyms are not
// translated and fall through.
func saWord(lang, label string) string {
	switch label {
	case "API keys":
		return i18n.T(lang, "API keys")
	case "Account":
		return i18n.T(lang, "Account")
	case "Accounts":
		return i18n.T(lang, "Accounts")
	case "Addresses":
		return i18n.T(lang, "Addresses")
	case "Administration":
		return i18n.T(lang, "Administration")
	case "Advertising":
		return i18n.T(lang, "Advertising")
	case "Analytics":
		return i18n.T(lang, "Analytics")
	case "Audience":
		return i18n.T(lang, "Audience")
	case "Backups":
		return i18n.T(lang, "Backups")
	case "Bot protection":
		return i18n.T(lang, "Bot protection")
	case "Comments":
		return i18n.T(lang, "Comments")
	case "Compose":
		return i18n.T(lang, "Compose")
	case "Connect a device":
		return i18n.T(lang, "Connect a device")
	case "Content":
		return i18n.T(lang, "Content")
	case "Decisions":
		return i18n.T(lang, "Decisions")
	case "Diagnostics":
		return i18n.T(lang, "Diagnostics")
	case "Domains":
		return i18n.T(lang, "Domains")
	case "Every site's analytics":
		return i18n.T(lang, "Every site's analytics")
	case "Explorer":
		return i18n.T(lang, "Explorer")
	case "Faults":
		return i18n.T(lang, "Faults")
	case "Governance":
		return i18n.T(lang, "Governance")
	case "Home":
		return i18n.T(lang, "Home")
	case "Integrations":
		return i18n.T(lang, "Integrations")
	case "Mail":
		return i18n.T(lang, "Mail")
	case "Mailbox":
		return i18n.T(lang, "Mailbox")
	case "Mailbox settings":
		return i18n.T(lang, "Mailbox settings")
	case "Media":
		return i18n.T(lang, "Media")
	case "Members":
		return i18n.T(lang, "Members")
	case "Messages":
		return i18n.T(lang, "Messages")
	case "Monetization":
		return i18n.T(lang, "Monetization")
	case "Monitoring":
		return i18n.T(lang, "Monitoring")
	case "My profile":
		return i18n.T(lang, "My profile")
	case "My site":
		return i18n.T(lang, "My site")
	case "New post":
		return i18n.T(lang, "New post")
	case "Newsletter":
		return i18n.T(lang, "Newsletter")
	case "Outbox":
		return i18n.T(lang, "Outbox")
	case "Outside services":
		return i18n.T(lang, "Outside services")
	case "Overview":
		return i18n.T(lang, "Overview")
	case "PGP keys":
		return i18n.T(lang, "PGP keys")
	case "Pages":
		return i18n.T(lang, "Pages")
	case "Posts":
		return i18n.T(lang, "Posts")
	case "Power":
		return i18n.T(lang, "Power")
	case "Profile":
		return i18n.T(lang, "Profile")
	case "Replay":
		return i18n.T(lang, "Replay")
	case "Revenue":
		return i18n.T(lang, "Revenue")
	case "Security":
		return i18n.T(lang, "Security")
	case "Settings":
		return i18n.T(lang, "Settings")
	case "Sign-in attempts":
		return i18n.T(lang, "Sign-in attempts")
	case "Sign-in security":
		return i18n.T(lang, "Sign-in security")
	case "Site":
		return i18n.T(lang, "Site")
	case "Storage":
		return i18n.T(lang, "Storage")
	case "System":
		return i18n.T(lang, "System")
	case "System state":
		return i18n.T(lang, "System state")
	case "Theme":
		return i18n.T(lang, "Theme")
	case "Theme store":
		return i18n.T(lang, "Theme store")
	case "Tools and plugins":
		return i18n.T(lang, "Tools and plugins")
	case "Topology":
		return i18n.T(lang, "Topology")
	case "Updates":
		return i18n.T(lang, "Updates")
	case "Visitors":
		return i18n.T(lang, "Visitors")
	case "Website":
		return i18n.T(lang, "Website")
	case "Worlds":
		return i18n.T(lang, "Worlds")
	}
	return label
}
