package validate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
	"github.com/johalputt/vayupress/internal/vcb"
)

// hasCode reports whether a result contains a finding with the given code.
func hasCode(r *Result, code string) bool {
	for _, f := range r.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

func codes(r *Result) string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, string(f.Severity)+":"+f.Code)
	}
	return strings.Join(out, ", ")
}

// goodPlugin returns a fully valid plugin manifest.
func goodPlugin() *vcb.PluginManifest {
	return &vcb.PluginManifest{
		VCB:            vcb.ManifestVersion,
		Name:           "seo-stamp",
		Version:        "1.2.0",
		Description:    "Stamps SEO metadata after publish.",
		License:        "MIT",
		MinHost:        "3.13.40",
		Hooks:          []string{"article.create", "article.update"},
		APIPermissions: []string{"posts:read", "posts:write"},
		Executable:     "bin/seo-stamp",
		Sandbox: vcb.SandboxCaps{
			AllowedReadPaths: []string{"/data/public"},
			TimeoutMS:        2000,
			MemoryMaxBytes:   128 << 20,
		},
	}
}

func TestPluginValidPassesCleanly(t *testing.T) {
	r := Plugin(goodPlugin(), Options{HostVersion: "3.13.41"})
	if !r.OK() {
		t.Fatalf("valid plugin must pass, got: %s", codes(r))
	}
	for _, f := range r.Findings {
		if f.Severity == Error {
			t.Errorf("unexpected error finding: %+v", f)
		}
	}
}

func TestPluginFailureClasses(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(m *vcb.PluginManifest)
		code   string
	}{
		{"unsupported schema", func(m *vcb.PluginManifest) { m.VCB = 99 }, "manifest.vcb.unsupported"},
		{"missing name", func(m *vcb.PluginManifest) { m.Name = " " }, "plugin.name.missing"},
		{"name with separator", func(m *vcb.PluginManifest) { m.Name = "a/b" }, "plugin.name.chars"},
		{"missing version", func(m *vcb.PluginManifest) { m.Version = "" }, "plugin.version.missing"},
		{"garbage version", func(m *vcb.PluginManifest) { m.Version = "one.two" }, "plugin.version.invalid"},
		{"unknown hook", func(m *vcb.PluginManifest) { m.Hooks = []string{"article.created.v1"} }, "plugin.hook.unknown"},
		{"legacy SPEC hook", func(m *vcb.PluginManifest) { m.Hooks = []string{"articles.write"} }, "plugin.hook.unknown"},
		{"unknown capability", func(m *vcb.PluginManifest) { m.APIPermissions = []string{"posts:sudo"} }, "plugin.apiperm.unknown"},
		{"wildcard capability", func(m *vcb.PluginManifest) { m.APIPermissions = []string{"*:*"} }, "plugin.apiperm.wildcard"},
		{"section wildcard", func(m *vcb.PluginManifest) { m.APIPermissions = []string{"posts:*"} }, "plugin.apiperm.wildcard"},
		{"missing executable", func(m *vcb.PluginManifest) { m.Executable = "" }, "plugin.executable.missing"},
		{"absolute executable", func(m *vcb.PluginManifest) { m.Executable = "/usr/bin/x" }, "plugin.executable.absolute"},
		{"traversal executable", func(m *vcb.PluginManifest) { m.Executable = "../../etc/passwd" }, "plugin.executable.traversal"},
		{"bad sha256", func(m *vcb.PluginManifest) { m.ExecutableSHA256 = "nothex" }, "plugin.sha256.invalid"},
		{"malformed env", func(m *vcb.PluginManifest) { m.Env = []string{"NOEQUALS"} }, "plugin.env.malformed"},
		{"relative read path", func(m *vcb.PluginManifest) { m.Sandbox.AllowedReadPaths = []string{"rel"} }, "plugin.sandbox.readpath"},
		{"relative write path", func(m *vcb.PluginManifest) { m.Sandbox.AllowedWritePaths = []string{"rel"} }, "plugin.sandbox.writepath"},
		{"timeout out of range", func(m *vcb.PluginManifest) { m.Sandbox.TimeoutMS = 700_000 }, "plugin.sandbox.timeout"},
		{"bad run_as", func(m *vcb.PluginManifest) { m.Sandbox.RunAs = "root" }, "plugin.sandbox.runas"},
		{"http download", func(m *vcb.PluginManifest) {
			m.Distribution = &vcb.DistributionMeta{DownloadURL: "http://x.example/p", SHA256: strings.Repeat("a", 64)}
		}, "plugin.download.insecure"},
		{"bad distribution hash", func(m *vcb.PluginManifest) {
			m.Distribution = &vcb.DistributionMeta{DownloadURL: "https://x.example/p", SHA256: "zz"}
		}, "plugin.download.sha256"},
		{"inverted host range", func(m *vcb.PluginManifest) { m.MinHost, m.MaxHost = "4.0.0", "3.0.0" }, "host.range.inverted"},
		{"host below min", func(m *vcb.PluginManifest) { m.MinHost = "9.0.0" }, "host.range"},
		{"host above max", func(m *vcb.PluginManifest) { m.MaxHost = "3.0.0" }, "host.range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := goodPlugin()
			tc.mutate(m)
			r := Plugin(m, Options{HostVersion: "3.13.41"})
			if r.OK() {
				t.Fatalf("expected %s to fail validation", tc.name)
			}
			if !hasCode(r, tc.code) {
				t.Errorf("expected code %s, got: %s", tc.code, codes(r))
			}
		})
	}
}

