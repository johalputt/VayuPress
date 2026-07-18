package main

// handlers_media_sideload.go — automatic "import instead of hotlink" for images
// referenced by external URL (Pixabay, Unsplash, anywhere).
//
// A hotlinked external image is fragile: the source may block hotlinking (403 to
// the reader), rot, force mixed content on an http:// URL, or leak the reader's
// IP to a third party. VayuPress's answer is to SIDELOAD — fetch the bytes once,
// server-side, through the SSRF-hardened safefetch client, validate + re-encode
// through the same trusted media pipeline as a direct upload, and rewrite the
// reference to the local /media/<hash> URL. The image then renders reliably in
// the post AND on post cards, from the site's own origin.
//
// This is applied authoritatively at save time (so it also covers the API,
// MCP, and HTML-import write paths, not just the editor) and is best-effort:
// any URL that cannot be sideloaded is left exactly as-is, so a save never fails
// because of one unreachable image.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/johalputt/vayupress/internal/mode"
)

// maxSideloadPerSave bounds how many external images one save pass will fetch,
// so a save never stalls on a document that pastes many external images at once.
// Already-local images are free to skip, so the rest are re-hosted over the next
// few saves.
const maxSideloadPerSave = 12

// isLocalOrInlineImage reports whether a URL is already same-origin/local (a
// /media path, any site-relative path, a data: URI) or otherwise not an external
// http(s) URL we should sideload.
func isLocalOrInlineImage(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" {
		return true
	}
	lu := strings.ToLower(u)
	if strings.HasPrefix(u, "/") || strings.HasPrefix(lu, "data:") {
		return true
	}
	// An absolute URL that already points at our own /media store is local.
	if strings.Contains(lu, "/media/") {
		return true
	}
	return !strings.HasPrefix(lu, "http://") && !strings.HasPrefix(lu, "https://")
}

// sideloadRemoteImage fetches an external image URL and re-hosts it locally,
// returning the local /media URL. ok=false leaves the caller to keep the
// original URL (best-effort: never fail the surrounding save).
func sideloadRemoteImage(ctx context.Context, url string) (localURL string, ok bool) {
	res, err := remoteImageFetcher.Get(ctx, strings.TrimSpace(url))
	if err != nil || res.Status < 200 || res.Status >= 300 {
		return "", false
	}
	stored, err := storeValidatedMedia(res.Body, false)
	if err != nil {
		return "", false
	}
	return stored.URL, true
}

// sideloadImageURL returns a rewritten URL (local) when the input was an
// external image that we successfully re-hosted; otherwise it returns the input
// unchanged with changed=false.
func sideloadImageURL(ctx context.Context, url string) (string, bool) {
	if isLocalOrInlineImage(url) {
		return url, false
	}
	if local, ok := sideloadRemoteImage(ctx, url); ok {
		return local, true
	}
	return url, false
}

// sideloadBlocksImages walks a block-document JSON and re-hosts any external
// image URLs it finds (image block `url`, gallery block `images[]`). It operates
// on the raw JSON so every other field on every block is preserved byte-for-byte
// except the URLs it rewrites. Returns the (possibly) rewritten JSON and whether
// anything changed. Skipped entirely in read-only/quarantined mode so a save
// never breaks; unreachable images are left as-is.
func sideloadBlocksImages(ctx context.Context, blocksJSON string) (string, bool) {
	if strings.TrimSpace(blocksJSON) == "" {
		return blocksJSON, false
	}
	if cur := mode.Global.Current(); cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		return blocksJSON, false
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(blocksJSON), &blocks); err != nil {
		return blocksJSON, false
	}
	// Bound the work per save: each external image is a network fetch, and this
	// runs on every autosave. Already-rewritten (local) URLs are skipped for free,
	// so across a few saves every image ends up local; any left over this budget
	// is picked up on the next save. This keeps a save from stalling on a post
	// that pastes dozens of external images at once.
	budget := maxSideloadPerSave
	changed := false
	for _, b := range blocks {
		if budget <= 0 {
			break
		}
		var typ string
		if raw, ok := b["type"]; ok {
			_ = json.Unmarshal(raw, &typ)
		}
		switch typ {
		case "image":
			if raw, ok := b["url"]; ok {
				var u string
				if json.Unmarshal(raw, &u) == nil && !isLocalOrInlineImage(u) {
					if local, did := sideloadImageURL(ctx, u); did {
						budget--
						if nb, err := json.Marshal(local); err == nil {
							b["url"] = nb
							changed = true
						}
					}
				}
			}
		case "gallery":
			if raw, ok := b["images"]; ok {
				var imgs []string
				if json.Unmarshal(raw, &imgs) == nil {
					any := false
					for i, u := range imgs {
						if budget <= 0 {
							break
						}
						if isLocalOrInlineImage(u) {
							continue
						}
						if local, did := sideloadImageURL(ctx, u); did {
							budget--
							imgs[i] = local
							any = true
						}
					}
					if any {
						if nb, err := json.Marshal(imgs); err == nil {
							b["images"] = nb
							changed = true
						}
					}
				}
			}
		}
	}
	if !changed {
		return blocksJSON, false
	}
	out, err := json.Marshal(blocks)
	if err != nil {
		return blocksJSON, false
	}
	return string(out), true
}
