// Package plugins manages the hook registry and worker pool for plugin execution (ADR-0032/ADR-0046).
package plugins

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/resource"
	"github.com/johalputt/vayupress/internal/sandbox"
)

// HookFunc is the signature all plugin hooks must implement.
type HookFunc func(ctx context.Context, payload map[string]interface{}) error

// Registry holds the mapping from event names to registered hook functions.
type Registry struct {
	mu    sync.RWMutex
	hooks map[string][]HookFunc
}

// NewRegistry creates an empty hook registry.
func NewRegistry() *Registry {
	return &Registry{hooks: make(map[string][]HookFunc)}
}

// Register appends fn to the list of hooks invoked when event fires.
func (reg *Registry) Register(event string, fn HookFunc) {
	reg.mu.Lock()
	reg.hooks[event] = append(reg.hooks[event], fn)
	reg.mu.Unlock()
}

// Handlers returns a snapshot of handlers registered for event.
func (reg *Registry) Handlers(event string) []HookFunc {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	fns := reg.hooks[event]
	if len(fns) == 0 {
		return nil
	}
	out := make([]HookFunc, len(fns))
	copy(out, fns)
	return out
}

// pool constants
const (
	DefaultPoolSize   = 4
	DefaultQueueDepth = 32
	hookTimeout       = 2 * time.Second
	failThresh        = 5
)

type job struct {
	event   string
	fn      HookFunc
	payload map[string]interface{}
}

// Manager owns the plugin worker pool lifecycle (ADR-0032).
type Manager struct {
	registry *Registry
	queue    chan job
	failures sync.Map // key → int64
	disabled sync.Map // key → bool
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	started  bool
	mu       sync.Mutex
}

// New creates a Manager backed by the given registry.
// Call Start to launch the worker pool.
func New(registry *Registry) *Manager {
	return &Manager{registry: registry}
}

// Start launches poolSize worker goroutines with an internal queue of queueDepth.
func (m *Manager) Start(poolSize, queueDepth int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return
	}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.queue = make(chan job, queueDepth)
	m.started = true
	for i := 0; i < poolSize; i++ {
		m.wg.Add(1)
		go m.worker(i)
	}
	logging.LogInfo("plugins", fmt.Sprintf("pool started: workers=%d queue=%d (ADR-0032)", poolSize, queueDepth))
}

func (m *Manager) worker(id int) {
	defer m.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			atomic.AddInt64(&metrics.MetricPluginPanics, 1)
			logging.LogJSON(logging.LogFields{Level: "error", Component: "plugins", Msg: fmt.Sprintf("worker-%d PANIC: %v — terminated", id, r)})
		}
	}()
	for {
		select {
		case <-m.ctx.Done():
			// drain remaining jobs
			for {
				select {
				case j, ok := <-m.queue:
					if !ok {
						return
					}
					m.run(j)
				default:
					return
				}
			}
		case j, ok := <-m.queue:
			if !ok {
				return
			}
			m.run(j)
		}
	}
}

func (m *Manager) run(j job) {
	// Enforce per-plugin concurrency ceiling (ADR-0055).
	if lim := resource.Get("plugin.exec"); lim != nil {
		if err := lim.Acquire(); err != nil {
			atomic.AddInt64(&metrics.MetricPluginDisabled, 1)
			logging.LogJSON(logging.LogFields{Level: "warn", Component: "plugins", Msg: "plugin.exec at capacity — dropping: " + j.event})
			return
		}
		defer lim.Release()
	}
	timeout := time.Duration(config.Cfg.PluginTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = hookTimeout
	}
	key := fmt.Sprintf("%s:%p", j.event, j.fn)
	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	err := safeCall(j.event, j.fn, ctx, j.payload)
	cancel()
	if err != nil {
		v, _ := m.failures.LoadOrStore(key, int64(0))
		n := v.(int64) + 1
		m.failures.Store(key, n)
		if n >= failThresh {
			m.disabled.Store(key, true)
			atomic.AddInt64(&metrics.MetricPluginDisabled, 1)
			logging.LogJSON(logging.LogFields{Level: "warn", Component: "plugins", Msg: fmt.Sprintf("hook disabled after %d failures: %s", n, j.event)})
		}
	} else {
		m.failures.Store(key, int64(0))
	}
}

