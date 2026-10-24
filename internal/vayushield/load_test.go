// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

// The shield under load: an open-loop stream of requests at a fixed rate, as
// traffic arrives whether or not the server keeps up, through the whole
// middleware with recording on (the batched writer to a migrated database),
// rate limits, automatic jailing and a set of operator rules. Run with
//
//	VP_SHIELD_LOAD=1 go test -run TestTheShieldUnderLoad -v ./internal/vayushield/
//
// and VP_SHIELD_RATES (default "1000,5000") and VP_SHIELD_SECONDS (default
// 10). It is not run otherwise: its figures are measurements, not checks.
//
// The mix is a site's, not one client's: most requests are readers on two
// thousand addresses, then crawlers, scripts, scanners probing for known
// holes, and a few addresses flooding. Each request's time is the
// middleware's own, the handler behind it does nothing.
func TestTheShieldUnderLoad(t *testing.T) {
	if os.Getenv("VP_SHIELD_LOAD") == "" {
		t.Skip("a measurement: set VP_SHIELD_LOAD=1 to run it")
	}
	seconds := 10
	if n, err := strconv.Atoi(os.Getenv("VP_SHIELD_SECONDS")); err == nil && n > 0 {
		seconds = n
	}
	rates := []int{1000, 5000}
	if v := os.Getenv("VP_SHIELD_RATES"); v != "" {
		rates = nil
		for _, f := range strings.Split(v, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(f)); err == nil && n > 0 {
				rates = append(rates, n)
			}
		}
	}
	dir := t.TempDir()
	config.Cfg.DBPath = filepath.Join(dir, "load.db")
	if err := dbpkg.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbpkg.DB.Close() })

	for _, rate := range rates {
		m := New(Config{Enabled: true, DB: dbpkg.DB, Bots: botdb.New(dbpkg.DB)})
		m.ApplySettings(Settings{
			Enabled: true, RateLimit: true, RatePerMinute: 120, Burst: 40,
			AutoBlock: true, AutoBlockJailMinutes: 10,
			Rules: loadRules,
		})
		done := make(chan struct{})
		m.StartAsyncWrites(done, nil)
		h := m.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		res := runLoad(h, rate, time.Duration(seconds)*time.Second)
		close(done)
		<-m.AsyncWrites().Stopped()
		res.dropped, res.lost = m.AsyncWrites().Dropped(), m.AsyncWrites().Lost()
		t.Log(res.String())
	}
}

// loadRules are twenty rules of the kinds an operator writes, none of which
// matches most requests, so every one is evaluated for most of them.
var loadRules = func() string {
	var b strings.Builder
	for i := 0; i < 16; i++ {
		fmt.Fprintf(&b, "block path=/private-%d method=POST\n", i)
	}
	b.WriteString("challenge path=/wp-login.php\nblock path=/.env\nlimit 30/min path=/search\nlog client=badbot\n")
	return b.String()
}()

type loadResult struct {
	rate, sent, late   int
	took               time.Duration
	p50, p95, p99, max time.Duration
	status             map[int]int
	cpu                time.Duration
	heapMax            uint64
	dropped, lost      int64
}

func (r loadResult) String() string {
	codes := make([]int, 0, len(r.status))
	for c := range r.status {
		codes = append(codes, c)
	}
	sort.Ints(codes)
	var cs []string
	for _, c := range codes {
		cs = append(cs, fmt.Sprintf("%d×%d", c, r.status[c]))
	}
	return fmt.Sprintf("%d req/s for %v: %d sent (%.0f/s achieved), %d late; middleware p50 %v p95 %v p99 %v max %v; "+
		"CPU %v (%.0f%% of one core); heap grew at most %d MiB; recording: %d dropped, %d lost; statuses %s",
		r.rate, r.took.Round(time.Millisecond), r.sent, float64(r.sent)/r.took.Seconds(), r.late,
		r.p50, r.p95, r.p99, r.max, r.cpu.Round(time.Millisecond), 100*r.cpu.Seconds()/r.took.Seconds(),
		r.heapMax>>20, r.dropped, r.lost, strings.Join(cs, " "))
}

