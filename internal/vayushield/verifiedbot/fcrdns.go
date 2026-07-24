package verifiedbot

import (
	"context"
	"net/netip"
	"strings"
	"time"
)

// Resolver is the DNS surface FCrDNS needs; *net.Resolver satisfies it. Injected
// so tests use a deterministic fake and never touch the network.
type Resolver interface {
	LookupAddr(ctx context.Context, addr string) ([]string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// fcrdnsJob is a queued forward-confirmed reverse-DNS verification.
type fcrdnsJob struct {
	ip     netip.Addr
	vendor *vendorDef
}

// kickFCrDNS schedules an off-hot-path FCrDNS verification for ip against the
// vendor's PTR suffixes. It is single-flight per IP and non-blocking: if the
// worker queue is full the job is dropped (the IP simply stays unverified and is
// handled on UA), so a spoof flood of distinct IPs can never turn into an
// unbounded pile of blocking DNS lookups (a self-inflicted DoS).
func (v *Verifier) kickFCrDNS(ip netip.Addr, vd *vendorDef) {
	if v.resolver == nil || len(vd.ptrSuffix) == 0 {
		return
	}
	if _, busy := v.pending.LoadOrStore(ip, struct{}{}); busy {
		return
	}
	select {
	case v.jobs <- fcrdnsJob{ip: ip, vendor: vd}:
	default:
		v.pending.Delete(ip) // queue full — allow a later re-kick
	}
}

// runFCrDNSWorker drains the FCrDNS queue until done is closed.
func (v *Verifier) runFCrDNSWorker(done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case job := <-v.jobs:
			v.doFCrDNS(job)
		}
	}
}

func (v *Verifier) doFCrDNS(job fcrdnsJob) {
	defer v.pending.Delete(job.ip)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if v.confirmFCrDNS(ctx, job.ip, job.vendor) {
		v.cache.put(job.ip, Verified, job.vendor.name, job.vendor.class, v.now().Add(positiveTTL))
		v.logf("verifiedbot: %s FCrDNS-confirmed %s", job.vendor.name, job.ip)
		return
	}
	// Claimed this vendor's UA but reverse DNS did not confirm — spoof suspect.
	v.cache.put(job.ip, SpoofSuspect, job.vendor.name, job.vendor.class, v.now().Add(negativeTTL))
}

// confirmFCrDNS performs the forward-confirmed reverse-DNS check: reverse-lookup
// the IP, require a PTR ending in one of the vendor's suffixes, then forward
// resolve that hostname and confirm it maps back to the SAME IP.
func (v *Verifier) confirmFCrDNS(ctx context.Context, ip netip.Addr, vd *vendorDef) bool {
	names, err := v.resolver.LookupAddr(ctx, ip.String())
	if err != nil {
		return false
	}
	want := ip.Unmap()
	for _, name := range names {
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
		if host == "" || !hasVendorSuffix(host, vd.ptrSuffix) {
			continue
		}
		addrs, err := v.resolver.LookupHost(ctx, host)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ra, err := netip.ParseAddr(strings.TrimSpace(a))
			if err != nil {
				continue
			}
			if ra.Unmap() == want {
				return true
			}
		}
	}
	return false
}

// hasVendorSuffix reports whether host ends in one of the vendor's PTR suffixes
// on a DNS-label boundary (so "evilgooglebot.com" does not match ".googlebot.com").
func hasVendorSuffix(host string, suffixes []string) bool {
	for _, s := range suffixes {
		s = strings.ToLower(s)
		dotted := "." + strings.TrimPrefix(s, ".")
		if host == strings.TrimPrefix(dotted, ".") || strings.HasSuffix(host, dotted) {
			return true
		}
	}
	return false
}
