# ADR-0150 — VayuVeil: observation control for a server install

- **Status:** Accepted — scope corrected 2026-08-05 (see §0)
- **Date:** 2026-07-29 (registry and posture report shipped 2026-08-04; scope
  corrected 2026-08-05, server track complete at all seven steps 2026-08-05 —
  §5 marks each)
- **Relates to:** ADR-0141 (VayuOS Spaces), ADR-0143 (Tor Space anonymity model),
  ADR-0123 (privileged agent / privilege separation)

## 0. The scope correction, first, because everything below depends on it

**This ADR was originally written for a desktop operating system, and VayuPress
is not one.** It is a server binary that runs headless on somebody's VPS. The
original build order committed to a hardened Wayland compositor (P1), a
per-sandbox XWayland and a mandatory-access-control policy set (P4), egress
correlation for capture-holding processes (P5) and measured boot with remote
attestation (P6). None of those can be delivered by a Go process that serves
HTTP, and listing them as "planned" told every reader they were coming.

**That was an authoring failure, and it is recorded here in the active voice
because the passive one was the first instinct.** The author of this ADR designed
from the THREAT rather than from the PRODUCT: screen capture on X11 is a real and
interesting problem, a serious plan was written for it, and that plan was then
filed here as a roadmap without being checked against what this binary can
execute. It is the same failure §8 exists to prevent, aimed at the roadmap
instead of at the panel — and the cost was not internal, because a published
article read the phase table and announced six forthcoming phases.

The check that was missing takes one sentence and belongs at the top of any
future ADR in this repo: **what can this binary actually execute?** Everything
below §0 has now been rewritten to that question.

So the work splits in two, and only one half belongs to this repository:

| Track | What it covers | Home |
| --- | --- | --- |
| **Server track (S)** | What this process can verifiably do to protect *itself*, and what it can honestly *report* about the host it runs on | **This ADR. All seven steps shipped; §5 marks each.** |
| **Endpoint track (E)** | The compositor, grants, sandboxing, MAC, accessibility mediation, input/clipboard policy, egress correlation | A desktop operating system. **Not this binary, and not on its roadmap.** |

What this document is now: §1 states the question a VayuPress host actually
poses. §2 is a threat model whose actors are the ones on a server — a process
under the same account, an artifact this process left behind, another local
account. §5 is a build order every step of which this binary CAN run, which is
the property the old one lacked, and all seven are now built. That is stated
here as a count rather than as a word, because a summary word covering a mixed
state is how "planned" became "coming" the first time — and because "complete"
here means *the seven steps in §5 are done*, not that the observation channels in
§3.1 are enforced. They are not, they cannot be from a server, and §8 says so
permanently.

The desktop channels remain enumerated in §3.1 and remain probed, because "absent
from this host" is a fact worth proving, and because an install sharing a machine
with a desktop session is a real configuration in which they are not absent.

What is gone is the layer-by-layer enforcement design that used to fill §3.2 —
seventy lines specifying a compositor, a sandbox and a policy set. §3.2 now
records why it was cut rather than keeping it "for reference": a threat model and
an implementation design are different things, and the durable value of the first
was being used to justify keeping the second.

## The claim, worded to be defensible

The original claim was written for the endpoint track and this binary cannot
support it. What follows is the claim the **server track** actually makes, and
every word of it is enforced by a test:

> **VayuVeil enumerates every channel through which observation is possible,
> reports for each whether it is present, absent or unverified — never reading
> absence as protection — and applies the small set of controls a userspace
> server process genuinely holds over itself, reporting each one only after
> reading it back from the kernel. It claims nothing it has not verified.**

Read what that does *not* say. It does not say this install cannot be observed.
It does not say the screen is safe; on most hosts running this binary there is no
screen. It says **the report is honest**, which is a smaller claim and an
achievable one.

This follows `internal/anonaudit`, which refuses to claim "100% anonymous"
because a false guarantee puts a real person at real risk. The equivalent lie
here would be "screenshot-proof". A user who believes that will type a seed
phrase in front of a compromised kernel.

