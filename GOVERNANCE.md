# VayuPress Governance

This document is the entry-point summary of VayuPress governance.
The authoritative source is [GOVERNANCE-CONSTITUTION.md](GOVERNANCE-CONSTITUTION.md).

> **Today (2026-10-25)** one maintainer, Ankush Choudhary Johal (@johalputt),
> holds every role below; the review board and the votes they describe begin
> when there are maintainers to fill them ([docs/MAINTAINERS.md](docs/MAINTAINERS.md)).

## Governance Architecture

| Layer                  | Document(s)                                                         | Mutability              |
|------------------------|---------------------------------------------------------------------|-------------------------|
| Constitution           | `GOVERNANCE-CONSTITUTION.md`                                        | Highest (amendment-protected) |
| Institutional Policies | `docs/SUSTAINABILITY.md`, `docs/LICENSING.md`, `ETHICS.md`         | High (RFC + 75%)        |
| Governance Policies    | `CONTRIBUTING.md`, `SECURITY.md`, `docs/RELEASES.md`               | Medium (RFC + majority) |
| Engineering Standards  | `docs/ARCHITECTURE.md`, `docs/CI-GOVERNANCE.md`, `docs/QUALITY-GATES.md` | Lower (RFC + baselines) |
| Operational Runbooks   | `docs/OPERATIONS.md`, `docs/operations/disaster-recovery.md`       | Live documents          |
| ADRs                   | `/docs/adr/`                                                        | Immutable once accepted |

## Priority Order

Security = Data Integrity > Ethical Compliance > Reliability > Simplicity > Performance > DX > Feature Velocity

## Amendment Rules

| Change Type                 | Requirement                                                              |
|-----------------------------|--------------------------------------------------------------------------|
| Immutable Core Principles   | RFC, 14-day review, 75% maintainer approval, BDFL approval              |
| Standard Governance Change  | RFC, 7-day review, simple majority                                       |
| Emergency Override          | BDFL or Security Lead; expires 30 days unless ratified                  |

## Maintainer Roles

| Role              | Responsibilities                           | Term             |
|-------------------|--------------------------------------------|------------------|
| BDFL              | Final decision-maker                       | Permanent        |
| Architecture Lead | Enforce Prompts 1–4                        | 1 year           |
| Performance Lead  | Enforce Prompts 2, 10                      | 1 year           |
| Security Lead     | Enforce Prompt 9                           | 1 year           |
| Community Lead    | RFCs, onboarding, community health         | 1 year           |
| Release Manager   | Prompt 8, releases, LTS                    | 6 months rotating|
| Ethics Lead       | Enforce ethical guidelines                 | 1 year           |

## RFC Process

Required for: new dependencies, core architecture changes, major new features, governance modifications, ethical concerns.

Open an issue with the RFC proposal template, written from [docs/rfc-template.md](docs/rfc-template.md). Minimum 7-day discussion. An accepted design is recorded as an ADR in [docs/adr/](docs/adr/INDEX.md).

## Contact

governance@vayupress.com — rfc@vayupress.com — community@vayupress.com
