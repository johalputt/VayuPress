# Incident Response — VayuPress

**Status:** Authoritative  
**Last reviewed:** 2026-10-16

---

## Severity Levels

| Severity | Definition | Response time |
|----------|-----------|---------------|
| P0 — Critical | Data exfiltration, RCE, complete service loss | Immediate (< 1 hour) |
| P1 — High | Auth bypass, privilege escalation, significant data corruption | < 4 hours |
| P2 — Medium | Denial of service, partial data unavailability, security regression | < 24 hours |
| P3 — Low | Information disclosure, non-critical degradation | < 72 hours |

---

## Where an incident shows first

1. **The console.** The system bar shows the install's mode on every page — a
   move to **read-only**, **quarantined** or **degraded** is the install
   reporting a fault it escalated (`internal/fault`). The bell carries backup
   failures, VayuShield events and new sign-ins.
2. **The log.** `journalctl -u vayupress` is JSON, one object per line; look
   for `"level":"error"` and the component: `auth-lockout`, `sandbox`,
   `vayushield`, `db`, `vayukeep`, `update`.
3. **Metrics** (`/metrics`, locally or with the API key):
   `vayupress_auth_lockouts_total`, `vayupress_plugin_panics_total`,
   `vayupress_migration_drift_detected_total`, `vayupress_csp_violations_total`,
   `vayupress_dead_letter_total`.
4. **VayuShield's posture report** (VayuOS → Shield), which judges every
   control by what is enforcing now.
5. **An external report**, through [SECURITY.md](../../SECURITY.md).

---

## Playbooks

### PB-1: An account is being attacked, or was taken over

**Symptom:** `vayupress_auth_lockouts_total` rising; `auth-lockout` lines in the
log; a sign-in the owner does not recognise.

1. Lockout is automatic: 5 failed sign-ins from one address in 15 minutes
   locks that address out for an hour (`internal/auth`).
2. If an account was used: change its password, and the owner's second factor
   (VayuOS → Profile); sign-in sessions end with the password change.
3. Revoke what the account could have minted: scoped API keys (VayuOS → API
   keys) and connector grants (VayuOS → Connector).
4. Read the audit log (VayuOS → Governance) for what was done in its name; it
   is append-only, so it was not edited.
5. A mailbox account: VayuOS → Mail › Accounts; a reset through the recovery
   pipeline revokes every app password and session and holds queued outbound
   mail ([MAIL-RECOVERY.md](../MAIL-RECOVERY.md)).

### PB-2: A key or secret leaked

- **The install API key** (`API_KEY`): rotate it — RB-09 in
  [OPERATIONS.md](../OPERATIONS.md).
- **A scoped key or a connector grant:** revoke it in VayuOS; nothing else is
  affected.
- **A payment, AI or DNS provider's key:** rotate it at the provider first,
  then store the new one in VayuOS → API Keys (encrypted at rest).
- **The backup passphrase:** set a new one; generations already written stay
  under the old one, so treat them as exposed and keep them only until new
  generations under the new passphrase have passed a drill.

### PB-3: Database corruption

**Symptom:** `SQLITE_CORRUPT`, the install in read-only mode, or the service not
starting on a database error.

Follow [wal-corruption-recovery.md](../operations/wal-corruption-recovery.md).

### PB-4: Abusive traffic

**Symptom:** p95 rising (`vayupress_http_request_duration_seconds`), the
console slowing, the shield's counters climbing.

1. VayuOS → Shield shows each layer's counters, who is jailed and why, and
   releases a jail.
2. VayuShield sheds and challenges on its own; check it is **protecting**, not
   observing.
3. A single source nginx should refuse outright:
   ```bash
   echo "deny <ip>;" | sudo tee -a /etc/nginx/conf.d/blocked.conf
   sudo nginx -t && sudo systemctl reload nginx
   ```
4. A flood that fills the uplink is beyond any host
   ([THREAT-MODEL.md](../THREAT-MODEL.md), RR-04): it needs capacity in front.

### PB-5: A plugin misbehaving

Plugins run only when `VAYU_PLUGINS_ENABLED=true`. Five plugin faults within two
minutes move the install to **quarantined**, where no plugin is run.

1. `journalctl -u vayupress | grep '"component":"sandbox"'`
2. To stop all plugins now: remove `VAYU_PLUGINS_ENABLED` from
   `/etc/vayupress/env` and restart.
3. A syscall refused (`EPERM`): `sudo ausearch -m SECCOMP --start recent`
   names it. If the plugin legitimately needs it, the allowlist is
   `allowedSyscalls` in `internal/sandbox/confinement_linux.go`; if not, the
   plugin is attempting something it was not granted — investigate its source.

### PB-6: A suspected tampered binary or database

**Symptom:** `migration drift` at start; a binary that does not match a
release.

Follow DR-03 in [disaster-recovery.md](../operations/disaster-recovery.md). The
in-app updater installs only releases whose Ed25519 signature verifies against
your pinned key; a binary that did not arrive that way, or from a cosign-verified
release, is suspect.

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