### The original claim, retained for the endpoint track

> No software running on this machine can observe the screen, the keyboard, the
> clipboard, or window content without an explicit, visible, revocable grant from
> the person using it — and every attempt, granted or refused, is recorded.

That is the right claim for a desktop and remains the endpoint track's target.
**This binary does not make it**, and no surface it renders may imply otherwise.

## 1. The question this actually answers on a VayuPress host

This started from a desktop worry — that an operating system silently
screenshots the screen — and the first draft answered that worry. It is a real
problem: on X11 any process running as you can read every window and every
password field with no permission, no indicator and no log, and the adversary is
not the OS vendor but the software the user already installed.

**None of that is the question on the machine VayuPress runs on.** There is no
screen, no browser extension and no cracked game. There is a Go process holding
decrypted mail, a keystore key, session tokens and PGP private material, on a
host that also runs whatever else the operator put there.

So the question this ADR answers is the server one:

> **What on this host can read what this process holds, what has this process
> verifiably done about it, and where is it still exposed?**

That reframing is what the rest of this document is built on, and it changes the
answers. The dangerous channel on a desktop is the compositor; here it is an
artifact this process leaves lying around — a core dump, a swap page, a backup —
because those contain the same secrets and outlive the process that made them.

The desktop channels are still enumerated in §3.1 and still probed, because
"absent from this host" is a fact worth reporting and worth being able to prove.
A VayuPress install on a machine that also runs a desktop session is a real
configuration, and on that machine those channels stop being absent.

## 2. Threat model

### Actors, on the machine this binary actually runs on

| # | Actor | Capability | In scope |
| --- | --- | --- | --- |
| A1 | Another process under the same account | Reads this process's memory via `/proc/<pid>/mem`, attaches with ptrace, opens any file this user can | **Yes — fully** |
| A2 | An artifact this process left behind | A core dump, a swap page, a backup archive, a temp file. Holds the same secrets and outlives the process | **Yes — fully** |
| A3 | A different unprivileged account on the host | Whatever file modes and namespaces permit | **Yes — fully** |
| A4 | An operator at a console or SSH session | Legitimately privileged, but what they type stays in console screen memory afterwards | **Partially — the residue is in scope, the session is not** |
| A5 | Attacker with root | Reads `/dev/mem`, loads modules, reads any file | **Partially — cost raised, not closed** |
| A6 | The hypervisor, or another tenant escaping it | Reads guest memory from outside | **No** |
| A7 | Firmware / SMM / management engine | Below the OS entirely | **No** |
| A8 | Physical access to the disk | Carries the volume away | **No — that is disk encryption's job, not this subsystem's** |

**A1 and A2 are where a server install actually loses data**, and they are the
two this binary can do something about. A2 in particular is the one operators
underestimate: an encrypted data directory does not cover the swap file, the core
dump or the backup, and each of those holds exactly what the directory was
encrypted to protect.

### Assets

Enumerated, because an unenumerated asset is an unprotected one. Ordered by what
is actually at risk here rather than by what a desktop would rank first:

1. **Decrypted mail held in memory** while it is being served or indexed
2. **The keystore key**, and anything sealed under it
3. **Session tokens** for every signed-in operator
4. **PGP private material** in memory during a sign or decrypt
5. **The SQLite database file** and its write-ahead log
6. **Backup archives** — every one of the above, at rest, portable
7. **Swap pages and core dumps** containing any of 1–4
8. **Console screen memory** — what a root login typed, still readable afterwards

The remaining channels exist only if this host ALSO runs a desktop session, and
are reported as absent when it does not:

1. Framebuffer, window pixels and window text
2. Keyboard, pointer, clipboard and the accessibility bus
3. Notification bodies, window titles and thumbnailer caches

## 3. The architecture: no chokepoint, so an enumeration instead

