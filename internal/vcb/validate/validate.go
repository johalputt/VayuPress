// SPDX-License-Identifier: Apache-2.0

// Package validate implements the static VCB compatibility checks (ADR-0135).
// It takes a parsed extension manifest and returns structured findings —
// ERROR means the extension WILL misbehave against the real contracts
// (compile abort, silent no-op, denied capability, refused install); WARN
// means it deserves the operator's attention. Every rule here is lifted from
// an enforcement point that already exists in the codebase (the theme
// compiler, the plugin sandbox, the registry, the API permission enum) — the
// validator never invents policy, it only surfaces the real one early.
package validate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/theme"
	"github.com/johalputt/vayupress/internal/vcb"
)

// Severity classifies a finding.
type Severity string

const (
	// Error findings make the extension incompatible; the CLI exits 1.
	Error Severity = "ERROR"
	// Warn findings are compatible but noteworthy.
	Warn Severity = "WARN"
)

// Finding is one validation result with a stable machine-readable code.
type Finding struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Field    string   `json:"field,omitempty"`
	Message  string   `json:"message"`
}

// Result collects the findings for one manifest.
type Result struct {
	Kind     string    `json:"kind"` // "plugin" or "theme"
	Name     string    `json:"name,omitempty"`
	Findings []Finding `json:"findings"`
}

// OK reports whether the manifest passed with no ERROR findings.
func (r *Result) OK() bool {
	for _, f := range r.Findings {
		if f.Severity == Error {
			return false
		}
	}
	return true
}

func (r *Result) errf(code, field, format string, args ...interface{}) {
	r.Findings = append(r.Findings, Finding{Severity: Error, Code: code, Field: field, Message: fmt.Sprintf(format, args...)})
}

func (r *Result) warnf(code, field, format string, args ...interface{}) {
	r.Findings = append(r.Findings, Finding{Severity: Warn, Code: code, Field: field, Message: fmt.Sprintf(format, args...)})
}

// Options tunes a validation run.
type Options struct {
	// HostVersion is the running VayuPress version (e.g. "3.13.41"). When
	// set, manifests with MinHost/MaxHost bounds are checked against it.
	HostVersion string

	// BaseDir is the directory the manifest was loaded from — the root the
	// plugin's relative Executable resolves against.
	BaseDir string

	// CheckFiles additionally verifies that the plugin executable exists
	// under BaseDir and, when ExecutableSHA256 is declared, that the on-disk
	// binary matches it (the same check the sandbox performs before launch).
	CheckFiles bool
}

// maxCustomCSSBytes is the hard cap on a theme's CustomCSS. The whole
// compiled stylesheet is served inline on every page load; a theme carrying
// more CSS than this is a performance defect on the low-resource VPSes
// VayuPress targets.
const maxCustomCSSBytes = 64 << 10 // 64 KiB

// warnCustomCSSBytes is the advisory threshold.
const warnCustomCSSBytes = 16 << 10 // 16 KiB

// ── Plugin ────────────────────────────────────────────────────────────────────

