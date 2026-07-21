package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/johalputt/vayupress/internal/logging"
)

// ApplyOptions configures a verified binary apply.
type ApplyOptions struct {
	Current    string
	DryRun     bool
	PubKeyHex  string
	DBPath     string
	BackupDir  string
	BinaryPath string // path to the currently-running binary to replace (os.Executable())

	// AllowUnsigned permits applying a release using SHA-256 checksum
	// verification alone when no pinned release public key is configured
	// (PubKeyHex is empty). When a pinned key IS present the Ed25519 signature is
	// always required regardless of this flag. The CLI leaves this false
	// (strict, signature-mandatory); the authenticated admin UI sets it so an
	// operator can update in one click without first provisioning a signing key.
	AllowUnsigned bool

	// IncludePrerelease opts into the development channel: GitHub pre-releases
	// (unreleased builds) become eligible for install, not just stable releases.
	// Verification is unchanged — checksum always, signature when a key is pinned.
	IncludePrerelease bool
}

// Guard injects a mode lookup so apply can refuse in unsafe modes.
type Guard struct {
	CurrentMode func() string
}

// PreflightMode refuses an apply when the runtime is in a mode that forbids
// mutating the binary (read-only, quarantined, maintenance). It is the subset of
// PreflightApply that the authenticated admin UI enforces: an operator's
// explicit, admin-role-checked click is itself the opt-in, so the env flag and a
// pinned key are not required there (verification still happens in
// ApplyVerified — checksum always, signature when a key is pinned).
func PreflightMode(currentMode string) error {
	switch strings.ToLower(strings.TrimSpace(currentMode)) {
	case "read-only", "readonly":
		return errors.New("update: apply refused — system mode is read-only")
	case "quarantined":
		return errors.New("update: apply refused — system mode is quarantined")
	case "maintenance":
		return errors.New("update: apply refused — system mode is maintenance")
	}
	return nil
}

// PreflightApply runs all safety gates and returns an error if apply must not
// proceed:
//   - VAYU_SELFUPDATE_ENABLED must be "true" (enabled==true)
//   - mode must not be read-only / quarantined / maintenance
//   - pinned pubkey must be present
//
// This is the strict gate used by the CLI. The admin UI uses PreflightMode plus
// ApplyOptions.AllowUnsigned instead.
func PreflightApply(enabled bool, currentMode string, pubKeyHex string) error {
	if !enabled {
		return errors.New("update: apply refused — set VAYU_SELFUPDATE_ENABLED=true to opt in")
	}
	if err := PreflightMode(currentMode); err != nil {
		return err
	}
	if strings.TrimSpace(pubKeyHex) == "" {
		return errors.New("update: apply refused — pinned release public key (VAYU_RELEASE_PUBKEY) is empty")
	}
	return nil
}

