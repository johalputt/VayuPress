package mail

import (
	"context"
	"net"
	"strings"
	"time"
)

// DNSRecord is a single record VayuOS asks the operator (or a provider API) to
// publish for mail to authenticate correctly.
type DNSRecord struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Priority int    `json:"priority,omitempty"`
}

// PlannedRecords returns the MX/SPF/DKIM/DMARC records VayuMail expects for the
// configured domain. dkimTXT is the value from DKIM.PublicTXT(); dkimName is
// DKIM.RecordName().
func PlannedRecords(cfg Config, dkimName, dkimTXT string) []DNSRecord {
	recs := []DNSRecord{
		{Type: "MX", Name: cfg.Domain + ".", Value: cfg.Hostname + ".", Priority: 10},
	}
	if cfg.SPFEnabled {
		recs = append(recs, DNSRecord{Type: "TXT", Name: cfg.Domain + ".", Value: "v=spf1 a mx ~all"})
	}
	if cfg.DKIMEnabled && dkimTXT != "" {
		recs = append(recs, DNSRecord{Type: "TXT", Name: dkimName + ".", Value: dkimTXT})
	}
	if cfg.DMARCEnabled {
		recs = append(recs, DNSRecord{
			Type:  "TXT",
			Name:  "_dmarc." + cfg.Domain + ".",
			Value: "v=DMARC1; p=quarantine; rua=mailto:postmaster@" + cfg.Domain + "; adkim=s; aspf=s",
		})
	}
	return recs
}

// RecordHealth is the live verification state of one expected record.
type RecordHealth struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Found   string `json:"found"`
	Message string `json:"message"`
}

// DomainHealth aggregates DNS checks for a domain.
type DomainHealth struct {
	Domain    string         `json:"domain"`
	Records   []RecordHealth `json:"records"`
	AllOK     bool           `json:"all_ok"`
	CheckedAt time.Time      `json:"checked_at"`
}

// CheckHealth performs live DNS lookups (MX, SPF, DKIM, DMARC) and reports
// whether each expected record is present. Lookups are best-effort and bounded.
func CheckHealth(ctx context.Context, cfg Config, dkimName string) *DomainHealth {
	res := &DomainHealth{Domain: cfg.Domain, CheckedAt: time.Now().UTC(), AllOK: true}
	resolver := net.Resolver{}

	// MX
	mxOK := false
	var mxFound string
	if mxs, err := resolver.LookupMX(ctx, cfg.Domain); err == nil && len(mxs) > 0 {
		mxOK = true
		mxFound = strings.TrimSuffix(mxs[0].Host, ".")
	}
	res.Records = append(res.Records, health("MX", cfg.Domain, mxOK, mxFound))

	// SPF
	if cfg.SPFEnabled {
		res.Records = append(res.Records, txtContains(ctx, &resolver, "SPF", cfg.Domain, "v=spf1"))
	}
	// DKIM
	if cfg.DKIMEnabled {
		res.Records = append(res.Records, txtContains(ctx, &resolver, "DKIM", dkimName, "v=DKIM1"))
	}
	// DMARC
	if cfg.DMARCEnabled {
		res.Records = append(res.Records, txtContains(ctx, &resolver, "DMARC", "_dmarc."+cfg.Domain, "v=DMARC1"))
	}

	for _, r := range res.Records {
		if !r.OK {
			res.AllOK = false
		}
	}
	return res
}

func health(typ, name string, ok bool, found string) RecordHealth {
	msg := "missing"
	if ok {
		msg = "ok"
	}
	return RecordHealth{Type: typ, Name: name, OK: ok, Found: found, Message: msg}
}

func txtContains(ctx context.Context, r *net.Resolver, typ, name, needle string) RecordHealth {
	txts, err := r.LookupTXT(ctx, name)
	if err != nil {
		return health(typ, name, false, "")
	}
	for _, t := range txts {
		if strings.Contains(t, needle) {
			return health(typ, name, true, t)
		}
	}
	return health(typ, name, false, strings.Join(txts, " | "))
}
