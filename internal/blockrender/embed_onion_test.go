// SPDX-License-Identifier: Apache-2.0

package blockrender

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// The finding, in the attacker's voice — except the attacker here is the
// product's own interface, and the person it misleads is the reader.
//
// A Tor Space strips every external origin from every CSP directive, frame-src
// included (render.applyOnionCSP). The video facade's whole design is that
// clicking it injects an iframe pointed at a third-party origin. Put those two
// together and a reader on the onion site gets a poster with a play button, a
// pointer cursor and a keyboard handler, clicks it, and nothing happens: the
// iframe is created and the page's own policy refuses to load it. No error
// reaches them, and the interface goes on advertising a control that cannot
// work there.
//
// That is the same class of defect as a changelog claiming a hardening that is
// not in the build. The fix is not to make the frame load — in a Tor Space it
// must not — but to stop claiming it will.
const onionVideoBlocks = `[{"type":"embed","kind":"video",` +
	`"url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ",` +
	`"embedSrc":"https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",` +
	`"title":"A talk about something","provider":"YouTube",` +
	`"thumbURL":"/media/0123456789abcdef0123456789abcdef.jpg"}]`

func TestVideoFacadeIsNotOfferedInATorSpace(t *testing.T) {
	prev := config.Cfg.OnionMode
	config.Cfg.OnionMode = true
	t.Cleanup(func() { config.Cfg.OnionMode = prev })

	html, _, err := Render(onionVideoBlocks)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if strings.Contains(html, "data-embed-src") {
		t.Errorf("a Tor Space rendered a click-to-load facade:\n%s\n\n"+
			"frame-src carries no external origin on an onion page, so the iframe this attribute "+
			"tells video-facade.js to inject is refused by the page's own CSP. The reader gets a "+
			"play button that silently does nothing.", html)
	}
	if strings.Contains(html, "video-facade") {
		t.Errorf("a Tor Space rendered facade markup:\n%s", html)
	}

	// Degrading is not the same as disappearing. The reader must still get the
	// video — as a link they can follow, with the poster this install stored
	// locally, which is a thing Tor Browser can actually do.
	if !strings.Contains(html, "embed-card") {
		t.Errorf("the video did not fall back to a link card:\n%s\n\n"+
			"Removing the facade without leaving anything behind loses the content entirely, "+
			"which is a worse answer than a dead play button.", html)
	}
	if !strings.Contains(html, "https://www.youtube.com/watch?v=dQw4w9WgXcQ") {
		t.Errorf("the link card does not link to the video:\n%s", html)
	}
	if !strings.Contains(html, "A talk about something") {
		t.Errorf("the link card lost the title:\n%s", html)
	}
	// The poster is local media, so it is served under img-src 'self' and does
	// not reach off-onion.
	if !strings.Contains(html, "/media/0123456789abcdef0123456789abcdef.jpg") {
		t.Errorf("the link card lost the locally-stored poster:\n%s", html)
	}
}

// The clearnet behaviour is the control for the test above: same block, same
// renderer, and here the facade IS the right answer. Without this, deleting the
// facade outright would pass the Tor test.
func TestVideoFacadeIsStillOfferedOnClearnet(t *testing.T) {
	prev := config.Cfg.OnionMode
	config.Cfg.OnionMode = false
	t.Cleanup(func() { config.Cfg.OnionMode = prev })

	html, _, err := Render(onionVideoBlocks)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(html, `data-embed-src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"`) {
		t.Errorf("clearnet lost the click-to-load facade:\n%s", html)
	}
	if !strings.Contains(html, "video-facade") {
		t.Errorf("clearnet lost the facade markup:\n%s", html)
	}
	// Still click-to-load: no iframe exists until the reader acts.
	if strings.Contains(html, "<iframe") {
		t.Errorf("an iframe was rendered into the page rather than injected on click:\n%s\n\n"+
			"That would load the third party for every reader who merely scrolls past.", html)
	}
}
