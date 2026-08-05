# ADR-0150 — VayuVeil: observation control for a server install

- **Status:** Accepted — scope corrected 2026-08-05 (see §0)
- **Date:** 2026-07-29 (registry and posture report shipped 2026-08-04; scope
  corrected and server track completed 2026-08-05)
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

That was the more interesting failure, because it is the same one §8 exists to
prevent, pointed at the roadmap instead of at the panel. A phase table promising
six phases that will never ship here is a claim about the future that nobody
checked, and it was published on a public page about a *privacy* subsystem.

So the work splits in two, and only one half belongs to this repository:

| Track | What it covers | Home |
| --- | --- | --- |
| **Server track (S)** | What this process can verifiably do to protect *itself*, and what it can honestly *report* about the host it runs on | **This ADR. Complete.** |
| **Endpoint track (E)** | The compositor, grants, sandboxing, MAC, accessibility mediation, input/clipboard policy, egress correlation | A desktop operating system. **Not this binary, and not on its roadmap.** |

The endpoint track is not cancelled and it is not wrong — §3.2 remains the right
design for the machine a person actually types on. It is simply not something a
web server can implement, and saying so is more useful than a table of
aspirations. It is preserved below as design of record, explicitly marked out of
scope, so that if a VayuOS desktop is ever built it starts from a finished
threat model rather than a blank page.

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

## 1. Correcting the premise this started from

The motivating worry was that Windows silently screenshots the screen and sells
it. That is not what happens. Windows Recall snapshots on Copilot+ hardware —
opt-in, local, encrypted, and redesigned after precisely this criticism.
Telemetry collects diagnostics, not pixels.

**The real flaw is worse, because it is structural and it is everywhere.**

On X11, any process running as you can read the entire screen — every window,
every password field — with no permission, no indicator, and no log. That is not
a bug; X11 predates the threat. On Windows, `BitBlt` and the Desktop Duplication
API need no privilege either. macOS alone prompts.

So the adversary is not the OS vendor. It is **the software the user already
installed**: a browser extension, a free PDF tool, a cracked game, a compromised
update. Every one of them can do it today, and nothing tells you when.

## 2. Threat model

### Actors, in ascending capability

| # | Actor | Capability | In scope |
|---|---|---|---|
| A1 | Unprivileged app, user's UID | Any documented API, any device node the user can open | **Yes — fully** |
| A2 | Malicious app with a plausible reason to capture (conferencing, remote support) | Will *ask*, then abuse the grant | **Yes — fully** |
| A3 | Compromised legitimate app (supply-chain) | Inherits that app's existing grants | **Yes — fully** |
| A4 | Local attacker with a shell as the user | Everything A1 has, plus persistence | **Yes — fully** |
| A5 | Attacker with root | Reads `/dev/mem`, loads modules, patches the compositor | **Partially — cost raised, not closed** |
| A6 | Kernel/driver-level attacker | Reads GPU memory directly | **No** |
| A7 | Firmware/SMM/ME | Below the OS entirely | **No** |
| A8 | Physical/optical | Camera, HDMI splitter, hostile monitor, TEMPEST | **No** |

**A1–A4 is where essentially all real-world screen exfiltration lives.** Closing
it completely is worth doing even though A5–A8 remain.

### Assets

Not just "the screen". Enumerated, because an unenumerated asset is an
unprotected one:

1. Framebuffer / composited output (screenshots, recording)
2. Individual window content (per-surface capture)
3. **Window content as text** — accessibility APIs, the channel everyone forgets
4. Keyboard input (keyloggers — strictly worse than screenshots)
5. Pointer position and clicks
6. Clipboard and primary selection
7. Notification bodies
8. Window titles and application list (metadata leaks intent)
9. Thumbnails and previews generated from files
10. Suspend/hibernate images and core dumps containing any of the above

## 3. The architecture: one chokepoint, exhaustively enumerated

Wayland is what makes this tractable. A Wayland client **cannot** read another
window or the screen — capture happens only if the compositor hands it over. The
compositor becomes a real chokepoint, and a chokepoint is something you can put
policy on. X11 has no equivalent and never will.

### 3.1 The Observation Contract registry — the "no loophole" mechanism

This is the centrepiece, and it is deliberately the same construction as
`internal/vayushield/rule.go`.

Every interface through which observation is *possible* is a registered
`ObservationChannel` with four obligations expressed as **types whose zero values
are invalid**:

```go
type Channel struct {
    ID        ChannelID     // wlr-screencopy, at-spi, /dev/input, …
    Asset     Asset         // which of the ten assets it exposes
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

### 3.2 The enforcement stack — ENDPOINT TRACK, out of scope for this binary

**Everything in this subsection except L7 and L8 requires a desktop operating
system.** It is design of record for a machine a person types on, retained so the
endpoint track would not start from nothing. A Go server process cannot ship a
compositor, cannot mediate a grant for a screen that does not exist, and cannot
install a MAC policy. Read L0–L6 as a specification for software that does not
exist yet, not as work this repository is going to do.

The two layers that *do* have a server-side meaning are marked where they appear:
**L7 memory hygiene** has a process-scoped subset this binary implements and
verifies, and **L8 audit** has a subset covering what the posture report and the
capture suite found. Both are delivered in the server track (§5).

**L0 — Compositor (the new TCB).**
- No `wlr-screencopy`, no `zwlr_export_dmabuf`, no compositor-specific capture
  protocol compiled in. Not "gated" — **absent**. An absent protocol has no
  bypass.
- Capture exists only via `xdg-desktop-portal` ScreenCast/Screenshot.
- The consent dialog is **drawn by the compositor**, on its own layer, above
  everything, unfocusable and unreachable by clients. A client-drawn dialog can
  be spoofed; this is the difference between a permission and a suggestion.
- Per-surface `no-capture` flag, honoured in every capture path — password
  managers, VayuMail, wallet UIs render black in every screenshot, the way
  `FLAG_SECURE` works on Android.
- Compositor written in a memory-safe language and kept small. It is now the
  thing everything else trusts.

**L1 — Grants.**
- Scope: one window, or one output, never "everything" by default.
- Time-boxed with a visible countdown; expiry is automatic, not a reminder.
- Revocable mid-stream from a compositor-drawn control, not from the app.
- **Not persistent across restarts** unless the user explicitly pins it, and a
  pinned grant is listed permanently in the panel.
- A grant names the process, its binary hash, and its sandbox — not just "an app
  wants to share your screen".

**L2 — Sandbox.**
- Every app in its own namespace: no `/dev/dri` render node beyond its own, no
  `/dev/fb*`, no `/dev/input*`, no `/proc/<other>/mem`, no ptrace.
- **A rootless XWayland instance per sandbox.** This matters more than it looks:
  a *shared* XWayland reintroduces X11's flaw wholesale, because X11 clients
  inside it can see each other. One per sandbox restores the isolation.
- seccomp filters on the syscalls that reach framebuffer and input paths.

**L3 — Mandatory access control.**
- SELinux/AppArmor policy denying the device nodes and D-Bus interfaces above to
  everything not explicitly allowlisted. Defence in depth: this is what still
  holds if the sandbox is escaped but root is not obtained.

**L4 — Accessibility, the forgotten bypass.**
- AT-SPI can read the full text of every window. It is a complete capture channel
  that reads no pixels, and it is almost always left wide open.
- Treated as a first-class channel in the registry: default-deny, same
  confirmation, same indicator, same audit. A screen reader is a legitimate and
  important grant — it is not an exemption from being asked for.

**L5 — Input and clipboard.**
- `zwp_virtual_keyboard`, `zwlr_data_control` (clipboard reading),
  `input-method`: default-deny, explicit grant.
- Clipboard reads are per-paste by default, not ambient.
- A keylogger grant is presented with different, blunter language than a
  screenshot grant, because it is worse.

**L6 — Egress correlation.**
- A process holding an active capture grant is placed in a network namespace
  whose egress is **default-deny with an allowlist**. Conferencing needs one
  endpoint, not the internet.
- The panel shows capture and egress together: *"this app is recording your screen
  and talking to these hosts."* Neither fact alone is the story.

**L7 — Memory hygiene.** *(Partly server track — see S3.)*
- Hibernate and swap encrypted, or hibernate disabled. A suspend image contains
  the framebuffer. *(Endpoint.)*
- Core dumps disabled for capture-capable processes. **This is the part the
  server track delivers**, narrowed to one process: `PR_SET_DUMPABLE=0` and
  `RLIMIT_CORE=0`, each read back from the kernel before being reported, and
  scoped in the report to the VayuPress process rather than credited to the
  host-wide channel.
- GPU buffers zeroed on release — uninitialised VRAM has historically leaked one
  process's framebuffer to the next. *(Endpoint.)*

**L8 — Audit.** *(Partly server track — see S7.)*
- Append-only, kernel-backed record of **every attempt, allowed or refused**, with
  process, binary hash, channel, asset, and the user's answer. *(Endpoint: there
  are no grants to record without a compositor to mediate them.)*
- **What the server track can honestly record** is narrower and still worth
  having: what the capture suite found on this host, and what each enforcement
  verification read back.
- Refusals matter as much as grants: they are how you discover that something has
  been trying for a month.

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
- **Permanent Fail rows** that no configuration clears, one per out-of-scope
  actor: kernel-level attacker, DMA-capable hardware, firmware, physical/optical.
  A report where everything eventually goes green teaches the user to stop
  reading it.
- Live grants and their remaining time, always reachable in one action.

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

**S3 — Process self-protection, each control read back from the kernel.**
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

**S4 — The capture suite, and the honest arithmetic of it.**
Eleven techniques are named. This binary can attempt four — framebuffer read,
evdev read, DRM card-node read, and another process's memory via
`/proc/1/mem` — and reports the other seven as NOT ATTEMPTED with the reason,
which is never counted as a defence. Widening the attempted set is worth doing
only where a real capture can be tried without a client library this binary does
not link; anything less is a probe wearing a test's clothes.

**S5 — Reading host posture, which is not the same as providing it.**
Whether the host has Secure Boot enabled or a TPM present is a fact a server
process can read and an operator benefits from seeing. It must be rendered as
*what this host already has*, never as something VayuVeil provides, and it is
**not** attestation: nothing here measures the boot chain or proves the
configuration to a remote party.

**S6 — Root-requested hardening, requested by the panel and verified after.**
Unit-level directives (`NoNewPrivileges=`, `ProtectSystem=`, `ProtectHome=`,
`PrivateTmp=`) need root and therefore go through the existing
provision-request path: the panel **requests** and **reports what happened**, per
the standing rule that a fix reaches the operator through the binary rather than
through a command in a chat reply. Whatever the root side does is then verified
the same way as everything else — read back from `/proc/self/status` — because a
directive in a unit file that did not take is a configuration, not a control.

**S7 — Audit trail and operator documentation.**
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
