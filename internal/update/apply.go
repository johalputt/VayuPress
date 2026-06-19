package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
}

// Guard injects a mode lookup so apply can refuse in unsafe modes.
type Guard struct {
	CurrentMode func() string
}

// PreflightApply runs all safety gates and returns an error if apply must not
// proceed:
//   - VAYU_SELFUPDATE_ENABLED must be "true" (enabled==true)
//   - mode must not be read-only / quarantined / maintenance
//   - pinned pubkey must be present
func PreflightApply(enabled bool, currentMode string, pubKeyHex string) error {
	if !enabled {
		return errors.New("update: apply refused — set VAYU_SELFUPDATE_ENABLED=true to opt in")
	}
	switch strings.ToLower(strings.TrimSpace(currentMode)) {
	case "read-only", "readonly":
		return errors.New("update: apply refused — system mode is read-only")
	case "quarantined":
		return errors.New("update: apply refused — system mode is quarantined")
	case "maintenance":
		return errors.New("update: apply refused — system mode is maintenance")
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

	rel, err := CheckLatest(ctx, client, owner, repo)
	if err != nil {
		return "", err
	}
	if !UpdateAvailable(opt.Current, rel.Version) {
		return "", fmt.Errorf("update: no newer release available (current=%s latest=%s)", opt.Current, rel.Version)
	}

	binAsset := findAsset(rel.Assets, []string{".sig", ".sha256"}, true)
	sigAsset := findAsset(rel.Assets, []string{".sig"}, false)
	sumAsset := findAsset(rel.Assets, []string{".sha256"}, false)
	if binAsset == nil || sigAsset == nil || sumAsset == nil {
		return "", fmt.Errorf("update: release %s missing binary, .sig, or .sha256 asset", rel.Version)
	}

	binData, err := download(ctx, client, binAsset.DownloadURL)
	if err != nil {
		return "", fmt.Errorf("update: download binary: %w", err)
	}
	sigData, err := download(ctx, client, sigAsset.DownloadURL)
	if err != nil {
		return "", fmt.Errorf("update: download sig: %w", err)
	}
	sumData, err := download(ctx, client, sumAsset.DownloadURL)
	if err != nil {
		return "", fmt.Errorf("update: download checksum: %w", err)
	}

	// Verify checksum, then signature — both must pass before any write.
	expectedHex := firstHexToken(string(sumData))
	if err := VerifyChecksum(binData, expectedHex); err != nil {
		return "", err
	}
	if err := VerifySignature(opt.PubKeyHex, binData, strings.TrimSpace(string(sigData))); err != nil {
		return "", err
	}

	logging.LogInfo("update", fmt.Sprintf("verified release %s (checksum + Ed25519 signature OK)", rel.Version))

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

// findAsset returns the first asset whose name matches the criteria. When
// exclude is true, suffixes is treated as a deny-list (return first asset that
// matches none); otherwise suffixes is an allow-list.
func findAsset(assets []Asset, suffixes []string, exclude bool) *Asset {
	for i := range assets {
		name := strings.ToLower(assets[i].Name)
		matched := false
		for _, s := range suffixes {
			if strings.HasSuffix(name, s) {
				matched = true
				break
			}
		}
		if exclude && !matched {
			return &assets[i]
		}
		if !exclude && matched {
			return &assets[i]
		}
	}
	return nil
}

// firstHexToken extracts the leading whitespace-delimited token (the typical
// `sha256sum` output format is "<hex>  <filename>").
func firstHexToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\n"); i >= 0 {
		return s[:i]
	}
	return s
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
