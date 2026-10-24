// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// liftCodeBlocks swaps each code block for a placeholder and returns its
// highlighted HTML under that placeholder (see renderContentHTML). A code
// block is a <pre> whose one child is a <code> holding only text, found by
// parsing the post as a browser would. It was found by a pattern once, which
// also matched "<pre><code>" written inside an attribute's value and put
// chroma's markup back there; nothing was found to inject through it, but a
// parsed element cannot be inside an attribute at all. The language is the
// class "language-LANG" or "lang-LANG" on the <code>, which the sanitiser
// would strip, which is why this runs before it.
func liftCodeBlocks(raw, nonce string) (string, map[string]string) {
	if !strings.Contains(strings.ToLower(raw), "<pre") {
		return raw, nil
	}
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(raw), body)
	if err != nil {
		return raw, nil
	}
	highlighted := map[string]string{}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if lang, code, ok := codeBlock(c); ok {
				if out, ok := highlightOne(lang, code); ok {
					token := "VAYUCODE" + nonce + strconv.Itoa(len(highlighted)) + "ENDVAYUCODE"
					highlighted[token] = out
					n.InsertBefore(&html.Node{Type: html.TextNode, Data: token}, c)
					n.RemoveChild(c)
				}
			} else {
				walk(c)
			}
			c = next
		}
	}
	var out strings.Builder
	for _, n := range nodes {
		holder := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
		holder.AppendChild(n)
		walk(holder)
		for c := holder.FirstChild; c != nil; c = c.NextSibling {
			if err := html.Render(&out, c); err != nil {
				return raw, nil
			}
		}
	}
	return out.String(), highlighted
}

// codeBlock reports whether n is a code block (see liftCodeBlocks), with its
// language and its text as written.
func codeBlock(n *html.Node) (lang, code string, ok bool) {
	if n.Type != html.ElementNode || n.DataAtom != atom.Pre {
		return "", "", false
	}
	c := n.FirstChild
	if c == nil || c.NextSibling != nil || c.Type != html.ElementNode || c.DataAtom != atom.Code {
		return "", "", false
	}
	var b strings.Builder
	for t := c.FirstChild; t != nil; t = t.NextSibling {
		if t.Type != html.TextNode {
			return "", "", false
		}
		b.WriteString(t.Data)
	}
	for _, a := range c.Attr {
		if a.Key == "class" {
			for _, prefix := range []string{"language-", "lang-"} {
				if strings.HasPrefix(a.Val, prefix) {
					lang = a.Val[len(prefix):]
				}
			}
		}
	}
	return lang, b.String(), true
}
