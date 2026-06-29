package mail

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// tlsMode describes the provenance of the certificate a listener presents. It
// drives operator-facing diagnostics: only "static" and "acme" certificates are
// trusted by mainstream mail clients (the Gmail app, Apple Mail, Thunderbird,
// Outlook). A "selfsigned" certificate still encrypts opportunistic STARTTLS
// between MTAs, but mobile/desktop clients reject it — surfacing as the dreaded
// "Couldn't open connection to server".
type tlsMode string

const (
	tlsModeNone       tlsMode = "none"       // TLS unavailable
	tlsModeStatic     tlsMode = "static"     // operator-provided CA-signed cert
	tlsModeACME       tlsMode = "acme"       // auto-provisioned (Let's Encrypt)
	tlsModeSelfSigned tlsMode = "selfsigned" // in-memory fallback
)

// trusted reports whether the active certificate is one a real mail client will
// accept without a manual exception.
func (m tlsMode) trusted() bool { return m == tlsModeStatic || m == tlsModeACME }

// tlsProvider bundles the *tls.Config shared by every mail TLS listener
// (implicit-TLS 993/995/465 and STARTTLS on 25/143/110/587) with the metadata
// the engine needs for diagnostics. In ACME mode it also carries the HTTP-01
// challenge handler that must be served on :80 for issuance/renewal to succeed.
type tlsProvider struct {
	config      *tls.Config
	mode        tlsMode
	manager     *autocert.Manager
	httpHandler http.Handler // non-nil only in ACME mode
	hosts       []string     // hostnames the certificate is provisioned for
	note        string       // human-readable explanation of the chosen mode
}

// trusted reports whether real mail clients will accept the served certificate.
func (p *tlsProvider) trusted() bool { return p != nil && p.mode.trusted() }

// buildTLSProvider selects, in priority order:
//
//  1. a static operator certificate (TLSCertFile/TLSKeyFile) — trusted;
//  2. native ACME auto-provisioning (ACMEEnabled) — trusted, auto-renewing;
//  3. an in-memory self-signed certificate — encrypts, but clients reject it.
//
// It never returns nil so the listeners always have TLS to offer; any soft
// failure (e.g. ACME misconfiguration) degrades to the self-signed fallback and
// is recorded in the provider note rather than aborting mail startup.
func buildTLSProvider(cfg Config) (*tlsProvider, error) {
	// 1) Operator-supplied certificate wins outright.
	//
	// The keypair is served through a hot-reloading loader rather than baked
	// into Certificates once at boot. This makes the certbot/Let's Encrypt path
	// genuinely "set and forget": when the certificate is renewed on disk (every
	// ~60 days) VayuMail picks up the new files on the next handshake, with NO
	// restart required. Previously a renewed certificate kept serving the stale
	// (eventually expired) copy until the operator remembered to restart — a
	// silent path back to "Couldn't open connection to server".
	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		rc, err := newReloadingCert(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			// A configured-but-unreadable certificate is the single most common
			// "I set the cert and it's still self-signed" trap: Let's Encrypt
			// stores privkey.pem as root:root 0600, but VayuMail runs as a non-root
			// service, so LoadX509KeyPair fails with permission denied. Rather than
			// hard-fail mail startup, degrade to the self-signed fallback and record
			// the exact reason + fix so it surfaces on the Connect tab.
			fb, ferr := selfSignedProvider(cfg)
			if ferr != nil {
				return nil, fmt.Errorf("vayumail: load TLS keypair: %w", err)
			}
			reason := err.Error()
			if os.IsPermission(err) || strings.Contains(reason, "permission denied") {
				reason = "the mail service user cannot read " + cfg.TLSKeyFile +
					" (Let's Encrypt keys are root-only by default). Re-run deploy/vayumail-setup.sh, which copies the certificate to a service-readable location"
			}
			fb.note = "configured TLS certificate could not be loaded: " + reason + "; using self-signed fallback"
			return fb, nil
		}
		return &tlsProvider{
			config: &tls.Config{GetCertificate: rc.getCertificate, MinVersion: tls.VersionTLS12},
			mode:   tlsModeStatic,
			note:   "using operator-provided certificate (" + cfg.TLSCertFile + "); auto-reloads on renewal",
		}, nil
	}

	// 2) Native ACME (Let's Encrypt) auto-provisioning.
	if cfg.ACMEEnabled {
		if p, err := newACMEProvider(cfg); err == nil {
			return p, nil
		} else if p != nil {
			// Soft failure: fall through to self-signed but keep the reason.
			fb, ferr := selfSignedProvider(cfg)
			if ferr != nil {
				return nil, ferr
			}
			fb.note = "ACME unavailable (" + err.Error() + "); using self-signed fallback"
			return fb, nil
		} else {
			return nil, err
		}
	}

	// 3) Self-signed fallback.
	return selfSignedProvider(cfg)
}