A desktop design would put policy on the compositor, because a Wayland client
cannot read another window unless the compositor hands it over — one place
everything must pass through, and a chokepoint is something you can put policy
on.

**A server has no such point.** This process cannot mediate what another process
on the host does, and nothing here is going to become the thing every read goes
through. That is a constraint, not a phase that has not landed yet.

What is available instead is an enumeration and an honest report: name every
channel through which observation is possible, probe each one against the
running host, and report what was found — including, and especially, that a
channel could not be checked. The value is not enforcement. It is that an
operator can answer "what can see this install?" from evidence rather than from
assumption, and that the answer cannot quietly improve itself.

Two things follow, and both are load-bearing:

- **A channel nobody thought of cannot be silently introduced** (§3.1). That is
  the only honest meaning of "no loophole" here.
- **The few things this process CAN do to itself are done and then read back**
  (§5), and they are reported at their real scope — this process, not the host.

### 3.1 The Observation Contract registry — the "no loophole" mechanism

This is the centrepiece, and it is deliberately the same construction as
`internal/vayushield/rule.go`.

Every interface through which observation is *possible* is a registered
`ObservationChannel` with four obligations expressed as **types whose zero values
are invalid**:

```go
type Channel struct {
    ID        ChannelID     // wlr-screencopy, at-spi, /dev/input, …
    Asset     Asset         // which enumerated asset it exposes
    Default   Disposition   // dispositionUnset is NOT a valid answer
    Grant     GrantModel    // grantUnset is NOT a valid answer
    Indicator IndicatorKind // indicatorUnset is NOT a valid answer
    Audit     AuditLevel    // auditUnset is NOT a valid answer
    Rationale string        // why this disposition, in prose
}
```

Three properties, each enforced by test rather than by review:

1. **Exhaustiveness.** A generated inventory of every capture-capable interface
   present in the running system is diffed against the registry. An interface in
   the system and not in the registry **fails the build.**
2. **Completeness.** `Channel.Complete()` rejects any zero-valued obligation, so
   a channel added without deciding its policy does not compile past the test.
3. **Default-deny.** `Disposition` has no "allow" that is not paired with a
   `GrantModel` requiring user confirmation.

That is the only honest meaning of "no loophole": not that we thought of
everything, but that **anything we did not think of cannot be silently
introduced.** VayuShield's `rule.go` exists because a convention re-implemented
by hand at eight sites gets forgotten at the ninth. Same reasoning, higher
stakes.

### 3.2 What enforcing a channel would require — ENDPOINT TRACK

An earlier draft of this section carried a full layer-by-layer design for the
enforcement stack: the compositor and its consent surface, the grant model, the
sandbox and its per-sandbox XWayland, the mandatory-access-control policy set,
the accessibility mediator, the input and clipboard rules, and egress
correlation. Roughly seventy lines of implementation design for software that
does not exist.

**It has been cut, and the reasoning is worth keeping.** The justification for
holding it was "so a desktop project would start from a finished threat model" —
but the threat model is §2, which stays, and §3.2 was an implementation *design*.
Those are different things, and the value of the first was being used to justify
the second. A speculative design also ages badly: the protocol names in it would
be stale before anyone built against them.

The cost was not hypothetical. Each registered channel declared the *phase* that
would enforce it, and the panel and the posture report both rendered that — so
the product told an operator, on every row, that enforcement was coming in a
numbered phase. None is coming here. That is the same defect as the phase table
which led a published article to announce six forthcoming phases, and it
survived the correction of the table because the mechanism generating the
expectation was in the code, not the document.

So a channel now declares what enforcing it would **require**, which is a true
statement about the world rather than a promise about a roadmap:

| Requirement | Covers |
| --- | --- |
| A capture-mediating compositor | Screen, window pixels, input, clipboard, window metadata |
| A mediator on the accessibility bus | Window text read without touching a pixel |
| A sandbox and mandatory-access-control policy | Device nodes, cross-process memory, per-application confinement |
| Host configuration outside this process | Swap, hibernation, core-dump policy |

