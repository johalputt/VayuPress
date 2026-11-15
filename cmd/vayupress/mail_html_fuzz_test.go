// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// fetchingAttrs are the attributes that make a browser fetch a URL by
// themselves, with no click: what a tracker in a message rides on.
var fetchingAttrs = map[string]bool{
	"src": true, "srcset": true, "background": true, "poster": true, "data": true,
	"lowsrc": true, "dynsrc": true, "longdesc": true, "action": true, "formaction": true,
}

// The HTML of a message comes from whoever sent it. With pictures off, the
// reader's copy of it fetches nothing and runs nothing: no element that
// executes or embeds, no event attribute, no style, and no attribute that
// loads a URL. The output is read again by an HTML parser here, as the
// browser will read it, not judged by the sanitiser that produced it.
func FuzzMailHTMLNoImages(f *testing.F) {
	for _, s := range []string{
		`<p>Hello <b>there</b> <a href="https://example.org/">a link</a></p>`,
		`<img src="https://t.example/pixel.gif" alt="logo" width="1" height="1"><img srcset="https://t.example/a.png 2x">`,
		`<table background="https://t.example/bg.png"><tr><td style="background:url(https://t.example/x)">x</td></tr></table>`,
		`<script>alert(1)</script><svg onload="x()"><image href="https://t.example/i"/></svg><iframe src="https://e.example/"></iframe>`,
		`<video poster="https://t.example/p.jpg"><source src="https://t.example/v.mp4"></video><object data="https://t.example/o"></object>`,
		`<link rel="stylesheet" href="https://t.example/s.css"><style>@import url(https://t.example/i.css)</style><form action="https://e.example/"><button formaction="https://e.example/">x</button></form>`,
		`<a href="javascript:alert(1)">x</a><img src=x onerror=alert(1)><math><mtext><img src=x></mtext></math>`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		out := mailHTMLNoImages(raw)
		ctx := &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}
		nodes, err := xhtml.ParseFragment(strings.NewReader(out), ctx)
		if err != nil {
			t.Fatalf("the reader's copy does not parse: %v", err)
		}
		var walk func(n *xhtml.Node)
		walk = func(n *xhtml.Node) {
			if n.Type == xhtml.ElementNode {
				switch n.Data {
				case "script", "style", "iframe", "frame", "object", "embed", "link", "meta", "base", "form", "video", "audio", "source", "svg", "math", "image":
					t.Fatalf("<%s> got through:\n%s", n.Data, out)
				}
				for _, a := range n.Attr {
					key := strings.ToLower(a.Key)
					if strings.HasPrefix(key, "on") || key == "style" {
						t.Fatalf("%s on <%s> got through:\n%s", a.Key, n.Data, out)
					}
					if fetchingAttrs[key] {
						t.Fatalf("%s on <%s> would fetch %q with pictures off", a.Key, n.Data, a.Val)
					}
					if key == "href" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Val)), "javascript:") {
						t.Fatalf("a javascript: link got through: %q", a.Val)
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		for _, n := range nodes {
			walk(n)
		}
	})
}
