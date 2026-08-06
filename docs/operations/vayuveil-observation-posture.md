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
is a warning wasted — and the **Unit hardening** card below is how you act on it
without opening a terminal.

## Unit hardening — asking root, then checking it took

The service runs unprivileged and cannot edit its own systemd unit; that is one
of the controls, not a limitation to work around. So the panel **requests** and
**reports what happened**.

Press **Request hardening and restart** on the VayuVeil page. The panel writes an
empty flag file, a root-side `.path` unit runs a fixed root-owned worker, and the
worker writes a drop-in at
`/etc/systemd/system/vayupress.service.d/20-vayuveil-hardening.conf`. Nothing
you can do from the console influences which directives root writes — the list
lives in the worker.

**Only five directives are ever written**, and the rule that picks them is the
rule the whole subsystem runs on: a directive earns its place only if this
process can read its effect back afterwards.

| Directive | What it denies | Read back from |
| --- | --- | --- |
| `NoNewPrivileges=yes` | privilege gain through a setuid binary or file capability | `PR_GET_NO_NEW_PRIVS` |
| `PrivateDevices=yes` | the framebuffer, input event devices and DRM card nodes | `/proc/self/mountinfo` |
| `PrivateTmp=yes` | `/tmp/.X11-unix` and anything else left in a shared `/tmp` | `/proc/self/mountinfo` |
| `ProtectHome=yes` | home directories, including thumbnail caches | `/proc/self/mountinfo` |
| `MemorySwapMax=0` | the kernel paging this service's memory to disk at all | the service's own cgroup |

Everything else systemd offers is refused, and the page prints each refusal with
its reason. `ProtectSystem=strict` is the one worth understanding: it is in the
shipped unit and correct there, because that unit also carries a
`ReadWritePaths=` list matched to the data directory. A drop-in written from a
button cannot know that list, and a wrong one leaves the service unable to write
its own database at the next restart.

### Three things to know before pressing it

- **The service restarts.** Systemd applies unit directives at exec, so a
  drop-in written under a running process does nothing until it starts again.
  The page will disconnect for a moment; reload it.
- **It reverts itself if the service does not come back.** The worker samples the
  unit repeatedly rather than once — a crash loop with a five-second retry looks
  *active* if you check at the wrong moment — and on failure it removes the
  drop-in and restarts without it. Nothing hardened, nothing broken, and the card
  says so.
- **`MemorySwapMax=0` has a cost.** It is what keeps decrypted mail and the
  keystore key off the disk, and it means that under real memory pressure the
  service is killed rather than swapped.

### The verdict is the kernel, never the worker's report

This is the distinction the card exists to hold, and it decides what each chip
means. The comparison that separates *awaiting restart* from *did not take* is
against the **drop-in file's own timestamp** — the file systemd read at exec —
and never against the worker's report, which is written after the restart it is
reporting on.

| Chip | What it means |
| --- | --- |
| **in force** | every directive verified present, read back from the kernel |
| **requested** | a request is waiting for the worker; nothing has changed |
| **awaiting restart** | the drop-in was written *after* this process started, so this process does not have it — a configuration, not yet a control |
| **did not take** | the drop-in was already in place when this process started and the directive is *still* absent — written somewhere this service does not read from, overridden, or refused by the host |
| **partly skipped** | everything still missing is something the worker declined to write, with its reason shown; a restart will not change it |
| **reverted** | the service did not come back, so the drop-in was removed |
| **failed** | the worker reported a problem; its own sentence is on the card |

**did not take** is the only one of these that is a `Fail` in the posture report,
and it is a `Fail` because it is the state in which you have been told a control
exists that does not.

### If the card shows a command instead of a button

The root-side worker is not on this host **yet**. If subdomain provisioning is
set up here, the daily sweep installs it from the signed release bundle with no
terminal use — the card will then show the button instead. The copyable command
is there for a host without provisioning, or for an operator who would rather
not wait.

**Allow up to two daily sweeps.** The sweep upgrades its own driver, and the
upgraded driver only takes effect on the following run, so the first sweep
delivers the worker and the second installs its watcher. That is worth stating
rather than rounding down to "within a day": a page that promises one day and
takes two teaches an operator that the page guesses.

That the sweep does this at all is a fix in its own right. The self-upgrade path
delivers root-side **scripts** to every install and does not write systemd
**units**, so the hardening worker arrived on machines with nothing watching for
its request. The sweep now writes and enables the watcher too.

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

## Reading it without opening the panel

Two read-only MCP tools expose the same report any connected client can call —
`vayuveil_posture` and `vayuveil_unit_controls`. They exist because this
subsystem's entire product is a report, and until they did that report lived in
exactly one place: a browser page. Both are scoped to `settings/read`, the same
scope VayuShield's read tools use.

`vayuveil_posture` returns every row with its verdict and reasoning, the summary
counts, whether reporting is switched on, and a `scope` sentence shipped
**alongside** the numbers rather than left in documentation — a caller reading
only the counts still cannot conclude that this install defends a screen. Each
row carries a `permanent` flag, so a limit that is there by construction is
distinguishable from a failure somebody can act on. Alert on the latter.

`vayuveil_unit_controls` returns the five baseline directives with `in_force`
and `known` as **separate** fields, the drop-in verdict, what the last run wrote
and skipped, and the list of directives deliberately refused with their reasons.

**The capture suite is metered — at most one run every 15 seconds, for every
caller including the page.** It opens device nodes, so running it once per
request would let a loop turn a read into device I/O. Both the page and the
payload state how old the result is; the payload field is `capture_suite_age`.

That caching is confined to the *experiment*. The control rows — process
hardening, the service sandbox, host posture — are read from the kernel on every
call and are never cached, because a remembered control is a configuration
wearing evidence's clothes. Nothing goes green on the strength of the capture
suite, which is why metering it is honest and metering a control would not be.

**Neither tool can write, and nothing on this surface ever will.** The write that
would matter here makes root edit a systemd unit and restart a live service, and
a model's context is full of text other people wrote — a blog comment, a fetched
page, a mail body. Requesting hardening stays a human pressing a button in
VayuOS, having read what it costs.

## Reading it at boot

The posture is written to the log at startup too, because a subsystem visible
only in a panel is invisible on the day the panel will not load. The line names
the registered channel count, how many are enforced host-wide (zero, and it says
so), the process controls, and the sandbox.

It logs at **warning** level for the two states worth acting on: a dumpable
process, or a unit that never applied the sandbox. An unanswerable platform logs
at info — warning on "unknown" would train you to ignore the level on exactly the
machines where a real warning still means something.