// TestPluginNetworkAndWritesAreWarnings pins that risky-but-legal declarations
// surface as WARN, never silently, and never as ERROR.
func TestPluginNetworkAndWritesAreWarnings(t *testing.T) {
	m := goodPlugin()
	m.Sandbox.AllowNetwork = true
	m.Sandbox.AllowedWritePaths = []string{"/data/tmp"}
	r := Plugin(m, Options{HostVersion: "3.13.41"})
	if !r.OK() {
		t.Fatalf("network+writes are legal declarations, got errors: %s", codes(r))
	}
	if !hasCode(r, "plugin.sandbox.network") || !hasCode(r, "plugin.sandbox.writes") {
		t.Errorf("expected network and writes warnings, got: %s", codes(r))
	}
}

// TestPluginFileChecks proves CheckFiles verifies existence and the same
// SHA-256 the sandbox enforces before launch.
func TestPluginFileChecks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("#!/bin/sh\necho plugin\n")
	if err := os.WriteFile(filepath.Join(dir, "bin", "seo-stamp"), body, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)

	m := goodPlugin()
	m.ExecutableSHA256 = hex.EncodeToString(sum[:])
	r := Plugin(m, Options{HostVersion: "3.13.41", BaseDir: dir, CheckFiles: true})
	if !r.OK() {
		t.Fatalf("matching hash must pass: %s", codes(r))
	}

	m.ExecutableSHA256 = strings.Repeat("0", 64)
	r = Plugin(m, Options{HostVersion: "3.13.41", BaseDir: dir, CheckFiles: true})
	if !hasCode(r, "plugin.sha256.mismatch") {
		t.Errorf("wrong hash must be caught, got: %s", codes(r))
	}

	m = goodPlugin()
	m.Executable = "bin/missing"
	r = Plugin(m, Options{HostVersion: "3.13.41", BaseDir: dir, CheckFiles: true})
	if !hasCode(r, "plugin.executable.absent") {
		t.Errorf("missing executable must be caught, got: %s", codes(r))
	}
}

// goodTheme returns a fully valid theme manifest (name distinct from every
// built-in preset).
func goodTheme() *vcb.ThemeManifest {
	return &vcb.ThemeManifest{
		VCB: vcb.ManifestVersion,
		Tokens: theme.Tokens{
			Name:         "Nightfall Test",
			BgDark:       "#0b1020",
			SurfaceDark:  "#141a2e",
			TextDark:     "#e2e8f0",
			MutedDark:    "#8ba4c4",
			AccentDark:   "#0ea5e9",
			Accent2Dark:  "#a78bfa",
			HiDark:       "#fbbf24",
			GreenDark:    "#34d399",
			BgLight:      "#ffffff",
			SurfaceLight: "#f1f5f9",
			TextLight:    "#0f172a",
			MutedLight:   "#475569",
			AccentLight:  "#0284c7",
			Accent2Light: "#7c3aed",
			HiLight:      "#b45309",
			FontSans:     "Inter, sans-serif",
			FontMono:     "JetBrains Mono, monospace",
			FontSizeBase: "1rem",
			LineHeight:   "1.6",
			MaxWidth:     "72rem",
			RadiusSm:     "6px",
			RadiusLg:     "14px",
			CustomCSS:    ".hero { letter-spacing: 0.02em; }",
			Options:      map[string]string{"width": "wide"},
		},
		Meta: theme.ThemeMeta{Name: "Nightfall Test", Category: "Dark"},
	}
}

func TestThemeValidPassesCleanly(t *testing.T) {
	// Guard the fixture itself: "width"="wide" must be a real choice.
	valid := false
	for _, o := range theme.AllOptions() {
		if o.Key == "width" {
			for _, c := range o.Choices {
				if c.Value == "wide" {
					valid = true
				}
			}
		}
	}
	if !valid {
		t.Skip("fixture option width=wide no longer in schema; update fixture")
	}
	r := Theme(goodTheme(), Options{HostVersion: "3.13.41"})
	if !r.OK() {
		t.Fatalf("valid theme must pass, got: %s", codes(r))
	}
}

