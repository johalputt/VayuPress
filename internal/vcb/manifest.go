package vcb

// manifest.go — the on-disk plugin manifest ("plugin.json"). Until VCB,
// sandbox.Manifest was only ever constructed in Go code: there was nothing a
// third-party developer could ship, nothing a validator could parse before
// load, and no declared link between a plugin and the hooks it subscribes to
// or the API permissions it needs. PluginManifest closes that gap. It is a
// declaration format — the runtime contract stays sandbox.Manifest, produced
// via ToSandboxManifest so a validated manifest maps 1:1 onto the enforced
// capabilities (deny by default; everything must be declared).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/johalputt/vayupress/internal/sandbox"
)

// ManifestVersion is the current VCB manifest schema version. A manifest
// declaring a higher version than the running host understands is rejected by
// the validator (fail-closed), never partially interpreted.
const ManifestVersion = 1

// ManifestFilename is the canonical file name an extension package carries at
// its root.
const ManifestFilename = "plugin.json"

// MaxManifestBytes bounds how much of a manifest file the loader reads —
// a manifest is a small declaration, not a data file.
const MaxManifestBytes = 256 << 10 // 256 KiB

// PluginManifest is the versioned, on-disk declaration of a plugin: identity,
// host-compatibility bounds, the hooks it subscribes to, the API permissions
// it wants, its sandbox capability requests, and optional distribution
// metadata. Every capability is opt-in; an empty manifest grants nothing.
type PluginManifest struct {
	// VCB is the manifest schema version (must equal ManifestVersion).
	VCB int `json:"vcb"`

	// Name uniquely identifies the plugin (required; used in logs, metrics,
	// and the registry key "name@version").
	Name string `json:"name"`

	// Version is the plugin artifact's own release version (required).
	Version string `json:"version"`

	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	License     string `json:"license,omitempty"`

	// MinHost / MaxHost bound the VayuPress versions this plugin supports.
	// Empty = open on that side. The validator refuses a plugin whose range
	// does not include the running host.
	MinHost string `json:"min_host,omitempty"`
	MaxHost string `json:"max_host,omitempty"`

	// Hooks lists the hook names this plugin subscribes to. Every entry must
	// be in the VCB hook catalogue (hooks.go) — a misspelled hook name used to
	// fail silently (it simply never fired); now it fails validation.
	Hooks []string `json:"hooks,omitempty"`

	// APIPermissions lists the "section:action" capabilities the plugin needs
	// when calling the VayuPress API with its own key. Each entry must be a
	// valid non-wildcard capability; the admin key-manager pre-checks exactly
	// these boxes when the operator mints the plugin's key (least privilege).
	APIPermissions []string `json:"api_permissions,omitempty"`

	// Executable is the plugin binary, as a path relative to the manifest's
	// directory (absolute paths are rejected — a package must be relocatable).
	Executable string `json:"executable"`

	// ExecutableSHA256 is the expected hex SHA-256 of the binary. When set,
	// the sandbox verifies it before every launch.
	ExecutableSHA256 string `json:"executable_sha256,omitempty"`

	// Args are optional arguments passed to the binary on startup.
	Args []string `json:"args,omitempty"`

	// Env is a list of KEY=VALUE variables for the subprocess (the parent
	// environment is never inherited).
	Env []string `json:"env,omitempty"`

	// Sandbox declares the runtime capability requests. Zero value = no
	// filesystem access, no network, default limits.
	Sandbox SandboxCaps `json:"sandbox,omitempty"`

	// Distribution optionally carries registry metadata for installable
	// packages (HTTPS download + artifact hash).
	Distribution *DistributionMeta `json:"distribution,omitempty"`
}

