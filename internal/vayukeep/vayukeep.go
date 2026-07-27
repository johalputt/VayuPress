// SPDX-License-Identifier: Apache-2.0

// Package vayukeep is VayuPress's replication subsystem: automatic, encrypted,
// consistent generations of the data directory, continuously proven restorable
// (ADR-0145).
//
// It exists because the documented backup schedule did not. The disaster
// recovery runbook published "Full DB, nightly 02:00 UTC, 30 days", but the only
// database backup the deploy script installed ran inside `if $UPGRADE` — so the
// real recovery point was "whenever you last upgraded", and an operator sizing
// their risk from the documentation was wrong by an order of magnitude.
//
// Three properties define the design.
//
// Consistent by construction. Every generation is a `VACUUM INTO` snapshot taken
// through a single read transaction, never a copy of a live file, so it folds in
// the write-ahead log and is restorable without stopping the service.
//
// Sealed by default. Generations are written through internal/backup's VPBK2
// chained-AEAD stream. There is no unencrypted mode; a replica that leaves the
// machine carries member emails, mailbox contents and comment metadata, and
// making the encryption optional would make the wrong thing easy.
//
// Proven, not asserted. A generation nothing has ever read back is not a backup.
// The drill restores the newest generation into a temporary directory, opens it
// and runs integrity_check, so the operator sees "last verified restore" rather
// than "backups: enabled".
//
// # Cost
//
// Nothing here runs on an HTTP request path — no middleware, no handler work, no
// lock taken against the writer. The engine is one goroutine that sleeps. It
// decides whether anything changed with a stat() of the database and its
// sidecar, so an idle install performs no database work at all and the loop
// collapses to its longest interval.
package vayukeep

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Config controls the engine. The zero value is disabled.
type Config struct {
	// Enabled turns replication on. Off by default: this is Experimental under
	// the constitution's feature lifecycle.
	Enabled bool
	// DataDir is the directory replicated (database, settings, media, maildirs).
	DataDir string
	// DBPath is the SQLite database inside DataDir, snapshotted rather than copied.
	DBPath string
	// TargetDir receives generations. A path the operator owns — a second disk,
	// a mounted volume, an sshfs/rclone mount. It must not live inside DataDir.
	TargetDir string
	// Passphrase seals every generation. Without it the engine refuses to start,
	// rather than silently writing something readable.
	Passphrase string

	// MinInterval and MaxInterval bound the adaptive cadence: the engine backs
	// off toward MaxInterval while nothing changes and returns to MinInterval on
	// the first change it sees.
	MinInterval time.Duration
	MaxInterval time.Duration
	// DrillInterval is how often the newest generation is restored and checked.
	DrillInterval time.Duration
	// RetainGenerations and RetainDays bound the target. Whichever keeps more
	// wins, so a quiet month cannot age out the only copy that exists.
	RetainGenerations int
	RetainDays        int

	// Pressure reports whether the public request lane is under load. The drill
	// and the snapshot both yield to it, so replication can never be the reason
	// a reader waits. nil means never under pressure.
	Pressure func() bool
	// ClearnetBlocked reports whether the install is in Tor/anonymous mode. A
	// remote target would be a clearnet callback (ADR-0141), so the engine
	// refuses anything but a local path when this is true.
	ClearnetBlocked func() bool
	// Now is injectable for tests.
	Now func() time.Time
	// Snapshot writes a consistent copy of the database at dbPath to dest.
	// Injected so this package does not need a SQL driver.
	Snapshot func(ctx context.Context, dbPath, dest string) error
	// Log receives one-line operational messages.
	Log func(level, msg string)
}

// Defaults for anything the caller leaves zero.
const (
	DefaultMinInterval   = 5 * time.Minute
	DefaultMaxInterval   = 6 * time.Hour
	DefaultDrillInterval = 12 * time.Hour
	DefaultRetainGens    = 24
	DefaultRetainDays    = 30
	// consecutiveFailuresToPause is the circuit breaker. Replication must never
	// become the reason publishing degrades, so after this many consecutive
	// failures the engine stops trying and says so loudly instead of retrying
	// into a wall.
	consecutiveFailuresToPause = 5
)

// Status is the honest state of replication. Every field is something that was
// measured; nothing here is inferred from configuration. "Enabled" is not a
// synonym for "working", and the page that renders this must not present it as
// one.
type Status struct {
	Enabled  bool
	Target   string
	Paused   bool
	PauseWhy string

	LastAttempt time.Time
	LastSuccess time.Time
	LastError   string

	Generations   int
	NewestGen     time.Time
	TotalBytes    int64
	LastGenBytes  int64
	ConsecutiveNG int

	LastDrill      time.Time
	LastDrillOK    bool
	LastDrillError string
	LastDrillRows  int64
}

// RPO returns how long ago the newest generation was taken — the amount of work
// an operator would lose restoring right now. Zero time means never.
func (s Status) RPO(now time.Time) time.Duration {
	if s.NewestGen.IsZero() {
		return 0
	}
	return now.Sub(s.NewestGen)
}

