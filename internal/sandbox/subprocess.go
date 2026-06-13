package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/trace"
)

// ErrQuarantined is returned when a plugin has exceeded its restart budget
// and has been permanently disabled until the process restarts.
var ErrQuarantined = errors.New("sandbox: plugin quarantined after repeated crashes")

// isEPERM returns true if the error wraps a syscall.EPERM permission error.
func isEPERM(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.EPERM
	}
	return false
}

// SubprocessPlugin manages the lifecycle of a single sandboxed plugin process.
// Call Invoke to dispatch a hook; the subprocess handles it and returns a Response.
// On crash the subprocess is restarted up to Manifest.MaxRestarts times before quarantine.
type SubprocessPlugin struct {
	manifest      Manifest
	mu            sync.Mutex
	cmd           *exec.Cmd
	stdin         *bufio.Writer
	stdout        *bufio.Scanner
	crashes       int
	quarantined   bool
	invocations   atomic.Int64
	cgroupCleanup func()
	confinement   *PluginConfinement
	telemetry     PluginTelemetry
}

// NewSubprocessPlugin creates a SubprocessPlugin. Call Start before Invoke.
func NewSubprocessPlugin(m Manifest) *SubprocessPlugin {
	return &SubprocessPlugin{
		manifest:      m,
		cgroupCleanup: func() {},
		confinement:   &PluginConfinement{},
	}
}

// start launches the plugin subprocess. Caller must hold p.mu.
func (p *SubprocessPlugin) start() error {
	// Verify binary integrity before launching.
	if p.manifest.ExecutableHash != "" {
		if err := verifyExecutableHash(p.manifest.Executable, p.manifest.ExecutableHash); err != nil {
			return err
		}
	}

	// P28: Set up filesystem confinement (tmpfs scratch, mount namespace).
	p.confinement = SetupConfinement(p.manifest)

	// P28: Close stray inherited FDs before exec — CLOEXEC all extras.
	CloseExtraFDs(nil)

	cmd := exec.Command(p.manifest.Executable, p.manifest.Args...)
	// Minimal, sanitised environment — parent env is NOT inherited.
	cmd.Env = PrepareExecEnv(p.manifest, p.confinement.ScratchDir())

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		p.confinement.Cleanup()
		return fmt.Errorf("sandbox: stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		p.confinement.Cleanup()
		return fmt.Errorf("sandbox: stdout pipe: %w", err)
	}
	// Discard stderr to prevent subprocess from polluting host output.
	cmd.Stderr = nil

	applyProcAttr(cmd)
	applyRunAs(cmd, p.manifest.RunAs)
	// P27 namespaces + P28 mount namespace.
	nsFlags := namespaceCloneflags(p.manifest) | MountNamespaceFlags(p.manifest)
	applyNamespaceFlags(cmd, nsFlags)

	if err := cmd.Start(); err != nil {
		// EPERM may mean the kernel rejected namespace creation (e.g. unprivileged user).
		// Retry without namespace flags so the plugin still starts.
		if isEPERM(err) && nsFlags != 0 {
			logging.LogJSON(logging.LogFields{
				Level:     "warn",
				Component: "sandbox",
				Msg:       fmt.Sprintf("sandbox: namespace flags rejected (EPERM) for %s — retrying without namespaces", p.manifest.Name),
			})
			cmd2 := exec.Command(p.manifest.Executable, p.manifest.Args...)
			cmd2.Env = cmd.Env
			stdinPipe2, err2 := cmd2.StdinPipe()
			if err2 != nil {
				return fmt.Errorf("sandbox: stdin pipe (retry): %w", err2)
			}
			stdoutPipe2, err2 := cmd2.StdoutPipe()
			if err2 != nil {
				return fmt.Errorf("sandbox: stdout pipe (retry): %w", err2)
			}
			cmd2.Stderr = nil
			applyProcAttr(cmd2)
			applyRunAs(cmd2, p.manifest.RunAs)
			// Cloneflags intentionally left zero — no namespace isolation.
			if err2 := cmd2.Start(); err2 != nil {
				return fmt.Errorf("sandbox: start %s: %w", p.manifest.Executable, err2)
			}
			cmd = cmd2
			stdinPipe = stdinPipe2
			stdoutPipe = stdoutPipe2
		} else {
			return fmt.Errorf("sandbox: start %s: %w", p.manifest.Executable, err)
		}
	}
	p.cgroupCleanup = setupCgroup(p.manifest, cmd.Process.Pid)
	p.cmd = cmd
	p.stdin = bufio.NewWriter(stdinPipe)

	maxBytes := p.manifest.effectiveMaxMessageBytes()
	scanner := bufio.NewScanner(io.LimitReader(stdoutPipe, maxBytes))
	scanner.Buffer(make([]byte, 64*1024), int(maxBytes))
	p.stdout = scanner

	// Goroutine to detect unexpected process exit and log it.
	go func() {
		if err := cmd.Wait(); err != nil {
			logging.LogJSON(logging.LogFields{
				Level:     "warn",
				Component: "sandbox",
				Msg:       fmt.Sprintf("plugin %s exited: %v", p.manifest.Name, err),
			})
		}
	}()

	logging.LogInfo("sandbox", fmt.Sprintf("started plugin subprocess: %s pid=%d", p.manifest.Name, cmd.Process.Pid))
	return nil
}

