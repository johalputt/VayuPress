# What every change passes before it ships

There is no separate QA step. A commit reaches `main` only by passing sixteen
required CI jobs — one of which, the cross-compile matrix, is itself seven
parallel builds — and `ci-pass` re-checks every one by name. A job that ran
without being listed there would be a job that cannot fail the build.

## The Go code itself

| Gate | What it catches |
|---|---|
| `go vet`, `gofmt -l` | Suspicious constructs; unformatted source |
| `staticcheck` | Dead stores, impossible conditions, misused stdlib |
| `golangci-lint run` (v2, zero issues) | The aggregate linter set, errcheck included |
| `gosec -severity high -confidence high` | Injection, weak crypto, hardcoded credentials |
| `govulncheck` | Known CVEs on code paths actually reachable from this binary |
| `go build -race` + `go test -race` | Data races, in unit and integration runs |
| `go test -shuffle=on` | Tests that only pass in their usual order — shared state between tests |
| `go test ./internal/archcheck/` | Layering violations: which package may import which |
| `go test ./internal/compat/` | Golden-file breaks in the plugin/theme contract (VCB) |
| deadcode gate | Newly unreachable code, against a committed baseline |
| **cross-compile matrix** | **Architecture-specific compile errors, on seven Linux ABIs** |
| **`go mod tidy` drift, `go mod verify`** | **Undeclared or unused dependencies; module contents that no longer match `go.sum`** |
| **reproducible build** | **Two cold builds of identical source that are not byte-identical** |

## Everything around the code

| Gate | What it catches |
|---|---|
| trufflehog (filesystem mode) | Committed secrets |
| shellcheck, markdownlint | Broken shell, malformed docs |
| **heredoc audit** | **An unescaped backtick inside an unquoted heredoc — live command substitution in a file that is supposed to be config text** |
| SPDX + license check | A source file with no licence header |
| ADR completeness, required docs | A design decision that shipped unrecorded |
| Governance / ethics / security-policy / community checks | Drift between the Constitution and the repository |
| source-sync | A missing entry point or required package, or a module that does not build |
| **release-metadata consistency** | **`.release-version` and the top `CHANGELOG.md` section disagreeing** |

## In the browser

Playwright drives the real console and the real public site, on a fresh install
in CI's order (`e2e.yml`):

| Gate | What it catches |
|---|---|
| The design lint | Any console value off the token scale, on every console page |
| Screenshot baselines | A change to any of sixteen surfaces, console and public site, against approved pictures |
| Contrast | A text colour below WCAG AA against its surface, in both schemes |
| Halcyon's checks | Sideways scrolling, overlays, reflow at 320 px, keyboard focus and control names, on every page type |
| The specs | The behaviour itself: mail, chat, the editor, settings, members, the Mailboxes page, the page cache |
| Lighthouse (`lighthouse.yml`) | Home, a post and four console pages against their budgets |

A pull request's screenshots are compared with the baselines on `main`; a
baseline changes only when a commit names the surface it moved, and CI records
the new picture after the run is green.

## Every gate is mutation-tested

A gate that has not been seen to fail on the thing it forbids has proved
nothing. Each new check is run once against a deliberately broken copy of the
code it guards and must fail **at its own assertion**. Three false greens this
has caught:

- **A test that judges output by calling the function under test** compares
  the code with itself: loosening the validator loosened the assertion in the
  same edit. The unacceptable is stated independently.
- **A seed that trips several rules kills a mutant of none of them**: the
  hostile input carrying one forbidden character carried another too, so no
  single rule was ever deciding. One seed per rule.
- **Two guards for one outcome** are each unkillable by an outcome-only
  assertion: either alone failed the run, so "it failed" never said which
  fired. The specific reason is asserted.

## Why some gates exist

The release-metadata check exists because of how releases work here: pushing `.release-version`
*is* the release. Nothing downstream re-derives the version. Without that check a
version can be bumped and announced while the notes describe a different release
— or a changelog can claim fixes shipped when no build was ever cut.

The cross-compile matrix earned its place the same way. Every other Go gate runs
on the runner's own `linux/amd64`, so nothing asked whether the source compiled
anywhere else. It did not: `internal/sandbox` named `syscall.SYS_EPOLL_WAIT` in
architecture-neutral code, and the arm64 and riscv64 ABIs have never defined it.
ARM VPSes are squarely this product's audience.

It then failed on its own first run, twice, on code written to fix that — a
32-bit `int` cannot hold `SECCOMP_RET_KILL_PROCESS`, and `SYS_SOCKET` does not
exist on 386, which multiplexes through `socketcall(2)`. Both were in test files,
which is why they had survived a local per-architecture `go build`: `go build`
does not compile tests and `go vet` does. That is the argument for the gate in
one sentence — the person adding it had just spent a day on this exact class of
bug and still shipped two more.

## What these gates do not prove

They run on `linux/amd64`; the cross-compile
matrix proves the other six architectures *compile*, not that they were tested,
and releases remain a single native build.

The reproducibility gate proves the build is deterministic: two cold builds,
each from an empty module cache and its own temporary directory, are
byte-identical. `go.mod` pins the Go toolchain and the release builds with
`GOTOOLCHAIN=auto`, so a tag decides its own compiler rather than inheriting
whatever the runner happened to have — which is what makes the property a
statement about the tag instead of about one afternoon.

The remaining variable is the C side: the release links SQLite statically, so
the host's gcc and libc still participate. Reproducing a published binary
exactly therefore means matching the release runner's C toolchain as well. The
Go version is recorded in the artifact itself (`go version <file>`); the C
toolchain is not, and pinning it is open work.

An earlier version of this section claimed builds were *not* reproducible. That
was wrong, and it was published in two sets of release notes before being
checked properly. The measurement behind it compared two binaries built while
the working tree was being edited, which is not a reproducibility test at all.
The controlled test — cold, independent caches, unmodified tree — passes, and is
now a gate so the answer stops depending on who measured it.

The shell lint is worth a note of its own, because for a long time it could not
fail. It ran `find scripts/ -name "*.sh" -exec shellcheck {} \;`, and `find`
returns 0 whatever its `-exec` reports — so the step printed its success line
unconditionally. It runs through `xargs -0` now. It had been concealing a real
error: a config-writing heredoc with an unquoted delimiter, where backticks are
live command substitution rather than text, so every generated nginx file
carried a comment with words silently deleted. The heredoc audit above gates
that whole class rather than the single instance.

Two style tools were evaluated and rejected rather than added: `gofumpt` (96
files, formatting preference rather than correctness) and `go vet -vettool=shadow`
(127 findings, overwhelmingly benign shadowing). A gate nobody can keep green
gets disabled, and a disabled gate protects nothing.