The server track (§5) enforces the process-scoped subset of the last row and
says so on each of its rows. Everything above it needs a desktop operating
system, which is the correction §0 records.

### 3.3 What VayuOS itself must never do

Stated as a constitutional constraint, because the product is the thing best
placed to violate it:

- **No Recall-equivalent.** No periodic snapshotting, no local screen index, no
  "timeline", not even opt-in. The moment VayuOS keeps a searchable history of
  your screen, it *is* the threat, whatever the encryption.
- **No telemetry containing screen content, window titles, or app inventory.**
- VayuVeil's own capture path is a registered channel, subject to its own rules,
  and appears in its own audit log.

## 4. The posture report

`internal/veilaudit`, modelled on `anonaudit` and `shieldaudit`. Computed from
what is **observed**, never from what is configured.

- Per-channel rows: registered / enforcing / default / grant model / indicator /
  last attempt.
- Green means **verified enforcing**, not "switched on". If a channel cannot be
  verified, it reads *unverified* — absent evidence is never a pass.
- **Permanent Fail rows** that no configuration clears, one for each actor the
  report will never defend against: root on this machine, a kernel or
  driver-level attacker, DMA-capable hardware and firmware, a camera pointed at a
  screen, a person who grants capture to malware, and a compromised compositor.
  The last two describe an endpoint and stay in the report anyway — on a server
  they are permanently failing, which is accurate, and removing a row because it
  is unreachable here is how a report starts flattering itself.

  A report where everything eventually goes green teaches the reader to stop
  reading it, so the rows that can never clear are what make the rows that can
  worth believing.
- **No live-grant view**, because there are no grants. Mediating a grant needs
  the compositor §3.2 records as out of scope, so there is nothing to list and
  the page does not pretend to have a list.

## 5. Build order — the server track

There are two rules for what may appear in this list, and they are what killed
the old one.

**A step earns its place only if this process can VERIFY the result by reading it
back.** Applying a control and reporting success because the syscall returned 0
is the transport-level mistake §6 names. Every row below states what is read back
and from where. A control that cannot be re-read after being set does not go in
the report at all — it would be indistinguishable from a control that silently
stopped working.

**A step must change what an operator can rely on.** A probe that looks like a
test and proves nothing about whether content could be captured is theatre, and
theatre in a privacy report is worse than an empty report, because it spends
trust it did not earn.

**S1 — The Observation Contract registry and host inventory. SHIPPED.**
`internal/vayuveil` holds twenty channels, each answering disposition, grant
model, indicator, audit level and enforcing phase, with the zero value of every
one of those invalid, plus the host probes. Ship the registry before anything
else, so nothing lands undeclared.

**S2 — The posture report. SHIPPED.**
`internal/veilaudit` computes it; `/os/vayuveil` renders it with the
activate/deactivate control. The switch governs **reporting**: activating it
makes the install inventory itself, and turning it off exposes nothing that was
not already exposed.

> `Pass` means *verified enforcing* and nothing else. An interface absent from
> the host is reported as absent, never as defended — a headless server has no
> framebuffer, and calling that protection would be the §8 lie in a different
> costume. An interface the platform would not answer about is *unverified*,
> never clean.

**S3 — Process self-protection, each control read back from the kernel. SHIPPED.**
Shipped: `PR_SET_DUMPABLE=0` verified with `PR_GET_DUMPABLE`, and
`RLIMIT_CORE=0` verified with `getrlimit`. Two independent mechanisms on one
channel, reported as two rows rather than averaged into one — an operator needs
to know *which* is holding, and a single row would hide one of them failing.

Extending the same pattern: `PR_SET_NO_NEW_PRIVS` read back via
`PR_GET_NO_NEW_PRIVS`, the effective capability set and seccomp mode read from
`/proc/self/status`, the Yama `ptrace_scope` tunable read from `/proc/sys`, and
the data directory and keystore file modes read back with `stat`.

