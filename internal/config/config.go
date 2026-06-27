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

var Cfg struct {
	APIKey              string
	DBPath              string
	CacheDir            string
	MediaDir            string
	MeiliHost           string
	MeiliMasterKey      string
	Domain              string
	Port                string
	WorkerCount         int
	CFZoneID            string
	CFAPIToken          string
	IndexNowKey         string
	StorageQuotaGB      int64
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
	MaxReplayCount       int
	ReplayBatchLimit     int
	WALSizeThresholdMB   int
	PprofRateLimit       int
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

func Load() {
	Cfg.APIKey = MustEnv("API_KEY")
	Cfg.DBPath = EnvOr("DB_PATH", "/var/lib/vayupress/vayupress.db")
	Cfg.CacheDir = EnvOr("CACHE_DIR", "/var/cache/vayupress")
	Cfg.MediaDir = EnvOr("MEDIA_DIR", "/var/lib/vayupress/media")
	Cfg.MeiliHost = EnvOr("MEILI_HOST", "http://localhost:7700")
	Cfg.MeiliMasterKey = EnvOr("MEILI_MASTER_KEY", "")
	Cfg.Domain = EnvOr("DOMAIN", "localhost")
	Cfg.Port = EnvOr("PORT", "8080")
	Cfg.CFZoneID = EnvOr("CF_ZONE_ID", "")
	Cfg.CFAPIToken = EnvOr("CF_API_TOKEN", "")
	Cfg.IndexNowKey = EnvOr("INDEXNOW_KEY", "")
	Cfg.TmpDir = EnvOr("TMP_DIR", "/tmp/vayupress")
	Cfg.WorkerCount = GetEnvAsInt("WORKER_COUNT", 3)
	Cfg.BackupRetainDays = GetEnvAsInt("BACKUP_RETAIN_DAYS", 30)
	Cfg.StorageQuotaGB = int64(GetEnvAsInt("STORAGE_QUOTA_GB", 200))
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
