package vayutor

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// reconcileInterval is how often the engine re-checks the toggle, the domain
// list, and the control connection, bringing onions up/down to match. It is also
// when the aggregate visit counter is flushed to the store.
const reconcileInterval = 60 * time.Second

// OnionRecord is one persisted onion identity. PrivateKey is the tor
// "ED25519-V3:<base64>" blob that pins a stable .onion address across restarts.
type OnionRecord struct {
	Host       string
	Address    string
	PrivateKey string
}

// Store persists onion identities and the visit counter. It is injected so this
// package stays free of any DB import and is unit-testable with a fake.
type Store interface {
	LoadOnions(ctx context.Context) ([]OnionRecord, error)
	SaveOnion(ctx context.Context, rec OnionRecord) error
	DeleteOnion(ctx context.Context, host string) error
	LoadVisits(ctx context.Context) int64
	SaveVisits(ctx context.Context, n int64) error
}

// Config injects host dependencies. Domains returns every clearnet host to
// publish as an onion; Active reports the operator's one-click toggle.
type Config struct {
	Enabled     bool                                        // env master switch (VAYUOS_TOR != off)
	ControlAddr string                                      // "127.0.0.1:9051" or a unix socket path
	CookiePath  string                                      // control auth cookie file
	Target      string                                      // local HTTP addr onions map to, e.g. "127.0.0.1:8080"
	Store       Store                                       // identity + counter persistence
	Domains     func(ctx context.Context) ([]string, error) // all hosted clearnet hosts
	Active      func() bool                                 // one-click on/off (settings-backed)

	// Managed mode lets VayuPress run its OWN tor daemon (as the unprivileged
	// service user) when the external control port is absent/unreachable, so
	// VayuTor works with only the `tor` binary present — no root, no systemd, no
	// manual control-port setup. Preferred order at connect time: a reachable
	// ControlAddr (an existing system tor) first, then the managed instance.
	Managed    bool   // enable the self-managed tor fallback (VAYUOS_TOR_MANAGED != 0)
	TorBinary  string // resolved `tor` executable (exec.LookPath); "" → managed disabled
	ManagedDir string // writable base dir VayuPress owns for the managed tor state
}

// Engine is the VayuTor subsystem. It owns the control connection, the
// onion↔host registry, and the aggregate visit counter. It implements the
// VayuOS Subsystem contract (Name/Start).
type Engine struct {
	cfg Config

	mu           sync.RWMutex
	onionByHost  map[string]string // clearnet host -> onion host
	hostByOnion  map[string]string // onion host -> clearnet host
	ctrl         *control
	connected    bool
	lastErr      string
	bootPct      int       // tor network bootstrap %, 0..100 (100 = onions publishable)
	bootNote     string    // tor's human summary of the current bootstrap phase
	bootBestPct  int       // highest bootstrap % seen this connection (stall detect)
	bootMovedAt  time.Time // when bootstrap last advanced
	fascistTried bool      // already fell back to firewall-friendly ports this activation

	visits int64 // atomic; aggregate onion pageviews (no PII, no time)

	managed  *managedTor // self-managed tor daemon (nil unless Managed + a binary)
	usingMgd bool        // the live control connection is to our managed tor

	kick chan struct{}
	done chan struct{}
}

// NewEngine constructs the engine from injected dependencies.
func NewEngine(cfg Config) *Engine {
	e := &Engine{
		cfg:         cfg,
		onionByHost: map[string]string{},
		hostByOnion: map[string]string{},
		kick:        make(chan struct{}, 1),
		done:        make(chan struct{}),
	}
	// Create the managed supervisor whenever managed mode is on and we have a
	// writable dir. The tor binary is resolved LAZILY at connect time (not here),
	// so installing tor after VayuPress has started is picked up on the next
	// reconcile — no restart required.
	if cfg.Managed && strings.TrimSpace(cfg.ManagedDir) != "" {
		e.managed = newManagedTor(cfg.TorBinary, cfg.ManagedDir)
	}
	return e
}

// Name identifies the subsystem for the boot orchestrator.
func (e *Engine) Name() string { return "VayuTor" }