// Invoke sends a hook invocation to the subprocess and reads the response.
// If the subprocess is dead it is restarted (up to max restarts).
// A timed-out invocation kills and restarts the subprocess.
func (p *SubprocessPlugin) Invoke(ctx context.Context, hook string, payload map[string]interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.quarantined {
		return ErrQuarantined
	}

	// Enforce capabilities before allowing the invocation.
	if err := EnforceCapabilities(p.manifest, hook, payload); err != nil {
		return err
	}

	// Ensure subprocess is running.
	if p.cmd == nil || p.cmd.ProcessState != nil {
		if err := p.restart(); err != nil {
			return err
		}
	}

	corrID := trace.CorrelationID(ctx)
	causID := trace.CausationID(ctx)

	req := Request{
		HookName:      hook,
		Payload:       payload,
		CorrelationID: corrID,
		CausationID:   causID,
		TraceID:       corrID,
		Capabilities: Capabilities{
			AllowNetwork:      p.manifest.AllowNetwork,
			AllowedReadPaths:  p.manifest.AllowedReadPaths,
			AllowedWritePaths: p.manifest.AllowedWritePaths,
		},
	}

	line, err := marshalRequest(req)
	if err != nil {
		return fmt.Errorf("sandbox: marshal request: %w", err)
	}

	// Write request with timeout enforcement.
	timeout := p.manifest.effectiveTimeout()
	deadline := time.Now().Add(timeout)

	// Send request.
	if _, err := p.stdin.Write(append(line, '\n')); err != nil {
		return p.handleCrash(fmt.Errorf("sandbox: write: %w", err))
	}
	if err := p.stdin.Flush(); err != nil {
		return p.handleCrash(fmt.Errorf("sandbox: flush: %w", err))
	}

	// Read response with deadline.
	respCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		if p.stdout.Scan() {
			respCh <- p.stdout.Bytes()
		} else {
			errCh <- fmt.Errorf("sandbox: stdout closed (plugin crashed?)")
		}
	}()

	select {
	case <-time.After(time.Until(deadline)):
		p.telemetry.TimeoutCount.Add(1)
		p.killSubprocess()
		return p.handleCrash(fmt.Errorf("sandbox: plugin %s timed out after %s", p.manifest.Name, timeout))
	case <-ctx.Done():
		p.killSubprocess()
		return ctx.Err()
	case err := <-errCh:
		return p.handleCrash(err)
	case respBytes := <-respCh:
		resp, err := unmarshalResponse(respBytes)
		if err != nil {
			return fmt.Errorf("sandbox: parse response: %w", err)
		}
		// Forward plugin log lines through host logging pipeline.
		for _, ll := range resp.LogLines {
			logging.LogJSON(logging.LogFields{
				Level:         ll.Level,
				Component:     "plugin:" + p.manifest.Name,
				CorrelationID: corrID,
				Msg:           ll.Message,
			})
		}
		p.invocations.Add(1)
		p.telemetry.InvocationCount.Add(1)
		if !resp.OK {
			p.telemetry.FailureCount.Add(1)
			return fmt.Errorf("plugin %s: %s", p.manifest.Name, resp.Error)
		}
		p.telemetry.SuccessCount.Add(1)
		return nil
	}
}

// Telemetry returns a point-in-time snapshot of plugin runtime metrics.
func (p *SubprocessPlugin) Telemetry() TelemetrySnapshot {
	return p.telemetry.snapshot(p.manifest.Name)
}

// killSubprocess terminates the running subprocess immediately. Caller must hold p.mu.
func (p *SubprocessPlugin) killSubprocess() {
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill() //nolint:errcheck
	}
	p.cmd = nil
	p.stdin = nil
	p.stdout = nil
	if p.cgroupCleanup != nil {
		p.cgroupCleanup()
		p.cgroupCleanup = func() {}
	}
	if p.confinement != nil {
		p.confinement.Cleanup()
		p.confinement = &PluginConfinement{}
	}
}

// handleCrash increments the crash counter and quarantines the plugin if the
// restart budget is exhausted. Caller must hold p.mu.
func (p *SubprocessPlugin) handleCrash(cause error) error {
	p.crashes++
	p.killSubprocess()
	logging.LogJSON(logging.LogFields{
		Level:     "warn",
		Component: "sandbox",
		Msg:       fmt.Sprintf("plugin %s crash #%d: %v", p.manifest.Name, p.crashes, cause),
	})
	if p.crashes >= p.manifest.effectiveMaxRestarts() {
		p.quarantined = true
		logging.LogJSON(logging.LogFields{
			Level:     "error",
			Component: "sandbox",
			Msg:       fmt.Sprintf("plugin %s quarantined after %d crashes", p.manifest.Name, p.crashes),
		})
		return ErrQuarantined
	}
	return cause
}

// restart starts a fresh subprocess after a crash. Caller must hold p.mu.
func (p *SubprocessPlugin) restart() error {
	if p.quarantined {
		return ErrQuarantined
	}
	if err := p.start(); err != nil {
		return p.handleCrash(err)
	}
	return nil
}

// Stats returns a snapshot of the subprocess plugin state.
func (p *SubprocessPlugin) Stats() SubprocessStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	running := p.cmd != nil && p.cmd.ProcessState == nil
	var pid int
	if p.cmd != nil && p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	return SubprocessStats{
		Name:        p.manifest.Name,
		Running:     running,
		PID:         pid,
		Crashes:     p.crashes,
		Quarantined: p.quarantined,
		Invocations: p.invocations.Load(),
	}
}

// Shutdown gracefully terminates the subprocess.
func (p *SubprocessPlugin) Shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.killSubprocess()
}

// SubprocessStats is a point-in-time snapshot of subprocess state.
type SubprocessStats struct {
	Name        string `json:"name"`
	Running     bool   `json:"running"`
	PID         int    `json:"pid,omitempty"`
	Crashes     int    `json:"crashes"`
	Quarantined bool   `json:"quarantined"`
	Invocations int64  `json:"invocations"`
}
