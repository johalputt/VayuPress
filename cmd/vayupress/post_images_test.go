// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/pace"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/settings"
)

// postImagesApp is an install with a Pixabay key, the switch on, and the
// media library in a temporary directory.
func postImagesApp(t *testing.T) *App {
	t.Helper()
	openMigratedDB(t)
	prev := config.Cfg.MediaDir
	config.Cfg.MediaDir = filepath.Join(t.TempDir(), "media")
	t.Cleanup(func() { config.Cfg.MediaDir = prev })
	a := &App{siteSettings: settings.New(dbpkg.DB)}
	a.secrets = secrets.New(dbpkg.DB, nil, filepath.Join(t.TempDir(), ".kek"))
	ctx := context.Background()
	if _, err := a.secrets.Upsert(ctx, secrets.ProviderPixabay, "Pixabay", "", "test-key", true, false); err != nil {
		t.Fatal(err)
	}
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyFeaturePostImages: "on"}); err != nil {
		t.Fatal(err)
	}
	return a
}

func aPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 9))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// fakePixabay answers searches with search and every image with img, and
// records what was asked.
type fakePixabay struct {
	search string
	img    []byte
	asked  []string
}

func (f *fakePixabay) install(t *testing.T) {
	prevT, prevI := pixabayFetchText, pixabayFetchImage
	t.Cleanup(func() { pixabayFetchText, pixabayFetchImage = prevT, prevI })
	pixabayFetchText = func(_ context.Context, u string) (int, []byte, error) {
		f.asked = append(f.asked, u)
		return 200, []byte(f.search), nil
	}
	pixabayFetchImage = func(_ context.Context, u string) (int, []byte, error) {
		f.asked = append(f.asked, u)
		if strings.Contains(u, "gone") {
			return 404, nil, nil
		}
		return 200, f.img, nil
	}
}

func (f *fakePixabay) count(part string) int {
	n := 0
	for _, u := range f.asked {
		if strings.Contains(u, part) {
			n++
		}
	}
	return n
}