// Available reports whether the subsystem is switched on at the environment
// level (the master kill switch). It says nothing about the runtime toggle.
func (e *Engine) Available() bool { return e != nil && e.cfg.Enabled }

// Start launches the reconcile loop. When the env master switch is off it is a
// no-op so the binary still boots. It never blocks boot on Tor being reachable —
// onion outages degrade gracefully (the clearnet site is unaffected).
func (e *Engine) Start(ctx context.Context) error {
	if !e.Available() {
		return nil
	}
	if e.cfg.Store != nil {
		atomic.StoreInt64(&e.visits, e.cfg.Store.LoadVisits(ctx))
	}
	go e.loop(ctx)
	return nil
}

// Stop tears down onions and the control connection.
func (e *Engine) Stop(_ context.Context) error {
	select {
	case <-e.done:
	default:
		close(e.done)
	}
	e.flushVisits(context.Background())
	e.mu.Lock()
	if e.ctrl != nil {
		_ = e.ctrl.Close() // closing drops the (non-detached) onions
		e.ctrl = nil
	}
	e.connected = false
	e.mu.Unlock()
	if e.managed != nil {
		e.managed.stop() // terminate our tor child (also tears its onions down)
	}
	return nil
}

// Kick asks the loop to reconcile immediately (used right after the operator
// flips the one-click toggle so onions come up/down without waiting a minute).
func (e *Engine) Kick() {
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

// loop reconciles on a ticker, on an explicit Kick, and until ctx/done.
func (e *Engine) loop(ctx context.Context) {
	t := time.NewTicker(reconcileInterval)
	defer t.Stop()
	e.reconcile(ctx)
	for {
		select {
		case <-e.done:
			return
		case <-ctx.Done():
			return
		case <-e.kick:
			e.reconcile(ctx)
		case <-t.C:
			e.flushVisits(ctx)
			e.reconcile(ctx)
		}
	}
}

// active reports whether onions should currently be up: the env switch is on AND
// the operator's one-click toggle is on.
func (e *Engine) active() bool {
	if !e.cfg.Enabled {
		return false
	}
	return e.cfg.Active == nil || e.cfg.Active()
}

// reconcile brings the live onion set in line with (toggle, domain list). It is
// resilient: any control error marks the engine disconnected and is retried on
// the next tick; the clearnet site is never affected.
func (e *Engine) reconcile(ctx context.Context) {
	if !e.active() {
		e.teardown()
		return
	}
	if err := e.ensureConnected(); err != nil {
		e.setErr(err.Error())
		return
	}

	hosts, err := e.cfg.Domains(ctx)
	if err != nil {
		e.setErr("domain list: " + err.Error())
		return
	}
	want := map[string]bool{}
	for _, h := range hosts {
		h = normHost(h)
		if h != "" && !strings.HasSuffix(h, ".onion") {
			want[h] = true
		}
	}

	// Snapshot what is already live on THIS control connection. Re-issuing
	// ADD_ONION for an already-added onion is an error in tor, so reconcile is
	// incremental: only add newly-wanted domains, only remove gone ones.
	e.mu.RLock()
	liveByHost := make(map[string]string, len(e.onionByHost))
	for h, o := range e.onionByHost {
		liveByHost[h] = o
	}
	e.mu.RUnlock()

	// Persisted identities, keyed by clearnet host.
	persisted := map[string]OnionRecord{}
	if e.cfg.Store != nil {
		if recs, lerr := e.cfg.Store.LoadOnions(ctx); lerr == nil {
			for _, r := range recs {
				persisted[normHost(r.Host)] = r
			}
		}
	}

	// Remove onions (and stored identities) for hosts no longer served.
	for host, onionHost := range liveByHost {
		if !want[host] {
			e.mu.RLock()
			ctrl := e.ctrl
			e.mu.RUnlock()
			if ctrl != nil {
				_ = ctrl.delOnion(serviceIDOf(onionHost))
			}
			delete(liveByHost, host)
			e.mu.Lock()
			delete(e.hostByOnion, normHost(onionHost))
			delete(e.onionByHost, host)
			e.mu.Unlock()
			if e.cfg.Store != nil {
				_ = e.cfg.Store.DeleteOnion(ctx, host)
			}
		}
	}

	// Add onions for newly-wanted domains not already live.
	for host := range want {
		if _, ok := liveByHost[host]; ok {
			continue
		}
		blob := "NEW:ED25519-V3"
		if rec, ok := persisted[host]; ok && rec.PrivateKey != "" {
			blob = rec.PrivateKey
		}
		e.mu.RLock()
		ctrl := e.ctrl
		e.mu.RUnlock()
		if ctrl == nil {
			e.setErr("control connection lost")
			return
		}
		on, aerr := ctrl.addOnion(blob, e.cfg.Target)
		if aerr != nil {
			e.setErr(aerr.Error())
			return
		}
		e.mu.Lock()
		e.onionByHost[host] = on.host
		e.hostByOnion[normHost(on.host)] = host
		e.mu.Unlock()
		// Persist a freshly-minted identity so the address is stable next boot.
		if on.privateKey != "" && e.cfg.Store != nil {
			_ = e.cfg.Store.SaveOnion(ctx, OnionRecord{Host: host, Address: on.host, PrivateKey: on.privateKey})
		}
	}

	e.mu.Lock()
	e.lastErr = ""
	e.mu.Unlock()

	e.queryBootstrap()
}

// queryBootstrap asks tor how far its network bootstrap has progressed. Until it
// reaches 100% the onion descriptors cannot be published, so a visitor sees
// "Onion site not found" even though VayuTor reports Active. Best-effort.
func (e *Engine) queryBootstrap() {
	e.mu.RLock()
	ctrl := e.ctrl
	e.mu.RUnlock()
	if ctrl == nil {
		return
	}
	phase, err := ctrl.getInfo("status/bootstrap-phase")
	if err != nil {
		return
	}
	pct, note := parseBootstrap(phase)
	e.mu.Lock()
	e.bootPct = pct
	e.bootNote = note
	// Track forward progress so we can tell a slow-but-climbing bootstrap (fine,
	// leave it alone) apart from a genuinely stuck one (needs intervention).
	if pct > e.bootBestPct {
		e.bootBestPct = pct
		e.bootMovedAt = time.Now()
	}
	stalled := pct < 100 && e.usingMgd && e.managed != nil && !e.fascistTried &&
		!e.bootMovedAt.IsZero() && time.Since(e.bootMovedAt) > bootStallTimeout
	if stalled {
		e.fascistTried = true
	}
	e.mu.Unlock()

	// A bootstrap that hasn't advanced for a while on a locked-down host usually
	// means only 80/443 outbound is allowed. Restart tor in firewall-friendly
	// mode (once) — it never fires while bootstrap is still climbing.
	if stalled {
		e.managed.setStrict(true)
		e.managed.stop() // next reconcile reaps + respawns with ReachableAddresses
		e.setErr("bootstrap stalled — retrying Tor via firewall-friendly ports (80/443)")
		e.Kick()
	}
}

// bootStallTimeout is how long bootstrap may sit without advancing before
// VayuTor retries tor in firewall-friendly mode. Generous, so a merely-slow
// first bootstrap (downloading the consensus) is never disrupted.
const bootStallTimeout = 150 * time.Second

// parseBootstrap extracts the PROGRESS percentage and SUMMARY text from a tor
// "status/bootstrap-phase" line, e.g.
// `NOTICE BOOTSTRAP PROGRESS=100 TAG=done SUMMARY="Done"`.
func parseBootstrap(line string) (int, string) {
	pct := 0
	if i := strings.Index(line, "PROGRESS="); i >= 0 {
		rest := line[i+len("PROGRESS="):]
		n := 0
		for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
			pct = pct*10 + int(rest[n]-'0')
			n++
		}
	}
	note := ""
	if i := strings.Index(line, `SUMMARY="`); i >= 0 {
		rest := line[i+len(`SUMMARY="`):]
		if j := strings.IndexByte(rest, '"'); j >= 0 {
			note = rest[:j]
		}
	}
	return pct, note
}

