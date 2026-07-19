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
	// Per-page aggregate onion counts (opt-in; see pagestats.go). The map is
	// "host path" → cumulative count — no time, identity, or ordering.
	LoadPageHits(ctx context.Context) map[string]int64
	SavePageHits(ctx context.Context, hits map[string]int64) error
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
	PageStats   func() bool                                 // opt-in per-page onion counts (settings-backed); nil ⇒ off
	Notify      func(event string, payload map[string]any)  // fire an operator alert (→ webhooks); nil ⇒ no alerts

	// Managed mode lets VayuPress run its OWN tor daemon (as the unprivileged
	// service user) when the external control port is absent/unreachable, so
	// VayuTor works with only the `tor` binary present — no root, no systemd, no
	// manual control-port setup. Preferred order at connect time: a reachable
	// ControlAddr (an existing system tor) first, then the managed instance.
	Managed     bool            // enable the self-managed tor fallback (VAYUOS_TOR_MANAGED != 0)
	TorBinary   string          // resolved `tor` executable (exec.LookPath); "" → managed disabled
	ManagedDir  string          // writable base dir VayuPress owns for the managed tor state
	Bridges     []string        // static operator Bridge lines from env (VAYUOS_TOR_BRIDGES)
	BridgesLive func() []string // live operator Bridge lines from settings (VayuOS form); read each reconcile

	// TorDownload lets the managed tor download the Tor Project's official static
	// build into VayuPress's own state dir when the *system* tor is missing or too
	// old to validate today's consensus (e.g. an EOL distro's 0.4.2.x). Runs as the
	// unprivileged service user — no root, no apt. Falls back to the system tor if
	// the download fails or the modern build won't run on the host. Default on
	// (VAYUOS_TOR_DOWNLOAD != 0).
	TorDownload bool

	// SpaceOnionTarget drives the ONE dedicated onion for the Anonymous Tor Space
	// (ADR-0141): it returns the child instance's loopback target ("127.0.0.1:<port>")
	// while the Space is active, or "" to tear the onion down (keeping its key). nil
	// ⇒ the feature is off. This onion is minted with its OWN persistent key under a
	// reserved store host and is kept OUT of the per-domain map, so the domain reap
	// loop never touches it and its address is stable across restarts.
	SpaceOnionTarget func() string
	// SpaceOnionReady is called with the dedicated onion's address once it is live,
	// so the host can hand the child its DOMAIN. nil ⇒ ignored.
	SpaceOnionReady func(onion string)
	// SpaceSites returns the IDs of EXTRA Tor-world sites that each want their own
	// dedicated onion (all routing to the same child target as SpaceOnionTarget) —
	// the "one-click add Tor site" feature (ADR-0141). Each site's onion is minted
	// with its own persistent key under a reserved store host and kept out of the
	// per-domain map, exactly like the primary Space onion. The bool reports whether
	// the answer is DEFINITIVE (the child was reached): on false the engine leaves
	// every live site onion in place and retries next tick, so a transient child
	// hiccup never blips a published .onion. nil ⇒ feature off.
	SpaceSites func() ([]string, bool)
	// SpaceSiteReady is called with (siteID, onion) once an extra site's onion is
	// live, so the host can register that .onion as a domain in the child. nil ⇒ ignored.
	SpaceSiteReady func(siteID, onion string)
}

// operatorBridges returns the operator-configured bridge lines, preferring the
// live settings-backed source (the VayuOS form) over the static env value so
// changes made from the admin UI take effect with no restart.
func (e *Engine) operatorBridges() []string {
	if e.cfg.BridgesLive != nil {
		if live := e.cfg.BridgesLive(); len(live) > 0 {
			return live
		}
	}
	return e.cfg.Bridges
}

// Engine is the VayuTor subsystem. It owns the control connection, the
// onion↔host registry, and the aggregate visit counter. It implements the
// VayuOS Subsystem contract (Name/Start).
type Engine struct {
	cfg Config

	mu          sync.RWMutex
	onionByHost map[string]string // clearnet host -> onion host
	hostByOnion map[string]string // onion host -> clearnet host
	ctrl        *control
	connected   bool
	lastErr     string
	bootPct     int       // tor network bootstrap %, 0..100 (100 = onions publishable)
	bootNote    string    // tor's human summary of the current bootstrap phase
	bootBestPct int       // highest bootstrap % seen this connection (stall detect)
	bootMovedAt time.Time // when bootstrap last advanced
	esc         escLevel  // one-shot escalation rung: direct → 80/443 → bridges
	torVer      string    // tor daemon version (GETINFO version), for diagnostics

	visits int64 // atomic; aggregate onion pageviews (no PII, no time)

	pageMu   sync.Mutex       // guards pageHits
	pageHits map[string]int64 // "host path" → cumulative count (opt-in; aggregate only)

	// Onion health tracking (see health.go). All guarded by e.mu.
	wantCount          int           // number of clearnet hosts that should have an onion
	health             string        // committed coarse state (off/starting/healthy/degraded/down)
	healthReason       string        // human reason for the current state
	healthSince        time.Time     // when the committed state was entered
	healthPending      string        // an outage state awaiting the debounce
	healthPendingSince time.Time     // when healthPending was first seen
	healthAlerted      bool          // an unresolved "onion_down" alert has been sent
	healthLog          []HealthEvent // recent transitions (bounded), newest last

	managed  *managedTor // self-managed tor daemon (nil unless Managed + a binary)
	usingMgd bool        // the live control connection is to our managed tor

	spaceOnionHost string            // the live Anonymous Tor Space onion (ADR-0141); guarded by e.mu
	spaceSiteHosts map[string]string // extra Tor-world site onions: siteID → live host; guarded by e.mu

	vanityMu sync.Mutex   // guards vanity
	vanity   *vanityState // in-progress vanity-address search (nil if none)

	kick chan struct{}
	done chan struct{}
}