// loadRequest is one request of the mix.
func loadRequest(rng *rand.Rand) *http.Request {
	pick := rng.Intn(100)
	var path, ua, ip string
	switch {
	case pick < 70: // readers
		path = []string{"/", "/hello-vayupress", "/notes/one-binary", "/tags/welcome", "/about"}[rng.Intn(5)]
		ua = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15"
		ip = fmt.Sprintf("203.0.%d.%d", rng.Intn(8), 1+rng.Intn(250))
	case pick < 80: // crawlers claiming to be one
		path = "/sitemap.xml"
		ua = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
		ip = fmt.Sprintf("66.249.66.%d", 1+rng.Intn(50))
	case pick < 90: // scripts
		path = "/feed"
		ua = []string{"python-requests/2.32.3", "curl/8.7.1", "Go-http-client/1.1"}[rng.Intn(3)]
		ip = fmt.Sprintf("198.51.100.%d", 1+rng.Intn(200))
	case pick < 95: // scanners
		path = []string{"/wp-login.php", "/.env", "/?id=1%27%20OR%201%3D1--", "/cgi-bin/../../etc/passwd"}[rng.Intn(4)]
		ua = "Mozilla/5.0 zgrab/0.x"
		ip = fmt.Sprintf("192.0.2.%d", 1+rng.Intn(50))
	default: // a few addresses flooding
		path = "/"
		ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/126.0"
		ip = fmt.Sprintf("100.64.0.%d", 1+rng.Intn(5))
	}
	r := httptest.NewRequest(http.MethodGet, "http://site.test"+path, nil)
	r.RemoteAddr = ip + ":40000"
	r.Header.Set("User-Agent", ua)
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	r.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	return r
}

// runLoad sends rate requests a second for d, open loop: each is due at its
// time whether or not the last is done, and one that cannot start within a
// millisecond of it is counted late.
func runLoad(h http.Handler, rate int, d time.Duration) loadResult {
	workers := 4 * runtime.GOMAXPROCS(0)
	jobs := make(chan *http.Request, rate/10+16)
	var mu sync.Mutex
	lat := make([]time.Duration, 0, rate*int(d/time.Second))
	status := map[int]int{}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mine, codes := make([]time.Duration, 0, 4096), map[int]int{}
			for r := range jobs {
				w := httptest.NewRecorder()
				t0 := time.Now()
				h.ServeHTTP(w, r)
				mine = append(mine, time.Since(t0))
				codes[w.Code]++
			}
			mu.Lock()
			lat = append(lat, mine...)
			for c, n := range codes {
				status[c] += n
			}
			mu.Unlock()
		}()
	}
	rng := rand.New(rand.NewSource(1))
	reqs := make([]*http.Request, 0, rate*int(d/time.Second))
	for i := 0; i < cap(reqs); i++ {
		reqs = append(reqs, loadRequest(rng))
	}
	// The heap is measured over what it was once the requests were built, so
	// the figure is what the shield and its recording hold, not the load.
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var heapMax uint64
	stopHeap := make(chan struct{})
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-stopHeap:
				return
			case <-time.After(100 * time.Millisecond):
				runtime.ReadMemStats(&ms)
				if ms.HeapAlloc > heapMax {
					heapMax = ms.HeapAlloc
				}
			}
		}
	}()
	cpu0 := cpuTime()
	start := time.Now()
	late := 0
	interval := time.Second / time.Duration(rate)
	for i, r := range reqs {
		due := start.Add(time.Duration(i) * interval)
		if wait := time.Until(due); wait > 0 {
			time.Sleep(wait)
		}
		select {
		case jobs <- r:
		default:
			late++
			jobs <- r
		}
	}
	close(jobs)
	wg.Wait()
	took := time.Since(start)
	cpu := cpuTime() - cpu0
	close(stopHeap)
	if heapMax > base.HeapAlloc {
		heapMax -= base.HeapAlloc
	} else {
		heapMax = 0
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	at := func(q float64) time.Duration { return lat[int(q*float64(len(lat)-1))] }
	return loadResult{rate: rate, sent: len(reqs), late: late, took: took, p50: at(0.50), p95: at(0.95), p99: at(0.99),
		max: lat[len(lat)-1], status: status, cpu: cpu, heapMax: heapMax}
}

// cpuTime is the CPU the process's Go code and its collector have used,
// from runtime/metrics, so the figure is the same on every platform.
func cpuTime() time.Duration {
	s := []metrics.Sample{{Name: "/cpu/classes/user:cpu-seconds"}, {Name: "/cpu/classes/gc/total:cpu-seconds"}}
	metrics.Read(s)
	return time.Duration((s[0].Value.Float64() + s[1].Value.Float64()) * float64(time.Second))
}
