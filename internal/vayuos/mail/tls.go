package mail

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

// loadTLSConfig builds the TLS configuration used by the SMTP/submission/IMAP
// listeners. It prefers an operator-provided certificate (cfg.TLSCertFile +
// cfg.TLSKeyFile, e.g. a Let's Encrypt pair); when none is configured it falls
// back to an in-memory self-signed certificate for cfg.Hostname. A self-signed
// cert is sufficient for opportunistic STARTTLS — sending MTAs encrypt without
// verifying — and lets the receive side offer TLS immediately, while operators
// can drop in a CA-signed cert for verified client (Thunderbird/mobile) use.
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