// Plugin statically validates a plugin manifest against the real sandbox,
// registry, hook, and API-permission contracts.
func Plugin(m *vcb.PluginManifest, opts Options) *Result {
	r := &Result{Kind: "plugin", Name: m.Name}

	checkManifestVersion(r, m.VCB)

	// Identity — mirrors sandbox.Manifest.Validate() and registry.Register()
	// (Name, Version required), plus package hygiene.
	if strings.TrimSpace(m.Name) == "" {
		r.errf("plugin.name.missing", "name", "name is required (sandbox and registry both refuse a nameless plugin)")
	} else if len(m.Name) > 100 {
		r.errf("plugin.name.length", "name", "name is %d characters (max 100)", len(m.Name))
	} else if strings.ContainsAny(m.Name, "/\\") {
		r.errf("plugin.name.chars", "name", "name must not contain path separators")
	}
	if strings.TrimSpace(m.Version) == "" {
		r.errf("plugin.version.missing", "version", "version is required (the registry keys packages by name@version)")
	} else if !vcb.ValidVersion(m.Version) {
		r.errf("plugin.version.invalid", "version", "version %q is not a dotted numeric version (e.g. 1.2.0)", m.Version)
	}
	if m.Description == "" {
		r.warnf("plugin.description.missing", "description", "a description helps operators review what they are installing")
	}
	if m.License == "" {
		r.warnf("plugin.license.missing", "license", "declare a license so operators know the terms")
	}

	checkHostRange(r, m.MinHost, m.MaxHost, opts.HostVersion)

	// Hooks — every subscription must be in the enumerated catalogue. A name
	// outside it would never fire (Manager.Fire finds zero handlers, silently).
	for _, h := range m.Hooks {
		if !vcb.ValidHook(vcb.HookName(h)) {
			r.errf("plugin.hook.unknown", "hooks", "hook %q is not in the VCB hook catalogue — it would never fire; valid hooks: %s", h, strings.Join(vcb.HookNames(), ", "))
		}
	}

	checkAPIPermissions(r, "plugin", m.APIPermissions)

	// Executable — required, relative, traversal-free (a package must be
	// relocatable and must not reach outside its own directory).
	switch {
	case strings.TrimSpace(m.Executable) == "":
		r.errf("plugin.executable.missing", "executable", "executable is required (path relative to the manifest)")
	case filepath.IsAbs(m.Executable):
		r.errf("plugin.executable.absolute", "executable", "executable must be a relative path inside the package, got %q", m.Executable)
	case pathEscapes(m.Executable):
		r.errf("plugin.executable.traversal", "executable", "executable path %q escapes the package directory", m.Executable)
	}

	if m.ExecutableSHA256 != "" && !validHexDigest(m.ExecutableSHA256) {
		r.errf("plugin.sha256.invalid", "executable_sha256", "executable_sha256 must be 64 hex characters")
	}

	if opts.CheckFiles && opts.BaseDir != "" && m.Executable != "" && !filepath.IsAbs(m.Executable) && !pathEscapes(m.Executable) {
		checkExecutableFile(r, opts.BaseDir, m.Executable, m.ExecutableSHA256)
	}

	// Env — the sandbox passes entries verbatim; a malformed one is inert.
	for _, e := range m.Env {
		if !strings.Contains(e, "=") {
			r.errf("plugin.env.malformed", "env", "env entry %q is not KEY=VALUE", e)
		}
	}

	checkSandboxCaps(r, m.Sandbox)

	// Distribution — mirrors registry.Install's integrity requirements.
	if d := m.Distribution; d != nil {
		if !strings.HasPrefix(d.DownloadURL, "https://") {
			r.errf("plugin.download.insecure", "distribution.download_url", "download_url must be HTTPS, got %q", d.DownloadURL)
		}
		if !validHexDigest(d.SHA256) {
			r.errf("plugin.download.sha256", "distribution.sha256", "distribution sha256 must be 64 hex characters (the registry verifies it on install)")
		}
	}

	return r
}

