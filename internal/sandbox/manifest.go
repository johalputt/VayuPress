// Package sandbox provides process-isolated plugin execution (ADR-0056).
// Plugins declare a Manifest describing their capabilities; the sandbox
// launches them as child processes and communicates via JSON-over-stdio IPC.
// Crashes in a sandboxed plugin are fully contained — the main process is unaffected.
package sandbox

import (
	"errors"
	"strings"
	"time"
)

// ErrCapabilityDenied is returned when a plugin requests an operation outside
// its declared Manifest capabilities.
var ErrCapabilityDenied = errors.New("sandbox: capability denied")

// Manifest declares the identity and capability permissions of a sandboxed plugin.
// Operators must explicitly grant each capability; unlisted capabilities are denied.
type Manifest struct {
	// Name is a unique identifier for this plugin (used in logs and metrics).
	Name string

	// Executable is the path to the plugin binary. The binary must implement
	// the sandbox IPC protocol (read JSON requests from stdin, write JSON responses to stdout).
	Executable string

	// Args are optional arguments passed to the plugin binary on startup.
	Args []string

	// AllowedReadPaths lists filesystem path prefixes the plugin may read.
	// An empty list means no filesystem access is granted.
	AllowedReadPaths []string

	// AllowedWritePaths lists filesystem path prefixes the plugin may write.
	AllowedWritePaths []string

	// AllowNetwork permits the plugin to make outbound network calls.
	// Default false: no network access declared.
	AllowNetwork bool

	// Timeout is the maximum duration for a single hook invocation.
	// If zero, defaults to DefaultPluginTimeout.
	Timeout time.Duration

	// MaxRestarts is how many times a crashed plugin process will be restarted
	// before being quarantined. Zero means use DefaultMaxRestarts.
	MaxRestarts int

	// Env is a list of additional KEY=VALUE environment variables passed to the
	// plugin subprocess. Sensitive parent env vars are NOT inherited by default.
	Env []string
}

const (
	DefaultPluginTimeout = 2 * time.Second
	DefaultMaxRestarts   = 3
)

// Validate checks that the manifest has the minimum required fields.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return errors.New("sandbox.Manifest: Name is required")
	}
	if m.Executable == "" {
		return errors.New("sandbox.Manifest: Executable is required")
	}
	return nil
}

// AllowsReadPath returns true if the manifest grants read access to path.
func (m *Manifest) AllowsReadPath(path string) bool {
	for _, prefix := range m.AllowedReadPaths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// AllowsWritePath returns true if the manifest grants write access to path.
func (m *Manifest) AllowsWritePath(path string) bool {
	for _, prefix := range m.AllowedWritePaths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// effectiveTimeout returns the manifest timeout or the default.
func (m *Manifest) effectiveTimeout() time.Duration {
	if m.Timeout > 0 {
		return m.Timeout
	}
	return DefaultPluginTimeout
}

// effectiveMaxRestarts returns the manifest max restarts or the default.
func (m *Manifest) effectiveMaxRestarts() int {
	if m.MaxRestarts > 0 {
		return m.MaxRestarts
	}
	return DefaultMaxRestarts
}
