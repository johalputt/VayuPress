# ADR-0176 — A plugin is confined in its own child step, before exec

- **Status:** Accepted
- **Date:** 2026-11-07
- **Code:** `internal/sandbox/helper_linux.go`, `internal/sandbox/confinement_linux.go`, `internal/sandbox/subprocess.go`, `cmd/vayupress/main.go`.
- **Amends:** ADR-0057 and ADR-0059, whose seccomp filter, `NoNewPrivs` and capability drop were written and never applied to a plugin.

## 0. The question

The seccomp filter, `NoNewPrivs` and the capability drop each act on the
thread that sets them and are kept across `execve`, so each has to be set
inside the plugin's process, after the fork and before the plugin's own code
runs. Go's `os/exec` offers no hook there, and nothing applied them: every
plugin ran with the host's privilege inside its namespaces. The mount work
had the opposite fault: the parent did it, in the host's own mount tree,
making `/` private and covering `/proc` entries for every process on the
machine.

## 1. Decision

**The host starts itself as the plugin's child step.** It runs
`/proc/self/exe __vayupress-sandbox-exec …` with the plugin's namespaces and
environment; `main` hands that argument to the sandbox before anything else
is set up. The child step, on one locked thread:

1. in its own mount namespace only, makes `/` private, mounts the scratch
   tmpfs, and covers the masked `/proc` and `/sys` entries (a file with
   `/dev/null`, a directory with an empty read-only tmpfs);
2. empties the capability bounding set;
3. takes the plugin's `RunAs` identity, groups first;
4. empties its effective, permitted and inheritable capabilities;
5. sets `NoNewPrivs` and installs the filter;
6. execs the plugin.

**Any step that fails stops the plugin.** The reason is written to the
plugin's stdout as a refused response, so the first invocation fails with
it; nothing runs unconfined. When the kernel refuses namespaces and the
operator has allowed plugins without them, the child step mounts nothing
(a mount there would land in the host's tree) and still applies steps 2 to 6.

**The filter follows the manifest.** Every plugin gets what a static program
needs to start, read its stdin, write its stdout and run its threads,
including `execve` (the child step's own exec) and the call that sets
thread-local storage on x86-64 and 386. A plugin that declares paths gets
the file calls, and the write calls when it declares paths to write; one
that declares the network gets the socket calls. Anything else is `EPERM`.

**Allowing `execve` keeps the filter shut.** The filter, `NoNewPrivs` and
the empty capability sets are all kept across `exec`, so anything a plugin
runs runs under the same three; the tests read the kernel's own account of
a plugin that has exec'd itself.

## 2. What was found by running real plugins

The allowlist had been tested only by a probe inside the test binary. Run
under it, the example plugins in `docs/plugins/examples` stopped before
`main`: the Go runtime's first call on x86-64, `arch_prctl`, was refused.
A plugin that opened a file died too: the netpoller wakes itself through an
eventfd, and a refused `eventfd2` is a fatal runtime error. Both are now on
the list, and the tests build and run a real example plugin under it.

## 3. Consequences

- `Manifest.DropCaps` and `Manifest.SeccompProfile` are gone: the drop and
  the filter apply to every plugin, and nothing read either field.
- A dynamically linked plugin that declares no path cannot load its
  libraries. Plugins are built static (`CGO_ENABLED=0` for Go); the plugin
  spec says so.
- The filter decides which kinds of call a plugin makes, not which paths it
  reaches. Paths are its `RunAs` identity's, as before.
- The 386 entries (`set_thread_area`, `socketcall`) are read from the Go
  runtime and the ABI, not seen to work: no 386 kernel runs these tests.

## 4. How it is checked

`internal/sandbox/helper_linux_test.go` starts plugins as the host does,
with the test binary as the child step. A probe plugin reads
`/proc/self/status` from inside: `Seccomp: 2`, `NoNewPrivs: 1`, empty
`CapEff`, `CapPrm` and `CapBnd`, and `EPERM` for a call off the list, as
started and after it execs itself. The `wordcount` example answers a hook
under the filter. A plugin with its own mount tree leaves the host's
`/proc/self/mountinfo` unchanged.