> **Scope is stated on every row and is the point.** These cover the VayuPress
> process and nothing else on the machine. Every other process is exactly as
> dumpable as it was, so the host-wide `core-dumps` and `proc-pid-mem` channels
> do **not** go green on the strength of them. The temptation to credit the host
> channel for a process-scoped control is right mechanism, wrong subject, with no
> way for a reader to tell — which is precisely the defect §8 exists to prevent.

**S4 — The capture suite, and the honest arithmetic of it. SHIPPED.**
Eleven techniques are named. This binary can attempt four — framebuffer read,
evdev read, DRM card-node read, and another process's memory via
`/proc/1/mem` — and reports the other seven as NOT ATTEMPTED with the reason,
which is never counted as a defence. Widening the attempted set is worth doing
only where a real capture can be tried without a client library this binary does
not link; anything less is a probe wearing a test's clothes.

**S5 — Reading host posture, which is not the same as providing it. SHIPPED.**
Whether the host has Secure Boot enabled or a TPM present is a fact a server
process can read and an operator benefits from seeing. It must be rendered as
*what this host already has*, never as something VayuVeil provides, and it is
**not** attestation: nothing here measures the boot chain or proves the
configuration to a remote party.

**S6 — Root-requested hardening, requested by the panel and verified after. SHIPPED.**
Unit-level directives need root, so they go through the same request path as
subdomain provisioning: the panel writes an empty flag file, a root-side `.path`
unit runs a fixed root-owned worker, and the panel then **reports what
happened**. Nothing an unprivileged process can express reaches root except
"go" — the directive list lives in the worker and in `HardenBaseline`, both
root-owned.

Two decisions did the real work here, and both narrowed the step rather than
widening it.

*What may be written.* Only directives whose effect this process can read back
afterwards: `NoNewPrivileges=`, `PrivateDevices=`, `PrivateTmp=`, `ProtectHome=`
and `MemorySwapMax=0`. Five. Everything else systemd offers is refused **on the
page, with its reason**, because a directive that cannot be re-read would be
written, reported as applied, and never checked again — a configuration reported
as a control, arriving through a button labelled "harden". `ProtectSystem=strict`
is the sharpest case: it is in the shipped unit and correct there, and it is
correct there *only* because that unit also carries a `ReadWritePaths=` list
matched to the install's data directory. A drop-in written from a panel cannot
know that list, and a wrong one means the service returns from its next restart
unable to write its own database.

*What "applied" is allowed to mean.* Systemd applies unit directives at exec, so
a drop-in written under a running process changes nothing about that process.
The panel therefore never treats the result file as the verdict — the kernel is,
read on every page load — and the result file supplies only the timestamp that
tells **awaiting restart** apart from **written and did not take**. The second is
the only `Fail` this row can produce, and it is a `Fail` precisely because it is
the state in which an operator has been told a control exists that does not.

The worker restarts the service so the directives take effect, samples the unit
repeatedly rather than once (a crash loop with a five-second retry is *active*
if you look at the wrong moment), and if it does not stay up it **removes the
drop-in and restarts without it**. A hardening button that can lock an operator
out of their own panel is worse than the exposure it closes.

**S7 — Audit trail and operator documentation. SHIPPED.**
L8 narrowed to what this track can honestly record: what the capture suite found
and what the enforcement verification said, written down rather than living only
on a page nobody reloaded. Refusals matter as much as grants — they are how you
discover something has been trying for a month.

### Not in the build order, and why

