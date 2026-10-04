// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_pictures.go — a message's pictures, loaded through this server
// when the reader asks for them, so the sender's server sees this server
// rather than the reader's address and device.
//
// Each picture's address is rewritten to /os/vayumail/picture with an HMAC of
// the original, made with a key that exists only in this process: the
// endpoint fetches only addresses this server itself put in a message it
// showed, so it is no open proxy. What comes back is served only when its
// bytes are a PNG, JPEG, GIF or WebP, whatever the sender's server claims:
// never SVG, which would run as a document of this origin.

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/johalputt/vayupress/internal/safefetch"
)

// mailPictureKey signs picture addresses. A restart makes a new one, so a
// page opened before it shows its pictures again only when reopened.
var mailPictureKey = func() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic("vayumail: no randomness for the picture key: " + err.Error())
	}
	return k
}()

// mailPictureFetch fetches a picture: the install's guarded client (public
// addresses only, size and time capped), which a test replaces.
var mailPictureFetch = safefetch.New(safefetch.Options{MaxBytes: 10 << 20, Timeout: 10 * time.Second}).Get

func mailPictureSig(u string) string {
	m := hmac.New(sha256.New, mailPictureKey)
	m.Write([]byte(u))
	return hex.EncodeToString(m.Sum(nil))
}

// mailPictureTypes are what is served, by what the bytes are.
var mailPictureTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// proxyImageSources points every remote picture in already sanitised HTML at
// the proxy, keeps a data: picture as it is, and drops every other source
// (srcset included, whose candidates would be fetched directly).
func proxyImageSources(sanitized string) string {
	ctx := &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := xhtml.ParseFragment(strings.NewReader(sanitized), ctx)
	if err != nil {
		return ""
	}
	var out bytes.Buffer
	for _, n := range nodes {
		proxyImgAttrs(n)
		if err := xhtml.Render(&out, n); err != nil {
			return ""
		}
	}
	return out.String()
}

func proxyImgAttrs(n *xhtml.Node) {
	if n.Type == xhtml.ElementNode && n.Data == "img" {
		keep := n.Attr[:0]
		for _, a := range n.Attr {
			switch strings.ToLower(a.Key) {
			case "src":
				src := strings.TrimSpace(a.Val)
				low := strings.ToLower(src)
				switch {
				case strings.HasPrefix(low, "https://"), strings.HasPrefix(low, "http://"):
					a.Val = "/os/vayumail/picture?u=" + url.QueryEscape(src) + "&s=" + mailPictureSig(src)
				case strings.HasPrefix(low, "data:image/"):
				default:
					continue
				}
			case "srcset", "background", "lowsrc", "dynsrc":
				continue
			}
			keep = append(keep, a)
		}
		n.Attr = keep
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		proxyImgAttrs(c)
	}
}

// handleVayuOSMailPicture fetches one signed picture and serves it.
func (a *App) handleVayuOSMailPicture(w http.ResponseWriter, r *http.Request) {
	u, sig := r.URL.Query().Get("u"), r.URL.Query().Get("s")
	if u == "" || !hmac.Equal([]byte(sig), []byte(mailPictureSig(u))) {
		http.NotFound(w, r)
		return
	}
	res, err := mailPictureFetch(r.Context(), u)
	if err != nil || res.Status != http.StatusOK {
		http.NotFound(w, r)
		return
	}
	ctype := http.DetectContentType(res.Body)
	if !mailPictureTypes[ctype] {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Length", strconv.Itoa(len(res.Body)))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(res.Body)
}
