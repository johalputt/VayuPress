package main

// onion_transport.go — the OUTBOUND half of onion-to-onion VayuTalk delivery
// (ADR-0142). A guard-railed Tor lane: an HTTP client that routes through a Tor
// SOCKS proxy and REFUSES to dial anything that is not a .onion. This is the
// deanonymisation-critical boundary of the whole feature, so the .onion-only
// check happens in the dialer, before any connection is attempted, and is
// exercised directly by unit tests.
//
// The lane is inert unless the operator both enables federation AND points
// VAYUOS_TOR_SOCKS_ADDR at a Tor SOCKS proxy (their own tor, or the managed tor
// once it exposes a loopback SOCKS port). With no SOCKS address configured, no
// client can be built and nothing is ever dialled.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"

	"github.com/johalputt/vayupress/internal/config"
)

// onionKeyInflight dedups concurrent over-Tor sender-key fetches so a burst of
// inbound messages from the same new sender triggers at most one fetch.
var (
	onionKeyInflightMu sync.Mutex
	onionKeyInflight   = map[string]bool{}
)

// ensureOnionSenderKey best-effort fetches a remote onion sender's public key over
// Tor and imports it, so a later signature verification can identify the signer.
// It is a no-op unless we are in the Tor world with federation on and a SOCKS
// proxy configured, the sender is a remote onion handle, and the key is not
// already local. Safe to call from a goroutine on the operator's own stream (never
// driven by an unauthenticated request), and deduped while in flight.
func (a *App) ensureOnionSenderKey(from string) {
	if !config.Cfg.OnionMode || a.vayuPGP == nil {
		return
	}
	if !talkHostIsOnion(hostPart(from)) {
		return
	}
	if !a.talkOnionFederationEnabled(context.Background()) {
		return
	}
	socks := a.resolvedTorSocksAddr(context.Background())
	if socks == "" {
		return
	}
	if _, err := a.vayuPGP.GetPublicKey(from); err == nil {
		return // already have it
	}
	onionKeyInflightMu.Lock()
	if onionKeyInflight[from] {
		onionKeyInflightMu.Unlock()
		return
	}
	onionKeyInflight[from] = true
	onionKeyInflightMu.Unlock()
	defer func() {
		onionKeyInflightMu.Lock()
		delete(onionKeyInflight, from)
		onionKeyInflightMu.Unlock()
	}()

	client, err := newOnionHTTPClient(socks)
	if err != nil {
		return
	}
	_, _ = a.vayuPGP.LookupOnionKey(from, client)
}

// errNotOnion is returned by the guarded dialer when asked to reach a host that
// is not a .onion — the invariant that keeps this lane from ever touching
// clearnet.
var errNotOnion = errors.New("onion transport: refusing to dial a non-onion host")

// managedTalkSocksPort / managedTalkSocksAddr is the loopback SOCKS port the
// managed tor opens on demand for the onion lane (ADR-0142). Kept off tor's
// conventional 9050/9051 to avoid clashing with a system tor. Loopback-bound.
const (
	managedTalkSocksPort = 9250
	managedTalkSocksAddr = "127.0.0.1:9250"
)

// torSocksAddr returns the operator-configured Tor SOCKS proxy address (host:port)
// from the environment, or "" when unset. This is the explicit override; see
// resolvedTorSocksAddr for the effective value including the managed tor's port.
func torSocksAddr() string {
	return strings.TrimSpace(config.EnvOr("VAYUOS_TOR_SOCKS_ADDR", ""))
}

// resolvedTorSocksAddr is the SOCKS address the outbound onion lane actually
// dials: the explicit VAYUOS_TOR_SOCKS_ADDR override if set, else the managed
// tor's loopback SOCKS port when we are in the Tor world with federation on (the
// managed tor opens that port on the same condition). "" means the lane is inert.
func (a *App) resolvedTorSocksAddr(ctx context.Context) string {
	if v := torSocksAddr(); v != "" {
		return v
	}
	if config.Cfg.OnionMode && a.talkOnionFederationEnabled(ctx) {
		return managedTalkSocksAddr
	}
	return ""
}

// onionGuardedDialContext returns a DialContext that dials host:port through the
// Tor SOCKS proxy at socksAddr, but only when the host is a .onion. Any other
// host (clearnet FQDN, bare IP, empty) is refused with errNotOnion before a
// connection is opened. DNS is never resolved locally — the .onion name is handed
// to tor, which resolves it inside the network.
func onionGuardedDialContext(socksAddr string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	fwd, err := proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("onion transport: bad SOCKS proxy %q: %w", socksAddr, err)
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			host = addr
		}
		if !talkHostIsOnion(host) {
			return nil, errNotOnion
		}
		if cd, ok := fwd.(proxy.ContextDialer); ok {
			return cd.DialContext(ctx, network, addr)
		}
		return fwd.Dial(network, addr)
	}, nil
}