func addImagelessPost(t *testing.T, slug, title, content, created, image string) {
	t.Helper()
	if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,created_at,updated_at,status,is_page,feature_image) VALUES(?,?,?,?,?,?,'published',0,?)`,
		slug, title, slug, content, created, created, image); err != nil {
		t.Fatal(err)
	}
}

func cardImage(t *testing.T, slug string) string {
	t.Helper()
	var img string
	if err := dbpkg.DB.QueryRow(`SELECT feature_image FROM articles WHERE slug=?`, slug).Scan(&img); err != nil {
		t.Fatal(err)
	}
	return img
}

func runPass(a *App) {
	a.postImagesPass(context.Background(), pace.New(pace.Sources{}).NewJob(pace.JobConfig{Min: 1, Max: 10}), 0)
}

// TestPostsWithoutACardImageGetOneFromPixabay — one seed per way: a post
// whose own Pixabay picture still answers keeps that picture; one whose
// picture is gone, and one with none, get the first photo found for their
// title; one Pixabay finds nothing for is left as it was; one that has a
// card image is not touched. Every image is copied into the media library.
func TestPostsWithoutACardImageGetOneFromPixabay(t *testing.T) {
	a := postImagesApp(t)
	f := &fakePixabay{img: aPNG(t), search: `{"hits":[{"largeImageURL":"https://pixabay.com/get/found_1280.jpg","pageURL":"https://pixabay.com/photos/found"}]}`}
	f.install(t)
	addImagelessPost(t, "own-picture", "Own picture", `<img src="https://pixabay.com/get/own_1280.jpg">`, "2026-03-01 10:00:00", "")
	addImagelessPost(t, "picture-gone", "Rivers of the North", `<img src="https://pixabay.com/get/gone_1280.jpg">`, "2026-03-02 10:00:00", "")
	addImagelessPost(t, "no-picture", "Mountains at Dawn", `<p>words</p>`, "2026-03-03 10:00:00", "")
	addImagelessPost(t, "has-one", "Has one", `<p>words</p>`, "2026-03-04 10:00:00", "/media/kept.webp")
	runPass(a)

	for _, slug := range []string{"own-picture", "picture-gone", "no-picture"} {
		if img := cardImage(t, slug); !strings.HasPrefix(img, "/media/") {
			t.Errorf("%s has card image %q, want one copied into the media library", slug, img)
		}
	}
	if f.count("own_1280") != 1 || f.count("q=own+picture") != 0 {
		t.Errorf("a post whose own picture answers was searched for anyway: %v", f.asked)
	}
	if f.count("q=rivers+north") != 1 || f.count("q=mountains+dawn") != 1 {
		t.Errorf("posts with no picture, or a gone one, were not searched for by title: %v", f.asked)
	}
	if img := cardImage(t, "has-one"); img != "/media/kept.webp" {
		t.Errorf("a post with a card image was given another: %q", img)
	}
	if f.count("has+one") != 0 {
		t.Errorf("a post with a card image was searched for: %v", f.asked)
	}
}

// TestAPostIsTriedOnce — Pixabay finding nothing leaves the post alone, and
// no later pass asks again: each try is recorded, and a pass reads only the
// posts newer than the newest tried and older than the oldest.
func TestAPostIsTriedOnce(t *testing.T) {
	a := postImagesApp(t)
	f := &fakePixabay{img: aPNG(t), search: `{"hits":[]}`}
	f.install(t)
	addImagelessPost(t, "nothing-found", "Nothing found here", `<p>x</p>`, "2026-03-02 10:00:00", "")
	runPass(a)
	if img := cardImage(t, "nothing-found"); img != "" {
		t.Fatalf("a post Pixabay found nothing for was given %q", img)
	}
	searches := f.count("q=")
	runPass(a)
	if f.count("q=") != searches {
		t.Errorf("a second pass searched again for a post already tried: %v", f.asked)
	}
	addImagelessPost(t, "newer", "Newer post", `<p>x</p>`, "2026-03-05 10:00:00", "")
	addImagelessPost(t, "older", "Older post", `<p>x</p>`, "2026-01-01 10:00:00", "")
	runPass(a)
	if f.count("q=newer+post") != 1 || f.count("q=older+post") != 1 {
		t.Errorf("a later pass did not take the posts above and below those tried: %v", f.asked)
	}
}

// TestTheJobStopsWhenSwitchedOff — the switch is read before each post.
func TestTheJobStopsWhenSwitchedOff(t *testing.T) {
	a := postImagesApp(t)
	f := &fakePixabay{img: aPNG(t), search: `{"hits":[{"largeImageURL":"https://pixabay.com/get/x_1280.jpg"}]}`}
	f.install(t)
	addImagelessPost(t, "waiting", "Waiting post", `<p>x</p>`, "2026-03-02 10:00:00", "")
	if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeyFeaturePostImages: "off"}); err != nil {
		t.Fatal(err)
	}
	runPass(a)
	if len(f.asked) != 0 || cardImage(t, "waiting") != "" {
		t.Errorf("the job ran with its switch off: %v", f.asked)
	}
}

// TestTheFetcherKeepsToPixabaysAddresses — neither a post's HTML, a search
// answer nor a redirect sends a fetch anywhere but Pixabay.
func TestTheFetcherKeepsToPixabaysAddresses(t *testing.T) {
	calls := 0
	get := pixabayGet(func(_ context.Context, u string) (*safefetch.Result, error) {
		calls++
		final := u
		if strings.Contains(u, "redirect") {
			final = "https://evil.example/"
		}
		return &safefetch.Result{Status: 200, Body: []byte("x"), FinalURL: final}, nil
	})
	if _, _, err := get(context.Background(), "https://evil.example/a.jpg"); !errors.Is(err, errNotPixabay) || calls != 0 {
		t.Errorf("another host: %v after %d requests", err, calls)
	}
	if _, _, err := get(context.Background(), "https://pixabay.com/get/a.jpg?redirect=1"); !errors.Is(err, errNotPixabay) {
		t.Errorf("an answer from another host was taken: %v", err)
	}
	if st, _, err := get(context.Background(), "https://cdn.pixabay.com/photo/a.jpg"); err != nil || st != 200 {
		t.Errorf("Pixabay's own address: %d %v", st, err)
	}
}

// TestAPassEndsWhenATryCannotBeRecorded — an unrecorded try would be the
// next batch's first post again, asked of Pixabay for ever. The pass ends
// after it instead; the next pass, minutes later, tries again.
func TestAPassEndsWhenATryCannotBeRecorded(t *testing.T) {
	a := postImagesApp(t)
	f := &fakePixabay{img: aPNG(t), search: `{"hits":[]}`}
	f.install(t)
	addImagelessPost(t, "stuck", "Stuck post", `<p>x</p>`, "2026-03-02 10:00:00", "")
	if _, err := dbpkg.DB.Exec(`DROP TABLE post_image_tries`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { runPass(a); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("the pass did not end; Pixabay was asked %d times", len(f.asked))
	}
	if n := f.count("q=stuck+post"); n != 1 {
		t.Errorf("Pixabay was asked about the post %d times, want once", n)
	}
}