| Excluded | Reason |
| --- | --- |
| P1 screen, P2 input/clipboard, P3 accessibility | Need a compositor. A server binary has no screen to mediate and must not pretend to mediate one. |
| P4 sandbox and MAC | A kernel LSM policy set, installed and enforced host-wide. Out of reach of an unprivileged userspace process, and only partly in reach of the root helper. |
| P5 egress correlation | Requires binding a capture grant to a network namespace. There are no capture grants here to bind. |
| P6 attestation | Measured boot and remote attestation need a hardware root of trust and a boot chain this binary is not part of. Reading whether the host *already* has Secure Boot or a TPM is S5, and must never be labelled as this. |
| `mlock` on secret material | Go's garbage collector moves memory, so locking a page does not reliably pin the secret that was in it. A control that cannot be verified to still cover what it claims is theatre, and this project does not ship theatre in a privacy report. |
| Wayland and AT-SPI capture attempts | Both need a real client connection. Probing that a socket exists proves the socket exists, not that content could be read from it, so these stay NOT ATTEMPTED and stay visible as such. |

## 6. How each step is proven

Per this project's release discipline, the adversarial pass **gates** each step
and does not trail it. For this subsystem specifically:

- **A red-team capture suite** naming a corpus of real capture techniques:
  screencopy, dmabuf export, `/dev/fb0`, XWayland root-window read, AT-SPI text
  dump, evdev keylogger, clipboard poll, thumbnailer abuse, `/proc/pid/mem`,
  ptrace.
- **What "runs on every build" means, stated exactly, because the earlier wording
  overstated it.** The suite's assertions run on every build against an injected
  reader that returns bytes — which is what proves the assertions bite, and a
  suite that only ever ran where nothing is present could not prove that. The
  suite is additionally run against the real host, with the real reader, on every
  load of `/os/vayuveil`, and any technique that comes away holding content is a
  Fail on the report. What is *not* claimed: that a live capture attempt is made
  on a CI runner as a build gate. A CI runner has no framebuffer, no input
  devices and no card nodes, so such a run would pass vacuously and would be
  evidence of nothing.
- **The suite is the specification.** A technique that is not in it is not
  defended, and the report must not imply otherwise. Of the eleven named, four
  are attempted here; the remaining seven are reported NOT ATTEMPTED with the
  reason, and NOT ATTEMPTED is never counted as a defence.
- **Mutation-tested**: every defence is re-broken and the corresponding attack
  must succeed. A defence whose attack still fails when the defence is removed
  was never the thing stopping it.
- **Artifact-level, not transport-level.** The lesson of 29 Jul: a subsystem that
  returns the right status while producing the wrong bytes passes every
  transport check. So the suite asserts on the **captured pixels and text** — did
  the attacker end up holding content? — never on whether an API returned an
  error.

## 7. Naming

`VayuVeil` — the observation-control subsystem, alongside VayuShield (network),
VayuPGP (mail), VayuTor (transport). "Veil" carries the right meaning: something
you choose to lift.

The name was chosen when this was an endpoint project and it survives the scope
correction intact, because what the server track ships is still a veil in the
same sense — it says what can be seen, rather than promising nothing can be.

## 8. What we will never claim

Recorded here so no future copy quietly upgrades it:

1. **Not** "screenshot-proof" or "impossible to capture".
2. **Not** protection against a kernel-level or root attacker. Cost raised;
   attack open.
3. **Not** protection against DMA-capable hardware, firmware, ME/PSP, or SMM.
4. **Not** protection against a camera pointed at the screen, an HDMI splitter, a
   hostile monitor, or electromagnetic reconstruction.
5. **Not** protection against a user who grants a capture to malware. Policy did
   its job; the answer was still yes. Grants are therefore worded to make the
   consequence legible, and are revocable and time-boxed so a mistake is bounded.
6. **Not** a defence against a compromised compositor. It is the TCB, and the
   honest response is to keep it small, memory-safe, signed and measured — not to
   claim it cannot fail. (Endpoint track; this binary has no compositor.)
7. **Not** protection of anything other than the VayuPress process itself. Every
   control the server track applies is process-scoped, and every row that carries
   one says so. The rest of the machine is exactly as observable as it was.
8. **Not** a roadmap for the endpoint track. §3.2 describes software that does
   not exist, and no surface may render it as forthcoming — which is the mistake
   §0 records, made once, on a public page.

Everything in §8 appears in the posture report as a permanent Fail. That is what
makes the rest of the report worth believing.
