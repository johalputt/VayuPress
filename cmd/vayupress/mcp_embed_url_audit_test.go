// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/mcp"
)

// Adversarial pass over embed_url. The premise: I hold a media:write API key —
// the narrowest credential this panel issues — and I want the server to do
// something for me that this credential was never meant to buy.
//
// What makes embed_url worth attacking specifically is that it MOVED a
// capability across a trust boundary. Fetching an arbitrary URL from the
// server's own network position previously required an admin console session
// plus CSRF. This tool hands the same outbound fetch to a key whose entire
// grant is "write media".

func embedTool(t *testing.T) mcp.Tool {
	t.Helper()
	for _, tl := range (&App{}).buildMCPServer().Tools() {
		if tl.Name == "embed_url" {
			return tl
		}
	}
	t.Fatal("embed_url is not registered")
	return mcp.Tool{}
}

// embedActorCtx builds a request context carrying a DISTINCT api key, because
// the per-key rate limiter is keyed on it and these tests would otherwise spend
// one another's budget. Sharing it makes the suite order-dependent, which is the
// exact shape of the flake this repo has already chased once: a test that passes
// alone, fails in a full run, and blames the wrong control.
var embedAuditSeq atomic.Int64

func embedActorCtx(id string) context.Context {
	// Unique per invocation, not merely per test: the limiter window is a
	// minute, so a -count=N run would otherwise accumulate across repeats and
	// start refusing calls the assertions expect to succeed. Stable within one
	// invocation, because the load test shares a context across its goroutines
	// on purpose — that is the thing being measured.
	id += "-" + strconv.FormatInt(embedAuditSeq.Add(1), 10)
	p := apikeys.NewPermissions()
	p.Grant(apikeys.SectionMedia, apikeys.ActionWrite)
	return auth.RequestWithKeyInfo(
		httptest.NewRequest("POST", "/mcp", nil),
		apikeys.KeyInfo{ID: id, Label: "audit", Scope: apikeys.ScopeExternal, Perms: p},
	).Context()
}

func callEmbedAs(t *testing.T, ctx context.Context, url string) (string, error) {
	t.Helper()
	args, err := json.Marshal(map[string]string{"url": url})
	if err != nil {
		t.Fatal(err)
	}
	return embedTool(t).Handler(ctx, args)
}

// FINDING 1 — the server is a probe with a readable oracle.
//
// The resolver distinguishes its failures: a blocked private address, a
// transport failure carrying the Go error verbatim ("no such host", "connection
// refused", "i/o timeout"), and a non-2xx status. Handed to a media:write key
// those three answers are an internal-network mapper and a port scanner:
//
//	embed_url https://intranet.corp.internal/  -> "private/blocked address"
//	  => that name resolves to RFC1918 space. I have just mapped your network
//	     without a single packet of my own.
//	embed_url https://198.51.100.7:8080/       -> "connection refused" vs a hang
//	  => I am scanning from your IP, and reading the results in your replies.
//
// The fix is not to make the tool less useful. It is that a caller learns
// whether ITS OWN request was well-formed, and nothing about what the server
// found when it went looking.
func TestEmbedURLDoesNotLeakWhatTheServerFoundOutThere(t *testing.T) {
	// Each of these fails for a DIFFERENT reason on the wire. A caller must not
	// be able to tell them apart.
	probes := []string{
		"https://127.0.0.1/",             // loopback — refused by policy
		"https://10.0.0.1/",              // RFC1918 — refused by policy
		"https://169.254.169.254/latest", // cloud metadata — refused by policy
		"https://[::1]/",                 // loopback v6
		"https://no-such-host.invalid/",  // DNS failure
	}

	ctx := embedActorCtx("audit-oracle")
	seen := map[string]string{}
	for _, p := range probes {
		out, err := callEmbedAs(t, ctx, p)
		if err == nil {
			t.Fatalf("embed_url(%q) succeeded, returning %q — none of these should resolve", p, out)
		}
		seen[p] = err.Error()
	}

	// Every answer must be the same sentence.
	var first, firstURL string
	for u, msg := range seen {
		if first == "" {
			first, firstURL = msg, u
			continue
		}
		if msg != first {
			t.Errorf("embed_url distinguishes its failures, which makes this server a probe:\n"+
				"  %s -> %q\n  %s -> %q\n\n"+
				"A media:write key can walk a network and read the results in these replies.",
				firstURL, first, u, msg)
		}
	}

	// And the sentence must not carry the transport's own words.
	for u, msg := range seen {
		for _, leak := range []string{
			"no such host", "connection refused", "i/o timeout", "dial tcp",
			"private", "blocked address", "169.254", "127.0.0.1", "10.0.0.1",
		} {
			if strings.Contains(strings.ToLower(msg), leak) {
				t.Errorf("embed_url(%q) leaked %q in %q — that is a fact about the target, "+
					"not about the caller's request", u, leak, msg)
			}
		}
	}
}

