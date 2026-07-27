# ADR-0145 — VayuKeep: continuous, encrypted, self-verifying replication

Status: Proposed
Date: 2026-07-27
Deciders: VayuPress core

## Context

VayuPress has two backup paths. Each is half-right, and neither is the half the
other has.

| | `scripts/deploy-vayupress.sh` | `vayupress backup` (`internal/backup`) |
|---|---|---|
| Consistent DB copy | **yes** — `sqlite3 .backup` | **no** — hot `filepath.Walk` of the live file |
| Encrypted | **no** — plain `.db` in `/var/backups` | **yes** — AES-256-GCM, Argon2id |
| Covers media / maildirs / PGP keystore | **no** — DB only | **yes** — whole data directory |
| Runs automatically | **no** — see below | **no** — manual CLI only |
| Needs an external binary | **yes** — the `sqlite3` CLI | no |

### The documented backup schedule does not exist

`docs/operations/disaster-recovery.md` publishes this table:

| Backup | Frequency | Retention | Location |
|--------|-----------|-----------|----------|
| Full DB | Nightly 02:00 UTC | 30 days | `/var/backups/vayupress/` |

No such job is installed. The deploy script's only recurring units are
`vayupress-provision.{service,path,timer}` (DNS re-check, `OnCalendar=daily`).
The database backup at `scripts/deploy-vayupress.sh:1177` sits inside
`if $UPGRADE && [[ -f "${DB_PATH}" ]]` — it runs **only during an upgrade
deploy**.

So the true recovery point objective is *"whenever you last upgraded"*, which on
a stable install is weeks. The runbook says 24 hours. An operator reading the
documentation would size their risk wrongly by an order of magnitude, and the
first time they learn otherwise is during an incident.

### The encrypted path can produce an unrestorable archive

`backup.Create` walks the data directory with `filepath.Walk` and tars whatever
bytes it finds, including `vayupress.db` and its `-wal` while writers are active.
A hot-copied SQLite pair is not guaranteed consistent. The CLI is candid about
it — `cmd/vayupress/backup_cli.go:71` prints:

> `Tip: stop the vayupress service (or pick a quiet moment) for a perfectly consistent database snapshot.`

Advice is not a mechanism. `internal/update/snapshot.go:305` already does this
correctly with `VACUUM INTO`; the backup path simply does not use it. Under the
constitution's **Data Integrity: Absolute (1.0)** weighting, a backup that can
silently be unrestorable is the most serious defect in this document.

### The stated restore guarantee is stronger than the code

`internal/backup/backup.go` opens with:

> `every chunk is independently authenticated, so tampering is detected before a single byte is written on restore`

`Extract` streams tar entries straight to disk as it reads them. A tampered or
truncated archive is detected partway through, **after** earlier files have
already been written over the destination. Three specific gaps sit behind that
sentence:

1. **No terminal authentication.** The frame format is
   `magic · salt[16] · (len · GCM ciphertext)…` with no final-frame marker and
   no frame count. Truncating at any frame boundary yields a clean `io.EOF` from
   `decReader`. It is caught today only because gzip's CRC32/ISIZE trailer is
   missing — defence by accident, from a layer that has no idea it is a security
   control.
2. **Header outside the AEAD.** `AAD` is `nil`, so `magic` and `salt` are
   unauthenticated. Only a denial (wrong key → `ErrBadPassphrase`), but free to
   close.
3. **Partial-restore residue.** There is no staging directory and no atomic
   swap, so a failed restore leaves a half-overwritten data directory.

Reordering and cross-archive splicing are *not* vulnerable — the nonce is
positional and the key is salt-unique per archive. Those are already right.

### Smaller findings

- **Passphrase echoes.** `backupPassphrase` reads via `bufio.Scanner` and says
  so ("input not hidden"). It lands in terminal scrollback and tmux logs.
- **No dedup.** Every archive re-tars the whole data directory. Against a
  200 GB default storage quota, a 30-day retention of full copies is not a plan.
- **Never verified.** `DR-06: Backup Verification Failure` exists as a manual
  runbook. Nothing checks a backup unless a human remembers to.
- **`sqlite3` CLI dependency** in the deploy path, against *"One Binary: no
  runtime dependencies beyond SQLite."*

### Why not Litestream

