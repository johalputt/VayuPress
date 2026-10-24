# VayuPress Operations Runbook

**Last reviewed**: 2026-10-16, against VayuPress 3.18.7  
**Audience**: Production operators, on-call maintainers

Everything here can also be seen from VayuOS: the system bar shows the mode and
the world on every page, and System holds the state of the install, backups,
storage and updates. This runbook is for the shell.

---

## Quick Reference

```bash
# Service status, and its log
systemctl status vayupress
journalctl -u vayupress -n 100 --no-pager

# Health (local; most detail routes answer only locally or with the API key)
curl -s http://localhost:8080/health
curl -s http://localhost:8080/health/dependencies

# Restart, stop
sudo systemctl restart vayupress
sudo systemctl stop vayupress
```

---

## What runs where

| Process | Listens on | User | Managed by |
|---------|-----------|------|-----------|
| vayupress | 127.0.0.1:8080 (HTTP, behind nginx); 25, 587, 143, 993, 110, 995 (mail) | `www-data` | systemd (`vayupress.service`) |
| nginx | 80, 443 | `www-data` | systemd |
| tor (only with VayuTor on) | local control port | `www-data`, a child of vayupress | vayupress itself |
| fail2ban | — | root | systemd |
| certbot | — | root | its systemd timer |

| Path | What |
|------|------|
| `/var/lib/vayupress` | Data: `vayupress.db` (+ `-wal`, `-shm`), `media/`, `vayudata/mail`, `vayudata/pgp` |
| `/var/cache/vayupress` | The page cache — safe to clear; it is rebuilt |
| `/etc/vayupress/env` | Configuration and secrets (`API_KEY`, `DOMAIN`, `VAYUKEEP_TARGET` …) |
| `/opt/vayupress` | The source and build; the binary is linked at `/usr/local/bin/vayupress` |

The firewall (`ufw`) allows 22, 80, 443 and the mail ports.

---

## Health Endpoints

| Endpoint | What it reports |
|----------|-----------------|
| `GET /health`, `GET /health/live` | Liveness and the running version |
| `GET /health/ready` | Readiness: the database and workers are up |
| `GET /health/db` | The database |
| `GET /health/workers`, `GET /health/queue` | The write workers and the queue's depth |
| `GET /health/search` | VayuFind, the built-in search |
| `GET /health/storage` | Disk use of the data directory |
| `GET /health/migrations` | Applied migrations and their checksums |
| `GET /health/dependencies` | Everything above, together |

---

## Runbooks

### RB-01: The service does not start

1. Read the log: `journalctl -u vayupress -n 50`.
2. **`migration drift`** means an applied migration's recorded checksum no
   longer matches the binary's. Do not restart it away: follow DR-03 in
   [operations/disaster-recovery.md](operations/disaster-recovery.md).
3. **`database is locked`** at start: another process holds it. Find it with
   `pgrep -a vayupress`; a leftover CLI run (`vayupress backup`, `domains …`)
   finishes on its own.
4. **`required env not set`** names the variable missing from
   `/etc/vayupress/env`; `systemctl cat vayupress` shows the unit reads it.
5. Ownership: `ls -la /var/lib/vayupress` — the service runs as `www-data`
   and must own its data.

### RB-02: Memory or disk growing

1. Memory: `systemctl status vayupress` (the `Memory:` line), and
   `curl -s http://localhost:8080/metrics | grep vayupress_memory_alloc_bytes`.
2. The queue: `curl -s http://localhost:8080/health/queue`. A backlog that
   does not drain points at the database (RB-05).
3. The WAL: `ls -lh /var/lib/vayupress/vayupress.db-wal`. The server
   checkpoints on its own, more often under write load
   (`vayupress_wal_adaptive_checkpoints_total`); a WAL that keeps growing means
   a long read is holding it — usually a backup or an export in progress.
4. A memory figure that keeps climbing across hours is a defect: restart, and
   report it with the `/metrics` output from before the restart.

### RB-03: Database corruption

Follow [operations/wal-corruption-recovery.md](operations/wal-corruption-recovery.md).

### RB-04: A TLS certificate expired

