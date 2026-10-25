# Contributing to VayuPress

Thank you for your interest in contributing. VayuPress is governed by the [VayuPress Governance Constitution v6.0](GOVERNANCE-CONSTITUTION.md). All contributions must align with it.

## Before You Start

1. Read [GOVERNANCE.md](GOVERNANCE.md) and [ETHICS.md](ETHICS.md).
2. Read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) to understand the system.
3. Check open issues and RFCs to avoid duplicate work.

## Contribution Types

| Type                      | Process                                    |
|---------------------------|--------------------------------------------|
| Bug fix / doc improvement | Open a PR directly                         |
| New feature (small)       | Open an issue first; get maintainer buy-in |
| New dependency            | RFC required                               |
| Architecture change       | RFC required                               |
| Governance change         | RFC + vote required                        |

## RFC Process

Required for: new dependencies, core architecture changes, major features, governance modifications.

Open an issue with the **RFC proposal** template (`.github/ISSUE_TEMPLATE/rfc_proposal.yml`), written
from [docs/rfc-template.md](docs/rfc-template.md). Minimum 7-day discussion. An accepted design is
recorded as an ADR in [docs/adr/](docs/adr/INDEX.md).

## PR Requirements

| Requirement | Enforced by |
|-------------|-------------|
| Passing CI | Every workflow runs on the pull request: `go vet`, gofmt, staticcheck, golangci-lint, gosec, `go test -race` (unit and integration), the architecture tests, markdownlint, shellcheck, the SPDX and encoding checks, and the browser specs |
| Tests for new code | Review. No coverage threshold is enforced by CI |
| Docs for new features | Review, and a changelog entry under `[Unreleased]` |
| DCO sign-off (`git commit -s`) | Asked of every contributor; not checked by CI |
| Ethical review | [docs/ETHICAL-REVIEW-PROCESS.md](docs/ETHICAL-REVIEW-PROCESS.md), for the changes it lists |

Sign your commits with `git commit -s` (Developer Certificate of Origin).

## Licensing of contributions

VayuPress is licensed under **Apache-2.0** ([LICENSE](LICENSE)). By opening a pull request you
license your contribution under those terms, as Apache-2.0 §5 provides, and you sign off the
[Developer Certificate of Origin](https://developercertificate.org/) with `git commit -s` —
certifying that you wrote the contribution or have the right to submit it.

**You keep the copyright in your own work.** It is not assigned to anyone, and **there is no
Contributor Licence Agreement — there will not be one.** Only a party that holds the copyright
can sell proprietary licences to a codebase, so refusing to collect it forecloses that business
permanently, for the author as much as for anyone else.

Two things follow that are worth stating plainly rather than leaving to be discovered:

- **Every release already published stays under Apache-2.0 for good.** That grant is
  irrevocable. Anyone may fork from any published tag and continue, whatever this repository
  does afterwards.
- **The outbound licence on *future* versions could change**, because Apache-2.0 §4 permits
  distributing a derivative work as a whole under different terms. **Contributing does not give
  you a veto over that, and it is not intended to** — the maintainer can change the licence on
  future versions without any contributor's permission, and the absence of a CLA does not alter
  it. Be clear about that before you contribute.

  There is no present intention to change it; the current assessment is to stay on Apache-2.0,
  and [docs/LICENSING.md](docs/LICENSING.md) records a pledge to make that permanent. If it ever
  does change, the commitment is that it will be **forward-only, at a major version, and
  announced in advance** — contributors will be told, not asked, because pretending otherwise
  would misrepresent where the decision actually sits.

New Go files must carry `// SPDX-License-Identifier: Apache-2.0` as their first line. CI enforces
it; `python3 scripts/spdx-headers.py` adds it. The reasoning for all of the above is in
[docs/LICENSING.md](docs/LICENSING.md).

## Code Standards

- Go as pinned in `go.mod` (the `toolchain` line; 1.27 today). Run `golangci-lint run` before submitting.
- SQLite-first: all new features must work with SQLite.
- No new external dependencies without RFC approval.
- No React/Vue/Angular in public paths — HTMX + Alpine.js only.
- Test what you change; a security rule is mutation-tested (break it and see the test fail).
- Run `go test -race ./...` — zero races allowed; CI runs it.

## Onboarding Path

1. Read the governance docs.
2. Submit a small first PR (docs, tests, a typo fix).
3. After a few merged PRs, ask for review rights on the areas you know
   ([docs/MAINTAINERS.md](docs/MAINTAINERS.md)).

## Code of Conduct

Be respectful, stay on-topic, follow the governance. Violations:
- Warning → 7-day temporary ban → permanent ban.
- Ethical violations (harassment, discrimination, unethical AI usage): immediate permanent ban.

## Contact

community@vayupress.com — governance@vayupress.com