// checkSandboxCaps validates the declared runtime capability requests against
// the sandbox's real semantics, and surfaces the security-relevant ones.
func checkSandboxCaps(r *Result, c vcb.SandboxCaps) {
	for _, p := range c.AllowedReadPaths {
		if !filepath.IsAbs(p) {
			r.errf("plugin.sandbox.readpath", "sandbox.allowed_read_paths", "read path %q must be absolute (the sandbox matches path prefixes)", p)
		}
	}
	for _, p := range c.AllowedWritePaths {
		if !filepath.IsAbs(p) {
			r.errf("plugin.sandbox.writepath", "sandbox.allowed_write_paths", "write path %q must be absolute (the sandbox matches path prefixes)", p)
		}
	}
	if c.TimeoutMS < 0 || c.TimeoutMS > 600_000 {
		r.errf("plugin.sandbox.timeout", "sandbox.timeout_ms", "timeout_ms %d out of range [0, 600000]", c.TimeoutMS)
	}
	if c.MaxRestarts < 0 || c.MaxRestarts > 100 {
		r.errf("plugin.sandbox.restarts", "sandbox.max_restarts", "max_restarts %d out of range [0, 100]", c.MaxRestarts)
	}
	if c.MaxMessageBytes < 0 || c.MaxMessageBytes > 64<<20 {
		r.errf("plugin.sandbox.msgbytes", "sandbox.max_message_bytes", "max_message_bytes %d out of range [0, 64MiB]", c.MaxMessageBytes)
	}
	if c.MemoryMaxBytes < 0 {
		r.errf("plugin.sandbox.memory", "sandbox.memory_max_bytes", "memory_max_bytes must be >= 0")
	}
	if c.CPUQuotaPercent < 0 || c.CPUQuotaPercent > 1600 {
		r.errf("plugin.sandbox.cpu", "sandbox.cpu_quota_percent", "cpu_quota_percent %d out of range [0, 1600]", c.CPUQuotaPercent)
	}
	if c.MaxPIDs < 0 {
		r.errf("plugin.sandbox.pids", "sandbox.max_pids", "max_pids must be >= 0")
	}
	if c.RunAs != "" && !validRunAs(c.RunAs) {
		r.errf("plugin.sandbox.runas", "sandbox.run_as", "run_as %q must be numeric \"uid:gid\"", c.RunAs)
	}
	if c.AllowNetwork {
		r.warnf("plugin.sandbox.network", "sandbox.allow_network", "plugin declares outbound network access — operators should review why")
	}
	if len(c.AllowedWritePaths) > 0 {
		r.warnf("plugin.sandbox.writes", "sandbox.allowed_write_paths", "plugin declares filesystem write access to %d path prefix(es)", len(c.AllowedWritePaths))
	}
	if c.MemoryMaxBytes == 0 {
		r.warnf("plugin.sandbox.nomem", "sandbox.memory_max_bytes", "no memory ceiling declared — consider one; VayuPress targets low-resource hosts")
	}
}

// ── Theme ─────────────────────────────────────────────────────────────────────

// Theme statically validates a theme manifest against the exact rules the
// compiler enforces at apply time (hex colours abort the compile; bad
// dimensions and unsafe font characters are silently dropped; unknown option
// keys/values are silent no-ops) plus catalogue and CSP requirements.
func Theme(m *vcb.ThemeManifest, opts Options) *Result {
	t := m.Tokens
	r := &Result{Kind: "theme", Name: t.Name}

	checkManifestVersion(r, m.VCB)

	if strings.TrimSpace(t.Name) == "" {
		r.errf("theme.name.missing", "tokens.Name", "theme name is required")
	} else {
		for _, p := range theme.AllPresets() {
			if strings.EqualFold(p.Name, t.Name) {
				r.errf("theme.name.collision", "tokens.Name", "name %q collides with a built-in preset — pick a distinct name", t.Name)
				break
			}
		}
	}

	// Colours: a non-empty value that fails the compiler's hex check ABORTS
	// CompileCSS at apply time. Surface it now.
	for _, tv := range theme.ColorTokens(t) {
		if !theme.ValidHexColor(tv.Value) {
			r.errf("theme.color.invalid", "tokens."+tv.Field, "%s %q is not a valid hex colour (#rgb…#rrggbbaa) — the compiler refuses the whole theme", tv.Field, tv.Value)
		}
	}

	// Dimensions: the compiler SILENTLY DROPS invalid values — the theme
	// applies but not as designed. That silence is a defect; flag it.
	for _, tv := range theme.DimensionTokens(t) {
		if !theme.SafeDimensionOK(tv.Value) {
			r.errf("theme.dimension.invalid", "tokens."+tv.Field, "%s %q is not a number with optional unit (rem|em|px|ch|%%|vw|vh) — the compiler silently drops it", tv.Field, tv.Value)
		}
	}

	// Fonts: characters outside the sanitiser's allowlist are silently
	// stripped, so what renders would differ from what was declared.
	for _, tv := range theme.FontTokens(t) {
		if !theme.SafeFontOK(tv.Value) {
			r.errf("theme.font.unsafe", "tokens."+tv.Field, "%s %q contains characters the compiler strips (quotes, brackets, ;:(){}<> or non-ASCII) — declare the font stack without them", tv.Field, tv.Value)
		}
	}

	checkThemeOptions(r, t.Options)
	checkCustomCSS(r, t.CustomCSS)

	// Meta — the catalogue keys by name and filters by a fixed category set.
	if m.Meta.Name != "" && m.Meta.Name != t.Name {
		r.errf("theme.meta.name", "meta.name", "meta.name %q must equal tokens.Name %q (deploying an entry applies the preset by that name)", m.Meta.Name, t.Name)
	}
	if m.Meta.Category != "" && !validCategory(m.Meta.Category) {
		r.errf("theme.meta.category", "meta.category", "category %q is not one of %s", m.Meta.Category, strings.Join(theme.AllCategories(), ", "))
	}

	// Round-trip: the store persists Tokens as JSON; a theme that does not
	// survive marshal/unmarshal unchanged would drift on save.
	if !roundTripsClean(t) {
		r.errf("theme.roundtrip", "tokens", "tokens do not survive a JSON round-trip unchanged")
	}

	checkHostRange(r, m.MinHost, m.MaxHost, opts.HostVersion)
	checkAPIPermissions(r, "theme", m.APIPermissions)

	return r
}

