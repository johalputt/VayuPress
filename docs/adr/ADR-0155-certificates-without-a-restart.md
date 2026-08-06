# ADR-0155 — Adding a domain must not restart the server

- **Status:** Accepted; P1–P5 shipped in v3.17.11
- **Date:** 2026-08-06
- **Supersedes nothing.** Corrects behaviour introduced piecemeal across
  `scripts/setup-vayudomain.sh`, `scripts/setup-talk-subdomain.sh` and the
  certbot renewal hook written by `scripts/deploy-vayupress.sh`.

## 0. The report, and what is actually true

> Adding a domain or subdomain for a certificate restarts VayuPress. The site
> 502s while it comes back. `mcp.<domain>` and `api.<domain>` do not do this —
> they are smooth. Make every domain behave like those.

The comparison is exact, and it is the whole diagnosis. Three helpers provision a
subdomain and **only reload nginx**:

| Helper | What it does at the end |
| --- | --- |
| `setup-mcp-subdomain.sh:229` | `systemctl reload nginx` |
| `setup-api-subdomain.sh:213` | `systemctl reload nginx` |
| `setup-openpgpkey-subdomain.sh:387` | `systemctl reload nginx` |

Two do something else as well:

| Helper | Extra step |
| --- | --- |
| `setup-vayudomain.sh:682` | `systemctl try-restart vayupress`, then poll `/health` for 60s |
| `setup-talk-subdomain.sh:265` | `systemctl try-restart vayupress` |

Plus the certbot deploy hook written by `deploy-vayupress.sh:1016`, which
restarts the app on **every mail certificate renewal** — quarterly, unattended,
with nobody watching.

**Two of those three restarts are already unnecessary today.** Not "could be
removed with work" — unnecessary, because the mechanism that makes them
unnecessary is already in the binary and already tested. That is the finding this
ADR exists to act on.

## 1. Why the outage is as long as it is

nginx terminates TLS and proxies to the app on `:8080`. While the app is down,
every request is a 502 — there is no queue and no retry. **The outage is exactly
the app's startup time**, whatever that is on a given install.

That number must be read, not guessed. `cmd/vayupress/main.go:1286` already logs
it on every boot as `startup complete in <N>ms`.

**It has now been read, and it refutes the sentence above.** On the reference
install (`smtp.johal.in`, a live blog with mail):

```
startup complete in 1200ms
startup complete in 1112ms
```

**Roughly 1.1–1.2 seconds.** So a restart of this service costs about a second of
502 — not minutes. Two consequences follow, and the second is the important one.

*P5 is a nicety on this install, not the main event.* §4 said the measurement
would decide that, and it has: socket activation turns a ~1.2s error window into
a ~1.2s wait. Worth having, correct, and not the thing anybody noticed.

*The ten-minute outage that prompted this ADR is therefore NOT startup time, and
it is not yet explained.* Removing the restarts removes about a second per
provisioning run, which is real and is not what was reported. The leading
hypothesis — stated as a hypothesis, because nothing here has verified it — is
that `systemctl try-restart vayupress` was being issued **from inside a systemd
unit** (`vayupress-provision.service`, `TimeoutStartSec=900`). systemd queues
jobs, and a restart requested from within a running unit can wait on that unit's
own transaction rather than executing immediately; the service would then be
stopped and not started again until the provisioning run finished. That fits a
multi-minute outage with a 1.2-second startup, and it is closed by P1 whichever
explanation is correct, because the restart is simply gone.

Confirming it needs one look at a real run:
`journalctl -u vayupress -u vayupress-provision --since <the time a domain was added>`,
reading the gap between the stop and the subsequent start. Until somebody looks,
this ADR claims only what it measured: startup is ~1.2s, and three restarts that
did not need to exist have been removed.

## 2. The three restarts, each with a verdict

### 2.1 Custom domains — `setup-vayudomain.sh` — REMOVE

The helper obtains a certificate, writes a vhost, reloads nginx, and records the
outcome with `vayupress domains set-tls`. That last call runs in a **separate CLI
process** and writes to SQLite. Then it restarts the server so it notices.

It does not need to. `internal/domain/domain.go:270`:

> `cacheTTL` bounds how long a resolved snapshot is trusted before a refresh.
> Host resolution runs on every public request, so the hot path must not touch
> SQLite; writes invalidate the cache immediately, and **the TTL only bounds
> staleness from an out-of-band DB edit.**

A CLI process writing the registry *is* an out-of-band DB edit — precisely the
case the thirty-second TTL was designed for. The running server picks up a new
domain within thirty seconds, with no restart, by a mechanism that already ships
and already has tests. The restart buys nothing the TTL does not already give,
and costs a full startup of 502s.

### 2.2 Mail certificate renewal — the certbot deploy hook — REMOVE

The hook copies the renewed keypair into `/var/lib/vayupress/mailcert/` and
restarts the app. `internal/vayuos/mail/tls.go:216` already handles this:

> `reloadingCert` serves an operator-supplied keypair from disk and transparently
> reloads it when the underlying files change … so every mail TLS listener picks
> up a renewed certificate on the next handshake — **no process restart, no
> expired-cert outage.** Reload attempts are throttled … and a failed reload
> (e.g. certbot mid-write) keeps serving the last-good certificate.

`tls.go:164` lists `/var/lib/vayupress/mailcert/fullchain.pem` among the
candidate paths and `tls.go:118` wires exactly those through `newReloadingCert`.
So the hot-reload covers the path the hook writes to. The restart is redundant,
and it is the worst of the three because it fires unattended on renewal.

### 2.3 VayuTalk subdomain — `setup-talk-subdomain.sh` — NEEDS A CODE CHANGE