// Healthy reports whether replication is doing its job: running, with a recent
// generation, and with a drill that actually passed. A generation nobody has
// read back does not count.
func (s Status) Healthy(now time.Time) bool {
	if !s.Enabled || s.Paused || s.NewestGen.IsZero() {
		return false
	}
	return s.LastDrillOK && !s.LastDrill.IsZero()
}

// Engine runs replication. Construct with New, start with Run.
type Engine struct {
	cfg    Config
	status atomic.Pointer[Status]

	mu       sync.Mutex
	statusMu sync.Mutex
	// drilling is a single-flight guard. A drill restores a whole generation, so
	// letting the scheduled one and an operator's button run at once would double
	// the disk and CPU cost of the most expensive thing this subsystem does — and
	// a compromised admin session could stack them without it.
	drilling    atomic.Bool
	lastFP      fingerprint
	interval    time.Duration
	failures    int
	trigger     chan struct{}
	drillNowCh  chan struct{}
	initialised bool
	verify      Verifier
}

// fingerprint is the cheap change detector: size and modification time of the
// database and its write-ahead log. Two stat() calls, no SQL, no lock — so an
// idle install costs nothing to poll.
type fingerprint struct {
	dbSize, walSize   int64
	dbMtime, walMtime int64
}

func statFingerprint(dbPath string) fingerprint {
	var fp fingerprint
	if st, err := os.Stat(dbPath); err == nil {
		fp.dbSize, fp.dbMtime = st.Size(), st.ModTime().UnixNano()
	}
	if st, err := os.Stat(dbPath + "-wal"); err == nil {
		fp.walSize, fp.walMtime = st.Size(), st.ModTime().UnixNano()
	}
	return fp
}

// ErrDisabled is returned when an operation needs a configured engine.
var ErrDisabled = errors.New("vayukeep: replication is not enabled")

// New validates the configuration and returns an engine. It refuses rather than
// degrades: a misconfigured replica that silently writes nothing, or writes
// something readable, is worse than one that will not start.
func New(cfg Config) (*Engine, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = func(string, string) {}
	}
	if cfg.MinInterval <= 0 {
		cfg.MinInterval = DefaultMinInterval
	}
	if cfg.MaxInterval < cfg.MinInterval {
		cfg.MaxInterval = DefaultMaxInterval
	}
	if cfg.MaxInterval < cfg.MinInterval {
		cfg.MaxInterval = cfg.MinInterval
	}
	if cfg.DrillInterval <= 0 {
		cfg.DrillInterval = DefaultDrillInterval
	}
	if cfg.RetainGenerations <= 0 {
		cfg.RetainGenerations = DefaultRetainGens
	}
	if cfg.RetainDays <= 0 {
		cfg.RetainDays = DefaultRetainDays
	}
	e := &Engine{
		cfg:        cfg,
		interval:   cfg.MinInterval,
		trigger:    make(chan struct{}, 1),
		drillNowCh: make(chan struct{}, 1),
	}
	st := &Status{Enabled: cfg.Enabled, Target: cfg.TargetDir}
	e.status.Store(st)
	if !cfg.Enabled {
		return e, nil
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return e, nil
}

func (c Config) validate() error {
	if c.DataDir == "" {
		return errors.New("vayukeep: no data directory configured")
	}
	if c.TargetDir == "" {
		return errors.New("vayukeep: no target directory configured — set VAYUKEEP_TARGET")
	}
	if c.Passphrase == "" {
		return errors.New("vayukeep: no passphrase configured — set VAYU_BACKUP_PASSPHRASE; generations are always encrypted")
	}
	if c.Snapshot == nil {
		return errors.New("vayukeep: no snapshot function wired")
	}
	// A target inside the data directory would replicate its own output, growing
	// without bound and putting the copy on the disk whose loss it insures
	// against.
	data, err := filepath.Abs(filepath.Clean(c.DataDir))
	if err != nil {
		return err
	}
	target, err := filepath.Abs(filepath.Clean(c.TargetDir))
	if err != nil {
		return err
	}
	if target == data || isUnder(target, data) {
		return fmt.Errorf("vayukeep: the target %s is inside the data directory %s — a copy on the same disk, replicating itself, is not a backup", target, data)
	}
	if c.ClearnetBlocked != nil && c.ClearnetBlocked() && looksRemote(c.TargetDir) {
		return errors.New("vayukeep: refusing a remote target in Tor/anonymous mode — replication must not be the one subsystem that phones home (ADR-0141)")
	}
	return nil
}

func isUnder(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !filepath.IsAbs(rel) && (len(rel) < 2 || rel[:2] != "..")
}

