package verifiedbot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// ── test doubles ───────────────────────────────────────────────────────────────

type fakeRT struct{ resp map[string]string }

func (f fakeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	body, ok := f.resp[req.URL.String()]
	code := http.StatusOK
	if !ok {
		code, body = http.StatusNotFound, ""
	}
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func fakeClient(resp map[string]string) *http.Client {
	return &http.Client{Transport: fakeRT{resp: resp}}
}

type fakeResolver struct {
	ptr  map[string][]string // ip -> PTR names
	host map[string][]string // name -> ips
}

func (f fakeResolver) LookupAddr(_ context.Context, addr string) ([]string, error) {
	if n, ok := f.ptr[addr]; ok {
		return n, nil
	}
	return nil, fmt.Errorf("no PTR for %s", addr)
}

func (f fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if a, ok := f.host[host]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("no A for %s", host)
}

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

const googlebotUA = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"

// ── parsing + set ──────────────────────────────────────────────────────────────

func TestParsePrefixesGoogleShape(t *testing.T) {
	data := []byte(`{"creationTime":"x","prefixes":[{"ipv4Prefix":"66.249.64.0/19"},{"ipv6Prefix":"2001:4860:4801::/48"}]}`)
	pfx, err := parsePrefixes(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(pfx) != 2 {
		t.Fatalf("want 2 prefixes, got %d", len(pfx))
	}
	set := newCIDRSet(pfx)
	if !set.contains(addr("66.249.66.1")) {
		t.Error("66.249.66.1 must be in Googlebot range")
	}
	if set.contains(addr("8.8.8.8")) {
		t.Error("8.8.8.8 must NOT be in Googlebot range")
	}
	if !set.contains(addr("2001:4860:4801::abcd")) {
		t.Error("v6 address must match the v6 prefix")
	}
}

func TestParsePrefixesArrayShape(t *testing.T) {
	pfx, err := parsePrefixes([]byte(`["1.2.3.4","5.6.7.0/24"]`))
	if err != nil {
		t.Fatal(err)
	}
	set := newCIDRSet(pfx)
	if !set.contains(addr("1.2.3.4")) || !set.contains(addr("5.6.7.200")) {
		t.Error("array feed IPs must match")
	}
	if set.contains(addr("1.2.3.5")) {
		t.Error("host route 1.2.3.4/32 must not match a neighbour")
	}
}

func TestParsePrefixesRejectsGarbage(t *testing.T) {
	if _, err := parsePrefixes([]byte(`not json`)); err == nil {
		t.Error("garbage feed must error (so last-good is kept)")
	}
	if _, err := parsePrefixes([]byte(`{"prefixes":[]}`)); err == nil {
		t.Error("empty prefix list must error")
	}
}

func TestHasVendorSuffixBoundary(t *testing.T) {
	suf := []string{".googlebot.com"}
	if !hasVendorSuffix("crawl-66-249-66-1.googlebot.com", suf) {
		t.Error("legit googlebot PTR must match")
	}
	if hasVendorSuffix("evilgooglebot.com", suf) {
		t.Error("evilgooglebot.com must NOT match .googlebot.com (label boundary)")
	}
}

// ── Classify behaviour ─────────────────────────────────────────────────────────

func TestClassifyUnknownForNonCrawler(t *testing.T) {
	v := New(Config{})
	got, _, _ := v.Classify(addr("1.2.3.4"), "Mozilla/5.0 (Windows NT 10.0) Chrome/130")
	if got != Unknown {
		t.Fatalf("a normal browser must be Unknown, got %v", got)
	}
}

func TestClassifyUAOnlyVendorIsUnverifiable(t *testing.T) {
	v := New(Config{})
	got, name, class := v.Classify(addr("1.2.3.4"), "Mozilla/5.0 (compatible; Bytespider)")
	if got != Unverifiable || class != ClassAIAgent {
		t.Fatalf("UA-only crawler must be Unverifiable ai_agent, got %v/%s", got, class)
	}
	_ = name
}

func TestClassifyFeedNotLoadedIsSEOSafe(t *testing.T) {
	// No client, no disk cache => Google feed never loads => a Googlebot UA must
	// still be Unverifiable (allow on UA), never SpoofSuspect. This is the "don't
	// de-index because WE couldn't fetch the feed" guarantee.
	v := New(Config{})
	got, _, _ := v.Classify(addr("66.249.66.1"), googlebotUA)
	if got != Unverifiable {
		t.Fatalf("with no feed data, a crawler UA must be Unverifiable, got %v", got)
	}
}

func TestClassifyVerifiedByFeed(t *testing.T) {
	feeds := map[string]string{
		"https://developers.google.com/crawling/ipranges/common-crawlers.json": `{"prefixes":[{"ipv4Prefix":"66.249.64.0/19"}]}`,
	}
	v := New(Config{Client: fakeClient(feeds), CacheDir: t.TempDir()})
	v.refreshAll(context.Background())

	got, name, _ := v.Classify(addr("66.249.66.1"), googlebotUA)
	if got != Verified || name != "Googlebot" {
		t.Fatalf("Googlebot from a real Google IP must be Verified, got %v/%s", got, name)
	}
	// Cache hit path returns the same.
	if g2, _, _ := v.Classify(addr("66.249.66.1"), googlebotUA); g2 != Verified {
		t.Fatalf("cached verdict must remain Verified, got %v", g2)
	}
}

func TestDiskCacheSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	feeds := map[string]string{
		"https://developers.google.com/crawling/ipranges/common-crawlers.json": `{"prefixes":[{"ipv4Prefix":"66.249.64.0/19"}]}`,
	}
	v1 := New(Config{Client: fakeClient(feeds), CacheDir: dir})
	v1.refreshAll(context.Background())

	// A brand-new verifier with NO client but the same cache dir must load the
	// last-good feed from disk and verify immediately (offline / post-restart).
	v2 := New(Config{CacheDir: dir})
	if got, _, _ := v2.Classify(addr("66.249.66.1"), googlebotUA); got != Verified {
		t.Fatalf("disk-cached feed must verify after restart, got %v", got)
	}
}

func TestSpoofSuspectViaFCrDNSDisproof(t *testing.T) {
	// Google feed loaded; a googlebot UA arrives from an IP NOT in the feed whose
	// reverse DNS does NOT resolve to a googlebot host -> after the (here,
	// synchronous) FCrDNS check it is cached SpoofSuspect.
	feeds := map[string]string{
		"https://developers.google.com/crawling/ipranges/common-crawlers.json": `{"prefixes":[{"ipv4Prefix":"66.249.64.0/19"}]}`,
	}
	res := fakeResolver{
		ptr:  map[string][]string{"203.0.113.9": {"host.evil.example."}},
		host: map[string][]string{"host.evil.example": {"203.0.113.9"}},
	}
	v := New(Config{Client: fakeClient(feeds), CacheDir: t.TempDir(), Resolver: res})
	v.refreshAll(context.Background())

	spoof := addr("203.0.113.9")
	// First hit is SEO-safe (Unverifiable) while the backstop is pending.
	if got, _, _ := v.Classify(spoof, googlebotUA); got != Unverifiable {
		t.Fatalf("first hit from an unlisted IP should be Unverifiable, got %v", got)
	}
	// Drive the FCrDNS check deterministically instead of racing the worker pool.
	v.doFCrDNS(fcrdnsJob{ip: spoof, vendor: mustVendor(t, "Googlebot")})
	if got, _, _ := v.Classify(spoof, googlebotUA); got != SpoofSuspect {
		t.Fatalf("a googlebot UA from a non-google IP must become SpoofSuspect, got %v", got)
	}
}

func TestFCrDNSConfirmsRealCrawler(t *testing.T) {
	// Yandex has no feed; identity is confirmed purely by forward-confirmed
	// reverse DNS. A matching PTR that forward-resolves back to the same IP -> Verified.
	ip := "77.88.55.70"
	res := fakeResolver{
		ptr:  map[string][]string{ip: {"77-88-55-70.spider.yandex.com."}},
		host: map[string][]string{"77-88-55-70.spider.yandex.com": {ip}},
	}
	v := New(Config{Resolver: res})
	yandexUA := "Mozilla/5.0 (compatible; YandexBot/3.0; +http://yandex.com/bots)"

	if got, _, _ := v.Classify(addr(ip), yandexUA); got != Unverifiable {
		t.Fatalf("first Yandex hit should be Unverifiable pending FCrDNS, got %v", got)
	}
	v.doFCrDNS(fcrdnsJob{ip: addr(ip), vendor: mustVendor(t, "YandexBot")})
	if got, name, _ := v.Classify(addr(ip), yandexUA); got != Verified || name != "YandexBot" {
		t.Fatalf("FCrDNS-confirmed Yandex must be Verified, got %v/%s", got, name)
	}
}

func TestFCrDNSForwardMismatchIsSpoof(t *testing.T) {
	// PTR ends in the right suffix, but the forward lookup resolves to a DIFFERENT
	// IP — the classic partial-spoof; must NOT confirm.
	ip := "77.88.55.70"
	res := fakeResolver{
		ptr:  map[string][]string{ip: {"77-88-55-70.spider.yandex.com."}},
		host: map[string][]string{"77-88-55-70.spider.yandex.com": {"9.9.9.9"}},
	}
	v := New(Config{Resolver: res})
	v.doFCrDNS(fcrdnsJob{ip: addr(ip), vendor: mustVendor(t, "YandexBot")})
	yandexUA := "Mozilla/5.0 (compatible; YandexBot/3.0; +http://yandex.com/bots)"
	if got, _, _ := v.Classify(addr(ip), yandexUA); got != SpoofSuspect {
		t.Fatalf("a PTR that forward-resolves elsewhere must be SpoofSuspect, got %v", got)
	}
}

func TestWorkerPoolAsyncPath(t *testing.T) {
	// End-to-end through the real worker pool: Start, kick, then poll the cache.
	ip := "77.88.55.70"
	res := fakeResolver{
		ptr:  map[string][]string{ip: {"77-88-55-70.spider.yandex.com."}},
		host: map[string][]string{"77-88-55-70.spider.yandex.com": {ip}},
	}
	v := New(Config{Resolver: res})
	done := make(chan struct{})
	defer close(done)
	v.Start(done)
	yandexUA := "Mozilla/5.0 (compatible; YandexBot/3.0; +http://yandex.com/bots)"
	v.Classify(addr(ip), yandexUA) // kicks the async job

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, _, _ := v.Classify(addr(ip), yandexUA); got == Verified {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("async FCrDNS worker did not confirm Yandex within the deadline")
}

func TestCrawlActivityCounts(t *testing.T) {
	v := New(Config{})
	// Two Googlebot hits (UA-recognised; no feed loaded -> Unverifiable but served)
	// and one Bytespider hit; a normal browser must not be counted.
	v.Classify(addr("66.249.66.1"), googlebotUA)
	v.Classify(addr("66.249.66.2"), googlebotUA)
	v.Classify(addr("1.2.3.4"), "Mozilla/5.0 (compatible; Bytespider)")
	v.Classify(addr("9.9.9.9"), "Mozilla/5.0 (Windows NT 10.0) Chrome/130")

	stats := v.Stats()
	got := map[string]int64{}
	for _, s := range stats {
		got[s.Name] = s.Count
	}
	if got["Googlebot"] != 2 {
		t.Fatalf("Googlebot count = %d, want 2", got["Googlebot"])
	}
	if got["Bytespider"] != 1 {
		t.Fatalf("Bytespider count = %d, want 1", got["Bytespider"])
	}
	// A normal browser is not a crawler and must not appear.
	for _, s := range stats {
		if s.Count == 0 {
			t.Fatalf("Stats must omit zero-activity vendors, got %s=0", s.Name)
		}
	}
	// Sorted most-crawled first.
	if len(stats) >= 2 && stats[0].Count < stats[1].Count {
		t.Fatal("Stats must be sorted by count descending")
	}
}

func mustVendor(t *testing.T, name string) *vendorDef {
	t.Helper()
	for i := range registry {
		if registry[i].name == name {
			return &registry[i]
		}
	}
	t.Fatalf("vendor %q not in registry", name)
	return nil
}