1. `sudo certbot certificates` — which one, and when.
2. `sudo certbot renew` (add `--force-renewal` only if a renewal is due and
   was refused), then `sudo nginx -t && sudo systemctl reload nginx`.
3. If the HTTP-01 challenge fails: port 80 must be open (`sudo ufw status`)
   and the name must point at this server, with any CDN proxy off for it
   during the challenge.

VayuMail reloads its certificate from disk on its own; nothing needs a
restart.

### RB-05: Errors (5xx) rising

1. `journalctl -u vayupress | grep '"level":"error"' | tail -20`
2. `curl -s http://localhost:8080/health/dependencies` for the failing part.
3. Database errors: RB-02 step 3, then RB-03.
4. Search errors: rebuild the index — publishing is never blocked by it:
   ```bash
   curl -X POST http://localhost:8080/os/api/search/reindex -H "X-API-Key: $API_KEY"
   ```
5. Pages slow or failing only when not yet cached: the console's Storage page
   shows the page cache and the warmer's progress.

### RB-06: Disk space running low

1. `df -h /var/lib/vayupress /var/cache/vayupress`
2. `sudo du -sh /var/lib/vayupress/* /var/cache/vayupress | sort -rh`
3. Rendered pages are disposable. VayuOS → System › Storage shows what they
   and stale temporary files hold; **Refresh every page** redraws them and
   removes the pages of posts that no longer exist. By hand, the page
   directories (`/var/cache/vayupress/posts`, `home`, `tags`) can be deleted;
   each page is drawn again when next asked for. Leave the rest of the cache
   directory: it holds the search index's snapshot and the sitemaps.
4. Media nothing uses is listed in the media library as unused; delete from
   there, so posts that do use a file keep it.
5. Logs: `sudo logrotate -f /etc/logrotate.d/vayupress`.
6. VayuKeep's target must be **outside** this disk, or a full disk takes the
   backups with it.

### RB-07: Restore from a backup

Follow [operations/backup-restore.md](operations/backup-restore.md): verify the
archive, stop the service, `vayupress restore`, hand the directory back to
`www-data`, start.

### RB-08: fail2ban blocking a legitimate visitor

The installer configures two jails on nginx's error log, `nginx-http-auth` and
`nginx-limit-req` (`/etc/fail2ban/jail.d/vayupress.conf`).

1. `sudo fail2ban-client status nginx-limit-req`
2. `sudo fail2ban-client set nginx-limit-req unbanip <IP>`
3. To never ban an address, add it to `ignoreip` in `/etc/fail2ban/jail.local`
   and `sudo fail2ban-client reload`.

VayuShield's own jails are separate; VayuOS → Shield shows and releases them.

### RB-09: Rotating the install's API key

The install key (`API_KEY` in `/etc/vayupress/env`) can do everything. Prefer
**scoped keys** for scripts and agents (VayuOS → API keys), which can
be rotated, paused or revoked without a restart. To rotate the install key:

```bash
NEW_KEY=$(openssl rand -hex 32)
sudo sed -i "s/^API_KEY=.*/API_KEY=${NEW_KEY}/" /etc/vayupress/env
sudo systemctl restart vayupress
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8080/metrics -H "X-API-Key: ${NEW_KEY}"
```

A key is sent as `X-API-Key: <key>` or `Authorization: Bearer <key>`.

### RB-10: Analytics or the shield's record has gaps

Page views, events, reading engagement and the shield's events are written
in batches by bounded queues (`docs/ANALYTICS.md`, "When recording fails").

1. **System › Monitoring › Telemetry**: each queue's waiting count against its
   cap, and what it dropped (full) and lost (refused) since boot, with the
   database's last refusal.
2. Losses with a refusal named: the database is refusing writes. Check
   **Write connection** on the same page and RB-06; a held lock or a full disk is
   the usual cause. A refused batch is tried three times before it is lost.
3. Drops without losses: more traffic than the queue's cap between flushes.
   Enforcement and pages are unaffected; only the record thins.
4. The system mode reads read-only: the queues are holding, not losing. They
   write when the mode returns.

### RB-11: The shield refuses a real reader