// selfSignedProvider mints the in-memory self-signed fallback config.
func selfSignedProvider(cfg Config) (*tlsProvider, error) {
	cert, err := selfSignedCert(cfg.Hostname)
	if err != nil {
		return nil, fmt.Errorf("vayumail: self-signed cert: %w", err)
	}
	return &tlsProvider{
		config: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		mode:   tlsModeSelfSigned,
		note:   "no trusted certificate configured; mail clients will reject this self-signed certificate",
	}, nil
}

// reloadingCert serves an operator-supplied (e.g. certbot/Let's Encrypt) keypair
// from disk and transparently reloads it when the underlying files change. It is
// wired into tls.Config.GetCertificate so every mail TLS listener
// (993/995/465 implicit TLS and STARTTLS on 25/143/110/587) picks up a renewed
// certificate on the next handshake — no process restart, no expired-cert
// outage. Reload attempts are throttled (reloadCheckEvery) so a busy server
// does not stat the filesystem on every handshake, and a failed reload (e.g.
// certbot mid-write) keeps serving the last-good certificate.
type reloadingCert struct {
	certFile, keyFile string

	mu        sync.RWMutex
	cached    *tls.Certificate
	certMod   time.Time
	keyMod    time.Time
	lastCheck time.Time
}

// reloadCheckEvery bounds how often the cert files are stat-ed for changes.
const reloadCheckEvery = 30 * time.Second

// newReloadingCert loads the keypair once (failing fast if it is missing or
// malformed) and returns a loader ready to serve and hot-reload it.
func newReloadingCert(certFile, keyFile string) (*reloadingCert, error) {
	rc := &reloadingCert{certFile: certFile, keyFile: keyFile}
	if err := rc.reload(); err != nil {
		return nil, err
	}
	return rc, nil
}

// reload reads the keypair from disk and atomically swaps it in. On any error
// the previously cached certificate is left untouched.
func (rc *reloadingCert) reload() error {
	cert, err := tls.LoadX509KeyPair(rc.certFile, rc.keyFile)
	if err != nil {
		return err
	}
	var certMod, keyMod time.Time
	if fi, serr := os.Stat(rc.certFile); serr == nil {
		certMod = fi.ModTime()
	}
	if fi, serr := os.Stat(rc.keyFile); serr == nil {
		keyMod = fi.ModTime()
	}
	rc.mu.Lock()
	rc.cached = &cert
	rc.certMod, rc.keyMod, rc.lastCheck = certMod, keyMod, time.Now()
	rc.mu.Unlock()
	return nil
}

// getCertificate is the tls.Config.GetCertificate callback. It serves the
// cached certificate, reloading first if the files changed since the last check
// (rate-limited by reloadCheckEvery).
func (rc *reloadingCert) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	rc.maybeReload()
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.cached, nil
}

// maybeReload reloads the keypair when either file's modification time has
// changed, at most once per reloadCheckEvery window. A failed reload keeps the
// last-good certificate (so a partial certbot write never breaks TLS).
func (rc *reloadingCert) maybeReload() {
	rc.mu.RLock()
	since := time.Since(rc.lastCheck)
	prevCertMod, prevKeyMod := rc.certMod, rc.keyMod
	rc.mu.RUnlock()
	if since < reloadCheckEvery {
		return
	}

	ci, errC := os.Stat(rc.certFile)
	ki, errK := os.Stat(rc.keyFile)
	if errC != nil || errK != nil {
		// Files temporarily unavailable (e.g. mid-renewal). Keep serving the
		// cached cert; just bump lastCheck so we retry next window.
		rc.mu.Lock()
		rc.lastCheck = time.Now()
		rc.mu.Unlock()
		return
	}
	if ci.ModTime().Equal(prevCertMod) && ki.ModTime().Equal(prevKeyMod) {
		rc.mu.Lock()
		rc.lastCheck = time.Now()
		rc.mu.Unlock()
		return
	}
	if err := rc.reload(); err != nil {
		// Keep the old certificate; retry on the next window.
		rc.mu.Lock()
		rc.lastCheck = time.Now()
		rc.mu.Unlock()
	}
}