// checkThemeOptions flags unknown option keys and out-of-vocabulary values —
// both are SILENT NO-OPS at apply time (applyThemeOptions ignores them). The
// schema is the FULL option vocabulary (theme.AllOptionDefs), not the per-name
// narrowing: applyThemeOptions honors every option by key regardless of theme
// name, so a third-party theme legitimately may set density/headingscale even
// though it can never carry a built-in preset name.
func checkThemeOptions(r *Result, opts map[string]string) {
	if len(opts) == 0 {
		return
	}
	schema := map[string]map[string]bool{}
	for _, o := range theme.AllOptionDefs() {
		vals := map[string]bool{}
		for _, c := range o.Choices {
			vals[c.Value] = true
		}
		schema[o.Key] = vals
	}
	for k, v := range opts {
		vals, ok := schema[k]
		if !ok {
			r.errf("theme.option.unknown", "tokens.options."+k, "option key %q is not in the option schema for this theme — it is a silent no-op; valid keys: %s", k, strings.Join(theme.OptionKeys(), ", "))
			continue
		}
		if !vals[v] {
			r.errf("theme.option.value", "tokens.options."+k, "option %q value %q is not an allowed choice — it is a silent no-op", k, v)
		}
	}
}

// checkCustomCSS enforces the CSP and performance contract on per-theme CSS.
// The site runs style-src 'self': external fetches would be blocked by the
// browser (and are a privacy leak by intent), so they are compatibility
// errors, not style choices.
func checkCustomCSS(r *Result, css string) {
	if css == "" {
		return
	}
	if len(css) > maxCustomCSSBytes {
		r.errf("theme.css.size", "tokens.custom_css", "custom_css is %d bytes (max %d) — it is inlined into every page load", len(css), maxCustomCSSBytes)
	} else if len(css) > warnCustomCSSBytes {
		r.warnf("theme.css.large", "tokens.custom_css", "custom_css is %d bytes — consider trimming; it is inlined into every page load", len(css))
	}
	lower := strings.ToLower(css)
	if strings.Contains(lower, "@import") {
		r.errf("theme.css.import", "tokens.custom_css", "@import is not allowed (CSP style-src 'self' blocks external stylesheets; zero third-party fetches is the contract)")
	}
	if externalURLRef(css) {
		r.errf("theme.css.external", "tokens.custom_css", "url() must not reference an external host (CSP style-src 'self'; zero third-party fetches is the contract)")
	}
	if strings.Contains(lower, "expression(") {
		r.errf("theme.css.expression", "tokens.custom_css", "expression() is not allowed")
	}
	if strings.Contains(css, "<") {
		r.errf("theme.css.markup", "tokens.custom_css", "custom_css must not contain '<' (markup inside CSS breaks the stylesheet and is an injection smell)")
	}
	checkThemeTokenRefs(r, css)
}

// vpVarRefRE matches a var(--vp-…) reference and captures the token name plus
// the delimiter that follows it: ')' means the reference has no fallback, ','
// means a fallback value is supplied.
var vpVarRefRE = regexp.MustCompile(`var\(\s*(--vp-[a-z0-9-]+)\s*([,)])`)

