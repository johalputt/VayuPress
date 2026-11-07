// SPDX-License-Identifier: Apache-2.0

package pixabay

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// TestLinkInFindsAPostsOwnPixabayPicture — older posts open with the address
// the search API handed back when they were written.
func TestLinkInFindsAPostsOwnPixabayPicture(t *testing.T) {
	for _, c := range []struct{ name, html, want string }{
		{"a get link", `<figure><img src="https://pixabay.com/get/g1a2b_1280.jpg" alt=""></figure>`, "https://pixabay.com/get/g1a2b_1280.jpg"},
		{"a cdn link", `<img src="https://cdn.pixabay.com/photo/2026/01/02/x-123_1280.png">`, "https://cdn.pixabay.com/photo/2026/01/02/x-123_1280.png"},
		{"anything after the name", `<img src="https://pixabay.com/get/a.jpg?x=1&amp;y=2">`, "https://pixabay.com/get/a.jpg"},
		{"another site", `<img src="https://images.example.com/pixabay.com/a.jpg">`, ""},
		{"plain http", `<img src="http://pixabay.com/get/a.jpg">`, ""},
		{"nothing", `<p>no picture</p>`, ""},
	} {
		if got := LinkIn(c.html); got != c.want {
			t.Errorf("%s: LinkIn = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestQueryKeepsATitlesWordsThatMatter — Pixabay matches every word, so the
// small words and anything past four would find nothing.
func TestQueryKeepsATitlesWordsThatMatter(t *testing.T) {
	for title, want := range map[string]string{
		"The Dark Reason Evolution Made Humans Ticklish (It Was Never Supposed To Be Funny).": "dark reason evolution made",
		"How to Build a RAG App with Vertex AI":                                               "build rag app vertex",
		"6 November 2026":                                                                     "november 2026",
		"":                                                                                    "",
	} {
		if got := Query(title); got != want {
			t.Errorf("Query(%q) = %q, want %q", title, got, want)
		}
	}
}

// answer returns a Getter that answers every request with status and body
// and records what was asked.
func answer(status int, body string, asked *[]string) Getter {
	return func(_ context.Context, u string) (int, []byte, error) {
		*asked = append(*asked, u)
		return status, []byte(body), nil
	}
}

// TestSearchTakesTheFirstPixabayPhoto — the first hit whose image is on
// Pixabay is taken, large first; a hit pointing anywhere else is skipped.
func TestSearchTakesTheFirstPixabayPhoto(t *testing.T) {
	var asked []string
	body := `{"hits":[
		{"largeImageURL":"https://evil.example.com/a.jpg","pageURL":"https://pixabay.com/photos/a"},
		{"largeImageURL":"","webformatURL":"https://pixabay.com/get/b_640.jpg","pageURL":"https://pixabay.com/photos/b","user":"bee"},
		{"largeImageURL":"https://pixabay.com/get/c_1280.jpg"}]}`
	hit, ok, err := Search(context.Background(), answer(200, body, &asked), "k3y", "dark reason")
	if err != nil || !ok {
		t.Fatalf("Search = %v, %v", ok, err)
	}
	if hit.Image != "https://pixabay.com/get/b_640.jpg" || hit.Page != "https://pixabay.com/photos/b" || hit.User != "bee" {
		t.Errorf("Search chose %+v, want the second hit (the first is not on Pixabay)", hit)
	}
	u, _ := url.Parse(asked[0])
	if q := u.Query(); u.Host != "pixabay.com" || q.Get("q") != "dark reason" || q.Get("safesearch") != "true" || q.Get("image_type") != "photo" {
		t.Errorf("the search asked %s", asked[0])
	}
}

// TestSearchSaysWhyAndNeverTheKey — a refused search names its status, and
// the error never carries the key that was in the address.
func TestSearchSaysWhyAndNeverTheKey(t *testing.T) {
	var asked []string
	_, ok, err := Search(context.Background(), answer(429, "Too many", &asked), "s3cret-key", "cats")
	if ok || err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("a refused search gave %v, %v", ok, err)
	}
	if strings.Contains(err.Error(), "s3cret-key") {
		t.Errorf("the error carries the key: %v", err)
	}
	fail := func(context.Context, string) (int, []byte, error) { return 0, nil, errors.New("down") }
	if _, _, err := Search(context.Background(), fail, "k", "cats"); err == nil {
		t.Error("a failed fetch gave no error")
	}
}

// TestSearchAsksNothingWithoutAKeyOrAQuery — no key, or a title with no
// words left, is no search at all.
func TestSearchAsksNothingWithoutAKeyOrAQuery(t *testing.T) {
	var asked []string
	for _, c := range [][2]string{{"", "cats"}, {"k", ""}} {
		if _, ok, err := Search(context.Background(), answer(200, `{"hits":[]}`, &asked), c[0], c[1]); ok || err != nil {
			t.Errorf("Search(%q, %q) = %v, %v", c[0], c[1], ok, err)
		}
	}
	if len(asked) != 0 {
		t.Errorf("asked Pixabay %d times with no key or no query", len(asked))
	}
}

// TestOnlyPixabaysOwnAddressesAreFetchable — one seed per rule.
func TestOnlyPixabaysOwnAddressesAreFetchable(t *testing.T) {
	for u, want := range map[string]bool{
		"https://pixabay.com/api/?q=x":        true,
		"https://cdn.pixabay.com/photo/a.jpg": true,
		"http://pixabay.com/get/a.jpg":        false,
		"https://pixabay.com.evil.example/a":  false,
		"https://notpixabay.com/a.jpg":        false,
		"https://evil.example/?u=pixabay.com": false,
	} {
		if got := Fetchable(u); got != want {
			t.Errorf("Fetchable(%q) = %v, want %v", u, got, want)
		}
	}
}