// ApplyVerified downloads the release binary plus its .sig and .sha256, verifies
// the checksum AND the Ed25519 signature against the pinned public key, backs up
// the database, then atomically replaces the running binary. In DryRun it
// verifies everything but does NOT replace. It never restarts/execs the process;
// it returns the new version and prints restart instructions to the operator.
func ApplyVerified(ctx context.Context, client *http.Client, owner, repo string, opt ApplyOptions, st *Store) (string, error) {
	if client == nil {
		return "", fmt.Errorf("update: nil http client")
	}

	rel, err := CheckLatestChannel(ctx, client, owner, repo, opt.IncludePrerelease)
	if err != nil {
		return "", err
	}
	if !UpdateAvailable(opt.Current, rel.Version) {
		return "", fmt.Errorf("update: no newer release available (current=%s latest=%s)", opt.Current, rel.Version)
	}

	// Verification policy: the SHA-256 checksum is ALWAYS verified. The Ed25519
	// signature is required whenever a release key is pinned; if none is pinned,
	// apply proceeds on checksum alone only when the caller opted in
	// (AllowUnsigned, i.e. an authenticated admin clicking Update).
	verifySig := strings.TrimSpace(opt.PubKeyHex) != ""
	if !verifySig && !opt.AllowUnsigned {
		return "", errors.New("update: apply refused — pinned release public key (VAYU_RELEASE_PUBKEY) is empty")
	}

	binAsset := selectBinaryAsset(rel.Assets, runtime.GOOS, runtime.GOARCH)
	if binAsset == nil {
		return "", fmt.Errorf("update: release %s has no installable binary asset", rel.Version)
	}
	sumAsset := selectChecksumAsset(rel.Assets, binAsset.Name)
	if sumAsset == nil {
		return "", fmt.Errorf("update: release %s is missing a .sha256 checksum for %s", rel.Version, binAsset.Name)
	}
	var sigAsset *Asset
	if verifySig {
		sigAsset = selectSignatureAsset(rel.Assets, binAsset.Name)
		if sigAsset == nil {
			return "", fmt.Errorf("update: release %s missing a .sig asset for %s (required because a release public key is pinned)", rel.Version, binAsset.Name)
		}
	}

	binData, err := download(ctx, client, binAsset.DownloadURL)
	if err != nil {
		return "", fmt.Errorf("update: download binary: %w", err)
	}
	// Turn the most confusing failure into a clear one: when a proxy or network
	// hiccup between this server and GitHub's download CDN returns an HTML/JSON
	// error page (or a truncated transfer) instead of the binary, the bytes hash
	// to something that will never match — surfacing as a cryptic "checksum
	// mismatch". Detect that here and say what actually happened, so the operator
	// knows it is a transport problem, not a corrupt or mismatched release.
	if why := binaryDownloadProblem(binData); why != "" {
		return "", fmt.Errorf("update: the release binary did not download correctly — %s. "+
			"This is a network/proxy problem between this server and GitHub's download CDN, not a checksum problem with the release itself. "+
			"Retry; if it persists, make sure the server can reach github.com and its release-download hosts "+
			"(release-assets.githubusercontent.com, objects.githubusercontent.com) outbound", why)
	}
	sumData, err := download(ctx, client, sumAsset.DownloadURL)
	if err != nil {
		return "", fmt.Errorf("update: download checksum: %w", err)
	}

	// Checksum must pass before any signature check or write. checksumForFile
	// tolerates both a per-binary ".sha256" ("<hex>  <file>") and a combined
	// SHA256SUMS listing (many files), picking the line for this binary.
	expectedHex := checksumForFile(sumData, binAsset.Name)
	if err := VerifyChecksum(binData, expectedHex); err != nil {
		return "", fmt.Errorf("%w — the %d bytes downloaded do not match the release's published SHA-256. "+
			"This normally means the download was corrupted or intercepted in transit (a proxy/CDN issue), not a bad release; retry the update", err, len(binData))
	}

	if verifySig {
		sigData, derr := download(ctx, client, sigAsset.DownloadURL)
		if derr != nil {
			return "", fmt.Errorf("update: download sig: %w", derr)
		}
		if err := VerifySignature(opt.PubKeyHex, binData, strings.TrimSpace(string(sigData))); err != nil {
			return "", err
		}
		logging.LogInfo("update", fmt.Sprintf("verified release %s (checksum + Ed25519 signature OK)", rel.Version))
	} else {
		logging.LogInfo("update", fmt.Sprintf("verified release %s (SHA-256 checksum OK; signature check skipped — no pinned release key)", rel.Version))
	}

	if opt.DryRun {
		logging.LogInfo("update", "dry-run — verification passed, binary NOT replaced")
		return rel.Version, nil
	}

	// Always back up the database before mutating the binary.
	backupPath := ""
	if opt.DBPath != "" {
		bp, berr := CreateBackup(opt.DBPath, opt.BackupDir)
		if berr != nil {
			return "", fmt.Errorf("update: backup failed, aborting apply: %w", berr)
		}
		backupPath = bp
		logging.LogInfo("update", "database backed up to "+backupPath)
	}

	if opt.BinaryPath == "" {
		return "", fmt.Errorf("update: empty binary path — cannot replace")
	}
	if err := atomicReplace(opt.BinaryPath, binData); err != nil {
		return "", err
	}

	logging.LogInfo("update", fmt.Sprintf("binary replaced: %s → %s (old kept at %s.bak)", opt.Current, rel.Version, opt.BinaryPath))
	_ = backupPath // surfaced by caller via history record
	return rel.Version, nil
}

