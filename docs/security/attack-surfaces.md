# Attack Surfaces — VayuPress

**Status:** Authoritative  
**Last reviewed:** 2026-10-16  
**Review cycle:** Every architectural change or quarterly

Each mitigation names the mechanism that enforces it. A surface the product
does not have is not listed: federation, the AI runtime, article signing,
Merkle proofs, archives and DID login were described here and removed from
the code on 2026-09-25 (ADR-0061's status says which).

---

## Surface 1 — HTTP Ingress (Nginx → Go)

| Vector | Mechanism | Mitigation |
|--------|-----------|------------|
| Path traversal | URL-encoded `../` sequences | Nginx normalises the path before forwarding; every cache, media and theme path is confined to its directory after cleaning (`render.CacheWrite`, `unsafePathComponent`, the media name pattern) |
| Client IP spoofing | Forged `X-Forwarded-For` / `X-Real-IP` | `realIPMiddleware` honours forwarding headers only from a configured trusted proxy (`auth.ClientIP`); chi's `RealIP`, which trusts them from anyone, is not used |
| Request smuggling | `Content-Length` / `Transfer-Encoding` conflict | Nginx parses each request and forwards it to the backend on its own connection |
| Slow-loris DoS | Incomplete headers held open | Nginx `client_header_timeout 10s`, `client_body_timeout 10s` (`scripts/deploy-vayupress.sh`) |
| Large body amplification | Unbounded POST body | Nginx `client_max_body_size 50M`; handlers read through `http.MaxBytesReader` / `io.LimitReader` |
| TLS downgrade | SSLv3 / TLS 1.0 | Nginx `ssl_protocols TLSv1.2 TLSv1.3` |
| Script injection | Inline script in a page | CSP admits scripts by per-request nonce and by hash only (ADR-0036); inline style attributes are allowed (`style-src-attr 'unsafe-inline'`), styles cannot run script |

---

## Surface 2 — Plugin Subprocess IPC

| Vector | Mechanism | Mitigation |
|--------|-----------|------------|
| JSON injection over stdin | Malformed plugin response escapes scanner | `bufio.Scanner` with hard byte cap; response parsed into typed struct |
| Stdout flooding | Plugin emits unbounded bytes | `io.LimitReader` on the plugin's stdout before the scanner (`internal/sandbox/subprocess.go`) |
| Symlink escalation | Plugin creates symlink pointing outside scratch | `CLONE_NEWNS` + `MS_NODEV/NOSUID` on tmpfs scratch |
| Syscall escalation | Plugin calls `open("/etc/shadow")` | Seccomp-BPF allowlist; `EPERM` returned, not crash |
| Capability abuse | Plugin tries `setuid(0)` | `capset(2)` zeros all caps before plugin exec |
| FD inheritance | Parent FD leaks to child | `CloseExtraFDs` sets `CLOEXEC` on all non-stdio FDs |
| Crash loop amplification | Buggy plugin respawns endlessly | `MaxRestarts` budget; quarantine after exhaustion (ADR-0057) |
| Environment leakage | Host env vars expose secrets | `PrepareExecEnv` builds minimal allowlist; parent env not inherited |

---

## Surface 3 — SQLite Database

| Vector | Mechanism | Mitigation |
|--------|-----------|------------|
| SQL injection | Dynamic query construction | Values bound with `?` placeholders |
| Concurrent writes | Interleaved writers corrupt a transaction | One writer connection (`DB.SetMaxOpenConns(1)`, `internal/db`); readers use a separate read-only pool |
| Migration tampering | An applied migration's file changed | SHA-256 of each applied migration recorded in `schema_migrations` and checked at start-up (`checksumSQL`) |
| Corrupt restore or update | A damaged database file put in place | `PRAGMA integrity_check` on a staged update before it is applied (`internal/update/snapshot.go`) and in each VayuKeep restore drill |

---

## Unmitigated / Accepted Risks

| Risk | Current Status | Planned Mitigation |
|------|---------------|-------------------|
| Seccomp on an unlisted architecture | The filter covers amd64, arm64, riscv64, ppc64le, s390x, arm and 386 (`internal/sandbox/seccomp_arch_linux.go`); elsewhere none is installed, never one built for another architecture | Add an architecture when a build targets it |
| Mutual TLS for plugin IPC | Not implemented | When the plugin SDK is external |
| `PRAGMA integrity_check` at start-up | Not run when the database is opened | Run on each staged update and in each restore drill |
