// SPDX-License-Identifier: Apache-2.0

package analytics

// links.go — a reader following a link off the site, or fetching a file.
//
// The counting script reports both from the link the reader clicked, each as
// an event of its own type, so neither is a custom event: a site's
// conversions and its "Custom events" stay what the site tracks, and a click
// out is not a goal reached. Both are events of the visit, so a visit that
// ended in one is not a bounce: the reader did something.
//
// What is kept is where the link led, as a host and a path. The script sends
// no query string, and the server drops any it is given, with the fragment,
// the port and any user name: a link's query is where a token or an address
// is carried.

import (
	"net/url"
	"strings"
)

// The event types the counting script reports for links: 1 is a page view,
// 2 a custom event, 3 a search.
const (
	eventOutbound = 4
	eventDownload = 5
)

// linkTarget is where a reported link led, as kept: "host/path" for a link
// off the site, and for a file on another host; the path alone for a file on
// the page's own host. ok is false for anything that is not an http(s) link.
func linkTarget(raw, pageHost string, download bool) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	path := NormalizePath(u.EscapedPath())
	if download && strings.EqualFold(host, strings.TrimSpace(pageHost)) {
		return trunc(path, 200), true
	}
	return trunc(host+path, 200), true
}