// ResolveInstallPath returns the real on-disk path of the binary to replace,
// following symlinks. An enterprise deployment runs VayuPress from a
// service-owned, writable directory (e.g. /var/lib/vayupress/bin/vayupress) and
// exposes /usr/local/bin/vayupress as a convenience symlink to it. In that
// layout the atomic swap must target the resolved real file in the writable
// directory — replacing the symlink itself would fail on a read-only /usr and
// would not update the running binary. A "(deleted)" marker (left after a prior
// swap unlinked the old inode) is stripped first; on any error the input path is
// returned unchanged so single-file layouts behave exactly as before.
func ResolveInstallPath(execPath string) string {
	p := strings.TrimSuffix(strings.TrimSpace(execPath), " (deleted)")
	if p == "" {
		return execPath
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// atomicReplace writes data to a temp file in the same dir, makes it executable,
// keeps the old binary as <target>.bak, then os.Rename over the target. Falls
// back to copy+chmod if rename fails (e.g. cross-device).
func atomicReplace(target string, data []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".vayupress-update-*")
	if err != nil {
		return fmt.Errorf("update: temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op if successfully renamed away

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("update: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("update: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("update: close temp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("update: chmod temp: %w", err)
	}

	// Keep the old binary as a .bak rollback artifact (best-effort copy).
	if err := copyFile(target, target+".bak", 0o755); err != nil {
		logging.LogError("update", "could not back up old binary", err.Error())
	}

	if err := os.Rename(tmpName, target); err != nil {
		// Cross-device or other rename failure → fall back to copy+chmod.
		if cerr := copyFile(tmpName, target, 0o755); cerr != nil {
			return fmt.Errorf("update: rename failed (%v) and copy fallback failed: %w", err, cerr)
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "vayupress-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 512<<20)) // 512 MiB cap
}

// metadataAssetSuffixes lists the non-executable artefacts that are commonly
// attached to a release alongside the binary (checksums, signatures, SBOMs,
// notes). None of these may ever be mistaken for the binary to install.
var metadataAssetSuffixes = []string{
	".sha256", ".sha512", ".sha1", ".md5",
	".sig", ".asc", ".pem", ".pub", ".cert", ".crt",
	".bundle", ".sbom", ".spdx", ".json", ".cdx",
	".txt", ".md", ".sum",
}

// isMetadataAsset reports whether name is a release sidecar rather than the
// executable itself.
func isMetadataAsset(name string) bool {
	n := strings.ToLower(name)
	for _, s := range metadataAssetSuffixes {
		if strings.HasSuffix(n, s) {
			return true
		}
	}
	return false
}

// archAliases maps a Go GOARCH to the substrings release artefacts commonly use
// for the same architecture, so a download matches whatever naming a release
// adopts.
var archAliases = map[string][]string{
	"amd64":   {"amd64", "x86_64", "x64"},
	"arm64":   {"arm64", "aarch64"},
	"arm":     {"armv7", "armv6", "armhf", "arm"},
	"386":     {"386", "i386", "x86"},
	"ppc64le": {"ppc64le"},
	"s390x":   {"s390x"},
	"riscv64": {"riscv64"},
}

// selectBinaryAsset chooses the release asset that is the executable for the
// running platform. It first discards every checksum/signature/SBOM sidecar,
// then — when a release ships builds for several platforms — prefers the asset
// whose name advertises the running GOOS and GOARCH so the correct build is
// installed. When exactly one binary candidate remains it is returned as-is,
// which keeps single-binary releases (VayuPress's own) working unchanged.
func selectBinaryAsset(assets []Asset, goos, goarch string) *Asset {
	cands := make([]*Asset, 0, len(assets))
	for i := range assets {
		if isMetadataAsset(assets[i].Name) {
			continue
		}
		cands = append(cands, &assets[i])
	}
	if len(cands) == 0 {
		return nil
	}
	if len(cands) == 1 {
		return cands[0]
	}

	wantArch := archAliases[goarch]
	if len(wantArch) == 0 {
		wantArch = []string{goarch}
	}
	var osOnlyMatch *Asset
	for _, a := range cands {
		n := strings.ToLower(a.Name)
		if goos != "" && !strings.Contains(n, goos) {
			continue
		}
		if osOnlyMatch == nil {
			osOnlyMatch = a
		}
		for _, al := range wantArch {
			if strings.Contains(n, al) {
				return a // exact OS + arch match
			}
		}
	}
	if osOnlyMatch != nil {
		return osOnlyMatch // right OS, arch not encoded in the name
	}
	return cands[0] // no platform hints in any name — best effort
}

// selectChecksumAsset finds the .sha256 file that verifies the chosen binary.
// It prefers an exact "<binary>.sha256" sibling and otherwise falls back to the
// sole .sha256 asset when a release ships just one.
func selectChecksumAsset(assets []Asset, binaryName string) *Asset {
	return selectSidecar(assets, binaryName, ".sha256")
}

// selectSignatureAsset finds the Ed25519 .sig file for the chosen binary, with
// the same exact-sibling-then-sole-asset preference as selectChecksumAsset.
func selectSignatureAsset(assets []Asset, binaryName string) *Asset {
	return selectSidecar(assets, binaryName, ".sig")
}

// selectSidecar returns the asset named "<binaryName><suffix>" if present, else
// the only asset carrying suffix when a release ships exactly one.
func selectSidecar(assets []Asset, binaryName, suffix string) *Asset {
	want := strings.ToLower(binaryName) + suffix
	var sole *Asset
	count := 0
	for i := range assets {
		n := strings.ToLower(assets[i].Name)
		if n == want {
			return &assets[i]
		}
		if strings.HasSuffix(n, suffix) {
			sole = &assets[i]
			count++
		}
	}
	if count == 1 {
		return sole
	}
	return nil
}

// checksumForFile extracts the SHA-256 hex for binaryName from a checksum file.
// It accepts both a per-binary ".sha256" ("<hex>  <file>", the format produced by
// `sha256sum <file>`) and a combined listing (many "<hex>  <file>" lines, e.g.
// SHA256SUMS / checksums.txt): with several lines it returns the hash on the line
// whose filename column matches the binary; with one line it returns its hash.
func checksumForFile(sumData []byte, binaryName string) string {
	base := strings.ToLower(binaryName)
	firstTok := ""
	for _, ln := range strings.Split(string(sumData), "\n") {
		fields := strings.Fields(strings.TrimSpace(ln))
		if len(fields) == 0 {
			continue
		}
		if firstTok == "" {
			firstTok = fields[0]
		}
		if len(fields) >= 2 {
			// The filename column may be "*name" (binary mode), "name", or a path.
			fn := strings.ToLower(strings.TrimPrefix(fields[len(fields)-1], "*"))
			if fn == base || strings.HasSuffix(fn, "/"+base) {
				return fields[0]
			}
		}
	}
	return firstTok
}

// binaryDownloadProblem returns a human-readable reason when the downloaded
// "binary" is obviously not a VayuPress executable — an empty response, or an
// HTML/JSON error page from a proxy/CDN — else "". It lets the updater report a
// transport failure as such instead of a cryptic checksum error. (A subtly
// truncated-but-binary transfer still fails the checksum, whose error now says
// so.)
func binaryDownloadProblem(data []byte) string {
	trimmed := bytes.TrimSpace(data)
	switch {
	case len(data) == 0:
		return "the download was empty (0 bytes)"
	case bytes.HasPrefix(trimmed, []byte("<")) || bytes.HasPrefix(trimmed, []byte("{")) || bytes.HasPrefix(trimmed, []byte("[")):
		return fmt.Sprintf("the download returned a %d-byte HTML/JSON page, not a binary", len(data))
	}
	return ""
}

// RestartInstructions returns operator guidance after a successful apply.
func RestartInstructions(newVersion string) string {
	return fmt.Sprintf(
		"VayuPress %s installed. The running process was NOT restarted.\n"+
			"Restart via your service manager to activate, e.g.:\n"+
			"  sudo systemctl restart vayupress\n"+
			"Rollback (if needed): move <binary>.bak back over the binary, then restart.",
		newVersion)
}