// newACMEProvider configures autocert for the mail hostname(s). On a hard
// configuration error (no usable hostname) it returns (nil, err); on a softer
// error where the caller may still want a fallback it returns (provider!=nil
// only for the success case). The HTTP-01 challenge responder it exposes via
// httpHandler must be served on cfg.ACMEHTTPAddr by the engine.
func newACMEProvider(cfg Config) (*tlsProvider, error) {
	hosts := acmeHosts(cfg)
	if len(hosts) == 0 {
		// Distinguishable soft error: caller falls back to self-signed.
		return &tlsProvider{}, fmt.Errorf("no mail hostname to certify (set DOMAIN/Hostname)")
	}
	cacheDir := cfg.ACMECacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(cfg.StorageDir, "acme")
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return &tlsProvider{}, fmt.Errorf("acme cache dir %s: %w", cacheDir, err)
	}
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(cacheDir),
		HostPolicy: autocert.HostWhitelist(hosts...),
		Email:      cfg.ACMEEmail,
	}
	if cfg.ACMEDirectoryURL != "" {
		m.Client = &acme.Client{DirectoryURL: cfg.ACMEDirectoryURL}
	}

	// A self-signed certificate is kept as a last-resort responder so the TLS
	// listeners still answer while issuance is settling (the HTTP-01 challenge
	// may take a few seconds, or be briefly unreachable after a restart). MTAs
	// using opportunistic STARTTLS keep encrypting; once the real certificate is
	// issued and cached, autocert serves it transparently on the next handshake.
	fallback, err := selfSignedCert(cfg.Hostname)
	if err != nil {
		return &tlsProvider{}, fmt.Errorf("acme fallback cert: %w", err)
	}

	base := m.TLSConfig() // sets GetCertificate + ALPN (acme-tls/1) for TLS-ALPN-01
	base.MinVersion = tls.VersionTLS12
	autocertGet := base.GetCertificate
	base.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		cert, gerr := autocertGet(hello)
		if gerr == nil {
			return cert, nil
		}
		// Never mask a TLS-ALPN-01 challenge handshake with a normal cert.
		for _, proto := range hello.SupportedProtos {
			if proto == acme.ALPNProto {
				return nil, gerr
			}
		}
		fb := fallback
		return &fb, nil
	}

	return &tlsProvider{
		config:      base,
		mode:        tlsModeACME,
		manager:     m,
		httpHandler: m.HTTPHandler(nil),
		hosts:       hosts,
		note:        "auto-provisioning a trusted certificate via ACME for " + strings.Join(hosts, ", "),
	}, nil
}

// warmUp kicks off certificate issuance in the background so the trusted
// certificate is cached before the first real client connects (rather than
// lazily on the first handshake). Errors are non-fatal: the listeners serve the
// self-signed fallback until issuance succeeds, then pick up the real cert.
func (p *tlsProvider) warmUp(ctx context.Context) {
	if p == nil || p.manager == nil {
		return
	}
	for _, h := range p.hosts {
		host := h
		go func() {
			// autocert.GetCertificate only inspects ServerName here; issuance
			// runs against the configured ACME directory and caches on success.
			_, _ = p.manager.GetCertificate(&tls.ClientHelloInfo{ServerName: host})
		}()
	}
	_ = ctx
}

// acmeHosts returns the deduplicated, non-empty hostnames to certify.
func acmeHosts(cfg Config) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		h = strings.TrimSpace(strings.ToLower(h))
		if h == "" || h == "localhost" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	add(cfg.Hostname)
	for _, h := range cfg.ACMEExtraHosts {
		add(h)
	}
	return out
}

// loadTLSConfig is intentionally removed: the engine now uses buildTLSProvider,
// which supersedes it (static cert, ACME, and the self-signed fallback). Tests
// obtain a *tls.Config via buildTLSProvider(...).config.

// selfSignedCert mints a short-lived in-memory ECDSA certificate for host. The
// private key never touches disk.
func selfSignedCert(host string) (tls.Certificate, error) {
	if host == "" {
		host = "localhost"
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		DNSNames:              []string{host},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return tls.X509KeyPair(certPEM, keyPEM)
}