This one is honest today. The helper sets `VAYUOS_TALK_HOST` in
`/etc/vayupress/env`, and `cmd/vayupress/vayuos_mail.go:1264` reads it with
`config.EnvOr`. A process's environment cannot change without an exec, so the
restart is doing real work.

The fix is to stop reading a host from the environment. Store it in settings —
the same store the VayuVeil switch and every other runtime toggle uses — and keep
the env var as a fallback so an existing install is not broken by the upgrade.
Then the restart deletes like the other two.

## 3. What this is not

**It is not a fix for restarts that genuinely have to happen.** An in-app update
replaces the binary; that requires an exec and always will. §5 addresses the
outage those cause, and it addresses it by making a restart not *be* an
outage — not by pretending it does not happen.

## 4. Build order

Each step states what it changes and how it is proven. A step that cannot be
verified after the fact does not go in this list.

**P1 — Delete the two redundant restarts.** `setup-vayudomain.sh`'s
`restart_app_verified` and the mailcert deploy hook's `try-restart`. Proven by a
test that reads every provisioning helper and fails if any of them restarts the
app for a certificate — the same shape as the existing guard that asserts no
helper reads the request flag. The 60-second health poll goes with it: it exists
only to watch a restart that no longer happens.

**P2 — Move the VayuTalk host out of the environment.** Read it from settings,
fall back to `VAYUOS_TALK_HOST` when unset so existing installs keep working, and
have the helper write the setting instead of the env file. Then delete the third
restart. Proven by a test that the advertised host changes without a re-exec.

**P3 — Report what actually happened.** With the restarts gone, the provisioning
result should say *no restart was needed* rather than reporting a wait it no
longer performs. The panel currently narrates a step that will not exist; a
result line describing work nobody did is the same defect as a posture row
claiming a control nobody verified.

**P4 — Measure the startup, then decide whether it is a defect. DONE, and the
answer moved the goalposts.** ~1.2s on the reference install (§1), so P5 is a
nicety there and the reported outage is something else.

The measurement itself is now a product feature rather than a one-off. The
operator who reported this could not read their own journal —
`journalctl -u vayupress` answered *"No journal files were opened due to
insufficient permissions"* — and a number that needs root and a shell is a number
that never informs a decision. So the install records its own startup durations
into a short ring and the **Update & Backup page states what a restart costs**,
as a range across recorded boots, paired with whether the socket queues: the same
1.2 seconds means "every visitor gets a 502" or "every visitor waits" depending
on that, and those are different decisions. This is §13's rule about diagnostics
belonging on the page, applied to the one number this whole ADR turns on.

**P5 — Make the restarts that remain stop being outages: systemd socket
activation.** With a `vayupress.socket` unit, systemd owns the listening socket
and holds it across a restart of `vayupress.service`. Connections arriving mid-
restart **queue in the kernel backlog instead of being refused**, so nginx gets a
slow response rather than a connection error, and the visitor gets latency rather
than a 502. This is the step that answers "super smooth" for the in-app update,
which is the one restart nothing can remove.

Two things make it real rather than aspirational: the app must accept an
inherited listener (`sd_listen_fds`, or the `systemd.socket` conventions Go
libraries already implement), and installing the unit needs root — which now goes
through the provision-request path built for ADR-0150 §5 S6, so the panel
requests it and reports what happened rather than printing a command.

## 5. How each step is proven

- **P1/P2:** a source-level guard over `scripts/*.sh` asserting no certificate
  path restarts the app, plus the existing helper-delivery guards so the change
  actually reaches installs rather than only fresh ones. That lesson is three
  days old and cost a release.
- **P3:** render the provisioning card and read it, never assert on handler
  source.
- **P4:** a number from a live journal, quoted as a range across boots rather
  than a single flattering figure.
- **P5:** verified from outside — a request issued *during* a restart must return
  a response, not a connection error. A test that only proves the unit file
  parses proves nothing about the outage.

## 6. What the audit found

The pre-release adversarial pass asked one question about P2 — *widening
`AllKeys` made six keys writable; did that open a path for someone to write
them?* The import path was safe (it applies six hardcoded presentation keys),
and the **export** path was not, in a way that predated this ADR entirely.

`handleThemeExport` emitted every key in `AllKeys` into a bundle the panel invites
an operator to download and "apply everywhere", while promising "no secrets …
safe to share". It carried `tor.space_api_key`, the shield's allow and deny CIDR
lists, the cluster peers, the subscribed intelligence feeds, payment
configuration and contact addresses — and, once P2 landed, the VayuKeep backup
destination.

The cause is the one worth remembering: **the set of keys that are not part of a
theme existed only in a test.** The conformance test kept the list, the exporter
iterated `AllKeys`, and nothing joined them. A duplicate of production truth
inside a test is a duplicate that drifts, and this one drifted into a credential
leak. It is `settings.NotPortable` now, and the test reads it.

One mutation survived its first run and changed the shape of the fix. Restoring
the leaking loop left every new test green, because those tests re-derived the
exporter's filter rather than calling it. They exercise the real handler over
HTTP now, seeded with a distinct canary per key, which is the only version that
can fail.

## 7. Risks worth stating before building

- **Thirty seconds is not zero.** After P1 a newly provisioned domain starts
  serving within the registry's TTL rather than instantly. That is strictly
  better than a restart, and the panel should say so rather than implying the
  domain is live the moment certbot returns.
- **Removing the health poll removes a check.** It currently catches an app that
  did not come back. It only exists because the restart exists; with no restart
  there is nothing to catch. The install-wide health signal belongs on the panel,
  where it already is.
- **Socket activation changes how the service starts.** It is the one step here
  that can break a boot, which is why it is last, behind a measurement, and
  behind the request-and-verify path rather than a hand-edited unit.
