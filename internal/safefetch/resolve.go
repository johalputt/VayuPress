// SPDX-License-Identifier: Apache-2.0

package safefetch

import (
	"context"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// resolveIPAddr looks a host up, falling back to public resolvers when the
// system one cannot answer.
//
// WHY. On a live install the system stub resolver stopped answering — every
// lookup returned `lookup <host> on 127.0.0.53:53: read udp …: i/o timeout`.
// Nothing in the binary was broken and nothing in the binary could fix it, so
// VayuShield's published-range feeds silently stopped refreshing and ran on
// whatever they had cached, for days, while the panel went on describing them as
// current. Everything else that reaches the network — verified-bot lists, remote
// image fetches, webhooks — degraded the same way.
//
// A host-side repair reaches only an operator with a shell, which is exactly the
// answer this project rules out. So the binary carries a fallback: try the
// system resolver first, and if it CANNOT ANSWER AT ALL, ask a public resolver
// over TCP before giving up.
//
// The limits on that, because a fallback resolver is a real decision and not a
// free win:
//
//   - It is a fallback, never a preference. A working system resolver is always
//     used, so an operator's split-horizon or internal DNS keeps working.
//   - It runs ONLY on a transport failure — a timeout or a refused connection.
//     An authoritative NXDOMAIN is an answer, and asking somebody else until a
//     name resolves is how a typo becomes a connection to a stranger.
//   - It is refused outright when clearnet egress is blocked (Tor mode). A
//     direct DNS query is a clearnet callback that would leak the onion
//     server's existence, and no feed is worth that.
//   - It changes nothing about validation. Whatever address comes back still
//     goes through the same private/reserved checks; this widens no policy.
//   - It is loud. The first time it is used, and every time the system resolver
//     recovers, is logged — an install quietly resolving through a third party
//     is precisely the sort of thing an operator should never discover by
//     accident.
//   - VAYU_DNS_FALLBACK=off disables it for an operator who would rather fail
//     than send a query off-box.
//
// The fallbacks are two independent operators, queried over TCP so a UDP path
// that is being dropped (the observed failure) cannot swallow them too.
// fallbackNetwork is TCP on purpose. The failure this fallback exists for was
// UDP being dropped on the way to the stub resolver; a fallback that reaches for
// the same transport as the thing that broke is not a fallback. Named so the
// choice is pinned by a test rather than left as a string somebody tidies.
const fallbackNetwork = "tcp"

var fallbackResolvers = []string{
	"1.1.1.1:53",
	"9.9.9.9:53",
}

// dnsFallbackUsed reports whether the fallback is currently carrying lookups, so
// the state can be surfaced rather than inferred from log lines.
var dnsFallbackUsed atomic.Bool

// DNSFallbackActive reports whether the system resolver last failed and a public
// resolver answered in its place. Read by the console so an operator can see
// that their host's DNS is broken, which is otherwise invisible.
func DNSFallbackActive() bool { return dnsFallbackUsed.Load() }

func dnsFallbackEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("VAYU_DNS_FALLBACK")), "off")
}

// transportFailure distinguishes "the resolver could not be reached" from "the
// resolver answered, and the answer is that this name does not exist".
//
// Only the first justifies asking somebody else. Retrying an NXDOMAIN elsewhere
// would mean a mistyped hostname eventually resolving somewhere, which is a
// worse failure than not resolving at all.
func transportFailure(err error) bool {
	var de *net.DNSError
	if !asDNSError(err, &de) {
		return false
	}
	// Checked BEFORE the transport flags and winning over all of them: a resolver
	// may set IsTimeout alongside IsNotFound, and "this name does not exist" is
	// an answer however slowly it arrived. Asking a second resolver until a name
	// resolves is how a mistyped host becomes a connection to a stranger.
	if de.IsNotFound {
		return false
	}
	return de.IsTimeout || de.IsTemporary || strings.Contains(de.Err, "connection refused") ||
		strings.Contains(de.Err, "server misbehaving") || strings.Contains(de.Err, "no such host is served")
}

func asDNSError(err error, out **net.DNSError) bool {
	for e := err; e != nil; {
		if de, ok := e.(*net.DNSError); ok {
			*out = de
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

// fallbackPermitted is the whole decision, in one place and with no I/O, so it
// can be tested directly.
//
// It lives apart from resolveIPAddr because the first attempt at these tests
// exercised the decision only through a real lookup — and a lookup of a
// nonexistent name fails either way, so removing the Tor-mode guard or the
// operator's off switch changed nothing the tests could see. Both survived
// mutation. A guard that cannot be observed failing is not a tested guard.
func fallbackPermitted(err error) bool {
	if !dnsFallbackEnabled() {
		return false
	}
	// A clearnet DNS query from a Tor-mode install announces that the onion
	// server exists. Nothing gained from a feed refresh is worth that.
	if ClearnetBlocked() {
		return false
	}
	return transportFailure(err)
}

func resolveIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err == nil {
		if dnsFallbackUsed.CompareAndSwap(true, false) {
			logging.LogInfo("safefetch", "the system resolver is answering again; public-resolver fallback is no longer in use")
		}
		return ips, nil
	}
	if !fallbackPermitted(err) {
		return nil, err
	}

	for _, server := range fallbackResolvers {
		r := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: 4 * time.Second}
				return d.DialContext(ctx, fallbackNetwork, server)
			},
		}
		cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		got, ferr := r.LookupIPAddr(cctx, host)
		cancel()
		if ferr != nil || len(got) == 0 {
			continue
		}
		if dnsFallbackUsed.CompareAndSwap(false, true) {
			logging.LogError("safefetch", "the system resolver is not answering; using a public resolver instead",
				"first failure: "+err.Error()+" — set VAYU_DNS_FALLBACK=off to refuse this and fail instead")
		}
		return got, nil
	}
	return nil, err
}
