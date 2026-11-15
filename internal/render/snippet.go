// SPDX-License-Identifier: Apache-2.0

package render

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// MaxSnippetBytes bounds one snippet: a tag manager's loader and its
// configuration are a few hundred bytes, and every public page carries it.
const MaxSnippetBytes = 16 << 10

// Snippet is a site's own code for its public pages, read as the browser
// will read it.
type Snippet struct {
	// HTML is the snippet as it goes into the page, its line breaks made
	// "\n" as a browser makes them before it hashes a script.
	HTML string
	// Hashes allow each inline script, and nothing else, in script-src.
	Hashes []string
	// ScriptOrigins are the origins its <script src> load from, which the
	// site's policy must allow for them to run.
	ScriptOrigins []string
}

// ParseSnippet reads what an operator typed for a site's head or footer.
// An inline script is allowed by the hash of its exact text, which is why a
// snippet may hold no inline event attribute and no javascript: address:
// neither can be allowed by a hash without 'unsafe-hashes', which would
// allow that text wherever an injection could put it.
func ParseSnippet(raw string) (Snippet, error) {
	s := strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	if strings.TrimSpace(s) == "" {
		return Snippet{}, nil
	}
	if len(s) > MaxSnippetBytes {
		return Snippet{}, errors.New("a snippet is at most 16 KB")
	}
	out := Snippet{HTML: s}
	seen := map[string]bool{}
	z := html.NewTokenizer(strings.NewReader(s))
	inScript, scriptHasSrc := false, false
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if errors.Is(z.Err(), io.EOF) {
				if inScript {
					return Snippet{}, errors.New("a <script> is not closed")
				}
				return out, nil
			}
			return Snippet{}, errors.New("the snippet cannot be read as HTML")
		case html.TextToken:
			if inScript && !scriptHasSrc {
				sum := sha256.Sum256(z.Raw())
				h := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
				if !seen[h] {
					seen[h] = true
					out.Hashes = append(out.Hashes, h)
				}
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); string(name) == "script" {
				inScript = false
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			attrs := map[string]string{}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				key := strings.ToLower(string(k))
				attrs[key] = string(v)
				if strings.HasPrefix(key, "on") {
					return Snippet{}, errors.New("an inline handler (" + key + "=) cannot be allowed by a hash: attach it from a script instead")
				}
				switch key {
				case "href", "src", "action", "formaction", "xlink:href", "data":
					if scheme := strings.ToLower(strings.TrimSpace(string(v))); strings.HasPrefix(scheme, "javascript:") || strings.HasPrefix(scheme, "vbscript:") {
						return Snippet{}, errors.New("a javascript: address cannot be allowed: load the code from a script instead")
					}
				}
			}
			switch tag {
			case "base":
				return Snippet{}, errors.New("a <base> would change where every link on the page goes")
			case "meta":
				if _, ok := attrs["http-equiv"]; ok {
					return Snippet{}, errors.New("a <meta http-equiv> would change how the page is served, which the site's settings decide")
				}
			case "script":
				src, has := attrs["src"]
				inScript, scriptHasSrc = tt == html.StartTagToken, has
				if has {
					origin, err := scriptOrigin(src)
					if err != nil {
						return Snippet{}, err
					}
					if origin != "" && !seen[origin] {
						seen[origin] = true
						out.ScriptOrigins = append(out.ScriptOrigins, origin)
					}
				}
			}
		}
	}
}

// scriptOrigin is the origin a <script src> loads from: "" for the site's
// own, which 'self' already allows.
func scriptOrigin(src string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(src))
	if err != nil {
		return "", errors.New("a script's address cannot be read: " + src)
	}
	if u.Scheme == "" && u.Host == "" {
		return "", nil
	}
	if !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return "", errors.New("a script loads only over https: " + src)
	}
	return "https://" + strings.ToLower(u.Host), nil
}
