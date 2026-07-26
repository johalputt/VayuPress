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

Submit RFCs to `vayupress/rfcs`. Minimum 7-day discussion. Simple majority to accept.

## PR Requirements

| Requirement             | Enforcement              |
|-------------------------|--------------------------|
| DCO signed-off          | FAIL if missing          |
| Passing CI              | FAIL if missing          |
| Tests for new code      | FAIL if missing          |
| Docs for new features   | FAIL if missing          |
| Governance validation   | FAIL if violations       |
| Changelog entry         | Warn if missing          |
| Ethical review          | FAIL for high-risk changes|

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
  distributing a derivative work as a whole under different terms. There is no present intention
  to do so — the current assessment is to stay on Apache-2.0. If it ever changes, the commitment
  is that it will be **forward-only, at a major version, announced in advance, and put to
  contributors first** even though their agreement would not be legally required.

New Go files must carry `// SPDX-License-Identifier: Apache-2.0` as their first line. CI enforces
it; `python3 scripts/spdx-headers.py` adds it. The reasoning for all of the above is in
[docs/LICENSING.md](docs/LICENSING.md).

## Code Standards

- Go 1.22+. Run `golangci-lint run` before submitting.
- SQLite-first: all new features must work with SQLite.
- No new external dependencies without RFC approval.
- No React/Vue/Angular in public paths — HTMX + Alpine.js only.
- Test coverage >70% (>80% for critical paths).
- Run `go test -race ./...` — zero races allowed.

## Onboarding Path

1. Read governance docs and acknowledge.
2. Submit a small first PR (docs, tests, typo fix).
3. A mentor will be assigned.
4. After 3 merged PRs, request write access to non-critical paths.

## Code of Conduct

Be respectful, stay on-topic, follow the governance. Violations:
- Warning → 7-day temporary ban → permanent ban.
- Ethical violations (harassment, discrimination, unethical AI usage): immediate permanent ban.

## Contact

community@vayupress.com — governance@vayupress.com