1. Ask for the time and the page. The site's Security page, under **Newest
   refusals**, lists each with the request id its log line carries, and
   **Why it was refused** and **Which gate acted** name the rule or signal.
2. A rule (`line N` or `site line N`): change it, or put `test` in front to
   watch it without acting. A site's Security page replays the site's
   recorded views against the rules as typed, before saving.
3. The jail or reputation: release the address on VayuOS → Shield.
4. A country rule: the country comes from the proxy's headers when trusted,
   or the embedded table; check what the request carried.
5. To never challenge or refuse an address, add it to the allow list. An
   allow is never jailed.

### RB-12: An account cannot sign in: "Too many failed sign-in attempts"

Five failures within 15 minutes on one account, from any address and any
door, pause its sign-ins for 15 minutes, doubling with each pause to 24
hours. Its owner was mailed, with where the attempts came from.

1. **VayuOS → Shield**, "Limits set elsewhere", lists the paused accounts
   and until when.
2. If the attempts were the owner's (an old password in a phone's mail app
   is the usual cause), fix that first, then **Lift** the account. Lifting
   is recorded in the audit log (`account.unlock`).
3. If they were not, leave the pause to run, and suggest a new password.
   The owner's sessions already signed in are unaffected.

---

## Metrics

`GET /metrics` serves the Prometheus exposition format to a request carrying
the API key, or from the machine itself; anyone else gets a 404.
[deploy/monitoring/](../deploy/monitoring/) holds ready alerts and a Grafana
dashboard built on these names.

| Metric | Watch for | Runbook |
|--------|-----------|---------|
| `vayupress_http_request_duration_seconds` | p95 rising | RB-05 |
| `vayupress_memory_alloc_bytes` | climbing across hours | RB-02 |
| `vayupress_storage_used_bytes` | approaching the disk | RB-06 |
| `vayupress_migration_drift_detected_total` | any | RB-01 |
| `vayupress_queue_failed_total`, `vayupress_dead_letter_total` | rising | RB-05 |
| `vayupress_wal_checkpoint_duration_ms_total` | steps up | RB-02 |
| `vayupress_cache_hit_ratio` | falling | RB-05 step 5 |
| `vayupress_auth_lockouts_total` | rising | a sign-in attack; see VayuShield |
| `vayupress_workers_alive` | below the configured count | RB-01 |

---

## What runs on its own

| When | What | Where |
|------|------|-------|
| Adaptive: every 5 min while written to, up to 6 h when idle | A backup generation | VayuKeep (when a target is set) |
| Every 12 h | A restore drill of the newest generation | VayuKeep |
| Every 5 min | Listing cards built, then stale pages rebuilt, paced by load | The cache warmer |
| Daily | DNS and certificate re-check for every domain | `vayupress-provision.timer` |
| Twice a day | Certificate renewal | certbot's timer |
| Every start | Migrations applied and their checksums verified | `internal/db` |

---

## Capacity

A single **8 GB RAM / 4 vCPU / 50 GB NVMe** VPS runs everything; johal.in runs
234,727 posts, fourteen sites and the mail server on one Contabo VPS 10. The
binary is held under 45 MB gzipped (`make check-size`). Background work — backups, the
warmer, card building — asks the host pacer (`internal/pace`) before each step,
so it slows down while visitors need the machine.

---

## Incident Classification

| Severity | Definition | Response Time | Escalation |
|----------|-----------|--------------|-----------|
| P1 Critical | Site down, data loss, security breach | 15 min | BDFL + Security Lead |
| P2 High | Degraded performance, elevated errors | 1 hour | On-call maintainer |
| P3 Medium | Single feature broken, non-critical | 4 hours | Community Lead |
| P4 Low | Cosmetic, advisory | Next sprint | Regular backlog |

Security incidents: email security@vayupress.com immediately, and follow
[security/incident-response.md](security/incident-response.md).

---

## Emergency Contacts

| Role | Email |
|------|-------|
| BDFL / Owner | admin@vayupress.com |
| Security Lead | security@vayupress.com |
| On-call (ops) | ops@vayupress.com |
