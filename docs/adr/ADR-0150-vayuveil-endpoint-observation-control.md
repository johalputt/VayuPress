# ADR-0150 — VayuVeil: endpoint observation control

- **Status:** Accepted — **P0 shipped**, P1–P6 not started
- **Date:** 2026-07-29 (P0 shipped 2026-08-04)
- **Relates to:** ADR-0141 (VayuOS Spaces), ADR-0143 (Tor Space anonymity model),
  ADR-0123 (privileged agent / privilege separation)

## The claim, worded to be defensible

> **No software running on this machine can observe the screen, the keyboard, the
> clipboard, or window content without an explicit, visible, revocable grant from
> the person using it — and every attempt, granted or refused, is recorded.**

Read what that does *not* say. It does not say observation is impossible. It says
no **software on this machine** can do it **silently**. That boundary is the whole
design, and §8 states what sits outside it.

This follows `internal/anonaudit`, which refuses to claim "100% anonymous"
because a false guarantee puts a real person at real risk. The equivalent lie
here would be "screenshot-proof". A user who believes that will type a seed
phrase in front of a compromised kernel.

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

### 3.2 The enforcement stack

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

**L7 — Memory hygiene.**
- Hibernate and swap encrypted, or hibernate disabled. A suspend image contains
  the framebuffer.
- Core dumps disabled for capture-capable processes.
- GPU buffers zeroed on release — uninitialised VRAM has historically leaked one
  process's framebuffer to the next.

**L8 — Audit.**
- Append-only, kernel-backed record of **every attempt, allowed or refused**, with
  process, binary hash, channel, asset, and the user's answer.
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

## 5. Build order

Each phase is independently shippable and independently useful. No phase claims
more than it has verified.

**P0 — Threat model and the registry (weeks). SHIPPED.**
The `ObservationChannel` type, the exhaustiveness test, the generated system
inventory, and the ADR. **Ship the registry before the enforcement**, so every
later phase has a declared home and nothing lands undeclared.

> **What P0 actually is, in the tree:** `internal/vayuveil` holds the registry —
> twenty channels, each answering disposition, grant model, indicator, audit
> level and enforcing phase, with the zero value of every one of those invalid
> — plus the host probes. `internal/veilaudit` computes the posture report.
> `/os/vayuveil` shows both and carries the activate/deactivate control.
>
> **P0 enforces nothing, and every surface says so.** The switch governs
> *reporting*: activating it makes the install inventory itself, and turning it
> off exposes nothing that was not already exposed. `veilaudit` cannot emit a
> passing row while no phase is enforcing — `Pass` means *verified enforcing*,
> so at P0 the report is green nowhere, by construction and by test. An
> interface absent from the host is reported as absent rather than as defended:
> a headless server has no framebuffer, and calling that protection would be the
> §8 lie in a different costume.

**P1 — Screen, honestly (months).**
Hardened compositor with no capture protocol, portal-only path, compositor-drawn
consent, per-surface `no-capture`, indicator, audit. Claim covers channels 1–2
only, and the report says so.

**P2 — Input and clipboard.**
L5 in full. Bluntly worded grants for keyboard capture.

**P3 — Accessibility.**
L4. Needs care and real screen-reader users testing it — a privacy feature that
breaks assistive technology is a failure with a moral dimension, not a trade-off.

**P4 — Sandbox and MAC.**
L2 and L3, per-sandbox XWayland, the policy set.

**P5 — Egress correlation and memory hygiene.**
L6 and L7.

**P6 — Attestation.**
Measured boot, signed compositor, remote attestation of the enforcing config.
This is what raises the cost of A5 (root) — it cannot close it.

## 6. How each phase is proven

Per this project's release discipline, the adversarial pass **gates** each phase
and does not trail it. For this subsystem specifically:

- **A red-team capture suite**: a corpus of real capture techniques —
  screencopy, dmabuf export, `/dev/fb0`, XWayland root-window read, AT-SPI text
  dump, evdev keylogger, clipboard poll, thumbnailer abuse, `/proc/pid/mem`,
  ptrace. Every one runs on every build and **must fail to capture**.
- The suite is the specification. A technique that is not in it is not defended,
  and the report must not imply otherwise.
- **Mutation-tested**: every defence is re-broken and the corresponding attack
  must succeed. A defence whose attack still fails when the defence is removed
  was never the thing stopping it.
- **Artifact-level, not transport-level.** The lesson of 29 Jul: a subsystem that
  returns the right status while producing the wrong bytes passes every
  transport check. So the suite asserts on the **captured pixels and text** — did
  the attacker end up holding content? — never on whether an API returned an
  error.

## 7. Naming

`VayuVeil` — the endpoint observation-control subsystem, alongside VayuShield
(network), VayuPGP (mail), VayuTor (transport). "Veil" carries the right meaning:
something you choose to lift.

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
   claim it cannot fail.

Everything in §8 appears in the posture report as a permanent Fail. That is what
makes the rest of the report worth believing.
