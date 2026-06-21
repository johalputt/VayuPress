package render

import (
	"strings"
	"testing"
)

func TestBuildCSPBaseline(t *testing.T) {
	csp := BuildCSP("abc123", nil)
	if !strings.Contains(csp, "script-src 'self' 'nonce-abc123'") {
		t.Errorf("baseline missing nonce: %q", csp)
	}
	if strings.Contains(csp, "frame-src") {
		t.Errorf("baseline must not carry frame-src: %q", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("baseline missing frame-ancestors: %q", csp)
	}
}

func TestBuildCSPExtendsFrameSrcForAllowlistedOrigins(t *testing.T) {
	csp := BuildCSP("n", []string{"https://www.youtube-nocookie.com"})
	if !strings.Contains(csp, "frame-src 'self' https://www.youtube-nocookie.com") {
		t.Errorf("expected narrow frame-src extension, got %q", csp)
	}
}

func TestBuildCSPDropsUnknownOrigins(t *testing.T) {
	// A tampered sidecar / crafted block can never widen the policy.
	csp := BuildCSP("n", []string{"https://evil.example", "javascript:alert(1)"})
	if strings.Contains(csp, "frame-src") {
		t.Errorf("unknown origins must be dropped, got %q", csp)
	}
}

func TestBuildCSPDeduplicatesAndSorts(t *testing.T) {
	csp := BuildCSP("n", []string{
		"https://player.vimeo.com",
		"https://www.youtube-nocookie.com",
		"https://player.vimeo.com",
	})
	want := "frame-src 'self' https://player.vimeo.com https://www.youtube-nocookie.com"
	if !strings.Contains(csp, want) {
		t.Errorf("expected sorted, de-duped frame-src %q in %q", want, csp)
	}
}

func TestVideoEmbedSrc(t *testing.T) {
	cases := []struct {
		provider, id, want string
	}{
		{"youtube", "dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"vimeo", "123456789", "https://player.vimeo.com/video/123456789"},
		{"youtube", "bad id!", ""},               // unsafe id rejected
		{"youtube", "../../etc/passwd", ""},      // traversal rejected
		{"dailymotion", "abc123def", ""},         // provider not allowlisted
		{"youtube", strings.Repeat("a", 65), ""}, // over length cap
	}
	for _, c := range cases {
		if got := VideoEmbedSrc(c.provider, c.id); got != c.want {
			t.Errorf("VideoEmbedSrc(%q,%q)=%q want %q", c.provider, c.id, got, c.want)
		}
	}
}

func TestFrameOriginsInHTML(t *testing.T) {
	html := `<p>hi</p><div class="video-facade" data-embed-src="https://www.youtube-nocookie.com/embed/abc">` +
		`</div><div class="video-facade" data-embed-src="https://player.vimeo.com/video/123"></div>`
	got := FrameOriginsInHTML(html)
	if len(got) != 2 {
		t.Fatalf("expected 2 origins, got %v", got)
	}
	if got[0] != "https://player.vimeo.com" || got[1] != "https://www.youtube-nocookie.com" {
		t.Errorf("unexpected origins (want sorted): %v", got)
	}

	if FrameOriginsInHTML("<p>no embeds here</p>") != nil {
		t.Error("plain content must yield no frame origins")
	}
}
