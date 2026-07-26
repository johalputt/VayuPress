# EU Cyber Resilience Act — readiness assessment

**Status:** working assessment, not legal advice. Dates and obligations below should be checked
against the current text of Regulation (EU) 2024/2847 before anything is relied on, and a lawyer
consulted if VayuPress ever earns revenue in the EU beyond donations.

**Assessment date:** 2026-07

---

## Why this document exists

The Cyber Resilience Act is the first regulation that attaches security obligations to software
itself rather than to the organisation deploying it. It matters to VayuPress for two separate
reasons, and the second is the one that will actually be felt.

**First**, VayuPress may or may not be in scope. The analysis below concludes it is most likely
out of scope today, with one condition that could change that.

**Second — and independent of the first — VayuPress's users are in scope.** A company in the EU
that deploys VayuPress on its own infrastructure has obligations of its own, and will discharge
some of them by asking upstream: *give me an SBOM, tell me your support period, show me your
vulnerability-disclosure process, prove your releases are signed.* A project that can answer
those in one link wins deployments from projects that cannot. That is true whether or not the CRA
ever applies to this repository directly.

So the useful posture is not "are we obliged" but "can we answer". Most of the answer already
exists; this document says where the gaps are.

---

## Timeline

| Date | What applies |
|---|---|
| 10 December 2024 | Regulation entered into force |
| 11 June 2026 | Provisions on notified bodies apply |
| **11 September 2026** | **Reporting obligations for actively exploited vulnerabilities and severe incidents apply** |
| **11 December 2027** | **Full application — all remaining obligations** |

The September 2026 date is the near one and is the one to plan against.

---

## Scope assessment

The CRA applies to "products with digital elements" made available on the EU market **in the
course of a commercial activity**. Free and open-source software supplied outside a commercial
activity falls outside the manufacturer regime.

The regulation also creates a lighter-touch category — the **open-source software steward** — for
legal entities providing sustained support to open-source products intended for commercial use.
That category is defined in terms of legal persons; a natural person publishing a project is a
different situation again.

**Applied to VayuPress today:**

| Factor | Position |
|---|---|
| Published as open source under Apache-2.0 | Yes |
| Sold, licensed for a fee, or offered as a paid service | No |
| Paid support contracts | No |
| Monetised through the software (ads, data, upsell) | No — telemetry is opt-in, off by default, and transmits nothing about the operator |
| Funded by | Donations |
| Published by | A natural person, not a company |

**Conclusion: most likely outside the manufacturer regime.** Donations given without an
intention of profit generally do not constitute commercial activity for this purpose.

**The condition that would change it.** Charging for VayuPress in any form — hosted service,
paid support, a paid tier, a commercial licence — very likely brings the offering inside scope as
a manufacturer, with CE marking, conformity assessment and technical documentation attached. This
is worth knowing *before* any decision to monetise, not after, because the compliance cost is a
real input to that decision and is easy to discover too late.

---

## Gap analysis

What the manufacturer regime asks for, and where VayuPress stands. Marked against what a
downstream commercial user will ask for, since that is the binding constraint either way.

| Requirement | Status | Where |
|---|---|---|
| Coordinated vulnerability disclosure policy | **Have** | [`SECURITY.md`](../SECURITY.md) — contact, SLAs by CVSS band, no-public-issue rule, advisory process |
| Software bill of materials | **Have** | `.github/workflows/sbom.yml` — CycloneDX via `cyclonedx-gomod`, on every push and weekly |
| Vulnerability scanning in CI | **Have** | `govulncheck` on every push, weekly schedule, and as a release gate |
| Signed releases / integrity verification | **Have** | cosign signing in `tag-release.yml` and `release.yml` |
| Per-file licence identification | **Have** | SPDX headers on every Go file, gated in CI |
| Security-by-default configuration | **Have** | Deny-by-default posture recorded in [`GOVERNANCE-CONSTITUTION.md`](../GOVERNANCE-CONSTITUTION.md); no telemetry in core |
| Documented change history | **Have** | [`CHANGELOG.md`](../CHANGELOG.md), maintained per release |
| **Defined support period** | **Gap** | `SECURITY.md` names versions `1.0.x` / `LTS 1.x` / `< 0.9.0`. The project is at 3.14.x. The table is stale and states no end date for any version |
| **SBOM published with releases** | **Gap** | The SBOM is generated as a CI artefact and expires with the run. It is not attached to GitHub Releases, so a downstream user cannot fetch the SBOM for the version they deployed |
| **Vulnerability reporting route to ENISA/CSIRT** | **Gap, conditional** | Only applies if in scope. No process exists for the 24-hour early-warning / 72-hour notification path that would apply from September 2026 |
| **Machine-readable security contact** | **Gap** | No `security.txt` (RFC 9116). Trivial to add; automated scanners look for it |

---

## Recommended actions

Ordered by value per unit of effort. None of these require a legal opinion; all of them are
things a downstream user will ask for.

1. **Fix the supported-versions table in `SECURITY.md`.** It currently describes a version series
   the project left years ago, which reads as neglect to anyone evaluating the project and is
   worse than saying nothing. Replace it with the actual policy: which minor series receives
   security fixes, and for how long after a successor ships. The CRA's default expectation is a
   five-year support period or the product's lifetime if shorter — a self-hosted product with an
   operator who cannot be forced to upgrade should be thinking in those terms regardless of the
   regulation.

2. **Attach the SBOM to each GitHub Release.** The generator already exists and runs; only the
   upload target is missing. An SBOM that expires with a CI run cannot answer "what was in the
   version I deployed", which is the only question an SBOM is for.

3. **Add `/.well-known/security.txt`** (RFC 9116) pointing at `security@vayupress.com` with an
   expiry field. Small, standard, and machine-discoverable.

4. **Record the scope position.** This document is that record. Revisit it if the funding model
   ever changes — that is the single trigger that moves VayuPress from "out of scope" to "in
   scope as a manufacturer".

5. **Do not pursue CE marking or conformity assessment.** Not applicable to a non-commercial
   open-source project, and starting it would imply a manufacturer status that does not apply.

---

## What this does not cover

- **VayuMail-Mobile** is a separate distributed product and would need its own assessment. A
  mobile application distributed through an app store has a different scope analysis to a
  self-hosted server binary.
- **Operators of VayuPress installations** carry their own obligations under NIS2, the GDPR and
  potentially the CRA as deployers. Nothing here transfers to them or relieves them.
- **The legal question of what counts as commercial activity** is genuinely unsettled at the
  margins, particularly for donation-funded projects with a single maintainer. The position taken
  above is the reasonable reading, not a settled one.

---

## See also

- [`SECURITY.md`](../SECURITY.md) — vulnerability disclosure process
- [`docs/LICENSING.md`](LICENSING.md) — licence posture and the DCO
- [`.github/workflows/sbom.yml`](../.github/workflows/sbom.yml) — SBOM and vulnerability scanning
- [`GOVERNANCE-CONSTITUTION.md`](../GOVERNANCE-CONSTITUTION.md) — the binding project rules