func TestThemeFailureClasses(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(m *vcb.ThemeManifest)
		code   string
	}{
		{"unsupported schema", func(m *vcb.ThemeManifest) { m.VCB = 2 }, "manifest.vcb.unsupported"},
		{"missing name", func(m *vcb.ThemeManifest) { m.Tokens.Name = "" }, "theme.name.missing"},
		{"preset collision", func(m *vcb.ThemeManifest) { m.Tokens.Name = "Aurora"; m.Meta.Name = "Aurora" }, "theme.name.collision"},
		{"bad colour", func(m *vcb.ThemeManifest) { m.Tokens.BgDark = "blue" }, "theme.color.invalid"},
		{"bad dimension", func(m *vcb.ThemeManifest) { m.Tokens.MaxWidth = "calc(100% - 2rem)" }, "theme.dimension.invalid"},
		{"unsafe font", func(m *vcb.ThemeManifest) { m.Tokens.FontSans = `"Segoe UI"; }` }, "theme.font.unsafe"},
		{"unknown option key", func(m *vcb.ThemeManifest) { m.Tokens.Options = map[string]string{"nope": "x"} }, "theme.option.unknown"},
		{"unknown option value", func(m *vcb.ThemeManifest) { m.Tokens.Options = map[string]string{"width": "galactic"} }, "theme.option.value"},
		{"css import", func(m *vcb.ThemeManifest) { m.Tokens.CustomCSS = "@import url(x.css);" }, "theme.css.import"},
		{"css external url", func(m *vcb.ThemeManifest) { m.Tokens.CustomCSS = "body{background:url( 'https://x.example/a.png')}" }, "theme.css.external"},
		{"css protocol-relative", func(m *vcb.ThemeManifest) { m.Tokens.CustomCSS = "body{background:url(//x.example/a.png)}" }, "theme.css.external"},
		{"css markup", func(m *vcb.ThemeManifest) { m.Tokens.CustomCSS = "</style><script>1</script>" }, "theme.css.markup"},
		{"css oversized", func(m *vcb.ThemeManifest) { m.Tokens.CustomCSS = strings.Repeat("a", maxCustomCSSBytes+1) }, "theme.css.size"},
		{"meta name mismatch", func(m *vcb.ThemeManifest) { m.Meta.Name = "Other" }, "theme.meta.name"},
		{"bad category", func(m *vcb.ThemeManifest) { m.Meta.Category = "Cyberpunk" }, "theme.meta.category"},
		{"wildcard api perm", func(m *vcb.ThemeManifest) { m.APIPermissions = []string{"themes:*"} }, "theme.apiperm.wildcard"},
		{"host below min", func(m *vcb.ThemeManifest) { m.MinHost = "9.9.9" }, "host.range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := goodTheme()
			tc.mutate(m)
			r := Theme(m, Options{HostVersion: "3.13.41"})
			if r.OK() {
				t.Fatalf("expected %s to fail validation", tc.name)
			}
			if !hasCode(r, tc.code) {
				t.Errorf("expected code %s, got: %s", tc.code, codes(r))
			}
		})
	}
}

// TestThemeLocalURLAllowed pins that same-origin url() references stay legal —
// only external fetches violate the CSP contract.
func TestThemeLocalURLAllowed(t *testing.T) {
	m := goodTheme()
	m.Tokens.CustomCSS = ".hero{background:url(/static/media/bg.png)}"
	r := Theme(m, Options{HostVersion: "3.13.41"})
	if !r.OK() {
		t.Fatalf("local url() must be allowed, got: %s", codes(r))
	}
}

// TestBuiltinPresetsSatisfyThemeContract runs every shipped preset through the
// validator (minus the name-collision rule, which by construction they all
// trip). If a built-in preset ever violates the published contract, either the
// preset or the contract is wrong — both are release blockers.
func TestBuiltinPresetsSatisfyThemeContract(t *testing.T) {
	for _, p := range theme.AllPresets() {
		m := &vcb.ThemeManifest{VCB: vcb.ManifestVersion, Tokens: p}
		r := Theme(m, Options{})
		for _, f := range r.Findings {
			if f.Severity != Error || f.Code == "theme.name.collision" {
				continue
			}
			t.Errorf("built-in preset %q violates the VCB theme contract: [%s] %s: %s", p.Name, f.Code, f.Field, f.Message)
		}
	}
}

// TestVersionHelpers pins the host-range comparison semantics.
func TestVersionHelpers(t *testing.T) {
	if vcb.CompareVersions("3.13.41", "v3.13.41") != 0 {
		t.Error("v prefix must be ignored")
	}
	if vcb.CompareVersions("3.13.9", "3.13.41") != -1 {
		t.Error("numeric compare, not lexicographic")
	}
	if vcb.CompareVersions("3.13", "3.13.0") != 0 {
		t.Error("missing parts count as zero")
	}
	if !vcb.HostInRange("3.13.41", "3.13.0", "") || vcb.HostInRange("3.12.0", "3.13.0", "") {
		t.Error("open-ended min bound broken")
	}
	if !vcb.ValidVersion("1.2.3") || vcb.ValidVersion("") || vcb.ValidVersion("one") {
		t.Error("ValidVersion misclassifies")
	}
}
