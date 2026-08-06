// SPDX-License-Identifier: Apache-2.0

package config

import (
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const ConfigVersion = "1.0"
const MinCompatibleConfigVersion = "1.0"

// DefaultMediaQuotaGB is the media-directory ceiling when MEDIA_QUOTA_GB is not
// set, and the floor the enforcement falls back to when MediaQuotaBytes is unset.
//
// 5 GB, chosen against what the upload path can actually put there: a raster is
// capped at 8 MB and a PDF at 32 MB, and rasters are downscaled before they land,
// so a real library of optimised images runs to tens of megabytes and 5 GB is
// orders of magnitude above any blog that is not being attacked.
//
// What it does NOT promise, because the first draft of this comment promised it:
// that the database will have room. ~5 GiB is about 2.5% of the 200 GB default
// STORAGE_QUOTA_GB, which means a media library at its ceiling cannot on its own
// trip the storage quota that gates article creation — a relationship between two
// configured numbers, not a fact about the volume. STORAGE_QUOTA_GB is
// self-declared and is never compared against free space (statfs is read only to
// draw the Storage panel), so on a small disk 5 GiB of media is a real fraction of
// it. The honest guarantee is narrower and still worth having: the media library's
// contribution to filling the disk is now bounded and knowable instead of open-
// ended. Sizing the volume remains the operator's.
//
// The trade is deliberate in that direction: an operator who genuinely wants a
// bigger library raises one variable and sees a clear refusal telling them so,
// whereas the previous default — no ceiling at all — was only discovered by the
// install going down.
const DefaultMediaQuotaGB = 5

var Cfg struct {
	APIKey      string
	DBPath      string
	CacheDir    string
	MediaDir    string
	Domain      string
	Port        string
	WorkerCount int
	CFZoneID    string
	CFAPIToken  string
	IndexNowKey string
	// APIHost is an optional dedicated, CDN-proxy-off host for the REST API
	// (e.g. api.<domain>), so machine clients reach /api without a CDN
	// bot-challenge. Empty means the REST API is advertised on the apex domain.
	APIHost string
	// OnionMode selects the whole-install Tor / anonymous world (VAYUOS_MODE=tor):
	// the install is a self-contained VayuOS "Tor Space" (ADR-0141) that must never
	// make clearnet callbacks (IndexNow, webmention, …) which would phone home and
	// de-anonymise it. The default (VAYUOS_MODE=clearnet, or unset) preserves every
	// existing behaviour — this flag only ever removes clearnet egress, never adds.
	OnionMode      bool
	StorageQuotaGB int64
	// MediaQuotaBytes caps the total size of MediaDir. storeValidatedMedia
	// content-addresses what it stores, so duplicate uploads collapse to one
	// file — but DISTINCT files were unbounded, and a credential holding nothing
	// but media:write could upload unique bytes until the filesystem was full.
	// A full disk is not a media outage here: SQLite stops being able to write
	// and the whole install answers 502, which is the failure this box has
	// already seen once. A narrow key must not be able to reach it.
	//
	// Read from MEDIA_QUOTA_GB but held in BYTES, because the enforcement is a
	// byte comparison and a GB-granular field cannot express a boundary any test
	// can stand on — a limit nobody can prove bites is the kind that turns out
	// not to.
	//
	// Non-positive means "not configured", and the enforcement then falls back to
	// DefaultMediaQuotaGB rather than to no ceiling: a Cfg that was never Load()ed
	// (a subcommand, a test) must not silently be the unlimited case.
	MediaQuotaBytes     int64
	MediaRetainDays     int
	CacheMaxSizeGB      int64
	SmokeTestTimeout    time.Duration
	BackupRetainDays    int
	TmpDir              string
	QueueSaturationWarn int
	QueueHardLimit      int // reject new jobs above this depth (backpressure)
	// Queue retention: terminal write_jobs rows are pruned so the queue table
	// (and the database file) cannot grow without bound. Completed jobs are kept
	// for JobRetentionHours; dead-letter/quarantined jobs for DeadJobRetentionDays.
	JobRetentionHours    int
	DeadJobRetentionDays int
	PluginTimeoutMS      int // per-plugin execution budget in milliseconds
	PluginMaxConcurrent  int // max simultaneous plugin executions
	MaintenanceMode      bool
	CSPReportOnly        bool // send Content-Security-Policy-Report-Only (staging) instead of enforcing
	GovernanceActuation  bool // when true, exhausted governance budgets drive automatic mode escalation (default off)
	VacuumCooldownMin    int

	// VayuKeep — automatic encrypted replication (ADR-0145). Off by default:
	// Experimental under the constitution's feature lifecycle. The passphrase is
	// deliberately NOT a dedicated variable — it is the same VAYU_BACKUP_PASSPHRASE
	// the CLI uses, so an operator has exactly one secret to look after and a
	// generation is always restorable with `vayupress restore`.
	VayuKeepEnabled    bool
	VayuKeepTarget     string
	VayuKeepMinMin     int
	VayuKeepMaxMin     int
	VayuKeepDrillMin   int
	VayuKeepRetainGen  int
	MaxReplayCount     int
	ReplayBatchLimit   int
	WALSizeThresholdMB int
	PprofRateLimit     int
	// SearchReconcileMin is the interval, in minutes, between background search
	// drift checks. 0 disables the periodic reconciler entirely.
	SearchReconcileMin int

	// SMTP email delivery (Tier 1). When SMTPHost is empty, email is a no-op:
	// subscriber/comment flows still work, delivery is simply skipped.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string // "VayuPress <hello@example.com>"
	SMTPTLS      string // starttls (default) | ssl | none

	// SchedulerTickSec is how often the publishing scheduler scans for posts
	// whose scheduled time has arrived. 0 disables scheduled publishing.
	SchedulerTickSec int

	// AnalyticsRetainDays bounds how long privacy-first view aggregates are kept.
	AnalyticsRetainDays int

	// Social auto-posting (Mastodon-compatible). Empty = disabled.
	MastodonInstance string
	MastodonToken    string

	// AI writing assistant — local Ollama endpoint. Empty = disabled.
	AIURL   string
	AIModel string

	// Stripe webhook signing secret for paid-member upgrades. Empty = disabled.
	StripeWebhookSecret string

	// TrustedProxies is the set of CIDR ranges whose X-Forwarded-For /
	// X-Real-IP headers are honoured when deriving the real client IP. Requests
	// arriving directly from any other address have their forwarding headers
	// ignored, so a client cannot spoof its IP to evade rate limiting / lockout
	// or impersonate a TRUSTED_IPS entry. Defaults to loopback because the
	// shipped deployment runs nginx on the same host.
	TrustedProxies []*net.IPNet
}

// apiKeyUnset is the APIKey value for a subcommand that serves no HTTP.
//
// NOT the empty string. Empty compares equal to an absent header, so any
// `subtle.ConstantTimeCompare(hdr, Cfg.APIKey)` reached with an unset key would
// authenticate a request that carried no key at all. A NUL byte cannot appear in
// an HTTP header value, so this can never match anything — the same
// "zero value is not a valid answer" rule the rest of this codebase follows.
const apiKeyUnset = "\x00cli-no-api-key\x00"

// Load reads the configuration for the SERVING process. API_KEY is required:
// the server authenticates with it, and starting without one would serve an
// unauthenticated admin API.
func Load() { load(true) }

// LoadLocalCLI reads the configuration for a subcommand that only touches this
// machine — no listener, no authentication, nothing to protect with a key.
//
// It exists because requiring API_KEY there was not caution, it was breakage.
// `vayupress domains hosts` is a local SQLite read, and the privileged
// provisioning helper drives it from a systemd unit that carried no
// EnvironmentFile — so the command exited
//
//	{"level":"fatal","component":"config","msg":"required env not set","key":"API_KEY"}
//
// before reading anything, and an entire install provisioned no certificates for
// a week. A configuration check strict enough to break a command that does not
// use the value it is checking protects nobody.
func LoadLocalCLI() { load(false) }

func load(requireAPIKey bool) {
	if requireAPIKey {
		Cfg.APIKey = MustEnv("API_KEY")
	} else {
		Cfg.APIKey = apiKeyUnset
	}
	Cfg.DBPath = EnvOr("DB_PATH", "/var/lib/vayupress/vayupress.db")
	Cfg.CacheDir = EnvOr("CACHE_DIR", "/var/cache/vayupress")
	Cfg.MediaDir = EnvOr("MEDIA_DIR", "/var/lib/vayupress/media")
	Cfg.Domain = EnvOr("DOMAIN", "localhost")
	Cfg.Port = EnvOr("PORT", "8080")
	Cfg.CFZoneID = EnvOr("CF_ZONE_ID", "")
	Cfg.CFAPIToken = EnvOr("CF_API_TOKEN", "")
	Cfg.IndexNowKey = EnvOr("INDEXNOW_KEY", "")
	Cfg.APIHost = EnvOr("VAYUOS_API_HOST", "")
	Cfg.OnionMode = onionModeFromEnv(EnvOr("VAYUOS_MODE", "clearnet"))
	Cfg.TmpDir = EnvOr("TMP_DIR", "/tmp/vayupress")
	Cfg.WorkerCount = GetEnvAsInt("WORKER_COUNT", 3)
	Cfg.BackupRetainDays = GetEnvAsInt("BACKUP_RETAIN_DAYS", 30)
	Cfg.StorageQuotaGB = int64(GetEnvAsInt("STORAGE_QUOTA_GB", 200))
	Cfg.MediaQuotaBytes = int64(GetEnvAsInt("MEDIA_QUOTA_GB", DefaultMediaQuotaGB)) * 1024 * 1024 * 1024
	Cfg.MediaRetainDays = GetEnvAsInt("MEDIA_RETAIN_DAYS", 365)
	Cfg.CacheMaxSizeGB = int64(GetEnvAsInt("CACHE_MAX_SIZE_GB", 10))
	Cfg.QueueSaturationWarn = GetEnvAsInt("QUEUE_SATURATION_WARN", 100)
	Cfg.QueueHardLimit = GetEnvAsInt("QUEUE_HARD_LIMIT", 1000)
	Cfg.JobRetentionHours = GetEnvAsInt("QUEUE_JOB_RETENTION_HOURS", 24)
	Cfg.DeadJobRetentionDays = GetEnvAsInt("QUEUE_DEAD_JOB_RETENTION_DAYS", 7)
	Cfg.PluginTimeoutMS = GetEnvAsInt("PLUGIN_TIMEOUT_MS", 2000)
	Cfg.PluginMaxConcurrent = GetEnvAsInt("PLUGIN_MAX_CONCURRENT", 8)
	st := GetEnvAsInt("SMOKE_TEST_TIMEOUT", 30)
	Cfg.SmokeTestTimeout = time.Duration(st) * time.Second
	Cfg.MaintenanceMode = os.Getenv("VAYU_MAINTENANCE") == "true"
	Cfg.CSPReportOnly = os.Getenv("CSP_REPORT_ONLY") == "true"
	Cfg.GovernanceActuation = os.Getenv("GOVERNANCE_ACTUATION") == "true"
	Cfg.VacuumCooldownMin = GetEnvAsInt("VACUUM_COOLDOWN_MIN", 10)

	Cfg.VayuKeepTarget = strings.TrimSpace(EnvOr("VAYUKEEP_TARGET", ""))
	// Enabled follows the target: configuring somewhere to replicate to IS the
	// intent to replicate. A separate on/off switch only creates the state where
	// an operator set a target, believes they have backups, and does not.
	Cfg.VayuKeepEnabled = Cfg.VayuKeepTarget != "" && os.Getenv("VAYUKEEP_OFF") != "true"
	Cfg.VayuKeepMinMin = GetEnvAsInt("VAYUKEEP_MIN_MINUTES", 5)
	Cfg.VayuKeepMaxMin = GetEnvAsInt("VAYUKEEP_MAX_MINUTES", 360)
	Cfg.VayuKeepDrillMin = GetEnvAsInt("VAYUKEEP_DRILL_MINUTES", 720)
	Cfg.VayuKeepRetainGen = GetEnvAsInt("VAYUKEEP_RETAIN_GENERATIONS", 24)
	Cfg.MaxReplayCount = GetEnvAsInt("MAX_REPLAY_COUNT", 3)
	Cfg.ReplayBatchLimit = GetEnvAsInt("REPLAY_BATCH_LIMIT", 100)
	Cfg.WALSizeThresholdMB = GetEnvAsInt("WAL_SIZE_THRESHOLD_MB", 32)
	Cfg.PprofRateLimit = GetEnvAsInt("PPROF_RATE_LIMIT", 5)
	Cfg.SearchReconcileMin = GetEnvAsInt("SEARCH_RECONCILE_MIN", 60)
	Cfg.SMTPHost = EnvOr("SMTP_HOST", "")
	Cfg.SMTPPort = GetEnvAsInt("SMTP_PORT", 587)
	Cfg.SMTPUsername = EnvOr("SMTP_USERNAME", "")
	Cfg.SMTPPassword = EnvOr("SMTP_PASSWORD", "")
	Cfg.SMTPFrom = EnvOr("SMTP_FROM", "VayuPress <noreply@"+Cfg.Domain+">")
	Cfg.SMTPTLS = EnvOr("SMTP_TLS", "starttls")
	Cfg.SchedulerTickSec = GetEnvAsInt("SCHEDULER_TICK_SEC", 60)
	Cfg.AnalyticsRetainDays = GetEnvAsInt("ANALYTICS_RETAIN_DAYS", 365)
	Cfg.MastodonInstance = EnvOr("SOCIAL_MASTODON_INSTANCE", "")
	Cfg.MastodonToken = EnvOr("SOCIAL_MASTODON_TOKEN", "")
	Cfg.AIURL = EnvOr("VAYU_AI_URL", "")
	Cfg.AIModel = EnvOr("VAYU_AI_MODEL", "llama3.2")
	Cfg.StripeWebhookSecret = EnvOr("STRIPE_WEBHOOK_SECRET", "")
	Cfg.TrustedProxies = parseCIDRs(EnvOr("TRUSTED_PROXIES", "127.0.0.0/8,::1/128"))
	// "Behind Cloudflare/CDN" real-visitor-IP mode. Boot default from the env;
	// the VayuOS panel toggle overrides it live (SetTrustCloudflare) with no
	// restart. Truthy values: 1/true/yes/on.
	switch strings.ToLower(strings.TrimSpace(EnvOr("TRUST_CLOUDFLARE", ""))) {
	case "1", "true", "yes", "on", "enabled":
		SetTrustCloudflare(true)
	}
}

// onionModeFromEnv maps VAYUOS_MODE to the whole-install Tor/anonymous switch.
// "tor", "onion" and "anonymous" enable it; anything else (including the
// "clearnet" default and any unrecognised value) leaves the install in the
// normal clearnet world, so a typo can never silently drop clearnet features.
func onionModeFromEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "tor", "onion", "anonymous":
		return true
	default:
		return false
	}
}

// parseCIDRs parses a comma-separated list of CIDR ranges, skipping any that
// fail to parse (logged, not fatal — a malformed entry must not break startup).
func parseCIDRs(s string) []*net.IPNet {
	var nets []*net.IPNet
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Accept a bare IP by promoting it to a single-host CIDR.
		if !strings.Contains(part, "/") {
			if ip := net.ParseIP(part); ip != nil {
				if ip.To4() != nil {
					part += "/32"
				} else {
					part += "/128"
				}
			}
		}
		if _, n, err := net.ParseCIDR(part); err == nil {
			nets = append(nets, n)
		} else {
			log.Printf(`{"level":"warn","component":"config","msg":"ignoring invalid TRUSTED_PROXIES entry","entry":"%s"}`, part)
		}
	}
	return nets
}

func MustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf(`{"level":"fatal","component":"config","msg":"required env not set","key":"%s"}`, k)
	}
	return v
}

func EnvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func GetEnvAsInt(name string, defaultVal int) int {
	v := os.Getenv(name)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return defaultVal
	}
	return n
}
