package payments

// egress.go — the safe fallback outbound client for the payment-gateway clients.
//
// The app always injects its shared, egress-guarded outbound client, but the
// gateway constructors also accept a nil client for convenience. That fallback
// must NOT be http.DefaultClient: on a Tor Space a direct dial to a payment
// processor would reveal the onion server's real IP (ADR-0141), and a plain
// client is not SSRF-hardened. It routes through safefetch instead.

import (
	"net/http"
	"time"

	"github.com/johalputt/vayupress/internal/safefetch"
)

// guardedDefaultClient is the SSRF-hardened, egress-guarded fallback used when a
// gateway client is constructed without an explicit *http.Client.
func guardedDefaultClient() *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: safefetch.SafeTransport(safefetch.TransportOptions{}),
	}
}
