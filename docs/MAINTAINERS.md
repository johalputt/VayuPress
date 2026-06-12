# VayuPress Maintainers

**Version**: 1.0.0 (Prompt 11 — Community Governance)  
**Governed by**: VayuPress Governance Constitution v6.0, Prompt 11

---

## Current Maintainers

| Name | GitHub | Role | Email | Since |
|------|--------|------|-------|-------|
| Ankush Choudhary Johal | @johalputt | BDFL + Architecture Lead | admin@vayupress.com | 2026-06-12 |

*Additional maintainers welcome — see "Becoming a Maintainer" below.*

---

## Maintainer Roles

The Constitution defines 7 maintainer roles. One person may hold multiple roles; no role may be vacant for more than 90 days.

### BDFL (Benevolent Dictator For Life)

**Responsibilities**:
- Final decision authority on architecture, community, and ethics disputes
- Ratifies ADRs and RFC outcomes
- Sets release schedule and version strategy
- Represents VayuPress publicly

**Constraints**:
- Cannot unilaterally override Security Lead on P1 security incidents
- Must publish annual transparency report
- Succession plan must exist and be documented

**Contact**: admin@vayupress.com

---

### Architecture Lead

**Responsibilities**:
- Owns `docs/ARCHITECTURE.md` and all ADRs
- Reviews and approves changes to `main.go` architecture
- Enforces SQLite-first doctrine (ADR-0001)
- Maintains the performance budget (Prompt 2)
- Reviews all RFCs for architectural soundness

**Contact**: architecture@vayupress.com

---

### Performance Lead

**Responsibilities**:
- Owns binary size < 45 MB, memory < 800 MB idle, JS < 50 KB gz budgets
- Runs benchmarks on every release candidate
- Proposes and reviews Prompt 2 compliance
- Maintains `docs/PERFORMANCE.md` (when created)

**Contact**: performance@vayupress.com

---

### Security Lead

**Responsibilities**:
- Owns `SECURITY.md`, `docs/THREAT-MODEL.md`
- Triages vulnerability reports within 72 hours (P1 critical)
- Reviews all auth, crypto, network code changes
- Manages coordinated disclosure process
- Signs off on security-related releases
- Runs `govulncheck` and `gosec` review per release

**Contact**: security@vayupress.com

---

### Community Lead

**Responsibilities**:
- Owns `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`
- Moderates GitHub Discussions and issues
- Manages RFC process (facilitates, does not decide)
- Onboards new contributors
- Burnout prevention: monitors maintainer activity, raises concerns
- Mental health support: connects contributors with resources when needed

**Contact**: community@vayupress.com

---

### Release Manager

**Responsibilities**:
- Owns `docs/RELEASES.md` and `CHANGELOG.md`
- Coordinates release timeline and pre-release checklist
- Tags releases and publishes GitHub releases
- Manages hotfix coordination
- Ensures SHA-256 checksums are published

**Contact**: releases@vayupress.com

---

### Ethics Lead

**Responsibilities**:
- Chairs the Ethical Review Board (Prompt 12)
- Owns `ETHICS.md` and `docs/ETHICAL-REVIEW-PROCESS.md`
- Reviews features for dark patterns, surveillance, data minimization
- Publishes annual ethics metrics
- Triages reports to ethics@vayupress.com

**Contact**: ethics@vayupress.com

---

## Ethical Review Board

The Ethical Review Board consists of 3 rotating maintainers (including the Ethics Lead). Rotation occurs annually or when a member steps down.

**Current ERB**: @johalputt (Ethics Lead, chair)  
**ERB contact**: ethics@vayupress.com  

ERB decisions require 2/3 majority. The BDFL may appeal an ERB decision, but the appeal itself is logged and published.

---

## Becoming a Maintainer

### Path to Maintainership

1. **Contributor** — Merged ≥ 1 PR; signed DCO; active in issues/discussions
2. **Regular Contributor** — ≥ 5 meaningful PRs over ≥ 3 months; demonstrates understanding of Constitution
3. **Maintainer candidate** — Nominated by existing maintainer; confirmed by BDFL after 2-week public comment period

### Requirements

- Agree to the Code of Conduct and Governance Constitution
- Sign the DCO on all commits (`git commit -s`)
- Respond to mentions within 7 days (or designate a backup)
- Participate in ≥ 1 release per quarter, or raise a concern
- Attend ≥ 1 community sync per quarter (async notes accepted)

### Nomination Process

Open an issue titled: `[NOMINATION] <Name> for <Role>` with:
- GitHub profile
- Contribution summary (PRs, reviews, discussions)
- Why this role specifically
- Endorsement from an existing maintainer

After 14 days of community comment, BDFL makes the decision.

---

## Stepping Down

Maintainers may step down at any time by:
1. Opening an issue: `[STEPPING DOWN] <Name> from <Role>`
2. Completing any in-progress release or critical task (or handing it off)
3. Updating this file and `.github/CODEOWNERS`

Stepping down is honorable. There is no minimum tenure requirement.

---

## Burnout Prevention

The Community Lead actively monitors for signs of maintainer burnout:

- Unresponsive for > 14 days without notice → check-in
- Declining review quality → private conversation
- Negative tone in issues → private conversation + possible break
- Request for break → honored immediately, no questions asked

**Mental health resources**: A curated list is available at community@vayupress.com. No maintainer should feel alone.

Every maintainer is entitled to take a 4-week break per year without explanation. During breaks, other maintainers cover responsibilities.

---

## Decision-Making

| Decision Type | Process | Who Decides |
|--------------|---------|------------|
| Bug fix | PR review | Any 1 maintainer |
| New feature | PR review + CI | Any 1 maintainer (+ RFC if architectural) |
| ADR acceptance | PR review + 7-day comment | Architecture Lead + BDFL |
| RFC | 14-day public comment | Relevant lead + BDFL ratification |
| Security incident | Security Lead escalates | Security Lead + BDFL |
| Maintainer nomination | 14-day comment | BDFL |
| Ethics dispute | ERB review | ERB 2/3 majority |
| Breaking change | RFC + major version | BDFL after RFC |
| Constitution amendment | RFC + 30-day comment | BDFL |
