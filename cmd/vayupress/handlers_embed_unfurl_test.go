package main

import "testing"

// TestDetectVideoEmbed verifies that video detection matches the provider host
// by exact equality after URL parsing — never as an unanchored substring — so a
// malicious URL that merely *contains* a provider host cannot be misclassified.
func TestDetectVideoEmbed(t *testing.T) {
	cases := []struct {
		name     string
		url      string
		wantProv string
	}{
		{"yt watch", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "youtube"},
		{"yt short host", "https://youtu.be/dQw4w9WgXcQ", "youtube"},
		{"yt embed", "https://www.youtube.com/embed/dQw4w9WgXcQ", "youtube"},
		{"yt shorts", "https://youtube.com/shorts/dQw4w9WgXcQ", "youtube"},
		{"vimeo plain", "https://vimeo.com/123456789", "vimeo"},
		{"vimeo video path", "https://vimeo.com/video/123456789", "vimeo"},

		// SSRF / spoofing attempts — the provider host appears only as a path or
		// query substring, so detection must REFUSE them.
		{"spoof yt in query", "https://evil.com/?x=youtube.com/watch?v=dQw4w9WgXcQ", ""},
		{"spoof yt in path", "https://evil.com/youtube.com/embed/dQw4w9WgXcQ", ""},
		{"spoof vimeo in path", "https://evil.com/vimeo.com/123456789", ""},
		{"subdomain spoof", "https://youtube.com.evil.com/watch?v=dQw4w9WgXcQ", ""},
		{"not a video", "https://example.com/article", ""},
		{"yt no id", "https://www.youtube.com/watch?v=", ""},
		{"vimeo non-numeric", "https://vimeo.com/abcdef", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov, src := detectVideoEmbed(tc.url)
			if prov != tc.wantProv {
				t.Errorf("provider = %q, want %q (url %s)", prov, tc.wantProv, tc.url)
			}
			if tc.wantProv == "" && src != "" {
				t.Errorf("expected no embed src for %s, got %q", tc.url, src)
			}
			if tc.wantProv != "" && src == "" {
				t.Errorf("expected embed src for %s, got empty", tc.url)
			}
		})
	}
}