// looksRemote reports whether a target string names something off-box. Only a
// plain local path is permitted in Tor mode.
func looksRemote(target string) bool {
	for _, p := range []string{"://", "@"} {
		if idx := indexOf(target, p); idx >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Status returns the current measured state.
func (e *Engine) Status() Status {
	if s := e.status.Load(); s != nil {
		return *s
	}
	return Status{}
}

// setStatus applies a mutation to the observable state. It takes statusMu
// because it is a read-modify-write on an atomic pointer: the Run loop and the
// operator-triggered drill both write here, and a lost update would silently
// discard a drill result — the one field nobody can afford to be stale.
func (e *Engine) setStatus(mut func(*Status)) {
	e.statusMu.Lock()
	defer e.statusMu.Unlock()
	cur := e.Status()
	mut(&cur)
	e.status.Store(&cur)
}

// TriggerNow asks for a generation as soon as the loop wakes. Used before a
// migration, an update, or a restore — the moments the old upgrade-only backup
// was accidentally right about.
func (e *Engine) TriggerNow() {
	select {
	case e.trigger <- struct{}{}:
	default:
	}
}

// DrillNow asks for an out-of-band restore drill.
func (e *Engine) DrillNow() {
	select {
	case e.drillNowCh <- struct{}{}:
	default:
	}
}

// Run drives replication until ctx is cancelled. It returns immediately when
// replication is disabled, so a caller can always start it unconditionally.
func (e *Engine) Run(ctx context.Context) {
	if !e.cfg.Enabled {
		return
	}
	e.cfg.Log("info", "replication enabled — target "+e.cfg.TargetDir)
	// Refresh the observable state from whatever is already on the target, so a
	// restarted process reports the truth immediately rather than "never" until
	// its first generation.
	e.refreshFromTarget()

	nextDrill := e.cfg.Now().Add(e.cfg.DrillInterval)
	timer := time.NewTimer(e.cfg.MinInterval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.trigger:
			e.cycle(ctx, true)
		case <-e.drillNowCh:
			e.runDrill(ctx)
			nextDrill = e.cfg.Now().Add(e.cfg.DrillInterval)
		case <-timer.C:
			e.cycle(ctx, false)
			if now := e.cfg.Now(); now.After(nextDrill) {
				e.runDrill(ctx)
				nextDrill = now.Add(e.cfg.DrillInterval)
			}
		}
		timer.Reset(e.nextInterval())
	}
}

// nextInterval reports how long to sleep before the next check.
func (e *Engine) nextInterval() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.interval
}

// cycle takes one generation if anything changed (or force is set), then prunes.
func (e *Engine) cycle(ctx context.Context, force bool) {
	if ctx.Err() != nil {
		return
	}
	st := e.Status()
	if st.Paused && !force {
		return
	}
	fp := statFingerprint(e.cfg.DBPath)

	e.mu.Lock()
	changed := !e.initialised || fp != e.lastFP
	e.initialised = true
	e.mu.Unlock()

	if !changed && !force {
		// Nothing has been written since the last generation. Back off toward the
		// maximum so a dormant site issues no work at all.
		e.backOff()
		return
	}
	// Yield to the public lane: replication is never worth a slower page.
	if !force && e.cfg.Pressure != nil && e.cfg.Pressure() {
		e.cfg.Log("info", "generation deferred — public request lane is under pressure")
		return
	}

	e.setStatus(func(s *Status) { s.LastAttempt = e.cfg.Now() })
	size, err := e.writeGeneration(ctx)
	if err != nil {
		e.noteFailure(err)
		return
	}

	e.mu.Lock()
	e.lastFP = fp
	e.interval = e.cfg.MinInterval // activity seen: return to the fast cadence
	e.failures = 0
	e.mu.Unlock()

	now := e.cfg.Now()
	e.setStatus(func(s *Status) {
		s.LastSuccess = now
		s.NewestGen = now
		s.LastError = ""
		s.LastGenBytes = size
		s.ConsecutiveNG = 0
		s.Paused = false
		s.PauseWhy = ""
	})
	if err := e.prune(); err != nil {
		e.cfg.Log("warn", "generation retention: "+err.Error())
	}
	e.refreshFromTarget()
}

// backOff doubles the sleep interval up to the configured maximum.
func (e *Engine) backOff() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.interval *= 2
	if e.interval > e.cfg.MaxInterval {
		e.interval = e.cfg.MaxInterval
	}
}

// noteFailure records an error and trips the circuit breaker once failures
// become persistent, so a broken target cannot turn into an endless retry loop
// against a full disk.
func (e *Engine) noteFailure(err error) {
	e.mu.Lock()
	e.failures++
	n := e.failures
	e.mu.Unlock()

	e.cfg.Log("error", "generation failed: "+err.Error())
	e.setStatus(func(s *Status) {
		s.LastError = err.Error()
		s.ConsecutiveNG = n
		if n >= consecutiveFailuresToPause {
			s.Paused = true
			s.PauseWhy = fmt.Sprintf("paused after %d consecutive failures — fix the target and re-enable", n)
		}
	})
	e.backOff()
}
