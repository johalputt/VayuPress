// SPDX-License-Identifier: Apache-2.0

package render

import (
	"html/template"
	"regexp"
	"strconv"
	"strings"
)

// halcyon_members.go — the member pages under Halcyon.
//
// Sign-in, sign-up, the account, plans and pricing, the paywall, checkout and
// the author's page are hand-built in cmd/vayupress and share signup.css.
// Under Halcyon they keep that markup and stylesheet, which lay every
// component out, and take Halcyon's design over it: the theme's stylesheets
// after signup.css, the reader's saved appearance before paint, and a header
// (the focused one on a task page, the site's on the account and the author's
// page). Under any other theme the page is returned exactly as it was built.

// MemberPage says which header a member page takes.
type MemberPage int

const (
	// MemberTask is a page with one thing to do (sign in, sign up, pay, read
	// past a paywall): the focused header, one way forward and one way out.
	MemberTask MemberPage = iota
	// MemberSite is a page a reader browses (their account, an author): the
	// site's own header and footer.
	MemberSite
)

var (
	signupCSSLinkRe = regexp.MustCompile(`<link rel="stylesheet" href="/static/css/signup\.css\?v=[^"]*">`)
	// The paywall also links the shared templates' article.css, whose rules
	// for the whole page (body, headings, links) would fight Halcyon's.
	articleCSSLinkRe = regexp.MustCompile(`<link rel="stylesheet" href="/static/css/article\.css\?v=[^"]*">`)
	bodyOpenRe       = regexp.MustCompile(`<body[^>]*>`)
)

// memberPictographs are the emoji the member pages set in their headings and
// notices. Halcyon sets none: the words stay and the pictographs go. Each is
// matched with its neighbouring space, so a sentence keeps its spacing.
var memberPictographs = strings.NewReplacer(
	`<span class="ma-col-tag ma-col-tag--paid">✦</span>`, ``,
	"✦ ", "", "📮 ", "", "🔐 ", "", "🔔 ", "", "🎉 ", "", " 🎉", "", " 🪙", "", "💬 ", "", "📰 ", "",
)

// HalcyonMemberPage returns page in Halcyon's design when Halcyon is the
// active theme, and page unchanged otherwise.
func HalcyonMemberPage(page string, kind MemberPage) string {
	cfg, ok := Halcyon()
	if !ok {
		return page
	}
	c := halcyonChrome(getActiveSettings(), cfg, "member")
	open := `<html lang="en" class="h" data-face="` + cfg.Face + `" data-size="` + strconv.Itoa(cfg.TextSize) +
		`" data-site-face="` + cfg.Face + `" data-site-size="` + strconv.Itoa(cfg.TextSize) + `" data-appearance="` + cfg.Appearance + `"`
	if cfg.Appearance == "light" || cfg.Appearance == "dark" {
		open += ` data-theme="` + cfg.Appearance + `"`
	}
	page = strings.Replace(page, `<html lang="en">`, open+">", 1)

	page = articleCSSLinkRe.ReplaceAllString(page, "")
	head := HalcyonMembersHead() + `<script defer src="` + c.JSHref + `"></script>`
	if loc := signupCSSLinkRe.FindStringIndex(page); loc != nil {
		page = page[:loc[1]] + head + page[loc[1]:]
	} else {
		page = strings.Replace(page, `</head>`, head+`</head>`, 1)
	}

	var top, bottom string
	if kind == MemberSite {
		var b strings.Builder
		if err := halcyonTmpl["home"].ExecuteTemplate(&b, "bar", c); err == nil {
			top = b.String()
		}
		b.Reset()
		if err := halcyonTmpl["home"].ExecuteTemplate(&b, "end", c); err == nil {
			bottom = strings.TrimSuffix(strings.TrimSpace(b.String()), "</body></html>")
		}
	} else {
		top = `<header class="h-focus"><a class="h-name" href="` + template.HTMLEscapeString(BlogBase()) + `">` +
			template.HTMLEscapeString(c.SiteName) + `</a><a class="h-back" href="` + template.HTMLEscapeString(BlogBase()) +
			`">Back to the site</a></header>`
	}
	if loc := bodyOpenRe.FindStringIndex(page); loc != nil {
		page = page[:loc[1]] + top + page[loc[1]:]
	}
	if bottom != "" {
		if i := strings.LastIndex(page, "</body>"); i >= 0 {
			page = page[:i] + bottom + page[i:]
		}
	}
	return memberPictographs.Replace(page)
}
