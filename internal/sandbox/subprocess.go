package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/trace"
)

// ErrQuarantined is returned when a plugin has exceeded its restart budget
// and has been permanently disabled until the process restarts.
var ErrQuarantined = errors.New("sandbox: plugin quarantined after repeated crashes")

// SubprocessPlugin manages the lifecycle of a single sandboxed plugin process.
// Call Invoke to dispatch a hook; the subprocess handles it and returns a Response.
// On crash the subprocess is restarted up to Manifest.MaxRestarts times before quarantine.
type SubprocessPlugin struct {
	manifest    Manifest
	mu          sync.Mutex
	cmd         *exec.Cmd
	stdin       *bufio.Writer
	stdout      *bufio.Scanner
	crashes     int
	quarantined bool
	invocations atomic.Int64
}

// NewSubprocessPlugin creates a SubprocessPlugin. Call Start before Invoke.
func NewSubprocessPlugin(m Manifest) *SubprocessPlugin {
	return &SubprocessPlugin{manifest: m}
}

// start launches the plugin subprocess. Caller must hold p.mu.
func (p *SubprocessPlugin) start() error {
	cmd := exec.Command(p.manifest.Executable, p.manifest.Args...)
	// Minimal, sanitised environment — parent env is NOT inherited.
	cmd.Env = append([]string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=/tmp",
		"PLUGIN_NAME=" + p.manifest.Name,
	}, p.manifest.Env...)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("sandbox: stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("sandbox: stdout pipe: %w", err)
	}
	// Discard stderr to prevent subprocess from polluting host output.
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("sandbox: start %s: %w", p.manifest.Executable, err)
	}
	p.cmd = cmd
	p.stdin = bufio.NewWriter(stdinPipe)
	p.stdout = bufio.NewScanner(stdoutPipe)

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
		if !resp.OK {
			return fmt.Errorf("plugin %s: %s", p.manifest.Name, resp.Error)
		}
		return nil
	}
}

// killSubprocess terminates the running subprocess immediately. Caller must hold p.mu.
func (p *SubprocessPlugin) killSubprocess() {
	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill() //nolint:errcheck
	}
	p.cmd = nil
	p.stdin = nil
	p.stdout = nil
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
