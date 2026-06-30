package mail

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

// certForHosts builds a throwaway leaf certificate carrying the given DNS names.
func certForHosts(t *testing.T, hosts ...string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: hosts[0]},
		DNSNames:     hosts,
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(1<<31, 0),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, Leaf: leaf}
}

func TestTLSCertCovers(t *testing.T) {
	e := &Engine{}
	e.tlsConf = &tls.Config{Certificates: []tls.Certificate{certForHosts(t, "mail.example.com", "example.com")}}
	cases := map[string]bool{
		"mail.example.com":  true,
		"example.com":       true,
		"MAIL.EXAMPLE.COM":  true, // case-insensitive
		"other.example.com": false,
		"example.org":       false,
		"":                  false,
	}
	for host, want := range cases {
		if got := e.TLSCertCovers(host); got != want {
			t.Errorf("TLSCertCovers(%q)=%v want %v", host, got, want)
		}
	}

	// Wildcard cert covers one label to the left, not the bare apex or two labels.
	e.tlsConf = &tls.Config{Certificates: []tls.Certificate{certForHosts(t, "*.example.com")}}
	wild := map[string]bool{
		"mail.example.com": true,
		"a.b.example.com":  false,
		"example.com":      false,
	}
	for host, want := range wild {
		if got := e.TLSCertCovers(host); got != want {
			t.Errorf("wildcard TLSCertCovers(%q)=%v want %v", host, got, want)
		}
	}
}
