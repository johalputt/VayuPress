# VayuVeil — what can observe this install, and what it has verified

VayuVeil lives at **`/os/vayuveil`**. It answers one question honestly:

> What can observe this install, and which of the controls it claims have
> actually been read back from the kernel?

The design record is [ADR-0150](../adr/ADR-0150-vayuveil-endpoint-observation-control.md).

## Read this first: it is a report, not a shield

The switch on the page governs **reporting**. Turning VayuVeil on makes the
install inventory itself. Turning it off exposes nothing that was not already
exposed. There is no button here that defends anything.

That is not an apology. ADR-0150 was written for a desktop operating system —
the compositor, the per-window grants, the sandbox policy — and VayuPress is a
server binary that runs headless on a VPS. §0 of the ADR records the correction:
the **endpoint track** needs a desktop that does not exist, and the **server
track** is what this binary can do. This page is the server track.

## Green means one specific thing

A row is green only when **both** are true:

1. Something is doing the work, and
2. this process read back from the kernel that it is *still* doing it, at the
   moment the report ran.

Everything that clears that bar is **process-scoped** — it protects the
VayuPress process and nothing else on the machine — and every green row says so
in its own text. A reader who takes a process-scoped pass for a host-wide one has
been misled by the report, whatever it said elsewhere.

The other verdicts are worth reading precisely:

| Verdict | Means |
| --- | --- |
| **ok** | Verified enforcing, right now, at the scope the row states |
| **act** | Something that should not be reachable is reachable, or has already happened |
| **look** | A real exposure you should understand, or a control held by the host rather than by this install |
| **note** | Context, or a limitation that is not a fault |
| **unverified** | Could not be established. **Never** rounded up to "fine" |

**Unverified is not a soft pass.** A platform that would not answer is reported
as not having answered. That is the single rule this subsystem holds itself to,
because a privacy report that claims a pass it has not verified is worse than no
report — it is believed.

## What the server track actually verifies

### The process cannot be dumped

Two independent controls, on their own rows rather than averaged into one, so
you can see *which* is holding:

- `PR_SET_DUMPABLE=0`, read back with `PR_GET_DUMPABLE`. No core file, `/proc/<pid>`
  root-owned, `PTRACE_ATTACH` from a same-user process refused.
- `RLIMIT_CORE=0`, read back with `getrlimit`. Even if something later made the
  process dumpable, the kernel still writes no core file.

A core file would contain session tokens, the keystore key, decrypted mail and
any PGP material in memory at the time.

### The service sandbox

Read from `/proc/self/status` and `/proc/self/mountinfo` **at report time** —
never from the unit file, because a unit on disk records what somebody intended
and this report is built on what the process got.

- **Capture device nodes unreachable.** `PrivateDevices=yes` replaces `/dev` with
  a minimal tmpfs, so there is no `/dev/fb*`, no `/dev/input/event*`, no
  `/dev/dri/card*` and no `/dev/vcs*`. That is denial of exactly the capture
  channels this ADR enumerates.
- **Privileges actually held.** The effective capability set, as the kernel
  reports it. The shipped unit leaves `CAP_NET_BIND_SERVICE` and nothing else.
- **No privilege gain through `execve`.** `PR_GET_NO_NEW_PRIVS`. Inherited, so
  the in-app updater's re-exec and the Tor Space child keep it.

If a row here warns, it names the missing directive. A warning you cannot act on
is a warning wasted.

### Swap — the one that catches people out

**An encrypted data directory does not cover swap.** Anonymous memory — decrypted
mail, session tokens, the keystore key — is written to disk by the kernel under
memory pressure, continuously, with nothing to notice.

Two separate facts, deliberately not combined:

- **`VmSwap`** is an *outcome*: how many kB of this process are on disk already.
  Non-zero is reported as **act**, not as a warning, because it is not a risk —
  it has happened.
- **`memory.swap.max`** is a *control*: whether this service's cgroup permits
  swapping at all.

Zero bytes swapped **without** that control is luck, and luck is not a pass. The
row goes green only when nothing has swapped *and* the cgroup forbids it. A unit
setting `MemorySwapMax=0` is what makes that true.

Hibernation is reported separately, and having no hibernate target does **not**
mean this channel is clean — that conflation is what the swap row exists to
correct.

## The capture suite

Eleven techniques are named. **Four are attempted here**; the other seven need a
client library this binary does not link, and are reported NOT ATTEMPTED with
the reason. Not-attempted is never counted as a defence — a suite that quietly
skips what it cannot do reports a clean sweep it did not perform.

Attempted: the framebuffer, evdev, a DRM card node, **console screen memory**
(`/dev/vcs*`), and another process's memory via `/proc/1/mem`.

Console screen memory matters more on a server than on a desktop, and it was the
last one added for that reason. A headless host has no framebuffer and no Wayland
socket, so every other screen technique reports "nothing present" — but virtual
consoles exist, and whatever a root login typed at one is sitting in those nodes
as plain text.

The suite is judged on **bytes**, never on whether a call returned an error. A
subsystem that returns the right status while producing the wrong bytes passes
every transport check ever written.

**When does it run?** Its assertions run on every build, against an injected
reader that returns bytes — which is what proves they bite. The live suite runs
against the real host on every load of this page. It is *not* run as a CI gate: a
CI runner has no framebuffer, no input devices and no card nodes, so a live run
there would pass vacuously.

## The permanent failures

Some rows never clear, whatever you configure. They are one per out-of-scope
actor: a kernel-level attacker, DMA-capable hardware, firmware, and a camera
pointed at the screen.

They are there on purpose. **A report where everything eventually goes green
teaches you to stop reading it.** The rows that cannot be cleared are what make
the rows that can be worth believing.

## What is never claimed

Recorded in ADR-0150 §8 so no future copy quietly upgrades it. Not
"screenshot-proof". Not protection against root, DMA, firmware or a camera. Not
protection of anything other than the VayuPress process — every control here is
process-scoped. And not a roadmap: the endpoint track describes software that
does not exist, and nothing in the product may render it as forthcoming.

## Reading it at boot

The posture is written to the log at startup too, because a subsystem visible
only in a panel is invisible on the day the panel will not load. The line names
the registered channel count, how many are enforced host-wide (zero, and it says
so), the process controls, and the sandbox.

It logs at **warning** level for the two states worth acting on: a dumpable
process, or a unit that never applied the sandbox. An unanswerable platform logs
at info — warning on "unknown" would train you to ignore the level on exactly the
machines where a real warning still means something.