// newOnionHTTPClient builds an HTTP client whose every connection rides the Tor
// SOCKS proxy and can only reach .onion hosts. It refuses to follow redirects
// (a redirect to clearnet must never be chased) and never consults environment
// proxies. Returns an error when no SOCKS address is configured.
func newOnionHTTPClient(socksAddr string) (*http.Client, error) {
	socksAddr = strings.TrimSpace(socksAddr)
	if socksAddr == "" {
		return nil, errors.New("onion transport: no Tor SOCKS address configured (set VAYUOS_TOR_SOCKS_ADDR)")
	}
	dial, err := onionGuardedDialContext(socksAddr)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		Proxy:                 nil, // never honour HTTP(S)_PROXY — we route via SOCKS only
		DialContext:           dial,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          8,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 2 * time.Second,
		DisableKeepAlives:     true,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("onion transport: redirects are not followed")
		},
	}, nil
}

// onionEnvelope is the wire shape POSTed to a peer's /api/v1/talk/onion/deliver.
type onionEnvelope struct {
	To         string `json:"to"`
	From       string `json:"from"`
	Ciphertext string `json:"ciphertext"`
	TTLSeconds int    `json:"ttl_seconds"`
	Mode       string `json:"mode"`
}

// onionDeliverEnvelope hands a ciphertext envelope to the recipient's install over
// Tor by POSTing it to that .onion's delivery endpoint. peerHost is the bare
// .onion hostname (the recipient's domain); the guarded client refuses to reach
// anything else. It returns whether the peer delivered/queued it.
func onionDeliverEnvelope(ctx context.Context, client *http.Client, peerHost string, ciphertext []byte, from, to string, ttlSeconds int, mode string) (id string, delivered, queued bool, err error) {
	if client == nil {
		return "", false, false, errors.New("onion transport: no client")
	}
	if !talkHostIsOnion(peerHost) {
		return "", false, false, errNotOnion
	}
	payload, merr := json.Marshal(onionEnvelope{
		To:         to,
		From:       from,
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
		TTLSeconds: ttlSeconds,
		Mode:       mode,
	})
	if merr != nil {
		return "", false, false, merr
	}
	u := "http://" + peerHost + "/api/v1/talk/onion/deliver"
	req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if rerr != nil {
		return "", false, false, rerr
	}
	req.Header.Set("Content-Type", "application/json")
	resp, derr := client.Do(req)
	if derr != nil {
		return "", false, false, derr
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return "", false, false, fmt.Errorf("peer refused delivery (HTTP %d)", resp.StatusCode)
	}
	var out struct {
		ID        string `json:"id"`
		Delivered bool   `json:"delivered"`
		Queued    bool   `json:"queued"`
	}
	_ = json.Unmarshal(body, &out)
	return out.ID, out.Delivered, out.Queued, nil
}

// sendOverOnion performs a full outbound onion-to-onion delivery: fetch the
// recipient's key from their .onion over Tor, encrypt+sign locally, and hand the
// ciphertext to their install's delivery endpoint over Tor. Every failure branch
// returns a distinct, honest reason. It writes the HTTP response itself.
func (a *App) sendOverOnion(w http.ResponseWriter, r *http.Request, self, to, text string, ttl int, mode string) {
	ctx := r.Context()
	if !a.talkOnionFederationEnabled(ctx) {
		writeAPIError(w, r, http.StatusNotImplemented, "onion-federation-off",
			"To message a code on another .onion, turn on onion-to-onion delivery in VayuTalk settings first (experimental).", "")
		return
	}
	socksAddr := a.resolvedTorSocksAddr(ctx)
	if socksAddr == "" {
		writeAPIError(w, r, http.StatusServiceUnavailable, "onion-no-socks",
			"Onion delivery is on but no Tor SOCKS proxy is available yet. The managed tor opens one automatically; give it a moment after enabling, or set VAYUOS_TOR_SOCKS_ADDR.", "")
		return
	}
	client, cerr := newOnionHTTPClient(socksAddr)
	if cerr != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "onion-transport", "Could not open the Tor lane.", "")
		return
	}
	// Fetch the peer's key from THEIR onion over Tor. This also imports it locally
	// so the encrypt step below can resolve the recipient.
	if _, kerr := a.vayuPGP.LookupOnionKey(to, client); kerr != nil {
		writeAPIError(w, r, http.StatusNotFound, "no-recipient-key",
			"Couldn't fetch "+to+"'s key over Tor. Check the code, and that they are online and publishing a key.", "")
		return
	}
	a.ensureTalkKeypair(self)
	ciphertext, eerr := a.vayuPGP.EncryptAndSignFromEmail([]byte(text), to, self)
	if eerr != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "sender-key",
			"Couldn't prepare your encryption key. Try reloading.", "")
		return
	}
	id, delivered, queued, derr := onionDeliverEnvelope(ctx, client, hostPart(to), ciphertext, self, to, ttl, mode)
	if derr != nil {
		writeAPIError(w, r, http.StatusBadGateway, "onion-delivery-failed",
			"Couldn't deliver to "+to+" over Tor. They may be offline or not accepting messages.", "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"id": id, "delivered": delivered, "queued": queued})
}
