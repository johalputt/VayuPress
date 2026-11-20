// SPDX-License-Identifier: Apache-2.0

package main

import "github.com/johalputt/vayupress/internal/i18n"

// settingsWord is one of the Settings registry's words (settings_app.go: a
// category's name and line, a band's title and hint, a row's label, hint and
// placeholder) in lang. The registry stays in English, which its tests, the
// settings keys and every href are written against; a word is translated as
// it is drawn. Each case hands i18n.T a literal, so the catalogue test sees
// every one. An address shown as an example is not a word and falls through.
func settingsWord(lang, s string) string {
	switch s {
	case "Site identity":
		return i18n.T(lang, "Site identity")
	case "What your site is called, what it is about, and whose it is.":
		return i18n.T(lang, "What your site is called, what it is about, and whose it is.")
	case "Identity":
		return i18n.T(lang, "Identity")
	case "Site name":
		return i18n.T(lang, "Site name")
	case "Shown in the header, the tab and every email.":
		return i18n.T(lang, "Shown in the header, the tab and every email.")
	case "My Publication":
		return i18n.T(lang, "My Publication")
	case "Tagline":
		return i18n.T(lang, "Tagline")
	case "One line under the name.":
		return i18n.T(lang, "One line under the name.")
	case "A short description":
		return i18n.T(lang, "A short description")
	case "Description":
		return i18n.T(lang, "Description")
	case "Used in feeds, the sitemap and search results.":
		return i18n.T(lang, "Used in feeds, the sitemap and search results.")
	case "Author":
		return i18n.T(lang, "Author")
	case "The byline when a post names nobody else.":
		return i18n.T(lang, "The byline when a post names nobody else.")
	case "Your name":
		return i18n.T(lang, "Your name")
	case "Dates and times":
		return i18n.T(lang, "Dates and times")
	case "Time zone":
		return i18n.T(lang, "Time zone")
	case "Everything is stored in UTC; this changes only how times read.":
		return i18n.T(lang, "Everything is stored in UTC; this changes only how times read.")
	case "Appearance":
		return i18n.T(lang, "Appearance")
	case "How the console looks to you. Your site’s own design lives in Site › Theme.":
		return i18n.T(lang, "How the console looks to you. Your site’s own design lives in Site › Theme.")
	case "Console":
		return i18n.T(lang, "Console")
	case "Colour scheme":
		return i18n.T(lang, "Colour scheme")
	case "Match system follows your device.":
		return i18n.T(lang, "Match system follows your device.")
	case "Navigation":
		return i18n.T(lang, "Navigation")
	case "The links across the top of every public page.":
		return i18n.T(lang, "The links across the top of every public page.")
	case "Public menu":
		return i18n.T(lang, "Public menu")
	case "Empty restores Home, Feed and Console":
		return i18n.T(lang, "Empty restores Home, Feed and Console")
	case "Footer":
		return i18n.T(lang, "Footer")
	case "What sits at the bottom of every public page.":
		return i18n.T(lang, "What sits at the bottom of every public page.")
	case "Branding":
		return i18n.T(lang, "Branding")
	case "Your site’s mark and colours. Layout, type and the rest are edited live in Site › Theme.":
		return i18n.T(lang, "Your site’s mark and colours. Layout, type and the rest are edited live in Site › Theme.")
	case "Mark":
		return i18n.T(lang, "Mark")
	case "Colours":
		return i18n.T(lang, "Colours")
	case "Primary colour, light":
		return i18n.T(lang, "Primary colour, light")
	case "Links and buttons on the public site in light mode.":
		return i18n.T(lang, "Links and buttons on the public site in light mode.")
	case "Primary colour, dark":
		return i18n.T(lang, "Primary colour, dark")
	case "The same, in dark mode.":
		return i18n.T(lang, "The same, in dark mode.")
	case "Custom CSS":
		return i18n.T(lang, "Custom CSS")
	case "Stylesheet":
		return i18n.T(lang, "Stylesheet")
	case "Added to every public page. Never loaded in the console.":
		return i18n.T(lang, "Added to every public page. Never loaded in the console.")
	case "Writing":
		return i18n.T(lang, "Writing")
	case "What happens around what you publish.":
		return i18n.T(lang, "What happens around what you publish.")
	case "Contact form":
		return i18n.T(lang, "Contact form")
	case "On any page made from the Contact template":
		return i18n.T(lang, "On any page made from the Contact template")
	case "Send messages to":
		return i18n.T(lang, "Send messages to")
	case "Each message from a page's contact form is emailed here through VayuMail. To add the form to a page, type [[contact-form]] in it; [[contact-form: your thank-you]] sets that page's own confirmation.":
		return i18n.T(lang, "Each message from a page's contact form is emailed here through VayuMail. To add the form to a page, type [[contact-form]] in it; [[contact-form: your thank-you]] sets that page's own confirmation.")
	case "Reply to the sender":
		return i18n.T(lang, "Reply to the sender")
	case "Sends the visitor a note that their message arrived.":
		return i18n.T(lang, "Sends the visitor a note that their message arrived.")
	case "Links":
		return i18n.T(lang, "Links")
	case "In every post and every comment":
		return i18n.T(lang, "In every post and every comment")
	case "Ask search engines not to follow links to other sites":
		return i18n.T(lang, "Ask search engines not to follow links to other sites")
	case "On, each link to another site carries rel=\"nofollow\" (in a comment, rel=\"ugc nofollow\"). Off, search engines follow them as links you chose. A relative link, or one to this install’s main address, is never marked. Saved as you switch it.":
		return i18n.T(lang, "On, each link to another site carries rel=\"nofollow\" (in a comment, rel=\"ugc nofollow\"). Off, search engines follow them as links you chose. A relative link, or one to this install’s main address, is never marked. Saved as you switch it.")
	case "Members":
		return i18n.T(lang, "Members")
	case "How readers join. Plans and payments are in Audience › Monetization.":
		return i18n.T(lang, "How readers join. Plans and payments are in Audience › Monetization.")
	case "Sign-up":
		return i18n.T(lang, "Sign-up")
	case "Sign in and Sign up buttons":
		return i18n.T(lang, "Sign in and Sign up buttons")
	case "Shown in the public site’s menu.":
		return i18n.T(lang, "Shown in the public site’s menu.")
	case "Team":
		return i18n.T(lang, "Team")
	case "Email delivery":
		return i18n.T(lang, "Email delivery")
	case "The server your site’s own email goes out through: sign-in links, receipts, newsletters.":
		return i18n.T(lang, "The server your site’s own email goes out through: sign-in links, receipts, newsletters.")
	case "Outgoing mail":
		return i18n.T(lang, "Outgoing mail")
	case "Set in the environment":
		return i18n.T(lang, "Set in the environment")
	case "Advanced":
		return i18n.T(lang, "Advanced")
	case "Install":
		return i18n.T(lang, "Install")
	case "Where this install keeps its working files.":
		return i18n.T(lang, "Where this install keeps its working files.")
	case "Page cache":
		return i18n.T(lang, "Page cache")
	case "Home, posts and topics":
		return i18n.T(lang, "Home, posts and topics")
	case "Keep rendered pages":
		return i18n.T(lang, "Keep rendered pages")
	case "On, each page is drawn once and served from the cache until its content changes. Off, every visit draws its page: use it to check a change as it is drawn, then turn it back on, since pages are slower and under heavy traffic some visitors are asked to try again. Saved as you switch it.":
		return i18n.T(lang, "On, each page is drawn once and served from the cache until its content changes. Off, every visit draws its page: use it to check a change as it is drawn, then turn it back on, since pages are slower and under heavy traffic some visitors are asked to try again. Saved as you switch it.")
	case "Visitor cities":
		return i18n.T(lang, "Visitor cities")
	case "For Analytics":
		return i18n.T(lang, "For Analytics")
	case "Files":
		return i18n.T(lang, "Files")
	}
	return s
}
