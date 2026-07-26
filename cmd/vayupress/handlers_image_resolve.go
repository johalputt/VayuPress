// SPDX-License-Identifier: Apache-2.0

package main

// handlers_image_resolve.go — turn third-party image *page* links into the direct
// image they advertise, so a pasted Pixabay/Unsplash URL renders in a post.
//
// Storage-neutral by design: we NEVER download or re-host the image (the operator
// asked to keep hotlinks, not copies). We only fetch the target page's HTML once
// (through the SSRF-safe htmlFetcher) and read its og:image URL — then store that
// direct URL. A link that is already a direct image, site-relative, or has no
// og:image is left untouched. The public templates render these external URLs
// with referrerpolicy="no-referrer" so they load past simple hotlink protection.

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"strings"

	"github.com/johalputt/vayupress/internal/seo"
)

// maxImageResolvePerSave bounds how many external page links one save may resolve,
// so a document full of page URLs cannot fan out into unbounded outbound fetches.
const maxImageResolvePerSave = 8

// directImageExts are URL path suffixes we treat as an already-direct image link
// (no HTML fetch needed).
var directImageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".avif": true, ".svg": true, ".bmp": true, ".ico": true, ".jfif": true,
}

// pageHTMLFetch fetches a target page's HTML for og:image extraction. It is a var
// so tests can substitute a fetcher that reaches a loopback test server — the
// production htmlFetcher deliberately blocks private/reserved IPs (SSRF safety).
var pageHTMLFetch = func(ctx context.Context, rawURL string) ([]byte, bool) {
	res, err := htmlFetcher.Get(ctx, rawURL)
	if err != nil || res == nil {
		return nil, false
	}
	return res.Body, true
}

// looksLikeDirectImage reports whether rawURL already points straight at an image
// file (by path extension), so resolution can be skipped.
func looksLikeDirectImage(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	return directImageExts[strings.ToLower(path.Ext(u.Path))]
}

// resolveImageLink turns a third-party *page* URL (e.g. a Pixabay/Unsplash photo
// page) into the direct image URL it advertises via og:image — without downloading
// the image bytes. A URL that is already a direct image, site-relative, a data URI,
// non-http(s), or has no discoverable og:image is returned unchanged.
func (a *App) resolveImageLink(ctx context.Context, rawURL string) string {
	u := strings.TrimSpace(rawURL)
	if u == "" || strings.HasPrefix(u, "/") || strings.HasPrefix(strings.ToLower(u), "data:") {
		return rawURL
	}
	lu := strings.ToLower(u)
	if !strings.HasPrefix(lu, "http://") && !strings.HasPrefix(lu, "https://") {
		return rawURL
	}
	if looksLikeDirectImage(u) {
		return rawURL
	}
	body, ok := pageHTMLFetch(ctx, u)
	if !ok || len(body) == 0 {
		return rawURL
	}
	img := strings.TrimSpace(parseOGTags(string(body))["image"])
	if img == "" {
		return rawURL
	}
	// Resolve a protocol-relative or relative og:image against the page URL.
	if strings.HasPrefix(img, "//") {
		img = "https:" + img
	} else if base, perr := url.Parse(u); perr == nil {
		if ref, rerr := url.Parse(img); rerr == nil {
			img = base.ResolveReference(ref).String()
		}
	}
	limg := strings.ToLower(img)
	if !strings.HasPrefix(limg, "http://") && !strings.HasPrefix(limg, "https://") {
		return rawURL
	}
	return img
}

// resolveBlockImages rewrites the URL of each image/gallery block that points at a
// third-party *page* to the direct image that page advertises, so pasted
// Pixabay/Unsplash links render instead of showing a broken image. It only
// rewrites URL strings — nothing is downloaded or re-hosted — is bounded by
// maxImageResolvePerSave, and preserves every other block field via a
// RawMessage round-trip. On any parse issue it returns the input unchanged.
func (a *App) resolveBlockImages(ctx context.Context, blocksJSON string) string {
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(blocksJSON), &blocks); err != nil {
		return blocksJSON
	}
	getStr := func(m map[string]json.RawMessage, k string) string {
		raw, ok := m[k]
		if !ok {
			return ""
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
		return s
	}
	budget := maxImageResolvePerSave
	changed := false
	for _, blk := range blocks {
		if budget <= 0 {
			break
		}
		switch getStr(blk, "type") {
		case "image":
			u := getStr(blk, "url")
			if u == "" {
				continue
			}
			if resolved := a.resolveImageLink(ctx, u); resolved != u {
				if raw, err := json.Marshal(resolved); err == nil {
					blk["url"] = raw
					changed = true
					budget--
				}
			}
		case "gallery":
			raw, ok := blk["images"]
			if !ok {
				continue
			}
			var imgs []string
			if json.Unmarshal(raw, &imgs) != nil {
				continue
			}
			g := false
			for i, u := range imgs {
				if budget <= 0 {
					break
				}
				if strings.TrimSpace(u) == "" {
					continue
				}
				if resolved := a.resolveImageLink(ctx, u); resolved != u {
					imgs[i] = resolved
					g = true
					budget--
				}
			}
			if g {
				if nraw, err := json.Marshal(imgs); err == nil {
					blk["images"] = nraw
					changed = true
				}
			}
		}
	}
	if !changed {
		return blocksJSON
	}
	if out, err := json.Marshal(blocks); err == nil {
		return string(out)
	}
	return blocksJSON
}

// ensureFeatureImage gives a post a hero image automatically: when the author left
// the feature image blank, it adopts the first image in the rendered body; and it
// resolves a page-link feature image to its direct og:image. Storage-neutral — it
// only ever stores a URL string. No-op when meta is nil or no image can be found.
func (a *App) ensureFeatureImage(ctx context.Context, meta *PostMeta, contentHTML string) {
	if meta == nil {
		return
	}
	fi := strings.TrimSpace(meta.FeatureImage)
	if fi == "" {
		fi = strings.TrimSpace(seo.ExtractFirstImage(contentHTML))
	}
	if fi == "" {
		return
	}
	meta.FeatureImage = a.resolveImageLink(ctx, fi)
}