// The control for the test above, and it has to be a real one: returning a
// single constant string for every possible failure would satisfy "all network
// outcomes look alike" while making the tool unusable. So the property is
// sharper than sameness — a caller's OWN malformed input must be reported
// DIFFERENTLY from anything the server learned on the wire.
func TestEmbedURLStillReportsACallersOwnBadInput(t *testing.T) {
	ctx := embedActorCtx("audit-input")
	_, netErr := callEmbedAs(t, ctx, "https://10.0.0.1/")
	if netErr == nil {
		t.Fatal("a private address resolved; the rest of this test is meaningless")
	}

	for _, bad := range []string{"", "   ", "not a url", "ftp://example.com/x", "javascript:alert(1)"} {
		_, err := callEmbedAs(t, ctx, bad)
		if err == nil {
			t.Errorf("embed_url(%q) was accepted", bad)
			continue
		}
		if err.Error() == netErr.Error() {
			t.Errorf("embed_url(%q) answers a malformed URL with the same sentence it uses for an "+
				"unreachable one (%q).\n\nCollapsing every outcome into one string hides the oracle "+
				"and the caller's own mistake alike, which makes the tool unusable rather than safe.",
				bad, err)
		}
	}
}

// FINDING 2 — an unmetered outbound sink on a narrow credential.
//
// Every call holds an outbound connection for up to the fetch timeout. Nothing
// bounded how many a caller could have in flight, so a media:write key could pin
// arbitrarily many sockets by naming slow URLs — the same unmetered-sink shape
// already found once on the admin plane.
//
// Two controls answer it, and they are tested SEPARATELY on purpose. A single
// test that fires many concurrent calls passes with either control present and
// the other deleted, so it proves neither: it only proves that at least one of
// them exists. That is the same trap a pair of thresholds in the sweep detector
// fell into, and the fix is the same — isolate each control so its own mutation
// has somewhere to fail.

// The per-key rate limit, isolated: calls are SEQUENTIAL, so only ever one is in
// flight and the concurrency semaphore can never be the thing that refuses.
// Anything refused here was refused for rate.
func TestEmbedURLRateLimitsOneKey(t *testing.T) {
	ctx := embedActorCtx("audit-rate")
	refused := 0
	for i := 0; i < embedPerActorPerMin+8; i++ {
		if _, err := callEmbedAs(t, ctx, "https://no-such-host.invalid/"); err != nil &&
			strings.Contains(err.Error(), "try again in a minute") {
			refused++
		}
	}
	if refused == 0 {
		t.Errorf("one key made %d sequential embed_url calls and none was rate-limited.\n\n"+
			"Each call spends an outbound fetch from this server's network position, which is a "+
			"capability a media:write key was never meant to buy in bulk.", embedPerActorPerMin+8)
	}
}

// The concurrency ceiling, isolated: the slots are held directly, and the call
// is made by a FRESH key whose per-minute budget is untouched. Anything refused
// here was refused for concurrency.
//
// Holding the slots rather than racing real requests is deliberate. In a sandbox
// every fetch fails instantly, so slots free before they can saturate and a
// concurrency test built on real calls quietly measures nothing.
func TestEmbedURLBoundsConcurrentResolutions(t *testing.T) {
	for i := 0; i < embedMaxConcurrent; i++ {
		embedSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for i := 0; i < embedMaxConcurrent; i++ {
			<-embedSlots
		}
	})

	_, err := callEmbedAs(t, embedActorCtx("audit-sema"), "https://example.com/a")
	if err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Errorf("with all %d resolution slots held, embed_url returned %v.\n\n"+
			"Without this ceiling a caller can hold an unbounded number of outbound connections "+
			"open at once; the per-minute rate limit does not bound that, because a single "+
			"minute's budget spent all at once is still a burst that all hangs together.",
			embedMaxConcurrent, err)
	}
}
