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
	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("vayumail: load TLS keypair: %w", err)
		}
		return &tlsProvider{
			config: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
			mode:   tlsModeStatic,
			note:   "using operator-provided certificate (" + cfg.TLSCertFile + ")",
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

// loadTLSConfig builds the TLS configuration used by the SMTP/submission/IMAP
// listeners. It prefers an operator-provided certificate (cfg.TLSCertFile +
// cfg.TLSKeyFile, e.g. a Let's Encrypt pair); when none is configured it falls
// back to an in-memory self-signed certificate for cfg.Hostname. A self-signed
// cert is sufficient for opportunistic STARTTLS — sending MTAs encrypt without
// verifying — and lets the receive side offer TLS immediately, while operators
// can drop in a CA-signed cert (or enable ACME) for verified client use.
//
// It is retained for callers/tests that only need the *tls.Config; the engine
// uses buildTLSProvider so it can also drive ACME and diagnostics.
func loadTLSConfig(cfg Config) (*tls.Config, error) {
	var cert tls.Certificate
	var err error
	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		cert, err = tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("vayumail: load TLS keypair: %w", err)
		}
	} else {
		cert, err = selfSignedCert(cfg.Hostname)
		if err != nil {
			return nil, fmt.Errorf("vayumail: self-signed cert: %w", err)
		}
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

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