Measured against v0.5.15 (probe binaries, this repo's toolchain):

| build | size | delta |
|---|---|---|
| hello-world baseline | 1.37 MB | — |
| litestream core + `file` replica | 9.90 MB | **+8.53 MB** |
| litestream core + `s3` replica | 10.24 MB | **+8.87 MB** |

The weight is the core, not the S3 client. It pulls roughly fourteen distinct
third-party module roots, including `modernc.org/sqlite` — a **second** SQLite
engine alongside the existing cgo `mattn/go-sqlite3` — and
`prometheus/client_golang`, where `docs/architecture/kernel-boundary.md`
deliberately specifies in-process counters. Binary growth would be ~23% against
a >10% rejection threshold, and the dependency count is far past *"Max 5
transitive deps per dependency (FAIL >5 unless RFC)"*.

It also does not encrypt: no `encrypt` symbol appears in its `db.go` or
`replica*.go`. Replicating plaintext WAL to a third-party bucket is a
**downgrade** from the guarantee `internal/backup` already makes.

And its documented deployment is a sidecar daemon, which *"One Process: workers
are internal goroutines; no separate daemons"* forbids outright.

## Decision

Build **VayuKeep** — a VayuOS subsystem, not an eleventh product, so the
"One binary. Ten products." headline is unaffected.

It replaces both existing paths with one continuously-replicating, always-
encrypted, self-verifying system that costs the request path nothing.

Zero new Go modules. Everything is stdlib plus `golang.org/x/crypto/argon2`
(already a dependency) and the existing `internal/storage.Backend` interface.

## Design

### Layer 0 — consistency by construction

Never tar a live database again.

- **Database** — either `VACUUM INTO` a staging file (the proven path at
  `internal/update/snapshot.go:305`) for a full generation, or the WAL frame
  stream for deltas. Both produce a point-in-time-consistent image by
  definition.
- **Files** (media, maildirs, PGP public material) — content-addressed chunks,
  with change detection on `(inode, size, mtime)`. A file that has not changed
  is not read, not hashed and not shipped.
- **Secrets never travel.** The keystore DEK stays local, exactly as
  ADR-0141/0143 require. A replica cannot decrypt credentials even if the
  replica passphrase leaks — the credential ciphertext in SQLite is sealed under
  a key that was never in the DB.

### Layer 1 — the change stream

An in-process WAL tailer, running in one goroutine:

- After a commit, read the frames appended to `vayupress.db-wal` **since the
  last watermark**, using a plain `os.File` at a byte offset. No SQLite
  connection, no query, no lock. SQLite already wrote those bytes; VayuKeep only
  reads a file that exists.
- **Coalesce by page number inside the shipping window.** A hot page rewritten
  forty times in one second ships once, at its final state. Litestream ships
  every frame; this is where the efficiency comes from.
- Track the WAL header salt so a checkpoint reset is detected and resynced
  rather than silently mis-parsed.

**Checkpoint ownership without contention.** `internal/db/db.go:391` runs
`PRAGMA wal_checkpoint(PASSIVE)` periodically and `internal/update/snapshot.go:311`
runs `wal_checkpoint(TRUNCATE)`. A checkpoint that truncates frames VayuKeep has
not yet shipped is a **silent replication gap** — discovered only at restore.

Being in-process resolves this more cheaply than any external tool can. VayuKeep
never takes a lock. It publishes a "shipped through byte N" watermark, and the
existing checkpointer simply **skips this round** when the WAL end is beyond it.
Skipping is free; Litestream has to hold a long-lived read transaction from
another process to achieve the same thing, which is precisely the contention we
avoid.

**Safety valve.** If shipping stalls, the WAL must not grow without bound.
After a configurable byte or time ceiling, the checkpointer proceeds anyway,
VayuKeep marks the delta chain broken, and the next cycle promotes to a full
generation. Replication failure degrades to a slower RPO — it never blocks a
write, and never blocks publishing (constitution: circuit breakers).

### Layer 2 — cryptography

A key hierarchy, so the expensive step happens once and the frequent step is
nearly free.

```text
KEK  = Argon2id(passphrase, salt, t=3, m=64MiB, p=2)   once per generation
DEK  = random 256-bit, stored wrapped as AES-256-GCM(KEK, DEK) in the manifest
rec  = AES-256-GCM(DEK, plaintext)
       nonce = gen_id[4] ‖ seq[8]                       never repeats
       AAD   = gen_id ‖ seq ‖ prev_tag ‖ is_final       hash-chained
```

Three properties follow directly, and each closes a finding above:

- **Truncation becomes an authenticated claim.** `is_final` is inside the AAD,
  so "the stream ends here" is something the writer said and the reader
  verifies — not the absence of more bytes. Finding 1 closed by construction
  rather than by gzip's checksum.
- **Deletion and reordering break the chain.** Each record's AAD carries the
  previous record's tag. Removing record 7 makes record 8 fail to open.
- **Passphrase rotation is O(1).** Rewrap the DEK under a new KEK. No history is
  re-encrypted, so rotating a passphrase on a 200 GB archive is instant.

Argon2id at m=64 MiB runs once per generation, not per record — the current
per-archive cost, unchanged, amortised over a stream instead of a file.

The generation manifest is signed with the install's existing Ed25519 host key
(`internal/signing`, already kernel), so a restore verifies both *"this was not
tampered with"* and *"this came from my install."*

### Layer 3 — storage and dedup

- Chunk, hash the **plaintext**, encrypt under the DEK, and store under
  `HMAC(DEK, plaintext_hash)`. Dedup works because the name is stable for us;
  the backend learns nothing, because the name is a PRF output. Convergent
  encryption (naming by plaintext hash directly) is deliberately rejected — it
  leaks equality of content to whoever holds the bucket.
- Backends go through the existing `internal/storage.Backend` interface, which
  `docs/architecture/kernel-boundary.md` already lists as replaceable, and which
  already has a local implementation.
- S3-compatible targets use a small SigV4 signer written against stdlib
  `crypto/hmac` — roughly 150 lines — rather than the AWS SDK. This is the whole
  reason the design costs kilobytes where Litestream costs megabytes.
- SFTP/rsync targets reuse the operator's own infrastructure and avoid the
  question entirely.

### Layer 4 — automated, and actually intelligent

Every item here must be able to *fail*. A green indicator that cannot turn red
is the failure mode this project has hit repeatedly, most recently in v3.15.79.

1. **Continuous restore drill.** On a schedule, restore the newest generation
   into a temp directory, open it, run `PRAGMA integrity_check`, compare row
   counts in a sample of tables against live, then discard. The status page
   reports **"last verified restore: 14 min ago"**, not "backups: enabled". An
   untested backup is not a backup, and this is the single highest-value item in
   the ADR.
2. **Adaptive cadence.** Ship promptly during a write burst; back off
   exponentially when idle. A quiet blog performs no work and issues no requests
   to the backend.
3. **Snapshot when it pays.** Promote to a full generation when the cumulative
   delta passes a fraction of database size, bounding replay work at restore
   instead of letting a delta chain grow without limit.
4. **Honest lag.** Bytes behind, seconds behind, last successful ship, last
   verified restore. Modelled on `internal/anonaudit`: never claim more than has
   been verified.
5. **Pre-flight generations.** Automatic before `migrate`, before
   `update apply`, and before any restore — the three moments where the existing
   upgrade-only backup was accidentally right.
6. **Atomic restore.** Stage into a sibling directory, verify the whole chain,
   then swap. Nothing is overwritten until the archive has been proven complete.
   This is the mechanism the current doc comment already promises.

### Layer 5 — Tor and anti-leak

A remote target is clearnet egress. In `OnionMode`, VayuKeep refuses any target
that is not a local path or an onion host routed through the Tor dialer, via the
existing `safefetch.ClearnetBlocked()` kill switch (ADR-0141). Replication must
never become the one subsystem that phones home from a Tor Space.

## Performance budget

The requirement is no perceptible cost. These are the commitments, each with the
mechanism that delivers it — and each must be measured before Beta, not asserted.

| Path | Commitment | Mechanism |
|---|---|---|
| HTTP request path | **exactly zero** added work | Nothing runs in a handler. No middleware, no query, no lock. |
| SQLite writers | **no added fsync, no added lock** | The WAL is read as a file at an offset. SQLite is not consulted. |
| Checkpointer | **never blocked** | Watermark comparison, then skip. No lock is held against it. |
| Steady-state CPU | target < 0.5% of one core | AES-NI runs at GB/s; a busy install produces MB/s of coalesced delta. |
| Steady-state RSS | target < 16 MB | Bounded ring buffer, streaming records, no whole-file loads. |
| Idle install | no timers doing work | Adaptive cadence collapses to a long sleep. |
| Restore drill | yields under load | Skipped while the L0 sovereign lane reports pressure — the signal already exists as `PressureFn`. |
| Binary growth | target < 200 KB | Zero new modules; stdlib crypto and an existing interface. |

Two properties fall out of the architecture rather than from tuning. Because the
DSN sets `_synchronous=NORMAL`, SQLite does not fsync the WAL on every commit,
so the replica can legitimately hold commits a crashed local database lost.
And because coalescing collapses repeated page writes, a write-heavy install
ships *less* than its raw WAL volume, not more.

## What VayuKeep will not do

- No second daemon, no second database, no cluster.
- No plugin surface. The plugin table forbids in-process Go plugins, and the
  subprocess IPC runtime is the wrong shape for byte streams.
- No new browser surface, therefore **no CSP change whatsoever**. The only UI is
  a same-origin VayuOS status panel under the existing baseline policy.
- No replication of secrets. Ever.

## Migration and compatibility

- The `.vpbk` format gains a `VPBK2` magic with the hash chain and `is_final`.
  `VPBK1` archives stay readable by `Extract` so no existing backup is stranded.
- `vayupress backup` / `restore` keep working with identical flags; `backup` is
  re-pointed at the consistent snapshot path, which fixes the torn-archive
  defect for operators who never adopt continuous replication.
- The deploy script's upgrade-time `sqlite3 .backup` is replaced by a VayuKeep
  pre-flight generation, removing the `sqlite3` CLI dependency.
- The disaster-recovery runbook is corrected in the same change that makes it
  true, and gains DR-07 for point-in-time restore.

## Phases

1. **P1 — Correctness.** Consistent snapshot for `backup.Create`, `VPBK2` chain
   with `is_final`, header in AAD, staged atomic restore, hidden passphrase
   entry. Ships value even if nothing else lands, and closes every finding above
   that is a live defect.
2. **P2 — Continuous.** WAL tailer, coalescing, watermark and checkpoint gating,
   generation manifests, local backend.
3. **P3 — Remote.** `storage.Backend` targets, SigV4 signer, SFTP, Tor refusal.
4. **P4 — Intelligence.** Restore drill, adaptive cadence, status panel, lag
   metrics, circuit breaker.
5. **P5 — Point-in-time restore.** `vayupress restore --at <timestamp>`, DR-07.

Feature flag defaults **off** through P4; graduation to Stable requires the
benchmark table above to be measured, plus security and ethical review.

## Operational cost accounting

Mandatory per the constitution.

- **Configuration surface:** one enable flag, one target URL, one passphrase
  variable, two tuning knobs with defaults that work unattended.
- **Background goroutines:** one tailer, one shipper, one drill. All idle-capable.
- **Operational dependencies:** none added. One (`sqlite3` CLI) removed.
- **Observability:** four metrics — lag bytes, lag seconds, last ship, last
  verified restore — through the existing in-process `internal/metrics`.
- **Documentation:** DR runbook correction, DR-07, one operator guide.
- **Ethical:** replication moves personal data (member emails, comment IP
  hashes, mailbox contents) off-box. Encryption is mandatory and non-optional
  for exactly this reason, and the operator guide must state what leaves the
  server and under whose key.

This exceeds the 10% operational-complexity threshold and therefore requires RFC
approval alongside this ADR.

## Open questions

1. Content-defined chunking (rolling hash) would dedup maildirs far better than
   fixed 1 MiB blocks. Worth the complexity in P3, or defer?
2. Should the restore drill run against the *remote* replica rather than a local
   generation? It is the more honest test and the more expensive one.
3. Does the passphrase belong in the platform keystore (`internal/crypto`)
   rather than an environment variable, given ADR-0141's DEK-per-install model?

## Consequences

Positive: recovery point drops from "last upgrade" to seconds. Every backup is
encrypted, authenticated end-to-end, and continuously proven restorable. The
`sqlite3` CLI dependency goes away. Binary growth is kilobytes rather than the
~8.5 MB and fourteen module roots vendoring Litestream would cost.

Negative: VayuPress takes ownership of data-integrity-critical code that a
mature external project already solves. That risk is accepted only because P1's
findings show the current path is *already* unsafe, the constitution forbids the
sidecar deployment Litestream documents, and Litestream would not encrypt what
it replicates. The mitigation is the P4 restore drill: the system continuously
proves its own correctness rather than asking to be trusted.
