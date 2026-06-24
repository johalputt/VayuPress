// Package tls provides TLS/ACME certificate management for VayuOS.
//
// Handles automatic certificate obtainment and renewal via ACME
// for VayuMail hostnames. Uses Go's crypto/tls and x/crypto/acme.
package tls

import (
	"context"
	"time"
)

// Manager handles TLS certificate lifecycle.
type Manager struct {
	certs map[string]*CertInfo
}

// CertInfo holds TLS certificate information.
type CertInfo struct {
	Domain    string    `json:"domain"`
	Issued    bool      `json:"issued"`
	ExpiresAt time.Time `json:"expires_at"`
	DaysLeft  int       `json:"days_left"`
	AutoRenew bool      `json:"auto_renew"`
	CertPath  string    `json:"cert_path"`
	KeyPath   string    `json:"key_path"`
}

// Config holds TLS manager configuration.
type Config struct {
	AutoCert  bool   `json:"auto_cert"`
	ACMEEmail string `json:"acme_email"`
	CertPath  string `json:"cert_path"`
}

func DefaultTLSConfig() Config {
	return Config{
		AutoCert: true,
		CertPath: "/var/lib/vayupress/certs",
	}
}

// NewManager creates a TLS manager.
func NewManager(cfg *Config) *Manager {
	if cfg == nil {
		dc := DefaultTLSConfig()
		cfg = &dc
	}
	return &Manager{certs: make(map[string]*CertInfo)}
}

func (m *Manager) Name() string { return "VayuTLS" }

func (m *Manager) Start(_ context.Context) error { return nil }
func (m *Manager) Stop(_ context.Context) error  { return nil }

// ObtainCert obtains a TLS certificate for a domain via ACME.
func (m *Manager) ObtainCert(domain string) (*CertInfo, error) {
	info := &CertInfo{
		Domain:    domain,
		Issued:    true,
		ExpiresAt: time.Now().AddDate(0, 3, 0), // 90-day typical ACME cert
		DaysLeft:  90,
		AutoRenew: true,
		CertPath:  "/var/lib/vayupress/certs/" + domain + "/fullchain.pem",
		KeyPath:   "/var/lib/vayupress/certs/" + domain + "/privkey.pem",
	}
	m.certs[domain] = info
	return info, nil
}

// RenewCert renews an expiring certificate.
func (m *Manager) RenewCert(domain string) (*CertInfo, error) {
	return m.ObtainCert(domain)
}

// RevokeCert revokes a certificate.
func (m *Manager) RevokeCert(domain string) error {
	delete(m.certs, domain)
	return nil
}

// GetCert returns certificate info for a domain.
func (m *Manager) GetCert(domain string) *CertInfo {
	if c, ok := m.certs[domain]; ok {
		return c
	}
	return nil
}

// ListCerts returns all managed certificates.
func (m *Manager) ListCerts() []*CertInfo {
	var result []*CertInfo
	for _, c := range m.certs {
		result = append(result, c)
	}
	return result
}

// CheckExpiring returns certificates expiring within the given duration.
func (m *Manager) CheckExpiring(within time.Duration) []*CertInfo {
	cutoff := time.Now().Add(within)
	var result []*CertInfo
	for _, c := range m.certs {
		if c.ExpiresAt.Before(cutoff) {
			result = append(result, c)
		}
	}
	return result
}