// SandboxCaps mirrors the enforced fields of sandbox.Manifest in a
// JSON-friendly shape. Every field is deny/zero by default.
type SandboxCaps struct {
	// AllowNetwork permits outbound network calls. Default false.
	AllowNetwork bool `json:"allow_network,omitempty"`

	// AllowedReadPaths / AllowedWritePaths are absolute path prefixes the
	// plugin may read/write. Empty = no filesystem access.
	AllowedReadPaths  []string `json:"allowed_read_paths,omitempty"`
	AllowedWritePaths []string `json:"allowed_write_paths,omitempty"`

	// TimeoutMS caps a single hook invocation, in milliseconds.
	// 0 = host default (2000).
	TimeoutMS int `json:"timeout_ms,omitempty"`

	// MaxRestarts before the plugin is quarantined. 0 = host default (3).
	MaxRestarts int `json:"max_restarts,omitempty"`

	// MaxMessageBytes caps one IPC response line. 0 = host default (1 MiB).
	MaxMessageBytes int64 `json:"max_message_bytes,omitempty"`

	// Resource ceilings (cgroup v2). 0 = unlimited.
	MemoryMaxBytes  int64 `json:"memory_max_bytes,omitempty"`
	CPUQuotaPercent int   `json:"cpu_quota_percent,omitempty"`
	MaxPIDs         int   `json:"max_pids,omitempty"`

	// RunAs is an optional numeric "uid:gid" the subprocess runs under.
	RunAs string `json:"run_as,omitempty"`
}

// DistributionMeta mirrors registry.PluginMeta's integrity fields for
// installable packages.
type DistributionMeta struct {
	// DownloadURL must be HTTPS.
	DownloadURL string `json:"download_url"`
	// SHA256 is the hex digest of the downloadable artifact.
	SHA256 string `json:"sha256"`
}

// LoadPluginManifest reads and parses a plugin.json. It rejects unknown
// top-level fields (a typo like "api_permission" must not silently declare
// nothing) and oversized files. Validation beyond JSON shape lives in
// internal/vcb/validate.
func LoadPluginManifest(path string) (*PluginManifest, error) {
	raw, err := readBounded(path)
	if err != nil {
		return nil, err
	}
	return ParsePluginManifest(raw)
}

// ParsePluginManifest parses manifest bytes with unknown-field rejection.
func ParsePluginManifest(raw []byte) (*PluginManifest, error) {
	var m PluginManifest
	if err := strictUnmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("vcb: parse plugin manifest: %w", err)
	}
	return &m, nil
}

// ToSandboxManifest converts a validated manifest into the runtime
// sandbox.Manifest, resolving Executable against baseDir (the directory the
// manifest was loaded from). Capability mapping is 1:1 — nothing is granted
// that was not declared, and the sandbox's own defaults (PID/IPC isolation
// on, no parent env) still apply.
func (m *PluginManifest) ToSandboxManifest(baseDir string) sandbox.Manifest {
	return sandbox.Manifest{
		Name:              m.Name,
		Executable:        filepath.Join(baseDir, m.Executable),
		Args:              append([]string(nil), m.Args...),
		Env:               append([]string(nil), m.Env...),
		AllowedReadPaths:  append([]string(nil), m.Sandbox.AllowedReadPaths...),
		AllowedWritePaths: append([]string(nil), m.Sandbox.AllowedWritePaths...),
		AllowNetwork:      m.Sandbox.AllowNetwork,
		Timeout:           time.Duration(m.Sandbox.TimeoutMS) * time.Millisecond,
		MaxRestarts:       m.Sandbox.MaxRestarts,
		MaxMessageBytes:   m.Sandbox.MaxMessageBytes,
		ExecutableHash:    m.ExecutableSHA256,
		RunAs:             m.Sandbox.RunAs,
		ResourceLimits: sandbox.ResourceLimits{
			MemoryMaxBytes:  m.Sandbox.MemoryMaxBytes,
			CPUQuotaPercent: m.Sandbox.CPUQuotaPercent,
			MaxPIDs:         m.Sandbox.MaxPIDs,
		},
		IsolatePID: true,
		IsolateIPC: true,
	}
}

// readBounded reads a manifest file, refusing anything over MaxManifestBytes.
func readBounded(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("vcb: read manifest: %w", err)
	}
	if fi.Size() > MaxManifestBytes {
		return nil, fmt.Errorf("vcb: manifest %s is %d bytes (max %d)", path, fi.Size(), MaxManifestBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("vcb: read manifest: %w", err)
	}
	return raw, nil
}

// strictUnmarshal decodes JSON while rejecting unknown fields.
func strictUnmarshal(raw []byte, v interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
