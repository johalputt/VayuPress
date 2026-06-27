// Package db manages the SQLite database, migrations, and WORM audit log.
package db

import (
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Article is the canonical domain model for a published piece of content.
type Article struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Slug      string    `json:"slug"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Status is "published" (publicly visible) or "draft" (visible only inside
	// VayuOS). Empty is treated as "published" for backward compatibility.
	Status string `json:"status,omitempty"`
}

// DB is the package-level database connection. It is the single, serialized
// writer (MaxOpenConns=1) — SQLite permits only one writer at a time, and
// pinning the writer to one connection keeps the WAL write path lock-free and
// SQLITE_BUSY-free.
var DB *sql.DB

// RDB is the read-only connection pool (audit F-4). SQLite in WAL mode allows
// one writer to run concurrently with many readers, so read traffic is fanned
// across several connections here instead of queuing behind the single writer.
// It is opened query_only as defense-in-depth: a stray write routed through the
// read path fails fast rather than silently contending with the writer. RDB is
// nil for in-memory databases (tests), where Reader() falls back to DB because
// each :memory: connection is a distinct database.
var RDB *sql.DB

// Reader returns the connection used for read-only queries: the dedicated read
// pool when available, otherwise the writer connection. Callers that only
// SELECT should prefer Reader() so reads do not serialize behind writes.
func Reader() *sql.DB {
	if RDB != nil {
		return RDB
	}
	return DB
}

// WDB is the wrapped DB that records write latency metrics.
var WDB wrappedDB

type wrappedDB struct{ *sql.DB }

// Exec wraps sql.DB.Exec, recording latency and slow-query metrics for writes.
func (w wrappedDB) Exec(query string, args ...interface{}) (sql.Result, error) {
	q := strings.ToUpper(strings.TrimSpace(query))
	isWrite := strings.HasPrefix(q, "INSERT") || strings.HasPrefix(q, "UPDATE") || strings.HasPrefix(q, "DELETE")
	if !isWrite {
		return w.DB.Exec(query, args...)
	}
	start := time.Now()
	result, err := w.DB.Exec(query, args...)
	elapsed := time.Since(start)
	metrics.SQLiteWriteLatency.Record(elapsed)
	if elapsed.Milliseconds() > 100 {
		atomic.AddInt64(&metrics.MetricSlowQueries, 1)
		logging.LogJSON(logging.LogFields{Level: "warn", Component: "db", Msg: fmt.Sprintf("slow write %dms: %s", elapsed.Milliseconds(), q[:minInt(len(q), 80)])})
	}
	return result, err
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Init opens the SQLite database, applies all PRAGMAs, runs migrations, and verifies checksums.
func Init() error {
	var err error
	dsn := config.Cfg.DBPath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&_synchronous=NORMAL"
	DB, err = sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	DB.SetMaxOpenConns(1)
	DB.SetMaxIdleConns(1)
	DB.SetConnMaxLifetime(0)
	if err = DB.Ping(); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	WDB = wrappedDB{DB}
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA cache_size=-65536",
		"PRAGMA mmap_size=536870912", // 512 MB — sized for 200k-post workloads
		"PRAGMA temp_store=MEMORY",
		"PRAGMA journal_size_limit=67108864",
		"PRAGMA wal_autocheckpoint=1000",
	}
	for _, p := range pragmas {
		if _, err := DB.Exec(p); err != nil {
			return fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	if err := runMigrations(); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if err := verifyMigrationChecksums(); err != nil {
		return fmt.Errorf("migration drift: %w", err)
	}
	if err := openReadPool(); err != nil {
		return fmt.Errorf("read pool: %w", err)
	}
	logging.LogInfo("db", "ready — WAL+PRAGMAs enforced, migrations+checksums verified (ADR-0033/0034)")
	return nil
}

// isMemoryDB reports whether path refers to a SQLite in-memory database. Each
// connection to such a database is distinct, so a separate read pool cannot be
// opened against it.
func isMemoryDB(path string) bool {
	return path == ":memory:" ||
		strings.HasPrefix(path, "file::memory:") ||
		strings.Contains(path, "mode=memory")
}

// openReadPool opens the read-only connection pool (RDB) against the same
// on-disk database file as the writer. It is a no-op for in-memory databases,
// where Reader() transparently falls back to the writer connection.
func openReadPool() error {
	if isMemoryDB(config.Cfg.DBPath) {
		return nil
	}
	// query_only + the read-relevant PRAGMAs applied per connection by the
	// driver. No mmap_size here: it is a writer-side optimisation and not a
	// recognised DSN parameter.
	rdsn := config.Cfg.DBPath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&_synchronous=NORMAL&_query_only=true&_cache_size=-65536"
	rdb, err := sql.Open("sqlite3", rdsn)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	n := runtime.NumCPU()
	if n < 4 {
		n = 4
	}
	rdb.SetMaxOpenConns(n)
	rdb.SetMaxIdleConns(n)
	rdb.SetConnMaxLifetime(0)
	if err := rdb.Ping(); err != nil {
		_ = rdb.Close()
		return fmt.Errorf("ping: %w", err)
	}
	RDB = rdb
	logging.LogInfo("db", fmt.Sprintf("read pool ready — %d WAL reader connections (query_only) [F-4]", n))
	return nil
}

// StartWALCheckpointGoroutine runs adaptive WAL checkpoints in the background (ADR-0033).
func StartWALCheckpointGoroutine(doneCh <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		adaptiveBackoff := false
		for {
			select {
			case <-doneCh:
				return
			case <-ticker.C:
				walMB := walFileSizeMB()
				checkpointMode := "PASSIVE"
				if walMB > float64(config.Cfg.WALSizeThresholdMB) {
					checkpointMode = "RESTART"
					atomic.AddInt64(&metrics.MetricWALAdaptiveCheckpoints, 1)
					logging.LogJSON(logging.LogFields{Level: "warn", Component: "wal", Msg: fmt.Sprintf("WAL %.1fMB > threshold %dMB — RESTART checkpoint", walMB, config.Cfg.WALSizeThresholdMB)})
					adaptiveBackoff = true
				} else if adaptiveBackoff {
					adaptiveBackoff = false
					logging.LogInfo("wal", "adaptive backoff tick — skipping checkpoint")
					continue
				}
				start := time.Now()
				var pagesWritten int
				err := DB.QueryRow(fmt.Sprintf("PRAGMA wal_checkpoint(%s)", checkpointMode)).Scan(new(int), new(int), &pagesWritten)
				if err != nil {
					logging.LogError("wal", "checkpoint error", err.Error())
				} else {
					elapsed := time.Since(start)
					atomic.AddInt64(&metrics.MetricWALCheckpoints, 1)
					atomic.AddInt64(&metrics.MetricWALCheckpointDurationMS, elapsed.Milliseconds())
					logging.LogInfo("wal", fmt.Sprintf("checkpoint(%s) pages=%d dur=%dms total=%d",
						checkpointMode, pagesWritten, elapsed.Milliseconds(), atomic.LoadInt64(&metrics.MetricWALCheckpoints)))
				}
			}
		}
	}()
}

func walFileSizeMB() float64 {
	fi, err := os.Stat(config.Cfg.DBPath + "-wal")
	if err != nil {
		return 0
	}
	return float64(fi.Size()) / (1024 * 1024)
}

// StartStuckJobReaper resets stuck processing jobs every minute.
func StartStuckJobReaper(doneCh <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-doneCh:
				return
			case <-ticker.C:
				result, err := DB.Exec(`UPDATE write_jobs SET status='pending' WHERE status='processing' AND created_at < datetime('now','-5 minutes')`)
				if err != nil {
					logging.LogError("queue-reaper", "stuck job error", err.Error())
					continue
				}
				rows, _ := result.RowsAffected()
				if rows > 0 {
					atomic.AddInt64(&metrics.MetricQueueStuckResets, rows)
					logging.LogJSON(logging.LogFields{Level: "warn", Component: "queue-reaper", Msg: fmt.Sprintf("reset %d stuck jobs", rows)})
				}
			}
		}
	}()
}

// InitStorageCachedBytes computes initial storage usage in the background.
func InitStorageCachedBytes() {
	go func() {
		start := time.Now()
		cacheSize, _ := StorageDirSizeBytes(config.Cfg.CacheDir)
		dbSize := int64(0)
		if fi, err := os.Stat(config.Cfg.DBPath); err == nil {
			dbSize = fi.Size()
		}
		total := cacheSize + dbSize
		atomic.StoreInt64(&metrics.CachedStorageBytes, total)
		logging.LogInfo("storage", fmt.Sprintf("initial scan: %s (%dms)", FormatBytes(total), time.Since(start).Milliseconds()))
	}()
}

// StorageUsedBytes returns the cached storage usage in bytes.
func StorageUsedBytes() int64 { return atomic.LoadInt64(&metrics.CachedStorageBytes) }

// UpdateStorageDelta adjusts the cached storage counter by delta bytes.
func UpdateStorageDelta(delta int64) { atomic.AddInt64(&metrics.CachedStorageBytes, delta) }

// StorageDirSizeBytes recursively sums the size of all files under root.
func StorageDirSizeBytes(root string) (int64, error) {
	var total int64
	err := walkDir(root, func(size int64) { total += size })
	return total, err
}

func walkDir(root string, fn func(int64)) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		path := root + "/" + e.Name()
		if e.IsDir() {
			_ = walkDir(path, fn)
			continue
		}
		if fi, err := e.Info(); err == nil {
			fn(fi.Size())
		}
	}
	return nil
}

// StorageQuotaBytes returns the configured storage quota in bytes.
func StorageQuotaBytes() int64 { return config.Cfg.StorageQuotaGB * 1024 * 1024 * 1024 }

// FormatBytes formats a byte count as a human-readable string.
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// AuditLog appends a tamper-evident record to the WORM audit_log table.
func AuditLog(action, actor, target, detail string) {
	if DB == nil {
		return
	}
	if _, err := DB.Exec(
		`INSERT INTO audit_log(ts,action,actor,target,detail) VALUES(?,?,?,?,?)`,
		time.Now().UTC(), action, actor, target, detail,
	); err != nil {
		logging.LogJSON(logging.LogFields{Level: "error", Component: "audit", Msg: "failed to write audit record", Error: err.Error()})
	}
}

// AuditActor derives a stable actor identifier (client IP) from the request.
func AuditActor(r *http.Request) string {
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// ── Migration system ──────────────────────────────────────────────────────────

type migration struct {
	Version  string
	Up       string
	Down     string
	Checksum string
}

func checksumSQL(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

var migrations []migration

func init() {
	var err error
	migrations, err = loadMigrationsFS(migrationFS)
	if err != nil {
		panic("db: failed to load embedded migrations: " + err.Error())
	}
}

// loadMigrationsFS reads *.up.sql files from fs, pairs each with an optional
// *.down.sql, computes the checksum of the up SQL, and returns a sorted slice.
func loadMigrationsFS(fs embed.FS) ([]migration, error) {
	entries, err := fs.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	upFiles := make(map[string]string) // version → up SQL
	downFiles := make(map[string]string)

	for _, e := range entries {
		name := e.Name()
		b, err := fs.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		sql := strings.TrimRight(string(b), "\n")

		switch {
		case strings.HasSuffix(name, ".up.sql"):
			version := strings.TrimSuffix(name, ".up.sql")
			upFiles[version] = sql
		case strings.HasSuffix(name, ".down.sql"):
			version := strings.TrimSuffix(name, ".down.sql")
			downFiles[version] = sql
		}
	}

	versions := make([]string, 0, len(upFiles))
	for v := range upFiles {
		versions = append(versions, v)
	}
	sort.Strings(versions)

	result := make([]migration, 0, len(versions))
	for _, v := range versions {
		up := upFiles[v]
		result = append(result, migration{
			Version:  v,
			Up:       up,
			Down:     downFiles[v],
			Checksum: checksumSQL(up),
		})
	}
	return result, nil
}

// Migrations returns the full list of known migrations (used by health handlers).
func Migrations() []struct{ Version, Checksum string } {
	out := make([]struct{ Version, Checksum string }, len(migrations))
	for i, m := range migrations {
		out[i] = struct{ Version, Checksum string }{m.Version, m.Checksum}
	}
	return out
}

func runMigrations() error {
	dryRun := os.Getenv("VAYU_MIGRATE_DRY_RUN") == "true"
	if dryRun {
		logging.LogInfo("migrations", "DRY-RUN mode")
	}
	if !dryRun {
		if _, err := DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(id INTEGER PRIMARY KEY AUTOINCREMENT,version TEXT UNIQUE NOT NULL,checksum TEXT NOT NULL DEFAULT '',applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
			return fmt.Errorf("bootstrap schema_migrations: %w", err)
		}
	}
	for _, m := range migrations {
		if dryRun {
			logging.LogInfo("migrations", fmt.Sprintf("[dry-run] would apply: %s", m.Version))
			continue
		}
		var count int
		DB.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version=?`, m.Version).Scan(&count)
		if count > 0 {
			logging.LogInfo("migrations", "already applied: "+m.Version)
			continue
		}
		logging.LogInfo("migrations", "applying: "+m.Version)
		tx, err := DB.Begin()
		if err != nil {
			return fmt.Errorf("migration %s begin: %w", m.Version, err)
		}
		for _, stmt := range strings.Split(m.Up, "\n") {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				if strings.Contains(err.Error(), "duplicate column") || strings.Contains(err.Error(), "already exists") {
					logging.LogInfo("migrations", "column exists in "+m.Version+" — continuing")
					tx2, _ := DB.Begin()
					if tx2 != nil {
						tx = tx2
					}
					continue
				}
				return fmt.Errorf("migration %s exec: %w", m.Version, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version,checksum) VALUES(?,?)`, m.Version, m.Checksum); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s record: %w", m.Version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %s commit: %w", m.Version, err)
		}
		logging.LogInfo("migrations", "applied: "+m.Version)
	}
	return nil
}

func verifyMigrationChecksums() error {
	rows, err := DB.Query(`SELECT version, checksum FROM schema_migrations ORDER BY id ASC`)
	if err != nil {
		return fmt.Errorf("verifyMigrationChecksums query: %w", err)
	}
	defer rows.Close()
	migMap := make(map[string]string)
	for _, m := range migrations {
		migMap[m.Version] = m.Checksum
	}
	var drifted []string
	for rows.Next() {
		var version, storedChecksum string
		rows.Scan(&version, &storedChecksum)
		if storedChecksum == "" {
			continue
		}
		expected, ok := migMap[version]
		if !ok {
			continue
		}
		if storedChecksum != expected {
			atomic.AddInt64(&metrics.MetricMigrationDriftDetected, 1)
			logging.LogJSON(logging.LogFields{Level: "error", Component: "migrations", Msg: fmt.Sprintf("CHECKSUM DRIFT: %s stored=%s expected=%s", version, storedChecksum[:8], expected[:8])})
			drifted = append(drifted, version)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("verifyMigrationChecksums iterate: %w", err)
	}
	if len(drifted) > 0 {
		return fmt.Errorf("migration drift detected: %s — startup halted (ADR-0034)", strings.Join(drifted, ", "))
	}
	logging.LogInfo("migrations", fmt.Sprintf("checksum verification passed: %d migrations (ADR-0034)", len(migMap)))
	return nil
}

// RollbackMigration rolls back the named migration.
func RollbackMigration(version string) error {
	for i := len(migrations) - 1; i >= 0; i-- {
		if migrations[i].Version != version {
			continue
		}
		if migrations[i].Down == "" {
			return fmt.Errorf("migration %s has no Down SQL", version)
		}
		tx, err := DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i].Down); err != nil {
			tx.Rollback()
			return fmt.Errorf("rollback exec %s: %w", version, err)
		}
		if _, err := tx.Exec(`DELETE FROM schema_migrations WHERE version=?`, version); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		logging.LogInfo("migrations", "rolled back: "+version)
		return nil
	}
	return fmt.Errorf("migration %s not found", version)
}
