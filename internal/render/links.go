// SPDX-License-Identifier: Apache-2.0

package render

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// nofollowElsewhere marks each link to another site rel=nofollow, in HTML
// the article policy has already sanitised. A link within the site (relative,
// or absolute to the site's own host or its www.) is left as it is: a
// nofollow there tells search engines to discount the site's own internal
// linking, which is how bluemonday's preset, marking every link, cost every
// cross-link on johal.in.
func nofollowElsewhere(safe, host string) string {
	if !strings.Contains(safe, "<a ") {
		return safe
	}
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	var out strings.Builder
	z := html.NewTokenizer(strings.NewReader(safe))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		raw := string(z.Raw())
		if tt != html.StartTagToken {
			out.WriteString(raw)
			continue
		}
		tok := z.Token()
		if tok.DataAtom != atom.A {
			out.WriteString(raw)
			continue
		}
		href := ""
		for _, at := range tok.Attr {
			if at.Key == "href" {
				href = at.Val
			}
		}
		u, err := url.Parse(href)
		if err != nil || u.Host == "" || strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") == host {
			out.WriteString(raw)
			continue
		}
		tok.Attr = append(tok.Attr, html.Attribute{Key: "rel", Val: "nofollow"})
		out.WriteString(tok.String())
	}
	return out.String()
}