// ensureConnected dials + authenticates a control port if not already up. It
// prefers a reachable external control port (an existing system tor), and falls
// back to a VayuPress-managed tor daemon when one is configured — so activation
// succeeds with only the `tor` binary present, no external setup required.
func (e *Engine) ensureConnected() error {
	e.mu.RLock()
	ok := e.ctrl != nil && e.connected
	e.mu.RUnlock()
	if ok {
		return nil
	}

	// 1. External control port (system tor), when configured. Kept first so an
	//    operator's existing, purpose-built tor service always wins.
	var extErr error
	if strings.TrimSpace(e.cfg.ControlAddr) != "" {
		if c, err := e.dialAuth(e.cfg.ControlAddr, e.cfg.CookiePath); err == nil {
			e.adoptControl(c, false)
			return nil
		} else {
			extErr = err
		}
	}

	// 2. VayuPress-managed tor daemon (unprivileged child process). This is what
	//    makes VayuTor work after any update with zero manual setup.
	if e.managed != nil {
		if err := e.managed.ensure(context.Background()); err != nil {
			return err
		}
		c, err := e.dialAuth(e.managed.controlAddr(), e.managed.cookiePath())
		if err != nil {
			// The managed tor is wedged/unreachable (e.g. reset by peer). Tear it
			// down so the next reconcile reaps + respawns a clean daemon rather
			// than looping against a dead socket.
			e.managed.stop()
			return err
		}
		e.adoptControl(c, true)
		return nil
	}

	if extErr != nil {
		return extErr
	}
	return errNoControlPort
}

