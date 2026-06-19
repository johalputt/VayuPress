package config

import (
	"log"
	"os"
	"strconv"
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
	PluginTimeoutMS     int // per-plugin execution budget in milliseconds
	PluginMaxConcurrent int // max simultaneous plugin executions
	MaintenanceMode     bool
	CSPReportOnly       bool // send Content-Security-Policy-Report-Only (staging) instead of enforcing
	GovernanceActuation bool // when true, exhausted governance budgets drive automatic mode escalation (default off)
	VacuumCooldownMin   int
	MaxReplayCount      int
	ReplayBatchLimit    int
	WALSizeThresholdMB  int
	PprofRateLimit      int
	// SearchReconcileMin is the interval, in minutes, between background search
	// drift checks. 0 disables the periodic reconciler entirely.
	SearchReconcileMin int
}

func Load() {
	Cfg.APIKey = MustEnv("API_KEY")
	Cfg.DBPath = EnvOr("DB_PATH", "/var/lib/vayupress/data.db")
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