// checkThemeTokenRefs validates custom_css references to the sovereign --vp-*
// token namespace (ADR-0136 extends this to the new motion/elevation tokens).
// A theme never defines --vp-* itself — those are compiler-owned — so a
// var(--vp-…) reference to a name the compiler never emits, with no fallback,
// silently resolves to nothing: a typo that breaks the intended styling. It is
// reported as a WARNING (not an error): the token set can grow, and a stale
// validator must never fail a legitimate theme. References with a fallback are
// safe by construction and are not flagged.
func checkThemeTokenRefs(r *Result, css string) {
	valid := theme.VPTokenNames()
	seen := map[string]bool{}
	for _, m := range vpVarRefRE.FindAllStringSubmatch(css, -1) {
		name, delim := m[1], m[2]
		if delim == "," { // a fallback is supplied → safe even if the token is unknown
			continue
		}
		if valid[name] || seen[name] {
			continue
		}
		seen[name] = true
		r.warnf("theme.css.token.unknown", "tokens.custom_css",
			"custom_css references var(%s), which is not a VayuPress theme token — with no fallback it resolves to nothing; check the spelling (see the motion/elevation token reference in the VCB)", name)
	}
}

// cssWhitespace is the CSS token whitespace set (space, tab, LF, CR, FF). The
// original external-URL check trimmed only " \t", so a url() value pushed
// behind a newline slipped through.
const cssWhitespace = " \t\n\r\f"

// externalURLRef reports whether CSS references an external URL in any url(...)
// — http:, https:, or protocol-relative //. It defeats the two obfuscations a
// naive substring test misses: (1) CSS backslash escapes (url(\68ttps://…)
// resolves to https:// in the browser) and (2) any CSS whitespace, including
// newlines, before the value. Each url() body is CSS-unescaped, whitespace-
// and quote-trimmed, and lowercased before the scheme check, so the decoded
// value is what the browser would actually fetch.
func externalURLRef(css string) bool {
	lower := strings.ToLower(css)
	for i := 0; ; {
		j := strings.Index(lower[i:], "url(")
		if j < 0 {
			return false
		}
		start := i + j + 4
		body := lower[start:]
		if end := strings.IndexByte(body, ')'); end >= 0 {
			body = body[:end]
		}
		val := strings.Trim(cssUnescape(body), cssWhitespace)
		val = strings.Trim(val, "'\"")
		val = strings.Trim(val, cssWhitespace)
		val = strings.ToLower(val) // a hex escape may decode to an upper-case letter
		if strings.HasPrefix(val, "http:") || strings.HasPrefix(val, "https:") || strings.HasPrefix(val, "//") {
			return true
		}
		i = start
	}
}

// cssUnescape decodes CSS backslash escapes: "\" + 1–6 hex digits (optionally
// followed by one whitespace char) → that code point; "\" + any other char →
// that char literally. Other bytes pass through unchanged. This is what a
// browser does before resolving a url(), so url(\68ttps://evil) becomes
// https://evil — which the scheme check must see.
func cssUnescape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		i++ // consume the backslash
		if i >= len(s) {
			break
		}
		if isHexDigit(s[i]) {
			j := i
			for j < len(s) && j-i < 6 && isHexDigit(s[j]) {
				j++
			}
			if n, err := strconv.ParseInt(s[i:j], 16, 32); err == nil && n > 0 {
				b.WriteRune(rune(n))
			}
			i = j
			if i < len(s) && strings.IndexByte(cssWhitespace, s[i]) >= 0 {
				i++ // one optional trailing whitespace terminates a hex escape
			}
			continue
		}
		b.WriteByte(s[i]) // literal (non-hex) escape
		i++
	}
	return b.String()
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// ── Shared checks ─────────────────────────────────────────────────────────────

func checkManifestVersion(r *Result, v int) {
	if v != vcb.ManifestVersion {
		r.errf("manifest.vcb.unsupported", "vcb", "manifest declares vcb schema %d; this host understands %d (fail-closed: nothing is partially interpreted)", v, vcb.ManifestVersion)
	}
}