// NewEngine constructs the engine from injected dependencies.
func NewEngine(cfg Config) *Engine {
	e := &Engine{
		cfg:         cfg,
		onionByHost: map[string]string{},
		hostByOnion: map[string]string{},
		pageHits:    map[string]int64{},
		kick:        make(chan struct{}, 1),
		done:        make(chan struct{}),
	}
	// Create the managed supervisor whenever managed mode is on and we have a
	// writable dir. The tor binary is resolved LAZILY at connect time (not here),
	// so installing tor after VayuPress has started is picked up on the next
	// reconcile — no restart required.
	if cfg.Managed && strings.TrimSpace(cfg.ManagedDir) != "" {
		e.managed = newManagedTor(cfg.TorBinary, cfg.ManagedDir)
		// When enabled, the managed tor may download the Tor Project's current
		// static build if the system tor is absent/too old (see dist.go).
		e.managed.setDistEnabled(cfg.TorDownload)
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
		if hits := e.cfg.Store.LoadPageHits(ctx); len(hits) > 0 {
			e.pageMu.Lock()
			e.pageHits = hits
			e.pageMu.Unlock()
		}
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
	// Re-judge onion health at every exit (including early error returns), so an
	// outage is noticed and alerted even when reconcile bails early.
	defer e.evalHealth()
	if !e.active() {
		e.teardown()
		return
	}
	e.applyOperatorBridges()
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
	e.mu.Lock()
	e.wantCount = len(want)
	e.mu.Unlock()

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

	// Add onions for newly-wanted domains not already live. One host's identity
	// failing (e.g. a corrupt persisted ED25519 key tor rejects) must NOT abort
	// the whole reconcile — record it and continue, so the other domains AND the
	// dedicated Space onion below still come up. Only a lost control connection
	// (nothing can be added) short-circuits.
	var addErr string
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
			addErr = aerr.Error()
			continue
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

	// Surface any per-domain add error BEFORE the Space onion runs, so a
	// successful Space onion leaves the per-domain problem visible and a failing
	// one (setErr) takes precedence.
	e.mu.Lock()
	e.lastErr = addErr
	e.mu.Unlock()

	// The ONE dedicated Anonymous Tor Space onion (ADR-0141), managed separately
	// from the per-domain onions above so the reap loop never tears it down — and
	// reached regardless of any per-domain add failure so enabling the anonymous
	// Space never depends on the health of unrelated clearnet-domain onions.
	e.reconcileSpaceOnion(ctx)
	e.reconcileSpaceSites(ctx)

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
	// Capture the tor version once — an ancient tor (e.g. an EOL distro's 0.4.2.x)
	// can't validate today's consensus, and knowing the version turns that vague
	// error into precise guidance.
	e.mu.RLock()
	haveVer := e.torVer != ""
	e.mu.RUnlock()
	if !haveVer {
		if v, verr := ctrl.getInfo("version"); verr == nil && v != "" {
			e.mu.Lock()
			e.torVer = v
			e.mu.Unlock()
		}
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
	e.mu.Unlock()
	e.maybeEscalate()
}

// maybeEscalate advances the one-shot bootstrap-escalation ladder when a managed
// tor's bootstrap has genuinely STALLED (no forward progress for bootStallTimeout
// — a slow-but-climbing bootstrap is never disturbed). Rungs: direct → 80/443 →
// bridges. When tor's log proves an IP-level block (NOROUTE), or the operator
// configured bridges, it skips straight to bridges (firewall-friendly ports
// can't help a null-routed relay IP). escBridges is terminal.
func (e *Engine) maybeEscalate() {
	e.mu.Lock()
	stalled := e.bootPct < 100 && e.usingMgd && e.managed != nil && e.esc < escBridges &&
		!e.bootMovedAt.IsZero() && time.Since(e.bootMovedAt) > bootStallTimeout
	cur := e.esc
	e.mu.Unlock()
	if !stalled {
		return
	}

	next := cur + 1
	if cur == escDirect {
		if logHasNoRoute(e.managed.tailLog(12)) || len(e.operatorBridges()) > 0 {
			next = escBridges // ports-only can't fix a null-routed relay / operator opted in
		}
	}

	if next == escBridges {
		lines, ptPath, why := e.bridgeConfig()
		if len(lines) == 0 {
			e.setErr(why) // actionable: no bridges, or obfs4 configured but obfs4proxy missing
			return
		}
		e.mu.Lock()
		e.esc = escBridges
		e.mu.Unlock()
		e.managed.setStrict(false) // don't also narrow bridge reachability to 80/443
		e.managed.setBridges(lines, ptPath)
	} else { // escFascist
		e.mu.Lock()
		e.esc = escFascist
		e.mu.Unlock()
		e.managed.setStrict(true)
	}
	e.managed.stop() // next reconcile reaps + respawns with the new torrc
	e.setErr(escalationNote(next))
	e.Kick()
}

// applyOperatorBridges honours bridges the operator configured from VayuOS (or
// env) BEFORE any stall: if they set bridges, direct is presumed blocked, so the
// managed tor should use them straight away. It reconfigures + restarts the
// managed tor only when the effective bridge set actually changes (no restart
// loop), and drops back to a direct connection if the operator clears them.
func (e *Engine) applyOperatorBridges() {
	if e.managed == nil {
		return
	}
	ob := e.operatorBridges()
	if len(ob) == 0 {
		// Operator cleared bridges: if we were forced onto the bridges rung, go
		// back to direct so a now-unblocked network is used again.
		e.mu.Lock()
		wasBridges := e.esc == escBridges
		e.mu.Unlock()
		if wasBridges && e.managed.usingBridges() {
			e.mu.Lock()
			e.esc = escDirect
			e.mu.Unlock()
			e.managed.setBridges(nil, "")
			e.managed.setStrict(false)
			e.managed.stop()
			e.resetConn()
		}
		return
	}

	norm, needsPT := classifyBridges(ob)
	pt := ""
	if needsPT {
		if pt = e.managed.resolvePT(); pt == "" {
			if v := vanillaOnly(norm); len(v) > 0 {
				norm = v // obfs4proxy missing — use the plain bridges we have
			} else {
				e.setErr("obfs4 bridges are set but obfs4proxy is not installed — re-run the VayuPress updater to add it, then reload")
				return
			}
		}
	}
	if len(norm) == 0 || e.managed.bridgesEqual(norm, pt) {
		return // nothing to change (already applied)
	}
	e.managed.setBridges(norm, pt)
	e.managed.setStrict(false)
	e.mu.Lock()
	e.esc = escBridges
	e.mu.Unlock()
	e.managed.stop() // ensureConnected (next line of reconcile) respawns with bridges
	e.resetConn()
}

// resetConn drops the live control connection so the next ensureConnected
// re-dials (used after we restart the managed tor with a new config). It records
// no error — unlike setErr — because this is an intentional reconfiguration.
func (e *Engine) resetConn() {
	e.mu.Lock()
	if e.ctrl != nil {
		_ = e.ctrl.Close()
		e.ctrl = nil
	}
	e.connected = false
	e.bootPct = 0
	e.bootNote = ""
	e.bootBestPct = 0
	e.bootMovedAt = time.Time{}
	e.mu.Unlock()
}

// bridgeConfig resolves the effective bridge set for the bridges rung: operator
// lines (VAYUOS_TOR_BRIDGES) if any, else the built-in defaults. why is "" only
// on success; on failure lines is empty and why is an actionable message.
func (e *Engine) bridgeConfig() (lines []string, ptPath, why string) {
	raw := e.operatorBridges()
	if len(raw) == 0 {
		raw = defaultObfs4Bridges
	}
	norm, needsPT := classifyBridges(raw)
	if len(norm) == 0 {
		return nil, "", "the network blocks direct Tor connections and no bridges are configured — get obfs4 bridges from https://bridges.torproject.org (or email bridges@torproject.org), set VAYUOS_TOR_BRIDGES to those lines, and reload"
	}
	if needsPT {
		if ptPath = e.managed.resolvePT(); ptPath == "" {
			if v := vanillaOnly(norm); len(v) > 0 {
				return v, "", "" // fall back to the plain bridges we do have
			}
			return nil, "", "obfs4 bridges are configured but obfs4proxy is not installed — run `sudo apt-get install -y obfs4proxy` (or re-run the VayuPress updater), then reload"
		}
	}
	return norm, ptPath, ""
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
	// The dedicated Anonymous Tor Space onion (ADR-0141) is torn down inside tor
	// with the old control connection (onions are created without Detach), so its
	// live-host record is stale on any re-dial. Clear it here — the single choke
	// point for every reconnect — so reconcileSpaceOnion re-issues ADD_ONION from
	// the persisted key and restores the SAME address, exactly like per-domain
	// onions above. Without this the address is dead but SpaceOnion() keeps naming
	// it, so the child boots against an unpublished onion ("site not found").
	e.spaceOnionHost = ""
	e.spaceSiteHosts = nil
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
	e.esc = escDirect
	e.onionByHost = map[string]string{}
	e.hostByOnion = map[string]string{}
	// Drop the dedicated Space onion's live-host record too (see adoptControl):
	// tearing down the control connection removes it inside tor, so leaving it set
	// would strand a dead address across a re-activation.
	e.spaceOnionHost = ""
	e.spaceSiteHosts = nil
	mgd := e.managed
	e.mu.Unlock()
	// Stop our tor child when deactivated so "off" is truly off (no process, no
	// onions). Clear the escalation state so re-activation starts clean at rung 0
	// (direct, no ports restriction, no bridges). Kept outside the lock so we
	// never hold it across a process kill.
	if mgd != nil {
		mgd.setStrict(false)
		mgd.setBridges(nil, "")
		// Clear any managed-download cooldown so toggling VayuTor off→on forces a
		// fresh download attempt (the operator's manual "try again").
		mgd.resetDistCooldown()
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
	if e.cfg.Store == nil {
		return
	}
	_ = e.cfg.Store.SaveVisits(ctx, atomic.LoadInt64(&e.visits))
	// Persist per-page counts too (only meaningful when opted in; a no-op empty
	// map is cheap and keeps the store consistent if the operator just disabled it).
	e.pageMu.Lock()
	snap := make(map[string]int64, len(e.pageHits))
	for k, v := range e.pageHits {
		snap[k] = v
	}
	e.pageMu.Unlock()
	_ = e.cfg.Store.SavePageHits(ctx, snap)
}

// Status is the admin-facing snapshot of the subsystem.
type Status struct {
	Available      bool          // env master switch on
	Active         bool          // one-click toggle on
	Connected      bool          // control port reachable + authenticated
	Onions         []Onion       // per-domain mappings, sorted by host
	Visits         int64         // aggregate onion pageviews
	LastError      string        // last control error, if any
	BootstrapPct   int           // tor network bootstrap % (100 = onions reachable)
	BootstrapEng   string        // tor's summary of the current bootstrap phase
	LogPath        string        // managed tor's log file, for diagnostics ("" if external)
	LogTail        string        // last few lines of the managed tor log (bootstrap reason)
	Transport      string        // how the managed tor is reaching the network (direct/80,443/bridges)
	Obfs4Available bool          // the obfs4 pluggable-transport binary is present (needed for obfs4 bridges)
	TorVersion     string        // the tor daemon version, e.g. "0.4.2.7" (for diagnostics)
	TorManaged     bool          // running a VayuPress-downloaded tor (not the system one)
	TorDownloadErr string        // last managed-download failure, if any (for the fallback hint)
	PageStatsOn    bool          // per-page onion counts are opted in
	TopPages       []PageHit     // most-visited onion pages (aggregate, only when PageStatsOn)
	Health         string        // coarse onion health (off/starting/healthy/degraded/down)
	HealthReason   string        // human reason for the current health state
	HealthSince    time.Time     // when the current health state was entered
	HealthLog      []HealthEvent // recent health transitions (newest last)
}

// Onion is one domain↔onion mapping for the admin table.
type Onion struct {
	Host      string
	OnionHost string
}

// PageHit is one aggregate per-page onion count for the admin table. It carries
// no time, identity, or ordering — only a cumulative total.
type PageHit struct {
	Host  string
	Path  string
	Count int64
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
		TorVersion:   e.torVer,
	}
	if e.managed != nil {
		st.LogPath = e.managed.logPath()
		st.Transport = transportLabel(e.esc, e.managed.usingBridges())
		st.Obfs4Available = e.managed.resolvePT() != ""
		st.TorManaged = e.managed.usingDistBinary()
		st.TorDownloadErr = e.managed.distDownloadErr()
		if !st.Connected || st.BootstrapPct < 100 {
			st.LogTail = e.managed.tailLog(3)
		}
	}
	for host, onionHost := range e.onionByHost {
		st.Onions = append(st.Onions, Onion{Host: host, OnionHost: onionHost})
	}
	sortOnions(st.Onions)
	st.PageStatsOn = e.pageStatsOn()
	if st.PageStatsOn {
		st.TopPages = e.topPages(topPagesShown)
	}
	st.Health, st.HealthReason, st.HealthSince, st.HealthLog = e.healthSnapshot()
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