// errNoControlPort is returned when neither an external control port is
// configured nor a managed tor is available (no `tor` binary installed).
var errNoControlPort = errors.New("vayutor: no tor control port available — install the tor binary (the VayuPress updater does this) or set VAYUOS_TOR_CONTROL_ADDR")

// dialAuth opens and cookie-authenticates one control connection.
func (e *Engine) dialAuth(addr, cookiePath string) (*control, error) {
	c, err := dialControl(addr)
	if err != nil {
		return nil, err
	}
	var cookie []byte
	if cookiePath != "" {
		cookie, _ = os.ReadFile(cookiePath) // empty → password-less AUTHENTICATE
	}
	if err := c.authenticate(cookie); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// adoptControl installs a freshly-authenticated control connection and resets
// the live registry (onions from any prior connection were torn down with it).
func (e *Engine) adoptControl(c *control, managed bool) {
	e.mu.Lock()
	e.ctrl = c
	e.connected = true
	e.usingMgd = managed
	// Start bootstrap-progress tracking fresh for this connection.
	e.bootPct = 0
	e.bootNote = ""
	e.bootBestPct = 0
	e.bootMovedAt = time.Now()
	e.onionByHost = map[string]string{}
	e.hostByOnion = map[string]string{}
	e.mu.Unlock()
}

// teardown deletes all onions and closes the control connection (toggle off or
// subsystem disabled). Persisted identities are kept so the SAME addresses come
// back when re-activated.
func (e *Engine) teardown() {
	e.mu.Lock()
	if e.ctrl != nil {
		for onionHost := range e.hostByOnion {
			_ = e.ctrl.delOnion(serviceIDOf(onionHost))
		}
		_ = e.ctrl.Close()
		e.ctrl = nil
	}
	e.connected = false
	e.usingMgd = false
	e.bootPct = 0
	e.bootNote = ""
	e.bootBestPct = 0
	e.bootMovedAt = time.Time{}
	e.fascistTried = false
	e.onionByHost = map[string]string{}
	e.hostByOnion = map[string]string{}
	mgd := e.managed
	e.mu.Unlock()
	// Stop our tor child when deactivated so "off" is truly off (no process, no
	// onions). Re-activation respawns it fresh (non-strict). Kept outside the
	// lock so we never hold it across a process kill.
	if mgd != nil {
		mgd.setStrict(false)
		mgd.stop()
	}
}

// setErr records the last control error and marks the connection down so the
// next reconcile re-dials.
func (e *Engine) setErr(msg string) {
	e.mu.Lock()
	e.lastErr = msg
	if e.ctrl != nil {
		_ = e.ctrl.Close()
		e.ctrl = nil
	}
	e.connected = false
	e.bootPct = 0
	e.bootNote = ""
	e.mu.Unlock()
}

// OnionForHost returns the .onion host mapped to a clearnet host, or "".
func (e *Engine) OnionForHost(host string) string {
	if e == nil {
		return ""
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.onionByHost[normHost(host)]
}

// HostForOnion resolves an incoming .onion host to the clearnet host it serves.
func (e *Engine) HostForOnion(onionHost string) (string, bool) {
	if e == nil {
		return "", false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	h, ok := e.hostByOnion[normHost(onionHost)]
	return h, ok
}

// IncVisit records one onion pageview. No identifier, timestamp, path, or any
// other datum is kept — only this aggregate count (the entire VayuTor analytic).
func (e *Engine) IncVisit() {
	if e != nil {
		atomic.AddInt64(&e.visits, 1)
	}
}

// Visits returns the aggregate onion pageview count.
func (e *Engine) Visits() int64 { return atomic.LoadInt64(&e.visits) }

// flushVisits persists the counter (bounded to one write per reconcile tick).
func (e *Engine) flushVisits(ctx context.Context) {
	if e.cfg.Store != nil {
		_ = e.cfg.Store.SaveVisits(ctx, atomic.LoadInt64(&e.visits))
	}
}

// Status is the admin-facing snapshot of the subsystem.
type Status struct {
	Available    bool    // env master switch on
	Active       bool    // one-click toggle on
	Connected    bool    // control port reachable + authenticated
	Onions       []Onion // per-domain mappings, sorted by host
	Visits       int64   // aggregate onion pageviews
	LastError    string  // last control error, if any
	BootstrapPct int     // tor network bootstrap % (100 = onions reachable)
	BootstrapEng string  // tor's summary of the current bootstrap phase
	LogPath      string  // managed tor's log file, for diagnostics ("" if external)
	LogTail      string  // last few lines of the managed tor log (bootstrap reason)
}

// Onion is one domain↔onion mapping for the admin table.
type Onion struct {
	Host      string
	OnionHost string
}

// Snapshot returns the current status for the admin page.
func (e *Engine) Snapshot() Status {
	if e == nil {
		return Status{}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	lastErr := e.lastErr
	// When the managed tor is the relevant path and reported something more
	// specific (e.g. "tor is not installed"), prefer that — it is actionable,
	// whereas the raw external-dial error ("connection refused") is not.
	if !e.connected && e.managed != nil {
		if me := e.managed.lastError(); me != "" {
			lastErr = me
		}
	}
	st := Status{
		Available:    e.cfg.Enabled,
		Active:       e.active(),
		Connected:    e.connected,
		Visits:       atomic.LoadInt64(&e.visits),
		LastError:    lastErr,
		BootstrapPct: e.bootPct,
		BootstrapEng: e.bootNote,
	}
	if e.managed != nil {
		st.LogPath = e.managed.logPath()
		if !st.Connected || st.BootstrapPct < 100 {
			st.LogTail = e.managed.tailLog(3)
		}
	}
	for host, onionHost := range e.onionByHost {
		st.Onions = append(st.Onions, Onion{Host: host, OnionHost: onionHost})
	}
	sortOnions(st.Onions)
	return st
}

// normHost lowercases and strips a port + trailing dot from a host, matching the
// domain registry's NormalizeHost so onion↔host lookups are consistent.
func normHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.Contains(h[i:], "]") {
		// strip :port (but not an IPv6 literal's colons)
		if !strings.Contains(h, "]") {
			h = h[:i]
		}
	}
	return strings.TrimSuffix(h, ".")
}

// sortOnions orders mappings by host for a stable admin table (insertion sort —
// the list is tiny: one entry per hosted domain).
func sortOnions(o []Onion) {
	for i := 1; i < len(o); i++ {
		for j := i; j > 0 && o[j-1].Host > o[j].Host; j-- {
			o[j-1], o[j] = o[j], o[j-1]
		}
	}
}
