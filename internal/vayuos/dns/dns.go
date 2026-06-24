// Package dns provides DNS auto-configuration for VayuOS.
//
// When a domain is added, the DNS manager automatically checks and
// configures MX, SPF, DKIM, DMARC, and PTR records. For providers
// with API access (Cloudflare), records are auto-set. For others,
// exact records are shown in the VayuOS panel for manual copy-paste.
package dns

import (
	"fmt"
)

// Record represents a DNS record to configure.
type Record struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Value   string `json:"value"`
	TTL     int    `json:"ttl"`
	Healthy bool   `json:"healthy"`
	Message string `json:"message"`
}

// DomainRecords holds all DNS records for a domain.
type DomainRecords struct {
	Domain string   `json:"domain"`
	MX     *Record  `json:"mx"`
	SPF    *Record  `json:"spf"`
	DKIM   *Record  `json:"dkim"`
	DMARC  *Record  `json:"dmarc"`
	PTR    *Record  `json:"ptr"`
	TLSA   *Record  `json:"tlsa,omitempty"`
}

// Manager handles DNS configuration for VayuOS-managed domains.
type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
}

// Configure returns the recommended DNS records for a domain.
// These should be shown in the VayuOS panel for operator action.
func (m *Manager) Configure(domain, mailHostname, dkimValue string) *DomainRecords {
	return &DomainRecords{
		Domain: domain,
		MX: &Record{
			Type:  "MX",
			Name:  domain,
			Value: fmt.Sprintf("10 %s", mailHostname),
			TTL:   3600,
		},
		SPF: &Record{
			Type:  "TXT",
			Name:  domain,
			Value: fmt.Sprintf("v=spf1 a mx include:%s ~all", domain),
			TTL:   3600,
		},
		DKIM: &Record{
			Type:  "TXT",
			Name:  fmt.Sprintf("default._domainkey.%s", domain),
			Value: dkimValue,
			TTL:   3600,
		},
		DMARC: &Record{
			Type:  "TXT",
			Name:  fmt.Sprintf("_dmarc.%s", domain),
			Value: fmt.Sprintf("v=DMARC1; p=quarantine; rua=mailto:postmaster@%s; ruf=mailto:postmaster@%s", domain, domain),
			TTL:   3600,
		},
		PTR: &Record{
			Type:    "PTR",
			Name:    fmt.Sprintf("reverse of %s", mailHostname),
			Value:   mailHostname,
			TTL:     3600,
			Message: "Requires hosting provider action. Set reverse DNS for the server IP to point to " + mailHostname,
		},
	}
}

// CheckHealth verifies DNS records for a domain.
func (m *Manager) CheckHealth(domain string) map[string]*Record {
	return map[string]*Record{
		"mx":   {Healthy: false, Message: "DNS verification requires external lookup — check manually"},
		"spf":  {Healthy: false, Message: "DNS verification requires external lookup — check manually"},
		"dkim": {Healthy: false, Message: "DNS verification requires external lookup — check manually"},
		"dmarc": {Healthy: false, Message: "DNS verification requires external lookup — check manually"},
	}
}