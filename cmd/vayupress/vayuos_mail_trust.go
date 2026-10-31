// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_trust.go — the reader's trust line: one quiet line under the
// sender that says what is known about where a message came from, and an
// amber warning when a link or a name pretends to be someone else. Each part
// appears only when it applies. Nothing here calls out: the domain's check
// was sealed at delivery (internal/vayuos/mail/verdict.go), and the rest is
// read from the message itself.

import (
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/publicsuffix"
)

// isTrackerImg reports a picture that exists only to be fetched: a remote
// source drawn at most 2 pixels wide or high. It is never fetched, even when
// the holder loads pictures, and the trust line counts it.
func isTrackerImg(n *html.Node) bool {
	if n.Type != html.ElementNode || n.Data != "img" {
		return false
	}
	remote, tiny := false, false
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "src":
			low := strings.ToLower(strings.TrimSpace(a.Val))
			remote = strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://")
		case "width", "height":
			if px, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(a.Val), "px")); err == nil && px <= 2 {
				tiny = true
			}
		}
	}
	return remote && tiny
}

// mailTrust is what the trust line says about one message.
type mailTrust struct {
	Checked  string // the From domain that confirmed it sent the message
	Trackers int
	Warning  string // the amber line, "" when nothing pretends
}

// mailTrustFor reads sanitized, the message's HTML as the policy that keeps
// pictures leaves it, for trackers and for links whose words name one site
// and whose target is another; and the From header for a name that is an
// address other than the sender's. stranger says the sender is not among
// the holder's contacts, which the warning then says first.
func mailTrustFor(checked, sanitized, from string, stranger bool) mailTrust {
	t := mailTrust{Checked: checked}
	var says, goes string
	if sanitized != "" {
		ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
		if nodes, err := html.ParseFragment(strings.NewReader(sanitized), ctx); err == nil {
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if isTrackerImg(n) {
					t.Trackers++
				}
				if says == "" && n.Type == html.ElementNode && n.Data == "a" {
					says, goes = linkPretends(n)
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
			for _, n := range nodes {
				walk(n)
			}
		}
	}
	var warn string
	switch {
	case says != "":
		warn = "A link says " + says + " but goes to " + goes + "."
	default:
		if name, addr := namePretends(from); name != "" {
			warn = "The name says " + name + " but the mail comes from " + addr + "."
		}
	}
	if warn != "" && stranger {
		warn = "This sender is not in your contacts. " + warn
	}
	t.Warning = warn
	return t
}

// A link's words read as a web address: an optional scheme, a host with a
// dot, and an optional path. Words like "Read more" are not addresses and
// are never compared.
var linkWordsAddr = regexp.MustCompile(`(?i)^(https?://)?([a-z0-9-]+\.)+[a-z]{2,}(:\d+)?(/\S*)?$`)

// linkPretends gives the site a link's words name and the site it goes to
// when they are not the same site; "" when they agree or the words are not
// an address.
func linkPretends(a *html.Node) (says, goes string) {
	href := ""
	for _, at := range a.Attr {
		if strings.EqualFold(at.Key, "href") {
			href = at.Val
		}
	}
	words := strings.TrimSpace(nodeText(a))
	if href == "" || !linkWordsAddr.MatchString(words) {
		return "", ""
	}
	if !strings.Contains(words, "://") {
		words = "https://" + words
	}
	shown, err1 := url.Parse(words)
	target, err2 := url.Parse(strings.TrimSpace(href))
	if err1 != nil || err2 != nil || target.Hostname() == "" {
		return "", ""
	}
	s, g := siteOf(shown.Hostname()), siteOf(target.Hostname())
	if s == "" || g == "" || s == g {
		return "", ""
	}
	return s, g
}

// siteOf is a host's registrable domain (mail.example.co.uk is
// example.co.uk), so a link to another page of the same site never warns.
func siteOf(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if site, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return site
	}
	return host
}

// namePretends gives the address a From header's display name holds and
// the real address, when the name is an address on another site.
func namePretends(from string) (name, addr string) {
	a, err := mail.ParseAddress(from)
	if err != nil || !strings.Contains(a.Name, "@") {
		return "", ""
	}
	shown, err := mail.ParseAddress(strings.Trim(strings.TrimSpace(a.Name), "<>\"'"))
	if err != nil {
		return "", ""
	}
	if siteOf(domainPart(shown.Address)) == siteOf(domainPart(a.Address)) {
		return "", ""
	}
	return shown.Address, strings.ToLower(a.Address)
}

func domainPart(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return addr[i+1:]
	}
	return ""
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// mailTrustLine is the trust line and the warning, as the reader shows them.
func mailTrustLine(t mailTrust) string {
	var parts []string
	if t.Checked != "" {
		parts = append(parts, `<span class="mx-trust__item">`+saIcon("check-c")+esc(t.Checked)+` confirmed it sent this</span>`)
	}
	if t.Trackers > 0 {
		n := strconv.Itoa(t.Trackers) + " tracker"
		if t.Trackers > 1 {
			n += "s"
		}
		parts = append(parts, `<span class="mx-trust__item">`+saIcon("shield")+n+` blocked</span>`)
	}
	out := ""
	if len(parts) > 0 {
		out = `<p class="mx-trust">` + strings.Join(parts, "") + `</p>`
	}
	if t.Warning != "" {
		out += `<p class="mx-trust__warn" role="alert">` + saIcon("warn") + `<span>` + esc(t.Warning) + `</span></p>`
	}
	return out
}