func safeCall(event string, fn HookFunc, ctx context.Context, payload map[string]interface{}) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			if len(stack) > 2048 {
				stack = stack[:2048]
			}
			atomic.AddInt64(&metrics.MetricPluginPanics, 1)
			logging.LogJSON(logging.LogFields{Level: "error", Component: "plugins", Msg: fmt.Sprintf("PANIC in hook %s: %v", event, r), Error: stack})
			err = fmt.Errorf("plugin panic in hook %s: %v", event, r)
		}
	}()
	return fn(ctx, payload)
}

// Fire dispatches event to all registered handlers via the worker pool.
// Drops events when the queue is full (records metric).
func (m *Manager) Fire(event string, payload map[string]interface{}) {
	m.mu.Lock()
	started := m.started
	m.mu.Unlock()
	if !started {
		return
	}
	for _, fn := range m.registry.Handlers(event) {
		key := fmt.Sprintf("%s:%p", event, fn)
		if disabled, ok := m.disabled.Load(key); ok && disabled.(bool) {
			continue
		}
		j := job{event: event, fn: fn, payload: payload}
		select {
		case m.queue <- j:
		default:
			atomic.AddInt64(&metrics.MetricPluginPoolDropped, 1)
			logging.LogJSON(logging.LogFields{Level: "warn", Component: "plugins", Msg: fmt.Sprintf("hook dropped — queue full: %s", event)})
		}
	}
}

// subprocessEntry pairs a manifest with its live pool.
type subprocessEntry struct {
	manifest sandbox.Manifest
	pool     *sandbox.Pool
}

// subprocessPlugins holds all registered subprocess pools (ADR-0056).
var (
	spMu      sync.RWMutex
	spEntries []*subprocessEntry
)

// RegisterSubprocess registers a sandboxed subprocess plugin and wires it into
// the registry so its hooks fire via the subprocess IPC protocol (ADR-0056).
// poolSize controls how many worker processes to launch.
func RegisterSubprocess(reg *Registry, m sandbox.Manifest, hookEvent string, poolSize int) error {
	pool, err := sandbox.NewPool(m, poolSize)
	if err != nil {
		return fmt.Errorf("plugins.RegisterSubprocess %s: %w", m.Name, err)
	}
	spMu.Lock()
	spEntries = append(spEntries, &subprocessEntry{manifest: m, pool: pool})
	spMu.Unlock()

	reg.Register(hookEvent, func(ctx context.Context, payload map[string]interface{}) error {
		return pool.Invoke(ctx, hookEvent, payload)
	})
	logging.LogInfo("plugins", fmt.Sprintf("subprocess plugin registered: name=%s hook=%s workers=%d", m.Name, hookEvent, poolSize))
	return nil
}

// SubprocessStats returns a snapshot of all registered subprocess plugin pools.
func SubprocessStats() []sandbox.SubprocessStats {
	spMu.RLock()
	defer spMu.RUnlock()
	var out []sandbox.SubprocessStats
	for _, e := range spEntries {
		out = append(out, e.pool.Stats()...)
	}
	return out
}

// ShutdownSubprocesses terminates all subprocess plugin pools.
func ShutdownSubprocesses() {
	spMu.Lock()
	defer spMu.Unlock()
	for _, e := range spEntries {
		e.pool.Shutdown()
	}
}

// Shutdown cancels context, closes queue, and waits for all workers to drain.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	logging.LogInfo("plugins", "cancelling context and closing queue")
	m.cancel()
	close(m.queue)
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
		logging.LogInfo("plugins", "all workers drained")
	case <-time.After(10 * time.Second):
		logging.LogJSON(logging.LogFields{Level: "warn", Component: "plugins", Msg: "drain timeout (10s) exceeded"})
	}
}