func checkHostRange(r *Result, minHost, maxHost, host string) {
	if minHost != "" && !vcb.ValidVersion(minHost) {
		r.errf("host.min.invalid", "min_host", "min_host %q is not a dotted numeric version", minHost)
	}
	if maxHost != "" && !vcb.ValidVersion(maxHost) {
		r.errf("host.max.invalid", "max_host", "max_host %q is not a dotted numeric version", maxHost)
	}
	if minHost != "" && maxHost != "" && vcb.ValidVersion(minHost) && vcb.ValidVersion(maxHost) &&
		vcb.CompareVersions(minHost, maxHost) > 0 {
		r.errf("host.range.inverted", "min_host", "min_host %s is above max_host %s", minHost, maxHost)
	}
	if host != "" && !vcb.HostInRange(host, minHost, maxHost) {
		r.errf("host.range", "min_host", "this VayuPress is %s, outside the declared host range [%s, %s]", host, orOpen(minHost), orOpen(maxHost))
	}
}

func checkAPIPermissions(r *Result, kind string, perms []string) {
	for _, p := range perms {
		if !vcb.ValidCapability(p) {
			r.errf(kind+".apiperm.unknown", "api_permissions", "capability %q is not a valid section:action token", p)
			continue
		}
		if vcb.WildcardCapability(p) {
			r.errf(kind+".apiperm.wildcard", "api_permissions", "capability %q uses a wildcard — extensions must declare least-privilege grants, never * ", p)
		}
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func validCategory(c string) bool {
	for _, k := range theme.AllCategories() {
		if k == c {
			return true
		}
	}
	return false
}

func validHexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// validRunAs mirrors the sandbox's own parse EXACTLY (applyRunAs uses
// strconv.ParseUint(part, 10, 32)): the value must be two colon-separated
// unsigned 32-bit integers. Atoi would wrongly accept a '+'-signed part or a
// value above the uint32 ceiling (e.g. "4294967296") that the sandbox then
// silently rejects — leaving the subprocess running as the host user, the very
// outcome the run_as declaration exists to avoid.
func validRunAs(s string) bool {
	uidgid := strings.Split(s, ":")
	if len(uidgid) != 2 {
		return false
	}
	for _, part := range uidgid {
		if _, err := strconv.ParseUint(part, 10, 32); err != nil {
			return false
		}
	}
	return true
}

// pathEscapes reports whether a relative path climbs out of its base
// directory via "..".
func pathEscapes(rel string) bool {
	clean := filepath.Clean(rel)
	return clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// checkExecutableFile verifies the plugin binary exists inside the package
// directory and, when a hash is declared, matches it — the same check the
// sandbox performs before every launch. ALL filesystem access is confined to
// baseDir through os.Root: the manifest-supplied executable name is resolved
// inside the root and can never reach a file outside the package, even via an
// absolute path, a "../" sequence, or a symlink pointing out of the tree. This
// is the traversal-safe path sink (no untrusted data reaches an unconfined
// os.Open/os.Stat).
func checkExecutableFile(r *Result, baseDir, exe, wantHash string) {
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		r.errf("plugin.executable.absent", "executable", "cannot open package directory: %v", err)
		return
	}
	defer root.Close()

	fi, err := root.Stat(exe)
	if err != nil {
		r.errf("plugin.executable.absent", "executable", "executable %s not found: %v", exe, err)
		return
	}
	if fi.IsDir() {
		r.errf("plugin.executable.dir", "executable", "executable %s is a directory", exe)
		return
	}
	if wantHash == "" || !validHexDigest(wantHash) {
		return
	}
	got, err := rootFileSHA256(root, exe)
	if err != nil {
		r.errf("plugin.sha256.unreadable", "executable_sha256", "could not hash executable: %v", err)
		return
	}
	if got != strings.ToLower(wantHash) {
		r.errf("plugin.sha256.mismatch", "executable_sha256", "executable hash %s does not match declared %s (the sandbox refuses to launch it)", got, strings.ToLower(wantHash))
	}
}

// rootFileSHA256 hashes a file opened relative to root, so access stays
// confined to the package directory.
func rootFileSHA256(root *os.Root, name string) (string, error) {
	f, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func roundTripsClean(t theme.Tokens) bool {
	b, err := json.Marshal(t)
	if err != nil {
		return false
	}
	var back theme.Tokens
	if err := json.Unmarshal(b, &back); err != nil {
		return false
	}
	return reflect.DeepEqual(t, back)
}

func orOpen(s string) string {
	if s == "" {
		return "open"
	}
	return s
}
