# What every change passes before it ships

There is no separate QA step. A commit reaches `main` only by passing fifteen
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
| source-sync | `cmd/vayupress/main.go` diverging from the deploy script's copy |
| **release-metadata consistency** | **`.release-version` and the top `CHANGELOG.md` section disagreeing** |

The last one exists because of how releases work here: pushing `.release-version`
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
