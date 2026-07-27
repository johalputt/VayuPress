# VayuPress Sustainability

**Version**: 1.0.0 (Prompt 5 — Operations)  
**Area**: Financial and environmental sustainability

---

## Financial Sustainability

VayuPress is free, open-source software. The project's financial sustainability depends on voluntary support from individuals and organizations who benefit from VayuPress.

### Funding Model

VayuPress follows the **"pay it forward"** model:

- VayuPress is free to use, always
- Organizations using VayuPress commercially are encouraged (not required) to contribute financially
- Maintainers are not compensated unless explicitly funded through sponsor contributions
- All financial decisions are made by the BDFL with full transparency

### How to Support

| Method | Link | Notes |
|--------|------|-------|
| GitHub Sponsors | See `.github/FUNDING.yml` | One-time or recurring |
| OpenCollective | See `.github/FUNDING.yml` | For organizations; public ledger |
| In-kind contribution | Code, docs, testing | See `CONTRIBUTING.md` |

### Expenditure Policy

All funds received through GitHub Sponsors or OpenCollective are used exclusively for:

1. **Infrastructure** — CI/CD runners, domain registration, hosting for vayupress.com
2. **Security audits** — Third-party penetration testing (annual goal)
3. **Maintainer stipends** — If funding exceeds infrastructure costs, surplus is distributed equally among active maintainers (requires public announcement)
4. **Community events** — Meetups, conference sponsorships

Expenditures are published quarterly in GitHub Discussions under "Transparency Reports."

### Sustainability Goals

| Milestone | Target | Current Status |
|-----------|--------|----------------|
| Cover CI/CD costs | $50/mo | Seeking sponsors |
| Annual security audit | $5,000 | Seeking sponsors |
| Part-time Community Lead | $500/mo | Future goal |
| Full-time BDFL | $3,000/mo | Long-term goal |
| **Maintainer works on VayuPress full time** | **$5,000/mo** | **Long-term goal** |

### What the $5,000 figure is for

One thing only: **VayuPress is currently maintained part-time, and $5,000 per month is what would
let the maintainer work on it full-time.** That is the whole reason the number exists. It is not
a valuation, not a revenue target, and not a price for anything.

**If it is never reached, nothing changes.** VayuPress stays free, stays Apache-2.0, and stays
maintained part-time. It does not get sold, relicensed, or made source-available. The project's
values were settled before the funding question was asked, and the funding question does not get
to revise them.

That ordering matters more than the number. A project that would change its terms under financial
pressure has not actually decided anything — it has only decided what it will do while things are
going well.

### What funding does not buy

**Nothing about the licence.** VayuPress is Apache-2.0 permanently and unconditionally — that is
settled in [docs/LICENSING.md](LICENSING.md) and is not tied to any figure on this page. If this
project never receives another donation, the licence answer is identical.

**Nothing about access, features or support tiers.** There is no paid edition, no feature held
back for sponsors, and no plan for either. Every install gets the same binary.

**No influence over direction.** Sponsorship is not a vote. Technical decisions go through the
processes in [GOVERNANCE.md](../GOVERNANCE.md) regardless of who has funded what.

**Nobody has to pay anything to use VayuPress, now or ever.** These goals describe what would let
the maintainer keep working on it — not a price anyone owes.

---

## Environmental Sustainability

VayuPress is designed to be carbon-minimal:

### Architecture Choices

| Choice | Environmental Benefit |
|--------|----------------------|
| SQLite-first (single file) | No separate DB server process; lower CPU/memory |
| Single Go binary | No runtime overhead; cold starts are fast |
| No CDN required | Self-hosted fonts eliminate CDN round-trips |
| Zero telemetry | No analytics server; no data collection infrastructure |
| Efficient write queue | WAL adaptive checkpoint reduces disk I/O |
| No background polling | Event-driven; no wasted CPU cycles |

### Carbon Footprint Estimate

A typical VayuPress instance at idle uses:
- **CPU**: < 1% on a single core
- **Memory**: < 200 MB (idle, no content)
- **Disk I/O**: < 10 IOPS (WAL checkpoint + backup)
- **Network**: < 1 Kbps (health checks only)

This is approximately **100× more efficient** than a typical WordPress stack (PHP-FPM + MySQL + Redis + Nginx + CDN).

### Sustainability Commitments

1. We will never add a feature that requires always-on external services
2. We will never add analytics, telemetry, or tracking — even opt-in aggregate stats
3. We will prefer algorithmic efficiency over throwing compute at problems
4. We will document the carbon impact of significant new features in their ADR
5. Release binaries are built with `-trimpath` and `-s -w` to minimize size (less storage, faster transfer)

---

## Long-Term Viability

### Bus Factor Mitigation

The project's "bus factor" (how many maintainers can be lost before the project stalls) is addressed by:

- All decisions documented in ADRs (institutional memory is in the repo)
- Constitution v6.0 defines governance processes that work without any specific person
- BDFL succession plan required by Constitution (see `docs/MAINTAINERS.md`)
- Public infrastructure: GitHub for code, no proprietary tooling

### Fork Rights

VayuPress is Apache-2.0 licensed. Anyone may fork the project. If the project becomes unmaintained:

1. Forks are explicitly encouraged
2. The last maintainer should post a notice in README.md
3. The domain vayupress.com will redirect to the most active fork (if any) or show a maintenance notice

### Archival Policy

If the project is formally archived:
1. All data (issues, PRs, docs) remains public on GitHub
2. The final release binary and deploy script are preserved with checksums
3. A migration guide to alternatives is published

---

## Dependency Sustainability

We track the sustainability of our dependencies:

| Dependency | Upstream health | Action if deprecated |
|-----------|-----------------|---------------------|
| Go stdlib | Excellent (Google) | N/A |
| mattn/go-sqlite3 | Good (active) | Consider modernc.org/sqlite |
| Isso | Moderate (small project) | Replace with Go implementation |
| Let's Encrypt | Excellent (ISRG) | N/A |

Dependencies with declining upstream health are flagged in the Architecture Lead's annual review.
