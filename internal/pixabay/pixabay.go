// SPDX-License-Identifier: Apache-2.0

// Package pixabay finds a picture for a post: the Pixabay image a post's own
// HTML already carries, or the first photo Pixabay's search gives for the
// post's title. It only reads; the caller copies the picture into its own
// media library, because Pixabay's image addresses are not for linking to
// from a site (its terms: download an image to your own server to use it).
package pixabay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// Getter fetches a URL and gives its status and body; the caller's is the
// install's safe outbound fetch, bounded in size and time.
type Getter func(ctx context.Context, rawURL string) (int, []byte, error)

// searchEndpoint is Pixabay's image search.
const searchEndpoint = "https://pixabay.com/api/"

// Hit is the photo a search chose.
type Hit struct {
	Image string // the image to copy (largeImageURL, else webformatURL)
	Page  string // its page on Pixabay, the credit link
	User  string // who took it
}

// linkInPost finds an image a post's HTML takes from Pixabay: an <img src>
// or a srcset entry on pixabay.com or one of its subdomains, over https.
var linkInPost = regexp.MustCompile(`https://(?:[a-z0-9-]+\.)?pixabay\.com/[^\s"'<>]+\.(?:jpe?g|png|webp)`)

// LinkIn returns the first Pixabay image address in a post's HTML, up to
// its file name, or "".
// Older posts carry the address the search API returned when they were
// written; Pixabay keeps such an address for a while, so it may no longer
// answer, and the caller searches instead.
func LinkIn(html string) string {
	return linkInPost.FindString(html)
}

// stopWords are left out of a search: a title's small words narrow Pixabay's
// results to nothing ("the", "how", "with").
var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true,
	"for": true, "from": true, "how": true, "in": true, "into": true, "is": true, "it": true,
	"its": true, "of": true, "on": true, "or": true, "that": true, "the": true, "this": true,
	"to": true, "what": true, "when": true, "why": true, "with": true, "your": true, "you": true,
}

// Query is the search a title gives: its first four words that are not
// small words, lower case, letters and digits only. Pixabay matches every
// word, so more words find nothing.
func Query(title string) string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(w) < 2 || stopWords[w] {
			continue
		}
		out = append(out, w)
		if len(out) == 4 {
			break
		}
	}
	return strings.Join(out, " ")
}

// Search asks Pixabay for photos matching q, safe for every reader, and
// returns the first. It returns no Hit and no error when there is none.
func Search(ctx context.Context, get Getter, key, q string) (Hit, bool, error) {
	if key == "" || q == "" {
		return Hit{}, false, nil
	}
	v := url.Values{}
	v.Set("key", key)
	v.Set("q", q)
	v.Set("image_type", "photo")
	v.Set("safesearch", "true")
	v.Set("per_page", "3")
	status, body, err := get(ctx, searchEndpoint+"?"+v.Encode())
	if err != nil {
		return Hit{}, false, err
	}
	if status != 200 {
		// The key is in the address; the error names only the status.
		return Hit{}, false, fmt.Errorf("pixabay: search answered %d", status)
	}
	var res struct {
		Hits []struct {
			LargeImageURL string `json:"largeImageURL"`
			WebformatURL  string `json:"webformatURL"`
			PageURL       string `json:"pageURL"`
			User          string `json:"user"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return Hit{}, false, fmt.Errorf("pixabay: search answer: %w", err)
	}
	for _, h := range res.Hits {
		img := h.LargeImageURL
		if img == "" {
			img = h.WebformatURL
		}
		if Fetchable(img) {
			return Hit{Image: img, Page: h.PageURL, User: h.User}, true, nil
		}
	}
	return Hit{}, false, nil
}

// Fetchable reports whether rawURL is an https address on pixabay.com. Only
// such an address is fetched, and only an answer that ended on one is kept,
// so neither a post's HTML, a search answer nor a redirect can send a fetch
// anywhere else.
func Fetchable(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "pixabay.com" || strings.HasSuffix(h, ".pixabay.com")
}
