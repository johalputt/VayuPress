# Incident Response — VayuPress

**Status:** Authoritative  
**Last reviewed:** 2026-06-13

---

## Severity Levels

| Severity | Definition | Response time |
|----------|-----------|---------------|
| P0 — Critical | Data exfiltration, RCE, complete service loss | Immediate (< 1 hour) |
| P1 — High | Auth bypass, privilege escalation, significant data corruption | < 4 hours |
| P2 — Medium | Denial of service, partial data unavailability, security regression | < 24 hours |
| P3 — Low | Information disclosure, non-critical degradation | < 72 hours |

---

## Incident Detection Sources

1. **Structured logs** — `level=error` or `level=warn` entries with `component=sandbox`, `component=signing`, `component=federation`
2. **Metrics** — `vp_plugin_crashes_total`, `vp_plugin_quarantined_total`, `vp_signing_failures_total`
3. **Health endpoint** — `GET /healthz` returns `503` on critical subsystem failure
4. **External report** — via `SECURITY.md` responsible disclosure process

---

## Playbooks

### PB-1: Plugin Quarantine Storm

**Symptom:** Multiple plugins quarantined within short window; `vp_plugin_quarantined_total` spike.

```bash
# 1. Identify quarantined plugins
journalctl -u vayupress --since "10 minutes ago" | grep quarantined

# 2. Check if binary hash changed (supply chain compromise?)
sha256sum /opt/vayupress/plugins/*.bin

# 3. If hash mismatch: take plugin offline immediately
systemctl stop vayupress
# Restore plugin from verified backup
cp /backup/plugins/known-good/<name>.bin /opt/vayupress/plugins/
systemctl start vayupress

# 4. If runtime crash: collect coredump and plugin logs
coredumpctl dump > /tmp/plugin-coredump.$(date +%s)
```

### PB-2: Article Signature Verification Failure

**Symptom:** `signing: verify failed` in logs; `vp_signing_failures_total` > 0.

```bash
# 1. Identify affected article IDs
journalctl -u vayupress | grep "signing: verify" | grep -o "id=[^ ]*"

# 2. Quarantine affected articles (mark as unverified in DB)
sqlite3 /data/vayupress.db \
  "UPDATE articles SET verified=0 WHERE id IN (...)"

# 3. Determine cause: key rotation vs tamper
# Compare stored public key with current signing key public component
vayupress keydump --key /etc/vayupress/signing.key

# 4. If tamper: preserve evidence, restore from archive
vayupress archive restore --id <article-id> --target /tmp/restore/

# 5. Re-sign from restored canonical source
vayupress resign --article-id <id> --key /etc/vayupress/signing.key
```

### PB-3: WAL Corruption

**Symptom:** `SQLITE_CORRUPT` errors; `integrity_check` fails on startup.

See full runbook: [`docs/operations/wal-corruption-recovery.md`](../operations/wal-corruption-recovery.md).

### PB-4: Federation Inbox Flood

**Symptom:** Queue depth > 10,000; inbox goroutines saturated; memory pressure.

```bash
# 1. Identify flooding actor
journalctl -u vayupress | grep "inbox" | awk '{print $NF}' | sort | uniq -c | sort -rn | head

# 2. Block actor at Nginx level immediately
echo "deny <actor-ip>;" >> /etc/nginx/conf.d/blocked.conf
nginx -s reload

# 3. Drain queue safely
vayupress queue drain --actor <actor-id> --reason "flood-block"

# 4. Add to permanent blocklist
sqlite3 /data/vayupress.db \
  "INSERT INTO federation_blocks(actor_id, reason, blocked_at) VALUES('<actor>', 'flood', datetime('now'))"
```

### PB-5: Seccomp Violation (Unexpected Syscall)

**Symptom:** Plugin process receives `EPERM` on a syscall that should succeed; plugin behaviour degraded.

```bash
# 1. Enable strace on plugin process (dev/staging only)
strace -p <plugin-pid> -e trace=all 2>&1 | head -100

# 2. Identify blocked syscall number from audit log
ausearch -m SECCOMP --start recent

# 3. If legitimate: update allowedSyscalls in confinement_linux.go
# 4. If illegitimate: plugin is attempting restricted operations — investigate plugin source
```

---

## Post-Incident Requirements

For P0 and P1 incidents:
1. **Timeline document** within 24 hours of resolution.
2. **Root cause analysis** within 72 hours.
3. **ADR update** if architectural change is required.
4. **CHANGELOG entry** in next release.
5. **CVE request** if externally exploitable.

---

## Responsible Disclosure

Security vulnerabilities: see [`SECURITY.md`](../../SECURITY.md) at repository root.

Embargo period: 90 days from reporter notification before public disclosure.
